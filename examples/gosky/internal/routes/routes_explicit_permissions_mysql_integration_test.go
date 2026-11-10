//go:build integration

package routes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/model/repo"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
)

// See docs/authorization/explicit-admin-permissions.md for the prewritten inventory.
func TestMySQLExplicitAdminPermissionsIntegration(t *testing.T) {
	t.Run("project operations and revocation", testExplicitProjectOperations)
	t.Run("transaction failures and unique names", testExplicitProjectTransactionBoundaries)
	t.Run("independent management permissions", testExplicitManagementPermissions)
	t.Run("legacy conversion is deliberate and idempotent", testExplicitLegacyConversion)
}

func explicitGrant(t *testing.T, f *roleMenuFixture, role, capability string, grant bool) {
	t.Helper()
	actor := authz.PolicyActor{Subject: "e2e-operator", Domain: f.domains[0]}
	var err error
	if grant {
		_, err = f.store.GrantPermission(f.ctx, actor, role, actor.Domain, capability, authz.CapabilityAction)
	} else {
		_, err = f.store.RevokePermission(f.ctx, actor, role, actor.Domain, capability, authz.CapabilityAction)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := f.authorizer.ReloadPolicy(); err != nil {
		t.Fatal(err)
	}
}

func explicitProject(t *testing.T, f *roleMenuFixture, owner string) string {
	t.Helper()
	result, err := f.db.ExecContext(f.ctx, "INSERT INTO projects (tenant_id,owner_subject,project_name,project_memo) VALUES (?,?,?,?)", f.domains[0], f.subject(owner), "Original "+owner, "Initial memo")
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, query := range []string{"DELETE FROM opslogs WHERE project_id=?", "DELETE FROM project_members WHERE project_id=?", "DELETE FROM projects WHERE project_id=?"} {
			if _, err := f.db.ExecContext(ctx, query, id); err != nil {
				t.Error(err)
			}
		}
	})
	return strconv.FormatInt(id, 10)
}

func explicitActions(t *testing.T, f *roleMenuFixture, path, token string, want ...string) {
	t.Helper()
	var response struct {
		AllowedActions []string `json:"allowedActions"`
	}
	f.request(t, http.MethodGet, path, token, nil, http.StatusOK, &response)
	if !slices.Equal(response.AllowedActions, want) {
		t.Errorf("allowedActions=%v want=%v path=%s", response.AllowedActions, want, path)
	}
}

