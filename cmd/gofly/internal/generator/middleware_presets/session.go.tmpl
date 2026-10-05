package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/rest"
)

// SessionConfig configures a signed cookie session middleware.
type SessionConfig struct {
	Secret               []byte
	CookieName           string
	HeaderName           string // Projection of the verified ID for downstream handlers; never an input credential.
	DefaultID            string
	Path                 string
	TTL                  time.Duration
	Secure               bool // Force Secure even when local HTTP cookies are explicitly allowed.
	AllowInsecureCookies bool // Explicit opt-in for local HTTP development; never use on public deployments.
	SameSite             http.SameSite
}

type sessionContextKey struct {
	cookieName string
	keyDigest  [sha256.Size]byte
}

func sessionKey(config SessionConfig) sessionContextKey {
	return sessionContextKey{cookieName: config.CookieName, keyDigest: sha256.Sum256(config.Secret)}
}

// SessionMiddleware verifies or issues a signed session cookie and exposes its
// ID through a request header for downstream handlers. Set Secret to a unique
// high-entropy value before using this middleware in production.
func SessionMiddleware(config SessionConfig) rest.Middleware {
	config = resolveSessionConfig(config)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(config.Secret) == 0 {
				rest.WriteError(w, coreerrors.New(coreerrors.CodeInternal, "session secret is required"))
				return
			}
			sessionID := SessionID(r, config)
			if sessionID == "" {
				sessionID = config.DefaultID
				if sessionID == "" {
					var err error
					sessionID, err = NewSessionID()
					if err != nil {
						rest.WriteError(w, coreerrors.Wrap(coreerrors.CodeInternal, "generate session id", err))
						return
					}
				}
			}
			signed := SignSession(sessionID, config.Secret)
			// #nosec G124 -- cookies default to Secure/HttpOnly/Lax; plaintext cookies require explicit local-HTTP opt-in and SameSite=None retains Secure.
			cookie := &http.Cookie{Name: config.CookieName, Value: signed, Path: config.Path, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(config.TTL.Seconds())}
			cookie.SameSite = config.SameSite
			if config.AllowInsecureCookies && !config.Secure && config.SameSite != http.SameSiteNoneMode {
				cookie.Secure = false
			}
			http.SetCookie(w, cookie)
			r = r.WithContext(context.WithValue(r.Context(), sessionKey(config), sessionID))
			r.Header.Set(config.HeaderName, sessionID)
			next.ServeHTTP(w, r)
		})
	}
}

// NewSessionID returns a URL-safe 256-bit random session identifier.
func NewSessionID() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// SessionID returns the established request identity or verifies a signed cookie.
// Client-supplied session headers are never trusted as proof of identity.
func SessionID(r *http.Request, config SessionConfig) string {
	if r == nil {
		return ""
	}
	config = resolveSessionConfig(config)
	if sessionID, ok := r.Context().Value(sessionKey(config)).(string); ok && sessionID != "" {
		return sessionID
	}
	cookie, err := r.Cookie(config.CookieName)
	if err != nil {
		return ""
	}
	sessionID, ok := VerifySession(cookie.Value, config.Secret)
	if !ok {
		return ""
	}
	return sessionID
}

// SignSession signs a session ID with HMAC-SHA256.
func SignSession(sessionID string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(sessionID))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(sessionID)) + "." + sig
}

// VerifySession verifies a signed session cookie value and returns the session ID.
func VerifySession(value string, secret []byte) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || len(secret) == 0 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	sessionID := string(raw)
	want := SignSession(sessionID, secret)
	return sessionID, subtle.ConstantTimeCompare([]byte(value), []byte(want)) == 1
}

func resolveSessionConfig(config SessionConfig) SessionConfig {
	if config.CookieName == "" {
		config.CookieName = "gofly_session"
	}
	if config.HeaderName == "" {
		config.HeaderName = "X-Gofly-Session-Id"
	}
	if config.Path == "" {
		config.Path = "/"
	}
	if config.TTL <= 0 {
		config.TTL = time.Hour
	}
	if config.SameSite == 0 {
		config.SameSite = http.SameSiteLaxMode
	}
	return config
}
