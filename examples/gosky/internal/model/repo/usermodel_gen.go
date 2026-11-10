package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type User = entity.User

type userModel interface {
	FindOne(ctx context.Context, userID int64) (*User, error)
	Insert(ctx context.Context, in *User) (sql.Result, error)
	Update(ctx context.Context, in *User) error
	Delete(ctx context.Context, userID int64) error
}

type defaultUserModel struct {
	repo *UserRepo
}

func newUserModel(conn *storage.SQLStore, dialect ...storage.Dialect) *defaultUserModel {
	return &defaultUserModel{repo: NewUserRepo(conn, dialect...)}
}

func (m *defaultUserModel) FindOne(ctx context.Context, userID int64) (*User, error) {
	return m.repo.FindOne(ctx, userID)
}

func (m *defaultUserModel) Insert(ctx context.Context, in *User) (sql.Result, error) {
	return m.repo.InsertResult(ctx, in)
}

func (m *defaultUserModel) Update(ctx context.Context, in *User) error {
	return m.repo.Update(ctx, in)
}

func (m *defaultUserModel) Delete(ctx context.Context, userID int64) error {
	return m.repo.Delete(ctx, userID)
}

func (m *defaultUserModel) withSession(session *sql.Tx) UserModel {
	if m == nil || m.repo == nil {
		return m
	}
	clone := *m
	clone.repo = m.repo.WithTx(session)
	return &clone
}
