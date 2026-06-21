package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/core/auth"
	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/rest"
)

func TestDescribeCoversP1MiddlewareMatrix_BitsUT(t *testing.T) {
	got := strings.Join(describe(), " ")
	for _, want := range []string{"JWT", "CORS", "CSRF", "sessions", "OpenTelemetry", "Prometheus", "SSE", "WebSocket", "request validation"} {
		if !strings.Contains(got, want) {
			t.Fatalf("describe() = %q, missing %q", got, want)
		}
	}
}

func TestHTTPMiddlewareServerContracts_BitsUT(t *testing.T) {
	srv := newHTTPMiddlewareServer()

	preflight := httptest.NewRecorder()
	preflightReq := httptest.NewRequest(http.MethodOptions, "/orders", nil)
	preflightReq.Header.Set("Origin", "https://app.example.com")
	preflightReq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflightReq.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, X-CSRF-Token")
	srv.Handler().ServeHTTP(preflight, preflightReq)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("OPTIONS /orders = %d origin=%q", preflight.Code, preflight.Header().Get("Access-Control-Allow-Origin"))
	}

	tokenResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tokenResp, httptest.NewRequest(http.MethodGet, "/token", nil))
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("GET /token = %d %s", tokenResp.Code, tokenResp.Body.String())
	}
	var tokenBody map[string]string
	if err := json.Unmarshal(tokenResp.Body.Bytes(), &tokenBody); err != nil {
		t.Fatalf("decode token body: %v", err)
	}
	csrfCookie := cookieNamed(t, tokenResp.Result().Cookies(), "gofly_demo_csrf")
	sessionCookie := cookieNamed(t, tokenResp.Result().Cookies(), "gofly_demo_session")

	missingAuth := httptest.NewRecorder()
	missingAuthReq := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"tea","quantity":1}`))
	missingAuthReq.AddCookie(csrfCookie)
	missingAuthReq.AddCookie(sessionCookie)
	missingAuthReq.Header.Set("X-CSRF-Token", csrfCookie.Value)
	srv.Handler().ServeHTTP(missingAuth, missingAuthReq)
	if missingAuth.Code != http.StatusUnauthorized || !strings.Contains(missingAuth.Body.String(), string(coreerrors.CodeUnauthenticated)) {
		t.Fatalf("POST /orders without JWT = %d %s", missingAuth.Code, missingAuth.Body.String())
	}

	missingCSRF := httptest.NewRecorder()
	missingCSRFReq := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"tea","quantity":1}`))
	missingCSRFReq.Header.Set(auth.AuthorizationHeader, auth.BearerValue(tokenBody["token"]))
	missingCSRFReq.AddCookie(sessionCookie)
	srv.Handler().ServeHTTP(missingCSRF, missingCSRFReq)
	if missingCSRF.Code != http.StatusForbidden || !strings.Contains(missingCSRF.Body.String(), string(coreerrors.CodePermissionDenied)) {
		t.Fatalf("POST /orders without CSRF = %d %s", missingCSRF.Code, missingCSRF.Body.String())
	}

	created := httptest.NewRecorder()
	createdReq := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"tea","quantity":2}`))
	createdReq.Header.Set(auth.AuthorizationHeader, auth.BearerValue(tokenBody["token"]))
	createdReq.Header.Set("X-CSRF-Token", csrfCookie.Value)
	createdReq.Header.Set("Content-Type", "application/json")
	createdReq.AddCookie(csrfCookie)
	createdReq.AddCookie(sessionCookie)
	srv.Handler().ServeHTTP(created, createdReq)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"subject":"demo-user"`) || !strings.Contains(created.Body.String(), `"session_id":"demo-session"`) {
		t.Fatalf("POST /orders = %d %s", created.Code, created.Body.String())
	}
	if created.Header().Get(rest.RequestIDHeader) == "" || created.Header().Get("Traceparent") == "" {
		t.Fatalf("POST /orders missing request/trace headers: request=%q trace=%q", created.Header().Get(rest.RequestIDHeader), created.Header().Get("Traceparent"))
	}

	invalid := httptest.NewRecorder()
	invalidReq := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"sku":"x","quantity":0}`))
	invalidReq.Header.Set(auth.AuthorizationHeader, auth.BearerValue(tokenBody["token"]))
	invalidReq.Header.Set("X-CSRF-Token", csrfCookie.Value)
	invalidReq.AddCookie(csrfCookie)
	invalidReq.AddCookie(sessionCookie)
	srv.Handler().ServeHTTP(invalid, invalidReq)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), string(coreerrors.CodeInvalidArgument)) {
		t.Fatalf("POST /orders invalid = %d %s", invalid.Code, invalid.Body.String())
	}

	events := httptest.NewRecorder()
	srv.Handler().ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/events", nil))
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), "event: ready") || events.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("GET /events = %d content-type=%q body=%q", events.Code, events.Header().Get("Content-Type"), events.Body.String())
	}

	metrics := httptest.NewRecorder()
	srv.Handler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), "gofly_requests_total") {
		t.Fatalf("GET /metrics = %d %s", metrics.Code, metrics.Body.String())
	}

	openapi := httptest.NewRecorder()
	srv.Handler().ServeHTTP(openapi, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if openapi.Code != http.StatusOK || !strings.Contains(openapi.Body.String(), "http middleware demo") || !strings.Contains(openapi.Body.String(), "/ws") {
		t.Fatalf("GET /openapi.json = %d %s", openapi.Code, openapi.Body.String())
	}
}

func TestJWTAndSessionHelpers_BitsUT(t *testing.T) {
	token, err := demoJWT(time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("demoJWT() error = %v", err)
	}
	claims, err := auth.VerifyJWT(token, jwtSecret, auth.JWTOptions{Issuer: "gofly-demo", Audience: "orders", Now: func() time.Time { return time.Unix(1700000001, 0) }})
	if err != nil || claims.Subject != "demo-user" {
		t.Fatalf("VerifyJWT() claims=%#v err=%v", claims, err)
	}

	signed := signSession("session-123", sessionSecret)
	got, ok := verifySession(signed, sessionSecret)
	if !ok || got != "session-123" {
		t.Fatalf("verifySession() = %q %v", got, ok)
	}
	if _, ok := verifySession(signed+"tampered", sessionSecret); ok {
		t.Fatalf("verifySession() accepted tampered session")
	}
}

func cookieNamed(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %q in %#v", name, cookies)
	return nil
}
