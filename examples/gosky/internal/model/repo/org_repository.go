// MySQL persistence for tenant departments, positions, and the employee
// directory. Identity rows (users/tenant_users) remain the authentication
// authority; these queries enrich them with organization placement and
// role metadata. Every value is bound; identifiers stay code constants.
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
)

type orgRepository struct {
	store *storage.SQLStore
	rbac  *MySQLRBACRepository
}

// NewOrgRepository builds the organization repository. The shared RBAC
// repository supplies role lookups for employee projections.
func NewOrgRepository(store *storage.SQLStore, rbac *MySQLRBACRepository) app.OrgStore {
	return &orgRepository{store: store, rbac: rbac}
}

type orgAuditInput struct {
	tenantID   uint64
	actor      app.RBACActor
	operation  string
	objectType string
	objectID   string
	after      any
}

func (r *orgRepository) orgAudit(ctx context.Context, in orgAuditInput) error {
	subject := in.actor.Subject
	if subject == "" {
		subject = "system"
	}
	_, err := r.store.Exec(ctx, `INSERT INTO admin_audit_logs
(tenant_id, actor_subject, operation, object_type, object_id, before_state, after_state, request_id, trace_id)
VALUES (?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
		in.tenantID, subject, in.operation, in.objectType, in.objectID, mustJSON(in.after),
		in.actor.RequestID, in.actor.TraceID)
	return err
}

func mustJSON(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return string(data)
}

func isDuplicateOrg(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// ---------------- departments ----------------

func scanDepartment(scanner interface {
	Scan(dest ...any) error
}) (app.Department, error) {
	var d app.Department
	var managerID sql.NullInt64
	if err := scanner.Scan(&d.ID, &d.TenantID, &d.Name, &managerID, &d.ParentID, &d.Sort,
		&d.State, &d.System, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return app.Department{}, err
	}
	if managerID.Valid {
		d.ManagerID = uint64(managerID.Int64)
	}
	return d, nil
}

const departmentColumns = `department_id, tenant_id, name, manager_id, parent_id, sort, state, system_flag, created_at, updated_at`

func (r *orgRepository) CreateDepartment(ctx context.Context, tenantID uint64, input app.DepartmentInput, actor app.RBACActor) (app.Department, error) {
	if err := validateDepartmentInput(tenantID, input); err != nil {
		return app.Department{}, err
	}
	if input.ParentID != 0 {
		parent, ok, err := r.getDepartment(ctx, tenantID, input.ParentID)
		if err != nil {
			return app.Department{}, err
		}
		if !ok || parent.State != "active" {
			return app.Department{}, app.ErrOrgInvalidRelation
		}
	}
	now := time.Now().UTC()
	result, err := r.store.Exec(ctx, `INSERT INTO departments
(tenant_id, name, manager_id, parent_id, sort, state, system_flag, created_at, updated_at)
VALUES (?, ?, NULLIF(?,0), ?, ?, 'active', 0, ?, ?)`,
		tenantID, strings.TrimSpace(input.Name), input.ManagerID, input.ParentID, input.Sort, now, now)
	if err != nil {
		if isDuplicateOrg(err) {
			return app.Department{}, app.ErrOrgDuplicate
		}
		return app.Department{}, fmt.Errorf("create department: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return app.Department{}, err
	}
	if err := r.orgAudit(ctx, orgAuditInput{tenantID, actor, "department.create", "department", strconv.FormatInt(id, 10), input}); err != nil {
		return app.Department{}, err
	}
	d, _, err := r.getDepartment(ctx, tenantID, uint64(id))
	return d, err
}

func (r *orgRepository) UpdateDepartment(ctx context.Context, tenantID, id uint64, input app.DepartmentInput, actor app.RBACActor) error {
	if err := validateDepartmentInput(tenantID, input); err != nil {
		return err
	}
	existing, ok, err := r.getDepartment(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if !ok {
		return app.ErrOrgNotFound
	}
	if existing.System {
		return app.ErrOrgReserved
	}
	if input.ParentID == id {
		return app.ErrOrgInvalidRelation
	}
	result, err := r.store.Exec(ctx, `UPDATE departments
SET name = ?, manager_id = NULLIF(?,0), parent_id = ?, sort = ?, updated_at = ?
WHERE tenant_id = ? AND department_id = ? AND state <> 'deleted'`,
		strings.TrimSpace(input.Name), input.ManagerID, input.ParentID, input.Sort, time.Now().UTC(), tenantID, id)
	if err != nil {
		if isDuplicateOrg(err) {
			return app.ErrOrgDuplicate
		}
		return fmt.Errorf("update department: %w", err)
	}
	if err := changedRows(result); err != nil {
		return err
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "department.update", "department", strconv.FormatUint(id, 10), input})
}

func (r *orgRepository) DeleteDepartment(ctx context.Context, tenantID, id uint64, actor app.RBACActor) error {
	existing, ok, err := r.getDepartment(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if !ok {
		return app.ErrOrgNotFound
	}
	if existing.System {
		return app.ErrOrgReserved
	}
	var childCount int
	if err := r.store.QueryOne(ctx, `SELECT COUNT(*) FROM departments WHERE tenant_id = ? AND parent_id = ? AND state <> 'deleted'`,
		func(row *sql.Row) error { return row.Scan(&childCount) }, tenantID, id); err != nil {
		return err
	}
	if childCount > 0 {
		return app.ErrOrgHasChildren
	}
	var memberCount int
	if err := r.store.QueryOne(ctx, `SELECT COUNT(*) FROM tenant_users WHERE tenant_id = ? AND department_id = ? AND member_state = 'active'`,
		func(row *sql.Row) error { return row.Scan(&memberCount) }, tenantID, id); err != nil {
		return err
	}
	if memberCount > 0 {
		return app.ErrOrgHasMembers
	}
	if _, err := r.store.Exec(ctx, `DELETE FROM departments WHERE tenant_id = ? AND department_id = ?`, tenantID, id); err != nil {
		return fmt.Errorf("delete department: %w", err)
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "department.delete", "department", strconv.FormatUint(id, 10), nil})
}

func (r *orgRepository) ListDepartments(ctx context.Context, tenantID uint64) ([]app.Department, error) {
	rows, err := r.store.QueryRows(ctx, `SELECT `+departmentColumns+` FROM departments
WHERE tenant_id = ? AND state <> 'deleted' ORDER BY parent_id, sort ASC, department_id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list departments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]app.Department, 0)
	for rows.Next() {
		d, err := scanDepartment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate departments: %w", err)
	}
	names := r.managerNames(ctx, out)
	for i := range out {
		out[i].ManagerName = names[out[i].ManagerID]
	}
	return out, nil
}

