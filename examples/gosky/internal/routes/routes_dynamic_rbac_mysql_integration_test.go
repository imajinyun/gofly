//go:build integration

package routes

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/model/repo"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// DYNA-001..022: dynamic RBAC end-to-end behavior against a real migrated
// MySQL database. Every test uses fresh nonce-suffixed tenants and cleans its
// own rows; the standard casbin tables are shared but scoped by domain.
type dynamicRBACFixture struct {
	t       *testing.T
	ctx     context.Context
	db      *sql.DB
	server  *httptest.Server
	suffix  string
	rbac    *repo.MySQLRBACRepository
	storage *storage.SQLStore
	tenants []dynamicTenant
}

type dynamicTenant struct {
	id   uint64
	code string
	// admin is the bootstrapped policy administrator subject/login.
	admin string
}

func newDynamicRBACFixture(t *testing.T) *dynamicRBACFixture {
	t.Helper()
	dsn := os.Getenv("GOSKY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for isolated MySQL dynamic RBAC E2E")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Skipf("dynamic RBAC MySQL is unavailable: %v", err)
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	f := &dynamicRBACFixture{
		t:       t,
		ctx:     ctx,
		db:      db,
		suffix:  hex.EncodeToString(nonce[:]),
		storage: storage.NewSQLStore(db),
	}
	tables := authz.DefaultMySQLPolicyTables()
	policyStore, err := authz.NewMySQLPolicyStoreWithTables(db, tables)
	if err != nil {
		t.Fatal(err)
	}
	f.rbac = repo.NewMySQLRBACRepository(f.storage, policyStore)
	policyStore.SetRoleDirectory(f.rbac)

	// Two fresh tenants, each with one bootstrapped administrator.
	for index := range 2 {
		tenant := dynamicTenant{
			code:  fmt.Sprintf("dyn-%s-%d", f.suffix, index),
			admin: fmt.Sprintf("dyn-admin-%s-%d", f.suffix, index),
		}
		result, err := db.ExecContext(ctx, "INSERT INTO tenants (tenant_code, tenant_name) VALUES (?, 'Dynamic RBAC E2E')", tenant.code)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		tenant.id = uint64(id)
		if err := f.rbac.EnsureTenantCatalog(ctx, tenant.id); err != nil {
			t.Fatal(err)
		}
		if err := repo.NewOrgRepository(f.storage, f.rbac).EnsureTenantOrganization(ctx, tenant.id); err != nil {
			t.Fatal(err)
		}
		if err := f.insertUser(tenant.admin, tenant.admin); err != nil {
			t.Fatal(err)
		}
		var userID uint64
		if err := db.QueryRowContext(ctx, "SELECT user_id FROM users WHERE subject = ?", tenant.admin).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO tenant_users (tenant_id, user_id, member_realm) VALUES (?, ?, 'both')", tenant.id, userID); err != nil {
			t.Fatal(err)
		}
		actor := authz.PolicyActor{Subject: "e2e-bootstrap", Domain: strconv.FormatUint(tenant.id, 10)}
		if err := policyStore.BootstrapTenant(ctx, actor, tenant.admin); err != nil {
			t.Fatal(err)
		}
		f.tenants = append(f.tenants, tenant)
	}
	t.Cleanup(f.cleanup)

	authorizer, err := authz.NewMySQLAuthorizerWithTables("../../etc/authorization/casbin_model.conf", db, tables, []byte(httpAuthenticationSecret))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(authorizer.Close)
	identity := repo.NewMySQLIdentityRepository(f.storage)
	authentication, err := authn.NewService(authn.ServiceOptions{
		Repository: identity, AccessSecret: []byte(httpAuthenticationSecret),
		AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := authz.NewPolicyService(policyStore, authorizer)
	roleService := app.NewRoleService(policy, identity, f.rbac)
	rbacService := app.NewRBACService(f.rbac, authorizer)
	userAdminRepository := repo.NewMySQLUserAdminRepository(f.storage)
	userAdminService := app.NewUserAdminService(userAdminRepository, f.rbac, authorizer)
	compatibility, err := smartweb.NewService(authentication, authorizer, nil, roleService)
	if err != nil {
		t.Fatal(err)
	}
	compatibility.SetDirectory(rbacService)
	orgRepository := repo.NewOrgRepository(f.storage, f.rbac)
	if err := orgRepository.EnsureAllTenants(f.ctx); err != nil {
		t.Fatal(err)
	}
	departmentService := app.NewDepartmentService(authorizer, orgRepository)
	positionService := app.NewPositionService(authorizer, orgRepository)
	employeeService := app.NewEmployeeService(authorizer, orgRepository, f.rbac, policy)
	compatibility.SetOrganization(app.NewOrganizationFacade(departmentService, positionService, employeeService))
	services := svc.NewServiceContext(config.Config{})
	services.SetAuthorizer(authorizer)
	services.SetAuthenticationService(authentication)
	services.SetAdminSessionService(authentication)
	services.SetPolicyService(policy)
	services.SetSmartWebService(compatibility)
	services.SetRBACService(rbacService)
	services.SetUserAdminService(userAdminService)
	services.SetProjectService(app.NewProjectService(repo.NewMySQLProjectRepository(f.storage), authorizer))
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, services)
	f.server = httptest.NewServer(server.Handler())
	t.Cleanup(f.server.Close)
	return f
}

