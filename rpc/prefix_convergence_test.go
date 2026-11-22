package rpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreendpoint "github.com/imajinyun/gofly/core/endpoint"
)

// Failure modes for HTTP-RPC admin convergence:
//  1. Legacy /rpc/admin/* requests must no longer reach the admin handler so
//     the operational namespace is fully behind /gofly.
//  2. Canonical /gofly/rpc/admin/* requests must keep working with the same
//     bearer-token protection and JSON envelopes.
//  3. Business RPC dispatch (/rpc/<service>/<method>) must remain untouched,
//     since only administrative paths are being namespaced.
func TestRPCAdminConvergesUnderGoflyPrefix(t *testing.T) {
	s := NewServer(WithServerAdminToken("secret"))

	legacy := httptest.NewRecorder()
	s.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy /rpc/admin/state status = %d, want %d", legacy.Code, http.StatusNotFound)
	}

	canonical := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, coreendpoint.RPCAdminPrefix+"/state", nil)
	req.Header.Set("Authorization", "Bearer secret")
	s.ServeHTTP(canonical, req)
	if canonical.Code != http.StatusOK {
		t.Fatalf("canonical admin state status = %d, want %d body=%q", canonical.Code, http.StatusOK, canonical.Body.String())
	}

	descriptors := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, coreendpoint.RPCAdminPrefix+"/descriptors", nil)
	req.Header.Set("Authorization", "Bearer secret")
	s.ServeHTTP(descriptors, req)
	if descriptors.Code != http.StatusOK {
		t.Fatalf("canonical admin descriptors status = %d, want %d", descriptors.Code, http.StatusOK)
	}

	// Non-admin RPC traffic still uses the /rpc/ business namespace.
	business := httptest.NewRecorder()
	businessReq := httptest.NewRequest(http.MethodPost, "/rpc/unknown/method", strings.NewReader("{}"))
	s.ServeHTTP(business, businessReq)
	if !strings.Contains(business.Body.String(), "method not found") {
		t.Fatalf("business RPC dispatch body = %q, want governed method-not-found envelope", business.Body.String())
	}
}
