package entity

import "time"

const ProjectMemberTable = "project_members"

const ProjectMemberCacheKeyPrefix = "cache:project_members"

var ProjectMemberColumns = []string{"project_member_id", "project_id", "project_tenant_id", "member_subject", "member_role", "member_state", "member_memo", "granted_by", "revoked_by", "revoked_at", "created_at", "updated_at"}

type ProjectMember struct {
	ProjectMemberID int64      `db:"project_member_id" json:"projectMemberId"`
	ProjectID       int64      `db:"project_id" json:"projectId"`
	ProjectTenantID int64      `db:"project_tenant_id" json:"projectTenantId"`
	MemberSubject   string     `db:"member_subject" json:"memberSubject"`
	MemberRole      string     `db:"member_role" json:"memberRole"`
	MemberState     string     `db:"member_state" json:"memberState"`
	MemberMemo      string     `db:"member_memo" json:"memberMemo"`
	GrantedBy       string     `db:"granted_by" json:"grantedBy"`
	RevokedBy       string     `db:"revoked_by" json:"revokedBy"`
	RevokedAt       *time.Time `db:"revoked_at" json:"revokedAt"`
	CreatedAt       time.Time  `db:"created_at" json:"createdAt"`
	UpdatedAt       time.Time  `db:"updated_at" json:"updatedAt"`
}

var _ Tabler = (*ProjectMember)(nil)

func (ProjectMember) TableName() string { return ProjectMemberTable }
