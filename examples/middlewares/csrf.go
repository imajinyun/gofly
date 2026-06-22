package middleware

import (
	"github.com/gofly/gofly/rest"
)

// CSRFMiddleware returns double-submit-cookie CSRF protection. In production,
// pass a unique 32+ byte Secret and set Secure=true for HTTPS deployments.
func CSRFMiddleware(config rest.CSRFConfig) rest.Middleware {
	return rest.CSRFMiddleware(config)
}
