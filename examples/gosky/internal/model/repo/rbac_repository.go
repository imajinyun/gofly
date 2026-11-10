// Dynamic RBAC persistence: tenant roles, menus, role-menu grants, and the
// combined role_menus/Casbin transaction. All queries bind tenant and record
// IDs as parameters; request input never reaches SQL text.
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// MySQLRBACRepository implements app.RBACStore. The store must point at the
// same MySQL database as the policy store because role_menus and Casbin rules
// commit in one transaction.
type MySQLRBACRepository struct {
	store  *storage.SQLStore
	policy *authz.MySQLPolicyStore
}

func NewMySQLRBACRepository(store *storage.SQLStore, policy *authz.MySQLPolicyStore) *MySQLRBACRepository {
	return &MySQLRBACRepository{store: store, policy: policy}
}

var _ app.RBACStore = (*MySQLRBACRepository)(nil)
var _ authz.RoleDirectory = (*MySQLRBACRepository)(nil)

// ActiveRoleCodes returns active custom role codes in the numeric domain.
// Reserved roles are validated by the authorization package itself.
func (r *MySQLRBACRepository) ActiveRoleCodes(ctx context.Context, domain string) (map[string]struct{}, error) {
	tenantID, err := parseDomainTenant(domain)
	if err != nil {
		return nil, err
	}
	rows, err := r.store.QueryRows(ctx, `SELECT role_code FROM roles
WHERE tenant_id = ? AND role_state = 'active' AND system_flag = 0`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("query active role codes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	codes := make(map[string]struct{})
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("scan active role code: %w", err)
		}
		codes[code] = struct{}{}
	}
	return codes, rows.Err()
}