func testExplicitProjectOperations(t *testing.T) {
	f := newRoleMenuFixture(t)
	admin := f.login(t, "manager", 0, "admin")
	owner := f.login(t, "reader", 0, "admin")
	front := f.login(t, "reader", 0, "front")
	foreign := f.login(t, "reader", 1, "admin")
	owned := explicitProject(t, f, "reader")
	shared := explicitProject(t, f, "manager")
	path := "/api/admin/projects/" + owned
	sharedPath := "/api/admin/projects/" + shared
	explicitActions(t, f, path, owner, "read")
	f.request(t, http.MethodPut, path, owner, map[string]string{"name": "Denied"}, http.StatusForbidden, nil)
	explicitGrant(t, f, "reader", "project.update", true)
	explicitActions(t, f, path, owner, "read", "update")
	f.request(t, http.MethodPut, path, owner, map[string]string{"name": "Updated", "description": "Changed"}, http.StatusOK, nil)
	var name, memo string
	if err := f.db.QueryRowContext(f.ctx, "SELECT project_name,project_memo FROM projects WHERE project_id=?", owned).Scan(&name, &memo); err != nil || name != "Updated" || memo != "Changed" {
		t.Fatalf("update persisted name=%q memo=%q err=%v", name, memo, err)
	}
	f.request(t, http.MethodPut, path, owner, map[string]string{"name": ""}, http.StatusBadRequest, nil)
	f.request(t, http.MethodPut, path, foreign, map[string]string{"name": "Cross tenant"}, http.StatusForbidden, nil)
	f.request(t, http.MethodPut, sharedPath+"/members", admin, map[string]string{"subject": f.subject("reader"), "role": "viewer"}, http.StatusOK, nil)
	explicitActions(t, f, sharedPath, owner, "read")
	f.request(t, http.MethodPut, sharedPath, owner, map[string]string{"name": "Viewer denied"}, http.StatusForbidden, nil)
	f.request(t, http.MethodPut, sharedPath+"/members", admin, map[string]string{"subject": f.subject("reader"), "role": "editor"}, http.StatusOK, nil)
	explicitActions(t, f, sharedPath, owner, "read", "update")
	f.request(t, http.MethodPut, sharedPath, owner, map[string]string{"name": "Editor updated"}, http.StatusOK, nil)
	explicitGrant(t, f, "reader", "project.delete", true)
	f.request(t, http.MethodDelete, sharedPath, owner, nil, http.StatusForbidden, nil)
	explicitGrant(t, f, "reader", "project.update", false)
	explicitActions(t, f, sharedPath, owner, "read")
	f.request(t, http.MethodPut, sharedPath, owner, map[string]string{"name": "Revoked editor"}, http.StatusForbidden, nil)
	explicitGrant(t, f, "reader", "project.read", false)
	explicitGrant(t, f, "reader", "project.read.any", true)
	f.request(t, http.MethodGet, path, owner, nil, http.StatusForbidden, nil)
	f.request(t, http.MethodGet, "/api/admin/projects", owner, nil, http.StatusForbidden, nil)
	f.request(t, http.MethodGet, "/api/front/projects/"+owned, front, nil, http.StatusOK, nil)
	explicitGrant(t, f, "reader", "project.read", true)
	f.request(t, http.MethodPut, path+"/members", owner, map[string]string{"subject": f.subject("manager"), "role": "viewer"}, http.StatusForbidden, nil)
	explicitGrant(t, f, "reader", "project.member.manage", true)
	explicitActions(t, f, path, owner, "read", "delete", "members.manage")
	f.request(t, http.MethodPut, path+"/members", owner, map[string]string{"subject": f.subject("manager"), "role": "viewer"}, http.StatusOK, nil)
	f.request(t, http.MethodDelete, path, owner, nil, http.StatusOK, nil)
	f.request(t, http.MethodGet, path, owner, nil, http.StatusNotFound, nil)
	var deleted, audit int
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM projects WHERE project_id=? AND project_state='deleted' AND deleted_at IS NOT NULL", owned).Scan(&deleted); err != nil || deleted != 1 {
		t.Fatalf("soft delete count=%d err=%v", deleted, err)
	}
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM opslogs WHERE project_id=? AND operation IN ('project.update','project.delete') AND outcome='allowed'", owned).Scan(&audit); err != nil || audit != 2 {
		t.Fatalf("atomic write audit count=%d err=%v", audit, err)
	}
}

func testExplicitManagementPermissions(t *testing.T) {
	f := newRoleMenuFixture(t)
	reader := f.login(t, "reader", 0, "admin")
	admin := f.login(t, "manager", 0, "admin")
	for _, capability := range []string{"authorization.role.read", "authorization.role.member.add"} {
		explicitGrant(t, f, "reader", capability, true)
	}
	var login smartweb.ResponseDTO[smartweb.LoginResultVO]
	f.request(t, http.MethodGet, "/login/getLoginInfo", reader, nil, http.StatusOK, &login)
	var buttons []string
	for _, menu := range login.Data.MenuList {
		if menu.WebPerms != "" {
			buttons = append(buttons, menu.WebPerms)
		}
	}
	if !slices.Equal(buttons, []string{"system:role:employee:add"}) {
		t.Errorf("employee assigner buttons=%v", buttons)
	}
	f.request(t, http.MethodGet, "/role/getAll", reader, nil, http.StatusOK, nil)
	f.request(t, http.MethodPost, "/role/menu/updateRoleMenu", reader, map[string]any{"roleId": uint64(810000101), "menuIdList": []uint64{810000011}}, http.StatusForbidden, nil)
	f.request(t, http.MethodPost, "/role/dataScope/updateRoleDataScopeList", reader, map[string]any{"roleId": uint64(810000101), "dataScopeItemList": []map[string]int{{"dataScopeType": 1, "viewType": 2}}}, http.StatusForbidden, nil)
	var userID uint64
	if err := f.db.QueryRowContext(f.ctx, "SELECT user_id FROM users WHERE subject=?", f.subject("reader")).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"roleId": uint64(810000101), "employeeIdList": []uint64{userID}}
	f.request(t, http.MethodPost, "/role/employee/batchAddRoleEmployee", reader, input, http.StatusOK, nil)
	f.request(t, http.MethodPost, "/role/employee/batchRemoveRoleEmployee", reader, input, http.StatusForbidden, nil)
	input["roleId"] = uint64(810000103)
	f.request(t, http.MethodPost, "/role/employee/batchAddRoleEmployee", reader, input, http.StatusForbidden, nil)
	// A scope editor can change scope without gaining the permission editor's authority.
	explicitGrant(t, f, "reader", "authorization.role.scope.manage", true)
	explicitGrant(t, f, "reader", "project.read", false)
	f.request(t, http.MethodPost, "/role/dataScope/updateRoleDataScopeList", reader, map[string]any{"roleId": uint64(810000101), "dataScopeItemList": []map[string]int{{"dataScopeType": 1, "viewType": 1}}}, http.StatusOK, nil)
	caps, err := f.store.ListRoleCapabilities(f.ctx, f.domains[0], "reader")
	if err != nil || slices.Contains(caps, authz.CapabilityProjectRead) {
		t.Fatalf("scope change restored revoked operation: capabilities=%v err=%v", caps, err)
	}
	// Removing only the read grant must not strand the final permission administrator.
	var catalog smartweb.ResponseDTO[[]smartweb.MenuVO]
	f.request(t, http.MethodGet, "/menu/query", admin, nil, http.StatusOK, &catalog)
	var selected []uint64
	for _, item := range catalog.Data {
		if item.MenuID != 810000031 {
			selected = append(selected, item.MenuID)
		}
	}
	f.request(t, http.MethodPost, "/role/menu/updateRoleMenu", admin, map[string]any{"roleId": uint64(810000103), "menuIdList": selected}, http.StatusBadRequest, nil)
	// Admin cannot accidentally remove the last permission administrator.
	if err := f.db.QueryRowContext(f.ctx, "SELECT user_id FROM users WHERE subject=?", f.subject("manager")).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	input["employeeIdList"] = []uint64{userID}
	f.request(t, http.MethodPost, "/role/employee/batchRemoveRoleEmployee", admin, input, http.StatusBadRequest, nil)
}

