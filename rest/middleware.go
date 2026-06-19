// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gofly/gofly/core/auth"
	"github.com/gofly/gofly/core/breaker"
	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/metrics"
	"github.com/gofly/gofly/core/observability"
	"github.com/gofly/gofly/core/trace"
)

// RequestIDHeader is the header used for request identifiers.
const RequestIDHeader = "X-Request-Id"

// TraceMiddleware returns middleware that starts an OpenTelemetry trace span.
func TraceMiddleware(service string) Middleware {
	return TraceMiddlewareWithSampler(service, nil)
}

func TraceMiddlewareWithSampler(service string, sampler trace.Sampler) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, sc := observability.StartTrace(r.Context(), r.Header.Get(trace.TraceParentHeader), service, sampler)
			w.Header().Set(trace.TraceParentHeader, trace.TraceParent(sc))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RouteInfoMiddleware(method, path string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), routeInfoKey{}, routeInfo{Method: method, Path: path})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RecoverMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					slog.Error("panic recovered", "panic", v, "stack", string(debug.Stack()))
					writeError(w, http.StatusInternalServerError, coreerrors.CodeInternal, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func LogMiddleware() Middleware {
	return LogMiddlewareWithSampler(nil)
}

func LogMiddlewareWithSampler(sampler trace.Sampler) Middleware {
	return LogMiddlewareWithConfig(sampler, LogRedactionConfig{})
}

func LogMiddlewareWithConfig(sampler trace.Sampler, redaction LogRedactionConfig) Middleware {
	redactor := newLogRedactor(redaction)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := newStatusResponseWriter(w)
			next.ServeHTTP(sw, r)
			attrs := []any{"method", r.Method, "path", redactor.path(r), "status", sw.status, "duration", time.Since(start)}
			if len(redactor.headers) > 0 {
				attrs = append(attrs, "headers", redactor.safeHeaders(r.Header))
			}
			attrs = append(attrs, observability.TraceAttrs(r.Context())...)
			if !observability.ShouldLog(r.Context(), sampler, sw.status) {
				return
			}
			slog.InfoContext(r.Context(), "http request", attrs...)
		})
	}
}

func SecurityHeadersMiddleware(config SecurityHeadersConfig) Middleware {
	resolved := resolveSecurityHeaders(config)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for key, value := range resolved {
				if value != "" {
					w.Header().Set(key, value)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func MetricsMiddleware(reg *metrics.Registry) Middleware {
	if reg == nil {
		reg = metrics.Default
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reg.IncInFlight()
			defer reg.DecInFlight()
			sw := newStatusResponseWriter(w)
			next.ServeHTTP(sw, r)
			observability.Record(reg, routePattern(r), sw.status, time.Since(start))
		})
	}
}

func MetricsHandler(reg *metrics.Registry) HandlerFunc {
	if reg == nil {
		reg = metrics.Default
	}
	return func(ctx *Context) {
		ctx.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
		ctx.Response.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(ctx.Response).Encode(reg.Snapshot())
	}
}

func PrometheusMetricsHandler(reg *metrics.Registry) HandlerFunc {
	if reg == nil {
		reg = metrics.Default
	}
	return func(ctx *Context) {
		ctx.Response.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		ctx.Response.WriteHeader(http.StatusOK)
		if err := reg.WritePrometheus(ctx.Response); err != nil {
			slog.WarnContext(ctx.Request.Context(), "write prometheus metrics", "error", err)
		}
	}
}

func RequestIDMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get(RequestIDHeader)
			if requestID == "" {
				requestID = metadata.NewRequestID()
			}
			w.Header().Set(RequestIDHeader, requestID)
			ctx := metadata.Append(r.Context(), metadata.RequestIDKey, requestID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func TimeoutMiddleware(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		if timeout <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isUpgradedOrSSE(r) {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			tw := newTimeoutResponseWriter(w)
			done := make(chan struct{})
			panicCh := make(chan any, 1)
			go func() {
				defer func() {
					if v := recover(); v != nil {
						panicCh <- v
						return
					}
					close(done)
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			select {
			case v := <-panicCh:
				panic(v)
			case <-done:
				tw.flushTo(w)
			case <-ctx.Done():
				tw.markTimedOut()
				writeError(w, http.StatusGatewayTimeout, coreerrors.CodeDeadlineExceeded, "request timeout")
			}
		})
	}
}

func MaxBodyBytesMiddleware(maxBodyBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		if maxBodyBytes <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBodyBytes {
				writeError(w, http.StatusRequestEntityTooLarge, coreerrors.CodeResourceExhausted, "request body too large")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
			next.ServeHTTP(w, r)
		})
	}
}