func (r *MySQLRBACRepository) ListRoles(ctx context.Context, tenantID uint64, includeDisabled bool) ([]app.RoleRecord, error) {
	query := `SELECT role_id, tenant_id, role_code, role_name, remark, role_state, system_flag, created_at, updated_at
FROM roles WHERE tenant_id = ?`
	if !includeDisabled {
		query += ` AND role_state = 'active'`
	}
	query += ` ORDER BY system_flag DESC, role_id ASC`
	rows, err := r.store.QueryRows(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	roles := make([]app.RoleRecord, 0)
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (r *MySQLRBACRepository) GetRole(ctx context.Context, tenantID, roleID uint64) (app.RoleRecord, bool, error) {
	return r.getRole(ctx, "role_id = ?", tenantID, roleID)
}

func (r *MySQLRBACRepository) GetRoleByCode(ctx context.Context, tenantID uint64, code string) (app.RoleRecord, bool, error) {
	return r.getRole(ctx, "role_code = ?", tenantID, strings.TrimSpace(code))
}

func (r *MySQLRBACRepository) getRole(ctx context.Context, predicate string, tenantID uint64, arg any) (app.RoleRecord, bool, error) {
	var role app.RoleRecord
	var system bool
	var state string
	err := r.store.QueryOne(ctx, `SELECT role_id, tenant_id, role_code, role_name, remark, role_state, system_flag, created_at, updated_at
FROM roles WHERE tenant_id = ? AND `+predicate+` LIMIT 1`,
		func(row *sql.Row) error {
			return row.Scan(&role.ID, &role.TenantID, &role.Code, &role.Name, &role.Remark, &state, &system, &role.CreatedAt, &role.UpdatedAt)
		}, tenantID, arg)
	if errors.Is(err, sql.ErrNoRows) {
		return app.RoleRecord{}, false, nil
	}
	if err != nil {
		return app.RoleRecord{}, false, fmt.Errorf("get role: %w", err)
	}
	role.State = state
	role.System = system
	return role, true, nil
}

func (r *MySQLRBACRepository) CreateRole(ctx context.Context, tenantID uint64, input app.RoleInput, actor app.RBACActor) (app.RoleRecord, error) {
	now := time.Now().UTC()
	result, err := r.store.Exec(ctx, `INSERT INTO roles
(tenant_id, role_code, role_name, remark, role_state, system_flag, created_by, updated_by, created_at, updated_at)
VALUES (?, ?, ?, ?, 'active', 0, ?, ?, ?, ?)`,
		tenantID, strings.TrimSpace(input.Code), strings.TrimSpace(input.Name), strings.TrimSpace(input.Remark),
		actor.Subject, actor.Subject, now, now)
	if err != nil {
		if isDuplicateEntry(err) {
			return app.RoleRecord{}, app.ErrRBACDuplicateCode
		}
		return app.RoleRecord{}, fmt.Errorf("create role: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return app.RoleRecord{}, fmt.Errorf("read created role id: %w", err)
	}
	if err := r.insertAudit(ctx, auditInput{
		tenantID: tenantID, actor: actor, operation: "role.create", objectType: "role",
		objectID: strconv.FormatInt(id, 10), after: input,
	}); err != nil {
		return app.RoleRecord{}, err
	}
	role, found, err := r.GetRole(ctx, tenantID, uint64(id))
	if err != nil || !found {
		return app.RoleRecord{}, fmt.Errorf("reload created role: %w", err)
	}
	return role, nil
}

func (r *MySQLRBACRepository) UpdateRole(ctx context.Context, tenantID, roleID uint64, input app.RoleUpdate, actor app.RBACActor) error {
	result, err := r.store.Exec(ctx, `UPDATE roles SET role_name = ?, remark = ?, updated_by = ?
WHERE tenant_id = ? AND role_id = ? AND system_flag = 0`,
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Remark), actor.Subject, tenantID, roleID)
	if err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	if err := requireChanged(result, app.ErrRBACReservedRole); err != nil {
		if errors.Is(err, app.ErrRBACReservedRole) {
			return app.ErrRBACReservedRole
		}
		return err
	}
	return r.insertAudit(ctx, auditInput{
		tenantID: tenantID, actor: actor, operation: "role.update", objectType: "role",
		objectID: strconv.FormatUint(roleID, 10), after: input,
	})
}

func (r *MySQLRBACRepository) SetRoleState(ctx context.Context, tenantID, roleID uint64, disabled bool, actor app.RBACActor) error {
	role, found, err := r.GetRole(ctx, tenantID, roleID)
	if err != nil {
		return err
	}
	if !found {
		return app.ErrRBACRoleNotFound
	}
	if role.System {
		return app.ErrRBACReservedRole
	}
	state := "active"
	if disabled {
		state = "disabled"
	}
	err = r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(txCtx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(txCtx, `UPDATE roles SET role_state = ?, updated_by = ? WHERE tenant_id = ? AND role_id = ?`,
			state, actor.Subject, tenantID, roleID); err != nil {
			return fmt.Errorf("set role state: %w", err)
		}
		domain := strconv.FormatUint(tenantID, 10)
		policyActor := policyActor(actor, domain)
		if disabled {
			// A disabled role must stop authorizing immediately, so its subject
			// bindings are removed. The capability template (p rules) is kept
			// for re-enable; members must be reassigned afterwards.
			if _, err := r.policy.DeleteRoleBindingsInTx(txCtx, tx, policyActor, role.Code, domain); err != nil {
				return err
			}
			if err := r.policy.EnsurePermissionAdministratorInTx(txCtx, tx, domain); err != nil {
				return err
			}
		}
		return insertAuditTx(txCtx, tx, auditInput{
			tenantID: tenantID, actor: actor, operation: "role.state", objectType: "role",
			objectID: strconv.FormatUint(roleID, 10), after: map[string]string{"state": state},
		})
	})
	if err != nil {
		return mapPolicyError(fmt.Errorf("set role state transaction: %w", err))
	}
	return nil
}

func (r *MySQLRBACRepository) DeleteRole(ctx context.Context, tenantID, roleID uint64, actor app.RBACActor) error {
	role, found, err := r.GetRole(ctx, tenantID, roleID)
	if err != nil {
		return err
	}
	if !found {
		return app.ErrRBACRoleNotFound
	}
	if role.System {
		return app.ErrRBACReservedRole
	}
	domain := strconv.FormatUint(tenantID, 10)
	err = r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(txCtx context.Context, tx *sql.Tx) error {
		var lockedCode string
		var system bool
		err := tx.QueryRowContext(txCtx, `SELECT role_code, system_flag FROM roles WHERE tenant_id = ? AND role_id = ? FOR UPDATE`,
			tenantID, roleID).Scan(&lockedCode, &system)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrRBACRoleNotFound
		}
		if err != nil {
			return fmt.Errorf("lock role for deletion: %w", err)
		}
		if system {
			return app.ErrRBACReservedRole
		}
		count, err := r.policy.RoleBindingCountInTx(txCtx, tx, domain, lockedCode)
		if err != nil {
			return err
		}
		if count > 0 {
			return app.ErrRBACRoleInUse
		}
		if _, err := tx.ExecContext(txCtx, `DELETE FROM role_menus WHERE tenant_id = ? AND role_id = ?`, tenantID, roleID); err != nil {
			return fmt.Errorf("delete role grants: %w", err)
		}
		if _, err := r.policy.DeleteRolePolicyInTx(txCtx, tx, policyActor(actor, domain), lockedCode, domain); err != nil {
			return err
		}
		if err := r.policy.EnsurePermissionAdministratorInTx(txCtx, tx, domain); err != nil {
			return err
		}
		if _, err := tx.ExecContext(txCtx, `DELETE FROM roles WHERE tenant_id = ? AND role_id = ?`, tenantID, roleID); err != nil {
			return fmt.Errorf("delete role row: %w", err)
		}
		return insertAuditTx(txCtx, tx, auditInput{
			tenantID: tenantID, actor: actor, operation: "role.delete", objectType: "role",
			objectID: strconv.FormatUint(roleID, 10), before: role,
		})
	})
	if err != nil {
		return mapPolicyError(fmt.Errorf("delete role transaction: %w", err))
	}
	return nil
}

