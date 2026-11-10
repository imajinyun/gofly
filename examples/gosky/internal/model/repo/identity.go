package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
)

const findLoginIdentitySQL = `SELECT u.user_id, u.subject, u.password_hash, u.account_type,
	tu.tenant_id, tu.member_realm, t.tenant_code, u.login_name
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
JOIN tenants AS t ON t.tenant_id = tu.tenant_id
WHERE u.login_name = ?
	AND t.tenant_code = ?
	AND u.user_state = 'active'
	AND tu.member_state = 'active'
	AND t.tenant_state = 'active'
	AND (u.account_type = ? OR u.account_type = 'both')
	AND (tu.member_realm = ? OR tu.member_realm = 'both')
LIMIT 1`

const findUniqueLoginIdentitySQL = `SELECT u.user_id, u.subject, u.password_hash, u.account_type,
	tu.tenant_id, tu.member_realm, t.tenant_code, u.login_name
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
JOIN tenants AS t ON t.tenant_id = tu.tenant_id
WHERE u.login_name = ?
	AND u.user_state = 'active'
	AND tu.member_state = 'active'
	AND t.tenant_state = 'active'
	AND (u.account_type = ? OR u.account_type = 'both')
	AND (tu.member_realm = ? OR tu.member_realm = 'both')
ORDER BY t.tenant_id ASC
LIMIT 2`

const findSmartWebIdentitySQL = `SELECT u.user_id, u.subject, u.password_hash, u.account_type,
	tu.tenant_id, tu.member_realm, t.tenant_code, u.login_name
FROM auth_sessions AS s
JOIN users AS u ON u.user_id = s.user_id
JOIN tenant_users AS tu ON tu.tenant_id = s.tenant_id AND tu.user_id = s.user_id
JOIN tenants AS t ON t.tenant_id = s.tenant_id
WHERE s.session_id = ?
	AND u.subject = ?
	AND s.tenant_id = ?
	AND s.realm = ?
	AND s.revoked_at IS NULL
	AND s.expires_at > ?
	AND u.user_state = 'active'
	AND tu.member_state = 'active'
	AND t.tenant_state = 'active'
	AND (u.account_type = s.realm OR u.account_type = 'both')
	AND (tu.member_realm = s.realm OR tu.member_realm = 'both')
LIMIT 1`

const findActiveSessionSQL = `SELECT s.session_id, s.user_id, s.tenant_id, s.realm,
	s.refresh_token_hash, s.refresh_generation, s.issued_at, s.expires_at,
	s.last_seen_at, s.revoked_at
FROM auth_sessions AS s
JOIN users AS u ON u.user_id = s.user_id
JOIN tenant_users AS tu ON tu.tenant_id = s.tenant_id AND tu.user_id = s.user_id
JOIN tenants AS t ON t.tenant_id = s.tenant_id
WHERE s.session_id = ?
	AND u.subject = ?
	AND s.tenant_id = ?
	AND s.realm = ?
	AND s.revoked_at IS NULL
	AND s.expires_at > ?
	AND u.user_state = 'active'
	AND tu.member_state = 'active'
	AND t.tenant_state = 'active'
	AND (u.account_type = s.realm OR u.account_type = 'both')
	AND (tu.member_realm = s.realm OR tu.member_realm = 'both')
LIMIT 1`

const findRefreshIdentitySQL = `SELECT s.session_id, s.user_id, s.tenant_id, s.realm,
	s.refresh_token_hash, s.refresh_generation, s.issued_at, s.expires_at,
	s.last_seen_at, s.revoked_at, u.subject
FROM auth_sessions AS s
JOIN users AS u ON u.user_id = s.user_id
JOIN tenant_users AS tu ON tu.tenant_id = s.tenant_id AND tu.user_id = s.user_id
JOIN tenants AS t ON t.tenant_id = s.tenant_id
WHERE s.refresh_token_hash = ?
	AND s.revoked_at IS NULL
	AND s.expires_at > ?
	AND u.user_state = 'active'
	AND tu.member_state = 'active'
	AND t.tenant_state = 'active'
	AND (u.account_type = s.realm OR u.account_type = 'both')
	AND (tu.member_realm = s.realm OR tu.member_realm = 'both')
LIMIT 1`

