package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAIGovernedServiceStateAndControlPlane_BitsUT(t *testing.T) {
	server := buildAIGovernedServer(0, "test-token")

	state := httptest.NewRecorder()
	server.Handler().ServeHTTP(state, httptest.NewRequest(http.MethodGet, "/v1/state", nil))
	if state.Code != http.StatusOK {
		t.Fatalf("state status = %d body=%s, want 200", state.Code, state.Body.String())
	}
	if got := state.Header().Get("X-Gofly-Governed"); got != "true" {
		t.Fatalf("governance header = %q, want true", got)
	}

	cp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/control-plane", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	server.Handler().ServeHTTP(cp, req)
	if cp.Code != http.StatusOK {
		t.Fatalf("control-plane status = %d body=%s, want 200", cp.Code, cp.Body.String())
	}
	var snapshot map[string]any
	if err := json.NewDecoder(cp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode control-plane: %v", err)
	}
	if snapshot["metadata"] == nil || snapshot["checksum"] == nil {
		t.Fatalf("snapshot = %#v, want metadata and checksum", snapshot)
	}
}

func TestExpectedControlPlaneContract_BitsUT(t *testing.T) {
	contract := expectedControlPlaneContract()
	if contract["service"] != "ai-governed-service" || contract["adminPath"] != "/admin/control-plane" {
		t.Fatalf("contract = %#v, want service and admin path", contract)
	}
}