func testExplicitLegacyConversion(t *testing.T) {
	f := newRoleMenuFixture(t)
	converter, ok := any(f.store).(interface {
		UpgradeTenantExplicitPermissions(context.Context, authz.PolicyActor) (bool, error)
	})
	if !ok {
		t.Fatal("audited explicit-permission conversion is unavailable")
	}
	actor := authz.PolicyActor{Subject: "e2e-converter", Domain: f.domains[0]}
	// Fixture-owned table names were validated by newRoleMenuFixture.
	table := "`gosky_menu_rules_" + f.suffix + "`"
	for _, edge := range [][2]string{{"writer", "reader"}, {"admin", "writer"}} {
		if _, err := f.db.ExecContext(f.ctx, "INSERT INTO "+table+" (rule_key,ptype,domain,v0,v1,v2) VALUES (?,'g',?,?,?,?)", "legacy-"+edge[0]+f.suffix, actor.Domain, edge[0], edge[1], actor.Domain); err != nil {
			t.Fatal(err)
		}
	}
	explicitGrant(t, f, "writer", "project.read", false)
	changed, err := converter.UpgradeTenantExplicitPermissions(f.ctx, actor)
	if err != nil || !changed {
		t.Fatalf("legacy conversion changed=%t err=%v", changed, err)
	}
	caps, err := f.store.ListRoleCapabilities(f.ctx, actor.Domain, "writer")
	if err != nil || !slices.Contains(caps, authz.CapabilityProjectRead) {
		t.Fatalf("materialized direct writer grants=%v err=%v", caps, err)
	}
	explicitGrant(t, f, "writer", "project.read", false)
	changed, err = converter.UpgradeTenantExplicitPermissions(f.ctx, actor)
	if err != nil || changed {
		t.Fatalf("conversion retry changed=%t err=%v", changed, err)
	}
	caps, err = f.store.ListRoleCapabilities(f.ctx, actor.Domain, "writer")
	if err != nil || slices.Contains(caps, authz.CapabilityProjectRead) {
		t.Fatalf("retry resurrected revoked grant: %v err=%v", caps, err)
	}
	if _, err := f.store.GrantRole(f.ctx, actor, "writer", "reader", actor.Domain); err == nil {
		t.Fatal("role inheritance accepted through policy mutation API")
	}
}

