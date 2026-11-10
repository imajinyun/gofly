package smartweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory for the SmartAdmin-compatible auth boundary:
//
//  1. SW-AUTH-01: no Authorization header -> 401 with numeric code 30007 so
//     the unchanged axios interceptor sends the user to the login page
//     instead of showing a generic error toast.
//  2. SW-AUTH-02: malformed bearer token -> 401 with numeric code 30008
//     (expired/invalid session), same forced-logout behavior.
//  3. SW-AUTH-03: rejected token must never reach the protected handler.
//  4. SW-AUTH-04: a valid token establishes the realm principal in context;
//     handlers read it through authn.PrincipalFromContext exactly as they do
//     behind the standard BearerAuthMiddleware.
//  5. SW-AUTH-05: the 401 body is JSON and carries the numeric code at the
//     top level the frontend reads (res.code), not the rest string envelope.
func TestAuthMiddlewareEnforcesSmartAdminSessionCodes(t *testing.T) {
	rejecting := func(context.Context, string) (context.Context, error) {
		return context.Background(), auth.ErrInvalidCredentials
	}
	validValidator := func(_ context.Context, token string) (context.Context, error) {
		if token != "good-token" {
			return context.Background(), auth.ErrInvalidCredentials
		}
		return auth.NewContext(context.Background(), auth.Principal{
			Subject: "user:admin",
			Claims: map[string]any{
				"tenant_id": uint64(1001),
				"realm":     string(authn.RealmAdmin),
				"sid":       "session-1001",
			},
		}), nil
	}
	// Failure cases use an always-rejecting validator; the valid case uses
	// a fully claimed principal the admin interceptor accepts.
	middleware := AuthMiddleware(authn.NewAdminInterceptor(auth.Validator(rejecting)))
	validMiddleware := AuthMiddleware(authn.NewAdminInterceptor(auth.Validator(validValidator)))

	protectedCalled := false
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protectedCalled = true
		principal, ok := authn.PrincipalFromContext(r.Context())
		if !ok {
			t.Error("principal missing after compat auth middleware")
			http.Error(w, "no principal", http.StatusInternalServerError)
			return
		}
		if principal.Subject != "user:admin" || principal.Realm != authn.RealmAdmin {
			t.Errorf("principal = %#v", principal)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := middleware(protected)

	for _, tc := range []struct {
		name     string
		header   string
		wantCode int
	}{
		{name: "missing token is code 30007", header: "", wantCode: 30007},
		{name: "invalid token is code 30008", header: "Bearer bad-token", wantCode: 30008},
		{name: "non bearer scheme is code 30007", header: "Basic dXNlcjpwYXNz", wantCode: 30007},
	} {
		t.Run(tc.name, func(t *testing.T) {
			protectedCalled = false
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/role/getAll", nil)
			if tc.header != "" {
				request.Header.Set(auth.AuthorizationHeader, tc.header)
			}
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", recorder.Code, recorder.Body.String())
			}
			if protectedCalled {
				t.Fatal("protected handler ran for an unauthenticated request")
			}
			var body struct {
				Code int    `json:"code"`
				Msg  string `json:"msg"`
				OK   bool   `json:"ok"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode 401 body: %v; raw=%s", err, recorder.Body.String())
			}
			if body.Code != tc.wantCode || body.OK || body.Msg == "" {
				t.Fatalf("401 body = %#v, want code=%d ok=false with message", body, tc.wantCode)
			}
		})
	}

	t.Run("valid token reaches handler with admin principal", func(t *testing.T) {
		protectedCalled = false
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/role/getAll", nil)
		request.Header.Set(auth.AuthorizationHeader, "Bearer good-token")
		validMiddleware(protected).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent || !protectedCalled {
			t.Fatalf("valid request status=%d called=%v body=%s", recorder.Code, protectedCalled, recorder.Body.String())
		}
	})
}

// TestAuthMiddlewareType guards the route wiring contract: the compat
// middleware must be assignable as a rest route/group middleware.
func TestAuthMiddlewareType(t *testing.T) {
	var _ rest.Middleware = AuthMiddleware(authn.Interceptor{})
	var _ context.Context
}
