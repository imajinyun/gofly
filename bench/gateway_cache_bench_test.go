package bench

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	goflycache "github.com/imajinyun/gofly/cache"
	"github.com/imajinyun/gofly/gateway"
)

func BenchmarkGatewayProxy(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	g, err := gateway.New([]gateway.Route{{
		Method:     http.MethodGet,
		PathPrefix: "/api",
		Targets:    []string{upstream.URL},
	}})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	}
}

func BenchmarkCacheHotPath(b *testing.B) {
	c := goflycache.New[string](goflycache.WithName[string]("bench-hot-path"))
	c.Set("key", "value")

	b.ReportAllocs()
	for b.Loop() {
		v, ok := c.Get("key")
		if !ok || v != "value" {
			b.Fatal("unexpected cache miss or value")
		}
	}
}

func BenchmarkCacheHotPathGetOrLoadHit(b *testing.B) {
	c := goflycache.New[string](
		goflycache.WithName[string]("bench-hot-path-loader"),
		goflycache.WithLoader(func(context.Context, string) (string, error) {
			return "loaded", nil
		}),
	)
	if _, err := c.GetOrLoad(context.Background(), "key"); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		v, err := c.GetOrLoad(context.Background(), "key")
		if err != nil || v != "loaded" {
			b.Fatalf("unexpected cache value=%q err=%v", v, err)
		}
	}
}
