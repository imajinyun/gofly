package entity

import "time"

const ProjectTable = "projects"

const ProjectCacheKeyPrefix = "cache:projects"

var ProjectColumns = []string{"project_id", "tenant_id", "owner_subject", "project_name", "project_memo", "project_state", "created_by", "updated_by", "deleted_at", "created_at", "updated_at", "active_project_name"}

type Project struct {
	ProjectID         int64      `db:"project_id" json:"projectId"`
	TenantID          int64      `db:"tenant_id" json:"tenantId"`
	OwnerSubject      string     `db:"owner_subject" json:"ownerSubject"`
	ProjectName       string     `db:"project_name" json:"projectName"`
	ProjectMemo       string     `db:"project_memo" json:"projectMemo"`
	ProjectState      string     `db:"project_state" json:"projectState"`
	CreatedBy         string     `db:"created_by" json:"createdBy"`
	UpdatedBy         string     `db:"updated_by" json:"updatedBy"`
	DeletedAt         *time.Time `db:"deleted_at" json:"deletedAt"`
	CreatedAt         time.Time  `db:"created_at" json:"createdAt"`
	UpdatedAt         time.Time  `db:"updated_at" json:"updatedAt"`
	ActiveProjectName *string    `db:"active_project_name" json:"activeProjectName"`
}

var _ Tabler = (*Project)(nil)

func (Project) TableName() string { return ProjectTable }