func (r *MySQLRBACRepository) ListMenus(ctx context.Context, tenantID uint64, includeDisabled bool) ([]app.MenuRecord, error) {
	query := `SELECT menu_id, parent_id, menu_name, menu_type, sort_order, path, frame_url, frame_flag, cache_flag, component, web_perms, capability, icon, visible_flag, disabled_flag, system_flag
FROM menus WHERE tenant_id = ?`
	if !includeDisabled {
		query += ` AND disabled_flag = 0`
	}
	query += ` ORDER BY sort_order ASC, menu_id ASC`
	rows, err := r.store.QueryRows(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list menus: %w", err)
	}
	defer func() { _ = rows.Close() }()
	menus := make([]app.MenuRecord, 0)
	for rows.Next() {
		menu, err := scanMenu(rows)
		if err != nil {
			return nil, err
		}
		menus = append(menus, menu)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate menus: %w", err)
	}
	return menus, nil
}

func (r *MySQLRBACRepository) GetMenu(ctx context.Context, tenantID, menuID uint64) (app.MenuRecord, bool, error) {
	var menu app.MenuRecord
	var visible, disabled, system bool
	err := r.store.QueryOne(ctx, `SELECT menu_id, parent_id, menu_name, menu_type, sort_order, path, frame_url, frame_flag, cache_flag, component, web_perms, capability, icon, visible_flag, disabled_flag, system_flag
FROM menus WHERE tenant_id = ? AND menu_id = ? LIMIT 1`,
		func(row *sql.Row) error {
			return row.Scan(&menu.ID, &menu.ParentID, &menu.Name, &menu.Type, &menu.Sort,
				&menu.Path, &menu.FrameURL, &menu.FrameFlag, &menu.CacheFlag, &menu.Component, &menu.WebPerms, &menu.Capability, &menu.Icon,
				&visible, &disabled, &system)
		}, tenantID, menuID)
	if errors.Is(err, sql.ErrNoRows) {
		return app.MenuRecord{}, false, nil
	}
	if err != nil {
		return app.MenuRecord{}, false, fmt.Errorf("get menu: %w", err)
	}
	menu.Visible, menu.Disabled, menu.System = visible, disabled, system
	return menu, true, nil
}

