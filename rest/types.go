// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/imajinyun/gofly/core/auth"
)

// Middleware is an HTTP middleware function.
type Middleware func(http.Handler) http.Handler

// HandlerFunc is a gofly REST handler that receives a Context.
type HandlerFunc func(*Context)

// Route defines a single REST route including handler, middleware, and OpenAPI metadata.
type Route struct {
	Method      string
	Path        string
	Handler     HandlerFunc
	Middlewares []Middleware
	Summary     string
	Description string
	OperationID string
	Tags        []string
	Parameters  []Parameter
	RequestBody *RequestBody
	Responses   map[string]Response
}

// RouteOption configures route-level options.
type RouteOption func(*routeOptions)

type routeOptions struct {
	prefix       string
	middlewares  []Middleware
	auth         auth.Validator
	timeout      time.Duration
	maxBodyBytes int64
	recover      *bool
	trace        *bool
	log          *bool
	metrics      *bool
	breaker      *bool
	requestID    *bool
	trimStrings  *bool
	trimConfig   TrimStringsConfig
	rateLimit    *routeRateLimit
	adaptive     *routeAdaptiveLimit
	concurrency  *routeConcurrencyLimit
}

type routeRateLimit struct {
	enabled bool
	rate    int
	burst   int
}

type routeConcurrencyLimit struct {
	enabled bool
	limit   int
}

type routeAdaptiveLimit struct {
	enabled bool
	config  AdaptiveLimitConfig
}

type routeInfo struct {
	Method string
	Path   string
}

type routeInfoKey struct{}

type Context struct {
	Response  http.ResponseWriter
	Request   *http.Request
	Validator Validator
}

func (c *Context) JSON(code int, v any) {
	c.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Response.WriteHeader(code)
	_ = json.NewEncoder(c.Response).Encode(v)
}

func (c *Context) String(code int, s string) {
	c.Response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Response.WriteHeader(code)
	_, _ = fmt.Fprint(c.Response, s)
}

func (c *Context) Error(err error) { WriteError(c.Response, err) }

func (c *Context) Bind(v any) error {
	return bindJSON(c.Request, v, c.Validator)
}

func (c *Context) BindRequest(v any) error { return bindRequest(c.Request, v, c.Validator) }

func (c *Context) BindQuery(v any) error {
	if err := bindValues(v, BindSourceQuery, func(key string) []string { return c.Request.URL.Query()[key] }); err != nil {
		return err
	}
	return validateWith(v, c.Validator)
}

func (c *Context) BindPath(v any) error {
	if err := bindValues(v, BindSourcePath, func(key string) []string {
		if value := c.Request.PathValue(key); value != "" {
			return []string{value}
		}
		return nil
	}); err != nil {
		return err
	}
	return validateWith(v, c.Validator)
}

func (c *Context) BindHeader(v any) error {
	if err := bindValues(v, BindSourceHeader, func(key string) []string { return c.Request.Header.Values(key) }); err != nil {
		return err
	}
	return validateWith(v, c.Validator)
}

func (c *Context) Validate(v any) error { return validateWith(v, c.Validator) }

func (c *Context) Query(key string) string { return c.Request.URL.Query().Get(key) }

func (c *Context) PathValue(key string) string { return c.Request.PathValue(key) }

func (c *Context) RequestID() string { return c.Request.Header.Get(RequestIDHeader) }

func WithPrefix(prefix string) RouteOption {
	return func(opts *routeOptions) {
		opts.prefix = cleanPrefix(prefix)
	}
}

func WithMiddlewares(mws ...Middleware) RouteOption {
	return func(opts *routeOptions) {
		opts.middlewares = append(opts.middlewares, mws...)
	}
}

func WithAuth(validator auth.Validator) RouteOption {
	return func(opts *routeOptions) {
		opts.auth = validator
	}
}

func WithRecover() RouteOption {
	return func(opts *routeOptions) {
		opts.recover = boolPtr(true)
	}
}

func WithoutRecover() RouteOption {
	return func(opts *routeOptions) {
		opts.recover = boolPtr(false)
	}
}

