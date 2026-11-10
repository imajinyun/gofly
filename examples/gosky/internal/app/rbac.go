// Dynamic RBAC domain types and repository ports. Application services own
// validation and authorization; implementations in internal/model/repo own
// SQL and the combined role_menus/Casbin transaction.
package app

import (
	"context"
	"errors"
	"time"
)

// RBAC persistence sentinels let services map repository outcomes to stable
// API error codes without leaking SQL errors to handlers.
var (
	ErrRBACRoleNotFound    = errors.New("rbac role not found")
	ErrRBACMenuNotFound    = errors.New("rbac menu not found")
	ErrRBACRoleInUse       = errors.New("rbac role still has subject bindings")
	ErrRBACReservedRole    = errors.New("rbac reserved role cannot be modified here")
	ErrRBACDuplicateCode   = errors.New("rbac role code already exists")
	ErrRBACDuplicateUser   = errors.New("rbac user already exists")
	ErrRBACUserNotFound    = errors.New("rbac user not found")
	ErrRBACInvalidRelation = errors.New("rbac role-menu relation is invalid")
	ErrRBACInvalidState    = errors.New("rbac invalid state")
	// ErrOldPasswordMismatch is returned by self-service password change when
	// the supplied current password does not match the stored hash.
	ErrOldPasswordMismatch = errors.New("old password does not match")
)

// RBACActor is the verified identity and correlation data recorded with an
// RBAC mutation. It never carries credentials or request bodies.
type RBACActor struct {
	Subject   string
	RequestID string
	TraceID   string
}