func (r *MySQLRBACRepository) CreateMenu(ctx context.Context, tenantID uint64, input app.MenuInput, actor app.RBACActor) (app.MenuRecord, error) {
	var nextID uint64
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(txCtx context.Context, tx *sql.Tx) error {
		if err := tx.QueryRowContext(txCtx, `SELECT GREATEST(COALESCE(MAX(menu_id), ?) + 1, ?) FROM menus WHERE tenant_id = ? FOR UPDATE`,
			customMenuIDStart-1, customMenuIDStart, tenantID).Scan(&nextID); err != nil {
			return fmt.Errorf("allocate menu id: %w", err)
		}
		now := time.Now().UTC()
		visible := boolToTinyInt(input.Visible)
		disabledFlag := boolToTinyInt(input.Disabled)
		if _, err := tx.ExecContext(txCtx, `INSERT INTO menus
(tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, frame_url, frame_flag, cache_flag, component, web_perms, capability, icon, visible_flag, disabled_flag, system_flag, created_by, updated_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`,
			tenantID, nextID, input.ParentID, strings.TrimSpace(input.Name), input.Type, input.Sort,
			strings.TrimSpace(input.Path), strings.TrimSpace(input.FrameURL), boolToTinyInt(input.FrameFlag), boolToTinyInt(input.CacheFlag),
			strings.TrimSpace(input.Component), strings.TrimSpace(input.WebPerms),
			strings.TrimSpace(input.Capability), strings.TrimSpace(input.Icon), visible, disabledFlag,
			actor.Subject, actor.Subject, now, now); err != nil {
			return fmt.Errorf("create menu: %w", err)
		}
		return insertAuditTx(txCtx, tx, auditInput{
			tenantID: tenantID, actor: actor, operation: "menu.create", objectType: "menu",
			objectID: strconv.FormatUint(nextID, 10), after: input,
		})
	})
	if err != nil {
		return app.MenuRecord{}, mapPolicyError(err)
	}
	menu, found, err := r.GetMenu(ctx, tenantID, nextID)
	if err != nil {
		return app.MenuRecord{}, err
	}
	if !found {
		return app.MenuRecord{}, fmt.Errorf("reload created menu: %w", app.ErrRBACMenuNotFound)
	}
	return menu, nil
}

func (r *MySQLRBACRepository) UpdateMenu(ctx context.Context, tenantID, menuID uint64, input app.MenuInput, actor app.RBACActor) error {
	result, err := r.store.Exec(ctx, `UPDATE menus SET parent_id = ?, menu_name = ?, menu_type = ?, sort_order = ?,
path = ?, frame_url = ?, frame_flag = ?, cache_flag = ?, component = ?, web_perms = ?, capability = ?, icon = ?, visible_flag = ?, disabled_flag = ?, updated_by = ?
WHERE tenant_id = ? AND menu_id = ? AND system_flag = 0`,
		input.ParentID, strings.TrimSpace(input.Name), input.Type, input.Sort,
		strings.TrimSpace(input.Path), strings.TrimSpace(input.FrameURL), boolToTinyInt(input.FrameFlag), boolToTinyInt(input.CacheFlag),
		strings.TrimSpace(input.Component), strings.TrimSpace(input.WebPerms),
		strings.TrimSpace(input.Capability), strings.TrimSpace(input.Icon), boolToTinyInt(input.Visible), boolToTinyInt(input.Disabled),
		actor.Subject, tenantID, menuID)
	if err != nil {
		return fmt.Errorf("update menu: %w", err)
	}
	if err := requireChanged(result, app.ErrRBACReservedRole); err != nil {
		if errors.Is(err, app.ErrRBACReservedRole) {
			return app.ErrRBACReservedRole
		}
		return err
	}
	return r.insertAudit(ctx, auditInput{
		tenantID: tenantID, actor: actor, operation: "menu.update", objectType: "menu",
		objectID: strconv.FormatUint(menuID, 10), after: input,
	})
}

