// Tenant organization domain: departments, positions, and the employee
// directory. All types carry an explicit tenant ID derived from the
// verified JWT claim; clients cannot select another tenant.
package app

import (
	"context"
	"errors"
	"time"
)

var (
	ErrOrgNotFound        = errors.New("organization record not found")
	ErrOrgDuplicate       = errors.New("organization record already exists")
	ErrOrgReserved        = errors.New("seeded organization record is read-only")
	ErrOrgInvalidRelation = errors.New("organization relation is invalid")
	ErrOrgInvalidInput    = errors.New("organization input is invalid")
	ErrOrgHasMembers      = errors.New("organization record still has members")
	ErrOrgHasChildren     = errors.New("department has child departments")
)

// Department is one node of a tenant's department tree.
type Department struct {
	ID          uint64       `json:"departmentId"`
	TenantID    uint64       `json:"tenantId"`
	Name        string       `json:"departmentName"`
	ManagerID   uint64       `json:"managerId"`
	ParentID    uint64       `json:"parentId"`
	Sort        int          `json:"sort"`
	State       string       `json:"state"`
	System      bool         `json:"systemFlag"`
	CreatedAt   time.Time    `json:"createTime"`
	UpdatedAt   time.Time    `json:"updateTime"`
	ManagerName string       `json:"managerName,omitempty"`
	Children    []Department `json:"children,omitempty"`
}

// DepartmentInput carries create/update form fields.
type DepartmentInput struct {
	Name      string `json:"departmentName"`
	ManagerID uint64 `json:"managerId"`
	ParentID  uint64 `json:"parentId"`
	Sort      int    `json:"sort"`
}

// Position is a flat job-title entry within one tenant.
type Position struct {
	ID        uint64    `json:"positionId"`
	TenantID  uint64    `json:"tenantId"`
	Name      string    `json:"positionName"`
	Level     string    `json:"positionLevel"`
	Sort      int       `json:"sort"`
	Remark    string    `json:"remark"`
	State     string    `json:"state"`
	System    bool      `json:"systemFlag"`
	CreatedAt time.Time `json:"createTime"`
	UpdatedAt time.Time `json:"updateTime"`
}

// PositionInput carries create/update form fields.
type PositionInput struct {
	Name   string `json:"positionName"`
	Level  string `json:"positionLevel"`
	Sort   int    `json:"sort"`
	Remark string `json:"remark"`
}

// Employee is the directory projection of a tenant membership enriched
// with the global user profile and assigned role metadata.
type Employee struct {
	ID             uint64    `json:"employeeId"`
	UserID         uint64    `json:"userId"`
	Subject        string    `json:"-"`
	LoginName      string    `json:"loginName"`
	ActualName     string    `json:"actualName"`
	Avatar         string    `json:"avatar"`
	Gender         int       `json:"gender"`
	Phone          string    `json:"phone"`
	Email          string    `json:"email"`
	DepartmentID   uint64    `json:"departmentId"`
	DepartmentName string    `json:"departmentName,omitempty"`
	PositionID     uint64    `json:"positionId"`
	PositionName   string    `json:"positionName,omitempty"`
	Disabled       bool      `json:"disabledFlag"`
	Administrator  bool      `json:"administratorFlag"`
	Remark         string    `json:"remark"`
	RoleIDs        []uint64  `json:"roleIdList"`
	RoleNames      []string  `json:"roleNameList"`
	CreatedAt      time.Time `json:"createTime"`
}

// EmployeeInput carries add/update form fields. Password is set only on
// create; on update it must remain empty.
type EmployeeInput struct {
	EmployeeID   uint64   `json:"employeeId"`
	ActualName   string   `json:"actualName"`
	LoginName    string   `json:"loginName"`
	Password     string   `json:"password,omitempty"`
	Gender       int      `json:"gender"`
	Phone        string   `json:"phone"`
	Email        string   `json:"email"`
	DepartmentID uint64   `json:"departmentId"`
	PositionID   uint64   `json:"positionId"`
	Disabled     bool     `json:"disabledFlag"`
	Remark       string   `json:"remark"`
	RoleIDs      []uint64 `json:"roleIdList"`
}

// EmployeePage is a bounded employee directory page.
type EmployeePage struct {
	List     []Employee `json:"list"`
	Total    int64      `json:"total"`
	PageNum  uint64     `json:"pageNum"`
	PageSize uint64     `json:"pageSize"`
}

// OrgStore is the persistence port for organization management.
type OrgStore interface {
	CreateDepartment(context.Context, uint64, DepartmentInput, RBACActor) (Department, error)
	UpdateDepartment(context.Context, uint64, uint64, DepartmentInput, RBACActor) error
	DeleteDepartment(context.Context, uint64, uint64, RBACActor) error
	ListDepartments(context.Context, uint64) ([]Department, error)

	CreatePosition(context.Context, uint64, PositionInput, RBACActor) (Position, error)
	UpdatePosition(context.Context, uint64, uint64, PositionInput, RBACActor) error
	DeletePosition(context.Context, uint64, uint64, RBACActor) error
	ListPositions(context.Context, uint64, string) ([]Position, error)

	ListEmployees(context.Context, uint64, EmployeeQuery) (EmployeePage, error)
	ListAllEmployees(context.Context, uint64, bool) ([]Employee, error)
	EmployeesByDepartment(context.Context, uint64, uint64) ([]Employee, error)
	CreateEmployee(context.Context, uint64, EmployeeInput, string, RBACActor) (Employee, error)
	UpdateEmployee(context.Context, uint64, EmployeeInput, RBACActor) error
	SetEmployeeDisabled(context.Context, uint64, uint64, bool, RBACActor) error
	DeleteEmployees(context.Context, uint64, []uint64, RBACActor) error
	MoveEmployeesDepartment(context.Context, uint64, []uint64, uint64, RBACActor) error
	ResetEmployeePassword(context.Context, uint64, uint64, string, RBACActor) error
	UpdateEmployeeCenter(context.Context, uint64, uint64, EmployeeCenterInput, RBACActor) error
	UpdateEmployeeAvatar(context.Context, uint64, uint64, string, RBACActor) error
	SubjectRoles(context.Context, uint64, string) ([]string, error)
	EnsureTenantOrganization(context.Context, uint64) error
	EnsureAllTenants(context.Context) error
}

// EmployeeQuery filters and paginates the directory.
type EmployeeQuery struct {
	Keyword        string
	DepartmentID   uint64
	Disabled       *bool
	IncludeDeleted bool
	PageNum        uint64
	PageSize       uint64
}

// EmployeeCenterInput carries self-service profile edits.
type EmployeeCenterInput struct {
	ActualName string `json:"actualName"`
	Phone      string `json:"phone"`
	Email      string `json:"email"`
	Gender     int    `json:"gender"`
}
