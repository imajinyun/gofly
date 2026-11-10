package entity

import "time"

const OpslogTable = "opslogs"

const OpslogCacheKeyPrefix = "cache:opslogs"

var OpslogColumns = []string{"opslog_id", "operator_tenant_id", "operator_subject", "project_tenant_id", "project_id", "target_subject", "target_role", "operation", "outcome", "auth_basis", "reason", "error_code", "error_body", "request_id", "trace_id", "created_at"}

type Opslog struct {
	OpslogID         int64     `db:"opslog_id" json:"opslogId"`
	OperatorTenantID int64     `db:"operator_tenant_id" json:"operatorTenantId"`
	OperatorSubject  string    `db:"operator_subject" json:"operatorSubject"`
	ProjectTenantID  int64     `db:"project_tenant_id" json:"projectTenantId"`
	ProjectID        int64     `db:"project_id" json:"projectId"`
	TargetSubject    string    `db:"target_subject" json:"targetSubject"`
	TargetRole       string    `db:"target_role" json:"targetRole"`
	Operation        string    `db:"operation" json:"operation"`
	Outcome          string    `db:"outcome" json:"outcome"`
	AuthBasis        string    `db:"auth_basis" json:"authBasis"`
	Reason           string    `db:"reason" json:"reason"`
	ErrorCode        string    `db:"error_code" json:"errorCode"`
	ErrorBody        string    `db:"error_body" json:"errorBody"`
	RequestID        string    `db:"request_id" json:"requestId"`
	TraceID          string    `db:"trace_id" json:"traceId"`
	CreatedAt        time.Time `db:"created_at" json:"createdAt"`
}

var _ Tabler = (*Opslog)(nil)

func (Opslog) TableName() string { return OpslogTable }
