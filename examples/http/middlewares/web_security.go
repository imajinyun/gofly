package middleware

import (
	"time"

	"github.com/imajinyun/gofly/rest"
)

// SecurityHeadersMiddleware applies browser security headers. A zero config
// enables restrictive, production-safe defaults that can be overridden per app.
func SecurityHeadersMiddleware(config rest.SecurityHeadersConfig) rest.Middleware {
	return rest.SecurityHeadersMiddleware(resolveSecurityHeaders(config))
}

// MaxBodyBytesMiddleware rejects requests whose body exceeds maxBodyBytes.
func MaxBodyBytesMiddleware(maxBodyBytes int64) rest.Middleware {
	return rest.MaxBodyBytesMiddleware(maxBodyBytes)
}

// RequestTimeoutMiddleware bounds request handling time. WebSocket upgrades and
// SSE requests are intentionally passed through by gofly's runtime middleware.
func RequestTimeoutMiddleware(timeout time.Duration) rest.Middleware {
	return rest.TimeoutMiddleware(timeout)
}

func resolveSecurityHeaders(config rest.SecurityHeadersConfig) rest.SecurityHeadersConfig {
	if config.ContentSecurityPolicy == "" {
		config.ContentSecurityPolicy = "default-src 'self'; frame-ancestors 'none'"
	}
	if config.FrameOptions == "" {
		config.FrameOptions = "DENY"
	}
	if config.ContentTypeOptions == "" {
		config.ContentTypeOptions = "nosniff"
	}
	if config.ReferrerPolicy == "" {
		config.ReferrerPolicy = "no-referrer"
	}
	if config.PermissionsPolicy == "" {
		config.PermissionsPolicy = "geolocation=(), microphone=(), camera=()"
	}
	if config.HSTS == "" {
		config.HSTS = "max-age=31536000; includeSubDomains"
	}
	return config
}