const insertSessionSQL = `INSERT INTO auth_sessions (
	session_id, user_id, tenant_id, realm, refresh_token_hash, refresh_generation,
	issued_at, expires_at, last_seen_at
) VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)`

const selectRefreshSessionForUpdateSQL = `SELECT session_id, expires_at, revoked_at
FROM auth_sessions
WHERE refresh_token_hash = ?
FOR UPDATE`

// #nosec G101 -- refresh_token_hash is a fixed database column name, never a credential value.
const rotateRefreshTokenSQL = `UPDATE auth_sessions
SET refresh_token_hash = ?, refresh_generation = refresh_generation + 1, last_seen_at = ?
WHERE session_id = ?
	AND refresh_token_hash = ?
	AND revoked_at IS NULL
	AND expires_at > ?`

const revokeSessionSQL = `UPDATE auth_sessions
SET revoked_at = COALESCE(revoked_at, ?), last_seen_at = ?
WHERE session_id = ?`

const listActiveSessionsSQL = `SELECT session_id, issued_at, last_seen_at, expires_at
FROM auth_sessions
WHERE user_id = ?
	AND tenant_id = ?
	AND realm = ?
	AND revoked_at IS NULL
	AND expires_at > ?
ORDER BY last_seen_at DESC, session_id ASC
LIMIT ?`

const revokeOwnedSessionSQL = `UPDATE auth_sessions
SET revoked_at = ?, last_seen_at = ?
WHERE session_id = ?
	AND user_id = ?
	AND tenant_id = ?
	AND realm = ?
	AND revoked_at IS NULL
	AND expires_at > ?`

