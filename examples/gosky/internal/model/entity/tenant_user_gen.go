package entity

import "time"

const TenantUserTable = "tenant_users"

const TenantUserCacheKeyPrefix = "cache:tenant_users"

var TenantUserColumns = []string{"tenant_user_id", "tenant_id", "user_id", "member_realm", "member_state", "display_name", "joined_at", "left_at", "created_at", "updated_at"}

type TenantUser struct {
	TenantUserID int64      `db:"tenant_user_id" json:"tenantUserId"`
	TenantID     int64      `db:"tenant_id" json:"tenantId"`
	UserID       int64      `db:"user_id" json:"userId"`
	MemberRealm  string     `db:"member_realm" json:"memberRealm"`
	MemberState  string     `db:"member_state" json:"memberState"`
	DisplayName  string     `db:"display_name" json:"displayName"`
	JoinedAt     time.Time  `db:"joined_at" json:"joinedAt"`
	LeftAt       *time.Time `db:"left_at" json:"leftAt"`
	CreatedAt    time.Time  `db:"created_at" json:"createdAt"`
	UpdatedAt    time.Time  `db:"updated_at" json:"updatedAt"`
}

var _ Tabler = (*TenantUser)(nil)

func (TenantUser) TableName() string { return TenantUserTable }
