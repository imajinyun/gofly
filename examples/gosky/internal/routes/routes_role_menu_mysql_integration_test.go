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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
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

// AUTHN-036..038 and AUTHN-040: persisted sessions, HTTP handlers, transactions,
// audit events and Casbin decisions. Run with -json to retain a credential-free
// E2E transcript. Only fresh fixture identities and policy tables are mutated.
func TestMySQLSmartWebRoleMenuRoundTripIntegration(t *testing.T) {
	f := newRoleMenuFixture(t)
	admin := f.login(t, "manager", 0, "admin")
	reader := f.login(t, "reader", 0, "admin")
	front := f.login(t, "reader", 0, "front")
	for _, endpoint := range []struct {
		name, path string
		total      int
	}{
		{"tenant employees", "/employee/query", 2},
		{"role employees", "/role/employee/queryEmployee", 1},
	} {
		for _, page := range []struct {
			name   string
			number uint64
			count  int
		}{
			{"first page default", 0, endpoint.total},
			{"past last page", 2, 0},
			{"signed offset overflow", 922337203685477581, 0},
			{"unsigned offset wrap", 4611686018427387905, 0},
			{"largest page", ^uint64(0), 0},
		} {
			t.Run(endpoint.name+"/"+page.name, func(t *testing.T) {
				var response smartweb.ResponseDTO[smartweb.RoleEmployeePageVO]
				input := map[string]any{"pageNum": page.number, "pageSize": 20}
				if endpoint.path == "/role/employee/queryEmployee" {
					input["roleId"] = uint64(810000101)
				}
				f.request(t, http.MethodPost, endpoint.path, admin, input, http.StatusOK, &response)
				if response.Data.Total != endpoint.total || len(response.Data.List) != page.count {
					t.Errorf("page %d total=%d items=%d; want total=%d items=%d", page.number, response.Data.Total, len(response.Data.List), endpoint.total, page.count)
				}
			})
		}
	}
	otherBefore, err := f.store.ListRoleCapabilities(f.ctx, f.domains[1], authz.RoleReader)
	if err != nil {
		t.Fatal(err)
	}

	expected := []struct {
		name string
		id   uint64
		cap  authz.Capability
	}{
		{"Read related Projects", 810000011, authz.CapabilityProjectRead},
		{"Read all tenant Projects", 810000012, authz.CapabilityProjectReadAny},
		{"Manage Project collaborators", 810000013, authz.CapabilityProjectMemberManage},
		{"Edit role permissions", 810000014, authz.CapabilityAuthorizationRoleManage},
		{"Call Greeter", 810000015, authz.CapabilityGreeterHello},
		{"Watch Greeter stream", 810000016, authz.CapabilityGreeterWatch},
		{"Update related Projects", 810000017, authz.CapabilityProjectUpdate},
		{"Delete owned Projects", 810000018, authz.CapabilityProjectDelete},
		{"Manage all tenant Projects (scope only)", 810000019, authz.CapabilityProjectManageAny},
		{"View tenant roles and employees", 810000031, authz.CapabilityAuthorizationRoleRead},
		{"Edit Project read scope", 810000032, authz.CapabilityAuthorizationRoleScopeManage},
		{"Assign tenant roles", 810000033, authz.CapabilityAuthorizationRoleMemberAdd},
		{"Remove tenant roles", 810000034, authz.CapabilityAuthorizationRoleMemberRemove},
		{"Read policy audit events", 810000035, authz.CapabilityAuthorizationPolicyEventRead},
	}
	var catalog smartweb.ResponseDTO[[]smartweb.MenuVO]
	f.request(t, http.MethodGet, "/menu/query", admin, nil, http.StatusOK, &catalog)
	var selection smartweb.ResponseDTO[smartweb.RoleMenuSelectionVO]
	f.request(t, http.MethodGet, "/role/menu/getRoleSelectedMenu/810000101", admin, nil, http.StatusOK, &selection)
	flat := flattenRoleMenus(selection.Data.MenuTreeList)
	if !reflect.DeepEqual(flat, catalog.Data) {
		t.Error("menu catalog and selectable role tree describe different entries")
	}
	allIDs := make([]uint64, 0, len(flat))
	structuralIDs := make([]uint64, 0, 3)
	for _, node := range flat {
		allIDs = append(allIDs, node.MenuID)
		if node.MenuType != smartweb.SmartWebMenuTypePoint {
			structuralIDs = append(structuralIDs, node.MenuID)
		}
	}
	if !slices.Contains(structuralIDs, uint64(810000003)) {
		t.Fatal("Menu Catalog page is absent from the displayed tree")
	}
	var loginInfo smartweb.ResponseDTO[smartweb.LoginResultVO]
	f.request(t, http.MethodGet, "/login/getLoginInfo", admin, nil, http.StatusOK, &loginInfo)
	buttons := assertRoleMenuButtons(t, loginInfo.Data, true)
	for _, id := range buttons {
		if slices.Contains(allIDs, id) {
			t.Errorf("login-only button ID %d is an assignable catalog entry", id)
		}
	}

	for _, tc := range expected {
		t.Run(tc.name, func(t *testing.T) {
			var node smartweb.MenuVO
			count := 0
			for _, item := range flat {
				if item.MenuID == tc.id {
					node = item
					count++
				}
			}
			if count != 1 || node.MenuName != tc.name || node.MenuType != smartweb.SmartWebMenuTypePoint || node.WebPerms != "" {
				t.Errorf("capability %s node = %+v (count %d), want label %q without management-button permission", tc.cap, node, count, tc.name)
			}
			f.save(t, admin, append(slices.Clone(structuralIDs), tc.id), http.StatusOK)
			f.request(t, http.MethodGet, "/role/menu/getRoleSelectedMenu/810000101", admin, nil, http.StatusOK, &selection)
			if !sameRoleMenuIDs(selection.Data.SelectedMenuID, append(slices.Clone(structuralIDs), tc.id)) {
				t.Errorf("selected IDs = %v, want structure and %d", selection.Data.SelectedMenuID, tc.id)
			}
			stored, err := f.store.ListRoleCapabilities(f.ctx, f.domains[0], authz.RoleReader)
			if err != nil || !reflect.DeepEqual(stored, []authz.Capability{tc.cap}) {
				t.Fatalf("persisted reader capabilities = %v, err %v; want only %s", stored, err, tc.cap)
			}
			principal := auth.NewContext(f.ctx, auth.Principal{Subject: f.subject("reader"), Claims: map[string]any{"tenant_id": f.domains[0]}})
			for _, check := range expected {
				decision := f.authorizer.Authorize(principal, string(check.cap), authz.CapabilityAction)
				if check.cap == tc.cap && decision != nil || check.cap != tc.cap && (decision == nil || decision.Code != coreerrors.CodePermissionDenied) {
					t.Errorf("saved %s, decision for %s = %v", tc.cap, check.cap, decision)
				}
			}
			f.request(t, http.MethodGet, "/login/getLoginInfo", reader, nil, http.StatusOK, &loginInfo)
			if tc.cap == authz.CapabilityAuthorizationRoleRead {
				if len(loginInfo.Data.MenuList) != 3 {
					t.Errorf("role reader navigation=%v, want pages only", loginInfo.Data.MenuList)
				}
				for _, node := range loginInfo.Data.MenuList {
					if node.WebPerms != "" {
						t.Error("role read granted a management button")
					}
				}
			} else {
				assertRoleMenuButtons(t, loginInfo.Data, false)
			}
			before := f.eventCount(t)
			f.save(t, admin, selection.Data.SelectedMenuID, http.StatusOK)
			if after := f.eventCount(t); after != before {
				t.Errorf("unchanged round-trip added events: %d -> %d", before, after)
			}
			t.Logf("label=%q menuId=%d persisted=%s decisions=verified roundTrip=verified", tc.name, tc.id, tc.cap)
		})
	}

	f.save(t, admin, allIDs, http.StatusOK)
	f.request(t, http.MethodGet, "/role/menu/getRoleSelectedMenu/810000101", admin, nil, http.StatusOK, &selection)
	if !sameRoleMenuIDs(selection.Data.SelectedMenuID, allIDs) {
		t.Errorf("select-all round-trip = %v, want %v", selection.Data.SelectedMenuID, allIDs)
	}
	for _, badID := range append(buttons, uint64(999999999)) {
		before := f.eventCount(t)
		f.save(t, admin, []uint64{810000011, badID}, http.StatusBadRequest)
		f.request(t, http.MethodGet, "/role/menu/getRoleSelectedMenu/810000101", admin, nil, http.StatusOK, &selection)
		if f.eventCount(t) != before || !sameRoleMenuIDs(selection.Data.SelectedMenuID, allIDs) {
			t.Fatalf("rejected ID %d mutated policy or events", badID)
		}
	}
	f.save(t, admin, structuralIDs, http.StatusOK)
	for _, tc := range []struct {
		name, token string
		status      int
	}{
		{"reader without management", reader, http.StatusForbidden},
		{"front session", front, http.StatusUnauthorized},
		{"missing session", "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.eventCount(t)
			f.request(t, http.MethodGet, "/menu/query", tc.token, nil, tc.status, nil)
			f.save(t, tc.token, allIDs, tc.status)
			if f.eventCount(t) != before {
				t.Error("unauthorized request wrote audit events")
			}
		})
	}
	stored, err := f.store.ListRoleCapabilities(f.ctx, f.domains[0], authz.RoleReader)
	if err != nil || len(stored) != 0 {
		t.Fatalf("structure-only save left capabilities %v, err %v", stored, err)
	}
	otherAfter, err := f.store.ListRoleCapabilities(f.ctx, f.domains[1], authz.RoleReader)
	if err != nil || !reflect.DeepEqual(otherBefore, otherAfter) {
		t.Fatalf("other tenant changed: %v -> %v, err %v", otherBefore, otherAfter, err)
	}
	var event authz.PolicyEvent
	events, err := f.store.ListEvents(f.ctx, f.domains[0], 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range events {
		if candidate.RequestID == "role-menu-e2e" {
			event = candidate
			break
		}
	}
	if event.ActorSubject != f.subject("manager") {
		t.Fatalf("policy mutation audit actor = %q", event.ActorSubject)
	}
	t.Logf("allCapabilities=18 structuralNodes=%d loginButtons=21 tenantIsolation=verified auditEvents=%d", len(structuralIDs), len(events))
}

