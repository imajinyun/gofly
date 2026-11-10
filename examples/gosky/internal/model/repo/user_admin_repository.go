// Tenant user administration persistence: bounded directory reads, user
// creation, membership state changes, and password reset. Disabling a
// membership or resetting a password revokes the affected refresh sessions
// in the same MySQL transaction.
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
)

// MySQLUserAdminRepository implements app.UserAdminStore against the identity
// database. In the standard deployment this is the same MySQL database as the
// policy store.
type MySQLUserAdminRepository struct {
	store *storage.SQLStore
}

func NewMySQLUserAdminRepository(store *storage.SQLStore) *MySQLUserAdminRepository {
	return &MySQLUserAdminRepository{store: store}
}

var _ app.UserAdminStore = (*MySQLUserAdminRepository)(nil)

const userAdminColumns = `u.user_id, u.subject, u.login_name, COALESCE(NULLIF(tu.display_name, ''), u.display_name),
u.account_type, u.user_state, tu.member_realm, tu.member_state, u.created_at`

func (r *MySQLUserAdminRepository) ListUsers(ctx context.Context, tenantID uint64, pageNum, pageSize uint64, keywords string) (app.UserAdminPage, error) {
	pageNum, pageSize = normalizePage(pageNum, pageSize)
	where := `WHERE tu.tenant_id = ?`
	args := []any{tenantID}
	if keywords = strings.TrimSpace(keywords); keywords != "" {
		where += ` AND (u.login_name LIKE ? OR u.display_name LIKE ? OR tu.display_name LIKE ? OR u.subject LIKE ?)`
		like := "%" + keywords + "%"
		args = append(args, like, like, like, like)
	}
	var total int
	countArgs := append([]any{}, args...)
	if err := r.store.QueryOne(ctx, `SELECT COUNT(*) FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id `+where,
		func(row *sql.Row) error { return row.Scan(&total) }, countArgs...); err != nil {
		return app.UserAdminPage{}, fmt.Errorf("count tenant users: %w", err)
	}
	query := `SELECT ` + userAdminColumns + ` FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
` + where + ` ORDER BY u.user_id ASC LIMIT ? OFFSET ?`
	args = append(args, pageSize, (pageNum-1)*pageSize)
	rows, err := r.store.QueryRows(ctx, query, args...)
	if err != nil {
		return app.UserAdminPage{}, fmt.Errorf("list tenant users: %w", err)
	}
	defer func() { _ = rows.Close() }()
	users := make([]app.UserRecord, 0, pageSize)
	for rows.Next() {
		user, err := scanUserRecord(rows)
		if err != nil {
			return app.UserAdminPage{}, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return app.UserAdminPage{}, fmt.Errorf("iterate tenant users: %w", err)
	}
	return app.UserAdminPage{Users: users, Total: total}, nil
}

func (r *MySQLUserAdminRepository) GetUser(ctx context.Context, tenantID, userID uint64) (app.UserRecord, bool, error) {
	var user app.UserRecord
	err := r.store.QueryOne(ctx, `SELECT `+userAdminColumns+` FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
WHERE tu.tenant_id = ? AND u.user_id = ? LIMIT 1`,
		func(row *sql.Row) error {
			return scanUserRecordInto(row, &user)
		}, tenantID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return app.UserRecord{}, false, nil
	}
	if err != nil {
		return app.UserRecord{}, false, fmt.Errorf("get tenant user: %w", err)
	}
	return user, true, nil
}

func (r *MySQLUserAdminRepository) CreateUser(ctx context.Context, tenantID uint64, input app.CreateUserInput, subject, passwordHash string, actor app.RBACActor) (app.UserRecord, error) {
	realm := normalizeMemberRealm(input.MemberRealm)
	now := time.Now().UTC()
	var userID uint64
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(txCtx, `INSERT INTO users
(subject, login_name, password_hash, account_type, user_state, display_name, created_at, updated_at)
VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`,
			subject, strings.TrimSpace(input.LoginName), passwordHash, realm, strings.TrimSpace(input.DisplayName), now, now)
		if err != nil {
			if isDuplicateEntry(err) {
				return app.ErrRBACDuplicateUser
			}
			return fmt.Errorf("insert user: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read created user id: %w", err)
		}
		userID = uint64(id)
		if _, err := tx.ExecContext(txCtx, `INSERT INTO tenant_users
(tenant_id, user_id, member_realm, member_state, display_name, joined_at, created_at, updated_at)
VALUES (?, ?, ?, 'active', ?, ?, ?, ?)`,
			tenantID, userID, realm, strings.TrimSpace(input.DisplayName), now, now, now); err != nil {
			if isDuplicateEntry(err) {
				return app.ErrRBACDuplicateUser
			}
			return fmt.Errorf("insert tenant membership: %w", err)
		}
		return insertAuditTx(txCtx, tx, auditInput{
			tenantID: tenantID, actor: actor, operation: "user.create", objectType: "user",
			objectID: fmt.Sprintf("%d", userID),
			after:    map[string]any{"loginName": input.LoginName, "memberRealm": realm, "subject": subject},
		})
	})
	if err != nil {
		return app.UserRecord{}, mapPolicyError(fmt.Errorf("create user transaction: %w", err))
	}
	user, found, err := r.GetUser(ctx, tenantID, userID)
	if err != nil || !found {
		return app.UserRecord{}, fmt.Errorf("reload created user: %w", err)
	}
	return user, nil
}

