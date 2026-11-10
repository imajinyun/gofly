package repo

import (
	"context"
	"database/sql"
	"strings"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

var _ app.LoginSecurityStore = (*LoginSecurityRepository)(nil)

// LoginSecurityRepository persists SmartAdmin login audit rows, the
// consecutive-failure lock state, and the level-3 protect JSON config.
type LoginSecurityRepository struct {
	store *storage.SQLStore
}

func NewLoginSecurityRepository(store *storage.SQLStore) *LoginSecurityRepository {
	return &LoginSecurityRepository{store: store}
}

const resolveLoginIdentitySQL = `SELECT u.user_id, tu.tenant_id, u.subject, u.login_name,
COALESCE(NULLIF(tu.display_name, ''), u.display_name)
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
JOIN tenants AS t ON t.tenant_id = tu.tenant_id
WHERE u.login_name = ?
AND u.user_state = 'active'
AND tu.member_state = 'active'
AND t.tenant_state = 'active'
AND (u.account_type = 'admin' OR u.account_type = 'both')
AND (tu.member_realm = 'admin' OR tu.member_realm = 'both')
ORDER BY t.tenant_id ASC
LIMIT 2`

// ResolveLoginIdentity resolves an active admin identity only when the name
// maps to exactly one membership; ambiguity and absence both report found=false.
func (r *LoginSecurityRepository) ResolveLoginIdentity(ctx context.Context, loginName string) (app.LoginIdentity, bool, error) {
	loginName = strings.TrimSpace(loginName)
	if loginName == "" {
		return app.LoginIdentity{}, false, nil
	}
	rows, err := r.store.QueryRows(ctx, resolveLoginIdentitySQL, loginName)
	if err != nil {
		return app.LoginIdentity{}, false, err
	}
	defer func() { _ = rows.Close() }()
	identities := make([]app.LoginIdentity, 0, 2)
	for rows.Next() {
		var identity app.LoginIdentity
		if err := rows.Scan(&identity.UserID, &identity.TenantID, &identity.Subject,
			&identity.LoginName, &identity.DisplayName); err != nil {
			return app.LoginIdentity{}, false, err
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return app.LoginIdentity{}, false, err
	}
	if len(identities) != 1 {
		return app.LoginIdentity{}, false, nil
	}
	return identities[0], true, nil
}

// ResolveIdentityBySubject resolves the tenant-specific display name used on
// logout rows.
func (r *LoginSecurityRepository) ResolveIdentityBySubject(ctx context.Context, tenantID uint64, subject string) (app.LoginIdentity, bool, error) {
	var identity app.LoginIdentity
	err := r.store.QueryRow(ctx,
		`SELECT u.user_id, tu.tenant_id, u.subject, u.login_name,
COALESCE(NULLIF(tu.display_name, ''), u.display_name)
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
WHERE tu.tenant_id = ? AND u.subject = ?
  AND u.user_state = 'active' AND tu.member_state = 'active'`,
		tenantID, subject).Scan(&identity.UserID, &identity.TenantID, &identity.Subject,
		&identity.LoginName, &identity.DisplayName)
	if err == sql.ErrNoRows {
		return app.LoginIdentity{}, false, nil
	}
	if err != nil {
		return app.LoginIdentity{}, false, err
	}
	return identity, true, nil
}

// ---------------- login logs ----------------

func (r *LoginSecurityRepository) InsertLoginLog(ctx context.Context, tenantID uint64, entry app.LoginLog) error {
	userID := nullableUint64(entry.UserID)
	_, err := r.store.Exec(ctx,
		`INSERT INTO support_login_logs
(tenant_id, user_id, user_type, user_name, login_name, login_ip, login_ip_region,
user_agent, remark, login_device, login_result)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, userID, entry.UserType, entry.UserName, entry.LoginName, entry.LoginIP,
		entry.LoginIPRegion, entry.UserAgent, entry.Remark, entry.LoginDevice, entry.LoginResult)
	return err
}

func nullableUint64(value *uint64) any {
	if value == nil {
		return nil
	}
	return *value
}

func (r *LoginSecurityRepository) ListLoginLogs(ctx context.Context, tenantID uint64, query app.LoginLogQuery) (app.LoginLogPage, error) {
	where := []string{"tenant_id = ?"}
	args := []any{tenantID}
	if query.UserID != nil {
		where = append(where, "user_id = ?")
		args = append(args, *query.UserID)
	}
	if query.UserType != nil {
		where = append(where, "user_type = ?")
		args = append(args, *query.UserType)
	}
	if value := strings.TrimSpace(query.UserName); value != "" {
		where = append(where, "user_name LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.IP); value != "" {
		where = append(where, "login_ip LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if value := strings.TrimSpace(query.StartDate); value != "" {
		where = append(where, "created_at >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.EndDate); value != "" {
		where = append(where, "created_at <= ?")
		args = append(args, value+" 23:59:59")
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_login_logs WHERE `+filter, args...).Scan(&total); err != nil {
		return app.LoginLogPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT login_log_id, user_id, user_type, user_name, login_ip, login_ip_region,
user_agent, remark, login_device, login_result, created_at
FROM support_login_logs WHERE `+filter+` ORDER BY created_at DESC, login_log_id DESC LIMIT ? OFFSET ?`,
		pageArgs...)
	if err != nil {
		return app.LoginLogPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.LoginLog, 0, query.PageSize)
	for rows.Next() {
		var item app.LoginLog
		var userID sql.NullInt64
		if err := rows.Scan(&item.ID, &userID, &item.UserType, &item.UserName, &item.LoginIP,
			&item.LoginIPRegion, &item.UserAgent, &item.Remark, &item.LoginDevice,
			&item.LoginResult, &item.CreatedAt); err != nil {
			return app.LoginLogPage{}, err
		}
		if userID.Valid {
			id := uint64(userID.Int64)
			item.UserID = &id
		}
		items = append(items, item)
	}
	return app.LoginLogPage{List: items, Total: total}, rows.Err()
}

// ---------------- fail lock state ----------------

func (r *LoginSecurityRepository) GetFailState(ctx context.Context, tenantID, userID uint64) (app.LoginFail, bool, error) {
	var state app.LoginFail
	var lockTime sql.NullTime
	err := r.store.QueryRow(ctx,
		`SELECT login_fail_id, user_id, user_type, login_name, login_fail_count,
lock_flag, login_lock_begin_time, created_at, updated_at
FROM support_login_fails WHERE tenant_id = ? AND user_id = ?`,
		tenantID, userID).Scan(&state.ID, &state.UserID, &state.UserType, &state.LoginName,
		&state.LoginFailCount, &state.LockFlag, &lockTime, &state.CreatedAt, &state.UpdatedAt)
	if err == sql.ErrNoRows {
		return app.LoginFail{}, false, nil
	}
	if err != nil {
		return app.LoginFail{}, false, err
	}
	if lockTime.Valid {
		t := lockTime.Time
		state.LoginLockBeginTime = &t
	}
	return state, true, nil
}

// UpsertFailState inserts or replaces the mutable fail counters.
func (r *LoginSecurityRepository) UpsertFailState(ctx context.Context, tenantID, userID uint64, state app.LoginFail) error {
	var lockTime any
	if state.LoginLockBeginTime != nil {
		lockTime = *state.LoginLockBeginTime
	}
	_, err := r.store.Exec(ctx,
		`INSERT INTO support_login_fails
(tenant_id, user_id, user_type, login_name, login_fail_count, lock_flag, login_lock_begin_time)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE login_name = VALUES(login_name), login_fail_count = VALUES(login_fail_count),
lock_flag = VALUES(lock_flag), login_lock_begin_time = VALUES(login_lock_begin_time)`,
		tenantID, userID, state.UserType, state.LoginName, state.LoginFailCount, state.LockFlag, lockTime)
	return err
}

func (r *LoginSecurityRepository) DeleteFailState(ctx context.Context, tenantID, userID uint64) error {
	_, err := r.store.Exec(ctx,
		`DELETE FROM support_login_fails WHERE tenant_id = ? AND user_id = ?`, tenantID, userID)
	return err
}

func (r *LoginSecurityRepository) ListLoginFails(ctx context.Context, tenantID uint64, query app.LoginFailQuery) (app.LoginFailPage, error) {
	where := []string{"tenant_id = ?"}
	args := []any{tenantID}
	if value := strings.TrimSpace(query.LoginName); value != "" {
		where = append(where, "login_name LIKE ?")
		args = append(args, "%"+value+"%")
	}
	if query.LockFlag != nil {
		flag := 0
		if *query.LockFlag {
			flag = 1
		}
		where = append(where, "lock_flag = ?")
		args = append(args, flag)
	}
	if value := strings.TrimSpace(query.LockBeginTimeBegin); value != "" {
		where = append(where, "login_lock_begin_time >= ?")
		args = append(args, value+" 00:00:00")
	}
	if value := strings.TrimSpace(query.LockBeginTimeEnd); value != "" {
		where = append(where, "login_lock_begin_time <= ?")
		args = append(args, value+" 23:59:59")
	}
	filter := strings.Join(where, " AND ")

	var total int
	if err := r.store.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_login_fails WHERE `+filter, args...).Scan(&total); err != nil {
		return app.LoginFailPage{}, err
	}
	pageArgs := append(append([]any{}, args...), query.PageSize, (query.PageNum-1)*query.PageSize)
	rows, err := r.store.QueryRows(ctx,
		`SELECT login_fail_id, user_id, user_type, login_name, login_fail_count,
lock_flag, login_lock_begin_time, created_at, updated_at
FROM support_login_fails WHERE `+filter+` ORDER BY updated_at DESC, login_fail_id DESC LIMIT ? OFFSET ?`,
		pageArgs...)
	if err != nil {
		return app.LoginFailPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]app.LoginFail, 0, query.PageSize)
	for rows.Next() {
		var item app.LoginFail
		var lockTime sql.NullTime
		if err := rows.Scan(&item.ID, &item.UserID, &item.UserType, &item.LoginName,
			&item.LoginFailCount, &item.LockFlag, &lockTime, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return app.LoginFailPage{}, err
		}
		if lockTime.Valid {
			t := lockTime.Time
			item.LoginLockBeginTime = &t
		}
		items = append(items, item)
	}
	return app.LoginFailPage{List: items, Total: total}, rows.Err()
}

func (r *LoginSecurityRepository) DeleteLoginFails(ctx context.Context, tenantID uint64, ids []uint64) error {
	args := []any{tenantID}
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	_, err := r.store.Exec(ctx,
		`DELETE FROM support_login_fails WHERE tenant_id = ? AND login_fail_id IN (`+
			strings.Join(placeholders, ",")+`)`, args...)
	return err
}

// ---------------- level-3 protect config (support_configs JSON value) ----------------

func (r *LoginSecurityRepository) GetProtectConfig(ctx context.Context) (string, error) {
	var value string
	err := r.store.QueryRow(ctx,
		`SELECT config_value FROM support_configs WHERE config_key = ?`,
		app.Level3ProtectConfigKey).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func (r *LoginSecurityRepository) PutProtectConfig(ctx context.Context, raw string) error {
	_, err := r.store.Exec(ctx,
		`INSERT INTO support_configs (config_key, config_name, config_value, remark, system_flag)
VALUES (?, ?, ?, ?, 1)
ON DUPLICATE KEY UPDATE config_value = VALUES(config_value)`,
		app.Level3ProtectConfigKey, "三级等保配置", raw, "SmartAdmin level-3 protection JSON")
	return err
}