const insertAuthenticationAuditSQL = `INSERT INTO auth_login_logs (
	user_id, tenant_id, realm, login_name, subject, operation, outcome,
	reason_code, request_id, trace_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// MySQLIdentityRepository implements custom identity/session SQL operations.
// Its directory methods also implement app.TenantUserDirectory. Joins and
// lock-sensitive refresh-token rotation live here rather than in HTTP handlers.
type MySQLIdentityRepository struct {
	store *storage.SQLStore
}

func NewMySQLIdentityRepository(store *storage.SQLStore) *MySQLIdentityRepository {
	return &MySQLIdentityRepository{store: store}
}

// FindLoginIdentity finds one active user and active tenant membership that
// are both valid for the requested realm.
func (r *MySQLIdentityRepository) FindLoginIdentity(ctx context.Context, loginName, tenantCode, realm string) (authn.LoginIdentity, error) {
	if r == nil || r.store == nil {
		return authn.LoginIdentity{}, errors.New("identity SQL store is nil")
	}
	if !validLoginName(loginName) || !validTenantCode(tenantCode) || !validIdentityRealm(realm) {
		return authn.LoginIdentity{}, authn.ErrIdentityNotFound
	}
	var userID, tenantID uint64
	var identity authn.LoginIdentity
	err := r.store.QueryOne(ctx, findLoginIdentitySQL, func(row *sql.Row) error {
		return row.Scan(&userID, &identity.Subject, &identity.PasswordHash, &identity.AccountType, &tenantID, &identity.MemberRealm, &identity.TenantCode, &identity.LoginName)
	}, loginName, tenantCode, realm, realm)
	if errors.Is(err, sql.ErrNoRows) {
		return authn.LoginIdentity{}, authn.ErrIdentityNotFound
	}
	if err != nil {
		return authn.LoginIdentity{}, fmt.Errorf("find login identity: %w", err)
	}
	identity.UserID = userID
	identity.TenantID = tenantID
	return identity, nil
}

// FindUniqueLoginIdentity resolves an active identity only when its admin
// login name maps to exactly one eligible tenant membership. smart-web has no
// tenant selector, so ambiguity must fail closed rather than choosing an
// arbitrary tenant.
func (r *MySQLIdentityRepository) FindUniqueLoginIdentity(ctx context.Context, loginName, realm string) (authn.LoginIdentity, error) {
	if r == nil || r.store == nil {
		return authn.LoginIdentity{}, errors.New("identity SQL store is nil")
	}
	if !validLoginName(loginName) || !validIdentityRealm(realm) {
		return authn.LoginIdentity{}, authn.ErrIdentityNotFound
	}
	rows, err := r.store.QueryRows(ctx, findUniqueLoginIdentitySQL, loginName, realm, realm)
	if err != nil {
		return authn.LoginIdentity{}, fmt.Errorf("find unique login identity: %w", err)
	}
	defer rows.Close()
	identities := make([]authn.LoginIdentity, 0, 2)
	for rows.Next() {
		var identity authn.LoginIdentity
		if err := rows.Scan(&identity.UserID, &identity.Subject, &identity.PasswordHash, &identity.AccountType, &identity.TenantID, &identity.MemberRealm, &identity.TenantCode, &identity.LoginName); err != nil {
			return authn.LoginIdentity{}, fmt.Errorf("scan unique login identity: %w", err)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return authn.LoginIdentity{}, fmt.Errorf("iterate unique login identity: %w", err)
	}
	switch len(identities) {
	case 0:
		return authn.LoginIdentity{}, authn.ErrIdentityNotFound
	case 1:
		return identities[0], nil
	default:
		return authn.LoginIdentity{}, authn.ErrIdentityAmbiguous
	}
}

// FindSmartWebIdentity projects a verified active session back to its account
// metadata. The session, tenant, realm, and subject are all parameterized and
// must agree, so a client cannot use it to select another identity.
func (r *MySQLIdentityRepository) FindSmartWebIdentity(ctx context.Context, sessionID, subject string, tenantID uint64, realm string, now time.Time) (authn.LoginIdentity, error) {
	if r == nil || r.store == nil {
		return authn.LoginIdentity{}, errors.New("identity SQL store is nil")
	}
	if !validSessionID(sessionID) || !validSubject(subject) || tenantID == 0 || !validIdentityRealm(realm) || now.IsZero() {
		return authn.LoginIdentity{}, authn.ErrIdentityNotFound
	}
	var identity authn.LoginIdentity
	err := r.store.QueryOne(ctx, findSmartWebIdentitySQL, func(row *sql.Row) error {
		return row.Scan(&identity.UserID, &identity.Subject, &identity.PasswordHash, &identity.AccountType, &identity.TenantID, &identity.MemberRealm, &identity.TenantCode, &identity.LoginName)
	}, sessionID, subject, tenantID, realm, now)
	if errors.Is(err, sql.ErrNoRows) {
		return authn.LoginIdentity{}, authn.ErrIdentityNotFound
	}
	if err != nil {
		return authn.LoginIdentity{}, fmt.Errorf("find smart-web identity: %w", err)
	}
	return identity, nil
}

// CreateSession persists a newly issued refresh session. Callers must create
// the opaque refresh token separately and provide only its SHA-256 digest.
func (r *MySQLIdentityRepository) CreateSession(ctx context.Context, session authn.SessionRecord) error {
	if r == nil || r.store == nil {
		return errors.New("identity SQL store is nil")
	}
	if !validSessionRecord(session) {
		return errors.New("session record is invalid")
	}
	lastSeenAt := session.LastSeenAt
	if lastSeenAt.IsZero() {
		lastSeenAt = session.IssuedAt
	}
	if _, err := r.store.Exec(ctx, insertSessionSQL,
		session.SessionID,
		session.UserID,
		session.TenantID,
		session.Realm,
		session.RefreshTokenHash,
		session.IssuedAt,
		session.ExpiresAt,
		lastSeenAt,
	); err != nil {
		return fmt.Errorf("insert refresh session: %w", err)
	}
	return nil
}

// FindActiveSession verifies the session against the token's subject, tenant,
// and realm while also enforcing account and tenant-membership state.
func (r *MySQLIdentityRepository) FindActiveSession(ctx context.Context, sessionID, subject string, tenantID uint64, realm string, now time.Time) (authn.SessionRecord, error) {
	if r == nil || r.store == nil {
		return authn.SessionRecord{}, errors.New("identity SQL store is nil")
	}
	if !validSessionID(sessionID) || !validSubject(subject) || tenantID == 0 || !validIdentityRealm(realm) || now.IsZero() {
		return authn.SessionRecord{}, authn.ErrIdentityNotFound
	}
	var session authn.SessionRecord
	var userID, databaseTenantID, generation uint64
	err := r.store.QueryOne(ctx, findActiveSessionSQL, func(row *sql.Row) error {
		return row.Scan(
			&session.SessionID,
			&userID,
			&databaseTenantID,
			&session.Realm,
			&session.RefreshTokenHash,
			&generation,
			&session.IssuedAt,
			&session.ExpiresAt,
			&session.LastSeenAt,
			&session.RevokedAt,
		)
	}, sessionID, subject, tenantID, realm, now)
	if errors.Is(err, sql.ErrNoRows) {
		return authn.SessionRecord{}, authn.ErrIdentityNotFound
	}
	if err != nil {
		return authn.SessionRecord{}, fmt.Errorf("find active session: %w", err)
	}
	session.UserID = userID
	session.TenantID = databaseTenantID
	session.RefreshGeneration = generation
	return session, nil
}

// FindRefreshIdentity resolves one active refresh-token digest to the
// associated subject and session. The digest is already SHA-256 encoded by
// the caller, so the plaintext opaque token never reaches this repository.
func (r *MySQLIdentityRepository) FindRefreshIdentity(ctx context.Context, refreshTokenHash string, now time.Time) (authn.RefreshIdentity, error) {
	if r == nil || r.store == nil {
		return authn.RefreshIdentity{}, errors.New("identity SQL store is nil")
	}
	if !validTokenHash(refreshTokenHash) || now.IsZero() {
		return authn.RefreshIdentity{}, authn.ErrIdentityNotFound
	}
	var identity authn.RefreshIdentity
	var userID, tenantID, generation uint64
	err := r.store.QueryOne(ctx, findRefreshIdentitySQL, func(row *sql.Row) error {
		return row.Scan(
			&identity.Session.SessionID,
			&userID,
			&tenantID,
			&identity.Session.Realm,
			&identity.Session.RefreshTokenHash,
			&generation,
			&identity.Session.IssuedAt,
			&identity.Session.ExpiresAt,
			&identity.Session.LastSeenAt,
			&identity.Session.RevokedAt,
			&identity.Subject,
		)
	}, refreshTokenHash, now)
	if errors.Is(err, sql.ErrNoRows) {
		return authn.RefreshIdentity{}, authn.ErrIdentityNotFound
	}
	if err != nil {
		return authn.RefreshIdentity{}, fmt.Errorf("find refresh identity: %w", err)
	}
	identity.Session.UserID = userID
	identity.Session.TenantID = tenantID
	identity.Session.RefreshGeneration = generation
	return identity, nil
}

// RotateRefreshToken consumes one current refresh-token hash and atomically
// replaces it. A replay of the old digest returns changed=false.
func (r *MySQLIdentityRepository) RotateRefreshToken(ctx context.Context, previousHash, nextHash string, now time.Time) (bool, error) {
	if r == nil || r.store == nil {
		return false, errors.New("identity SQL store is nil")
	}
	if !validTokenHash(previousHash) || !validTokenHash(nextHash) || previousHash == nextHash || now.IsZero() {
		return false, errors.New("refresh token rotation input is invalid")
	}
	changed := false
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(transactionCtx context.Context, tx *sql.Tx) error {
		var sessionID string
		var expiresAt time.Time
		var revokedAt *time.Time
		err := tx.QueryRowContext(transactionCtx, selectRefreshSessionForUpdateSQL, previousHash).Scan(&sessionID, &expiresAt, &revokedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock refresh session: %w", err)
		}
		if revokedAt != nil || !expiresAt.After(now) {
			return nil
		}
		result, err := tx.ExecContext(transactionCtx, rotateRefreshTokenSQL, nextHash, now, sessionID, previousHash, now)
		if err != nil {
			return fmt.Errorf("rotate refresh session: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read refresh rotation result: %w", err)
		}
		changed = rows == 1
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("rotate refresh session transaction: %w", err)
	}
	return changed, nil
}

// RevokeSession is idempotent so logout can safely be retried by a client.
func (r *MySQLIdentityRepository) RevokeSession(ctx context.Context, sessionID string, now time.Time) error {
	if r == nil || r.store == nil {
		return errors.New("identity SQL store is nil")
	}
	if !validSessionID(sessionID) || now.IsZero() {
		return errors.New("session revocation input is invalid")
	}
	if _, err := r.store.Exec(ctx, revokeSessionSQL, now, now, sessionID); err != nil {
		return fmt.Errorf("revoke refresh session: %w", err)
	}
	return nil
}

// ListActiveSessions returns only the client-safe timestamps of sessions that
// belong to one already-verified user/tenant/realm scope. It never selects a
// refresh digest, so callers cannot accidentally serialize credential data.
func (r *MySQLIdentityRepository) ListActiveSessions(ctx context.Context, userID, tenantID uint64, realm string, now time.Time, limit uint) ([]authn.SessionRecord, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("identity SQL store is nil")
	}
	if userID == 0 || tenantID == 0 || !validIdentityRealm(realm) || now.IsZero() || limit < 1 || limit > 100 {
		return nil, errors.New("active session list input is invalid")
	}
	sessions := make([]authn.SessionRecord, 0, limit)
	if err := r.store.QueryAll(ctx, listActiveSessionsSQL, func(rows *sql.Rows) error {
		for rows.Next() {
			var session authn.SessionRecord
			if err := rows.Scan(&session.SessionID, &session.IssuedAt, &session.LastSeenAt, &session.ExpiresAt); err != nil {
				return err
			}
			sessions = append(sessions, session)
		}
		return nil
	}, userID, tenantID, realm, now, limit); err != nil {
		return nil, fmt.Errorf("list active sessions: %w", err)
	}
	return sessions, nil
}

// RevokeOwnedSessionWithAudit atomically revokes one active session only in
// the supplied owner/tenant/realm scope and records the matching allowed
// authentication operation. A mismatched or already-revoked ID is a silent,
// unchanged result so the endpoint does not disclose its existence.
func (r *MySQLIdentityRepository) RevokeOwnedSessionWithAudit(ctx context.Context, sessionID string, userID, tenantID uint64, realm string, now time.Time, audit authn.AuthAuditRecord) (bool, error) {
	if r == nil || r.store == nil {
		return false, errors.New("identity SQL store is nil")
	}
	if !validSessionID(sessionID) || userID == 0 || tenantID == 0 || !validIdentityRealm(realm) || now.IsZero() || !validAuditRecord(audit) {
		return false, errors.New("owned session revocation input is invalid")
	}
	changed := false
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(transactionCtx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(transactionCtx, revokeOwnedSessionSQL, now, now, sessionID, userID, tenantID, realm, now)
		if err != nil {
			return fmt.Errorf("revoke owned session: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read owned session revocation result: %w", err)
		}
		if rows == 0 {
			return nil
		}
		if rows != 1 {
			return fmt.Errorf("owned session revocation changed %d rows", rows)
		}
		if _, err := tx.ExecContext(transactionCtx, insertAuthenticationAuditSQL,
			audit.UserID,
			audit.TenantID,
			audit.Realm,
			audit.LoginName,
			audit.Subject,
			audit.Operation,
			audit.Outcome,
			audit.ReasonCode,
			audit.RequestID,
			audit.TraceID,
		); err != nil {
			return fmt.Errorf("insert owned session revocation audit: %w", err)
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("owned session revocation transaction: %w", err)
	}
	return changed, nil
}

func (r *MySQLIdentityRepository) RecordAudit(ctx context.Context, record authn.AuthAuditRecord) error {
	if r == nil || r.store == nil {
		return errors.New("identity SQL store is nil")
	}
	if !validAuditRecord(record) {
		return errors.New("authentication audit record is invalid")
	}
	if _, err := r.store.Exec(ctx, insertAuthenticationAuditSQL,
		record.UserID,
		record.TenantID,
		record.Realm,
		record.LoginName,
		record.Subject,
		record.Operation,
		record.Outcome,
		record.ReasonCode,
		record.RequestID,
		record.TraceID,
	); err != nil {
		return fmt.Errorf("insert authentication audit: %w", err)
	}
	return nil
}

func validSessionRecord(session authn.SessionRecord) bool {
	return validSessionID(session.SessionID) &&
		session.UserID > 0 &&
		session.TenantID > 0 &&
		validIdentityRealm(session.Realm) &&
		validTokenHash(session.RefreshTokenHash) &&
		!session.IssuedAt.IsZero() &&
		session.ExpiresAt.After(session.IssuedAt)
}

func validAuditRecord(record authn.AuthAuditRecord) bool {
	if !validIdentityRealm(record.Realm) || !validAuthenticationOperation(record.Operation) || !validAuthenticationOutcome(record.Outcome) {
		return false
	}
	return len([]rune(record.LoginName)) <= 255 &&
		len([]rune(record.Subject)) <= 255 &&
		len(record.ReasonCode) <= 64 &&
		len([]rune(record.RequestID)) <= 128 &&
		len([]rune(record.TraceID)) <= 128
}

func validLoginName(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 255
}

func validTenantCode(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character < 'a' || character > 'z' {
			if character < 'A' || character > 'Z' {
				if character < '0' || character > '9' {
					if character != '-' && character != '_' {
						return false
					}
				}
			}
		}
	}
	return true
}

func validSubject(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 255
}

func validSessionID(value string) bool { return validLowerHex(value, 32) }

func validTokenHash(value string) bool { return validLowerHex(value, 64) }

func validLowerHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validIdentityRealm(value string) bool { return value == "admin" || value == "front" }

func validAuthenticationOperation(value string) bool {
	return value == authn.AuthOperationLogin || value == authn.AuthOperationRefresh || value == authn.AuthOperationLogout || value == authn.AuthOperationAuthenticate || value == authn.AuthOperationSessionList || value == authn.AuthOperationSessionRevoke
}

func validAuthenticationOutcome(value string) bool {
	return value == authn.AuthOutcomeAllowed || value == authn.AuthOutcomeDenied || value == authn.AuthOutcomeError
}

var _ authn.IdentityRepository = (*MySQLIdentityRepository)(nil)

// ChangeOwnPassword verifies oldPassword against the current hash, replaces
// it with newHash, and revokes other active sessions in the same transaction.
// It serves self-service password changes where the verified principal (not
// an admin reset) proves knowledge of the old password.
func (r *MySQLIdentityRepository) ChangeOwnPassword(ctx context.Context, subject string, tenantID uint64, oldHash, newHash string, now time.Time) error {
	return r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		var userID uint64
		var storedHash, memberRealm string
		err := tx.QueryRowContext(txCtx,
			`SELECT u.user_id, u.password_hash, tu.member_realm
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
WHERE u.subject = ? AND tu.tenant_id = ? AND u.user_state = 'active' AND tu.member_state = 'active'
FOR UPDATE`, subject, tenantID).Scan(&userID, &storedHash, &memberRealm)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrRBACUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock identity for password change: %w", err)
		}
		if storedHash != oldHash {
			return app.ErrOldPasswordMismatch
		}
		result, err := tx.ExecContext(txCtx,
			`UPDATE users SET password_hash = ?, updated_at = ? WHERE user_id = ?`,
			newHash, now, userID)
		if err != nil {
			return fmt.Errorf("change password: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return app.ErrRBACUserNotFound
		}
		if err := revokeSessionsInTx(txCtx, tx, tenantID, userID, memberRealm, now); err != nil {
			return err
		}
		return nil
	})
}

// LoginEmail resolves a unique active login name's delivery email for the
// email second factor. An unknown login name returns known=false so the
// public send-code endpoint cannot enumerate accounts.
func (r *MySQLIdentityRepository) LoginEmail(ctx context.Context, loginName string) (string, bool, error) {
	rows, err := r.store.QueryRows(ctx, `SELECT COALESCE(u.email, '')
FROM users AS u
WHERE u.login_name = ? AND u.user_state = 'active'
ORDER BY u.user_id ASC LIMIT 1`, loginName)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", false, nil
	}
	var email string
	if err := rows.Scan(&email); err != nil {
		return "", false, err
	}
	return email, email != "", rows.Err()
}