func (r *MySQLRBACRepository) DeleteMenu(ctx context.Context, tenantID, menuID uint64, actor app.RBACActor) error {
	menu, found, err := r.GetMenu(ctx, tenantID, menuID)
	if err != nil {
		return err
	}
	if !found {
		return app.ErrRBACMenuNotFound
	}
	if menu.System {
		return app.ErrRBACReservedRole
	}
	domain := strconv.FormatUint(tenantID, 10)
	err = r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(txCtx context.Context, tx *sql.Tx) error {
		var capability string
		var system bool
		err := tx.QueryRowContext(txCtx, `SELECT capability, system_flag FROM menus WHERE tenant_id = ? AND menu_id = ? FOR UPDATE`,
			tenantID, menuID).Scan(&capability, &system)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrRBACMenuNotFound
		}
		if err != nil {
			return fmt.Errorf("lock menu for deletion: %w", err)
		}
		if system {
			return app.ErrRBACReservedRole
		}
		var childCount int
		if err := tx.QueryRowContext(txCtx, `SELECT COUNT(*) FROM menus WHERE tenant_id = ? AND parent_id = ?`,
			tenantID, menuID).Scan(&childCount); err != nil {
			return fmt.Errorf("count child menus: %w", err)
		}
		if childCount > 0 {
			return app.ErrRBACInvalidRelation
		}
		if _, err := tx.ExecContext(txCtx, `DELETE FROM menus WHERE tenant_id = ? AND menu_id = ?`, tenantID, menuID); err != nil {
			return fmt.Errorf("delete menu row: %w", err)
		}
		if capability != "" {
			if _, err := r.policy.RemoveCapabilityFromRolesInTx(txCtx, tx, policyActor(actor, domain), domain, capability); err != nil {
				return err
			}
			if err := r.policy.EnsurePermissionAdministratorInTx(txCtx, tx, domain); err != nil {
				return err
			}
		}
		return insertAuditTx(txCtx, tx, auditInput{
			tenantID: tenantID, actor: actor, operation: "menu.delete", objectType: "menu",
			objectID: strconv.FormatUint(menuID, 10), before: menu,
		})
	})
	if err != nil {
		return mapPolicyError(fmt.Errorf("delete menu transaction: %w", err))
	}
	return nil
}

func (r *MySQLRBACRepository) RoleCapabilities(ctx context.Context, tenantID uint64, roleCode string) ([]string, error) {
	domain := strconv.FormatUint(tenantID, 10)
	capabilities, err := r.policy.ListRoleCapabilities(ctx, domain, strings.TrimSpace(roleCode))
	if err != nil {
		return nil, mapPolicyError(fmt.Errorf("list role capabilities: %w", err))
	}
	out := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		out = append(out, string(capability))
	}
	return out, nil
}

func (r *MySQLRBACRepository) ReplaceRoleGrants(ctx context.Context, tenantID, roleID uint64, menuIDs []uint64, actor app.RBACActor) error {
	domain := strconv.FormatUint(tenantID, 10)
	policyActor := authz.PolicyActor{Subject: actor.Subject, Domain: domain, RequestID: actor.RequestID, TraceID: actor.TraceID}
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(txCtx context.Context, tx *sql.Tx) error {
		var roleCode, roleState string
		err := tx.QueryRowContext(txCtx, `SELECT role_code, role_state FROM roles WHERE tenant_id = ? AND role_id = ? FOR UPDATE`,
			tenantID, roleID).Scan(&roleCode, &roleState)
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrRBACRoleNotFound
		}
		if err != nil {
			return fmt.Errorf("lock role grants: %w", err)
		}
		if roleState != "active" {
			return app.ErrRBACInvalidState
		}
		capabilities, grantedMenus, err := r.resolveGrantMenus(txCtx, tx, tenantID, menuIDs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(txCtx, `DELETE FROM role_menus WHERE tenant_id = ? AND role_id = ?`, tenantID, roleID); err != nil {
			return fmt.Errorf("clear role grants: %w", err)
		}
		for _, menuID := range grantedMenus {
			if _, err := tx.ExecContext(txCtx, `INSERT INTO role_menus (tenant_id, role_id, menu_id) VALUES (?, ?, ?)`,
				tenantID, roleID, menuID); err != nil {
				return fmt.Errorf("insert role grant: %w", err)
			}
		}
		changed, err := r.policy.ReplaceManagedCapabilitiesInTx(txCtx, tx, policyActor, roleCode, domain, capabilities)
		if err != nil {
			return err
		}
		if changed {
			if err := r.policy.EnsurePermissionAdministratorInTx(txCtx, tx, domain); err != nil {
				return err
			}
		}
		return insertAuditTx(txCtx, tx, auditInput{
			tenantID: tenantID, actor: actor, operation: "role.grants.replace", objectType: "role",
			objectID: strconv.FormatUint(roleID, 10), after: map[string]any{"menus": menuIDs, "capabilities": capabilities},
		})
	})
	if err != nil {
		return mapPolicyError(fmt.Errorf("replace role grants transaction: %w", err))
	}
	return nil
}

