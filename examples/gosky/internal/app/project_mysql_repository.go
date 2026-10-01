package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/imajinyun/gofly/core/storage"
)

const projectSelectByIDSQL = `SELECT project_id, tenant_id, owner_subject, project_name, project_memo
FROM projects
WHERE project_id = ? AND project_state <> 'deleted'
LIMIT 1`

const projectActiveMemberSQL = `SELECT 1
FROM project_members
WHERE project_id = ? AND project_tenant_id = ? AND member_subject = ? AND member_state = 'active'
LIMIT 1`

const projectOperationInsertSQL = `INSERT INTO opslogs (
	operator_tenant_id,
	operator_subject,
	project_tenant_id,
	project_id,
	operation,
	outcome,
	auth_basis,
	reason,
	error_code,
	error_body,
	request_id,
	trace_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// MySQLProjectRepository is the production Project repository. It uses only
// fixed SQL with bound values and delegates connection lifecycle and query
// observability to gofly's SQLStore.
type MySQLProjectRepository struct {
	store *storage.SQLStore
}

func NewMySQLProjectRepository(store *storage.SQLStore) *MySQLProjectRepository {
	return &MySQLProjectRepository{store: store}
}

func (r *MySQLProjectRepository) GetProject(ctx context.Context, id uint64) (Project, error) {
	if r == nil || r.store == nil {
		return Project{}, errors.New("project sql store is nil")
	}
	var project Project
	err := r.store.QueryOne(ctx, projectSelectByIDSQL, func(row *sql.Row) error {
		return row.Scan(&project.ID, &project.TenantID, &project.OwnerSubject, &project.Name, &project.Description)
	}, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrProjectNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("query project by id: %w", err)
	}
	return project, nil
}

func (r *MySQLProjectRepository) IsActiveMember(ctx context.Context, projectID uint64, tenantID uint64, subject string) (bool, error) {
	if r == nil || r.store == nil {
		return false, errors.New("project sql store is nil")
	}
	var exists int
	err := r.store.QueryOne(ctx, projectActiveMemberSQL, func(row *sql.Row) error {
		return row.Scan(&exists)
	}, projectID, tenantID, subject)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query project member: %w", err)
	}
	return exists == 1, nil
}

func (r *MySQLProjectRepository) RecordProjectOperation(ctx context.Context, operation ProjectOperation) error {
	if r == nil || r.store == nil {
		return errors.New("project sql store is nil")
	}
	_, err := r.store.Exec(ctx, projectOperationInsertSQL,
		operation.ActorTenantID,
		operation.ActorSubject,
		operation.ProjectTenantID,
		operation.ProjectID,
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

var _ ProjectRepository = (*MySQLProjectRepository)(nil)
