//go:build integration

package authz

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"

	_ "github.com/go-sql-driver/mysql"
)

const mysqlPolicyIntegrationDomain = "1001"

func TestMySQLPolicyStoreAndWatcherIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the MySQL policy integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL policy database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL policy database: %v", err)
	}
	tables := isolatedMySQLPolicyTables(t, ctx, db)

	modelPath := writeMySQLPolicyModel(t)
	store, err := NewMySQLPolicyStoreWithTables(db, tables)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := PolicyActor{Subject: "bootstrap", Domain: mysqlPolicyIntegrationDomain, RequestID: "policy-bootstrap"}
	if err := store.BootstrapTenant(ctx, bootstrap, "tenant-admin"); err != nil {
		t.Fatalf("bootstrap tenant policy: %v", err)
	}
	if err := store.BootstrapTenant(ctx, bootstrap, "another-admin"); !errors.Is(err, ErrTenantAlreadyBootstrapped) {
		t.Fatalf("second bootstrap error = %v, want %v", err, ErrTenantAlreadyBootstrapped)
	}

	first, err := NewMySQLAuthorizerWithTables(modelPath, db, tables, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("new first MySQL authorizer: %v", err)
	}
	second, err := NewMySQLAuthorizerWithTables(modelPath, db, tables, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("new second MySQL authorizer: %v", err)
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	if err := second.StartMySQLPolicyWatcher(watchCtx, 10*time.Millisecond); err != nil {
		t.Fatalf("start second policy watcher: %v", err)
	}

	principalCtx := auth.NewContext(context.Background(), auth.Principal{Subject: "tenant-admin", Claims: map[string]any{"tenant_id": mysqlPolicyIntegrationDomain}})
	if err := first.Authorize(principalCtx, string(CapabilityProjectReadAny), CapabilityAction); err != nil {
		t.Fatalf("admin read before revocation: %v", err)
	}
	if err := second.Authorize(principalCtx, string(CapabilityProjectReadAny), CapabilityAction); err != nil {
		t.Fatalf("second admin read before revocation: %v", err)
	}

	policyService := NewPolicyService(store, first)
	if _, policyErr := policyService.RevokeRole(principalCtx, "tenant-admin", RoleAdmin, "policy-final-admin"); policyErr == nil || policyErr.Code != "failed_precondition" {
		t.Fatalf("revoke final admin error = %v, want failed precondition", policyErr)
	}
	if changed, policyErr := policyService.GrantRole(principalCtx, "replacement-admin", RoleAdmin, "policy-grant-replacement"); policyErr != nil || !changed {
		t.Fatalf("grant replacement admin = changed:%t error:%v, want changed with no error", changed, policyErr)
	}
	if changed, policyErr := policyService.RevokeRole(principalCtx, "tenant-admin", RoleAdmin, "policy-revoke-original"); policyErr != nil || !changed {
		t.Fatalf("revoke original admin = changed:%t error:%v, want changed with no error", changed, policyErr)
	}
	if got := first.Authorize(principalCtx, string(CapabilityProjectReadAny), CapabilityAction); got == nil || got.Code != "permission_denied" {
		t.Fatalf("first authorization after revocation = %v, want permission denied", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := second.Authorize(principalCtx, string(CapabilityProjectReadAny), CapabilityAction); got != nil && got.Code == "permission_denied" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second authorizer did not observe policy revocation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertMySQLPolicyAuditEvents(t, ctx, db, tables)
}

func TestMySQLPolicyStoreAndWatcherCustomTablesIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the MySQL policy integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL custom policy database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL custom policy database: %v", err)
	}
	tables := isolatedMySQLPolicyTables(t, ctx, db)

	var beforeDefault int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM casbin_rules WHERE domain = ?`, mysqlPolicyIntegrationDomain).Scan(&beforeDefault); err != nil {
		t.Fatal(err)
	}
	modelPath := writeMySQLPolicyModel(t)
	store, err := NewMySQLPolicyStoreWithTables(db, tables)
	if err != nil {
		t.Fatalf("new custom MySQL policy store: %v", err)
	}
	bootstrap := PolicyActor{Subject: "bootstrap", Domain: mysqlPolicyIntegrationDomain, RequestID: "custom-policy-bootstrap"}
	if err := store.BootstrapTenant(ctx, bootstrap, "tenant-admin"); err != nil {
		t.Fatalf("bootstrap custom tenant policy: %v", err)
	}
	var defaultRules int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM casbin_rules WHERE domain = ?`, mysqlPolicyIntegrationDomain).Scan(&defaultRules); err != nil {
		t.Fatalf("count default policy rules: %v", err)
	}
	if defaultRules != beforeDefault {
		t.Fatalf("default policy rules = %d, want unchanged count %d", defaultRules, beforeDefault)
	}

	first, err := NewMySQLAuthorizerWithTables(modelPath, db, tables, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("new first custom MySQL authorizer: %v", err)
	}
	second, err := NewMySQLAuthorizerWithTables(modelPath, db, tables, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("new second custom MySQL authorizer: %v", err)
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	if err := second.StartMySQLPolicyWatcher(watchCtx, 10*time.Millisecond); err != nil {
		t.Fatalf("start custom policy watcher: %v", err)
	}
	principalCtx := auth.NewContext(context.Background(), auth.Principal{Subject: "tenant-admin", Claims: map[string]any{"tenant_id": mysqlPolicyIntegrationDomain}})
	if err := first.Authorize(principalCtx, string(CapabilityProjectReadAny), CapabilityAction); err != nil {
		t.Fatalf("custom policy read before revocation: %v", err)
	}
	policyService := NewPolicyService(store, first)
	if changed, policyErr := policyService.GrantRole(principalCtx, "replacement-admin", RoleAdmin, "custom-policy-grant-replacement"); policyErr != nil || !changed {
		t.Fatalf("grant custom replacement admin = changed:%t error:%v, want changed with no error", changed, policyErr)
	}
	if changed, policyErr := policyService.RevokeRole(principalCtx, "tenant-admin", RoleAdmin, "custom-policy-revoke-original"); policyErr != nil || !changed {
		t.Fatalf("revoke custom original admin = changed:%t error:%v, want changed with no error", changed, policyErr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := second.Authorize(principalCtx, string(CapabilityProjectReadAny), CapabilityAction); got != nil && got.Code == "permission_denied" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second custom-table authorizer did not observe policy revocation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var eventCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tables.eventsSQL()+` WHERE domain = ?`, mysqlPolicyIntegrationDomain).Scan(&eventCount); err != nil {
		t.Fatalf("count custom policy audit events: %v", err)
	}
	if eventCount < 9 {
		t.Fatalf("custom policy audit event count = %d, want at least 9", eventCount)
	}
}

func TestMySQLPolicyStoreUpgradeCapabilitiesIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the MySQL policy integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL policy database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL policy database: %v", err)
	}
	tables := isolatedMySQLPolicyTables(t, ctx, db)

	store, err := NewMySQLPolicyStoreWithTables(db, tables)
	if err != nil {
		t.Fatal(err)
	}
	actor := PolicyActor{Subject: "platform-operator", Domain: mysqlPolicyIntegrationDomain, RequestID: "policy-capability-upgrade"}
	if err := store.BootstrapTenant(ctx, actor, "tenant-admin"); err != nil {
		t.Fatalf("bootstrap tenant policy: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM `+tables.rulesSQL()+` WHERE ptype = 'p' AND v0 = ? AND v1 = ? AND v2 = ? AND v3 = ?`, RoleAdmin, mysqlPolicyIntegrationDomain, string(CapabilityProjectMemberManage), CapabilityAction); err != nil {
		t.Fatalf("remove newly introduced member capability from legacy fixture: %v", err)
	}
	if changed, err := store.GrantPermission(ctx, actor, RoleReader, mysqlPolicyIntegrationDomain, "/api/v1/projects/:id", "GET"); err != nil || !changed {
		t.Fatalf("seed legacy project policy = changed:%t error:%v, want changed with no error", changed, err)
	}

	changed, err := store.UpgradeTenantCapabilities(ctx, actor)
	if err != nil || !changed {
		t.Fatalf("upgrade tenant capabilities = changed:%t error:%v, want changed with no error", changed, err)
	}

	authorizer, err := NewMySQLAuthorizerWithTables(writeMySQLPolicyModel(t), db, tables, []byte(testJWTSecret))
	if err != nil {
		t.Fatalf("new MySQL authorizer: %v", err)
	}
	principalCtx := auth.NewContext(context.Background(), auth.Principal{Subject: "tenant-admin", Claims: map[string]any{"tenant_id": mysqlPolicyIntegrationDomain}})
	if got := authorizer.Authorize(principalCtx, string(CapabilityProjectRead), CapabilityAction); got != nil {
		t.Fatalf("canonical project authorization = %v, want allowed", got)
	}
	if got := authorizer.Authorize(principalCtx, string(CapabilityProjectMemberManage), CapabilityAction); got == nil || got.Code != "permission_denied" {
		t.Fatalf("transport upgrade restored a revoked member-management grant: %v", got)
	}
	if got := authorizer.Authorize(principalCtx, "/api/v1/projects/:id", "GET"); got == nil || got.Code != "permission_denied" {
		t.Fatalf("legacy project authorization = %v, want permission denied", got)
	}
	if changed, err := store.UpgradeTenantCapabilities(ctx, actor); err != nil || changed {
		t.Fatalf("second capability upgrade = changed:%t error:%v, want unchanged with no error", changed, err)
	}
}

