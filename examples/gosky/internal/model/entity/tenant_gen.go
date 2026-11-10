package entity

import "time"

const TenantTable = "tenants"

const TenantCacheKeyPrefix = "cache:tenants"

var TenantColumns = []string{"tenant_id", "tenant_code", "tenant_name", "tenant_state", "created_at", "updated_at"}

type Tenant struct {
	TenantID    int64     `db:"tenant_id" json:"tenantId"`
	TenantCode  string    `db:"tenant_code" json:"tenantCode"`
	TenantName  string    `db:"tenant_name" json:"tenantName"`
	TenantState string    `db:"tenant_state" json:"tenantState"`
	CreatedAt   time.Time `db:"created_at" json:"createdAt"`
	UpdatedAt   time.Time `db:"updated_at" json:"updatedAt"`
}

var _ Tabler = (*Tenant)(nil)

func (Tenant) TableName() string { return TenantTable }
