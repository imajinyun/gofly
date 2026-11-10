package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type Project = entity.Project

type projectModel interface {
	FindOne(ctx context.Context, projectID int64) (*Project, error)
	Insert(ctx context.Context, in *Project) (sql.Result, error)
	Update(ctx context.Context, in *Project) error
	Delete(ctx context.Context, projectID int64) error
}

type defaultProjectModel struct {
	repo *ProjectRepo
}

func newProjectModel(conn *storage.SQLStore, dialect ...storage.Dialect) *defaultProjectModel {
	return &defaultProjectModel{repo: NewProjectRepo(conn, dialect...)}
}

func (m *defaultProjectModel) FindOne(ctx context.Context, projectID int64) (*Project, error) {
	return m.repo.FindOne(ctx, projectID)
}

func (m *defaultProjectModel) Insert(ctx context.Context, in *Project) (sql.Result, error) {
	return m.repo.InsertResult(ctx, in)
}

func (m *defaultProjectModel) Update(ctx context.Context, in *Project) error {
	return m.repo.Update(ctx, in)
}

func (m *defaultProjectModel) Delete(ctx context.Context, projectID int64) error {
	return m.repo.Delete(ctx, projectID)
}

func (m *defaultProjectModel) withSession(session *sql.Tx) ProjectModel {
	if m == nil || m.repo == nil {
		return m
	}
	clone := *m
	clone.repo = m.repo.WithTx(session)
	return &clone
}
