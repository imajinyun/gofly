//go:build integration

package authn_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/model/repo"

	_ "github.com/go-sql-driver/mysql"
)

const (
	identityIntegrationTenantID  uint64 = 987654321
	identityIntegrationUserID    uint64 = 987654322
	identityIntegrationSessionID        = "6f3d3d2dd49d4f35b29d6caccc5f0021"
)

func TestMySQLIdentityRepository_SessionLifecycleIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GOSKY_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("GOSKY_MYSQL_DSN is required for the MySQL identity integration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL identity database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL identity database: %v", err)
	}
	resetIdentityIntegrationFixture(t, ctx, db)
	t.Cleanup(func() { resetIdentityIntegrationFixture(t, context.Background(), db) })
	seedIdentityIntegrationFixture(t, ctx, db)

	repository := repo.NewMySQLIdentityRepository(storage.NewSQLStore(db))
	identity, err := repository.FindLoginIdentity(ctx, "identity-integration", "identity-it", string(authn.RealmAdmin))
	if err != nil {
		t.Fatalf("find active admin login identity: %v", err)
	}
	if identity.Subject != "user:identity-integration" || identity.TenantID != identityIntegrationTenantID || identity.UserID != identityIntegrationUserID {
		t.Fatalf("login identity = %#v, want seeded identity", identity)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	session := authn.SessionRecord{
		SessionID:        identityIntegrationSessionID,
		UserID:           identityIntegrationUserID,
		TenantID:         identityIntegrationTenantID,
		Realm:            string(authn.RealmAdmin),
		RefreshTokenHash: strings.Repeat("a", 64),
		IssuedAt:         now,
		ExpiresAt:        now.Add(time.Hour),
	}
	if err := repository.CreateSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	loaded, err := repository.FindActiveSession(ctx, session.SessionID, "user:identity-integration", identityIntegrationTenantID, string(authn.RealmAdmin), now)
	if err != nil {
		t.Fatalf("find active session: %v", err)
	}
	if loaded.RefreshTokenHash != session.RefreshTokenHash || loaded.RefreshGeneration != 1 {
		t.Fatalf("loaded session = %#v, want initial refresh session", loaded)
	}

	rotated, err := repository.RotateRefreshToken(ctx, session.RefreshTokenHash, strings.Repeat("b", 64), now.Add(time.Minute))
	if err != nil || !rotated {
		t.Fatalf("rotate refresh token = changed:%t err:%v, want changed", rotated, err)
	}
	if replayed, err := repository.RotateRefreshToken(ctx, session.RefreshTokenHash, strings.Repeat("c", 64), now.Add(2*time.Minute)); err != nil || replayed {
		t.Fatalf("replay previous refresh token = changed:%t err:%v, want unchanged without error", replayed, err)
	}

	if err := repository.RevokeSession(ctx, session.SessionID, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	if _, err := repository.FindActiveSession(ctx, session.SessionID, "user:identity-integration", identityIntegrationTenantID, string(authn.RealmAdmin), now.Add(4*time.Minute)); !errors.Is(err, authn.ErrIdentityNotFound) {
		t.Fatalf("find revoked session error = %v, want %v", err, authn.ErrIdentityNotFound)
	}

	auditUserID := identityIntegrationUserID
	auditTenantID := identityIntegrationTenantID
	if err := repository.RecordAudit(ctx, authn.AuthAuditRecord{
		UserID:    &auditUserID,
		TenantID:  &auditTenantID,
		Realm:     string(authn.RealmAdmin),
		LoginName: "identity-integration",
		Subject:   "user:identity-integration",
		Operation: authn.AuthOperationLogin,
		Outcome:   authn.AuthOutcomeAllowed,
		RequestID: "identity-integration-request",
		TraceID:   "identity-integration-trace",
	}); err != nil {
		t.Fatalf("record authentication audit: %v", err)
	}
	var auditCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_login_logs WHERE request_id = ? AND operation = 'login' AND outcome = 'allowed'`, "identity-integration-request").Scan(&auditCount); err != nil {
		t.Fatalf("count authentication audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("authentication audit count = %d, want 1", auditCount)
	}
}

func seedIdentityIntegrationFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO tenants (tenant_id, tenant_code, tenant_name) VALUES (?, ?, ?)`, identityIntegrationTenantID, "identity-it", "Identity integration"); err != nil {
		t.Fatalf("insert identity integration tenant: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (user_id, subject, login_name, password_hash, account_type, user_state) VALUES (?, ?, ?, ?, 'both', 'active')`, identityIntegrationUserID, "user:identity-integration", "identity-integration", "$argon2id$fixture"); err != nil {
		t.Fatalf("insert identity integration user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenant_users (tenant_id, user_id, member_realm, member_state) VALUES (?, ?, 'both', 'active')`, identityIntegrationTenantID, identityIntegrationUserID); err != nil {
		t.Fatalf("insert identity integration tenant membership: %v", err)
	}
}

func resetIdentityIntegrationFixture(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, deletion := range []struct {
		query string
		arg   uint64
	}{
		{query: `DELETE FROM auth_login_logs WHERE tenant_id = ?`, arg: identityIntegrationTenantID},
		{query: `DELETE FROM auth_sessions WHERE tenant_id = ?`, arg: identityIntegrationTenantID},
		{query: `DELETE FROM tenant_users WHERE tenant_id = ?`, arg: identityIntegrationTenantID},
		{query: `DELETE FROM users WHERE user_id = ?`, arg: identityIntegrationUserID},
		{query: `DELETE FROM tenants WHERE tenant_id = ?`, arg: identityIntegrationTenantID},
	} {
		if _, err := db.ExecContext(ctx, deletion.query, deletion.arg); err != nil {
			t.Fatalf("reset identity integration fixture: %v", err)
		}
	}
}
