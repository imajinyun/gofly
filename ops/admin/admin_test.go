package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofly/gofly/core/auth"
)

func TestCleanPathPrefix(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		fallback string
		want     string
	}{
		{name: "fallback", path: "  /  ", fallback: "/debug", want: "/debug"},
		{name: "adds leading slash", path: "admin/", fallback: "/debug", want: "/admin"},
		{name: "trims trailing slash", path: " /ops/ ", fallback: "/debug", want: "/ops"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanPathPrefix(tt.path, tt.fallback); got != tt.want {
				t.Fatalf("CleanPathPrefix(%q, %q) = %q, want %q", tt.path, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestWriteErrorAndMaskSensitiveMap(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusUnauthorized, "invalid token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "unauthorized" || payload.Error.Message != "invalid token" {
		t.Fatalf("error payload = %#v", payload)
	}

	masked := MaskSensitiveMap(map[string]string{"Authorization": "Bearer secret", "X-Trace": "abc", "api-key": "secret"})
	if masked["Authorization"] != "***" || masked["api-key"] != "***" || masked["X-Trace"] != "abc" {
		t.Fatalf("masked = %#v", masked)
	}
}

func TestAuthorizeBearerOrLocal(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		authHeader string
		remoteAddr string
		wantOK     bool
		wantStatus int
	}{
		{name: "local without token", remoteAddr: "127.0.0.1:1234", wantOK: true},
		{name: "remote without token", remoteAddr: "203.0.113.10:1234", wantOK: false, wantStatus: http.StatusForbidden},
		{name: "valid bearer token", token: "secret", authHeader: "Bearer secret", remoteAddr: "203.0.113.10:1234", wantOK: true},
		{name: "invalid bearer token", token: "secret", authHeader: "Bearer wrong", remoteAddr: "127.0.0.1:1234", wantOK: false, wantStatus: http.StatusUnauthorized},
		{name: "missing bearer token", token: "secret", remoteAddr: "127.0.0.1:1234", wantOK: false, wantStatus: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.authHeader != "" {
				req.Header.Set(auth.AuthorizationHeader, tt.authHeader)
			}
			rec := httptest.NewRecorder()

			got := AuthorizeBearerOrLocal(rec, req, tt.token, func(w http.ResponseWriter, status int, message string) {
				http.Error(w, message, status)
			})
			if got != tt.wantOK {
				t.Fatalf("AuthorizeBearerOrLocal() = %v, want %v", got, tt.wantOK)
			}
			if !tt.wantOK && rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestAuditMiddlewareRecordsAdminEvent(t *testing.T) {
	var got AuditEvent
	mw := AuditMiddleware("gateway", func(_ context.Context, event AuditEvent) {
		got = event
	})
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusAccepted, map[string]string{"ok": "true"})
	}))

	req := httptest.NewRequest(http.MethodPost, "/admin/routes", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if got.Component != "gateway" || got.Method != http.MethodPost || got.Path != "/admin/routes" || got.Status != http.StatusAccepted || got.Duration == "" {
		t.Fatalf("audit event = %#v, want populated admin event", got)
	}
}

func TestAuditMiddlewareNilSink(t *testing.T) {
	mw := AuditMiddleware("gateway", nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAuditNilSink(t *testing.T) {
	// Should not panic.
	Audit(context.Background(), nil, AuditEvent{Component: "test"})
}

func TestAuditZeroTime(t *testing.T) {
	var got AuditEvent
	Audit(context.Background(), func(_ context.Context, event AuditEvent) {
		got = event
	}, AuditEvent{Component: "test"})
	if got.At.IsZero() {
		t.Fatal("Audit did not set At")
	}
}

func TestSlogAuditSink(t *testing.T) {
	sink := SlogAuditSink(nil)
	if sink == nil {
		t.Fatal("SlogAuditSink(nil) should return non-nil sink")
	}
	// Should not panic when called.
	sink(context.Background(), AuditEvent{
		Component:  "test",
		Method:     "GET",
		Path:       "/",
		RemoteAddr: "127.0.0.1:1234",
		Status:     200,
		Duration:   "1ms",
	})
}

func TestErrorCode(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "bad_request"},
		{http.StatusUnauthorized, "unauthorized"},
		{http.StatusForbidden, "forbidden"},
		{http.StatusNotFound, "not_found"},
		{http.StatusConflict, "conflict"},
		{http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.StatusServiceUnavailable, "unavailable"},
		{http.StatusInternalServerError, "internal"},
		{http.StatusBadGateway, "internal"},
		{http.StatusTeapot, "error"},
		{999, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := ErrorCode(tt.status); got != tt.want {
				t.Fatalf("ErrorCode(%d) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestMaskSensitiveMapEmpty(t *testing.T) {
	if got := MaskSensitiveMap(nil); got != nil {
		t.Fatalf("MaskSensitiveMap(nil) = %v, want nil", got)
	}
	if got := MaskSensitiveMap(map[string]string{}); got != nil {
		t.Fatalf("MaskSensitiveMap(empty) = %v, want nil", got)
	}
}

func TestIsSensitiveKey(t *testing.T) {
	if !IsSensitiveKey("Authorization") {
		t.Fatal("Authorization should be sensitive")
	}
	if !IsSensitiveKey("x-api-key") {
		t.Fatal("x-api-key should be sensitive")
	}
	if IsSensitiveKey("trace-id") {
		t.Fatal("trace-id should not be sensitive")
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]string{"ok": "true"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestGovernanceEndpoints(t *testing.T) {
	endpoints := GovernanceEndpoints(WithGovernanceRoot(), WithGovernanceExplain())
	if len(endpoints) != 18 {
		t.Fatalf("len(GovernanceEndpoints(root, explain)) = %d, want 18", len(endpoints))
	}
	if endpoints[0] != (Endpoint{Method: http.MethodGet, Path: "/governance"}) {
		t.Fatalf("first endpoint = %#v, want governance root", endpoints[0])
	}
	last := endpoints[len(endpoints)-1]
	if last != (Endpoint{Method: http.MethodGet, Path: "/governance/explain"}) {
		t.Fatalf("last endpoint = %#v, want governance explain", last)
	}

	base := GovernanceEndpoints()
	hasRuntime := false
	for _, endpoint := range base {
		if endpoint.Path == "/governance" || endpoint.Path == "/governance/explain" {
			t.Fatalf("base GovernanceEndpoints should not include optional endpoint %#v", endpoint)
		}
		if endpoint == (Endpoint{Method: http.MethodGet, Path: "/governance/runtime"}) {
			hasRuntime = true
		}
	}
	if !hasRuntime {
		t.Fatal("base GovernanceEndpoints should include governance runtime endpoint")
	}
}
