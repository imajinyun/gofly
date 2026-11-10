package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/navigation"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func TestRegisterRoutesAdminSessionNavigationContract(t *testing.T) {
	tests := []struct {
		name       string
		tokenRealm authn.Realm
		withToken  bool
		wantStatus int
		wantCalls  int
	}{
		{name: "missing bearer token is rejected", wantStatus: http.StatusUnauthorized},
		{name: "front token cannot read admin navigation", tokenRealm: authn.RealmFront, withToken: true, wantStatus: http.StatusUnauthorized},
		{name: "admin token receives navigation projection", tokenRealm: authn.RealmAdmin, withToken: true, wantStatus: http.StatusOK, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			navigationService := &routeNavigationService{response: navigation.SessionProjection{
				Subject:  "admin-demo",
				TenantID: "1001",
				Realm:    "admin",
				Navigation: []navigation.MenuItem{{
					ID: "workspace.projects", Kind: navigation.MenuKindPage, Title: "Projects", Route: "/projects", Component: "gosky.projects.list",
				}},
			}}
			serviceContext := svc.NewServiceContext(config.Config{})
			serviceContext.SetAuthenticationService(&routeAuthenticationService{})
			serviceContext.SetNavigationService(navigationService)
			server := rest.MustNewServer(rest.Config{})
			RegisterRoutes(server, serviceContext)

			req := httptest.NewRequest(http.MethodGet, "/api/admin/auth/session", nil)
			if tt.withToken {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(sessionNavigationToken(t, tt.tokenRealm)))
			}
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if navigationService.calls != tt.wantCalls {
				t.Fatalf("navigation calls = %d, want %d", navigationService.calls, tt.wantCalls)
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			var response navigation.SessionProjection
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Subject != "admin-demo" || !sameNavigationMenuIDs(response.Navigation, []string{"workspace.projects"}) {
				t.Fatalf("navigation response = %#v", response)
			}
		})
	}
}

func TestRegisterRoutesAdminSessionNavigationUnavailable(t *testing.T) {
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&routeAuthenticationService{})
	serviceContext.SetNavigationService(&routeNavigationService{err: coreerrors.New(coreerrors.CodeUnavailable, "authorization policy is unavailable")})
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/auth/session", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(sessionNavigationToken(t, authn.RealmAdmin)))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestRegisterRoutesAdminSessionNavigationUsesCasbinProjection(t *testing.T) {
	authorizer := projectAuthorizationAuthorizer(t)
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&routeAuthenticationService{})
	serviceContext.SetAuthorizer(authorizer)
	serviceContext.SetNavigationService(navigation.NewAdminService(authorizer))
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/auth/session", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(sessionNavigationTokenForSubject(t, "reader-demo", authn.RealmAdmin)))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "project.read") || strings.Contains(rec.Body.String(), "reader-demo, reader") {
		t.Fatalf("navigation response leaks authorization internals: %s", rec.Body.String())
	}
	var response navigation.SessionProjection
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !sameNavigationMenuIDs(response.Navigation, []string{"workspace.projects"}) || len(response.Points) != 0 {
		t.Fatalf("reader projection = %#v", response)
	}
}

type routeNavigationService struct {
	response navigation.SessionProjection
	err      *coreerrors.Error
	calls    int
}

func (s *routeNavigationService) Project(_ context.Context, principal authn.RequestPrincipal) (navigation.SessionProjection, *coreerrors.Error) {
	s.calls++
	if principal.Subject != "admin-demo" || principal.TenantID != 1001 || principal.Realm != authn.RealmAdmin || principal.SessionID != "session-navigation" {
		return navigation.SessionProjection{}, coreerrors.New(coreerrors.CodeUnauthenticated, "unexpected request principal")
	}
	return s.response, s.err
}

func sessionNavigationToken(t *testing.T, realm authn.Realm) string {
	return sessionNavigationTokenForSubject(t, "admin-demo", realm)
}

func sessionNavigationTokenForSubject(t *testing.T, subject string, realm authn.Realm) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   subject,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra: map[string]any{
			"tenant_id": "1001",
			"realm":     string(realm),
			"sid":       "session-navigation",
		},
	}, []byte("route-authentication-service-secret-at-least-32-bytes"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func sameNavigationMenuIDs(items []navigation.MenuItem, want []string) bool {
	if len(items) != len(want) {
		return false
	}
	for index := range items {
		if items[index].ID != want[index] {
			return false
		}
	}
	return true
}
