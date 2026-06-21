package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceByNameAndPrimaryPath_BitsUT(t *testing.T) {
	spec, ok := serviceByName("gateway")
	if !ok || primaryPath(spec) != "/v1/checkout" {
		t.Fatalf("gateway spec = %#v ok=%v, want /v1/checkout", spec, ok)
	}
	if _, ok := serviceByName("missing"); ok {
		t.Fatal("missing service resolved, want false")
	}
}

func TestBuildMicroshopServerExposesRouteAndControlPlane_BitsUT(t *testing.T) {
	spec, _ := serviceByName("users")
	server := buildMicroshopServer(spec, 0)

	route := httptest.NewRecorder()
	server.Handler().ServeHTTP(route, httptest.NewRequest(http.MethodGet, "/v1/users/u-1001", nil))
	if route.Code != http.StatusOK {
		t.Fatalf("route status = %d body=%s, want 200", route.Code, route.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(route.Body).Decode(&body); err != nil {
		t.Fatalf("decode route body: %v", err)
	}
	if body["service"] != "users" {
		t.Fatalf("service = %v, want users", body["service"])
	}

	cp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/control-plane", nil)
	req.Header.Set("Authorization", "Bearer microshop-token")
	server.Handler().ServeHTTP(cp, req)
	if cp.Code != http.StatusOK {
		t.Fatalf("control-plane status = %d body=%s, want 200", cp.Code, cp.Body.String())
	}
}
