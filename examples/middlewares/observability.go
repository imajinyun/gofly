package middleware

import (
	"net/http"
	"net/http/pprof"
	"strings"

	"github.com/gofly/gofly/rest"
)

// RequestIDMiddleware propagates or generates X-Request-Id and stores it in
// request metadata for logs, traces, and downstream handlers.
func RequestIDMiddleware() rest.Middleware {
	return rest.RequestIDMiddleware()
}

// StructuredAccessLogMiddleware emits one structured slog access log per
// request with method, route path, status, latency, and trace attributes.
func StructuredAccessLogMiddleware(redaction rest.LogRedactionConfig) rest.Middleware {
	return rest.LogMiddlewareWithConfig(nil, redaction)
}

// RegisterPprofRoutes exposes standard pprof handlers under prefix. Protect
// these routes with APIKeyMiddleware, BasicAuthMiddleware, or admin-only network
// policy before exposing them outside localhost.
func RegisterPprofRoutes(srv *rest.Server, prefix string, middlewares ...rest.Middleware) {
	if srv == nil {
		return
	}
	prefix = strings.TrimRight("/"+strings.Trim(prefix, "/"), "/")
	if prefix == "" {
		prefix = "/debug/pprof"
	}
	addPprofRoute := func(method, path string, handler http.HandlerFunc) {
		srv.AddRoute(rest.Route{Method: method, Path: prefix + path, Middlewares: middlewares, Handler: func(c *rest.Context) {
			handler(c.Response, c.Request)
		}})
	}
	addPprofRoute(http.MethodGet, "/", pprof.Index)
	addPprofRoute(http.MethodGet, "/cmdline", pprof.Cmdline)
	addPprofRoute(http.MethodGet, "/profile", pprof.Profile)
	addPprofRoute(http.MethodGet, "/symbol", pprof.Symbol)
	addPprofRoute(http.MethodPost, "/symbol", pprof.Symbol)
	addPprofRoute(http.MethodGet, "/trace", pprof.Trace)
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: prefix + "/{name}", Middlewares: middlewares, Handler: func(c *rest.Context) {
		pprof.Index(c.Response, c.Request)
	}})
}
