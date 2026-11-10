package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/imajinyun/gofly/examples/gosky/internal/app"
)

const projectUpdateSQL = `UPDATE projects SET project_name=?, project_memo=?, updated_by=? WHERE project_id=? AND tenant_id=? AND project_state<>'deleted'`
const projectDeleteSQL = `UPDATE projects SET project_state='deleted', deleted_at=NOW(6), updated_by=? WHERE project_id=? AND tenant_id=? AND project_state<>'deleted'`

func (r *MySQLProjectRepository) MutateProject(ctx context.Context, mutation app.ProjectMutation, operation app.ProjectOperation) (bool, error) {
	if r == nil || r.store == nil {
		return false, errors.New("project sql store is nil")
	}
	expected := "project.update"
	if mutation.Delete {
		expected = "project.delete"
	}
	if mutation.ProjectID == 0 || mutation.ProjectTenant == 0 || !app.ValidProjectMemberSubject(mutation.ActorSubject) ||
		operation.ProjectID != mutation.ProjectID || operation.ActorTenantID != mutation.ProjectTenant || operation.ProjectTenantID != mutation.ProjectTenant ||
		operation.ActorSubject != mutation.ActorSubject || operation.Operation != expected || operation.Outcome != app.ProjectOperationOutcomeAllowed {
		return false, errors.New("project mutation audit is invalid")
	}
	changed := false
	err := r.store.Transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx *sql.Tx) error {
		if err := lockProjectAuthorization(ctx, tx, operation); err != nil {
			return err
		}
		var result sql.Result
		var err error
		if mutation.Delete {
			result, err = tx.ExecContext(ctx, projectDeleteSQL, mutation.ActorSubject, mutation.ProjectID, mutation.ProjectTenant)
		} else {
			result, err = tx.ExecContext(ctx, projectUpdateSQL, mutation.Name, mutation.Description, mutation.ActorSubject, mutation.ProjectID, mutation.ProjectTenant)
		}
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return app.ErrProjectConflict
			}
			return fmt.Errorf("write project: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read project mutation result: %w", err)
		}
		changed = count > 0
		return insertProjectOperation(ctx, tx, operation)
	})
	if err != nil {
		return false, fmt.Errorf("project mutation transaction: %w", err)
	}
	return changed, nil
}

// Lock the project before its membership rows; every resource mutation uses
// this order. The actor and authorization basis originate in the app service.
func lockProjectAuthorization(ctx context.Context, tx *sql.Tx, operation app.ProjectOperation) error {
	var tenant uint64
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT tenant_id, owner_subject FROM projects WHERE project_id=? AND project_state<>'deleted' FOR UPDATE`, operation.ProjectID).Scan(&tenant, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return app.ErrProjectNotFound
	}
	if err != nil {
		return fmt.Errorf("lock project authorization: %w", err)
	}
	if tenant != operation.ActorTenantID || tenant != operation.ProjectTenantID {
		return app.ErrProjectAccessChanged
	}
	if strings.HasPrefix(operation.Operation, "project.member.") && owner == operation.TargetSubject {
		return app.ErrProjectAccessChanged
	}
	switch operation.AuthorizationBasis {
	case app.ProjectAuthorizationTenantAdmin:
		return nil
	case app.ProjectAuthorizationOwner:
		if owner == operation.ActorSubject {
			return nil
		}
	case app.ProjectAuthorizationEditor:
		if operation.Operation != "project.update" {
			return app.ErrProjectAccessChanged
		}
		var role string
		err := tx.QueryRowContext(ctx, `SELECT member_role FROM project_members WHERE project_id=? AND project_tenant_id=? AND member_subject=? AND member_state='active' FOR UPDATE`, operation.ProjectID, tenant, operation.ActorSubject).Scan(&role)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lock project editor: %w", err)
		}
		if err == nil && role == "editor" {
			return nil
		}
	}
	return app.ErrProjectAccessChanged
}

var _ app.ProjectMutationRepository = (*MySQLProjectRepository)(nil)
