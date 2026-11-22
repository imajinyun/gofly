package grpc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	coreendpoint "github.com/imajinyun/gofly/core/endpoint"
)

// Failure modes for the gRPC-side admin mux convergence:
//  1. Legacy root-level probes/metrics must return 404 so they cannot be
//     scraped outside the shared /gofly namespace.
//  2. Canonical /gofly probe and metrics paths must keep serving.
func TestAdminMuxConvergesUnderGoflyPrefix(t *testing.T) {
	handler := newAdminServer("", nil, nil, nil).Handler

	legacy := []string{"/healthz", "/readyz", "/startupz", "/metrics", "/runtime"}
	for _, path := range legacy {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("legacy %s status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}

	canonical := []string{coreendpoint.HealthzPath, coreendpoint.ReadyzPath, coreendpoint.StartupzPath, coreendpoint.MetricsPath, coreendpoint.AdminPrefix + "/runtime"}
	for _, path := range canonical {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("canonical %s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
	}
}
