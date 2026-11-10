package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// TenantUser is a credential-free membership projection owned by the role
// use cases. Repositories supply it; browser adapters choose their own DTOs.
type TenantUser struct {
	UserID         uint64
	Subject        string
	LoginName      string
	DisplayName    string
	Phone          string
	DepartmentName string
	CreatedAt      time.Time
}

type RoleMemberPage struct {
	Users []TenantUser
	Total int
}

// TenantUserDirectory exposes only active, same-tenant identity lookups.
// Implementations bind tenant and lookup values as SQL parameters.
type TenantUserDirectory interface {
	ListActiveTenantUsersByIDs(context.Context, uint64, []uint64) ([]TenantUser, error)
	ListActiveTenantUsersBySubjects(context.Context, uint64, []string) ([]TenantUser, error)
	ListActiveTenantUsers(context.Context, uint64, string) ([]TenantUser, error)
}

type RoleProjectScope string

const (
	RoleProjectScopeRelated   RoleProjectScope = "related"
	RoleProjectScopeAllTenant RoleProjectScope = "all_tenant"
)

// RoleCodeDirectory reports active tenant-managed role codes. It is the
// application-side mirror of authz.RoleDirectory, satisfied by the RBAC
// repository.
type RoleCodeDirectory interface {
	ActiveRoleCodes(ctx context.Context, domain string) (map[string]struct{}, error)
}

// RoleService coordinates canonical policy operations with the tenant user
// directory. It owns membership validation and Project scope changes, without
// depending on a browser format or a concrete SQL repository. PolicyService
// remains the sole audited policy writer shared with the native API.
type RoleService struct {
	policy *authz.PolicyService
	users  TenantUserDirectory
	roles  RoleCodeDirectory
}

func NewRoleService(policy *authz.PolicyService, users TenantUserDirectory, roleDirectories ...RoleCodeDirectory) *RoleService {
	service := &RoleService{policy: policy, users: users}
	if len(roleDirectories) > 0 && roleDirectories[0] != nil {
		service.roles = roleDirectories[0]
	}
	return service
}

// roleAssignable reports whether role is a reserved code or an active
// tenant-managed role. Without a dynamic directory only reserved roles exist.
func (s *RoleService) roleAssignable(ctx context.Context, tenantID uint64, role string) bool {
	if isTenantRole(role) {
		return true
	}
	if s == nil || s.roles == nil || tenantID == 0 {
		return false
	}
	codes, err := s.roles.ActiveRoleCodes(ctx, strconv.FormatUint(tenantID, 10))
	if err != nil {
		return false
	}
	_, ok := codes[strings.TrimSpace(role)]
	return ok
}

func (s *RoleService) RoleCapabilities(ctx context.Context, role string) ([]authz.Capability, *coreerrors.Error) {
	if s == nil || s.policy == nil {
		return nil, coreerrors.New(coreerrors.CodeUnavailable, "role policy management is unavailable")
	}
	return s.policy.RoleCapabilities(ctx, role)
}

func (s *RoleService) ReplaceRoleCapabilities(ctx context.Context, role string, capabilities []authz.Capability, requestID string) *coreerrors.Error {
	if s == nil || s.policy == nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "role policy management is unavailable")
	}
	return s.policy.ReplaceRoleCapabilities(ctx, role, capabilities, requestID)
}

// SetProjectScope preserves every non-Project capability on the role.
func (s *RoleService) SetProjectScope(ctx context.Context, role string, scope RoleProjectScope, requestID string) *coreerrors.Error {
	if s == nil || s.policy == nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "role policy management is unavailable")
	}
	if scope != RoleProjectScopeRelated && scope != RoleProjectScopeAllTenant {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role Project scope is invalid")
	}
	return s.policy.SetRoleProjectScope(ctx, role, scope == RoleProjectScopeAllTenant, requestID)
}

