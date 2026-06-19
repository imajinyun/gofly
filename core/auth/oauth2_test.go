package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestOAuth2Server(t *testing.T, now time.Time) *OAuth2Server {
	t.Helper()
	kr, err := NewJWTKeyring(SigningKey{KID: "k1", Secret: []byte("oauth-secret")})
	if err != nil {
		t.Fatalf("NewJWTKeyring returned error: %v", err)
	}
	kr.SetClock(func() time.Time { return now })
	srv, err := NewOAuth2Server(OAuth2Config{
		Keyring: kr,
		Issuer:  "gofly",
		TTL:     time.Hour,
		Now:     func() time.Time { return now },
		Clients: map[string]OAuth2Client{
			"svc": {ID: "svc", Secret: "topsecret", Scopes: []string{"orders:read", "orders:write"}, Audience: "api"},
		},
	})
	if err != nil {
		t.Fatalf("NewOAuth2Server returned error: %v", err)
	}
	return srv
}

func TestOAuth2IssueClientCredentials(t *testing.T) {
	now := time.Unix(1000, 0)
	srv := newTestOAuth2Server(t, now)
	token, err := srv.Issue("svc", "topsecret", []string{"orders:read"})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}
	if token.TokenType != "Bearer" || token.ExpiresIn != 3600 || token.Scope != "orders:read" {
		t.Fatalf("token = %#v", token)
	}
	claims, err := srv.conf.Keyring.Verify(token.AccessToken, JWTOptions{Issuer: "gofly", Audience: "api", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if claims.Subject != "svc" {
		t.Fatalf("subject = %q, want svc", claims.Subject)
	}
	principal := claims.Principal()
	if !principal.HasPermission("orders:read") {
		t.Fatalf("principal missing scope: %#v", principal)
	}
}

func TestOAuth2RejectsBadSecretAndScope(t *testing.T) {
	now := time.Unix(1000, 0)
	srv := newTestOAuth2Server(t, now)
	if _, err := srv.Issue("svc", "wrong", []string{"orders:read"}); err == nil {
		t.Fatal("bad secret should error")
	} else if oerr, ok := err.(*OAuth2Error); !ok || oerr.Code != "invalid_client" {
		t.Fatalf("error = %v, want invalid_client", err)
	}
	if _, err := srv.Issue("svc", "topsecret", []string{"admin:all"}); err == nil {
		t.Fatal("disallowed scope should error")
	} else if oerr, ok := err.(*OAuth2Error); !ok || oerr.Code != "invalid_scope" {
		t.Fatalf("error = %v, want invalid_scope", err)
	}
	if _, err := srv.Issue("unknown", "x", nil); err == nil {
		t.Fatal("unknown client should error")
	}
}

func TestOAuth2TokenHandlerFormCredentials(t *testing.T) {
	now := time.Unix(1000, 0)
	srv := newTestOAuth2Server(t, now)
	form := url.Values{
		"grant_type":    {GrantClientCredentials},
		"client_id":     {"svc"},
		"client_secret": {"topsecret"},
		"scope":         {"orders:read orders:write"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var token OAuth2Token
	if err := json.Unmarshal(rec.Body.Bytes(), &token); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if token.AccessToken == "" || token.Scope != "orders:read orders:write" {
		t.Fatalf("token = %#v", token)
	}
}

func TestOAuth2TokenHandlerBasicAuth(t *testing.T) {
	now := time.Unix(1000, 0)
	srv := newTestOAuth2Server(t, now)
	form := url.Values{"grant_type": {GrantClientCredentials}, "scope": {"orders:read"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("svc", "topsecret")
	rec := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestOAuth2TokenHandlerErrors(t *testing.T) {
	now := time.Unix(1000, 0)
	srv := newTestOAuth2Server(t, now)

	// unsupported grant
	form := url.Values{"grant_type": {"password"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var oerr OAuth2Error
	_ = json.Unmarshal(rec.Body.Bytes(), &oerr)
	if oerr.Code != "unsupported_grant_type" {
		t.Fatalf("error code = %q, want unsupported_grant_type", oerr.Code)
	}

	// GET not allowed
	getReq := httptest.NewRequest(http.MethodGet, "/oauth/token", nil)
	getRec := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", getRec.Code)
	}

	// bad credentials -> 401
	badForm := url.Values{"grant_type": {GrantClientCredentials}, "client_id": {"svc"}, "client_secret": {"nope"}}
	badReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(badForm.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badRec := httptest.NewRecorder()
	srv.TokenHandler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("bad creds status = %d, want 401", badRec.Code)
	}
}

func TestOAuth2Validator(t *testing.T) {
	now := time.Unix(1000, 0)
	srv := newTestOAuth2Server(t, now)
	token, err := srv.Issue("svc", "topsecret", []string{"orders:read"})
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}
	opts := JWTOptions{Now: func() time.Time { return now }}
	validator := OAuth2Validator(srv.conf.Keyring, opts, "orders:read")
	ctx, err := validator(t.Context(), token.AccessToken)
	if err != nil {
		t.Fatalf("validator returned error: %v", err)
	}
	if SubjectFromContext(ctx) != "svc" {
		t.Fatalf("subject = %q, want svc", SubjectFromContext(ctx))
	}
	// missing required scope -> permission denied
	denied := OAuth2Validator(srv.conf.Keyring, opts, "orders:write")
	if _, err := denied(t.Context(), token.AccessToken); err == nil {
		t.Fatal("missing scope should be denied")
	}
}

func TestNewOAuth2ServerRequiresKeyring(t *testing.T) {
	if _, err := NewOAuth2Server(OAuth2Config{}); err == nil {
		t.Fatal("missing keyring should error")
	}
}
