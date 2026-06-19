package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIVersionMiddlewareHeaderQueryPathAndDefault(t *testing.T) {
	s := MustNewServer(Config{})
	mw := APIVersionMiddleware(APIVersionConfig{Default: "v1", Supported: []string{"v1", "v2", "v3"}, PathPrefix: true})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/resource", Handler: func(ctx *Context) { ctx.String(http.StatusOK, ctx.APIVersion()) }}, WithMiddlewares(mw))
	s.AddRoute(Route{Method: http.MethodGet, Path: "/v3/resource", Handler: func(ctx *Context) { ctx.String(http.StatusOK, ctx.APIVersion()) }}, WithMiddlewares(mw))
	tests := []struct {
		name string
		path string
		head string
		want string
	}{
		{name: "default", path: "/resource", want: "v1"},
		{name: "header", path: "/resource", head: "v2", want: "v2"},
		{name: "query", path: "/resource?version=v2", want: "v2"},
		{name: "path", path: "/v3/resource", want: "v3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.head != "" {
				req.Header.Set(DefaultAPIVersionHeader, tt.head)
			}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || rec.Body.String() != tt.want {
				t.Fatalf("status/body = %d/%q, want 200/%q", rec.Code, rec.Body.String(), tt.want)
			}
			if got := rec.Header().Get(DefaultAPIVersionHeader); got != tt.want {
				t.Fatalf("response version header = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIVersionMiddlewareRejectsUnsupported(t *testing.T) {
	s := MustNewServer(Config{})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/resource", Handler: func(ctx *Context) { ctx.String(http.StatusOK, "ok") }}, WithMiddlewares(APIVersionMiddleware(APIVersionConfig{Supported: []string{"v1"}})))
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set(DefaultAPIVersionHeader, "v9")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
