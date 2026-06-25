// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
)

const (
	// DefaultCSRFCookieName is the default CSRF cookie name.
	DefaultCSRFCookieName = "gofly_csrf"
	// DefaultCSRFHeaderName is the default CSRF token header name.
	DefaultCSRFHeaderName = "X-CSRF-Token"
	// DefaultDevelopmentCSRFSecret is the insecure development default secret.
	DefaultDevelopmentCSRFSecret = "gofly-development-csrf-secret"
)

// CSRFConfig configures Cross-Site Request Forgery (CSRF) protection using
// the double-submit cookie pattern. Tokens are HMAC-SHA256 signed with a
// server-side secret and validated on each state-changing request.
//
// The zero value loads development defaults: cookie name "gofly_csrf",
// header name "X-CSRF-Token", and a development-only placeholder secret.
// Production deployments must set Secret to a unique, cryptographically
// random value.
type CSRFConfig struct {
	// Secret is the HMAC key used to sign CSRF tokens. Must be at least
	// 32 bytes in production. Default: "gofly-development-csrf-secret" (insecure).
	Secret []byte

	// CookieName is the HTTP cookie name for the CSRF token.
	// Default: "gofly_csrf".
	CookieName string

	// HeaderName is the HTTP request header that carries the CSRF token
	// on state-changing requests. Default: "X-CSRF-Token".
	HeaderName string

	// TTL controls how long a CSRF token remains valid.
	// Default: 24 hours.
	TTL time.Duration

	// Path sets the Cookie Path attribute. Default: "/".
	Path string

	// Secure requires HTTPS for the CSRF cookie. In production this
	// should be true. Default: false (permissive for local dev).
	Secure bool

	// HTTPOnly prevents client-side JavaScript from reading the CSRF
	// cookie. Default: true.
	HTTPOnly bool

	// SameSite controls the SameSite attribute of the CSRF cookie.
	// Default: http.SameSiteStrictMode.
	SameSite http.SameSite
}

func CSRFMiddleware(c CSRFConfig) Middleware {
	c = resolveCSRFConfig(c)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				ensureCSRFCookie(w, r, c)
				next.ServeHTTP(w, r)
				return
			}
			cookie, err := r.Cookie(c.CookieName)
			if err != nil || cookie.Value == "" {
				writeError(w, http.StatusForbidden, coreerrors.CodePermissionDenied, "csrf token cookie is missing")
				return
			}
			token := r.Header.Get(c.HeaderName)
			if token == "" {
				token = r.FormValue("csrf_token")
			}
			if token == "" {
				writeError(w, http.StatusForbidden, coreerrors.CodePermissionDenied, "csrf token is missing")
				return
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(cookie.Value)) != 1 || !verifyCSRFToken(c.Secret, token, time.Now()) {
				writeError(w, http.StatusForbidden, coreerrors.CodePermissionDenied, "csrf token is invalid")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func NewCSRFToken(secret []byte, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate csrf nonce: %w", err)
	}
	expires := strconv.FormatInt(time.Now().Add(ttl).UnixNano(), 10)
	body := base64.RawURLEncoding.EncodeToString(nonce[:]) + "." + expires
	mac := csrfMAC(secret, body)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

func verifyCSRFToken(secret []byte, token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.UnixNano() > expires {
		return false
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want := csrfMAC(secret, parts[0]+"."+parts[1])
	return subtle.ConstantTimeCompare(mac, want) == 1
}

func ensureCSRFCookie(w http.ResponseWriter, r *http.Request, c CSRFConfig) {
	if cookie, err := r.Cookie(c.CookieName); err == nil && verifyCSRFToken(c.Secret, cookie.Value, time.Now()) {
		return
	}
	token, err := NewCSRFToken(c.Secret, c.TTL)
	if err != nil {
		return
	}
	// #nosec G124 -- resolveCSRFConfig forces HttpOnly and SameSite; production validation rejects non-Secure cookies.
	http.SetCookie(w, &http.Cookie{Name: c.CookieName, Value: token, Path: c.Path, Expires: time.Now().Add(c.TTL), MaxAge: int(c.TTL.Seconds()), Secure: c.Secure, HttpOnly: c.HTTPOnly, SameSite: c.SameSite})
}

func resolveCSRFConfig(c CSRFConfig) CSRFConfig {
	if len(c.Secret) == 0 {
		c.Secret = []byte(DefaultDevelopmentCSRFSecret)
	}
	if c.CookieName == "" {
		c.CookieName = DefaultCSRFCookieName
	}
	if c.HeaderName == "" {
		c.HeaderName = DefaultCSRFHeaderName
	}
	if c.TTL <= 0 {
		c.TTL = 12 * time.Hour
	}
	if c.Path == "" {
		c.Path = "/"
	}
	if c.SameSite == 0 {
		c.SameSite = http.SameSiteLaxMode
	}
	c.HTTPOnly = true
	return c
}

func csrfMAC(secret []byte, body string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(body))
	return mac.Sum(nil)
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}