func writeMySQLPolicyModel(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "casbin_model.conf")
	if err := os.WriteFile(path, []byte(defaultModel), 0o600); err != nil {
		t.Fatalf("write MySQL policy model: %v", err)
	}
	return path
}

func isolatedMySQLPolicyTables(t *testing.T, ctx context.Context, db *sql.DB) MySQLPolicyTables {
	t.Helper()
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(nonce[:])
	tables, err := NewMySQLPolicyTables("gosky_authz_rules_"+suffix, "gosky_authz_events_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	createCustomMySQLPolicyTables(t, ctx, db, tables)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		dropCustomMySQLPolicyTables(t, ctx, db, tables)
	})
	return tables
}

func assertMySQLPolicyAuditEvents(t *testing.T, ctx context.Context, db *sql.DB, tables MySQLPolicyTables) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tables.eventsSQL()+` WHERE domain = ?`, mysqlPolicyIntegrationDomain).Scan(&count); err != nil {
		t.Fatalf("count MySQL policy audit events: %v", err)
	}
	if count < 9 {
		t.Fatalf("policy audit event count = %d, want at least 9", count)
	}
}

func createCustomMySQLPolicyTables(t *testing.T, ctx context.Context, db *sql.DB, tables MySQLPolicyTables) {
	t.Helper()
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+tables.eventsSQL()+" LIKE casbin_policy_events"); err != nil {
		t.Fatalf("create custom policy events table: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+tables.rulesSQL()+" LIKE casbin_rules"); err != nil {
		dropCustomMySQLPolicyTables(t, context.Background(), db, tables)
		t.Fatalf("create custom policy rules table: %v", err)
	}
}

func dropCustomMySQLPolicyTables(t *testing.T, ctx context.Context, db *sql.DB, tables MySQLPolicyTables) {
	t.Helper()
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tables.eventsSQL()); err != nil {
		t.Errorf("drop custom policy events table: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tables.rulesSQL()); err != nil {
		t.Errorf("drop custom policy rules table: %v", err)
	}
}
