package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func TestRegisterRoutes(t *testing.T) {
	server := rest.MustNewServer(rest.Config{Middlewares: rest.MiddlewaresConfig{Health: true, Metrics: true}})
	RegisterRoutes(server, svc.NewServiceContext(config.Config{}))
	tests := []struct {
		name string
		path string
	}{
		{name: "ping", path: "/api/v1/ping?name=%20gofly%20"},
		{name: "health", path: "/healthz"},
		{name: "metrics", path: "/metrics"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestRegisterRoutesUsesTrimMiddleware(t *testing.T) {
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, svc.NewServiceContext(config.Config{}))
	server.AddRoute(rest.Route{Method: http.MethodPost, Path: "/echo", Handler: func(ctx *rest.Context) {
		var req struct {
			Name string `json:"name"`
		}
		if err := ctx.Bind(&req); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, req)
	}})
	req := httptest.NewRequest(http.MethodPost, "/echo?name=%20gofly%20", strings.NewReader(`{"name":"  gofly  "}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Name != "gofly" {
		t.Fatalf("name = %q, want gofly", resp.Name)
	}
}