func (f *dynamicRBACFixture) insertUser(subject, loginName string) error {
	_, err := f.db.ExecContext(f.ctx,
		"INSERT INTO users (subject, login_name, password_hash, account_type) VALUES (?, ?, ?, 'both')",
		subject, loginName, integrationPasswordHash("correct-password"))
	return err
}

func (f *dynamicRBACFixture) cleanup() {
	cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	for _, tenant := range f.tenants {
		id := strconv.FormatUint(tenant.id, 10)
		for _, query := range []string{
			"DELETE FROM casbin_policy_events WHERE domain = ?",
			"DELETE FROM casbin_rules WHERE domain = ?",
		} {
			if _, err := f.db.ExecContext(cleanupCtx, query, id); err != nil {
				f.t.Errorf("cleanup casbin %s: %v", id, err)
			}
		}
		for _, table := range []string{"role_menus", "menus", "roles", "admin_audit_logs", "auth_sessions", "auth_login_logs", "tenant_users", "departments", "positions"} {
			if _, err := f.db.ExecContext(cleanupCtx, "DELETE FROM "+table+" WHERE tenant_id = ?", tenant.id); err != nil {
				f.t.Errorf("cleanup %s tenant %d: %v", table, tenant.id, err)
			}
		}
		if _, err := f.db.ExecContext(cleanupCtx, "DELETE FROM tenants WHERE tenant_id = ?", tenant.id); err != nil {
			f.t.Errorf("cleanup tenant: %v", err)
		}
	}
	if _, err := f.db.ExecContext(cleanupCtx, "DELETE FROM users WHERE subject LIKE ?", "user:dyn-%"+f.suffix+"%"); err != nil {
		f.t.Errorf("cleanup users: %v", err)
	}
	_ = f.db.Close()
}

// do issues one JSON request and returns status and decoded body.
func (f *dynamicRBACFixture) do(method, path, token string, input any) (int, map[string]any) {
	f.t.Helper()
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			f.t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, body)
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "dynamic-rbac-e2e")
	if token != "" {
		req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
	}
	response, err := f.server.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		f.t.Fatal(err)
	}
	var decoded map[string]any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &decoded)
	}
	return response.StatusCode, decoded
}

func (f *dynamicRBACFixture) login(tenant dynamicTenant, loginName string) string {
	f.t.Helper()
	status, body := f.do(http.MethodPost, "/api/admin/auth/login", "", map[string]string{
		"loginName":  loginName,
		"tenantCode": tenant.code,
		"password":   "correct-password",
	})
	if status != http.StatusOK {
		f.t.Fatalf("login %s status=%d body=%v", loginName, status, body)
	}
	token, _ := body["accessToken"].(string)
	if token == "" {
		f.t.Fatalf("login %s returned no token: %v", loginName, body)
	}
	return token
}