func (s *RoleService) ListRoleMembers(ctx context.Context, role string, pageNum, pageSize uint64, keywords string) (RoleMemberPage, *coreerrors.Error) {
	tenantID, err := s.authorizeDirectory(ctx)
	if err != nil {
		return RoleMemberPage{}, err
	}
	if !s.roleAssignable(ctx, tenantID, role) {
		return RoleMemberPage{}, coreerrors.New(coreerrors.CodeInvalidArgument, "unknown gosky role")
	}
	bindings, policyErr := s.policy.ListRoles(ctx)
	if policyErr != nil {
		return RoleMemberPage{}, policyErr
	}
	subjects := make([]string, 0)
	for _, binding := range bindings {
		if binding.Role == role {
			subjects = append(subjects, binding.Subject)
		}
	}
	if len(subjects) == 0 {
		return RoleMemberPage{Users: make([]TenantUser, 0)}, nil
	}
	users, queryErr := s.users.ListActiveTenantUsersBySubjects(ctx, tenantID, subjects)
	if queryErr != nil {
		return RoleMemberPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "role member directory is unavailable", queryErr)
	}
	keywords = strings.ToLower(strings.TrimSpace(keywords))
	filtered := make([]TenantUser, 0, len(users))
	for _, user := range users {
		if keywords != "" && !strings.Contains(strings.ToLower(user.LoginName), keywords) && !strings.Contains(strings.ToLower(user.DisplayName), keywords) {
			continue
		}
		filtered = append(filtered, user)
	}
	return roleMemberPage(filtered, pageNum, pageSize), nil
}

func (s *RoleService) ListTenantUsers(ctx context.Context, pageNum, pageSize uint64, keywords string) (RoleMemberPage, *coreerrors.Error) {
	tenantID, err := s.authorizeDirectory(ctx)
	if err != nil {
		return RoleMemberPage{}, err
	}
	users, queryErr := s.users.ListActiveTenantUsers(ctx, tenantID, strings.TrimSpace(keywords))
	if queryErr != nil {
		return RoleMemberPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "role member directory is unavailable", queryErr)
	}
	return roleMemberPage(users, pageNum, pageSize), nil
}

func (s *RoleService) AddRoleMembers(ctx context.Context, role string, userIDs []uint64, requestID string) *coreerrors.Error {
	return s.mutateMembers(ctx, role, userIDs, requestID, true)
}

func (s *RoleService) RemoveRoleMembers(ctx context.Context, role string, userIDs []uint64, requestID string) *coreerrors.Error {
	return s.mutateMembers(ctx, role, userIDs, requestID, false)
}

func (s *RoleService) mutateMembers(ctx context.Context, role string, userIDs []uint64, requestID string, add bool) *coreerrors.Error {
	if s == nil || s.policy == nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "role member management is unavailable")
	}
	capability := authz.CapabilityAuthorizationRoleMemberRemove
	if add {
		capability = authz.CapabilityAuthorizationRoleMemberAdd
	}
	if err := s.policy.AuthorizeCapability(ctx, capability); err != nil {
		return err
	}
	tenantID, err := s.authorizeDirectory(ctx)
	if err != nil {
		return err
	}
	if !s.roleAssignable(ctx, tenantID, role) || !validRoleMemberIDs(userIDs) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role member update is invalid")
	}
	users, queryErr := s.users.ListActiveTenantUsersByIDs(ctx, tenantID, userIDs)
	if queryErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "role member directory is unavailable", queryErr)
	}
	if len(users) != len(userIDs) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role member update contains an inactive or cross-tenant user")
	}
	subjects := make([]string, 0, len(users))
	for _, user := range users {
		subjects = append(subjects, user.Subject)
	}
	var policyErr *coreerrors.Error
	if add {
		_, policyErr = s.policy.GrantRoles(ctx, subjects, role, requestID)
	} else {
		_, policyErr = s.policy.RevokeRoles(ctx, subjects, role, requestID)
	}
	return policyErr
}

func (s *RoleService) authorizeDirectory(ctx context.Context) (uint64, *coreerrors.Error) {
	if s == nil || s.policy == nil || s.users == nil {
		return 0, coreerrors.New(coreerrors.CodeUnavailable, "role member management is unavailable")
	}
	if err := s.policy.AuthorizeManagement(ctx); err != nil {
		return 0, err
	}
	principal, ok := auth.FromContext(ctx)
	if !ok {
		return 0, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	rawTenantID, ok := principal.Claims["tenant_id"].(string)
	if !ok {
		return 0, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	tenantID, err := strconv.ParseUint(rawTenantID, 10, 64)
	if err != nil || tenantID == 0 {
		return 0, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	return tenantID, nil
}

func isTenantRole(role string) bool {
	return role == authz.RoleReader || role == authz.RoleWriter || role == authz.RoleAdmin
}

func validRoleMemberIDs(ids []uint64) bool {
	if len(ids) == 0 || len(ids) > 100 {
		return false
	}
	seen := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func roleMemberPage(users []TenantUser, pageNum, pageSize uint64) RoleMemberPage {
	total := len(users)
	page := Paginate(users, pageNum, pageSize)
	// Copy so the projection cannot alias the role member result slice.
	projected := make([]TenantUser, len(page))
	copy(projected, page)
	return RoleMemberPage{Users: projected, Total: total}
}
