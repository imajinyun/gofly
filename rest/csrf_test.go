package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCSRFMiddlewareIssuesAndValidatesToken(t *testing.T) {
	secret := []byte("test-secret")
	s := csrfTestServer(secret)
	get := httptest.NewRequest(http.MethodGet, "/form", nil)
	getRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", getRec.Code)
	}
	cookies := getRec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != DefaultCSRFCookieName || cookies[0].Value == "" {
		t.Fatalf("unexpected csrf cookies: %+v", cookies)
	}
	post := httptest.NewRequest(http.MethodPost, "/form", nil)
	post.AddCookie(cookies[0])
	post.Header.Set(DefaultCSRFHeaderName, cookies[0].Value)
	postRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusOK {
		t.Fatalf("valid POST status = %d, want 200 body=%s", postRec.Code, postRec.Body.String())
	}
}

func TestCSRFMiddlewareIssuesHardenedDefaultCookie(t *testing.T) {
	secret := []byte("test-secret")
	s := csrfTestServer(secret)
	req := httptest.NewRequest(http.MethodGet, "/form", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %+v, want one csrf cookie", cookies)
	}
	cookie := cookies[0]
	if !cookie.HttpOnly {
		t.Fatalf("csrf cookie HttpOnly = false, want true")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("csrf cookie SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Fatalf("csrf cookie Path = %q, want /", cookie.Path)
	}
}

func TestCSRFMiddlewareRejectsMissingAndMismatchedToken(t *testing.T) {
	secret := []byte("test-secret")
	s := csrfTestServer(secret)
	token, err := NewCSRFToken(secret, time.Hour)
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	missing := httptest.NewRequest(http.MethodPost, "/form", nil)
	missingRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(missingRec, missing)
	if missingRec.Code != http.StatusForbidden {
		t.Fatalf("missing token status = %d, want 403", missingRec.Code)
	}
	mismatch := httptest.NewRequest(http.MethodPost, "/form", nil)
	mismatch.AddCookie(&http.Cookie{Name: DefaultCSRFCookieName, Value: token})
	mismatch.Header.Set(DefaultCSRFHeaderName, token+"x")
	mismatchRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(mismatchRec, mismatch)
	if mismatchRec.Code != http.StatusForbidden {
		t.Fatalf("mismatched token status = %d, want 403", mismatchRec.Code)
	}
}

func TestCSRFMiddlewareRejectsExpiredToken(t *testing.T) {
	secret := []byte("test-secret")
	s := csrfTestServer(secret)
	token, err := NewCSRFToken(secret, time.Nanosecond)
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	time.Sleep(time.Millisecond)
	req := httptest.NewRequest(http.MethodPost, "/form", nil)
	req.AddCookie(&http.Cookie{Name: DefaultCSRFCookieName, Value: token})
	req.Header.Set(DefaultCSRFHeaderName, token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expired token status = %d, want 403", rec.Code)
	}
}

func csrfTestServer(secret []byte) *Server {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/form", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "ok") }}, WithMiddlewares(CSRFMiddleware(CSRFConfig{Secret: secret, TTL: time.Hour})))
	s.AddRoute(Route{Method: http.MethodPost, Path: "/form", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "posted") }}, WithMiddlewares(CSRFMiddleware(CSRFConfig{Secret: secret, TTL: time.Hour})))
	return s
}