func WithTrace() RouteOption {
	return func(opts *routeOptions) {
		opts.trace = boolPtr(true)
	}
}

func WithoutTrace() RouteOption {
	return func(opts *routeOptions) {
		opts.trace = boolPtr(false)
	}
}

func WithRequestID() RouteOption {
	return func(opts *routeOptions) {
		opts.requestID = boolPtr(true)
	}
}

func WithoutRequestID() RouteOption {
	return func(opts *routeOptions) {
		opts.requestID = boolPtr(false)
	}
}

func WithLog() RouteOption {
	return func(opts *routeOptions) {
		opts.log = boolPtr(true)
	}
}

func WithoutLog() RouteOption {
	return func(opts *routeOptions) {
		opts.log = boolPtr(false)
	}
}

func WithMetrics() RouteOption {
	return func(opts *routeOptions) {
		opts.metrics = boolPtr(true)
	}
}

func WithoutMetrics() RouteOption {
	return func(opts *routeOptions) {
		opts.metrics = boolPtr(false)
	}
}

func WithBreaker() RouteOption {
	return func(opts *routeOptions) {
		opts.breaker = boolPtr(true)
	}
}

func WithoutBreaker() RouteOption {
	return func(opts *routeOptions) {
		opts.breaker = boolPtr(false)
	}
}

func WithTimeout(timeout time.Duration) RouteOption {
	return func(opts *routeOptions) {
		opts.timeout = timeout
	}
}

func WithoutTimeout() RouteOption {
	return func(opts *routeOptions) {
		opts.timeout = 0
	}
}

func WithMaxBodyBytes(maxBodyBytes int64) RouteOption {
	return func(opts *routeOptions) {
		opts.maxBodyBytes = maxBodyBytes
	}
}

func WithoutMaxBodyBytes() RouteOption {
	return func(opts *routeOptions) {
		opts.maxBodyBytes = 0
	}
}

func WithRateLimit(rate, burst int) RouteOption {
	return func(opts *routeOptions) {
		opts.rateLimit = &routeRateLimit{enabled: true, rate: rate, burst: burst}
	}
}

func WithoutRateLimit() RouteOption {
	return func(opts *routeOptions) {
		opts.rateLimit = &routeRateLimit{}
	}
}

func WithAdaptiveRateLimit(config AdaptiveLimitConfig) RouteOption {
	return func(opts *routeOptions) {
		opts.adaptive = &routeAdaptiveLimit{enabled: true, config: config}
	}
}

func WithoutAdaptiveRateLimit() RouteOption {
	return func(opts *routeOptions) {
		opts.adaptive = &routeAdaptiveLimit{}
	}
}

func WithMaxConcurrency(limit int) RouteOption {
	return func(opts *routeOptions) {
		opts.concurrency = &routeConcurrencyLimit{enabled: true, limit: limit}
	}
}

func WithoutMaxConcurrency() RouteOption {
	return func(opts *routeOptions) {
		opts.concurrency = &routeConcurrencyLimit{}
	}
}

func WithTrimStrings() RouteOption {
	return func(opts *routeOptions) {
		opts.trimStrings = boolPtr(true)
	}
}

func WithTrimStringsConfig(config TrimStringsConfig) RouteOption {
	return func(opts *routeOptions) {
		opts.trimStrings = boolPtr(true)
		opts.trimConfig = config
	}
}

func WithoutTrimStrings() RouteOption {
	return func(opts *routeOptions) {
		opts.trimStrings = boolPtr(false)
	}
}

func RoutePatternFromContext(ctx context.Context) string {
	info, ok := ctx.Value(routeInfoKey{}).(routeInfo)
	if !ok {
		return ""
	}
	return info.Method + " " + info.Path
}

func routePattern(r *http.Request) string {
	if pattern := RoutePatternFromContext(r.Context()); pattern != "" {
		return pattern
	}
	return r.Method + " " + r.URL.Path
}

func boolPtr(v bool) *bool { return &v }

func trimStringsEnabled(v *bool) bool {
	return v == nil || *v
}
