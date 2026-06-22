package middleware

import (
	"time"

	"github.com/gofly/gofly/core/auth"
	"github.com/gofly/gofly/rest"
)

// JWTConfig configures HS256 bearer JWT authentication.
type JWTConfig struct {
	Secret   []byte
	Issuer   string
	Audience string
}

// JWTMiddleware validates Authorization: Bearer <jwt> and stores the principal
// in the request context. Copy this file into internal/middleware and register
// it with server.Use(JWTMiddleware(config)) or on selected protected routes.
func JWTMiddleware(config JWTConfig) rest.Middleware {
	return rest.BearerAuthMiddleware(auth.JWTValidator(config.Secret, auth.JWTOptions{Issuer: config.Issuer, Audience: config.Audience}))
}

// SignJWT signs an HS256 JWT for tests, login handlers, or local development
// token endpoints. Production login flows should call this only after verifying
// user credentials and should set a short TTL.
func SignJWT(secret []byte, subject string, ttl time.Duration, opts auth.JWTOptions, extra map[string]any) (string, error) {
	now := time.Now()
	if opts.Now != nil {
		now = opts.Now()
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return auth.SignJWT(auth.JWTClaims{Subject: subject, Issuer: opts.Issuer, Audience: opts.Audience, IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(), Extra: extra}, secret)
}
