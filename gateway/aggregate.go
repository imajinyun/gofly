// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
)

const maxAggregationBodyBytes int64 = 4 << 20

type aggregationStepResult struct {
	index   int
	name    string
	status  int
	body    json.RawMessage
	retries int
	err     error
}

func (g *Gateway) aggregateOnce(r *http.Request, route Route, endpoint string, body []byte, brk *breaker.AdaptiveBreaker) (proxyResult, error) {
	if len(route.Aggregation.Steps) == 0 {
		err := errors.New("gateway aggregation requires at least one step")
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	results := make(chan aggregationStepResult, len(route.Aggregation.Steps))
	var wg sync.WaitGroup
	for i, step := range route.Aggregation.Steps {
		wg.Add(1)
		go func(index int, step AggregationStep) {
			defer wg.Done()
			results <- g.runAggregationStep(r, route, endpoint, body, index, step)
		}(i, step)
	}
	wg.Wait()
	close(results)

	data := make(map[string]json.RawMessage, len(route.Aggregation.Steps))
	failures := make(map[string]string)
	failedSteps := make([]string, 0)
	fallbackSteps := make([]string, 0)
	fallbacks := 0
	retries := 0
	requiredFailed := false
	for result := range results {
		step := route.Aggregation.Steps[result.index]
		retries += result.retries
		if result.err != nil || result.status >= http.StatusBadRequest {
			message := ""
			if result.err != nil {
				message = result.err.Error()
			} else {
				message = fmt.Sprintf("upstream status %d", result.status)
			}
			failures[result.name] = message
			failedSteps = append(failedSteps, result.name)
			if len(step.Fallback) > 0 {
				data[result.name] = normalizeAggregationBody(step.Fallback)
				fallbackSteps = append(fallbackSteps, result.name)
				fallbacks++
				continue
			}
			if step.Required {
				requiredFailed = true
			}
			continue
		}
		data[result.name] = result.body
	}
	status := http.StatusOK
	if requiredFailed {
		status = http.StatusBadGateway
	}
	g.recordAggregationRuntime(route, aggregationRuntimeUpdate{
		failures:      len(failures),
		fallbacks:     fallbacks,
		status:        status,
		retries:       retries,
		failedSteps:   failedSteps,
		fallbackSteps: fallbackSteps,
	})
	bodyBytes, err := renderAggregationResponse(route.Aggregation, data, failures, fallbacks > 0 || len(failures) > 0)
	if err != nil {
		if brk != nil {
			brk.MarkFailure()
		}
		return proxyResult{Endpoint: endpoint, Err: err}, err
	}
	success := !requiredFailed
	g.reportEndpoint(route, endpoint, success)
	if brk != nil {
		if success {
			brk.MarkSuccess()
		} else {
			brk.MarkFailure()
		}
	}
	return proxyResult{
		Endpoint: endpoint,
		Status:   status,
		Header:   http.Header{"Content-Type": []string{"application/json"}},
		Body:     bodyBytes,
	}, nil
}

func renderAggregationResponse(conf AggregationConfig, data map[string]json.RawMessage, failures map[string]string, degraded bool) ([]byte, error) {
	if len(conf.Shape.Mappings) == 0 && !strings.EqualFold(conf.Shape.Mode, "flat") {
		return json.Marshal(struct {
			Data   map[string]json.RawMessage `json:"data"`
			Errors map[string]string          `json:"errors,omitempty"`
		}{Data: data, Errors: failures})
	}
	source := aggregationShapeSource(data, failures, degraded)
	if len(conf.Shape.Mappings) > 0 {
		payload, err := aggregationPayloadFromMappings(source, conf.Shape.Mappings)
		if err != nil {
			return nil, err
		}
		return json.Marshal(payload)
	}
	payload := make(map[string]any, len(data)+2)
	for name, raw := range data {
		payload[name] = cloneAggregationRawValue(raw)
	}
	payload["degraded"] = degraded
	if len(failures) > 0 {
		payload["errors"] = cloneAggregationErrors(failures)
	}
	return json.Marshal(payload)
}

func aggregationShapeSource(data map[string]json.RawMessage, failures map[string]string, degraded bool) transcodePayloadSource {
	body := map[string]any{
		"degraded": degraded,
		"errors":   aggregationErrorsAny(failures),
		"data":     map[string]any{},
	}
	dataObject := body["data"].(map[string]any)
	for name, raw := range data {
		dataObject[name] = cloneAggregationRawValue(raw)
	}
	return transcodePayloadSource{Body: body}
}

func aggregationPayloadFromMappings(source transcodePayloadSource, mappings []AggregationPayloadMapping) (map[string]any, error) {
	payload := map[string]any{}
	for _, mapping := range mappings {
		target := strings.TrimSpace(mapping.Target)
		if target == "" {
			continue
		}
		value, ok, err := transcodeMappingSourceValue(source, mapping.Source)
		if err != nil {
			return nil, err
		}
		if !ok {
			if len(mapping.Default) == 0 {
				continue
			}
			value = cloneAggregationRawValue(mapping.Default)
		}
		if err := setTranscodeMappingTarget(payload, target, value); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func (g *Gateway) runAggregationStep(r *http.Request, route Route, endpoint string, body []byte, index int, step AggregationStep) aggregationStepResult {
	name := strings.TrimSpace(step.Name)
	if name == "" {
		name = fmt.Sprintf("step%d", index+1)
	}
	attempts := normalizeAggregationStepAttempts(step.Retry.Attempts)
	var last aggregationStepResult
	for attempt := 0; attempt < attempts; attempt++ {
		result := g.runAggregationStepAttempt(r, route, endpoint, body, index, step, name)
		result.retries = attempt
		if !aggregationStepShouldRetry(result, step.Retry) || attempt == attempts-1 {
			return result
		}
		if step.Retry.Backoff > 0 {
			timer := time.NewTimer(step.Retry.Backoff)
			select {
			case <-r.Context().Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				result.err = r.Context().Err()
				return result
			case <-timer.C:
			}
		}
		last = result
	}
	if last.name == "" {
		last = aggregationStepResult{index: index, name: name, err: errors.New("aggregation step retry exhausted")}
	}
	return last
}

func (g *Gateway) runAggregationStepAttempt(r *http.Request, route Route, endpoint string, body []byte, index int, step AggregationStep, name string) aggregationStepResult {
	target, err := aggregationStepURL(endpoint, step)
	if err != nil {
		return aggregationStepResult{index: index, name: name, err: err}
	}
	method := strings.ToUpper(strings.TrimSpace(step.Method))
	if method == "" {
		method = http.MethodGet
	}
	stepBody := body
	if len(step.Body) > 0 {
		stepBody = append([]byte(nil), step.Body...)
	}
	ctx := r.Context()
	var cancel context.CancelFunc
	if step.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(stepBody))
	if err != nil {
		return aggregationStepResult{index: index, name: name, err: err}
	}
	req.Header = cloneHeader(r.Header)
	applyHeaderPolicy(req.Header, route.Header)
	for key, value := range step.Headers {
		req.Header.Set(key, value)
	}
	setForwardHeaders(req, r, route)
	req.ContentLength = int64(len(stepBody))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(stepBody)), nil }
	resp, err := g.client.Do(req)
	if err != nil {
		return aggregationStepResult{index: index, name: name, err: err}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxAggregationBodyBytes+1)
	respBody, err := io.ReadAll(limited)
	if err != nil {
		return aggregationStepResult{index: index, name: name, status: resp.StatusCode, err: err}
	}
	if int64(len(respBody)) > maxAggregationBodyBytes {
		return aggregationStepResult{index: index, name: name, status: resp.StatusCode, err: errors.New("aggregation step response too large")}
	}
	return aggregationStepResult{index: index, name: name, status: resp.StatusCode, body: normalizeAggregationBody(respBody)}
}