func (f *dynamicRBACFixture) createUser(token, loginName string) (int, map[string]any) {
	return f.do(http.MethodPost, "/api/admin/system/users", token, map[string]any{
		"loginName":   loginName,
		"displayName": strings.Title(strings.Split(loginName, "-")[0]),
		"password":    "correct-password",
		"memberRealm": "both",
	})
}

// DYNA-002/004/005/006/016: create a custom role, grant capabilities, bind a
// member, observe enforcement, then delete the role only after unbinding.
func TestMySQLDynamicRoleLifecycleIntegration(t *testing.T) {
	f := newDynamicRBACFixture(t)
	tenant := f.tenants[0]
	adminToken := f.login(tenant, tenant.admin)

	code := "auditor_" + f.suffix[:8]
	status, body := f.do(http.MethodPost, "/api/admin/system/roles", adminToken, map[string]any{
		"code": code, "name": "Auditor", "remark": "read-only custom role",
	})
	if status != http.StatusCreated {
		t.Fatalf("create role status=%d body=%v", status, body)
	}
	roleID := uint64(body["id"].(float64))

	// DYNA-001: reserved codes and duplicates are rejected.
	for _, request := range []map[string]any{
		{"code": "admin", "name": "Fake admin"},
		{"code": "Bad Code", "name": "Invalid"},
		{"code": code, "name": "Duplicate"},
	} {
		if status, _ := f.do(http.MethodPost, "/api/admin/system/roles", adminToken, request); status != http.StatusBadRequest && status != http.StatusConflict {
			t.Fatalf("invalid role create %v status=%d", request, status)
		}
	}

	// Grant the auditor exactly project.read through the role-menu API.
	if status, body := f.do(http.MethodPut, fmt.Sprintf("/api/admin/system/roles/%d/menus", roleID), adminToken,
		map[string]any{"menuIds": []uint64{810000011}}); status != http.StatusOK {
		t.Fatalf("grant role menus status=%d body=%v", status, body)
	}
	status, body = f.do(http.MethodGet, fmt.Sprintf("/api/admin/system/roles/%d/menus", roleID), adminToken, nil)
	if status != http.StatusOK {
		t.Fatalf("read role menus status=%d", status)
	}
	selected := body["selectedMenuIds"].([]any)
	if len(selected) != 1 || uint64(selected[0].(float64)) != 810000011 {
		t.Fatalf("selected menus = %v", selected)
	}

	// Create the role member and bind them.
	member := fmt.Sprintf("dyn-member-%s", f.suffix)
	if status, body := f.createUser(adminToken, member); status != http.StatusCreated {
		t.Fatalf("create member status=%d body=%v", status, body)
	}
	memberSubject := "user:" + member
	if status, body := f.do(http.MethodPost, "/api/admin/authorization/roles", adminToken,
		map[string]string{"subject": memberSubject, "role": code}); status != http.StatusOK {
		t.Fatalf("bind custom role status=%d body=%v", status, body)
	}
	memberToken := f.login(tenant, member)

	// The auditor lacks role management and must be denied its endpoint.
	if status, _ := f.do(http.MethodPut, fmt.Sprintf("/api/admin/system/roles/%d/menus", roleID), memberToken,
		map[string]any{"menuIds": []uint64{810000011, 810000014}}); status != http.StatusForbidden {
		t.Fatalf("auditor role mutation status=%d, want 403", status)
	}

	// Admin grants role management to the custom role; enforcement now allows
	// the member because Casbin was reloaded in the same request.
	if status, body := f.do(http.MethodPut, fmt.Sprintf("/api/admin/system/roles/%d/menus", roleID), adminToken,
		map[string]any{"menuIds": []uint64{810000011, 810000014}}); status != http.StatusOK {
		t.Fatalf("extend role grants status=%d body=%v", status, body)
	}
	if status, _ := f.do(http.MethodGet, "/api/admin/system/roles", memberToken, nil); status != http.StatusForbidden {
		t.Fatalf("auditor without role.read status=%d, want 403", status)
	}

	// Role deletion is refused while it still has members.
	if status, _ := f.do(http.MethodDelete, fmt.Sprintf("/api/admin/system/roles/%d", roleID), adminToken, nil); status != http.StatusBadRequest {
		t.Fatalf("delete bound role status=%d, want 400", status)
	}
	if status, body := f.do(http.MethodDelete, "/api/admin/authorization/roles", adminToken,
		map[string]string{"subject": memberSubject, "role": code}); status != http.StatusOK {
		t.Fatalf("unbind custom role status=%d body=%v", status, body)
	}
	if status, _ := f.do(http.MethodDelete, fmt.Sprintf("/api/admin/system/roles/%d", roleID), adminToken, nil); status != http.StatusOK {
		t.Fatalf("delete empty role status=%d", status)
	}
	var pRules int
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM casbin_rules WHERE domain = ? AND ptype = 'p' AND v0 = ?",
		strconv.FormatUint(tenant.id, 10), code).Scan(&pRules); err != nil {
		t.Fatal(err)
	}
	if pRules != 0 {
		t.Fatalf("deleted role left %d policy rules", pRules)
	}
}