func (r *MySQLUserAdminRepository) SetMembershipState(ctx context.Context, tenantID, userID uint64, disabled bool, actor app.RBACActor) error {
	state := "active"
	if disabled {
		state = "disabled"
	}
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		var realm string
		err := tx.QueryRowContext(txCtx, `SELECT member_realm FROM tenant_users WHERE tenant_id = ? AND user_id = ? FOR UPDATE`,
			tenantID, userID).Scan(&realm)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrRBACUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock membership: %w", err)
		}
		now := time.Now().UTC()
		var leftAt any
		if disabled {
			leftAt = now
		}
		result, err := tx.ExecContext(txCtx, `UPDATE tenant_users SET member_state = ?, left_at = ?, updated_at = ?
WHERE tenant_id = ? AND user_id = ?`, state, leftAt, now, tenantID, userID)
		if err != nil {
			return fmt.Errorf("update membership state: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read membership change: %w", err)
		}
		if rows == 0 {
			return app.ErrRBACUserNotFound
		}
		if disabled {
			if err := revokeSessionsInTx(txCtx, tx, tenantID, userID, realm, now); err != nil {
				return err
			}
		}
		return insertAuditTx(txCtx, tx, auditInput{
			tenantID: tenantID, actor: actor, operation: "user.state", objectType: "user",
			objectID: fmt.Sprintf("%d", userID), after: map[string]string{"memberState": state},
		})
	})
	if err != nil {
		return mapPolicyError(fmt.Errorf("set membership state transaction: %w", err))
	}
	return nil
}

func (r *MySQLUserAdminRepository) ResetPassword(ctx context.Context, tenantID, userID uint64, passwordHash string, actor app.RBACActor) error {
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		var realm string
		err := tx.QueryRowContext(txCtx, `SELECT member_realm FROM tenant_users WHERE tenant_id = ? AND user_id = ? FOR UPDATE`,
			tenantID, userID).Scan(&realm)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrRBACUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock membership for password reset: %w", err)
		}
		result, err := tx.ExecContext(txCtx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE user_id = ?`,
			passwordHash, time.Now().UTC(), userID)
		if err != nil {
			return fmt.Errorf("reset password: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read password reset: %w", err)
		}
		if rows == 0 {
			return app.ErrRBACUserNotFound
		}
		if err := revokeSessionsInTx(txCtx, tx, tenantID, userID, realm, time.Now().UTC()); err != nil {
			return err
		}
		return insertAuditTx(txCtx, tx, auditInput{
			tenantID: tenantID, actor: actor, operation: "user.password.reset", objectType: "user",
			objectID: fmt.Sprintf("%d", userID),
		})
	})
	if err != nil {
		return mapPolicyError(fmt.Errorf("reset password transaction: %w", err))
	}
	return nil
}

// revokeSessionsInTx revokes active sessions for the membership scope. A
// 'both' membership covers both realms.
func revokeSessionsInTx(ctx context.Context, tx *sql.Tx, tenantID, userID uint64, realm string, now time.Time) error {
	query := `UPDATE auth_sessions SET revoked_at = ?, last_seen_at = ?
WHERE user_id = ? AND tenant_id = ? AND revoked_at IS NULL`
	args := []any{now, now, userID, tenantID}
	if realm != "both" {
		query += ` AND realm = ?`
		args = append(args, realm)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

func normalizeMemberRealm(realm string) string {
	switch strings.TrimSpace(realm) {
	case "admin", "front":
		return strings.TrimSpace(realm)
	default:
		return "both"
	}
}

func normalizePage(pageNum, pageSize uint64) (uint64, uint64) {
	if pageNum == 0 {
		pageNum = 1
	}
	if pageSize == 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return pageNum, pageSize
}

func scanUserRecord(rows interface{ Scan(dest ...any) error }) (app.UserRecord, error) {
	var user app.UserRecord
	if err := scanUserRecordInto(rows, &user); err != nil {
		return app.UserRecord{}, err
	}
	return user, nil
}

func scanUserRecordInto(scanner interface{ Scan(dest ...any) error }, user *app.UserRecord) error {
	return scanner.Scan(&user.ID, &user.Subject, &user.LoginName, &user.DisplayName,
		&user.AccountType, &user.UserState, &user.MemberRealm, &user.MemberState, &user.CreatedAt)
}
