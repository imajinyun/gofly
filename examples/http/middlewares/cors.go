package middleware

import (
	"net/http"
	"time"

	"github.com/imajinyun/gofly/rest"
)

// CORSMiddleware returns a reusable CORS middleware with production-safe exact
// origin matching. A blank AllowOrigins list stays restrictive; this function
// fills only operational defaults such as methods, headers, and preflight cache.
func CORSMiddleware(config rest.CORSConfig) rest.Middleware {
	if len(config.AllowOrigins) == 0 {
		return rest.CORSMiddleware(config)
	}
	if len(config.AllowMethods) == 0 {
		config.AllowMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	}
	if len(config.AllowHeaders) == 0 {
		config.AllowHeaders = []string{"Authorization", "Content-Type"}
	}
	if len(config.ExposeHeaders) == 0 {
		config.ExposeHeaders = []string{rest.RequestIDHeader}
	}
	if config.MaxAge <= 0 {
		config.MaxAge = 10 * time.Minute
	}
	return rest.CORSMiddleware(config)
}
