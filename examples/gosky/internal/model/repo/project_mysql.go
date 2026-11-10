package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/imajinyun/gofly/core/storage"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

const projectSelectByIDSQL = `SELECT project_id, tenant_id, owner_subject, project_name, project_memo
FROM projects
WHERE project_id = ? AND project_state <> 'deleted'
LIMIT 1`

const projectActiveMemberRoleSQL = `SELECT member_role
FROM project_members
WHERE project_id = ? AND project_tenant_id = ? AND member_subject = ? AND member_state = 'active'
LIMIT 1`

const projectActiveMembersSQL = `SELECT member_subject, member_role
FROM project_members
WHERE project_id = ?
	AND project_tenant_id = ?
	AND member_state = 'active'
	AND member_role IN ('viewer', 'editor')
ORDER BY member_subject ASC
LIMIT ?`

const projectListTenantAdminSQL = `SELECT project_id, tenant_id, owner_subject, project_name, project_memo
FROM projects
WHERE tenant_id = ? AND project_state <> 'deleted' AND project_id > ?
ORDER BY project_id ASC
LIMIT ?`

const projectListRelationScopeSQL = `SELECT p.project_id, p.tenant_id, p.owner_subject, p.project_name, p.project_memo
FROM projects AS p
LEFT JOIN project_members AS pm
	ON pm.project_id = p.project_id
	AND pm.project_tenant_id = p.tenant_id
	AND pm.member_subject = ?
	AND pm.member_state = 'active'
WHERE p.tenant_id = ?
	AND p.project_state <> 'deleted'
	AND p.project_id > ?
	AND (p.owner_subject = ? OR pm.member_role IN ('viewer', 'editor'))
ORDER BY p.project_id ASC
LIMIT ?`