// RoleRecord is the credential-free projection of one tenant role row.
type RoleRecord struct {
	ID        uint64
	TenantID  uint64
	Code      string
	Name      string
	Remark    string
	State     string
	System    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MenuRecord is the tenant-scoped menu/catalog/function-point row projected
// for console navigation and role editing.
type MenuRecord struct {
	ID         uint64
	ParentID   uint64
	Name       string
	Type       int
	Sort       int
	Path       string
	FrameURL   string
	Component  string
	WebPerms   string
	Capability string
	Icon       string
	Visible    bool
	Disabled   bool
	FrameFlag  bool
	CacheFlag  bool
	System     bool
}

// RoleInput carries administrator-supplied role definition fields. The role
// code is immutable after creation and reserved codes are rejected.
type RoleInput struct {
	Code   string
	Name   string
	Remark string
}

// RoleUpdate carries the mutable role metadata.
type RoleUpdate struct {
	Name   string
	Remark string
}

// MenuInput carries administrator-supplied menu fields.
type MenuInput struct {
	ParentID   uint64
	Name       string
	Type       int
	Sort       int
	Path       string
	FrameURL   string
	Component  string
	WebPerms   string
	Capability string
	Icon       string
	Visible    bool
	Disabled   bool
	FrameFlag  bool
	CacheFlag  bool
}

// UserRecord is the credential-free user administration projection.
type UserRecord struct {
	ID          uint64
	Subject     string
	LoginName   string
	DisplayName string
	AccountType string
	UserState   string
	MemberRealm string
	MemberState string
	CreatedAt   time.Time
}

// UserAdminPage is one bounded page of tenant users.
type UserAdminPage struct {
	Users []UserRecord
	Total int
}

// CreateUserInput carries the validated new-user request. The initial password
// is hashed by the service before reaching a repository and is never stored.
type CreateUserInput struct {
	LoginName   string
	DisplayName string
	Password    string
	// MemberRealm is admin, front, or both for the tenant_users membership.
	MemberRealm string
}

// RBACStore is the persistence port for dynamic roles, menus, and role-menu
// grants. All tenant and role selection comes from method arguments derived
// from the verified principal; implementations bind every value as a SQL
// parameter.
type RBACStore interface {
	// ActiveRoleCodes backs authz.RoleDirectory: active custom role codes in
	// the numeric domain, excluding the reserved reader/writer/admin codes.
	ActiveRoleCodes(ctx context.Context, domain string) (map[string]struct{}, error)

	ListRoles(ctx context.Context, tenantID uint64, includeDisabled bool) ([]RoleRecord, error)
	GetRole(ctx context.Context, tenantID, roleID uint64) (RoleRecord, bool, error)
	GetRoleByCode(ctx context.Context, tenantID uint64, code string) (RoleRecord, bool, error)
	CreateRole(ctx context.Context, tenantID uint64, input RoleInput, actor RBACActor) (RoleRecord, error)
	UpdateRole(ctx context.Context, tenantID, roleID uint64, input RoleUpdate, actor RBACActor) error
	SetRoleState(ctx context.Context, tenantID, roleID uint64, disabled bool, actor RBACActor) error
	// DeleteRole rejects reserved roles and roles that still have subject
	// bindings or the last management grant; it removes role_menus and the
	// role's Casbin rules atomically.
	DeleteRole(ctx context.Context, tenantID, roleID uint64, actor RBACActor) error

	ListMenus(ctx context.Context, tenantID uint64, includeDisabled bool) ([]MenuRecord, error)
	GetMenu(ctx context.Context, tenantID, menuID uint64) (MenuRecord, bool, error)
	CreateMenu(ctx context.Context, tenantID uint64, input MenuInput, actor RBACActor) (MenuRecord, error)
	UpdateMenu(ctx context.Context, tenantID, menuID uint64, input MenuInput, actor RBACActor) error
	DeleteMenu(ctx context.Context, tenantID, menuID uint64, actor RBACActor) error

	// RoleCapabilities returns the direct managed capabilities granted to the
	// role code, including custom roles.
	RoleCapabilities(ctx context.Context, tenantID uint64, roleCode string) ([]string, error)
	// ReplaceRoleGrants rewrites role_menus and the role's Casbin capability
	// policies in one transaction, then enforces the administrator invariant.
	ReplaceRoleGrants(ctx context.Context, tenantID, roleID uint64, menuIDs []uint64, actor RBACActor) error

	// MenusForSubject returns the currently enabled function-point menus
	// reachable through the subject's active roles in the tenant. Structural
	// ancestors are resolved by the service.
	MenusForSubject(ctx context.Context, tenantID uint64, subject string) ([]MenuRecord, error)

	// EnsureTenantCatalog idempotently provisions the reserved role rows and
	// deployment-owned menu catalog for one tenant.
	EnsureTenantCatalog(ctx context.Context, tenantID uint64) error

	// EffectiveManagers returns subjects retaining the complete management
	// capability set, used by the identity-disable last-administrator guard.
	EffectiveManagers(ctx context.Context, tenantID uint64) ([]string, error)
}

// UserAdminStore is the persistence port for tenant user administration.
type UserAdminStore interface {
	ListUsers(ctx context.Context, tenantID uint64, pageNum, pageSize uint64, keywords string) (UserAdminPage, error)
	GetUser(ctx context.Context, tenantID, userID uint64) (UserRecord, bool, error)
	// CreateUser inserts users and tenant_users in one transaction. The
	// password is already Argon2id-hashed.
	CreateUser(ctx context.Context, tenantID uint64, input CreateUserInput, subject, passwordHash string, actor RBACActor) (UserRecord, error)
	// SetMembershipState enables or disables the tenant membership and, on
	// disable, revokes that scope's refresh sessions in the same transaction.
	SetMembershipState(ctx context.Context, tenantID, userID uint64, disabled bool, actor RBACActor) error
	// ResetPassword replaces the hash and revokes the tenant's sessions for
	// the user atomically.
	ResetPassword(ctx context.Context, tenantID, userID uint64, passwordHash string, actor RBACActor) error
}

// ErrSerialMisconfigured indicates a serial number definition cannot be
// used (missing or invalid step range/format).
var ErrSerialMisconfigured = errors.New("serial number is misconfigured")