// HTTP rejects oversized audit inputs before storage. Use the real repository
// boundary to force a failure after the data write and verify MySQL rollback.
func testExplicitProjectTransactionBoundaries(t *testing.T) {
	f := newRoleMenuFixture(t)
	owned := explicitProject(t, f, "reader")
	shared := explicitProject(t, f, "manager")
	admin := f.login(t, "manager", 0, "admin")
	owner := f.login(t, "reader", 0, "admin")
	tenantID, err := strconv.ParseUint(f.domains[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := strconv.ParseUint(owned, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := mysql.ParseDSN(os.Getenv("GOSKY_MYSQL_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if dsn.Params == nil {
		dsn.Params = make(map[string]string)
	}
	dsn.Params["sql_mode"] = "'STRICT_ALL_TABLES'"
	db, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	repository := repo.NewMySQLProjectRepository(storage.NewSQLStore(db))
	for _, remove := range []bool{false, true} {
		operation := "project.update"
		if remove {
			operation = "project.delete"
		}
		t.Run(operation+" rolls back on audit failure", func(t *testing.T) {
			changed, err := repository.MutateProject(f.ctx,
				app.ProjectMutation{ProjectID: projectID, ProjectTenant: tenantID, ActorSubject: f.subject("reader"), Name: "Uncommitted", Description: "Uncommitted memo", Delete: remove},
				app.ProjectOperation{ProjectID: projectID, ProjectTenantID: tenantID, ActorTenantID: tenantID, ActorSubject: f.subject("reader"), Operation: operation, Outcome: app.ProjectOperationOutcomeAllowed, AuthorizationBasis: app.ProjectAuthorizationOwner, RequestID: strings.Repeat("x", 129)})
			var mysqlErr *mysql.MySQLError
			if changed || !errors.As(err, &mysqlErr) || mysqlErr.Number != 1406 {
				t.Fatalf("audit rollback changed=%t err=%v", changed, err)
			}
			var name, memo, state, deletedAt, updatedBy string
			if err := f.db.QueryRowContext(f.ctx, "SELECT project_name,project_memo,project_state,COALESCE(CAST(deleted_at AS CHAR),''),updated_by FROM projects WHERE project_id=?", owned).Scan(&name, &memo, &state, &deletedAt, &updatedBy); err != nil {
				t.Fatal(err)
			}
			if name != "Original reader" || memo != "Initial memo" || state != "active" || deletedAt != "" || updatedBy != "" {
				t.Fatalf("partial write after rollback: %q %q %q %q %q", name, memo, state, deletedAt, updatedBy)
			}
		})
	}
	sharedPath := "/api/admin/projects/" + shared
	f.request(t, http.MethodPut, sharedPath+"/members", admin, map[string]string{"subject": f.subject("reader"), "role": "editor"}, http.StatusOK, nil)
	sharedID, err := strconv.ParseUint(shared, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	stale := app.ProjectOperation{ProjectID: sharedID, ProjectTenantID: tenantID, ActorTenantID: tenantID, ActorSubject: f.subject("reader"), Operation: "project.update", Outcome: app.ProjectOperationOutcomeAllowed, AuthorizationBasis: app.ProjectAuthorizationEditor, RequestID: "stale-editor"}
	f.request(t, http.MethodDelete, sharedPath+"/members", admin, map[string]string{"subject": f.subject("reader"), "role": "editor"}, http.StatusOK, nil)
	changed, err := repository.MutateProject(f.ctx, app.ProjectMutation{ProjectID: sharedID, ProjectTenant: tenantID, ActorSubject: f.subject("reader"), Name: "Stale editor"}, stale)
	if changed || !errors.Is(err, app.ErrProjectAccessChanged) {
		t.Fatalf("stale editor changed=%t err=%v", changed, err)
	}
	var count int
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM opslogs WHERE project_id IN (?,?) AND operation IN ('project.update','project.delete') AND outcome='allowed'", owned, shared).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed mutations wrote allowed audits: count=%d err=%v", count, err)
	}
	explicitGrant(t, f, "reader", "project.update", true)
	ownedPath := "/api/admin/projects/" + owned
	f.request(t, http.MethodPut, ownedPath, owner, map[string]string{"name": "Original manager"}, http.StatusConflict, nil)
	var name string
	if err := f.db.QueryRowContext(f.ctx, "SELECT project_name FROM projects WHERE project_id=?", owned).Scan(&name); err != nil || name != "Original reader" {
		t.Fatalf("duplicate name mutation changed original: %q err=%v", name, err)
	}
	f.request(t, http.MethodDelete, sharedPath, admin, nil, http.StatusOK, nil)
	f.request(t, http.MethodPut, ownedPath, owner, map[string]string{"name": "Original manager"}, http.StatusOK, nil)
}
