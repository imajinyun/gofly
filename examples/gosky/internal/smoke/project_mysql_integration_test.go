//go:build integration

package smoke

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	projectMySQLIntegrationOtherID   uint64 = 990004
	projectMySQLIntegrationTenantID  uint64 = 1001
	projectMySQLOtherTenantID        uint64 = 2002
	projectMySQLIdentityUserBase     uint64 = 990100
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
	seedProjectMySQLIdentityFixture(t, ctx, db)
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
		{name: "active viewer", subject: "member-mysql", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "casbin reader without relation", subject: "project-reader", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusForbidden},
		{name: "casbin writer without relation", subject: "project-writer", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusForbidden},
		{name: "casbin admin reads tenant project", subject: "project-admin", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "same tenant no grant", subject: "viewer-mysql", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusForbidden},
		{name: "cross tenant", subject: "owner-mysql", tenant: "2002", projectID: projectMySQLIntegrationID, wantStatus: http.StatusForbidden},
		{name: "missing project", subject: "missing-reader", tenant: "1001", projectID: projectMySQLIntegrationMissingID, wantStatus: http.StatusNotFound},
		{name: "deleted project", subject: "deleted-reader", tenant: "1001", projectID: projectMySQLIntegrationDeletedID, wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+restAddr+"/api/admin/projects/"+strconv.FormatUint(tt.projectID, 10), nil)
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
	memberListTests := []struct {
		name       string
		subject    string
		tenant     string
		wantStatus int
		wantOwner  string
		wantItems  []projectMemberListItem
	}{
		{name: "owner lists collaborators", subject: "owner-mysql", tenant: "1001", wantStatus: http.StatusOK, wantOwner: "owner-mysql", wantItems: []projectMemberListItem{{Subject: "editor-mysql", Role: "editor"}, {Subject: "member-mysql", Role: "viewer"}}},
		{name: "tenant admin lists collaborators", subject: "project-admin", tenant: "1001", wantStatus: http.StatusOK, wantOwner: "owner-mysql", wantItems: []projectMemberListItem{{Subject: "editor-mysql", Role: "editor"}, {Subject: "member-mysql", Role: "viewer"}}},
		{name: "related collaborator cannot list", subject: "member-mysql", tenant: "1001", wantStatus: http.StatusForbidden},
		{name: "cross tenant owner cannot list", subject: "owner-mysql", tenant: "2002", wantStatus: http.StatusForbidden},
	}
	for _, tt := range memberListTests {
		t.Run(tt.name, func(t *testing.T) {
			response, status := requestProjectMemberList(t, ctx, client, restAddr, tt.subject, tt.tenant, output.String())
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
			if status == http.StatusOK && (response.OwnerSubject != tt.wantOwner || !equalProjectMemberListItems(response.Items, tt.wantItems)) {
				t.Fatalf("member list = %#v, want owner=%q items=%#v", response, tt.wantOwner, tt.wantItems)
			}
		})
	}
	memberMutationTests := []struct {
		name        string
		method      string
		subject     string
		tenant      string
		target      string
		role        string
		wantStatus  int
		wantChanged bool
	}{
		{name: "owner grants viewer", method: http.MethodPut, subject: "owner-mysql", tenant: "1001", target: "managed-viewer", role: "viewer", wantStatus: http.StatusOK, wantChanged: true},
		{name: "tenant admin grants editor", method: http.MethodPut, subject: "project-admin", tenant: "1001", target: "managed-editor", role: "editor", wantStatus: http.StatusOK, wantChanged: true},
		{name: "reader cannot grant viewer", method: http.MethodPut, subject: "project-reader", tenant: "1001", target: "blocked-viewer", role: "viewer", wantStatus: http.StatusForbidden},
		{name: "cross tenant owner cannot revoke viewer", method: http.MethodDelete, subject: "owner-mysql", tenant: "2002", target: "managed-viewer", role: "viewer", wantStatus: http.StatusForbidden},
		{name: "owner revokes viewer", method: http.MethodDelete, subject: "owner-mysql", tenant: "1001", target: "managed-viewer", role: "viewer", wantStatus: http.StatusOK, wantChanged: true},
	}
	for _, tt := range memberMutationTests {
		t.Run(tt.name, func(t *testing.T) {
			changed, status := requestProjectMemberMutation(t, ctx, client, restAddr, tt.method, tt.subject, tt.tenant, tt.target, tt.role, output.String())
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
			if status == http.StatusOK && changed != tt.wantChanged {
				t.Fatalf("changed = %t, want %t", changed, tt.wantChanged)
			}
		})
	}
	projectListTests := []struct {
		name    string
		subject string
		tenant  string
		wantIDs []string
	}{
		{name: "owner relation scope", subject: "owner-mysql", tenant: "1001", wantIDs: []string{"990001"}},
		{name: "viewer relation scope", subject: "member-mysql", tenant: "1001", wantIDs: []string{"990001"}},
		{name: "reader has no relation scope", subject: "project-reader", tenant: "1001", wantIDs: []string{}},
		{name: "admin sees tenant scope", subject: "project-admin", tenant: "1001", wantIDs: []string{"990001", "990004"}},
	}
	for _, tt := range projectListTests {
		t.Run(tt.name, func(t *testing.T) {
			items, status := requestProjectList(t, ctx, client, restAddr, tt.subject, tt.tenant, output.String())
			if status != http.StatusOK {
				t.Fatalf("status = %d, want %d", status, http.StatusOK)
			}
			gotIDs := make([]string, len(items))
			for index, item := range items {
				gotIDs[index] = item.ID
			}
			if !equalStringSlices(gotIDs, tt.wantIDs) {
				t.Fatalf("Project list IDs = %#v, want %#v", gotIDs, tt.wantIDs)
			}
		})
	}
	frontDetailTests := []struct {
		name       string
		subject    string
		tenant     string
		projectID  uint64
		wantStatus int
	}{
		{name: "front owner", subject: "owner-mysql", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "front active viewer", subject: "member-mysql", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusOK},
		{name: "front admin policy without relation", subject: "project-admin", tenant: "1001", projectID: projectMySQLIntegrationID, wantStatus: http.StatusForbidden},
		{name: "front cross tenant owner", subject: "owner-mysql", tenant: "2002", projectID: projectMySQLIntegrationID, wantStatus: http.StatusForbidden},
	}
	for _, tt := range frontDetailTests {
		t.Run(tt.name, func(t *testing.T) {
			status := requestFrontProjectDetail(t, ctx, client, restAddr, tt.name, tt.subject, tt.tenant, tt.projectID, output.String())
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
		})
	}
	frontListTests := []struct {
		name    string
		subject string
		tenant  string
		wantIDs []string
	}{
		{name: "front owner relation scope", subject: "owner-mysql", tenant: "1001", wantIDs: []string{"990001"}},
		{name: "front viewer relation scope", subject: "member-mysql", tenant: "1001", wantIDs: []string{"990001"}},
		{name: "front admin policy remains relation scope", subject: "project-admin", tenant: "1001", wantIDs: []string{}},
	}
	for _, tt := range frontListTests {
		t.Run(tt.name, func(t *testing.T) {
			items, status := requestFrontProjectList(t, ctx, client, restAddr, tt.name, tt.subject, tt.tenant, output.String())
			if status != http.StatusOK {
				t.Fatalf("status = %d, want %d", status, http.StatusOK)
			}
			gotIDs := make([]string, len(items))
			for index, item := range items {
				gotIDs[index] = item.ID
			}
			if !equalStringSlices(gotIDs, tt.wantIDs) {
				t.Fatalf("front Project list IDs = %#v, want %#v", gotIDs, tt.wantIDs)
			}
		})
	}
	assertProjectMySQLAuditRows(t, ctx, db)
	assertProjectMySQLMemberMutationRows(t, ctx, db)
	assertProjectMySQLMemberMutationAuditRows(t, ctx, db)
	assertProjectMySQLListAuditRows(t, ctx, db)
	assertProjectMySQLMemberListAuditRows(t, ctx, db)
	assertFrontProjectMySQLAuditRows(t, ctx, db)
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
	if _, err := db.ExecContext(ctx, `INSERT INTO projects (project_id, tenant_id, owner_subject, project_name, project_memo, created_by, updated_by) VALUES (?, ?, ?, ?, ?, ?, ?)`, projectMySQLIntegrationOtherID, projectMySQLIntegrationTenantID, "other-owner", "MySQL integration tenant project", "tenant scope verification", "other-owner", "other-owner"); err != nil {
		t.Fatalf("insert second MySQL integration project: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_members (project_id, project_tenant_id, member_subject, member_role, member_state, granted_by) VALUES (?, ?, ?, 'viewer', 'active', ?)`, projectMySQLIntegrationID, projectMySQLIntegrationTenantID, "member-mysql", "owner-mysql"); err != nil {
		t.Fatalf("insert MySQL integration member: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_members (project_id, project_tenant_id, member_subject, member_role, member_state, granted_by) VALUES (?, ?, ?, 'editor', 'active', ?)`, projectMySQLIntegrationID, projectMySQLIntegrationTenantID, "editor-mysql", "owner-mysql"); err != nil {
		t.Fatalf("insert MySQL integration editor: %v", err)
	}
}

func clearProjectMySQLAuthorizationFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`DELETE FROM opslogs WHERE project_id IN (?, ?, ?, ?) OR project_id = 0`,
		`DELETE FROM project_members WHERE project_id IN (?, ?, ?, ?)`,
		`DELETE FROM projects WHERE project_id IN (?, ?, ?, ?)`,
	} {
		args := []any{projectMySQLIntegrationID, projectMySQLIntegrationMissingID, projectMySQLIntegrationDeletedID, projectMySQLIntegrationOtherID}
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("clear MySQL integration fixture: %v", err)
		}
	}
}