func normalizeAggregationStepAttempts(attempts int) int {
	if attempts <= 0 {
		return 1
	}
	return attempts
}

func aggregationStepShouldRetry(result aggregationStepResult, policy RetryPolicy) bool {
	if policy.Attempts <= 1 {
		return false
	}
	if result.err != nil {
		return true
	}
	if len(policy.Statuses) == 0 {
		return result.status >= http.StatusInternalServerError
	}
	for _, status := range policy.Statuses {
		if result.status == status {
			return true
		}
	}
	return false
}

func aggregationStepURL(endpoint string, step AggregationStep) (*url.URL, error) {
	target := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if override := strings.TrimRight(strings.TrimSpace(step.Target), "/"); override != "" {
		target = override
	}
	if target == "" {
		return nil, errors.New("aggregation step target is required")
	}
	base, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse aggregation target: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, errors.New("aggregation target must include scheme and host")
	}
	path := strings.TrimSpace(step.Path)
	if path == "" {
		return nil, errors.New("aggregation step path is required")
	}
	base.Path = joinURLPath(base.Path, path)
	base.RawPath = ""
	return base, nil
}

func normalizeAggregationConfig(conf AggregationConfig) AggregationConfig {
	if len(conf.Steps) == 0 {
		conf.Steps = nil
		conf.Shape = cloneAggregationShape(conf.Shape)
		return conf
	}
	conf.Steps = cloneAggregationSteps(conf.Steps)
	conf.Shape = cloneAggregationShape(conf.Shape)
	for i := range conf.Steps {
		conf.Steps[i].Name = strings.TrimSpace(conf.Steps[i].Name)
		conf.Steps[i].Method = strings.ToUpper(strings.TrimSpace(conf.Steps[i].Method))
		conf.Steps[i].Path = strings.TrimSpace(conf.Steps[i].Path)
		conf.Steps[i].Target = strings.TrimRight(strings.TrimSpace(conf.Steps[i].Target), "/")
		conf.Steps[i].Retry = normalizeRetryPolicy(conf.Steps[i].Retry)
	}
	return conf
}

