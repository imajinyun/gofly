package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/rest"
)

const realmInterceptorTestSecret = "gosky-realm-interceptor-secret-at-least-32-bytes"

func TestRealmInterceptor_ProtectsBusinessRouteDomains(t *testing.T) {
	now := time.Unix(1_791_180_000, 0)
	tests := []struct {
		name        string
		interceptor Interceptor
		tokenRealm  string
		withToken   bool
		wantStatus  int
		wantCalled  bool
		wantRealm   Realm
	}{
		{
			name:        "admin token reaches admin route",
			interceptor: NewAdminInterceptor(realmValidator(now)),
			tokenRealm:  string(RealmAdmin),
			withToken:   true,
			wantStatus:  http.StatusNoContent,
			wantCalled:  true,
			wantRealm:   RealmAdmin,
		},
		{
			name:        "front token is rejected by admin route",
			interceptor: NewAdminInterceptor(realmValidator(now)),
			tokenRealm:  string(RealmFront),
			withToken:   true,
			wantStatus:  http.StatusUnauthorized,
			wantCalled:  false,
		},
		{
			name:        "missing token is rejected by front route",
			interceptor: NewFrontInterceptor(realmValidator(now)),
			withToken:   false,
			wantStatus:  http.StatusUnauthorized,
			wantCalled:  false,
		},
		{
			name:        "front token reaches front route",
			interceptor: NewFrontInterceptor(realmValidator(now)),
			tokenRealm:  string(RealmFront),
			withToken:   true,
			wantStatus:  http.StatusNoContent,
			wantCalled:  true,
			wantRealm:   RealmFront,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := rest.MustNewServer(rest.Config{Middlewares: rest.MiddlewaresConfig{Health: true}})
			called := false
			server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/protected", Handler: func(ctx *rest.Context) {
				called = true
				principal, ok := PrincipalFromContext(ctx.Request.Context())
				if !ok {
					t.Fatal("request principal is missing")
				}
				if principal.Realm != tt.wantRealm || principal.Subject != "user:42" || principal.TenantID != 1001 {
					t.Fatalf("principal = %#v, want subject user:42 tenant 1001 realm %q", principal, tt.wantRealm)
				}
				ctx.Response.WriteHeader(http.StatusNoContent)
			}}, rest.WithAuth(tt.interceptor.Validator()))

			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tt.withToken {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(realmToken(t, now, tt.tokenRealm)))
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if called != tt.wantCalled {
				t.Fatalf("handler called = %t, want %t", called, tt.wantCalled)
			}
		})
	}
}

func TestClassifyBusinessRouteDomain(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantDomain RouteDomain
		wantOK     bool
	}{
		{name: "admin path", path: "/api/admin/projects", wantDomain: RouteDomainAdmin, wantOK: true},
		{name: "front path", path: "/api/front/profile", wantDomain: RouteDomainFront, wantOK: true},
		{name: "public path", path: "/api/public/ping", wantDomain: RouteDomainPublic, wantOK: true},
		{name: "admin word is not a segment", path: "/api/adminish/projects", wantOK: false},
		{name: "front route may contain later admin segment", path: "/api/front/admin-preview", wantDomain: RouteDomainFront, wantOK: true},
		{name: "infrastructure control plane is not business admin", path: "/admin/control-plane", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDomain, gotOK := ClassifyBusinessRouteDomain(tt.path)
			if gotDomain != tt.wantDomain || gotOK != tt.wantOK {
				t.Fatalf("ClassifyBusinessRouteDomain(%q) = (%q, %t), want (%q, %t)", tt.path, gotDomain, gotOK, tt.wantDomain, tt.wantOK)
			}
		})
	}
}

func TestRealmInterceptor_RejectsRevokedSessionAfterJWTVerification(t *testing.T) {
	now := time.Unix(1_791_180_000, 0)
	server := rest.MustNewServer(rest.Config{})
	called := false
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/protected", Handler: func(ctx *rest.Context) {
		called = true
		ctx.Response.WriteHeader(http.StatusNoContent)
	}}, rest.WithAuth(NewAdminInterceptor(realmValidator(now), rejectingPrincipalValidator{}).Validator()))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(realmToken(t, now, string(RealmAdmin))))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusUnauthorized, recorder.Body.String())
	}
	if called {
		t.Fatal("handler must not run after stateful session rejection")
	}
}

type rejectingPrincipalValidator struct{}

func (rejectingPrincipalValidator) ValidatePrincipal(context.Context, RequestPrincipal) error {
	return errors.New("session revoked")
}

func realmValidator(now time.Time) auth.Validator {
	return auth.JWTValidator([]byte(realmInterceptorTestSecret), auth.JWTOptions{Issuer: "gosky", Audience: "gosky-web", Now: func() time.Time { return now }})
}

func realmToken(t *testing.T, now time.Time, realm string) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   "user:42",
		Issuer:    "gosky",
		Audience:  "gosky-web",
		IssuedAt:  now.Add(-time.Minute).Unix(),
		ExpiresAt: now.Add(time.Minute).Unix(),
		Extra: map[string]any{
			"tenant_id": "1001",
			"realm":     realm,
			"sid":       "session-42",
		},
	}, []byte(realmInterceptorTestSecret))
	if err != nil {
		t.Fatalf("sign realm token: %v", err)
	}
	return token
}
