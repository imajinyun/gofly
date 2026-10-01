package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/rest"
)

func TestTrimSpaceMiddleware(t *testing.T) {
	server := rest.MustNewServer(rest.Config{})
	server.Use(TrimSpaceMiddleware())
	server.AddRoute(rest.Route{Method: http.MethodPost, Path: "/trim", Handler: func(ctx *rest.Context) {
		var req struct {
			Name   string   `json:"name"`
			Tags   []string `json:"tags"`
			Nested struct {
				Value string `json:"value"`
			} `json:"nested"`
		}
		if err := ctx.Bind(&req); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]any{
			"query":  ctx.Query("q"),
			"name":   req.Name,
			"tag":    req.Tags[0],
			"nested": req.Nested.Value,
		})
	}})
	req := httptest.NewRequest(http.MethodPost, "/trim?q=%20hello%20", strings.NewReader(` {"name":"  gofly  ","tags":["  rpc  "],"nested":{"value":"  ok  "}} `))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%q", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"query": "hello", "name": "gofly", "tag": "rpc", "nested": "ok"} {
		if got[key] != want {
			t.Fatalf("%s = %q, want %q", key, got[key], want)
		}
	}
}
