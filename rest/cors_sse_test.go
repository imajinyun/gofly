package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCORSMiddlewareHandlesPreflight(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{CORS: &CORSConfig{
		AllowOrigins:     []string{"https://example.com"},
		AllowMethods:     []string{http.MethodGet, http.MethodPost},
		AllowHeaders:     []string{"Authorization"},
		AllowCredentials: true,
		MaxAge:           time.Hour,
	}}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/ping", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "pong") }})

	req := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("allow origin = %q, want https://example.com", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow credentials = %q, want true", got)
	}
}

func TestCORSMiddlewareRejectsWildcardWithCredentials(t *testing.T) {
	s := MustNewServer(Config{Middlewares: MiddlewaresConfig{CORS: &CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowCredentials: true,
	}}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/ping", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "pong") }})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow origin = %q, want empty for wildcard credentials", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("allow credentials = %q, want empty for wildcard credentials", got)
	}
}

func TestContextSSEWritesEvent(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/events", Handler: func(ctx *Context) {
		if err := ctx.SSE(SSEEvent{ID: "1", Event: "message", Data: "hello\nworld"}); err != nil {
			t.Fatalf("SSE returned error: %v", err)
		}
	}})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q, want text/event-stream", got)
	}
	want := "id: 1\nevent: message\ndata: hello\ndata: world\n\n"
	if rec.Body.String() != want {
		t.Fatalf("body = %q, want %q", rec.Body.String(), want)
	}
}