func TrimStringsMiddleware() Middleware {
	return TrimStringsMiddlewareWithConfig(TrimStringsConfig{})
}

func TrimStringsMiddlewareWithConfig(config TrimStringsConfig) Middleware {
	resolved := resolveTrimStringsConfig(config)
	return func(next http.Handler) http.Handler {
		if !resolved.query && !resolved.body {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if resolved.query {
				trimQuery(r)
			}
			if resolved.body {
				trimBody(r, resolved.json)
			}
			next.ServeHTTP(w, r)
		})
	}
}

type resolvedTrimStringsConfig struct {
	query bool
	body  bool
	json  bool
}

func resolveTrimStringsConfig(config TrimStringsConfig) resolvedTrimStringsConfig {
	return resolvedTrimStringsConfig{
		query: boolDefault(config.Query, true),
		body:  boolDefault(config.Body, true),
		json:  boolDefault(config.JSON, true),
	}
}

func boolDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func trimQuery(r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		for i, value := range values {
			values[i] = strings.TrimSpace(value)
		}
		q[key] = values
	}
	r.URL.RawQuery = q.Encode()
}

func trimBody(r *http.Request, trimJSON bool) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close() // body fully read; close error is benign
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return
	}
	data = bytes.TrimSpace(data)
	if trimJSON && isJSON(r.Header.Get("Content-Type")) && len(data) > 0 {
		data = trimJSONBody(data)
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	r.ContentLength = int64(len(data))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

func isJSON(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}

func trimJSONBody(data []byte) []byte {
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return data
	}
	trimmed, err := json.Marshal(trimJSONValue(payload))
	if err != nil {
		return data
	}
	return trimmed
}

func trimJSONValue(v any) any {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		for i, item := range value {
			value[i] = trimJSONValue(item)
		}
		return value
	case map[string]any:
		for key, item := range value {
			value[key] = trimJSONValue(item)
		}
		return value
	default:
		return value
	}
}

func RateLimitMiddleware(rate, burst int) Middleware {
	limiter := limit.New(rate, burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				writeError(w, http.StatusTooManyRequests, coreerrors.CodeResourceExhausted, "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func AdaptiveRateLimitMiddleware(limiter *limit.AdaptiveLimiter) Middleware {
	if limiter == nil {
		limiter = limit.NewAdaptiveLimiter()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := limiter.Allow()
			if err != nil {
				writeError(w, http.StatusTooManyRequests, coreerrors.CodeResourceExhausted, err.Error())
				return
			}
			sw := newStatusResponseWriter(w)
			next.ServeHTTP(sw, r)
			token.Done(sw.status < http.StatusInternalServerError)
		})
	}
}

func MaxConcurrencyMiddleware(max int) Middleware {
	limiter := limit.NewConcurrency(max)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.TryAcquire() {
				writeError(w, http.StatusServiceUnavailable, coreerrors.CodeUnavailable, "too many concurrent requests")
				return
			}
			defer limiter.Release()
			next.ServeHTTP(w, r)
		})
	}
}

func isUpgradedOrSSE(r *http.Request) bool {
	return r.Header.Get("Upgrade") != "" || r.Header.Get("Accept") == "text/event-stream"
}

type timeoutResponseWriter struct {
	mu        sync.Mutex
	header    http.Header
	body      bytes.Buffer
	status    int
	wrote     bool
	timedOut  bool
	committed bool
}

func newTimeoutResponseWriter(w http.ResponseWriter) *timeoutResponseWriter {
	header := make(http.Header)
	for key, values := range w.Header() {
		header[key] = append([]string(nil), values...)
	}
	return &timeoutResponseWriter{header: header, status: http.StatusOK}
}

func (w *timeoutResponseWriter) Header() http.Header {
	return w.header
}

func (w *timeoutResponseWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wrote || w.timedOut {
		return
	}
	w.status = status
	w.wrote = true
}

func (w *timeoutResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	if !w.wrote {
		w.status = http.StatusOK
		w.wrote = true
	}
	return w.body.Write(data)
}

func (w *timeoutResponseWriter) markTimedOut() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timedOut = true
}

func (w *timeoutResponseWriter) flushTo(dst http.ResponseWriter) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timedOut || w.committed {
		return
	}
	w.committed = true
	for key, values := range w.header {
		dst.Header()[key] = append([]string(nil), values...)
	}
	dst.WriteHeader(w.status)
	_, _ = io.Copy(dst, bytes.NewReader(w.body.Bytes()))
}

