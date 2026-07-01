package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkGatewayHTTPProxy(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	g, err := New([]Route{{
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