type projectMySQLIdentityFixture struct {
	userID  uint64
	subject string
	tenants []uint64
}

var projectMySQLIdentityFixtures = []projectMySQLIdentityFixture{
	{userID: projectMySQLIdentityUserBase + 1, subject: "owner-mysql", tenants: []uint64{projectMySQLIntegrationTenantID, projectMySQLOtherTenantID}},
	{userID: projectMySQLIdentityUserBase + 2, subject: "member-mysql", tenants: []uint64{projectMySQLIntegrationTenantID}},
	{userID: projectMySQLIdentityUserBase + 3, subject: "project-reader", tenants: []uint64{projectMySQLIntegrationTenantID}},
	{userID: projectMySQLIdentityUserBase + 4, subject: "project-writer", tenants: []uint64{projectMySQLIntegrationTenantID}},
	{userID: projectMySQLIdentityUserBase + 5, subject: "project-admin", tenants: []uint64{projectMySQLIntegrationTenantID}},
	{userID: projectMySQLIdentityUserBase + 6, subject: "viewer-mysql", tenants: []uint64{projectMySQLIntegrationTenantID}},
	{userID: projectMySQLIdentityUserBase + 7, subject: "missing-reader", tenants: []uint64{projectMySQLIntegrationTenantID}},
	{userID: projectMySQLIdentityUserBase + 8, subject: "deleted-reader", tenants: []uint64{projectMySQLIntegrationTenantID}},
	{userID: projectMySQLIdentityUserBase + 9, subject: "editor-mysql", tenants: []uint64{projectMySQLIntegrationTenantID}},
}

func seedProjectMySQLIdentityFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	clearProjectMySQLIdentityFixture(t, ctx, db)
	t.Cleanup(func() { clearProjectMySQLIdentityFixture(t, context.Background(), db) })
	for _, tenant := range []uint64{projectMySQLIntegrationTenantID, projectMySQLOtherTenantID} {
		if _, err := db.ExecContext(ctx, `INSERT INTO tenants (tenant_id, tenant_code, tenant_name, tenant_state)
VALUES (?, ?, ?, 'active')
ON DUPLICATE KEY UPDATE tenant_code = VALUES(tenant_code), tenant_name = VALUES(tenant_name), tenant_state = 'active'`, tenant, "project-mysql-smoke-"+strconv.FormatUint(tenant, 10), "Project MySQL smoke tenant"); err != nil {
			t.Fatalf("upsert MySQL identity tenant %d: %v", tenant, err)
		}
	}
	now := time.Now().UTC()
	for _, fixture := range projectMySQLIdentityFixtures {
		if _, err := db.ExecContext(ctx, `INSERT INTO users (user_id, subject, login_name, password_hash, account_type, user_state)
VALUES (?, ?, ?, ?, 'both', 'active')
ON DUPLICATE KEY UPDATE subject = VALUES(subject), login_name = VALUES(login_name), account_type = 'both', user_state = 'active'`, fixture.userID, fixture.subject, "project-mysql-"+fixture.subject, "not-used-by-session-smoke"); err != nil {
			t.Fatalf("upsert MySQL identity user %q: %v", fixture.subject, err)
		}
		for _, tenant := range fixture.tenants {
			if _, err := db.ExecContext(ctx, `INSERT INTO tenant_users (tenant_id, user_id, member_realm, member_state)
VALUES (?, ?, 'both', 'active')
ON DUPLICATE KEY UPDATE member_realm = 'both', member_state = 'active', left_at = NULL`, tenant, fixture.userID); err != nil {
				t.Fatalf("upsert MySQL identity membership subject=%q tenant=%d: %v", fixture.subject, tenant, err)
			}
			for _, realm := range []string{"admin", "front"} {
				sessionID := projectMySQLIdentitySessionIDForRealm(fixture.subject, tenant, realm)
				if _, err := db.ExecContext(ctx, `INSERT INTO auth_sessions (session_id, user_id, tenant_id, realm, refresh_token_hash, issued_at, expires_at, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE user_id = VALUES(user_id), tenant_id = VALUES(tenant_id), realm = VALUES(realm), refresh_token_hash = VALUES(refresh_token_hash), issued_at = VALUES(issued_at), expires_at = VALUES(expires_at), last_seen_at = VALUES(last_seen_at), revoked_at = NULL`, sessionID, fixture.userID, tenant, realm, projectMySQLIdentityRefreshHash(sessionID), now.Add(-time.Minute), now.Add(time.Hour), now); err != nil {
					t.Fatalf("upsert MySQL identity session subject=%q tenant=%d realm=%q: %v", fixture.subject, tenant, realm, err)
				}
			}
		}
	}
}

func clearProjectMySQLIdentityFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	userIDs := make([]any, 0, len(projectMySQLIdentityFixtures))
	for _, fixture := range projectMySQLIdentityFixtures {
		userIDs = append(userIDs, fixture.userID)
	}
	for _, query := range []string{
		`DELETE FROM auth_login_logs WHERE user_id IN (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		`DELETE FROM auth_sessions WHERE user_id IN (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		`DELETE FROM tenant_users WHERE user_id IN (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		`DELETE FROM users WHERE user_id IN (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	} {
		if _, err := db.ExecContext(ctx, query, userIDs...); err != nil {
			t.Fatalf("clear MySQL identity smoke fixture: %v", err)
		}
	}
}

func projectMySQLIdentitySessionID(subject string, tenant uint64) string {
	return projectMySQLIdentitySessionIDForRealm(subject, tenant, "admin")
}

func projectMySQLIdentitySessionIDForRealm(subject string, tenant uint64, realm string) string {
	digest := sha256.Sum256([]byte(subject + ":" + strconv.FormatUint(tenant, 10) + ":" + realm))
	return hex.EncodeToString(digest[:])[:32]
}

func projectMySQLIdentityRefreshHash(sessionID string) string {
	digest := sha256.Sum256([]byte("project-mysql-smoke-refresh:" + sessionID))
	return hex.EncodeToString(digest[:])
}

func assertProjectMemberTenantForeignKey(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `INSERT INTO project_members (project_id, project_tenant_id, member_subject, member_role, member_state, granted_by) VALUES (?, ?, ?, 'viewer', 'active', ?)`, projectMySQLIntegrationID, projectMySQLOtherTenantID, "cross-tenant-member", "owner-mysql")
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
	identityStore := map[string]any{"enabled": true, "driver": "mysql", "dsnEnv": "GOSKY_MYSQL_DSN", "issuer": "gosky", "audience": "gosky-web"}
	cfg["identityStore"] = identityStore
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("encode MySQL integration config: %v", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		t.Fatalf("write MySQL integration config: %v", err)
	}
}