func flattenRoleMenus(nodes []smartweb.MenuVO) []smartweb.MenuVO {
	result := make([]smartweb.MenuVO, 0)
	for _, node := range nodes {
		children := node.Children
		node.Children = nil
		result = append(result, node)
		result = append(result, flattenRoleMenus(children)...)
	}
	slices.SortFunc(result, func(a, b smartweb.MenuVO) int { return a.Sort - b.Sort })
	return result
}

func sameRoleMenuIDs(a, b []uint64) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func assertRoleMenuButtons(t *testing.T, info smartweb.LoginResultVO, allowed bool) []uint64 {
	t.Helper()
	want := []string{
		"system:role:menu:update", "system:role:dataScope:update",
		"system:role:employee:add", "system:role:employee:delete", "system:role:employee:batch:delete",
		"system:role:add", "system:role:update", "system:role:delete",
		"system:menu:add", "system:menu:update", "system:menu:batchDelete",
		"system:employee:add", "system:employee:update", "system:employee:delete",
		"system:employee:department:update", "system:employee:disabled", "system:employee:password:reset",
		"support:department:query", "system:department:add", "system:department:update", "system:department:delete",
	}
	var got []string
	var ids []uint64
	for _, node := range info.MenuList {
		if node.MenuType == smartweb.SmartWebMenuTypePoint {
			got = append(got, node.WebPerms)
			ids = append(ids, node.MenuID)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if info.AdministratorFlag || allowed && !slices.Equal(got, want) || !allowed && len(info.MenuList) != 0 {
		t.Errorf("allowed=%t menu point permissions=%v, administratorFlag=%t", allowed, got, info.AdministratorFlag)
	}
	return ids
}

type roleMenuFixture struct {
	ctx        context.Context
	db         *sql.DB
	store      *authz.MySQLPolicyStore
	authorizer *authz.Authorizer
	server     *httptest.Server
	suffix     string
	domains    []string
}

func newRoleMenuFixture(t *testing.T) *roleMenuFixture {
	t.Helper()
	dsn := os.Getenv("GOSKY_MYSQL_DSN")
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for isolated MySQL role-menu E2E")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	f := &roleMenuFixture{ctx: ctx, db: db, suffix: hex.EncodeToString(nonce[:])}
	tables, err := authz.NewMySQLPolicyTables("gosky_menu_rules_"+f.suffix, "gosky_menu_events_"+f.suffix)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{tables.Rules, "casbin_rules"}, {tables.Events, "casbin_policy_events"}} {
		// Identifiers are fixture-owned and validated above, never request data.
		if _, err := db.ExecContext(ctx, "CREATE TABLE `"+pair[0]+"` LIKE `"+pair[1]+"`"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := db.ExecContext(cleanupCtx, "DROP TABLE `"+pair[0]+"`"); err != nil {
				t.Errorf("cleanup fixture table: %v", err)
			}
		})
	}
	var userIDs []int64
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		for _, domain := range f.domains {
			for _, table := range []string{"auth_login_logs", "auth_sessions", "tenant_users", "tenants"} {
				if _, err := db.ExecContext(cleanupCtx, "DELETE FROM "+table+" WHERE tenant_id = ?", domain); err != nil {
					t.Errorf("cleanup fixture %s: %v", table, err)
				}
			}
		}
		for _, id := range userIDs {
			if _, err := db.ExecContext(cleanupCtx, "DELETE FROM users WHERE user_id = ?", id); err != nil {
				t.Errorf("cleanup fixture user: %v", err)
			}
		}
	})
	f.store, err = authz.NewMySQLPolicyStoreWithTables(db, tables)
	if err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		result, err := db.ExecContext(ctx, "INSERT INTO tenants (tenant_code, tenant_name) VALUES (?, ?)", f.tenantCode(index), "Role menu E2E")
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		f.domains = append(f.domains, strconv.FormatInt(id, 10))
	}
	for _, user := range []string{"manager", "reader"} {
		result, err := db.ExecContext(ctx, "INSERT INTO users (subject, login_name, password_hash, account_type) VALUES (?, ?, ?, 'both')", f.subject(user), f.subject(user), integrationPasswordHash("correct-password"))
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		userIDs = append(userIDs, id)
		for _, domain := range f.domains {
			if _, err := db.ExecContext(ctx, "INSERT INTO tenant_users (tenant_id, user_id, member_realm) VALUES (?, ?, 'both')", domain, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, domain := range f.domains {
		actor := authz.PolicyActor{Subject: "e2e-bootstrap", Domain: domain}
		if err := f.store.BootstrapTenant(ctx, actor, f.subject("manager")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.GrantRole(ctx, actor, f.subject("reader"), authz.RoleReader, domain); err != nil {
			t.Fatal(err)
		}
	}
	f.authorizer, err = authz.NewMySQLAuthorizerWithTables("../../etc/authorization/casbin_model.conf", db, tables, []byte(httpAuthenticationSecret))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.authorizer.Close)
	identity := repo.NewMySQLIdentityRepository(storage.NewSQLStore(db))
	authentication, err := authn.NewService(authn.ServiceOptions{
		Repository: identity, AccessSecret: []byte(httpAuthenticationSecret),
		AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := authz.NewPolicyService(f.store, f.authorizer)
	compatibility, err := smartweb.NewService(authentication, f.authorizer, nil, app.NewRoleService(policy, identity))
	if err != nil {
		t.Fatal(err)
	}
	rbacRepository := repo.NewMySQLRBACRepository(storage.NewSQLStore(db), f.store)
	orgRepository := repo.NewOrgRepository(storage.NewSQLStore(db), rbacRepository)
	departmentService := app.NewDepartmentService(f.authorizer, orgRepository)
	positionService := app.NewPositionService(f.authorizer, orgRepository)
	employeeService := app.NewEmployeeService(f.authorizer, orgRepository, rbacRepository, policy)
	compatibility.SetOrganization(app.NewOrganizationFacade(departmentService, positionService, employeeService))
	services := svc.NewServiceContext(config.Config{})
	services.SetAuthenticationService(authentication)
	services.SetAdminSessionService(authentication)
	services.SetSmartWebService(compatibility)
	services.SetAuthorizer(f.authorizer)
	services.SetProjectService(app.NewProjectService(repo.NewMySQLProjectRepository(storage.NewSQLStore(db)), f.authorizer))
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, services)
	f.server = httptest.NewServer(server.Handler())
	t.Cleanup(f.server.Close)
	return f
}

func (f *roleMenuFixture) subject(user string) string { return "menu-" + user + "-" + f.suffix }
func (f *roleMenuFixture) tenantCode(index int) string {
	return fmt.Sprintf("menu-%s-%d", f.suffix, index)
}
func (f *roleMenuFixture) login(t *testing.T, user string, tenant int, realm string) string {
	t.Helper()
	var tokens authn.TokenPair
	f.request(t, http.MethodPost, "/api/"+realm+"/auth/login", "", map[string]string{"loginName": f.subject(user), "tenantCode": f.tenantCode(tenant), "password": "correct-password"}, http.StatusOK, &tokens)
	if tokens.AccessToken == "" {
		t.Fatal("login returned no access token")
	}
	return tokens.AccessToken
}

func (f *roleMenuFixture) save(t *testing.T, token string, ids []uint64, status int) {
	t.Helper()
	f.request(t, http.MethodPost, "/role/menu/updateRoleMenu", token, map[string]any{"roleId": uint64(810000101), "menuIdList": ids}, status, nil)
}

func (f *roleMenuFixture) eventCount(t *testing.T) int {
	t.Helper()
	events, err := f.store.ListEvents(f.ctx, f.domains[0], 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func (f *roleMenuFixture) request(t *testing.T, method, path, token string, input any, status int, out any) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "role-menu-e2e")
	if token != "" {
		req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
	}
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s status=%d, want %d", method, path, response.StatusCode, status)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
	if status == http.StatusOK && !strings.HasPrefix(path, "/api/") {
		var envelope smartweb.ResponseDTO[json.RawMessage]
		if err := json.Unmarshal(data, &envelope); err != nil || !envelope.OK || envelope.Code != 0 {
			t.Fatalf("%s returned an unsuccessful envelope", path)
		}
	}
}