// resolveGrantMenus validates the requested IDs inside the transaction.
// Structural catalog/page nodes are accepted for display but not stored;
// operator buttons and unknown/disabled/cross-tenant IDs reject the request.
func (r *MySQLRBACRepository) resolveGrantMenus(ctx context.Context, tx *sql.Tx, tenantID uint64, menuIDs []uint64) ([]authz.Capability, []uint64, error) {
	seen := make(map[uint64]struct{}, len(menuIDs))
	uniqueIDs := make([]uint64, 0, len(menuIDs))
	for _, menuID := range menuIDs {
		if menuID == 0 {
			return nil, nil, app.ErrRBACInvalidRelation
		}
		if _, exists := seen[menuID]; exists {
			continue
		}
		seen[menuID] = struct{}{}
		uniqueIDs = append(uniqueIDs, menuID)
	}
	if len(uniqueIDs) == 0 {
		return []authz.Capability{}, []uint64{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(uniqueIDs)), ",")
	args := make([]any, 0, len(uniqueIDs)+1)
	args = append(args, tenantID)
	for _, id := range uniqueIDs {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, `SELECT menu_id, menu_type, capability, web_perms, disabled_flag
FROM menus WHERE tenant_id = ? AND menu_id IN (`+placeholders+`) FOR UPDATE`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("lock grant menus: %w", err)
	}
	defer func() { _ = rows.Close() }()
	found := make(map[uint64]struct {
		typ        int
		capability string
		webPerms   string
		disabled   bool
	}, len(uniqueIDs))
	for rows.Next() {
		var id uint64
		var typ int
		var capability, webPerms string
		var disabled bool
		if err := rows.Scan(&id, &typ, &capability, &webPerms, &disabled); err != nil {
			return nil, nil, fmt.Errorf("scan grant menu: %w", err)
		}
		found[id] = struct {
			typ        int
			capability string
			webPerms   string
			disabled   bool
		}{typ: typ, capability: capability, webPerms: webPerms, disabled: disabled}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate grant menus: %w", err)
	}
	managed := make(map[authz.Capability]struct{})
	capabilities := make([]authz.Capability, 0)
	grantedMenus := make([]uint64, 0)
	for _, menuID := range uniqueIDs {
		menu, ok := found[menuID]
		if !ok || menu.disabled {
			return nil, nil, app.ErrRBACInvalidRelation
		}
		if menu.typ != 3 {
			// Catalog and page nodes structure the tree but create no policy.
			continue
		}
		if menu.webPerms != "" {
			// Current-operator-only buttons can never be saved as target grants.
			return nil, nil, app.ErrRBACInvalidRelation
		}
		if menu.capability == "" {
			return nil, nil, app.ErrRBACInvalidRelation
		}
		capability := authz.Capability(menu.capability)
		if _, exists := managed[capability]; !exists {
			managed[capability] = struct{}{}
			capabilities = append(capabilities, capability)
		}
		grantedMenus = append(grantedMenus, menuID)
	}
	return capabilities, grantedMenus, nil
}

