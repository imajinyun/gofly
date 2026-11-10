package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type AuthLoginLog = entity.AuthLoginLog

type authLoginLogModel interface {
	FindOne(ctx context.Context, authLoginLogID int64) (*AuthLoginLog, error)
	Insert(ctx context.Context, in *AuthLoginLog) (sql.Result, error)
	Update(ctx context.Context, in *AuthLoginLog) error
	Delete(ctx context.Context, authLoginLogID int64) error
}

type defaultAuthLoginLogModel struct {
	repo *AuthLoginLogRepo
}

func newAuthLoginLogModel(conn *storage.SQLStore, dialect ...storage.Dialect) *defaultAuthLoginLogModel {
	return &defaultAuthLoginLogModel{repo: NewAuthLoginLogRepo(conn, dialect...)}
}

func (m *defaultAuthLoginLogModel) FindOne(ctx context.Context, authLoginLogID int64) (*AuthLoginLog, error) {
	return m.repo.FindOne(ctx, authLoginLogID)
}

func (m *defaultAuthLoginLogModel) Insert(ctx context.Context, in *AuthLoginLog) (sql.Result, error) {
	return m.repo.InsertResult(ctx, in)
}

func (m *defaultAuthLoginLogModel) Update(ctx context.Context, in *AuthLoginLog) error {
	return m.repo.Update(ctx, in)
}

func (m *defaultAuthLoginLogModel) Delete(ctx context.Context, authLoginLogID int64) error {
	return m.repo.Delete(ctx, authLoginLogID)
}

func (m *defaultAuthLoginLogModel) withSession(session *sql.Tx) AuthLoginLogModel {
	if m == nil || m.repo == nil {
		return m
	}
	clone := *m
	clone.repo = m.repo.WithTx(session)
	return &clone
}
