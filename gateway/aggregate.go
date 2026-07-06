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
	if shapedBody, ok, err := aggregationStepRequestBody(r, route, body, step); err != nil {
		return aggregationStepResult{index: index, name: name, err: err}
	} else if ok {
		stepBody = shapedBody
	}
	if err := applyAggregationStepQuery(target, r, route, body, step); err != nil {
		return aggregationStepResult{index: index, name: name, err: err}
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
	if err := applyAggregationStepHeaders(req.Header, r, route, body, step); err != nil {
		return aggregationStepResult{index: index, name: name, err: err}
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

func aggregationStepRequestBody(r *http.Request, route Route, body []byte, step AggregationStep) ([]byte, bool, error) {
	if len(step.Request.BodyMappings) == 0 {
		return nil, false, nil
	}
	payload, err := aggregationRequestPayloadFromMappings(r, route, body, step.Request.BodyMappings)
	if err != nil {
		return nil, false, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func applyAggregationStepQuery(target *url.URL, r *http.Request, route Route, body []byte, step AggregationStep) error {
	if len(step.Request.QueryMappings) == 0 {
		return nil
	}
	payload, err := aggregationRequestPayloadFromMappings(r, route, body, step.Request.QueryMappings)
	if err != nil {
		return err
	}
	query := target.Query()
	for key, value := range payload {
		query.Set(key, aggregationScalarString(value))
	}
	target.RawQuery = query.Encode()
	return nil
}

func applyAggregationStepHeaders(header http.Header, r *http.Request, route Route, body []byte, step AggregationStep) error {
	if len(step.Request.HeaderMappings) == 0 {
		return nil
	}
	payload, err := aggregationRequestPayloadFromMappings(r, route, body, step.Request.HeaderMappings)
	if err != nil {
		return err
	}
	for key, value := range payload {
		header.Set(key, aggregationScalarString(value))
	}
	return nil
}

func aggregationRequestPayloadFromMappings(r *http.Request, route Route, body []byte, mappings []AggregationPayloadMapping) (map[string]any, error) {
	return aggregationPayloadFromMappings(aggregationRequestSource(r, route, body), mappings)
}

func aggregationRequestSource(r *http.Request, route Route, body []byte) transcodePayloadSource {
	bodyValue := map[string]any{}
	if len(bytes.TrimSpace(body)) > 0 {
		var decoded any
		if err := json.Unmarshal(body, &decoded); err == nil {
			bodyValue = map[string]any{"body": decoded}
			if object, ok := decoded.(map[string]any); ok {
				bodyValue = object
			}
		}
	}
	query := map[string]any{}
	for key, values := range r.URL.Query() {
		if len(values) == 1 {
			query[key] = values[0]
		} else if len(values) > 1 {
			items := make([]any, len(values))
			for i, value := range values {
				items[i] = value
			}
			query[key] = items
		}
	}
	headers := map[string]any{}
	for key, values := range r.Header {
		if len(values) == 1 {
			headers[key] = values[0]
			headers[strings.ToLower(key)] = values[0]
		} else if len(values) > 1 {
			items := make([]any, len(values))
			for i, value := range values {
				items[i] = value
			}
			headers[key] = items
			headers[strings.ToLower(key)] = items
		}
	}
	return transcodePayloadSource{
		Body:   bodyValue,
		Path:   aggregationPathAny(transcodePathValues(r.URL.Path, route.PathPrefix, "", nil)),
		Query:  query,
		Header: headers,
	}
}

func aggregationPathAny(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func aggregationScalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
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
	for _, step := range conf.Steps {
		if err := validateAggregationRequestShape(step.Request); err != nil {
			return err
		}
	}
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

func validateAggregationRequestShape(shape AggregationRequestShape) error {
	for _, mapping := range shape.BodyMappings {
		if err := validateTranscodeMappingPath("aggregation request body source", mapping.Source, true, true); err != nil {
			return err
		}
		if err := validateTranscodeMappingPath("aggregation request body target", mapping.Target, false, false); err != nil {
			return err
		}
	}
	for _, mapping := range shape.QueryMappings {
		if err := validateTranscodeMappingPath("aggregation request query source", mapping.Source, true, true); err != nil {
			return err
		}
		if strings.TrimSpace(mapping.Target) == "" {
			return errors.New("aggregation request query target is required")
		}
	}
	for _, mapping := range shape.HeaderMappings {
		if err := validateTranscodeMappingPath("aggregation request header source", mapping.Source, true, true); err != nil {
			return err
		}
		if strings.TrimSpace(mapping.Target) == "" {
			return errors.New("aggregation request header target is required")
		}
	}
	return nil
}

func compareAggregationSteps(current, candidate []AggregationStep) []TranscodeProfileChange {
	currentByName := aggregationStepsByName(current)
	candidateByName := aggregationStepsByName(candidate)
	var changes []TranscodeProfileChange
	for name, currentStep := range currentByName {
		candidateStep, ok := candidateByName[name]
		if !ok {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "remove_step",
				Scope:    "aggregation_step",
				Source:   name,
				Severity: "breaking",
				Message:  "candidate removes an existing aggregation step",
			})
			continue
		}
		if strings.TrimSpace(currentStep.Path) != strings.TrimSpace(candidateStep.Path) {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "change_step_path",
				Scope:    "aggregation_step",
				Source:   name,
				Target:   strings.TrimSpace(candidateStep.Path),
				Severity: "breaking",
				Message:  "candidate changes an existing aggregation step path",
			})
		}
		if len(currentStep.Fallback) > 0 && len(candidateStep.Fallback) == 0 {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "remove_fallback",
				Scope:    "aggregation_step",
				Source:   name,
				Severity: "breaking",
				Message:  "candidate removes an existing aggregation fallback",
			})
		} else if !bytes.Equal(bytes.TrimSpace(currentStep.Fallback), bytes.TrimSpace(candidateStep.Fallback)) {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "change_fallback",
				Scope:    "aggregation_step",
				Source:   name,
				Severity: "breaking",
				Message:  "candidate changes an existing aggregation fallback",
			})
		}
	}
	for name := range candidateByName {
		if _, ok := currentByName[name]; !ok {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "add_step",
				Scope:    "aggregation_step",
				Source:   name,
				Severity: "info",
				Message:  "candidate adds a new aggregation step",
			})
		}
	}
	return changes
}