// DYNA-007..010: tenant menu catalog CRUD and system-row protection through
// both native and SmartAdmin compatibility endpoints.
func TestMySQLDynamicMenuCRUDIntegration(t *testing.T) {
	f := newDynamicRBACFixture(t)
	tenant := f.tenants[0]
	token := f.login(tenant, tenant.admin)

	status, body := f.do(http.MethodPost, "/api/admin/system/menus", token, map[string]any{
		"parentId": 810000001, "name": "Operations", "type": 2,
		"sort": 300, "path": "/system/ops", "component": "system/ops/index.vue", "visible": true,
	})
	if status != http.StatusCreated {
		t.Fatalf("create page status=%d body=%v", status, body)
	}
	menuID := uint64(body["id"].(float64))
	if menuID < 820000000 {
		t.Fatalf("custom menu id=%d below custom range", menuID)
	}

	for _, invalid := range []map[string]any{
		{"parentId": 999999999, "name": "Ghost", "type": 2},
		{"parentId": 810000002, "name": "Hack", "type": 3, "capability": "system.exec"},
		{"parentId": 810000002, "name": "Bad", "type": 3},
	} {
		if status, _ := f.do(http.MethodPost, "/api/admin/system/menus", token, invalid); status != http.StatusBadRequest {
			t.Fatalf("invalid menu %v status=%d", invalid, status)
		}
	}

	// Seeded structural rows and operator points cannot be mutated.
	if status, _ := f.do(http.MethodDelete, "/api/admin/system/menus/810000002", token, nil); status != http.StatusBadRequest {
		t.Fatalf("delete system menu status=%d, want 400", status)
	}
	if status, _ := f.do(http.MethodPut, "/api/admin/system/menus/810000002", token,
		map[string]any{"parentId": 810000001, "name": "Hijack", "type": 2}); status != http.StatusBadRequest {
		t.Fatalf("update system menu status=%d, want 400", status)
	}

	// SmartAdmin compatibility endpoints serve the same tenant catalog.
	status, body = f.do(http.MethodPost, "/menu/add", token, map[string]any{
		"menuName": "Compat Page", "menuType": 2, "parentId": 810000001, "sort": 301,
		"path": "/system/compat", "component": "system/compat/index.vue", "visibleFlag": true,
	})
	if status != http.StatusOK {
		t.Fatalf("compat add menu status=%d body=%v", status, body)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("compat add menu response data missing: %v", body)
	}
	compatID := uint64(data["menuId"].(float64))
	if status, _ := f.do(http.MethodGet, fmt.Sprintf("/menu/batchDelete?ids=%d", compatID), token, nil); status != http.StatusOK {
		t.Fatalf("compat delete menu status=%d", status)
	}

	if status, _ := f.do(http.MethodDelete, fmt.Sprintf("/api/admin/system/menus/%d", menuID), token, nil); status != http.StatusOK {
		t.Fatalf("delete custom menu status=%d", status)
	}
}