func (r *MySQLRBACRepository) MenusForSubject(ctx context.Context, tenantID uint64, subject string) ([]app.MenuRecord, error) {
	rows, err := r.store.QueryRows(ctx, `SELECT DISTINCT m.menu_id, m.parent_id, m.menu_name, m.menu_type, m.sort_order,
m.path, m.frame_url, m.frame_flag, m.cache_flag, m.component, m.web_perms, m.capability, m.icon, m.visible_flag, m.disabled_flag, m.system_flag
FROM casbin_rules AS g
JOIN roles AS r ON r.tenant_id = ? AND r.role_state = 'active' AND r.role_code = g.v1
JOIN casbin_rules AS p ON p.ptype = 'p' AND p.domain = g.domain AND p.v0 = g.v1 AND p.v3 = 'EXECUTE'
JOIN menus AS m ON m.tenant_id = ? AND m.capability = p.v2 AND m.disabled_flag = 0
WHERE g.ptype = 'g' AND g.domain = ? AND g.v0 = ?
ORDER BY m.sort_order ASC, m.menu_id ASC`,
		tenantID, tenantID, strconv.FormatUint(tenantID, 10), strings.TrimSpace(subject))
	if err != nil {
		return nil, fmt.Errorf("query subject menus: %w", err)
	}
	defer func() { _ = rows.Close() }()
	menus := make([]app.MenuRecord, 0)
	for rows.Next() {
		menu, err := scanMenu(rows)
		if err != nil {
			return nil, err
		}
		menus = append(menus, menu)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate menus: %w", err)
	}
	return menus, nil
}

// EnsureTenantCatalog seeds reserved role rows and the deployment-owned menu
// catalog idempotently for one tenant.
func (r *MySQLRBACRepository) EnsureTenantCatalog(ctx context.Context, tenantID uint64) error {
	return r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, func(txCtx context.Context, tx *sql.Tx) error {
		now := time.Now().UTC()
		for _, role := range seedRoles {
			if _, err := tx.ExecContext(txCtx, `INSERT IGNORE INTO roles
(tenant_id, role_code, role_name, remark, role_state, system_flag, created_by, updated_by, created_at, updated_at)
VALUES (?, ?, ?, ?, 'active', 1, 'system-bootstrap', 'system-bootstrap', ?, ?)`,
				tenantID, role.code, role.name, role.remark, now, now); err != nil {
				return fmt.Errorf("seed role %s: %w", role.code, err)
			}
		}
		for _, menu := range seedMenus {
			if _, err := tx.ExecContext(txCtx, `INSERT IGNORE INTO menus
(tenant_id, menu_id, parent_id, menu_name, menu_type, sort_order, path, component, web_perms, capability, icon, visible_flag, disabled_flag, system_flag, created_by, updated_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0, 1, 'system-bootstrap', 'system-bootstrap', ?, ?)`,
				tenantID, menu.id, menu.parentID, menu.name, menu.typ, menu.sort, menu.path, menu.component,
				menu.webPerms, menu.capability, menu.icon, now, now); err != nil {
				return fmt.Errorf("seed menu %d: %w", menu.id, err)
			}
		}
		return nil
	})
}

// EffectiveManagers returns subjects who currently retain the complete
// management capability set in the tenant. It is the read-side guard used
// before identity-level changes that run on a different connection pool.
func (r *MySQLRBACRepository) EffectiveManagers(ctx context.Context, tenantID uint64) ([]string, error) {
	domain := strconv.FormatUint(tenantID, 10)
	rows, err := r.store.QueryRows(ctx, `SELECT ptype, v0, v1, v2, v3 FROM casbin_rules WHERE domain = ?`, domain)
	if err != nil {
		return nil, fmt.Errorf("query management state: %w", err)
	}
	defer func() { _ = rows.Close() }()
	grants := make(map[string]map[authz.Capability]bool)
	bindings := make(map[string][]string)
	for rows.Next() {
		var ptype, v0, v1, v2, v3 string
		if err := rows.Scan(&ptype, &v0, &v1, &v2, &v3); err != nil {
			return nil, fmt.Errorf("scan management state: %w", err)
		}
		if ptype == "g" {
			bindings[v0] = append(bindings[v0], v1)
		}
		if ptype == "p" && v3 == authz.CapabilityAction {
			if grants[v0] == nil {
				grants[v0] = make(map[authz.Capability]bool)
			}
			grants[v0][authz.Capability(v2)] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate management state: %w", err)
	}
	required := []authz.Capability{
		authz.CapabilityAuthorizationRoleRead,
		authz.CapabilityAuthorizationRoleManage,
		authz.CapabilityAuthorizationRoleMemberAdd,
		authz.CapabilityAuthorizationRoleMemberRemove,
	}
	managers := make([]string, 0)
	for subject, roles := range bindings {
		complete := true
		for _, capability := range required {
			allowed := false
			for _, role := range roles {
				allowed = allowed || grants[role][capability]
			}
			complete = complete && allowed
		}
		if complete {
			managers = append(managers, subject)
		}
	}
	return managers, nil
}

type auditInput struct {
	tenantID   uint64
	actor      app.RBACActor
	operation  string
	objectType string
	objectID   string
	before     any
	after      any
}

func (r *MySQLRBACRepository) insertAudit(ctx context.Context, input auditInput) error {
	return r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		return insertAuditTx(txCtx, tx, input)
	})
}

