//go:build integration

package smoke

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"

	_ "github.com/go-sql-driver/mysql"
)

const (
	projectMySQLIntegrationID        uint64 = 990001
	projectMySQLIntegrationMissingID uint64 = 990002
	projectMySQLIntegrationDeletedID uint64 = 990003
	projectMySQLIntegrationTenantID  uint64 = 1001
	projectMySQLOtherTenantID        uint64 = 2002
)

func TestProjectMySQLAuthorizationIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the MySQL integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL integration database: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close MySQL integration database: %v", closeErr)
		}
	})
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL integration database: %v", err)
	}
	seedProjectMySQLAuthorizationFixture(t, ctx, db)
	assertProjectMemberTenantForeignKey(t, ctx, db)

	repo := generatedProjectRoot(t)
	configPath := filepath.Join(repo, "etc", "gosky.json")
	originalConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read gosky config before MySQL integration: %v", err)
	}
	t.Cleanup(func() {
		if restoreErr := os.WriteFile(configPath, originalConfig, 0o644); restoreErr != nil {
			t.Errorf("restore gosky config after MySQL integration: %v", restoreErr)
		}
	})

	buildCtx, cancelBuild := context.WithTimeout(t.Context(), 45*time.Second)
	binary := generatedServiceBinary(t, buildCtx, repo)
	cancelBuild()
	reservation := reserveLocalAddrs(t, 4)
	restAddr, rpcAddr, muxAddr, adminAddr := reservation.addresses[0], reservation.addresses[1], reservation.addresses[2], reservation.addresses[3]
	rewriteSmokeConfig(t, repo, restAddr, rpcAddr, muxAddr, adminAddr)
	enableProjectMySQLSmokeConfig(t, repo)

	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GOFLAGS=-count=1", "GOSKY_JWT_SECRET="+smokeJWTSecret, "GOSKY_MYSQL_DSN="+dsn)
	output := strings.Builder{}
	cmd.Stdout = &output
	cmd.Stderr = &output
	reservation.Release()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start MySQL integration service: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			stopGeneratedService(t, cmd, &output)
		}
	})
	waitHTTPStatus(t, ctx, "http://"+restAddr+"/healthz", http.StatusOK, &output)

	client := &http.Client{Timeout: 5 * time.Second}
	tests := []struct {
		name       string
		subject    string
		tenant     string
		projectID  uint64
		wantStatus int
	}{
		{name: "owner", subject: "owner-mysql", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "active member", subject: "member-mysql", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "casbin reader", subject: "project-reader", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "casbin writer inherits reader", subject: "project-writer", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "casbin admin inherits writer and reader", subject: "project-admin", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "same tenant no grant", subject: "viewer-mysql", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusForbidden},
		{name: "cross tenant", subject: "owner-mysql", tenant: "2002", projectID: projectMySQLIntegrationID, wantStatus: http.StatusForbidden},
		{name: "missing project", subject: "missing-reader", tenant: "1001", projectID: projectMySQLIntegrationMissingID, wantStatus: http.StatusNotFound},
		{name: "deleted project", subject: "deleted-reader", tenant: "1001", projectID: projectMySQLIntegrationDeletedID, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+restAddr+"/api/v1/projects/"+strconv.FormatUint(tt.projectID, 10), nil)
			if err != nil {
				t.Fatalf("new Project request: %v", err)
			}
			req.Header.Set("X-Request-ID", "mysql-"+tt.name)
			req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectMySQLIntegrationToken(t, tt.subject, tt.tenant)))
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request Project: %v\n%s", err, output.String())
			}
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Errorf("close Project response: %v", closeErr)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d\n%s", resp.StatusCode, tt.wantStatus, output.String())
			}
		})
	}
	assertProjectMySQLAuditRows(t, ctx, db)
	stopGeneratedService(t, cmd, &output)
	stopped = true
}

func seedProjectMySQLAuthorizationFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	clearProjectMySQLAuthorizationFixture(t, ctx, db)
	t.Cleanup(func() { clearProjectMySQLAuthorizationFixture(t, context.Background(), db) })
	if _, err := db.ExecContext(ctx, `INSERT INTO projects (project_id, tenant_id, owner_subject, project_name, project_memo, created_by, updated_by) VALUES (?, ?, ?, ?, ?, ?, ?)`, projectMySQLIntegrationID, projectMySQLIntegrationTenantID, "owner-mysql", "MySQL integration project", "real persistence verification", "owner-mysql", "owner-mysql"); err != nil {
		t.Fatalf("insert MySQL integration project: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO projects (project_id, tenant_id, owner_subject, project_name, project_memo, project_state, deleted_at, created_by, updated_by) VALUES (?, ?, ?, ?, ?, 'deleted', NOW(6), ?, ?)`, projectMySQLIntegrationDeletedID, projectMySQLIntegrationTenantID, "deleted-owner", "Deleted integration project", "soft deleted persistence verification", "deleted-owner", "deleted-owner"); err != nil {
		t.Fatalf("insert deleted MySQL integration project: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_members (project_id, project_tenant_id, member_subject, member_role, member_state, granted_by) VALUES (?, ?, ?, 'collaborator', 'active', ?)`, projectMySQLIntegrationID, projectMySQLIntegrationTenantID, "member-mysql", "owner-mysql"); err != nil {
		t.Fatalf("insert MySQL integration member: %v", err)
	}
}

func clearProjectMySQLAuthorizationFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`DELETE FROM opslogs WHERE project_id IN (?, ?, ?)`,
		`DELETE FROM project_members WHERE project_id IN (?, ?, ?)`,
		`DELETE FROM projects WHERE project_id IN (?, ?, ?)`,
	} {
		args := []any{projectMySQLIntegrationID, projectMySQLIntegrationMissingID, projectMySQLIntegrationDeletedID}
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("clear MySQL integration fixture: %v", err)
		}
	}
}

