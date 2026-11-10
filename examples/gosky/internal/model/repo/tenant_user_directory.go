package repo

import (
	"context"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
)

var _ app.TenantUserDirectory = (*MySQLIdentityRepository)(nil)

// tenantUserSelectColumns lists the membership projection shared by the role
// member lookups. Phone and department name are presentation fields required
// by the unchanged SmartAdmin role-employee table; the department join stays
// a LEFT JOIN so a missing placement never hides an active binding.
const tenantUserSelectColumns = `SELECT u.user_id, u.subject, u.login_name,
       COALESCE(NULLIF(tu.display_name, ''), u.display_name), u.created_at,
       COALESCE(u.phone, ''), COALESCE(d.name, '')
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
JOIN tenants AS t ON t.tenant_id = tu.tenant_id
LEFT JOIN departments AS d ON d.tenant_id = tu.tenant_id AND d.department_id = tu.department_id
`

const tenantUserActiveConditions = `WHERE tu.tenant_id = ?
	AND u.user_state = 'active'
	AND tu.member_state = 'active'
	AND t.tenant_state = 'active'
`

// ListActiveTenantUsersByIDs resolves only active user/tenant memberships in
// the supplied tenant. The caller must validate and bound IDs before invoking
// this method; values still remain SQL parameters rather than query text.
func (r *MySQLIdentityRepository) ListActiveTenantUsersByIDs(ctx context.Context, tenantID uint64, userIDs []uint64) ([]app.TenantUser, error) {
	if r == nil || r.store == nil || tenantID == 0 || len(userIDs) == 0 || len(userIDs) > 100 {
		return nil, authn.ErrIdentityNotFound
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(userIDs)), ",")
	query := tenantUserSelectColumns +
		tenantUserActiveConditions +
		"AND u.user_id IN (" + placeholders + ") ORDER BY u.user_id ASC"
	args := make([]any, 0, len(userIDs)+1)
	args = append(args, tenantID)
	for _, userID := range userIDs {
		args = append(args, userID)
	}
	return r.listTenantUsers(ctx, query, args...)
}

// ListActiveTenantUsersBySubjects is the reverse lookup for already-validated
// Casbin bindings. It cannot enumerate identities outside the verified tenant.
func (r *MySQLIdentityRepository) ListActiveTenantUsersBySubjects(ctx context.Context, tenantID uint64, subjects []string) ([]app.TenantUser, error) {
	if r == nil || r.store == nil || tenantID == 0 || len(subjects) == 0 || len(subjects) > 1000 {
		return nil, authn.ErrIdentityNotFound
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(subjects)), ",")
	query := tenantUserSelectColumns +
		tenantUserActiveConditions +
		"AND u.subject IN (" + placeholders + ") ORDER BY u.user_id ASC"
	args := make([]any, 0, len(subjects)+1)
	args = append(args, tenantID)
	for _, subject := range subjects {
		args = append(args, subject)
	}
	return r.listTenantUsers(ctx, query, args...)
}

// ListActiveTenantUsers returns an ordered, bounded current-tenant directory
// projection for the SmartAdmin employee selector. Search input is a bound
// LIKE value, never SQL syntax.
func (r *MySQLIdentityRepository) ListActiveTenantUsers(ctx context.Context, tenantID uint64, keywords string) ([]app.TenantUser, error) {
	if r == nil || r.store == nil || tenantID == 0 {
		return nil, authn.ErrIdentityNotFound
	}
	query := tenantUserSelectColumns + tenantUserActiveConditions
	args := []any{tenantID}
	if keywords = strings.TrimSpace(keywords); keywords != "" {
		query += ` AND (u.login_name LIKE ? OR u.display_name LIKE ? OR tu.display_name LIKE ? OR COALESCE(d.name, '') LIKE ?)`
		like := "%" + keywords + "%"
		args = append(args, like, like, like, like)
	}
	query += ` ORDER BY u.user_id ASC LIMIT 1000`
	return r.listTenantUsers(ctx, query, args...)
}

func (r *MySQLIdentityRepository) listTenantUsers(ctx context.Context, query string, args ...any) ([]app.TenantUser, error) {
	rows, err := r.store.QueryRows(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query active tenant users: %w", err)
	}
	defer func() { _ = rows.Close() }()
	users := make([]app.TenantUser, 0)
	for rows.Next() {
		var user app.TenantUser
		if err := rows.Scan(&user.UserID, &user.Subject, &user.LoginName, &user.DisplayName, &user.CreatedAt, &user.Phone, &user.DepartmentName); err != nil {
			return nil, fmt.Errorf("scan active tenant user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active tenant users: %w", err)
	}
	return users, nil
}
