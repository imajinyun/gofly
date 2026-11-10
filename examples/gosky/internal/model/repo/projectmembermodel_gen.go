package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type ProjectMember = entity.ProjectMember

type projectMemberModel interface {
	FindOne(ctx context.Context, projectMemberID int64) (*ProjectMember, error)
	Insert(ctx context.Context, in *ProjectMember) (sql.Result, error)
	Update(ctx context.Context, in *ProjectMember) error
	Delete(ctx context.Context, projectMemberID int64) error
}

type defaultProjectMemberModel struct {
	repo *ProjectMemberRepo
}

func newProjectMemberModel(conn *storage.SQLStore, dialect ...storage.Dialect) *defaultProjectMemberModel {
	return &defaultProjectMemberModel{repo: NewProjectMemberRepo(conn, dialect...)}
}

func (m *defaultProjectMemberModel) FindOne(ctx context.Context, projectMemberID int64) (*ProjectMember, error) {
	return m.repo.FindOne(ctx, projectMemberID)
}

func (m *defaultProjectMemberModel) Insert(ctx context.Context, in *ProjectMember) (sql.Result, error) {
	return m.repo.InsertResult(ctx, in)
}

func (m *defaultProjectMemberModel) Update(ctx context.Context, in *ProjectMember) error {
	return m.repo.Update(ctx, in)
}

func (m *defaultProjectMemberModel) Delete(ctx context.Context, projectMemberID int64) error {
	return m.repo.Delete(ctx, projectMemberID)
}

func (m *defaultProjectMemberModel) withSession(session *sql.Tx) ProjectMemberModel {
	if m == nil || m.repo == nil {
		return m
	}
	clone := *m
	clone.repo = m.repo.WithTx(session)
	return &clone
}
