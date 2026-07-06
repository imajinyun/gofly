// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/imajinyun/gofly/core/breaker"
)

const maxAggregationBodyBytes int64 = 4 << 20

type aggregationStepResult struct {
	index  int
	name   string
	status int
	body   json.RawMessage
	err    error
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
	fallbacks := 0
	requiredFailed := false
	for result := range results {
		step := route.Aggregation.Steps[result.index]
		if result.err != nil || result.status >= http.StatusBadRequest {
			message := ""
			if result.err != nil {
				message = result.err.Error()
			} else {
				message = fmt.Sprintf("upstream status %d", result.status)
			}
			failures[result.name] = message
			if len(step.Fallback) > 0 {
				data[result.name] = normalizeAggregationBody(step.Fallback)
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
	g.recordAggregationRuntime(route, len(failures), fallbacks, status)
	bodyBytes, err := json.Marshal(struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors map[string]string          `json:"errors,omitempty"`
	}{Data: data, Errors: failures})
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

func (g *Gateway) runAggregationStep(r *http.Request, route Route, endpoint string, body []byte, index int, step AggregationStep) aggregationStepResult {
	name := strings.TrimSpace(step.Name)
	if name == "" {
		name = fmt.Sprintf("step%d", index+1)
	}
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
	req, err := http.NewRequestWithContext(r.Context(), method, target.String(), bytes.NewReader(stepBody))
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
		return conf
	}
	conf.Steps = cloneAggregationSteps(conf.Steps)
	for i := range conf.Steps {
		conf.Steps[i].Name = strings.TrimSpace(conf.Steps[i].Name)
		conf.Steps[i].Method = strings.ToUpper(strings.TrimSpace(conf.Steps[i].Method))
		conf.Steps[i].Path = strings.TrimSpace(conf.Steps[i].Path)
		conf.Steps[i].Target = strings.TrimRight(strings.TrimSpace(conf.Steps[i].Target), "/")
	}
	return conf
}

func (g *Gateway) recordAggregationRuntime(route Route, failures, fallbacks, status int) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.aggregationRuntime == nil {
		g.aggregationRuntime = make(map[string]AggregationRuntimeSnapshot)
	}
	item := g.aggregationRuntime[routeKey(route)]
	item.LastFailures = failures
	item.LastFallbacks = fallbacks
	item.LastStatus = status
	g.aggregationRuntime[routeKey(route)] = item
}

func cloneAggregationConfig(conf AggregationConfig) AggregationConfig {
	conf.Steps = cloneAggregationSteps(conf.Steps)
	return conf
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
