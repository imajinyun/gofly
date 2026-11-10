package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory:
//   - only the verified admin principal can enumerate or revoke its sessions;
//   - session management returns no bearer or refresh credential material;
//   - front-realm tokens and front session-management paths are rejected.
func TestRegisterRoutesAdminSessionManagementContract(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name       string
		method     string
		path       string
		realm      authn.Realm
		withToken  bool
		wantStatus int
		wantList   bool
		wantRevoke bool
	}{
		{name: "missing credentials", method: http.MethodGet, path: "/api/admin/auth/sessions", wantStatus: http.StatusUnauthorized},
		{name: "front token is rejected", method: http.MethodGet, path: "/api/admin/auth/sessions", realm: authn.RealmFront, withToken: true, wantStatus: http.StatusUnauthorized},
		{name: "admin lists its active sessions", method: http.MethodGet, path: "/api/admin/auth/sessions?limit=10", realm: authn.RealmAdmin, withToken: true, wantStatus: http.StatusOK, wantList: true},
		{name: "invalid list limit is rejected", method: http.MethodGet, path: "/api/admin/auth/sessions?limit=0", realm: authn.RealmAdmin, withToken: true, wantStatus: http.StatusBadRequest},
		{name: "admin revokes its other session", method: http.MethodDelete, path: "/api/admin/auth/sessions/0123456789abcdef0123456789abcdef", realm: authn.RealmAdmin, withToken: true, wantStatus: http.StatusOK, wantRevoke: true},
		{name: "front session route is absent", method: http.MethodGet, path: "/api/front/auth/sessions", realm: authn.RealmFront, withToken: true, wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authenticationService := &routeAuthenticationService{}
			sessionService := &routeAdminSessionService{}
			serviceContext := svc.NewServiceContext(config.Config{})
			serviceContext.SetAuthenticationService(authenticationService)
			serviceContext.SetAdminSessionService(sessionService)
			server := rest.MustNewServer(rest.Config{})
			RegisterRoutes(server, serviceContext)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set(rest.RequestIDHeader, "admin-session-contract")
			if tt.withToken {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(routeAdminSessionToken(t, now, tt.realm)))
			}
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantList {
				if sessionService.list.Principal.Subject != "admin-session-user" || sessionService.list.Principal.TenantID != 1001 || sessionService.list.Principal.Realm != authn.RealmAdmin || sessionService.list.Limit != 10 {
					t.Fatalf("list input = %#v", sessionService.list)
				}
				var response authn.SessionListResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode session list: %v", err)
				}
				if len(response.Items) != 1 || response.Items[0].SessionID != "0123456789abcdef0123456789abcdef" || !response.Items[0].Current {
					t.Fatalf("session list = %#v", response)
				}
				if rec.Body.String() == "" || containsCredentialField(rec.Body.String()) {
					t.Fatalf("session response exposes credential material: %s", rec.Body.String())
				}
			}
			if tt.wantRevoke {
				if sessionService.revoke.Principal.Subject != "admin-session-user" || sessionService.revoke.SessionID != "0123456789abcdef0123456789abcdef" {
					t.Fatalf("revoke input = %#v", sessionService.revoke)
				}
				var response authn.SessionRevokeResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode session revoke: %v", err)
				}
				if !response.Changed {
					t.Fatalf("session revoke response = %#v", response)
				}
			}
			if !tt.wantList && sessionService.list.Principal.Subject != "" {
				t.Fatalf("unexpected session list input = %#v", sessionService.list)
			}
			if !tt.wantRevoke && sessionService.revoke.Principal.Subject != "" {
				t.Fatalf("unexpected session revoke input = %#v", sessionService.revoke)
			}
		})
	}
}

func routeAdminSessionToken(t *testing.T, now time.Time, realm authn.Realm) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "admin-session-user",
		IssuedAt:  now.Add(-time.Minute).Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
		Extra: map[string]any{
			"tenant_id": "1001",
			"realm":     string(realm),
			"sid":       "fedcba9876543210fedcba9876543210",
		},
	}, []byte("route-authentication-service-secret-at-least-32-bytes"))
	if err != nil {
		t.Fatalf("sign admin session token: %v", err)
	}
	return token
}

func containsCredentialField(value string) bool {
	for _, field := range []string{"accessToken", "refreshToken", "refresh_token_hash", "password"} {
		if contains(value, field) {
			return true
		}
	}
	return false
}

func contains(value, fragment string) bool {
	return len(fragment) > 0 && len(value) >= len(fragment) && (value == fragment || containsAt(value, fragment))
}

func containsAt(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

type routeAdminSessionService struct {
	list   authn.SessionListInput
	revoke authn.SessionRevokeInput
}

func (s *routeAdminSessionService) ListSessions(_ context.Context, input authn.SessionListInput) (authn.SessionListResponse, error) {
	s.list = input
	return authn.SessionListResponse{Items: []authn.SessionSummary{{SessionID: "0123456789abcdef0123456789abcdef", Current: true, IssuedAt: time.Unix(1_791_179_000, 0).UTC(), LastSeenAt: time.Unix(1_791_179_100, 0).UTC(), ExpiresAt: time.Unix(1_791_266_000, 0).UTC()}}}, nil
}

func (s *routeAdminSessionService) RevokeSession(_ context.Context, input authn.SessionRevokeInput) (authn.SessionRevokeResponse, error) {
	s.revoke = input
	return authn.SessionRevokeResponse{Changed: true}, nil
}

var _ authn.AdminSessionService = (*routeAdminSessionService)(nil)
