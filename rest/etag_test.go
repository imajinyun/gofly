package rest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestETagMiddlewareSetsAndValidatesIfNoneMatch(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/items", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "payload")
	}}, WithMiddlewares(ETagMiddleware(ETagConfig{})))
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "payload" {
		t.Fatalf("first response = %d/%q", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || !strings.HasPrefix(etag, `"`) {
		t.Fatalf("missing strong etag: %q", etag)
	}
	conditional := httptest.NewRequest(http.MethodGet, "/items", nil)
	conditional.Header.Set("If-None-Match", etag)
	conditionalRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(conditionalRec, conditional)
	if conditionalRec.Code != http.StatusNotModified || conditionalRec.Body.Len() != 0 {
		t.Fatalf("conditional response = %d/%q, want 304/empty", conditionalRec.Code, conditionalRec.Body.String())
	}
}

func TestETagMiddlewareHeadAndWeakMode(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodHead, Path: "/items", Handler: func(ctx *Context) {
		ctx.String(http.StatusOK, "payload")
	}}, WithMiddlewares(ETagMiddleware(ETagConfig{Weak: true})))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/items", nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD response = %d/%q, want 200/empty", rec.Code, rec.Body.String())
	}
	if etag := rec.Header().Get("ETag"); !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("weak etag = %q", etag)
	}
}

func TestETagMiddlewareSkipsLargeAndErrorResponses(t *testing.T) {
	s := MustNewServer(Config{})
	mw := ETagMiddleware(ETagConfig{MaxBodyBytes: 4})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/large", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "large") }}, WithMiddlewares(mw))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/error", Handler: func(ctx *Context) { ctx.String(http.StatusInternalServerError, "boom") }}, WithMiddlewares(mw))
	for _, path := range []string{"/large", "/error"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Header().Get("ETag") != "" {
			t.Fatalf("%s unexpectedly got ETag %q", path, rec.Header().Get("ETag"))
		}
	}
}