// DYNA-012..015: user provisioning, immediate session revocation on disable
// and password reset, and credential behavior.
func TestMySQLUserAdminLifecycleIntegration(t *testing.T) {
	f := newDynamicRBACFixture(t)
	tenant := f.tenants[0]
	adminToken := f.login(tenant, tenant.admin)

	loginName := fmt.Sprintf("dyn-managed-%s", f.suffix)
	if status, body := f.createUser(adminToken, loginName); status != http.StatusCreated {
		t.Fatalf("create user status=%d body=%v", status, body)
	}
	// DYNA-012: duplicate and weak payloads are rejected.
	if status, _ := f.createUser(adminToken, loginName); status != http.StatusConflict {
		t.Fatalf("duplicate user status=%d, want 409", status)
	}
	if status, _ := f.do(http.MethodPost, "/api/admin/system/users", adminToken, map[string]any{
		"loginName": "x", "password": "short",
	}); status != http.StatusBadRequest {
		t.Fatalf("invalid user status=%d, want 400", status)
	}

	var userID uint64
	if err := f.db.QueryRowContext(f.ctx, "SELECT user_id FROM users WHERE login_name = ?", loginName).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	userToken := f.login(tenant, loginName)

	// DYNA-013: disabling the membership revokes sessions immediately.
	if status, body := f.do(http.MethodPatch, fmt.Sprintf("/api/admin/system/users/%d/state", userID), adminToken,
		map[string]bool{"enabled": false}); status != http.StatusOK {
		t.Fatalf("disable user status=%d body=%v", status, body)
	}
	if status, body := f.do(http.MethodGet, "/login/getLoginInfo", userToken, nil); status != http.StatusUnauthorized {
		t.Fatalf("disabled member request status=%d, want 401 body=%v", status, body)
	}
	if status, _ := f.loginStatus(tenant, loginName); status != http.StatusUnauthorized {
		t.Fatalf("disabled member login status=%d, want 401", status)
	}

	// Re-enable, then reset password: old password rejected, new accepted.
	if status, _ := f.do(http.MethodPatch, fmt.Sprintf("/api/admin/system/users/%d/state", userID), adminToken,
		map[string]bool{"enabled": true}); status != http.StatusOK {
		t.Fatalf("enable user status=%d", status)
	}
	if status, _ := f.do(http.MethodPost, fmt.Sprintf("/api/admin/system/users/%d/reset-password", userID), adminToken,
		map[string]string{"password": "rotated-password-123"}); status != http.StatusOK {
		t.Fatalf("reset password status=%d", status)
	}
	if status, _ := f.loginStatus(tenant, loginName); status != http.StatusUnauthorized {
		t.Fatalf("old password after reset status=%d, want 401", status)
	}
	status, body := f.do(http.MethodPost, "/api/admin/auth/login", "", map[string]string{
		"loginName": loginName, "tenantCode": tenant.code, "password": "rotated-password-123",
	})
	if status != http.StatusOK {
		t.Fatalf("new password login status=%d body=%v", status, body)
	}

	// DYNA-015: the sole tenant administrator cannot be disabled.
	var adminID uint64
	if err := f.db.QueryRowContext(f.ctx, "SELECT user_id FROM users WHERE subject = ?", tenant.admin).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.do(http.MethodPatch, fmt.Sprintf("/api/admin/system/users/%d/state", adminID), adminToken,
		map[string]bool{"enabled": false}); status != http.StatusBadRequest {
		t.Fatalf("disable sole admin status=%d, want 400", status)
	}
}

