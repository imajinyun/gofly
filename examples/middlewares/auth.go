package middleware

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/rest"
)

const defaultAPIKeyHeader = "X-API-Key"

// APIKeyConfig configures API key authentication for service-to-service or
// machine clients. Keys are compared in constant time and the matched principal
// is attached to the request context.
type APIKeyConfig struct {
	HeaderName string
	QueryName  string
	Keys       map[string]auth.Principal
}

// APIKeyMiddleware validates an API key from HeaderName or QueryName and stores
// the configured principal in request context. Copy this file into
// internal/middleware and register with server.Use(APIKeyMiddleware(config)).
func APIKeyMiddleware(config APIKeyConfig) rest.Middleware {
	if config.HeaderName == "" {
		config.HeaderName = defaultAPIKeyHeader
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(config.Keys) == 0 {
				rest.WriteError(w, coreerrors.New(coreerrors.CodeInternal, "api key config requires at least one key"))
				return
			}
			key := r.Header.Get(config.HeaderName)
			if key == "" && config.QueryName != "" {
				key = r.URL.Query().Get(config.QueryName)
			}
			if key == "" {
				rest.WriteError(w, coreerrors.New(coreerrors.CodeUnauthenticated, "api key is required"))
				return
			}
			principal, ok := principalForAPIKey(key, config.Keys)
			if !ok {
				rest.WriteError(w, coreerrors.New(coreerrors.CodeUnauthenticated, "api key is invalid"))
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.NewContext(r.Context(), principal)))
		})
	}
}

// BasicAuthUser configures one HTTP Basic authentication account.
type BasicAuthUser struct {
	Password  string
	Principal auth.Principal
}

// BasicAuthConfig configures HTTP Basic authentication. Keep it for internal
// tooling or low-risk admin surfaces; prefer JWT/API keys for external APIs.
type BasicAuthConfig struct {
	Realm string
	Users map[string]BasicAuthUser
}

// BasicAuthMiddleware validates Authorization: Basic credentials and stores the
// matched principal in request context.
func BasicAuthMiddleware(config BasicAuthConfig) rest.Middleware {
	if config.Realm == "" {
		config.Realm = "gofly"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, password, ok := r.BasicAuth()
			if !ok {
				writeBasicAuthChallenge(w, config.Realm, "basic auth credentials are required")
				return
			}
			principal, ok := principalForBasicAuth(username, password, config.Users)
			if !ok {
				writeBasicAuthChallenge(w, config.Realm, "basic auth credentials are invalid")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.NewContext(r.Context(), principal)))
		})
	}
}

// RBACMiddleware enforces roles and permissions against the principal already
// attached by JWTMiddleware, APIKeyMiddleware, BasicAuthMiddleware, or another
// authenticator.
func RBACMiddleware(requirement auth.Requirement) rest.Middleware {
	return rest.RequireAuthorizationMiddleware(requirement)
}

// PrincipalFromContext returns the authenticated principal for handlers that
// need user details after auth middleware has run.
func PrincipalFromContext(ctx context.Context) (auth.Principal, bool) {
	return auth.FromContext(ctx)
}

func principalForAPIKey(presented string, keys map[string]auth.Principal) (auth.Principal, bool) {
	for expected, principal := range keys {
		if constantTimeStringEqual(presented, expected) {
			return principal, true
		}
	}
	return auth.Principal{}, false
}

func principalForBasicAuth(username, password string, users map[string]BasicAuthUser) (auth.Principal, bool) {
	for expectedUsername, user := range users {
		if constantTimeStringEqual(username, expectedUsername) && constantTimeStringEqual(password, user.Password) {
			principal := user.Principal
			if principal.Subject == "" {
				principal.Subject = expectedUsername
			}
			return principal, true
		}
	}
	return auth.Principal{}, false
}

func constantTimeStringEqual(a, b string) bool {
	aHash := sha256.Sum256([]byte(a))
	bHash := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(aHash[:], bHash[:]) == 1
}

func writeBasicAuthChallenge(w http.ResponseWriter, realm, message string) {
	realm = strings.NewReplacer("\\", "", "\"", "", "\r", "", "\n", "").Replace(realm)
	w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
	rest.WriteError(w, coreerrors.New(coreerrors.CodeUnauthenticated, message))
}
