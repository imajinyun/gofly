package entity

import "time"

const AuthLoginLogTable = "auth_login_logs"

const AuthLoginLogCacheKeyPrefix = "cache:auth_login_logs"

var AuthLoginLogColumns = []string{"auth_login_log_id", "user_id", "tenant_id", "realm", "login_name", "subject", "operation", "outcome", "reason_code", "request_id", "trace_id", "created_at"}

type AuthLoginLog struct {
	AuthLoginLogID int64     `db:"auth_login_log_id" json:"authLoginLogId"`
	UserID         *int64    `db:"user_id" json:"userId"`
	TenantID       *int64    `db:"tenant_id" json:"tenantId"`
	Realm          string    `db:"realm" json:"realm"`
	LoginName      string    `db:"login_name" json:"loginName"`
	Subject        string    `db:"subject" json:"subject"`
	Operation      string    `db:"operation" json:"operation"`
	Outcome        string    `db:"outcome" json:"outcome"`
	ReasonCode     string    `db:"reason_code" json:"reasonCode"`
	RequestID      string    `db:"request_id" json:"requestId"`
	TraceID        string    `db:"trace_id" json:"traceId"`
	CreatedAt      time.Time `db:"created_at" json:"createdAt"`
}

var _ Tabler = (*AuthLoginLog)(nil)

func (AuthLoginLog) TableName() string { return AuthLoginLogTable }
