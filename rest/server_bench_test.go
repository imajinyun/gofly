package rest

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func benchDiscardSlog() func() {
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return func() { slog.SetDefault(old) }
}

// BenchmarkServerRouteDispatch measures the hot path of routing a request
// through the mux to a registered handler (no middleware).
func BenchmarkServerRouteDispatch(b *testing.B) {
	restore := benchDiscardSlog()
	defer restore()

	s := MustNewServer(Config{})
	s.AddRoute(Route{
		Method:  http.MethodGet,
		Path:    "/users/{id}",
		Handler: func(ctx *Context) { ctx.String(http.StatusOK, ctx.PathValue("id")) },
	})
	handler := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

// BenchmarkServerRouteDispatchJSON measures routing plus JSON encoding, which
// is the most common response path for real services.
func BenchmarkServerRouteDispatchJSON(b *testing.B) {
	restore := benchDiscardSlog()
	defer restore()

	s := MustNewServer(Config{})
	s.AddRoute(Route{
		Method: http.MethodGet,
		Path:   "/users/{id}",
		Handler: func(ctx *Context) {
			ctx.JSON(http.StatusOK, map[string]string{"id": ctx.PathValue("id"), "name": "demo"})
		},
	})
	handler := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}
