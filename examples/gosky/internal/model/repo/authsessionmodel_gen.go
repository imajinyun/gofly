package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type AuthSession = entity.AuthSession

type authSessionModel interface {
	FindOne(ctx context.Context, sessionID string) (*AuthSession, error)
	Insert(ctx context.Context, in *AuthSession) (sql.Result, error)
	Update(ctx context.Context, in *AuthSession) error
	Delete(ctx context.Context, sessionID string) error
}

type defaultAuthSessionModel struct {
	repo *AuthSessionRepo
}

func newAuthSessionModel(conn *storage.SQLStore, dialect ...storage.Dialect) *defaultAuthSessionModel {
	return &defaultAuthSessionModel{repo: NewAuthSessionRepo(conn, dialect...)}
}

func (m *defaultAuthSessionModel) FindOne(ctx context.Context, sessionID string) (*AuthSession, error) {
	return m.repo.FindOne(ctx, sessionID)
}

func (m *defaultAuthSessionModel) Insert(ctx context.Context, in *AuthSession) (sql.Result, error) {
	return m.repo.InsertResult(ctx, in)
}

func (m *defaultAuthSessionModel) Update(ctx context.Context, in *AuthSession) error {
	return m.repo.Update(ctx, in)
}

func (m *defaultAuthSessionModel) Delete(ctx context.Context, sessionID string) error {
	return m.repo.Delete(ctx, sessionID)
}

func (m *defaultAuthSessionModel) withSession(session *sql.Tx) AuthSessionModel {
	if m == nil || m.repo == nil {
		return m
	}
	clone := *m
	clone.repo = m.repo.WithTx(session)
	return &clone
}
