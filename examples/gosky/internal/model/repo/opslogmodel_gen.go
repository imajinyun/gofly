package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type Opslog = entity.Opslog

type opslogModel interface {
	FindOne(ctx context.Context, opslogID int64) (*Opslog, error)
	Insert(ctx context.Context, in *Opslog) (sql.Result, error)
	Update(ctx context.Context, in *Opslog) error
	Delete(ctx context.Context, opslogID int64) error
}

type defaultOpslogModel struct {
	repo *OpslogRepo
}

func newOpslogModel(conn *storage.SQLStore, dialect ...storage.Dialect) *defaultOpslogModel {
	return &defaultOpslogModel{repo: NewOpslogRepo(conn, dialect...)}
}

func (m *defaultOpslogModel) FindOne(ctx context.Context, opslogID int64) (*Opslog, error) {
	return m.repo.FindOne(ctx, opslogID)
}

func (m *defaultOpslogModel) Insert(ctx context.Context, in *Opslog) (sql.Result, error) {
	return m.repo.InsertResult(ctx, in)
}

func (m *defaultOpslogModel) Update(ctx context.Context, in *Opslog) error {
	return m.repo.Update(ctx, in)
}

func (m *defaultOpslogModel) Delete(ctx context.Context, opslogID int64) error {
	return m.repo.Delete(ctx, opslogID)
}

func (m *defaultOpslogModel) withSession(session *sql.Tx) OpslogModel {
	if m == nil || m.repo == nil {
		return m
	}
	clone := *m
	clone.repo = m.repo.WithTx(session)
	return &clone
}