func (r *orgRepository) managerNames(ctx context.Context, departments []app.Department) map[uint64]string {
	ids := make([]any, 0, len(departments))
	seen := make(map[uint64]struct{})
	for _, d := range departments {
		if d.ManagerID == 0 {
			continue
		}
		if _, ok := seen[d.ManagerID]; ok {
			continue
		}
		seen[d.ManagerID] = struct{}{}
		ids = append(ids, d.ManagerID)
	}
	result := make(map[uint64]string)
	if len(ids) == 0 {
		return result
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = strings.TrimRight(placeholders, ",")
	rows, err := r.store.QueryRows(ctx,
		`SELECT user_id, COALESCE(NULLIF(display_name,''), login_name) FROM users WHERE user_id IN (`+placeholders+`)`, ids...)
	if err != nil {
		return result
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id uint64
		var name string
		if err := rows.Scan(&id, &name); err == nil {
			result[id] = name
		}
	}
	return result
}

func (r *orgRepository) getDepartment(ctx context.Context, tenantID, id uint64) (app.Department, bool, error) {
	var d app.Department
	var managerID sql.NullInt64
	err := r.store.QueryOne(ctx, `SELECT `+departmentColumns+` FROM departments
WHERE tenant_id = ? AND department_id = ? AND state <> 'deleted'`,
		func(row *sql.Row) error {
			return row.Scan(&d.ID, &d.TenantID, &d.Name, &managerID, &d.ParentID, &d.Sort,
				&d.State, &d.System, &d.CreatedAt, &d.UpdatedAt)
		}, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Department{}, false, nil
	}
	if err != nil {
		return app.Department{}, false, fmt.Errorf("get department: %w", err)
	}
	if managerID.Valid {
		d.ManagerID = uint64(managerID.Int64)
	}
	return d, true, nil
}

func validateDepartmentInput(tenantID uint64, input app.DepartmentInput) error {
	if tenantID == 0 || strings.TrimSpace(input.Name) == "" || len([]rune(input.Name)) > 50 {
		return app.ErrOrgInvalidInput
	}
	if input.Sort < 0 || input.Sort > 1_000_000 {
		return app.ErrOrgInvalidInput
	}
	return nil
}

// ---------------- positions ----------------

const positionColumns = `position_id, tenant_id, name, level, sort, remark, state, system_flag, created_at, updated_at`

func scanPosition(scanner interface{ Scan(dest ...any) error }) (app.Position, error) {
	var p app.Position
	if err := scanner.Scan(&p.ID, &p.TenantID, &p.Name, &p.Level, &p.Sort, &p.Remark,
		&p.State, &p.System, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return app.Position{}, err
	}
	return p, nil
}

func (r *orgRepository) CreatePosition(ctx context.Context, tenantID uint64, input app.PositionInput, actor app.RBACActor) (app.Position, error) {
	if err := validatePositionInput(input); err != nil {
		return app.Position{}, err
	}
	now := time.Now().UTC()
	result, err := r.store.Exec(ctx, `INSERT INTO positions
(tenant_id, name, level, sort, remark, state, system_flag, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'active', 0, ?, ?)`,
		tenantID, strings.TrimSpace(input.Name), strings.TrimSpace(input.Level), input.Sort,
		strings.TrimSpace(input.Remark), now, now)
	if err != nil {
		if isDuplicateOrg(err) {
			return app.Position{}, app.ErrOrgDuplicate
		}
		return app.Position{}, fmt.Errorf("create position: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return app.Position{}, err
	}
	if err := r.orgAudit(ctx, orgAuditInput{tenantID, actor, "position.create", "position", strconv.FormatInt(id, 10), input}); err != nil {
		return app.Position{}, err
	}
	return r.getPosition(ctx, tenantID, uint64(id))
}

func (r *orgRepository) UpdatePosition(ctx context.Context, tenantID, id uint64, input app.PositionInput, actor app.RBACActor) error {
	if err := validatePositionInput(input); err != nil {
		return err
	}
	var systemFlag bool
	err := r.store.QueryOne(ctx, `SELECT system_flag FROM positions WHERE tenant_id=? AND position_id=? AND state <> 'deleted'`,
		func(row *sql.Row) error { return row.Scan(&systemFlag) }, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return app.ErrOrgNotFound
	}
	if err != nil {
		return err
	}
	if systemFlag {
		return app.ErrOrgReserved
	}
	result, err := r.store.Exec(ctx, `UPDATE positions
SET name = ?, level = ?, sort = ?, remark = ?, updated_at = ?
WHERE tenant_id = ? AND position_id = ? AND state <> 'deleted'`,
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Level), input.Sort,
		strings.TrimSpace(input.Remark), time.Now().UTC(), tenantID, id)
	if err != nil {
		if isDuplicateOrg(err) {
			return app.ErrOrgDuplicate
		}
		return fmt.Errorf("update position: %w", err)
	}
	if err := changedRows(result); err != nil {
		return err
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "position.update", "position", strconv.FormatUint(id, 10), input})
}