func assertProjectMemberTenantForeignKey(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO project_members (project_id, project_tenant_id, member_subject, member_role, member_state, granted_by) VALUES (?, ?, ?, 'member', 'active', ?)`, projectMySQLIntegrationID, projectMySQLOtherTenantID, "cross-tenant-member", "owner-mysql")
	if err == nil {
		t.Fatal("insert cross-tenant Project member succeeded, want composite foreign-key rejection")
	}
}

func enableProjectMySQLSmokeConfig(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, "etc", "gosky.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read gosky config for MySQL integration: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode gosky config for MySQL integration: %v", err)
	}
	projectStore := jsonObject(t, cfg, "projectStore")
	projectStore["enabled"] = true
	projectStore["driver"] = "mysql"
	projectStore["dsnEnv"] = "GOSKY_MYSQL_DSN"
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("encode MySQL integration config: %v", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatalf("write MySQL integration config: %v", err)
	}
}

func projectMySQLIntegrationToken(t *testing.T, subject string, tenant string) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   subject,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra:     map[string]any{"tenant_id": tenant},
	}, []byte(smokeJWTSecret))
	if err != nil {
		t.Fatalf("sign MySQL integration JWT: %v", err)
	}
	return token
}

func assertProjectMySQLAuditRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT operator_subject, operator_tenant_id, project_tenant_id, project_id, outcome, auth_basis, reason, error_code, error_body, trace_id FROM opslogs WHERE project_id IN (?, ?, ?) ORDER BY opslog_id`, projectMySQLIntegrationID, projectMySQLIntegrationMissingID, projectMySQLIntegrationDeletedID)
	if err != nil {
		t.Fatalf("query MySQL Project audit rows: %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close MySQL Project audit rows: %v", closeErr)
		}
	}()
	want := map[projectAuditKey]string{
		{subject: "owner-mysql", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}:    "allowed/owner",
		{subject: "member-mysql", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}:   "allowed/member",
		{subject: "project-reader", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}: "allowed/casbin_policy",
		{subject: "project-writer", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}: "allowed/casbin_policy",
		{subject: "project-admin", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}:  "allowed/casbin_policy",
		{subject: "viewer-mysql", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}:   "denied/no_grant",
		{subject: "owner-mysql", operatorTenantID: projectMySQLOtherTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}:          "denied/cross_tenant",
		{subject: "missing-reader", operatorTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationMissingID}:                                            "not_found/not_found",
		{subject: "deleted-reader", operatorTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationDeletedID}:                                            "not_found/not_found",
	}
	got := make(map[projectAuditKey]string)
	for rows.Next() {
		var subject string
		var operatorTenantID uint64
		var projectTenantID uint64
		var projectID uint64
		var outcome string
		var basis string
		var reason string
		var errorCode string
		var errorBody string
		var traceID string
		if err := rows.Scan(&subject, &operatorTenantID, &projectTenantID, &projectID, &outcome, &basis, &reason, &errorCode, &errorBody, &traceID); err != nil {
			t.Fatalf("scan MySQL Project audit row: %v", err)
		}
		if reason != "" || errorCode != "" || errorBody != "" || traceID != "" {
			t.Fatalf("audit detail fields = reason=%q errorCode=%q errorBody=%q traceID=%q, want empty", reason, errorCode, errorBody, traceID)
		}
		got[projectAuditKey{subject: subject, operatorTenantID: operatorTenantID, projectTenantID: projectTenantID, projectID: projectID}] = outcome + "/" + basis
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate MySQL Project audit rows: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("MySQL Project audit rows = %#v, want %#v", got, want)
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Fatalf("MySQL Project audit for %+v = %q, want %q; all=%#v", key, got[key], expected, got)
		}
	}
}

type projectAuditKey struct {
	subject          string
	operatorTenantID uint64
	projectTenantID  uint64
	projectID        uint64
}
