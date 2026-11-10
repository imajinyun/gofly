//go:build integration

package routes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/model/repo"
)

// AUTHZ-054..056: reuse real identity/policy wiring but create only fresh
// tenant-owned Projects and logs. No fixed IDs or global log cleanup are used.
func TestMySQLProjectPersistenceBoundaryIntegration(t *testing.T) {
	f := newRoleMenuFixture(t)
	admin := f.login(t, "manager", 0, "admin")
	owner := f.login(t, "reader", 0, "admin")
	front := f.login(t, "reader", 0, "front")
	otherTenant := f.login(t, "reader", 1, "admin")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, tenant := range f.domains {
			for _, query := range []string{
				"DELETE FROM opslogs WHERE operator_tenant_id = ?",
				"DELETE FROM project_members WHERE project_tenant_id = ?",
				"DELETE FROM projects WHERE tenant_id = ?",
			} {
				if _, err := f.db.ExecContext(ctx, query, tenant); err != nil {
					t.Errorf("cleanup Project fixture: %v", err)
				}
			}
		}
	})
	seed := func(tenant, subject, name string) string {
		result, err := f.db.ExecContext(f.ctx, "INSERT INTO projects (tenant_id, owner_subject, project_name, project_memo) VALUES (?, ?, ?, '')", tenant, subject, name)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil || id <= 0 {
			t.Fatalf("insert Project ID=%d, err=%v", id, err)
		}
		return strconv.FormatInt(id, 10)
	}
	owned := seed(f.domains[0], f.subject("reader"), "Owned Project")
	shared := seed(f.domains[0], f.subject("manager"), "Shared Project")
	foreign := seed(f.domains[1], f.subject("reader"), "Foreign Project")
	path := func(id string) string { return "/api/admin/projects/" + id }
	for _, tc := range []struct {
		name, id, token string
		status          int
	}{
		{"owner", owned, owner, http.StatusOK},
		{"tenant admin", owned, admin, http.StatusOK},
		{"unrelated reader", shared, owner, http.StatusForbidden},
		{"cross tenant owner", owned, otherTenant, http.StatusForbidden},
		{"cross tenant resource", foreign, owner, http.StatusForbidden},
		{"missing identity", owned, "", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response app.ProjectResponse
			f.request(t, http.MethodGet, path(tc.id), tc.token, nil, tc.status, &response)
			if tc.status == http.StatusOK && response.ID != tc.id {
				t.Errorf("Project ID=%q, want %q", response.ID, tc.id)
			}
		})
	}
	var list app.ProjectListResponse
	f.request(t, http.MethodGet, "/api/admin/projects", owner, nil, http.StatusOK, &list)
	if len(list.Items) != 1 || list.Items[0].ID != owned {
		t.Fatalf("owner scope=%+v", list)
	}
	f.request(t, http.MethodGet, "/api/admin/projects", admin, nil, http.StatusOK, &list)
	if len(list.Items) != 2 {
		t.Fatalf("admin scope=%+v", list)
	}
	f.request(t, http.MethodGet, "/api/front/projects/"+owned, front, nil, http.StatusOK, nil)
	f.request(t, http.MethodGet, "/api/front/projects/"+shared, front, nil, http.StatusForbidden, nil)

	var mutation app.ProjectMemberMutationResponse
	for _, role := range []string{"viewer", "editor"} {
		f.request(t, http.MethodPut, path(shared)+"/members", admin, map[string]string{"subject": f.subject("reader"), "role": role}, http.StatusOK, &mutation)
		if !mutation.Changed {
			t.Fatalf("grant %s did not change relation", role)
		}
		f.request(t, http.MethodGet, path(shared), owner, nil, http.StatusOK, nil)
	}
	f.request(t, http.MethodGet, path(shared)+"/members", owner, nil, http.StatusForbidden, nil)
	var members app.ProjectMemberListResponse
	f.request(t, http.MethodGet, path(shared)+"/members", admin, nil, http.StatusOK, &members)
	if members.OwnerSubject != f.subject("manager") || len(members.Items) != 1 || members.Items[0].Role != app.ProjectMemberRoleEditor {
		t.Fatalf("collaborator list=%+v", members)
	}
	f.request(t, http.MethodGet, "/api/admin/projects?limit=1", owner, nil, http.StatusOK, &list)
	if len(list.Items) != 1 || list.Items[0].ID != owned || list.NextCursor != owned {
		t.Fatalf("first page=%+v", list)
	}
	list = app.ProjectListResponse{}
	f.request(t, http.MethodGet, "/api/admin/projects?limit=1&cursor="+owned, owner, nil, http.StatusOK, &list)
	if len(list.Items) != 1 || list.Items[0].ID != shared || list.NextCursor != "" {
		t.Fatalf("last page=%+v", list)
	}
	f.request(t, http.MethodDelete, path(shared)+"/members", admin, map[string]string{"subject": f.subject("reader"), "role": "viewer"}, http.StatusOK, &mutation)
	if mutation.Changed {
		t.Fatal("stale viewer revoke removed editor")
	}
	f.request(t, http.MethodGet, path(shared), owner, nil, http.StatusOK, nil)
	f.request(t, http.MethodDelete, path(shared)+"/members", admin, map[string]string{"subject": f.subject("reader"), "role": "editor"}, http.StatusOK, &mutation)
	if !mutation.Changed {
		t.Fatal("editor revoke did not change relation")
	}
	f.request(t, http.MethodGet, path(shared), owner, nil, http.StatusForbidden, nil)
	explicitGrant(t, f, "reader", "project.member.manage", true)
	f.request(t, http.MethodPut, path(owned)+"/members", owner, map[string]string{"subject": f.subject("manager"), "role": "viewer"}, http.StatusOK, &mutation)
	if !mutation.Changed {
		t.Fatal("non-admin Project owner could not grant collaborator")
	}

	// Force failure after the relationship write on a dedicated strict-mode
	// pool. Normal HTTP input is bounded and cannot inject this audit failure.
	dsn, err := mysql.ParseDSN(os.Getenv("GOSKY_MYSQL_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if dsn.Params == nil {
		dsn.Params = make(map[string]string)
	}
	dsn.Params["sql_mode"] = "'STRICT_ALL_TABLES'"
	probeDB, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = probeDB.Close() })
	probeDB.SetMaxOpenConns(1)
	repository := repo.NewMySQLProjectRepository(storage.NewSQLStore(probeDB))
	projectID, err := strconv.ParseUint(owned, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := strconv.ParseUint(f.domains[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	target := f.subject("rollback")
	changed, err := repository.MutateProjectMember(f.ctx,
		app.ProjectMemberMutation{ProjectID: projectID, ProjectTenant: tenantID, Subject: target, Role: app.ProjectMemberRoleViewer, ActorSubject: f.subject("reader")},
		app.ProjectOperation{ActorTenantID: tenantID, ProjectTenantID: tenantID, ProjectID: projectID, ActorSubject: f.subject("reader"), TargetSubject: target, TargetRole: "viewer", Operation: app.ProjectMemberUpsertOperation, Outcome: app.ProjectOperationOutcomeAllowed, AuthorizationBasis: app.ProjectAuthorizationOwner, RequestID: strings.Repeat("x", 129)},
	)
	var mysqlError *mysql.MySQLError
	if changed || !errors.As(err, &mysqlError) || mysqlError.Number != 1406 {
		t.Fatalf("audit failure changed=%t err=%v, want MySQL data-too-long error", changed, err)
	}
	var count int
	if err := f.db.QueryRowContext(f.ctx, "SELECT COUNT(*) FROM project_members WHERE project_id=? AND member_subject=?", projectID, target).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back member count=%d err=%v", count, err)
	}

	if _, err := f.db.ExecContext(f.ctx, "UPDATE projects SET project_state='deleted', deleted_at=NOW(6) WHERE project_id=?", owned); err != nil {
		t.Fatal(err)
	}
	f.request(t, http.MethodGet, path(owned), owner, nil, http.StatusNotFound, nil)
	if _, err := f.db.ExecContext(f.ctx, "DELETE FROM projects WHERE project_id=?", owned); err != nil {
		t.Fatal(err)
	}
	f.request(t, http.MethodGet, path(owned), owner, nil, http.StatusNotFound, nil)
	rows, err := f.db.QueryContext(f.ctx, "SELECT outcome,COUNT(*) FROM opslogs WHERE operator_tenant_id IN (?,?) GROUP BY outcome", f.domains[0], f.domains[1])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	outcomes := make(map[string]int)
	for rows.Next() {
		var outcome string
		var n int
		if err := rows.Scan(&outcome, &n); err != nil {
			t.Fatal(err)
		}
		outcomes[outcome] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if outcomes["allowed"] == 0 || outcomes["denied"] == 0 || outcomes["not_found"] != 2 {
		t.Fatalf("audit outcomes=%v", outcomes)
	}
	t.Logf("owner/viewer/editor/admin=verified tenantFence=verified cursor=verified staleRevoke=verified auditRollback=verified auditOutcomes=%v", outcomes)
}