func (r *orgRepository) DeletePosition(ctx context.Context, tenantID, id uint64, actor app.RBACActor) error {
	var systemFlag bool
	if err := r.store.QueryOne(ctx, `SELECT system_flag FROM positions WHERE tenant_id=? AND position_id=? AND state <> 'deleted'`,
		func(row *sql.Row) error { return row.Scan(&systemFlag) }, tenantID, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return app.ErrOrgNotFound
		}
		return err
	}
	if systemFlag {
		return app.ErrOrgReserved
	}
	var memberCount int
	if err := r.store.QueryOne(ctx, `SELECT COUNT(*) FROM tenant_users WHERE tenant_id=? AND position_id=? AND member_state='active'`,
		func(row *sql.Row) error { return row.Scan(&memberCount) }, tenantID, id); err != nil {
		return err
	}
	if memberCount > 0 {
		return app.ErrOrgHasMembers
	}
	if _, err := r.store.Exec(ctx, `DELETE FROM positions WHERE tenant_id=? AND position_id=?`, tenantID, id); err != nil {
		return fmt.Errorf("delete position: %w", err)
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "position.delete", "position", strconv.FormatUint(id, 10), nil})
}

func (r *orgRepository) ListPositions(ctx context.Context, tenantID uint64, keyword string) ([]app.Position, error) {
	rows, err := r.store.QueryRows(ctx, `SELECT `+positionColumns+` FROM positions
WHERE tenant_id = ? AND state <> 'deleted' ORDER BY sort ASC, position_id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]app.Position, 0)
	for rows.Next() {
		position, err := scanPosition(rows)
		if err != nil {
			return nil, err
		}
		if keyword != "" && !strings.Contains(strings.ToLower(position.Name), strings.ToLower(strings.TrimSpace(keyword))) {
			continue
		}
		out = append(out, position)
	}
	return out, rows.Err()
}

func (r *orgRepository) getPosition(ctx context.Context, tenantID, id uint64) (app.Position, error) {
	var p app.Position
	err := r.store.QueryOne(ctx, `SELECT `+positionColumns+` FROM positions
WHERE tenant_id = ? AND position_id = ? AND state <> 'deleted'`,
		func(row *sql.Row) error {
			return row.Scan(&p.ID, &p.TenantID, &p.Name, &p.Level, &p.Sort, &p.Remark,
				&p.State, &p.System, &p.CreatedAt, &p.UpdatedAt)
		}, tenantID, id)
	if err != nil {
		return app.Position{}, fmt.Errorf("get position: %w", err)
	}
	return p, nil
}

func (r *orgRepository) positionExists(ctx context.Context, tenantID, id uint64) (bool, error) {
	var count int
	err := r.store.QueryOne(ctx, `SELECT COUNT(*) FROM positions WHERE tenant_id=? AND position_id=? AND state <> 'deleted'`,
		func(row *sql.Row) error { return row.Scan(&count) }, tenantID, id)
	return count > 0, err
}

func validatePositionInput(input app.PositionInput) error {
	if strings.TrimSpace(input.Name) == "" || len([]rune(input.Name)) > 50 {
		return app.ErrOrgInvalidInput
	}
	if input.Sort < 0 || input.Sort > 1_000_000 || len([]rune(input.Remark)) > 255 || len(input.Level) > 32 {
		return app.ErrOrgInvalidInput
	}
	return nil
}

// ---------------- employees ----------------

const employeeSelect = `SELECT u.user_id, u.subject, u.login_name, COALESCE(NULLIF(u.display_name,''), u.login_name),
       COALESCE(u.avatar_url,''), COALESCE(u.gender,0), COALESCE(u.phone,''), COALESCE(u.email,''),
       COALESCE(tu.department_id,0), COALESCE(d.name,''), COALESCE(tu.position_id,0), COALESCE(p.name,''),
       tu.member_state = 'disabled', u.account_type = 'admin', COALESCE(tu.remark,''), u.created_at
FROM users AS u
JOIN tenant_users AS tu ON tu.user_id = u.user_id
LEFT JOIN departments AS d ON d.tenant_id = tu.tenant_id AND d.department_id = tu.department_id
LEFT JOIN positions AS p ON p.tenant_id = tu.tenant_id AND p.position_id = tu.position_id`

func scanEmployee(scanner interface{ Scan(dest ...any) error }) (app.Employee, error) {
	var e app.Employee
	if err := scanner.Scan(&e.UserID, &e.Subject, &e.LoginName, &e.ActualName,
		&e.Avatar, &e.Gender, &e.Phone, &e.Email,
		&e.DepartmentID, &e.DepartmentName, &e.PositionID, &e.PositionName,
		&e.Disabled, &e.Administrator, &e.Remark, &e.CreatedAt); err != nil {
		return app.Employee{}, err
	}
	e.ID = e.UserID
	return e, nil
}

func normalizeEmployeePage(pageNum, pageSize uint64) (uint64, uint64) {
	if pageNum == 0 {
		pageNum = 1
	}
	if pageSize == 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	// Match the compatibility page contract: an offset beyond the
	// 64-bit range is clamped to the largest representable page, so a
	// wrapped MySQL OFFSET cannot leak rows.
	if pageNum > 2 || pageSize > 0 {
		if pageNum > 1 && (pageNum-1) > (1<<63)/pageSize {
			pageNum = (1<<63)/pageSize + 1
		}
	}
	return pageNum, pageSize
}

func (r *orgRepository) ListEmployees(ctx context.Context, tenantID uint64, query app.EmployeeQuery) (app.EmployeePage, error) {
	pageNum, pageSize := normalizeEmployeePage(query.PageNum, query.PageSize)
	where := []string{"tu.tenant_id = ?", "tu.member_state IN ('active','disabled')"}
	args := []any{tenantID}
	if kw := strings.TrimSpace(query.Keyword); kw != "" {
		like := "%" + kw + "%"
		where = append(where, "(u.login_name LIKE ? OR u.display_name LIKE ? OR u.phone LIKE ? OR u.subject LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if query.DepartmentID != 0 {
		where = append(where, "tu.department_id = ?")
		args = append(args, query.DepartmentID)
	}
	if query.Disabled != nil {
		if *query.Disabled {
			where = append(where, "tu.member_state = 'disabled'")
		} else {
			where = append(where, "tu.member_state = 'active'")
		}
	}
	whereSQL := strings.Join(where, " AND ")
	var total int64
	if err := r.store.QueryOne(ctx, `SELECT COUNT(*) FROM users AS u
JOIN tenant_users AS tu ON tu.user_id=u.user_id WHERE `+whereSQL,
		func(row *sql.Row) error { return row.Scan(&total) }, args...); err != nil {
		return app.EmployeePage{}, err
	}
	args = append(args, pageSize, (pageNum-1)*pageSize)
	rows, err := r.store.QueryRows(ctx, employeeSelect+` WHERE `+whereSQL+
		` ORDER BY u.user_id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return app.EmployeePage{}, fmt.Errorf("list employees: %w", err)
	}
	defer func() { _ = rows.Close() }()
	page := app.EmployeePage{Total: total, PageNum: pageNum, PageSize: pageSize, List: make([]app.Employee, 0)}
	for rows.Next() {
		employee, err := scanEmployee(rows)
		if err != nil {
			return app.EmployeePage{}, err
		}
		page.List = append(page.List, employee)
	}
	if err := rows.Err(); err != nil {
		return app.EmployeePage{}, err
	}
	r.enrichRoles(ctx, tenantID, page.List)
	return page, nil
}

