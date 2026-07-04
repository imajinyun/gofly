package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallUserRPCAndSchema(t *testing.T) {
	resp, err := callUserRPC(context.Background(), "42")
	if err != nil {
		t.Fatalf("callUserRPC: %v", err)
	}
	if resp["id"] != "42" || resp["name"] != "demo-user" || resp["trace_id"] == "" {
		t.Fatalf("rpc response = %#v, want id/name/trace", resp)
	}
	schema := userSchema()
	if schema.Type != "object" || schema.Properties["id"].Type != "string" || len(schema.Required) != 2 {
		t.Fatalf("user schema = %#v, want object with id/name required", schema)
	}
}

func TestNewObservabilityDemoHandlers(t *testing.T) {
	admin, srv := newObservabilityDemo()
	if admin == nil || admin.Handler == nil || srv == nil {
		t.Fatalf("newObservabilityDemo returned admin=%v srv=%v, want handlers", admin, srv)
	}

	user := httptest.NewRecorder()
	srv.Handler().ServeHTTP(user, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	if user.Code != http.StatusOK || !strings.Contains(user.Body.String(), `"id":"42"`) {
		t.Fatalf("GET /users/42 = %d %s, want user response", user.Code, user.Body.String())
	}

	metricsJSON := httptest.NewRecorder()
	admin.Handler.ServeHTTP(metricsJSON, httptest.NewRequest(http.MethodGet, "/debug/metrics.json", nil))
	if metricsJSON.Code != http.StatusOK || !strings.Contains(metricsJSON.Body.String(), "requests") {
		t.Fatalf("GET /debug/metrics.json = %d %s, want metrics json", metricsJSON.Code, metricsJSON.Body.String())
	}

	health := httptest.NewRecorder()
	admin.Handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/debug/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("GET /debug/healthz = %d %s, want 200", health.Code, health.Body.String())
	}
}