func insertAuditTx(ctx context.Context, tx *sql.Tx, input auditInput) error {
	before, err := optionalJSON(input.before)
	if err != nil {
		return err
	}
	after, err := optionalJSON(input.after)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_audit_logs
(tenant_id, actor_subject, operation, object_type, object_id, before_state, after_state, request_id, trace_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.tenantID, input.actor.Subject, input.operation, input.objectType, input.objectID,
		before, after, input.actor.RequestID, input.actor.TraceID); err != nil {
		return fmt.Errorf("insert admin audit log: %w", err)
	}
	return nil
}

func optionalJSON(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal audit state: %w", err)
	}
	return string(data), nil
}

func policyActor(actor app.RBACActor, domain string) authz.PolicyActor {
	return authz.PolicyActor{Subject: actor.Subject, Domain: domain, RequestID: actor.RequestID, TraceID: actor.TraceID}
}

func parseDomainTenant(domain string) (uint64, error) {
	tenantID, err := strconv.ParseUint(strings.TrimSpace(domain), 10, 64)
	if err != nil || tenantID == 0 {
		return 0, fmt.Errorf("invalid policy domain %q", domain)
	}
	return tenantID, nil
}

func isDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func mapPolicyError(err error) error {
	switch {
	case errors.Is(err, authz.ErrLastTenantAdmin):
		return authz.ErrLastTenantAdmin
	case errors.Is(err, app.ErrRBACRoleNotFound), errors.Is(err, app.ErrRBACMenuNotFound),
		errors.Is(err, app.ErrRBACRoleInUse), errors.Is(err, app.ErrRBACReservedRole),
		errors.Is(err, app.ErrRBACDuplicateCode), errors.Is(err, app.ErrRBACInvalidRelation),
		errors.Is(err, app.ErrRBACInvalidState):
		return err
	}
	return err
}

func requireChanged(result sql.Result, noRowErr error) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect affected rows: %w", err)
	}
	if rows == 0 {
		return noRowErr
	}
	return nil
}

func boolToTinyInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRole(rows rowScanner) (app.RoleRecord, error) {
	var role app.RoleRecord
	var system bool
	if err := rows.Scan(&role.ID, &role.TenantID, &role.Code, &role.Name, &role.Remark, &role.State, &system, &role.CreatedAt, &role.UpdatedAt); err != nil {
		return app.RoleRecord{}, fmt.Errorf("scan role: %w", err)
	}
	role.System = system
	return role, nil
}

func scanMenu(rows rowScanner) (app.MenuRecord, error) {
	var menu app.MenuRecord
	var visible, disabled, system bool
	if err := rows.Scan(&menu.ID, &menu.ParentID, &menu.Name, &menu.Type, &menu.Sort,
		&menu.Path, &menu.FrameURL, &menu.FrameFlag, &menu.CacheFlag, &menu.Component, &menu.WebPerms, &menu.Capability, &menu.Icon,
		&visible, &disabled, &system); err != nil {
		return app.MenuRecord{}, fmt.Errorf("scan menu: %w", err)
	}
	menu.Visible, menu.Disabled, menu.System = visible, disabled, system
	return menu, nil
}