func (r *orgRepository) ListAllEmployees(ctx context.Context, tenantID uint64, includeDisabled bool) ([]app.Employee, error) {
	stateFilter := "tu.member_state = 'active'"
	if includeDisabled {
		stateFilter = "tu.member_state IN ('active','disabled')"
	}
	rows, err := r.store.QueryRows(ctx, employeeSelect+
		` WHERE tu.tenant_id = ? AND `+stateFilter+` ORDER BY u.user_id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list all employees: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]app.Employee, 0)
	for rows.Next() {
		employee, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, employee)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	r.enrichRoles(ctx, tenantID, out)
	return out, nil
}

func (r *orgRepository) EmployeesByDepartment(ctx context.Context, tenantID, departmentID uint64) ([]app.Employee, error) {
	rows, err := r.store.QueryRows(ctx, employeeSelect+
		` WHERE tu.tenant_id = ? AND tu.department_id = ? AND tu.member_state='active' ORDER BY u.user_id ASC`,
		tenantID, departmentID)
	if err != nil {
		return nil, fmt.Errorf("employees by department: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]app.Employee, 0)
	for rows.Next() {
		employee, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, employee)
	}
	return out, rows.Err()
}

// enrichRoles fills SmartAdmin role id/name lists using the g-rules and
// roles table. Fixed roles map to their SmartAdmin constants; custom
// roles use their database ids.
func (r *orgRepository) enrichRoles(ctx context.Context, tenantID uint64, employees []app.Employee) {
	if len(employees) == 0 {
		return
	}
	subjects := make([]any, 0, len(employees))
	for _, employee := range employees {
		subjects = append(subjects, employee.Subject)
	}
	placeholders := strings.Repeat("?,", len(subjects))
	placeholders = strings.TrimRight(placeholders, ",")
	domain := strconv.FormatUint(tenantID, 10)
	rows, err := r.store.QueryRows(ctx, `SELECT g.v0, g.v1 FROM casbin_rules AS g
WHERE g.domain=? AND g.ptype='g' AND g.v0 IN (`+placeholders+`)`, append([]any{domain}, subjects...)...)
	if err != nil {
		return
	}
	type roleRef struct {
		id   uint64
		name string
	}
	bySubject := make(map[string][]roleRef)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var subject, code string
		if err := rows.Scan(&subject, &code); err != nil {
			continue
		}
		bySubject[subject] = append(bySubject[subject], roleRef{id: fixedRoleSmartID(code), name: code})
	}
	// Custom role database ids/names.
	customRows, err := r.store.QueryRows(ctx, `SELECT r.role_id, r.role_code FROM roles r
WHERE r.tenant_id=? AND r.system_flag=0 AND r.role_state='active'`, tenantID)
	if err == nil {
		custom := make(map[string]roleRef)
		defer func() { _ = customRows.Close() }()
		for customRows.Next() {
			var id uint64
			var code string
			if customRows.Scan(&id, &code) == nil {
				custom[code] = roleRef{id: id, name: code}
			}
		}
		for i := range employees {
			refs := bySubject[employees[i].Subject]
			for j := range refs {
				if refs[j].id == 0 {
					if c, ok := custom[refs[j].name]; ok {
						refs[j] = c
					}
				}
			}
			employees[i].RoleIDs = make([]uint64, 0, len(refs))
			employees[i].RoleNames = make([]string, 0, len(refs))
			for _, ref := range refs {
				if ref.id == 0 {
					continue
				}
				employees[i].RoleIDs = append(employees[i].RoleIDs, ref.id)
				employees[i].RoleNames = append(employees[i].RoleNames, ref.name)
			}
		}
	}
}

func fixedRoleSmartID(code string) uint64 {
	switch code {
	case "reader":
		return 810000101
	case "writer":
		return 810000102
	case "admin":
		return 810000103
	}
	return 0
}

// CreateEmployee inserts the global user and its tenant membership with
// organization placement and profile fields in one transaction. Role
// bindings are handled by the application layer through the audited
// policy service. Password arrives already Argon2id-hashed.
func (r *orgRepository) CreateEmployee(ctx context.Context, tenantID uint64, input app.EmployeeInput, subject string, actor app.RBACActor) (app.Employee, error) {
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		now := time.Now().UTC()
		result, err := tx.ExecContext(txCtx, `INSERT INTO users
(subject, login_name, password_hash, account_type, display_name, gender, phone, email, user_state, created_at, updated_at)
VALUES (?, ?, ?, 'both', ?, ?, NULLIF(?,''), NULLIF(?,''), 'active', ?, ?)`,
			subject, strings.TrimSpace(input.LoginName), input.Password, strings.TrimSpace(input.ActualName),
			input.Gender, strings.TrimSpace(input.Phone), strings.TrimSpace(input.Email),
			now, now)
		if err != nil {
			return err
		}
		userID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(txCtx, `INSERT INTO tenant_users
(tenant_id, user_id, member_realm, member_state, display_name, department_id, position_id, remark, joined_at, created_at, updated_at)
VALUES (?, ?, 'both', 'active', ?, NULLIF(?,0), NULLIF(?,0), ?, NOW(6), NOW(6), NOW(6))`,
			tenantID, userID, strings.TrimSpace(input.ActualName), input.DepartmentID, input.PositionID,
			strings.TrimSpace(input.Remark)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if isDuplicateOrg(err) {
			return app.Employee{}, app.ErrOrgDuplicate
		}
		return app.Employee{}, fmt.Errorf("create employee: %w", err)
	}
	if err := r.orgAudit(ctx, orgAuditInput{tenantID, actor, "employee.create", "user", subject, input}); err != nil {
		return app.Employee{}, err
	}
	return r.getEmployeeBySubject(ctx, tenantID, subject)
}

// UpdateEmployee changes profile and organization placement. Login name,
// password, and subject are immutable here.
func (r *orgRepository) UpdateEmployee(ctx context.Context, tenantID uint64, input app.EmployeeInput, actor app.RBACActor) error {
	result, err := r.store.Exec(ctx, `UPDATE users u
JOIN tenant_users tu ON tu.user_id = u.user_id AND tu.tenant_id = ?
SET u.display_name = ?, u.gender = ?, u.phone = NULLIF(?,''), u.email = NULLIF(?,''), u.updated_at = ?,
    tu.display_name = ?, tu.department_id = NULLIF(?,0), tu.position_id = NULLIF(?,0), tu.remark = ?, tu.updated_at = ?
WHERE u.user_id = ? AND tu.member_state IN ('active','disabled')`,
		tenantID, strings.TrimSpace(input.ActualName), input.Gender, strings.TrimSpace(input.Phone),
		strings.TrimSpace(input.Email), time.Now().UTC(),
		strings.TrimSpace(input.ActualName), input.DepartmentID, input.PositionID,
		strings.TrimSpace(input.Remark), time.Now().UTC(), input.EmployeeID)
	if err != nil {
		if isDuplicateOrg(err) {
			return app.ErrOrgDuplicate
		}
		return fmt.Errorf("update employee: %w", err)
	}
	if err := changedRows(result); err != nil {
		return err
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "employee.update", "user",
		strconv.FormatUint(input.EmployeeID, 10), input})
}

func (r *orgRepository) SetEmployeeDisabled(ctx context.Context, tenantID, userID uint64, disabled bool, actor app.RBACActor) error {
	return r.employeeStateChange(ctx, tenantID, []uint64{userID}, disabled, actor, "employee.disable")
}

func (r *orgRepository) DeleteEmployees(ctx context.Context, tenantID uint64, ids []uint64, actor app.RBACActor) error {
	if len(ids) == 0 || len(ids) > 100 {
		return app.ErrOrgInvalidInput
	}
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		now := time.Now().UTC()
		if _, err := tx.ExecContext(txCtx, `UPDATE tenant_users SET member_state='deleted', left_at=?, updated_at=?
WHERE tenant_id=? AND member_state IN ('active','disabled') AND user_id IN (`+inInts(ids)+`)`, now, now, tenantID); err != nil {
			return err
		}
		return revokeEmployeeSessions(txCtx, tx, tenantID, ids, now)
	})
	if err != nil {
		return fmt.Errorf("delete employees: %w", err)
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "employee.delete", "user", fmt.Sprint(ids), nil})
}

func (r *orgRepository) MoveEmployeesDepartment(ctx context.Context, tenantID uint64, ids []uint64, departmentID uint64, actor app.RBACActor) error {
	if len(ids) == 0 || len(ids) > 100 || departmentID == 0 {
		return app.ErrOrgInvalidInput
	}
	var exists int
	if err := r.store.QueryOne(ctx, `SELECT COUNT(*) FROM departments WHERE tenant_id=? AND department_id=? AND state='active'`,
		func(row *sql.Row) error { return row.Scan(&exists) }, tenantID, departmentID); err != nil {
		return err
	}
	if exists == 0 {
		return app.ErrOrgInvalidRelation
	}
	result, err := r.store.Exec(ctx, `UPDATE tenant_users SET department_id=?, updated_at=?
WHERE tenant_id=? AND member_state='active' AND user_id IN (`+inInts(ids)+`)`,
		departmentID, time.Now().UTC(), tenantID)
	if err != nil {
		return fmt.Errorf("move employees: %w", err)
	}
	if err := changedRows(result); err != nil {
		return err
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "employee.move_department", "user", fmt.Sprint(ids),
		map[string]any{"departmentId": departmentID}})
}

func (r *orgRepository) ResetEmployeePassword(ctx context.Context, tenantID, userID uint64, passwordHash string, actor app.RBACActor) error {
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(txCtx, `UPDATE users SET password_hash=?, updated_at=? WHERE user_id=?`,
			passwordHash, time.Now().UTC(), userID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return app.ErrOrgNotFound
		}
		now := time.Now().UTC()
		return revokeEmployeeSessions(txCtx, tx, tenantID, []uint64{userID}, now)
	})
	if err != nil {
		return mapOrgError(err)
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "employee.reset_password", "user",
		strconv.FormatUint(userID, 10), nil})
}

func (r *orgRepository) UpdateEmployeeCenter(ctx context.Context, tenantID, userID uint64, input app.EmployeeCenterInput, actor app.RBACActor) error {
	result, err := r.store.Exec(ctx, `UPDATE users u
JOIN tenant_users tu ON tu.user_id=u.user_id AND tu.tenant_id=?
SET u.display_name=?, u.gender=?, u.phone=NULLIF(?,''), u.email=NULLIF(?,''), u.updated_at=?
WHERE u.user_id=? AND tu.member_state='active'`,
		tenantID, strings.TrimSpace(input.ActualName), input.Gender, strings.TrimSpace(input.Phone),
		strings.TrimSpace(input.Email), time.Now().UTC(), userID)
	if err != nil {
		return fmt.Errorf("update employee center: %w", err)
	}
	if err := changedRows(result); err != nil {
		return err
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "employee.update_center", "user",
		strconv.FormatUint(userID, 10), input})
}

func (r *orgRepository) UpdateEmployeeAvatar(ctx context.Context, tenantID, userID uint64, avatar string, actor app.RBACActor) error {
	result, err := r.store.Exec(ctx, `UPDATE users u
JOIN tenant_users tu ON tu.user_id=u.user_id AND tu.tenant_id=?
SET u.avatar_url=NULLIF(?,''), u.updated_at=?
WHERE u.user_id=? AND tu.member_state='active'`,
		tenantID, strings.TrimSpace(avatar), time.Now().UTC(), userID)
	if err != nil {
		return fmt.Errorf("update avatar: %w", err)
	}
	if err := changedRows(result); err != nil {
		return err
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, "employee.update_avatar", "user",
		strconv.FormatUint(userID, 10), nil})
}

func (r *orgRepository) EnsureTenantOrganization(ctx context.Context, tenantID uint64) error {
	if tenantID == 0 {
		return nil
	}
	_, err := r.store.Exec(ctx, `INSERT IGNORE INTO departments
(tenant_id, name, parent_id, sort, state, system_flag, created_at, updated_at)
VALUES (?, '总公司', 0, 1, 'active', 1, NOW(6), NOW(6))`, tenantID)
	return err
}

// EnsureAllTenants seeds the root department for every tenant missing
// one. Runs at service startup; idempotent.
func (r *orgRepository) EnsureAllTenants(ctx context.Context) error {
	_, err := r.store.Exec(ctx, `INSERT IGNORE INTO departments
(tenant_id, name, parent_id, sort, state, system_flag, created_at, updated_at)
SELECT t.tenant_id, '总公司', 0, 1, 'active', 1, NOW(6), NOW(6)
FROM tenants AS t
WHERE NOT EXISTS (
    SELECT 1 FROM departments d WHERE d.tenant_id = t.tenant_id AND d.parent_id = 0
)`)
	return err
}

func (r *orgRepository) employeeStateChange(ctx context.Context, tenantID uint64, ids []uint64, disabled bool, actor app.RBACActor, operation string) error {
	state := "active"
	if disabled {
		state = "disabled"
	}
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(txCtx context.Context, tx *sql.Tx) error {
		now := time.Now().UTC()
		result, err := tx.ExecContext(txCtx, `UPDATE tenant_users SET member_state=?, left_at=CASE WHEN ?='disabled' THEN ? ELSE NULL END, updated_at=?
WHERE tenant_id=? AND member_state IN ('active','disabled') AND user_id IN (`+inInts(ids)+`)`,
			state, state, now, now, tenantID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return app.ErrOrgNotFound
		}
		if disabled {
			return revokeEmployeeSessions(txCtx, tx, tenantID, ids, now)
		}
		return nil
	})
	if err != nil {
		return mapOrgError(err)
	}
	return r.orgAudit(ctx, orgAuditInput{tenantID, actor, operation, "user", fmt.Sprint(ids),
		map[string]any{"disabled": disabled}})
}

func (r *orgRepository) getEmployeeBySubject(ctx context.Context, tenantID uint64, subject string) (app.Employee, error) {
	var employee app.Employee
	err := r.store.QueryOne(ctx, employeeSelect+` WHERE tu.tenant_id=? AND u.subject=?`,
		func(row *sql.Row) error {
			e, scanErr := scanEmployee(row)
			if scanErr != nil {
				return scanErr
			}
			employee = e
			return nil
		}, tenantID, subject)
	if err != nil {
		return app.Employee{}, fmt.Errorf("get employee: %w", err)
	}
	r.enrichRoles(ctx, tenantID, []app.Employee{employee})
	return employee, nil
}

func revokeEmployeeSessions(ctx context.Context, tx *sql.Tx, tenantID uint64, userIDs []uint64, now time.Time) error {
	if len(userIDs) == 0 {
		return nil
	}
	query := `UPDATE auth_sessions SET revoked_at = ?, last_seen_at = ?
WHERE user_id IN (` + inInts(userIDs) + `) AND tenant_id = ? AND revoked_at IS NULL AND expires_at > ?`
	// User IDs are inlined as validated decimal strings by inInts, so
	// only the timestamps and tenant use bound parameters.
	args := []any{now, now, tenantID, now}
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func inInts(ids []uint64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatUint(id, 10)
	}
	return strings.Join(parts, ",")
}

func changedRows(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return app.ErrOrgNotFound
	}
	return nil
}

func mapOrgError(err error) error {
	switch {
	case errors.Is(err, app.ErrOrgNotFound), errors.Is(err, app.ErrOrgDuplicate),
		errors.Is(err, app.ErrOrgReserved), errors.Is(err, app.ErrOrgInvalidRelation),
		errors.Is(err, app.ErrOrgInvalidInput), errors.Is(err, app.ErrOrgHasMembers),
		errors.Is(err, app.ErrOrgHasChildren):
		return err
	default:
		return err
	}
}

// SubjectRoles returns the role codes currently bound to a subject
// within one tenant.
func (r *orgRepository) SubjectRoles(ctx context.Context, tenantID uint64, subject string) ([]string, error) {
	domain := strconv.FormatUint(tenantID, 10)
	rows, err := r.store.QueryRows(ctx,
		`SELECT v1 FROM casbin_rules WHERE ptype='g' AND domain=? AND v0=?`, domain, subject)
	if err != nil {
		return nil, fmt.Errorf("subject roles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	codes := make([]string, 0)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}