func projectMySQLIntegrationToken(t *testing.T, subject string, tenant string) string {
	return projectMySQLIntegrationTokenForRealm(t, subject, tenant, "admin")
}

func projectMySQLIntegrationTokenForRealm(t *testing.T, subject string, tenant, realm string) string {
	t.Helper()
	token, err := auth.SignJWT(auth.JWTClaims{
		Subject:   subject,
		Issuer:    "gosky",
		Audience:  "gosky-web",
		IssuedAt:  time.Now().Add(-time.Minute).Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		Extra: map[string]any{
			"tenant_id": tenant,
			"realm":     realm,
			"sid":       projectMySQLIdentitySessionIDForRealm(subject, mustProjectMySQLTenantID(t, tenant), realm),
		},
	}, []byte(smokeJWTSecret))
	if err != nil {
		t.Fatalf("sign MySQL integration JWT: %v", err)
	}
	return token
}

func mustProjectMySQLTenantID(t *testing.T, tenant string) uint64 {
	t.Helper()
	value, err := strconv.ParseUint(tenant, 10, 64)
	if err != nil {
		t.Fatalf("parse MySQL integration tenant %q: %v", tenant, err)
	}
	return value
}

func requestProjectMemberMutation(t *testing.T, ctx context.Context, client *http.Client, restAddr, method, subject, tenant, target, role, output string) (bool, int) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"subject": target, "role": role})
	if err != nil {
		t.Fatalf("marshal Project member mutation: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+restAddr+"/api/admin/projects/"+strconv.FormatUint(projectMySQLIntegrationID, 10)+"/members", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("new Project member mutation request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "mysql-member-"+strings.ReplaceAll(target, "-", "_"))
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectMySQLIntegrationToken(t, subject, tenant)))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request Project member mutation: %v\n%s", err, output)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close Project member mutation response: %v", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return false, resp.StatusCode
	}
	var response struct {
		Changed bool `json:"changed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("decode Project member mutation response: %v", err)
	}
	return response.Changed, resp.StatusCode
}

func requestProjectList(t *testing.T, ctx context.Context, client *http.Client, restAddr, subject, tenant, output string) ([]projectListItem, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+restAddr+"/api/admin/projects?limit=10", nil)
	if err != nil {
		t.Fatalf("new Project list request: %v", err)
	}
	req.Header.Set("X-Request-ID", "mysql-list-"+subject)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectMySQLIntegrationToken(t, subject, tenant)))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request Project list: %v\n%s", err, output)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close Project list response: %v", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	var response struct {
		Items []projectListItem `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("decode Project list response: %v", err)
	}
	return response.Items, resp.StatusCode
}

func requestFrontProjectDetail(t *testing.T, ctx context.Context, client *http.Client, restAddr, name, subject, tenant string, projectID uint64, output string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+restAddr+"/api/front/projects/"+strconv.FormatUint(projectID, 10), nil)
	if err != nil {
		t.Fatalf("new front Project detail request: %v", err)
	}
	req.Header.Set("X-Request-ID", "mysql-front-detail-"+strings.ReplaceAll(name, " ", "-"))
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectMySQLIntegrationTokenForRealm(t, subject, tenant, "front")))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request front Project detail: %v\n%s", err, output)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close front Project detail response: %v", closeErr)
		}
	}()
	return resp.StatusCode
}

