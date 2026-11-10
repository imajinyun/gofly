package routes

import (
	"bytes"
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

func TestRegisterRoutesAuthenticationPublicDomainContract(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantRealm authn.Realm
	}{
		{name: "admin login is explicitly public", path: "/api/admin/auth/login", wantRealm: authn.RealmAdmin},
		{name: "front login is explicitly public", path: "/api/front/auth/login", wantRealm: authn.RealmFront},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &routeAuthenticationService{}
			serviceContext := svc.NewServiceContext(config.Config{})
			serviceContext.SetAuthenticationService(service)
			server := rest.MustNewServer(rest.Config{})
			RegisterRoutes(server, serviceContext)

			body := bytes.NewBufferString(`{"loginName":"operator","tenantCode":"acme","password":"correct-password"}`)
			req := httptest.NewRequest(http.MethodPost, tt.path, body)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			if service.login.Realm != tt.wantRealm || service.login.LoginName != "operator" || service.login.TenantCode != "acme" || service.login.Password != "correct-password" {
				t.Fatalf("login input = %#v, want realm %q and request credentials", service.login, tt.wantRealm)
			}
			var response authn.TokenPair
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode login response: %v", err)
			}
			if response.AccessToken != "access-token" || response.RefreshToken != "refresh-token" || response.TokenType != "Bearer" {
				t.Fatalf("login response = %#v", response)
			}
		})
	}
}

type routeAuthenticationService struct {
	login authn.LoginInput
}

func (s *routeAuthenticationService) Login(_ context.Context, input authn.LoginInput) (authn.TokenPair, error) {
	s.login = input
	return authn.TokenPair{AccessToken: "access-token", RefreshToken: "refresh-token", TokenType: "Bearer", ExpiresIn: 900}, nil
}

func (*routeAuthenticationService) Refresh(context.Context, authn.RefreshInput) (authn.TokenPair, error) {
	return authn.TokenPair{AccessToken: "access-token", RefreshToken: "refresh-token", TokenType: "Bearer", ExpiresIn: 900}, nil
}

func (*routeAuthenticationService) Logout(context.Context, authn.LogoutInput) error { return nil }

func (*routeAuthenticationService) ValidatePrincipal(context.Context, authn.RequestPrincipal) error {
	return nil
}

func (*routeAuthenticationService) JWTValidator() auth.Validator {
	return auth.JWTValidator([]byte("route-authentication-service-secret-at-least-32-bytes"), auth.JWTOptions{Now: time.Now})
}