// DYNA-005: last-administrator invariant across member removal, capability
// replacement, and user disable paths.
func TestMySQLRBACLastAdministratorLockoutIntegration(t *testing.T) {
	f := newDynamicRBACFixture(t)
	tenant := f.tenants[1] // fresh tenant to avoid cross-test administrator changes
	adminToken := f.login(tenant, tenant.admin)

	// The only direct admin binding cannot be removed.
	if status, _ := f.do(http.MethodDelete, "/api/admin/authorization/roles", adminToken,
		map[string]string{"subject": tenant.admin, "role": "admin"}); status != http.StatusBadRequest {
		t.Fatalf("remove sole admin binding status=%d, want 400", status)
	}

	// A custom role holding the management set keeps the tenant alive while
	// the fixed admin binding is removed.
	status, body := f.do(http.MethodPost, "/api/admin/system/roles", adminToken, map[string]any{
		"code": "ops_" + f.suffix[:8], "name": "Operations",
	})
	if status != http.StatusCreated {
		t.Fatalf("create ops role status=%d body=%v", status, body)
	}
	roleID := uint64(body["id"].(float64))
	if status, body := f.do(http.MethodPut, fmt.Sprintf("/api/admin/system/roles/%d/menus", roleID), adminToken, map[string]any{
		"menuIds": []uint64{810000014, 810000031, 810000033, 810000034},
	}); status != http.StatusOK {
		t.Fatalf("grant management set status=%d body=%v", status, body)
	}
	if status, body := f.do(http.MethodPost, "/api/admin/authorization/roles", adminToken,
		map[string]string{"subject": tenant.admin, "role": "ops_" + f.suffix[:8]}); status != http.StatusOK {
		t.Fatalf("bind ops role status=%d body=%v", status, body)
	}
	if status, _ := f.do(http.MethodDelete, "/api/admin/authorization/roles", adminToken,
		map[string]string{"subject": tenant.admin, "role": "admin"}); status != http.StatusOK {
		t.Fatalf("remove admin with backup manager status=%d", status)
	}
	// Stripping the management set from the only remaining manager is blocked.
	if status, _ := f.do(http.MethodPut, fmt.Sprintf("/api/admin/system/roles/%d/menus", roleID), adminToken,
		map[string]any{"menuIds": []uint64{810000011}}); status != http.StatusBadRequest {
		t.Fatalf("strip last manager grants status=%d, want 400", status)
	}
}

// DYNA-002/017/019: roles, menus, and users never leak across tenant domains.
func TestMySQLRBACTenantIsolationIntegration(t *testing.T) {
	f := newDynamicRBACFixture(t)
	tenantA, tenantB := f.tenants[0], f.tenants[1]
	tokenA := f.login(tenantA, tenantA.admin)
	tokenB := f.login(tenantB, tenantB.admin)

	code := "secret_" + f.suffix[:8]
	status, body := f.do(http.MethodPost, "/api/admin/system/roles", tokenA, map[string]any{
		"code": code, "name": "Secret role",
	})
	if status != http.StatusCreated {
		t.Fatalf("create tenant A role status=%d body=%v", status, body)
	}
	roleID := uint64(body["id"].(float64))

	status, bodyB := f.do(http.MethodGet, "/api/admin/system/roles", tokenB, nil)
	if status != http.StatusOK {
		t.Fatalf("tenant B list roles status=%d", status)
	}
	for _, item := range bodyB["roles"].([]any) {
		if item.(map[string]any)["code"] == code {
			t.Fatalf("tenant A role leaked into tenant B: %v", item)
		}
	}
	if status, _ := f.do(http.MethodGet, fmt.Sprintf("/api/admin/system/roles/%d/menus", roleID), tokenB, nil); status != http.StatusNotFound {
		t.Fatalf("cross-tenant role read status=%d, want 404", status)
	}
	if status, _ := f.do(http.MethodDelete, fmt.Sprintf("/api/admin/system/roles/%d", roleID), tokenB, nil); status != http.StatusNotFound {
		t.Fatalf("cross-tenant role delete status=%d, want 404", status)
	}

	loginA := fmt.Sprintf("dyn-private-%s", f.suffix)
	if status, _ := f.createUser(tokenA, loginA); status != http.StatusCreated {
		t.Fatalf("create tenant A user status=%d", status)
	}
	status, bodyB = f.do(http.MethodGet, "/api/admin/system/users", tokenB, nil)
	if status != http.StatusOK {
		t.Fatalf("tenant B list users status=%d", status)
	}
	for _, item := range bodyB["list"].([]any) {
		if item.(map[string]any)["loginName"] == loginA {
			t.Fatalf("tenant A user leaked into tenant B list")
		}
	}
}

func (f *dynamicRBACFixture) loginStatus(tenant dynamicTenant, loginName string) (int, map[string]any) {
	return f.do(http.MethodPost, "/api/admin/auth/login", "", map[string]string{
		"loginName":  loginName,
		"tenantCode": tenant.code,
		"password":   "correct-password",
	})
}