func requestFrontProjectList(t *testing.T, ctx context.Context, client *http.Client, restAddr, name, subject, tenant, output string) ([]projectListItem, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+restAddr+"/api/front/projects?limit=10", nil)
	if err != nil {
		t.Fatalf("new front Project list request: %v", err)
	}
	req.Header.Set("X-Request-ID", "mysql-front-list-"+strings.ReplaceAll(name, " ", "-"))
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectMySQLIntegrationTokenForRealm(t, subject, tenant, "front")))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request front Project list: %v\n%s", err, output)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close front Project list response: %v", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	var response struct {
		Items []projectListItem `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("decode front Project list response: %v", err)
	}
	return response.Items, resp.StatusCode
}

func requestProjectMemberList(t *testing.T, ctx context.Context, client *http.Client, restAddr, subject, tenant, output string) (projectMemberListResponse, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+restAddr+"/api/admin/projects/"+strconv.FormatUint(projectMySQLIntegrationID, 10)+"/members?limit=10", nil)
	if err != nil {
		t.Fatalf("new Project member-list request: %v", err)
	}
	req.Header.Set("X-Request-ID", "mysql-member-list-"+subject)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(projectMySQLIntegrationToken(t, subject, tenant)))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request Project member list: %v\n%s", err, output)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close Project member list response: %v", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return projectMemberListResponse{}, resp.StatusCode
	}
	var response projectMemberListResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("decode Project member list response: %v", err)
	}
	return response, resp.StatusCode
}

type projectListItem struct {
	ID string `json:"id"`
}

type projectMemberListResponse struct {
	OwnerSubject string                  `json:"ownerSubject"`
	Items        []projectMemberListItem `json:"items"`
}

type projectMemberListItem struct {
	Subject string `json:"subject"`
	Role    string `json:"role"`
}

func assertProjectMySQLAuditRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT operator_subject, operator_tenant_id, project_tenant_id, project_id, outcome, auth_basis, reason, error_code, error_body, trace_id FROM opslogs WHERE project_id IN (?, ?, ?) AND operation = 'project.read' AND request_id NOT LIKE 'mysql-front-%' ORDER BY opslog_id`, projectMySQLIntegrationID, projectMySQLIntegrationMissingID, projectMySQLIntegrationDeletedID)
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
		{subject: "member-mysql", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}:   "allowed/viewer",
		{subject: "project-reader", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}: "denied/no_grant",
		{subject: "project-writer", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}: "denied/no_grant",
		{subject: "project-admin", operatorTenantID: projectMySQLIntegrationTenantID, projectTenantID: projectMySQLIntegrationTenantID, projectID: projectMySQLIntegrationID}:  "allowed/tenant_admin",
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

func assertProjectMySQLMemberMutationRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT member_subject, member_role, member_state, granted_by, revoked_by FROM project_members WHERE project_id = ? AND member_subject IN (?, ?, ?) ORDER BY member_subject`, projectMySQLIntegrationID, "blocked-viewer", "managed-editor", "managed-viewer")
	if err != nil {
		t.Fatalf("query MySQL Project member mutation rows: %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close MySQL Project member mutation rows: %v", closeErr)
		}
	}()
	type memberState struct {
		role      string
		state     string
		grantedBy string
		revokedBy string
	}
	got := make(map[string]memberState)
	for rows.Next() {
		var subject string
		var state memberState
		if err := rows.Scan(&subject, &state.role, &state.state, &state.grantedBy, &state.revokedBy); err != nil {
			t.Fatalf("scan MySQL Project member mutation row: %v", err)
		}
		got[subject] = state
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate MySQL Project member mutation rows: %v", err)
	}
	if _, exists := got["blocked-viewer"]; exists {
		t.Fatalf("reader-created member exists: %#v", got["blocked-viewer"])
	}
	if editor := got["managed-editor"]; editor != (memberState{role: "editor", state: "active", grantedBy: "project-admin"}) {
		t.Fatalf("managed editor = %#v, want active editor granted by tenant admin", editor)
	}
	if viewer := got["managed-viewer"]; viewer != (memberState{role: "viewer", state: "revoked", grantedBy: "owner-mysql", revokedBy: "owner-mysql"}) {
		t.Fatalf("managed viewer = %#v, want revoked viewer granted and revoked by owner", viewer)
	}
}

func assertProjectMySQLMemberMutationAuditRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT operator_subject, operator_tenant_id, operation, outcome, auth_basis, target_subject, target_role FROM opslogs WHERE project_id = ? AND operation IN ('project.member.upsert', 'project.member.revoke') ORDER BY opslog_id`, projectMySQLIntegrationID)
	if err != nil {
		t.Fatalf("query MySQL Project member mutation audit rows: %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close MySQL Project member mutation audit rows: %v", closeErr)
		}
	}()
	got := make(map[string]string)
	for rows.Next() {
		var subject, operation, outcome, basis, target, role string
		var tenantID uint64
		if err := rows.Scan(&subject, &tenantID, &operation, &outcome, &basis, &target, &role); err != nil {
			t.Fatalf("scan MySQL Project member mutation audit row: %v", err)
		}
		got[subject+"/"+strconv.FormatUint(tenantID, 10)+"/"+operation+"/"+target] = outcome + "/" + basis + "/" + role
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate MySQL Project member mutation audit rows: %v", err)
	}
	want := map[string]string{
		"owner-mysql/1001/project.member.upsert/managed-viewer":    "allowed/owner/viewer",
		"project-admin/1001/project.member.upsert/managed-editor":  "allowed/tenant_admin/editor",
		"project-reader/1001/project.member.upsert/blocked-viewer": "denied/no_grant/viewer",
		"owner-mysql/2002/project.member.revoke/managed-viewer":    "denied/cross_tenant/viewer",
		"owner-mysql/1001/project.member.revoke/managed-viewer":    "allowed/owner/viewer",
	}
	if len(got) != len(want) {
		t.Fatalf("MySQL Project member mutation audit rows = %#v, want %#v", got, want)
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Fatalf("MySQL Project member mutation audit %q = %q, want %q; all=%#v", key, got[key], expected, got)
		}
	}
}

func assertProjectMySQLListAuditRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT operator_subject, operator_tenant_id, project_tenant_id, project_id, outcome, auth_basis, operation FROM opslogs WHERE operation = 'project.list' AND request_id NOT LIKE 'mysql-front-%' ORDER BY opslog_id`)
	if err != nil {
		t.Fatalf("query MySQL Project list audit rows: %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close MySQL Project list audit rows: %v", closeErr)
		}
	}()
	got := make(map[string]string)
	for rows.Next() {
		var subject, outcome, basis, operation string
		var actorTenantID, projectTenantID, projectID uint64
		if err := rows.Scan(&subject, &actorTenantID, &projectTenantID, &projectID, &outcome, &basis, &operation); err != nil {
			t.Fatalf("scan MySQL Project list audit row: %v", err)
		}
		if actorTenantID != projectMySQLIntegrationTenantID || projectTenantID != projectMySQLIntegrationTenantID || projectID != 0 || outcome != "allowed" || operation != "project.list" {
			t.Fatalf("Project list audit row = subject=%q actorTenant=%d projectTenant=%d projectID=%d outcome=%q operation=%q", subject, actorTenantID, projectTenantID, projectID, outcome, operation)
		}
		got[subject] = basis
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate MySQL Project list audit rows: %v", err)
	}
	want := map[string]string{
		"owner-mysql":    "relation_scope",
		"member-mysql":   "relation_scope",
		"project-reader": "relation_scope",
		"project-admin":  "tenant_admin",
	}
	if len(got) != len(want) {
		t.Fatalf("Project list audit rows = %#v, want %#v", got, want)
	}
	for subject, expectedBasis := range want {
		if got[subject] != expectedBasis {
			t.Fatalf("Project list audit for %q = %q, want %q", subject, got[subject], expectedBasis)
		}
	}
}

func assertProjectMySQLMemberListAuditRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT operator_subject, operator_tenant_id, project_tenant_id, project_id, outcome, auth_basis, target_subject, target_role FROM opslogs WHERE operation = 'project.member.list' ORDER BY opslog_id`)
	if err != nil {
		t.Fatalf("query MySQL Project member-list audit rows: %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close MySQL Project member-list audit rows: %v", closeErr)
		}
	}()
	got := make(map[string]string)
	for rows.Next() {
		var subject, outcome, basis, targetSubject, targetRole string
		var actorTenantID, projectTenantID, projectID uint64
		if err := rows.Scan(&subject, &actorTenantID, &projectTenantID, &projectID, &outcome, &basis, &targetSubject, &targetRole); err != nil {
			t.Fatalf("scan MySQL Project member-list audit row: %v", err)
		}
		if targetSubject != "" || targetRole != "" || projectTenantID != projectMySQLIntegrationTenantID || projectID != projectMySQLIntegrationID {
			t.Fatalf("Project member-list audit row leaks target or scope: subject=%q projectTenant=%d projectID=%d target=%q/%q", subject, projectTenantID, projectID, targetSubject, targetRole)
		}
		got[subject+"/"+strconv.FormatUint(actorTenantID, 10)] = outcome + "/" + basis
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate MySQL Project member-list audit rows: %v", err)
	}
	want := map[string]string{
		"owner-mysql/1001":   "allowed/owner",
		"project-admin/1001": "allowed/tenant_admin",
		"member-mysql/1001":  "denied/no_grant",
		"owner-mysql/2002":   "denied/cross_tenant",
	}
	if len(got) != len(want) {
		t.Fatalf("Project member-list audit rows = %#v, want %#v", got, want)
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Fatalf("Project member-list audit %q = %q, want %q", key, got[key], expected)
		}
	}
}

func assertFrontProjectMySQLAuditRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT operator_subject, operator_tenant_id, project_tenant_id, project_id, operation, outcome, auth_basis, request_id FROM opslogs WHERE request_id LIKE 'mysql-front-%' ORDER BY opslog_id`)
	if err != nil {
		t.Fatalf("query MySQL front Project audit rows: %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close MySQL front Project audit rows: %v", closeErr)
		}
	}()
	got := make(map[string]string)
	for rows.Next() {
		var subject, operation, outcome, basis, requestID string
		var actorTenantID, projectTenantID, projectID uint64
		if err := rows.Scan(&subject, &actorTenantID, &projectTenantID, &projectID, &operation, &outcome, &basis, &requestID); err != nil {
			t.Fatalf("scan MySQL front Project audit row: %v", err)
		}
		if operation == "project.read" && projectID != projectMySQLIntegrationID {
			t.Fatalf("front Project detail audit project ID = %d, want %d", projectID, projectMySQLIntegrationID)
		}
		if operation == "project.list" && (projectID != 0 || projectTenantID != actorTenantID) {
			t.Fatalf("front Project list audit scope = actorTenant=%d projectTenant=%d projectID=%d", actorTenantID, projectTenantID, projectID)
		}
		got[requestID] = subject + "/" + strconv.FormatUint(actorTenantID, 10) + "/" + operation + "/" + outcome + "/" + basis
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate MySQL front Project audit rows: %v", err)
	}
	want := map[string]string{
		"mysql-front-detail-front-owner":                             "owner-mysql/1001/project.read/allowed/owner",
		"mysql-front-detail-front-active-viewer":                     "member-mysql/1001/project.read/allowed/viewer",
		"mysql-front-detail-front-admin-policy-without-relation":     "project-admin/1001/project.read/denied/no_grant",
		"mysql-front-detail-front-cross-tenant-owner":                "owner-mysql/2002/project.read/denied/cross_tenant",
		"mysql-front-list-front-owner-relation-scope":                "owner-mysql/1001/project.list/allowed/relation_scope",
		"mysql-front-list-front-viewer-relation-scope":               "member-mysql/1001/project.list/allowed/relation_scope",
		"mysql-front-list-front-admin-policy-remains-relation-scope": "project-admin/1001/project.list/allowed/relation_scope",
	}
	if len(got) != len(want) {
		t.Fatalf("MySQL front Project audit rows = %#v, want %#v", got, want)
	}
	for requestID, expected := range want {
		if got[requestID] != expected {
			t.Fatalf("MySQL front Project audit for %q = %q, want %q; all=%#v", requestID, got[requestID], expected, got)
		}
	}
}

func equalStringSlices(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func equalProjectMemberListItems(got, want []projectMemberListItem) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

type projectAuditKey struct {
	subject          string
	operatorTenantID uint64
	projectTenantID  uint64
	projectID        uint64
}
