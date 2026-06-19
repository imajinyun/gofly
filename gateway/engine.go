// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/observability"
	"github.com/gofly/gofly/core/trace"
)

// ServeHTTP matches the request against configured routes, applies governance
// policies, and proxies to the resolved upstream endpoint.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g == nil {
		http.Error(w, "gateway is nil", http.StatusServiceUnavailable)
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if g.logger != nil {
				g.logger.ErrorContext(r.Context(), "gateway panic recovered", "error", recovered, "stack", string(debug.Stack()))
			}
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	}()
	match, ok := g.match(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, span := observability.StartTrace(r.Context(), r.Header.Get(trace.TraceParentHeader), "gateway", trace.AlwaysSampler())
	r = r.WithContext(ctx)
	start := time.Now()
	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	g.serveRoute(recorder, r, match)
	duration := time.Since(start)
	if g.stats != nil {
		g.stats.Observe(routeKey(match.route), recorder.status, duration)
	}
	if g.registry != nil {
		observability.Record(g.registry, "gateway:"+routeKey(match.route), recorder.status, duration)
	}
	if g.logger != nil {
		attrs := []any{"route", routeKey(match.route), "method", r.Method, "path", r.URL.Path, "status", recorder.status, "duration", duration, "trace_id", span.TraceID, "span_id", span.SpanID}
		if recorder.status >= http.StatusInternalServerError {
			g.logger.WarnContext(r.Context(), "gateway request", attrs...)
		} else {
			g.logger.InfoContext(r.Context(), "gateway request", attrs...)
		}
	}
}

func (g *Gateway) serveRoute(w http.ResponseWriter, r *http.Request, match routeMatch) {
	route := g.governedRoute(r, match.route)
	if limiter := g.ruleRateLimiter(route); limiter != nil && !limiter.Allow() {
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}
	if limiter := g.ruleConcurrencyLimiter(route); limiter != nil {
		if !limiter.TryAcquire() {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		defer limiter.Release()
	}
	maxBodyBytes := route.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = g.maxBodyBytes
	}
	if maxBodyBytes > 0 && r.Body != nil {
		if r.ContentLength > maxBodyBytes {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	}
	ctx := r.Context()
	timeout := route.Timeout
	if timeout <= 0 {
		timeout = g.timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
		r = r.WithContext(ctx)
	}
	if !route.hostAllowed(r.Host) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	body, err := reusableBody(r)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	effectiveRoute := g.canaryRoute(r, route)
	g.shadow(ctx, r, effectiveRoute, body)
	result, err := g.proxyWithRetry(r, effectiveRoute, body)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, breaker.ErrOpen) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status = http.StatusServiceUnavailable
		} else if strings.Contains(err.Error(), "resolve gateway upstream") || strings.Contains(err.Error(), "pick gateway upstream") {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	if result.Retries > 0 && g.stats != nil {
		g.stats.IncRetry(routeKey(match.route), result.Retries)
	}
	copyResponseHeaders(w.Header(), result.Header, effectiveRoute.Header)
	w.WriteHeader(result.Status)
	if _, err := w.Write(result.Body); err != nil {
		if g.logger != nil {
			g.logger.ErrorContext(r.Context(), "gateway write proxy response failed", "error", err)
		}
	}
}

func (g *Gateway) proxyWithRetry(r *http.Request, route Route, body []byte) (proxyResult, error) {
	policy := normalizeRetryPolicy(route.Retry)
	if !policy.matchesMethod(r.Method) {
		policy.Attempts = 1
	}
	if policy.MaxBodyBytes > 0 && int64(len(body)) > policy.MaxBodyBytes {
		policy.Attempts = 1
	}
	var last proxyResult
	for attempt := 0; attempt < policy.Attempts; attempt++ {
		if attempt > 0 {
			if !g.allowRetry(route, last.Endpoint, policy) {
				return last, nil
			}
			if policy.Backoff > 0 {
				backoff := policy.Backoff
				if policy.RespectRetryAfter {
					backoff = retryAfterDelay(last.Header.Get("Retry-After"), backoff)
				}
				timer := time.NewTimer(backoff)
				select {
				case <-r.Context().Done():
					timer.Stop()
					return last, r.Context().Err()
				case <-timer.C:
				}
			}
		}
		result, err := g.proxyOnce(r, route, body)
		last = result
		last.Retries = attempt
		if err != nil {
			if attempt+1 < policy.Attempts {
				continue
			}
			return last, err
		}
		if !policy.shouldRetryStatus(result.Status) || attempt+1 >= policy.Attempts {
			return last, nil
		}
	}
	return last, last.Err
}

func retryAfterDelay(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	if seconds, err := time.ParseDuration(raw + "s"); err == nil && seconds > 0 {
		return seconds
	}
	if when, err := http.ParseTime(raw); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return fallback
}

func reusableBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close() // body fully read; close error is benign
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	r.ContentLength = int64(len(body))
	return body, nil
}
