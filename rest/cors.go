// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CORSConfig configures Cross-Origin Resource Sharing (CORS) response headers
// for a route or group. A zero-valued CORSConfig does not set any permissive
// headers, so browser requests from different origins will be blocked.
type CORSConfig struct {
	// AllowOrigins lists permitted origin URLs (exact match) or "*" for any
	// origin. When AllowCredentials is true, "*" is ignored and specific
	// origins must be listed. Default: empty (block cross-origin requests).
	AllowOrigins []string `json:"allowOrigins,omitempty"`

	// AllowMethods lists permitted HTTP methods for preflight (OPTIONS)
	// responses. Default: GET, POST, PUT, PATCH, DELETE, OPTIONS.
	AllowMethods []string `json:"allowMethods,omitempty"`

	// AllowHeaders lists permitted request headers in preflight responses.
	// Default: empty (echoes the client's Access-Control-Request-Headers).
	AllowHeaders []string `json:"allowHeaders,omitempty"`

	// ExposeHeaders lists response headers that browsers expose to
	// client JavaScript. Default: empty (only basic headers are exposed).
	ExposeHeaders []string `json:"exposeHeaders,omitempty"`

	// AllowCredentials, when true, sets
	// Access-Control-Allow-Credentials: true and prevents wildcard origins.
	AllowCredentials bool `json:"allowCredentials,omitempty"`

	// MaxAge sets how long (in duration) the preflight result can be
	// cached by the browser. Default: 0 (no cache header sent).
	MaxAge time.Duration `json:"maxAge,omitempty"`
}

func CORSMiddleware(config CORSConfig) Middleware {
	allowedOrigins := normalizeList(config.AllowOrigins)
	allowedMethods := normalizeMethods(config.AllowMethods)
	allowedHeaders := normalizeList(config.AllowHeaders)
	exposeHeaders := normalizeList(config.ExposeHeaders)
	maxAge := config.MaxAge

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if allowOrigin := matchOrigin(origin, allowedOrigins, config.AllowCredentials); allowOrigin != "" {
					w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
					w.Header().Add("Vary", "Origin")
					if config.AllowCredentials {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
				}
			}
			if len(exposeHeaders) > 0 {
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(exposeHeaders, ", "))
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowedMethods, ", "))
				if len(allowedHeaders) > 0 {
					w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
				} else if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
					w.Header().Set("Access-Control-Allow-Headers", requested)
				}
				if maxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(maxAge.Seconds())))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func normalizeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizeMethods(methods []string) []string {
	if len(methods) == 0 {
		return []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	}
	out := normalizeList(methods)
	for i, method := range out {
		out[i] = strings.ToUpper(method)
	}
	return out
}

func matchOrigin(origin string, allowed []string, credentials bool) string {
	if len(allowed) == 0 {
		return ""
	}
	for _, item := range allowed {
		if item == "*" {
			if credentials {
				return ""
			}
			return "*"
		}
		if item == origin {
			return origin
		}
	}
	return ""
}