func validateAggregationConfig(conf AggregationConfig) error {
	for _, mapping := range conf.Shape.Mappings {
		if err := validateTranscodeMappingPath("aggregation source", mapping.Source, true, true); err != nil {
			return err
		}
		if err := validateTranscodeMappingPath("aggregation target", mapping.Target, false, false); err != nil {
			return err
		}
	}
	return nil
}

type aggregationRuntimeUpdate struct {
	failures      int
	fallbacks     int
	status        int
	retries       int
	failedSteps   []string
	fallbackSteps []string
}

func (g *Gateway) recordAggregationRuntime(route Route, update aggregationRuntimeUpdate) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.aggregationRuntime == nil {
		g.aggregationRuntime = make(map[string]AggregationRuntimeSnapshot)
	}
	item := g.aggregationRuntime[routeKey(route)]
	item.LastFailures = update.failures
	item.LastFallbacks = update.fallbacks
	item.LastStatus = update.status
	item.LastRetries = update.retries
	item.Degraded = update.failures > 0
	item.FailedSteps = append([]string(nil), update.failedSteps...)
	item.FallbackStepsUsed = append([]string(nil), update.fallbackSteps...)
	g.aggregationRuntime[routeKey(route)] = item
}

func cloneAggregationConfig(conf AggregationConfig) AggregationConfig {
	conf.Steps = cloneAggregationSteps(conf.Steps)
	conf.Shape = cloneAggregationShape(conf.Shape)
	return conf
}

func cloneAggregationShape(shape AggregationShape) AggregationShape {
	shape.Mode = strings.TrimSpace(shape.Mode)
	if len(shape.Mappings) == 0 {
		shape.Mappings = nil
		return shape
	}
	out := make([]AggregationPayloadMapping, len(shape.Mappings))
	for i, mapping := range shape.Mappings {
		mapping.Source = strings.TrimSpace(mapping.Source)
		mapping.Target = strings.TrimSpace(mapping.Target)
		mapping.Default = append(json.RawMessage(nil), mapping.Default...)
		out[i] = mapping
	}
	shape.Mappings = out
	return shape
}

func cloneAggregationSteps(steps []AggregationStep) []AggregationStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]AggregationStep, len(steps))
	for i, step := range steps {
		step.Headers = cloneMap(step.Headers)
		step.Body = append(json.RawMessage(nil), step.Body...)
		step.Fallback = append(json.RawMessage(nil), step.Fallback...)
		out[i] = step
	}
	return out
}

func normalizeAggregationBody(body []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return json.RawMessage("null")
	}
	if json.Valid(trimmed) {
		return append(json.RawMessage(nil), trimmed...)
	}
	encoded, err := json.Marshal(string(trimmed))
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

func cloneAggregationRawValue(raw json.RawMessage) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return cloneTranscodeMappingDefault(value)
}

func cloneAggregationErrors(errorsByStep map[string]string) map[string]string {
	if len(errorsByStep) == 0 {
		return nil
	}
	out := make(map[string]string, len(errorsByStep))
	for key, value := range errorsByStep {
		out[key] = value
	}
	return out
}

func aggregationErrorsAny(errorsByStep map[string]string) map[string]any {
	if len(errorsByStep) == 0 {
		return nil
	}
	out := make(map[string]any, len(errorsByStep))
	for key, value := range errorsByStep {
		out[key] = value
	}
	return out
}