func compareAggregationShape(current, candidate AggregationShape) []TranscodeProfileChange {
	var changes []TranscodeProfileChange
	if strings.TrimSpace(current.Mode) != strings.TrimSpace(candidate.Mode) {
		changes = append(changes, TranscodeProfileChange{
			Kind:     "change_shape_mode",
			Scope:    "aggregation_shape",
			Source:   strings.TrimSpace(current.Mode),
			Target:   strings.TrimSpace(candidate.Mode),
			Severity: "breaking",
			Message:  "candidate changes aggregation response shape mode",
		})
	}
	return append(changes, compareAggregationShapeMappings(current.Mappings, candidate.Mappings)...)
}

func compareAggregationShapeMappings(current, candidate []AggregationPayloadMapping) []TranscodeProfileChange {
	currentBySource := aggregationMappingsBySource(current)
	candidateBySource := aggregationMappingsBySource(candidate)
	var changes []TranscodeProfileChange
	for source, currentMapping := range currentBySource {
		candidateMapping, ok := candidateBySource[source]
		if !ok {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "remove_mapping",
				Scope:    "aggregation_shape",
				Source:   source,
				Target:   currentMapping.Target,
				Severity: "breaking",
				Message:  "candidate removes an existing aggregation shape mapping",
			})
			continue
		}
		if strings.TrimSpace(currentMapping.Target) != strings.TrimSpace(candidateMapping.Target) {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "change_target",
				Scope:    "aggregation_shape",
				Source:   source,
				Target:   strings.TrimSpace(candidateMapping.Target),
				Severity: "breaking",
				Message:  "candidate changes an existing aggregation shape mapping target",
			})
		}
	}
	for source, mapping := range candidateBySource {
		if _, ok := currentBySource[source]; !ok {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "add_mapping",
				Scope:    "aggregation_shape",
				Source:   source,
				Target:   strings.TrimSpace(mapping.Target),
				Severity: "info",
				Message:  "candidate adds a new aggregation shape mapping",
			})
		}
	}
	return changes
}

func aggregationStepsByName(steps []AggregationStep) map[string]AggregationStep {
	out := make(map[string]AggregationStep, len(steps))
	for index, step := range steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			name = fmt.Sprintf("step%d", index+1)
		}
		out[name] = step
	}
	return out
}

func aggregationMappingsBySource(mappings []AggregationPayloadMapping) map[string]AggregationPayloadMapping {
	out := make(map[string]AggregationPayloadMapping, len(mappings))
	for _, mapping := range mappings {
		source := strings.TrimSpace(mapping.Source)
		if source == "" && len(mapping.Default) > 0 {
			source = "default:" + strings.TrimSpace(mapping.Target)
		}
		out[source] = mapping
	}
	return out
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
		step.Request = cloneAggregationRequestShape(step.Request)
		step.Body = append(json.RawMessage(nil), step.Body...)
		step.Fallback = append(json.RawMessage(nil), step.Fallback...)
		out[i] = step
	}
	return out
}

func cloneAggregationRequestShape(shape AggregationRequestShape) AggregationRequestShape {
	shape.QueryMappings = cloneAggregationPayloadMappings(shape.QueryMappings)
	shape.HeaderMappings = cloneAggregationPayloadMappings(shape.HeaderMappings)
	shape.BodyMappings = cloneAggregationPayloadMappings(shape.BodyMappings)
	return shape
}

func cloneAggregationPayloadMappings(mappings []AggregationPayloadMapping) []AggregationPayloadMapping {
	if len(mappings) == 0 {
		return nil
	}
	out := make([]AggregationPayloadMapping, len(mappings))
	for i, mapping := range mappings {
		mapping.Source = strings.TrimSpace(mapping.Source)
		mapping.Target = strings.TrimSpace(mapping.Target)
		mapping.Default = append(json.RawMessage(nil), mapping.Default...)
		out[i] = mapping
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
