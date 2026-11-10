//go:build integration

package authz_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/navigation"
	"github.com/imajinyun/gofly/examples/gosky/internal/routes"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"

	_ "github.com/go-sql-driver/mysql"
)

const (
	policyHTTPDomain = "8801"
	policyHTTPSecret = "gosky-policy-http-secret-that-is-at-least-32-bytes"
)

func TestMySQLPolicyManagementHTTPIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the MySQL policy HTTP integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL policy HTTP database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(nonce[:])
	tables, err := authz.NewMySQLPolicyTables("gosky_http_rules_"+suffix, "gosky_http_events_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{tables.Rules, "casbin_rules"}, {tables.Events, "casbin_policy_events"}} {
		if _, err := db.ExecContext(ctx, "CREATE TABLE `"+pair[0]+"` LIKE `"+pair[1]+"`"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := db.ExecContext(ctx, "DROP TABLE `"+pair[0]+"`"); err != nil {
				t.Error(err)
			}
		})
	}
	store, err := authz.NewMySQLPolicyStoreWithTables(db, tables)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := authz.PolicyActor{Subject: "bootstrap", Domain: policyHTTPDomain, RequestID: "bootstrap"}
	if err := store.BootstrapTenant(ctx, bootstrap, "tenant-admin"); err != nil {
		t.Fatalf("bootstrap tenant policy: %v", err)
	}

	modelPath := filepath.Join(t.TempDir(), "casbin_model.conf")
	if err := os.WriteFile(modelPath, []byte(policyHTTPModel), 0o600); err != nil {
		t.Fatalf("write policy model: %v", err)
	}
	authorizer, err := authz.NewMySQLAuthorizerWithTables(modelPath, db, tables, []byte(policyHTTPSecret))
	if err != nil {
		t.Fatalf("new MySQL authorizer: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthorizer(authorizer)
	serviceContext.SetAuthenticationService(policyHTTPAuthenticationService{})
	serviceContext.SetNavigationService(navigation.NewAdminService(authorizer))
	serviceContext.SetPolicyService(authz.NewPolicyService(store, authorizer))
	server := rest.MustNewServer(rest.Config{DisableDefaultMiddlewares: true})
	routes.RegisterRoutes(server, serviceContext)

	adminToken := signedPolicyHTTPToken(t, "tenant-admin", policyHTTPDomain)
	noGrantToken := signedPolicyHTTPToken(t, "no-grant", policyHTTPDomain)
	crossTenantToken := signedPolicyHTTPToken(t, "tenant-admin", "8802")
	if response := policyHTTPCall(t, server, http.MethodGet, "/api/admin/auth/session", adminToken, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "workspace.projects") || !strings.Contains(response.Body.String(), "access-control.roles") || strings.Contains(response.Body.String(), "authorization.role.manage") {
		t.Fatalf("admin session navigation = status:%d body:%s", response.Code, response.Body.String())
	}
	if response := policyHTTPCall(t, server, http.MethodPost, "/api/admin/authorization/roles", adminToken, `{"subject":"new-reader","role":"reader"}`); response.Code != http.StatusOK {
		t.Fatalf("grant role status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if response := policyHTTPCall(t, server, http.MethodGet, "/api/admin/authorization/roles", adminToken, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "new-reader") {
		t.Fatalf("list roles = status:%d body:%s, want new-reader", response.Code, response.Body.String())
	}
	if response := policyHTTPCall(t, server, http.MethodPost, "/api/admin/authorization/roles", noGrantToken, `{"subject":"blocked","role":"reader"}`); response.Code != http.StatusForbidden {
		t.Fatalf("no-grant role mutation status = %d, want 403; body=%s", response.Code, response.Body.String())
	}
	if response := policyHTTPCall(t, server, http.MethodPost, "/api/admin/authorization/roles", crossTenantToken, `{"subject":"blocked","role":"reader"}`); response.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant role mutation status = %d, want 403; body=%s", response.Code, response.Body.String())
	}
	if response := policyHTTPCall(t, server, http.MethodDelete, "/api/admin/authorization/roles", adminToken, `{"subject":"tenant-admin","role":"admin"}`); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "failed_precondition") {
		t.Fatalf("remove final admin status = %d, want 400 failed_precondition; body=%s", response.Code, response.Body.String())
	}
	if response := policyHTTPCall(t, server, http.MethodGet, "/api/admin/authorization/policy-events?limit=10", adminToken, ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "add_grouping") {
		t.Fatalf("list policy events = status:%d body:%s", response.Code, response.Body.String())
	}
	if response := policyHTTPCall(t, server, http.MethodGet, "/api/v1/authorization/roles", adminToken, ""); response.Code != http.StatusNotFound {
		t.Fatalf("retired policy route status = %d, want 404; body=%s", response.Code, response.Body.String())
	}
}

func policyHTTPCall(t *testing.T, server *rest.Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
	request.Header.Set(rest.RequestIDHeader, "policy-http-request")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func signedPolicyHTTPToken(t *testing.T, subject, domain string) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   subject,
		Issuer:    "gosky",
		Audience:  "gosky-web",
		IssuedAt:  time.Now().Add(-time.Minute).Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra: map[string]any{
			"tenant_id": domain,
			"realm":     string(authn.RealmAdmin),
			"sid":       "policy-http-session-" + subject,
		},
	}, []byte(policyHTTPSecret))
	if err != nil {
		t.Fatalf("sign policy HTTP token: %v", err)
	}
	return token
}

type policyHTTPAuthenticationService struct{}

func (policyHTTPAuthenticationService) Login(context.Context, authn.LoginInput) (authn.TokenPair, error) {
	return authn.TokenPair{}, authn.ErrAuthenticationRejected
}

func (policyHTTPAuthenticationService) Refresh(context.Context, authn.RefreshInput) (authn.TokenPair, error) {
	return authn.TokenPair{}, authn.ErrAuthenticationRejected
}

func (policyHTTPAuthenticationService) Logout(context.Context, authn.LogoutInput) error {
	return authn.ErrAuthenticationRejected
}

func (policyHTTPAuthenticationService) ValidatePrincipal(context.Context, authn.RequestPrincipal) error {
	return nil
}

func (policyHTTPAuthenticationService) JWTValidator() auth.Validator {
	return auth.JWTValidator([]byte(policyHTTPSecret), auth.JWTOptions{Issuer: "gosky", Audience: "gosky-web", Now: time.Now})
}

const policyHTTPModel = `[request_definition]
r = sub, dom, obj, act

[policy_definition]
p = sub, dom, obj, act

[role_definition]
g = _, _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub, r.dom) && r.dom == p.dom && keyMatch2(r.obj, p.obj) && regexMatch(r.act, p.act)
`
