package repo

import (
	"context"
	"database/sql"

	"github.com/imajinyun/gofly/core/storage"
	entity "github.com/imajinyun/gofly/examples/gosky/internal/model/entity"
)

type Tenant = entity.Tenant

type tenantModel interface {
	FindOne(ctx context.Context, tenantID int64) (*Tenant, error)
	Insert(ctx context.Context, in *Tenant) (sql.Result, error)
	Update(ctx context.Context, in *Tenant) error
	Delete(ctx context.Context, tenantID int64) error
}

type defaultTenantModel struct {
	repo *TenantRepo
}

func newTenantModel(conn *storage.SQLStore, dialect ...storage.Dialect) *defaultTenantModel {
	return &defaultTenantModel{repo: NewTenantRepo(conn, dialect...)}
}

func (m *defaultTenantModel) FindOne(ctx context.Context, tenantID int64) (*Tenant, error) {
	return m.repo.FindOne(ctx, tenantID)
}

func (m *defaultTenantModel) Insert(ctx context.Context, in *Tenant) (sql.Result, error) {
	return m.repo.InsertResult(ctx, in)
}

func (m *defaultTenantModel) Update(ctx context.Context, in *Tenant) error {
	return m.repo.Update(ctx, in)
}

func (m *defaultTenantModel) Delete(ctx context.Context, tenantID int64) error {
	return m.repo.Delete(ctx, tenantID)
}

func (m *defaultTenantModel) withSession(session *sql.Tx) TenantModel {
	if m == nil || m.repo == nil {
		return m
	}
	clone := *m
	clone.repo = m.repo.WithTx(session)
	return &clone
}