func BearerAuthMiddleware(validator auth.Validator) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if validator == nil {
				writeError(w, http.StatusInternalServerError, coreerrors.CodeInternal, "auth validator is required")
				return
			}
			token, ok := auth.ExtractBearer(r.Header.Get(auth.AuthorizationHeader))
			if !ok {
				writeError(w, http.StatusUnauthorized, coreerrors.CodeUnauthenticated, auth.ErrMissingCredentials.Error())
				return
			}
			ctx, err := validator(r.Context(), token)
			if err != nil {
				writeError(w, http.StatusUnauthorized, coreerrors.CodeUnauthenticated, auth.ErrInvalidCredentials.Error())
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func SignatureAuthMiddleware(opts auth.SignatureOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if r.Body != nil {
				_ = r.Body.Close() // body fully read; close error is benign
			}
			if err != nil {
				writeError(w, http.StatusBadRequest, coreerrors.CodeInvalidArgument, "read request body failed")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			r.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			}
			if err := auth.VerifyRequestSignature(r, body, opts); err != nil {
				writeError(w, http.StatusUnauthorized, coreerrors.CodeUnauthenticated, auth.ErrInvalidCredentials.Error())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuthorizationMiddleware enforces an RBAC requirement against the
// Principal already present in the request context. It must run after an
// authentication middleware (e.g. BearerAuthMiddleware) that populates the
// principal. A request without a principal yields 401; one that fails the
// requirement yields 403.
func RequireAuthorizationMiddleware(req auth.Requirement) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := auth.Authorize(r.Context(), req); err != nil {
				if errors.Is(err, auth.ErrMissingCredentials) {
					writeError(w, http.StatusUnauthorized, coreerrors.CodeUnauthenticated, err.Error())
					return
				}
				writeError(w, http.StatusForbidden, coreerrors.CodePermissionDenied, err.Error())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func BreakerMiddleware() Middleware {
	brk := breaker.New()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			err := brk.Do(r.Context(), func() error {
				sw := newStatusResponseWriter(w)
				next.ServeHTTP(sw, r)
				if sw.status >= http.StatusInternalServerError {
					return http.ErrAbortHandler
				}
				return nil
			})
			if errors.Is(err, breaker.ErrOpen) {
				writeError(w, http.StatusServiceUnavailable, coreerrors.CodeUnavailable, err.Error())
			}
		})
	}
}

func AdaptiveBreakerMiddleware(brk *breaker.AdaptiveBreaker) Middleware {
	if brk == nil {
		brk = breaker.NewAdaptive()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := brk.Allow(); err != nil {
				writeError(w, http.StatusServiceUnavailable, coreerrors.CodeUnavailable, err.Error())
				return
			}
			sw := newStatusResponseWriter(w)
			next.ServeHTTP(sw, r)
			if sw.status >= http.StatusInternalServerError {
				brk.MarkFailure()
				return
			}
			brk.MarkSuccess()
		})
	}
}

type logRedactor struct {
	headers map[string]struct{}
	queries map[string]struct{}
}

func newLogRedactor(config LogRedactionConfig) logRedactor {
	return logRedactor{headers: stringSet(config.Headers, http.CanonicalHeaderKey), queries: stringSet(config.Queries, strings.ToLower)}
}

func stringSet(values []string, normalize func(string) string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalize(strings.TrimSpace(value))
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func (r logRedactor) path(req *http.Request) string {
	path := routePattern(req)
	if len(r.queries) == 0 || req.URL.RawQuery == "" {
		return path
	}
	q := req.URL.Query()
	for key := range q {
		if _, ok := r.queries[strings.ToLower(key)]; ok {
			q.Set(key, "***")
		}
	}
	encoded := q.Encode()
	if encoded == "" {
		return path
	}
	return path + "?" + encoded
}

func (r logRedactor) safeHeaders(header http.Header) map[string]string {
	out := make(map[string]string, len(r.headers))
	for name := range r.headers {
		if header.Get(name) == "" {
			continue
		}
		out[name] = "***"
	}
	return out
}

func resolveSecurityHeaders(config SecurityHeadersConfig) map[string]string {
	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	if config.ContentSecurityPolicy != "" {
		headers["Content-Security-Policy"] = config.ContentSecurityPolicy
	}
	if config.FrameOptions != "" {
		headers["X-Frame-Options"] = config.FrameOptions
	}
	if config.ContentTypeOptions != "" {
		headers["X-Content-Type-Options"] = config.ContentTypeOptions
	}
	if config.ReferrerPolicy != "" {
		headers["Referrer-Policy"] = config.ReferrerPolicy
	}
	if config.PermissionsPolicy != "" {
		headers["Permissions-Policy"] = config.PermissionsPolicy
	}
	if config.HSTS != "" {
		headers["Strict-Transport-Security"] = config.HSTS
	}
	for key, value := range config.Custom {
		if strings.TrimSpace(key) != "" {
			headers[http.CanonicalHeaderKey(key)] = value
		}
	}
	return headers
}