const projectOperationInsertSQL = `INSERT INTO opslogs (
	operator_tenant_id,
	operator_subject,
	project_tenant_id,
	project_id,
	target_subject,
	target_role,
	operation,
	outcome,
	auth_basis,
	reason,
	error_code,
	error_body,
	request_id,
	trace_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const projectMemberUpsertSQL = `INSERT INTO project_members (
	project_id,
	project_tenant_id,
	member_subject,
	member_role,
	member_state,
	granted_by,
	revoked_by,
	revoked_at
) VALUES (?, ?, ?, ?, 'active', ?, '', NULL)
ON DUPLICATE KEY UPDATE
	member_role = VALUES(member_role),
	member_state = 'active',
	granted_by = VALUES(granted_by),
	revoked_by = '',
	revoked_at = NULL`

const projectMemberRevokeSQL = `UPDATE project_members
SET member_state = 'revoked', revoked_by = ?, revoked_at = NOW(6)
WHERE project_id = ?
	AND project_tenant_id = ?
	AND member_subject = ?
	AND member_role = ?
	AND member_state = 'active'`

// MySQLProjectRepository is the production Project repository. It uses only
// fixed SQL with bound values and delegates connection lifecycle and query
// observability to gofly's SQLStore.
type MySQLProjectRepository struct {
	store *storage.SQLStore
}

func NewMySQLProjectRepository(store *storage.SQLStore) *MySQLProjectRepository {
	return &MySQLProjectRepository{store: store}
}

func (r *MySQLProjectRepository) GetProject(ctx context.Context, id uint64) (app.Project, error) {
	if r == nil || r.store == nil {
		return app.Project{}, errors.New("project sql store is nil")
	}
	var project app.Project
	err := r.store.QueryOne(ctx, projectSelectByIDSQL, func(row *sql.Row) error {
		return row.Scan(&project.ID, &project.TenantID, &project.OwnerSubject, &project.Name, &project.Description)
	}, id)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Project{}, app.ErrProjectNotFound
	}
	if err != nil {
		return app.Project{}, fmt.Errorf("query project by id: %w", err)
	}
	return project, nil
}

func (r *MySQLProjectRepository) GetActiveMemberRole(ctx context.Context, projectID uint64, tenantID uint64, subject string) (app.ProjectMemberRole, error) {
	if r == nil || r.store == nil {
		return "", errors.New("project sql store is nil")
	}
	var role app.ProjectMemberRole
	err := r.store.QueryOne(ctx, projectActiveMemberRoleSQL, func(row *sql.Row) error {
		return row.Scan(&role)
	}, projectID, tenantID, subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query active project member role: %w", err)
	}
	if !role.Valid() {
		return "", fmt.Errorf("unexpected active project member role %q", role)
	}
	return role, nil
}

// ListActiveMembers returns only active, non-owner relationship roles in a
// deterministic order. All values are bound parameters; SQL structure and
// accepted role values remain code-owned.
func (r *MySQLProjectRepository) ListActiveMembers(ctx context.Context, projectID uint64, tenantID uint64, limit uint) ([]app.ProjectMemberResponse, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("project sql store is nil")
	}
	if projectID == 0 || tenantID == 0 || limit == 0 || limit > 100 {
		return nil, errors.New("project member list scope is invalid")
	}
	items := make([]app.ProjectMemberResponse, 0, limit)
	err := r.store.QueryAll(ctx, projectActiveMembersSQL, func(rows *sql.Rows) error {
		for rows.Next() {
			var item app.ProjectMemberResponse
			if err := rows.Scan(&item.Subject, &item.Role); err != nil {
				return fmt.Errorf("scan project member list row: %w", err)
			}
			if !app.ValidProjectMemberSubject(item.Subject) || !item.Role.Valid() {
				return fmt.Errorf("unexpected active project member %q with role %q", item.Subject, item.Role)
			}
			items = append(items, item)
		}
		return nil
	}, projectID, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("query project member list: %w", err)
	}
	return items, nil
}

// ListProjects applies the already validated tenant data scope with fixed SQL.
// The relation query binds the subject and tenant values; it never expands an
// untrusted request value into a predicate or SQL identifier.
func (r *MySQLProjectRepository) ListProjects(ctx context.Context, scope app.ProjectDataScope) ([]app.Project, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("project sql store is nil")
	}
	if !scope.Valid() {
		return nil, errors.New("project data scope is invalid")
	}
	projects := make([]app.Project, 0, scope.Limit+1)
	limit := scope.Limit + 1
	query := projectListRelationScopeSQL
	args := []any{scope.Subject, scope.TenantID, scope.AfterID, scope.Subject, limit}
	if scope.TenantAdmin {
		query = projectListTenantAdminSQL
		args = []any{scope.TenantID, scope.AfterID, limit}
	}
	err := r.store.QueryAll(ctx, query, func(rows *sql.Rows) error {
		for rows.Next() {
			var project app.Project
			if err := rows.Scan(&project.ID, &project.TenantID, &project.OwnerSubject, &project.Name, &project.Description); err != nil {
				return fmt.Errorf("scan project list row: %w", err)
			}
			projects = append(projects, project)
		}
		return nil
	}, args...)
	if err != nil {
		return nil, fmt.Errorf("query project list: %w", err)
	}
	return projects, nil
}

func (r *MySQLProjectRepository) RecordProjectOperation(ctx context.Context, operation app.ProjectOperation) error {
	if r == nil || r.store == nil {
		return errors.New("project sql store is nil")
	}
	_, err := r.store.Exec(ctx, projectOperationInsertSQL,
		operation.ActorTenantID,
		operation.ActorSubject,
		operation.ProjectTenantID,
		operation.ProjectID,
		operation.TargetSubject,
		operation.TargetRole,
		operation.Operation,
		operation.Outcome,
		operation.AuthorizationBasis,
		"",
		"",
		"",
		operation.RequestID,
		"",
	)
	if err != nil {
		return fmt.Errorf("insert project operation audit: %w", err)
	}
	return nil
}

// MutateProjectMember writes the relationship and its allowed audit record in
// one transaction. If either write fails, storage.SQLStore rolls back both and
// the caller fails closed.
func (r *MySQLProjectRepository) MutateProjectMember(ctx context.Context, mutation app.ProjectMemberMutation, operation app.ProjectOperation) (bool, error) {
	if r == nil || r.store == nil {
		return false, errors.New("project sql store is nil")
	}
	if !app.ValidProjectMemberSubject(mutation.Subject) || !mutation.Role.Valid() || !app.ValidProjectMemberSubject(mutation.ActorSubject) {
		return false, errors.New("project member mutation is invalid")
	}
	if operation.Outcome != app.ProjectOperationOutcomeAllowed || operation.TargetSubject != mutation.Subject || operation.TargetRole != string(mutation.Role) || operation.ProjectID != mutation.ProjectID || operation.ProjectTenantID != mutation.ProjectTenant || operation.ActorTenantID != mutation.ProjectTenant || operation.ActorSubject != mutation.ActorSubject || mutation.ProjectID == 0 || mutation.ProjectTenant == 0 {
		return false, errors.New("project member audit operation is invalid")
	}
	expectedOperation := app.ProjectMemberUpsertOperation
	if mutation.Revoke {
		expectedOperation = app.ProjectMemberRevokeOperation
	}
	if operation.Operation != expectedOperation {
		return false, errors.New("project member audit operation does not match mutation")
	}

	changed := false
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(transactionCtx context.Context, tx *sql.Tx) error {
		if err := lockProjectAuthorization(transactionCtx, tx, operation); err != nil {
			return err
		}
		var result sql.Result
		var execErr error
		if mutation.Revoke {
			result, execErr = tx.ExecContext(transactionCtx, projectMemberRevokeSQL,
				mutation.ActorSubject,
				mutation.ProjectID,
				mutation.ProjectTenant,
				mutation.Subject,
				mutation.Role,
			)
		} else {
			result, execErr = tx.ExecContext(transactionCtx, projectMemberUpsertSQL,
				mutation.ProjectID,
				mutation.ProjectTenant,
				mutation.Subject,
				mutation.Role,
				mutation.ActorSubject,
			)
		}
		if execErr != nil {
			return fmt.Errorf("mutate project member: %w", execErr)
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("read project member mutation result: %w", rowsErr)
		}
		changed = rows > 0
		if auditErr := insertProjectOperation(transactionCtx, tx, operation); auditErr != nil {
			return auditErr
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("mutate project member transaction: %w", err)
	}
	return changed, nil
}

func insertProjectOperation(ctx context.Context, tx *sql.Tx, operation app.ProjectOperation) error {
	_, err := tx.ExecContext(ctx, projectOperationInsertSQL,
		operation.ActorTenantID,
		operation.ActorSubject,
		operation.ProjectTenantID,
		operation.ProjectID,
		operation.TargetSubject,
		operation.TargetRole,
		operation.Operation,
		operation.Outcome,
		operation.AuthorizationBasis,
		"",
		"",
		"",
		operation.RequestID,
		"",
	)
	if err != nil {
		return fmt.Errorf("insert project operation audit: %w", err)
	}
	return nil
}

var _ app.ProjectRepository = (*MySQLProjectRepository)(nil)
var _ app.ProjectMemberMutationRepository = (*MySQLProjectRepository)(nil)
var _ app.ProjectMemberListRepository = (*MySQLProjectRepository)(nil)
var _ app.ProjectListRepository = (*MySQLProjectRepository)(nil)
