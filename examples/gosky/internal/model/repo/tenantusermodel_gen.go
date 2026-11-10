package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type TenantUser = entity.TenantUser

type tenantUserModel interface {
	FindOne(ctx context.Context, tenantUserID int64) (*TenantUser, error)
	Insert(ctx context.Context, in *TenantUser) (sql.Result, error)
	Update(ctx context.Context, in *TenantUser) error
	Delete(ctx context.Context, tenantUserID int64) error
}

type defaultTenantUserModel struct {
	repo *TenantUserRepo
}

func newTenantUserModel(conn *storage.SQLStore, dialect ...storage.Dialect) *defaultTenantUserModel {
	return &defaultTenantUserModel{repo: NewTenantUserRepo(conn, dialect...)}
}

func (m *defaultTenantUserModel) FindOne(ctx context.Context, tenantUserID int64) (*TenantUser, error) {
	return m.repo.FindOne(ctx, tenantUserID)
}

func (m *defaultTenantUserModel) Insert(ctx context.Context, in *TenantUser) (sql.Result, error) {
	return m.repo.InsertResult(ctx, in)
}

func (m *defaultTenantUserModel) Update(ctx context.Context, in *TenantUser) error {
	return m.repo.Update(ctx, in)
}

func (m *defaultTenantUserModel) Delete(ctx context.Context, tenantUserID int64) error {
	return m.repo.Delete(ctx, tenantUserID)
}

func (m *defaultTenantUserModel) withSession(session *sql.Tx) TenantUserModel {
	if m == nil || m.repo == nil {
		return m
	}
	clone := *m
	clone.repo = m.repo.WithTx(session)
	return &clone
}
