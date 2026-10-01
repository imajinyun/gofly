// Package gozero contains small migration adapters for projects that previously
// used go-zero-style HTTP handler signatures. go-zero is a third-party project;
// this package is not endorsed by or affiliated with its maintainers and does
// not include or depend on go-zero source code.
package gozero

import (
	"context"
	"net/http"

	"github.com/imajinyun/gofly/rest"
)

// Handler is a minimal HTTP handler shape used by migration code that previously
// targeted go-zero/httpx helpers.
type Handler func(http.ResponseWriter, *http.Request)

// FromHandler adapts a migration HTTP handler into a gofly REST handler.
func FromHandler(handler Handler) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if handler == nil {
			ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "go-zero handler is nil"})
			return
		}
		handler(ctx.Response, ctx.Request)
	}
}

// Middleware is a minimal HTTP middleware shape that can be passed to gofly
// routes after adaptation with FromMiddleware.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// FromMiddleware adapts migration HTTP middleware into gofly REST middleware.
func FromMiddleware(middleware Middleware) rest.Middleware {
	return func(next http.Handler) http.Handler {
		if middleware == nil {
			return next
		}
		return middleware(next.ServeHTTP)
	}
}

// RequestContext returns the request context with a nil-safe fallback for old
// migration code that accepted a request pointer.
func RequestContext(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return r.Context()
}
