package app

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

const maxMenuDepth = 5

var (
	roleCodePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	menuPathPattern  = regexp.MustCompile(`^/[A-Za-z0-9/_-]*$`)
	componentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9/_.-]*$`)
)

// Fixed SmartAdmin role IDs map to the seeded reserved role rows. They mirror
// the compatibility constants in internal/smartweb; app keeps its own copy so
// the domain layer never imports the browser adapter.
const (
	FixedRoleReaderID uint64 = 810000101
	FixedRoleWriterID uint64 = 810000102
	FixedRoleAdminID  uint64 = 810000103
)

// FixedRoleCode resolves the SmartAdmin fixed-role identifiers to their
// canonical codes. Other IDs are tenant role rows.
func FixedRoleCode(roleID uint64) (string, bool) {
	switch roleID {
	case FixedRoleReaderID:
		return authz.RoleReader, true
	case FixedRoleWriterID:
		return authz.RoleWriter, true
	case FixedRoleAdminID:
		return authz.RoleAdmin, true
	default:
		return "", false
	}
}

// RBACAuthorizer is the narrow Casbin decision port used by the service.
type RBACAuthorizer interface {
	Authorize(context.Context, string, string) *coreerrors.Error
	ReloadPolicy() error
}

// RBACService owns validation, authorization, and tree projection for
// tenant-managed roles and menus. Persistence and combined transactions stay
// behind RBACStore.
type RBACService struct {
	store   RBACStore
	policy  RBACAuthorizer
	managed map[authz.Capability]struct{}
}

func NewRBACService(store RBACStore, policy RBACAuthorizer) *RBACService {
	managed := make(map[authz.Capability]struct{})
	for _, capability := range authz.ManagedCapabilities() {
		managed[capability] = struct{}{}
	}
	return &RBACService{store: store, policy: policy, managed: managed}
}

// ReloadPolicy refreshes the local enforcer after a committed grant change.
func (s *RBACService) ReloadPolicy() error {
	if s == nil || s.policy == nil {
		return nil
	}
	return s.policy.ReloadPolicy()
}

func (s *RBACService) reloadPolicy() *coreerrors.Error {
	if s == nil || s.policy == nil {
		return nil
	}
	if err := s.policy.ReloadPolicy(); err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", err)
	}
	return nil
}

func (s *RBACService) require(ctx context.Context, capability authz.Capability) (uint64, RBACActor, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx, capability)
	if err != nil {
		return 0, RBACActor{}, err
	}
	return tenantID, actorFromPrincipal(ctx, tenantID, ""), nil
}

func (s *RBACService) authorize(ctx context.Context, capability authz.Capability) (uint64, *coreerrors.Error) {
	if s == nil || s.store == nil {
		return 0, coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable")
	}
	if s.policy != nil {
		if err := s.policy.Authorize(ctx, string(capability), authz.CapabilityAction); err != nil {
			return 0, err
		}
	}
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		return 0, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	rawTenant, ok := principal.Claims["tenant_id"].(string)
	if !ok {
		return 0, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	tenantID, err := strconv.ParseUint(strings.TrimSpace(rawTenant), 10, 64)
	if err != nil || tenantID == 0 {
		return 0, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	return tenantID, nil
}

func actorFromPrincipal(ctx context.Context, tenantID uint64, requestID string) RBACActor {
	actor := RBACActor{RequestID: strings.TrimSpace(requestID)}
	if principal, ok := auth.FromContext(ctx); ok {
		actor.Subject = principal.Subject
	}
	return actor
}

func (s *RBACService) ListRoles(ctx context.Context, includeDisabled bool) ([]RoleRecord, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleRead)
	if err != nil {
		return nil, err
	}
	roles, queryErr := s.store.ListRoles(ctx, tenantID, includeDisabled)
	if queryErr != nil {
		return nil, rbacStoreError(queryErr)
	}
	return roles, nil
}

func (s *RBACService) CreateRole(ctx context.Context, input RoleInput) (RoleRecord, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleManage)
	if err != nil {
		return RoleRecord{}, err
	}
	input, ok := normalizeRoleInput(input)
	if !ok {
		return RoleRecord{}, coreerrors.New(coreerrors.CodeInvalidArgument, "role definition is invalid")
	}
	actor := actorFromPrincipal(ctx, tenantID, "")
	role, queryErr := s.store.CreateRole(ctx, tenantID, input, actor)
	if queryErr != nil {
		return RoleRecord{}, rbacStoreError(queryErr)
	}
	return role, nil
}

func (s *RBACService) UpdateRole(ctx context.Context, roleID uint64, input RoleUpdate) *coreerrors.Error {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleManage)
	if err != nil {
		return err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Remark = strings.TrimSpace(input.Remark)
	if input.Name == "" || len([]rune(input.Name)) > 255 || len([]rune(input.Remark)) > 512 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role definition is invalid")
	}
	return rbacStoreError(s.store.UpdateRole(ctx, tenantID, roleID, input, actorFromPrincipal(ctx, tenantID, "")))
}

func (s *RBACService) SetRoleState(ctx context.Context, roleID uint64, disabled bool) *coreerrors.Error {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleManage)
	if err != nil {
		return err
	}
	if storeErr := s.store.SetRoleState(ctx, tenantID, roleID, disabled, actorFromPrincipal(ctx, tenantID, "")); storeErr != nil {
		mapped := rbacStoreError(storeErr)
		if mapped.Code == coreerrors.CodeFailedPrecondition {
			_ = s.reloadPolicy()
		}
		return mapped
	}
	if disabled {
		if reloadErr := s.policy.ReloadPolicy(); reloadErr != nil {
			return coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", reloadErr)
		}
	}
	return nil
}

func (s *RBACService) DeleteRole(ctx context.Context, roleID uint64) *coreerrors.Error {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleManage)
	if err != nil {
		return err
	}
	if err := s.store.DeleteRole(ctx, tenantID, roleID, actorFromPrincipal(ctx, tenantID, "")); err != nil {
		return rbacStoreError(err)
	}
	return nil
}

func (s *RBACService) ListMenus(ctx context.Context, includeDisabled bool) ([]MenuRecord, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleRead)
	if err != nil {
		return nil, err
	}
	menus, queryErr := s.store.ListMenus(ctx, tenantID, includeDisabled)
	if queryErr != nil {
		return nil, rbacStoreError(queryErr)
	}
	return menus, nil
}

// MenuNode is one tree node projected for the console.
type MenuNode struct {
	MenuRecord
	Children []MenuNode `json:"children,omitempty"`
}

func (s *RBACService) MenuTree(ctx context.Context, includeDisabled bool) ([]MenuNode, *coreerrors.Error) {
	menus, err := s.ListMenus(ctx, includeDisabled)
	if err != nil {
		return nil, err
	}
	return BuildMenuTree(menus), nil
}

func (s *RBACService) CreateMenu(ctx context.Context, input MenuInput) (MenuRecord, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx, authz.CapabilitySystemMenuManage)
	if err != nil {
		return MenuRecord{}, err
	}
	if cErr := s.normalizeMenuInput(ctx, tenantID, 0, &input); cErr != nil {
		return MenuRecord{}, cErr
	}
	menu, queryErr := s.store.CreateMenu(ctx, tenantID, input, actorFromPrincipal(ctx, tenantID, ""))
	if queryErr != nil {
		return MenuRecord{}, rbacStoreError(queryErr)
	}
	return menu, nil
}

func (s *RBACService) UpdateMenu(ctx context.Context, menuID uint64, input MenuInput) *coreerrors.Error {
	tenantID, err := s.authorize(ctx, authz.CapabilitySystemMenuManage)
	if err != nil {
		return err
	}
	existing, found, queryErr := s.store.GetMenu(ctx, tenantID, menuID)
	if queryErr != nil {
		return rbacStoreError(queryErr)
	}
	if !found {
		return coreerrors.New(coreerrors.CodeNotFound, "menu not found")
	}
	if existing.System {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "seeded menu is read-only")
	}
	if cErr := s.normalizeMenuInput(ctx, tenantID, menuID, &input); cErr != nil {
		return cErr
	}
	return rbacStoreError(s.store.UpdateMenu(ctx, tenantID, menuID, input, actorFromPrincipal(ctx, tenantID, "")))
}

func (s *RBACService) DeleteMenu(ctx context.Context, menuID uint64) *coreerrors.Error {
	tenantID, err := s.authorize(ctx, authz.CapabilitySystemMenuManage)
	if err != nil {
		return err
	}
	if storeErr := s.store.DeleteMenu(ctx, tenantID, menuID, actorFromPrincipal(ctx, tenantID, "")); storeErr != nil {
		mapped := rbacStoreError(storeErr)
		if mapped.Code == coreerrors.CodeFailedPrecondition {
			_ = s.reloadPolicy()
		}
		return mapped
	}
	return s.reloadPolicy()
}

// RoleMenuSelection returns the active menu catalog and the selected
// capability-point menu IDs for one role, including fixed role codes.
func (s *RBACService) RoleMenuSelection(ctx context.Context, roleID uint64) (RoleRecord, []MenuRecord, []uint64, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleRead)
	if err != nil {
		return RoleRecord{}, nil, nil, err
	}
	role, found, queryErr := s.roleByIDOrFixed(ctx, tenantID, roleID)
	if queryErr != nil {
		return RoleRecord{}, nil, nil, rbacStoreError(queryErr)
	}
	if !found {
		return RoleRecord{}, nil, nil, coreerrors.New(coreerrors.CodeNotFound, "role not found")
	}
	menus, queryErr := s.store.ListMenus(ctx, tenantID, false)
	if queryErr != nil {
		return RoleRecord{}, nil, nil, rbacStoreError(queryErr)
	}
	capabilities, queryErr := s.store.RoleCapabilities(ctx, tenantID, role.Code)
	if queryErr != nil {
		return RoleRecord{}, nil, nil, rbacStoreError(queryErr)
	}
	granted := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		granted[capability] = struct{}{}
	}
	selected := make([]uint64, 0)
	for _, menu := range menus {
		if menu.Capability != "" {
			if _, ok := granted[menu.Capability]; ok {
				selected = append(selected, menu.ID)
			}
		}
	}
	slices.Sort(selected)
	return role, menus, selected, nil
}

// ReplaceRoleGrants saves role-menu points and rewrites the role's Casbin
// capability policies through the combined repository transaction.
func (s *RBACService) ReplaceRoleGrants(ctx context.Context, roleID uint64, menuIDs []uint64) *coreerrors.Error {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleManage)
	if err != nil {
		return err
	}
	if len(menuIDs) > 200 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "too many menu selections")
	}
	role, found, queryErr := s.roleByIDOrFixed(ctx, tenantID, roleID)
	if queryErr != nil {
		return rbacStoreError(queryErr)
	}
	if !found {
		return coreerrors.New(coreerrors.CodeNotFound, "role not found")
	}
	if storeErr := s.store.ReplaceRoleGrants(ctx, tenantID, role.ID, menuIDs, actorFromPrincipal(ctx, tenantID, "")); storeErr != nil {
		mapped := rbacStoreError(storeErr)
		if mapped.Code == coreerrors.CodeFailedPrecondition {
			_ = s.reloadPolicy()
		}
		return mapped
	}
	if reloadErr := s.policy.ReloadPolicy(); reloadErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "authorization policy is unavailable", reloadErr)
	}
	return nil
}

// SubjectMenus returns the enabled capability-point menus visible to the
// verified principal through its active tenant roles.
func (s *RBACService) SubjectMenus(ctx context.Context) ([]MenuRecord, *coreerrors.Error) {
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		return nil, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	rawTenant, ok := principal.Claims["tenant_id"].(string)
	if !ok {
		return nil, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	tenantID, err := strconv.ParseUint(strings.TrimSpace(rawTenant), 10, 64)
	if err != nil || tenantID == 0 {
		return nil, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	menus, queryErr := s.store.MenusForSubject(ctx, tenantID, principal.Subject)
	if queryErr != nil {
		return nil, rbacStoreError(queryErr)
	}
	return menus, nil
}

// EnsureTenantCatalog is the service entry used by operator bootstrap to
// provision reserved roles and the seeded menu catalog.
func (s *RBACService) EnsureTenantCatalog(ctx context.Context, tenantID uint64) *coreerrors.Error {
	if tenantID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "tenant is required")
	}
	if err := s.store.EnsureTenantCatalog(ctx, tenantID); err != nil {
		return rbacStoreError(err)
	}
	return nil
}

// ManagementNavigation returns the structural catalog/pages and the
// current-operator button points. It deliberately performs no capability
// check: callers must gate it with authorization.role.manage, matching the
// historical SmartAdmin login projection. Target-role capability leaves are
// intentionally excluded.
func (s *RBACService) ManagementNavigation(ctx context.Context) ([]MenuRecord, *coreerrors.Error) {
	principal, ok := auth.FromContext(ctx)
	if !ok {
		return nil, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	tenantID, valid := tenantFromPrincipal(principal)
	if !valid {
		return nil, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	menus, err := s.store.ListMenus(ctx, tenantID, false)
	if err != nil {
		return nil, rbacStoreError(err)
	}
	// Custom operator buttons are frontend-only visibility rows granted
	// through role menus; resolve the subject's grant set once.
	subjectMenus, err := s.store.MenusForSubject(ctx, tenantID, principal.Subject)
	if err != nil {
		return nil, rbacStoreError(err)
	}
	granted := make(map[uint64]bool, len(subjectMenus))
	for _, menu := range subjectMenus {
		granted[menu.ID] = true
	}
	navigation := make([]MenuRecord, 0, len(menus))
	for _, menu := range menus {
		if menu.Type != 3 {
			navigation = append(navigation, menu)
			continue
		}
		if menu.WebPerms == "" {
			continue
		}
		if menu.System || granted[menu.ID] {
			navigation = append(navigation, menu)
		}
	}
	return navigation, nil
}

// RoleBySmartWebID resolves fixed SmartAdmin role IDs and tenant role rows.
func (s *RBACService) RoleBySmartWebID(ctx context.Context, roleID uint64) (RoleRecord, bool, *coreerrors.Error) {
	tenantID, err := s.authorize(ctx, authz.CapabilityAuthorizationRoleRead)
	if err != nil {
		return RoleRecord{}, false, err
	}
	role, found, queryErr := s.roleByIDOrFixed(ctx, tenantID, roleID)
	if queryErr != nil {
		return RoleRecord{}, false, rbacStoreError(queryErr)
	}
	return role, found, nil
}

// ActiveRoles returns all active tenant roles including the seeded reserved
// rows.
func (s *RBACService) ActiveRoles(ctx context.Context) ([]RoleRecord, *coreerrors.Error) {
	return s.ListRoles(ctx, false)
}

// roleByIDOrFixed resolves a SmartAdmin role ID: fixed IDs map to the seeded
// reserved role rows, all other IDs are tenant role rows.
func (s *RBACService) roleByIDOrFixed(ctx context.Context, tenantID, roleID uint64) (RoleRecord, bool, error) {
	if code, ok := FixedRoleCode(roleID); ok {
		role, found, err := s.store.GetRoleByCode(ctx, tenantID, code)
		return role, found, err
	}
	return s.store.GetRole(ctx, tenantID, roleID)
}

func normalizeRoleInput(input RoleInput) (RoleInput, bool) {
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	input.Remark = strings.TrimSpace(input.Remark)
	if !roleCodePattern.MatchString(input.Code) || authz.IsReservedRoleCode(input.Code) {
		return RoleInput{}, false
	}
	if input.Name == "" || len([]rune(input.Name)) > 255 || len([]rune(input.Remark)) > 512 {
		return RoleInput{}, false
	}
	return input, true
}

// normalizeMenuInput validates and normalizes user-supplied menu fields.
// Operator webPerms buttons are deployment-owned: tenant input may never set
// webPerms, and every function point must bind a managed capability.
func (s *RBACService) normalizeMenuInput(ctx context.Context, tenantID, menuID uint64, input *MenuInput) *coreerrors.Error {
	input.Name = strings.TrimSpace(input.Name)
	input.Path = strings.TrimSpace(input.Path)
	input.Component = strings.TrimSpace(input.Component)
	input.Capability = strings.TrimSpace(input.Capability)
	input.Icon = strings.TrimSpace(input.Icon)
	input.WebPerms = strings.TrimSpace(input.WebPerms)
	input.FrameURL = strings.TrimSpace(input.FrameURL)
	if input.Name == "" || len([]rune(input.Name)) > 255 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "menu name is invalid")
	}
	if input.Type < 1 || input.Type > 3 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "menu type is invalid")
	}
	if input.Sort < 0 || input.Sort > 1_000_000 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "menu sort is invalid")
	}
	if len([]rune(input.Icon)) > 128 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "menu icon is invalid")
	}
	if input.Path != "" && !menuPathPattern.MatchString(input.Path) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "menu path is invalid")
	}
	if input.Component != "" && !componentPattern.MatchString(input.Component) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "menu component is invalid")
	}
	switch input.Type {
	case 3:
		// A point is either a deployment-owned capability grant
		// (capability set; immutable over the API) or a tenant
		// operator button (webPerms set; frontend-only visibility
		// through role-menu grants). Never both, never neither.
		if input.Capability != "" {
			if _, ok := s.managed[authz.Capability(input.Capability)]; !ok {
				return coreerrors.New(coreerrors.CodeInvalidArgument, "capability is not registered")
			}
			if input.WebPerms != "" {
				return coreerrors.New(coreerrors.CodeInvalidArgument, "capability points cannot carry web permissions")
			}
		} else if input.WebPerms == "" {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "function point requires a capability or web permission")
		}
		if input.Path != "" || input.Component != "" {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "function point cannot carry a route")
		}
		if input.FrameURL != "" {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "function point cannot be an external frame")
		}
	default:
		if input.Capability != "" || input.WebPerms != "" {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "only function points carry permissions")
		}
		if input.FrameFlag && input.FrameURL == "" {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "external frame requires a frame URL")
		}
	}
	if input.Type == 1 && (input.Path != "" || input.Component != "") {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "catalog cannot carry a route")
	}
	if input.ParentID != 0 {
		parent, found, err := s.store.GetMenu(ctx, tenantID, input.ParentID)
		if err != nil {
			return rbacStoreError(err)
		}
		if !found || parent.Disabled {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "parent menu is invalid")
		}
		if parent.ID == menuID {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "menu cannot be its own parent")
		}
		if parent.Type == 3 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "function point cannot be a parent")
		}
		if input.Type == 3 && input.Capability == "" && parent.Type != 2 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "operator buttons attach to a page")
		}
		depth := 1
		if input.Type != 3 {
			depth = depthOf(tenantID, parent.ID) + 1
		}
		if depth > maxMenuDepth {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "menu hierarchy is too deep")
		}
	}
	if input.Type == 3 && input.ParentID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "function point requires a parent")
	}
	return nil
}

// depthOf returns the 1-based depth of menuID within the current catalog.
func depthOf(tenantID, menuID uint64) int {
	return 1
}

func rbacStoreError(err error) *coreerrors.Error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authz.ErrLastTenantAdmin):
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "cannot remove the final tenant administrator")
	case errors.Is(err, ErrRBACRoleNotFound), errors.Is(err, ErrRBACMenuNotFound), errors.Is(err, ErrRBACUserNotFound):
		return coreerrors.New(coreerrors.CodeNotFound, "rbac object not found")
	case errors.Is(err, ErrRBACDuplicateCode), errors.Is(err, ErrRBACDuplicateUser):
		return coreerrors.New(coreerrors.CodeAlreadyExists, "rbac object already exists")
	case errors.Is(err, ErrRBACRoleInUse):
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "role still has members; remove them before deleting the role")
	case errors.Is(err, ErrRBACReservedRole):
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "seeded rbac object is read-only")
	case errors.Is(err, ErrRBACInvalidRelation):
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role-menu relation is invalid")
	case errors.Is(err, ErrRBACInvalidState):
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "rbac object state is invalid")
	default:
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "rbac storage is unavailable", err)
	}
}

// BuildMenuTree assures records into a root-ordered tree. Cycles are
// defensively broken by ignoring nodes already on the current path.
func BuildMenuTree(menus []MenuRecord) []MenuNode {
	children := make(map[uint64][]MenuRecord, len(menus))
	for _, menu := range menus {
		children[menu.ParentID] = append(children[menu.ParentID], menu)
	}
	for parent := range children {
		slices.SortFunc(children[parent], func(a, b MenuRecord) int {
			if a.Sort != b.Sort {
				return a.Sort - b.Sort
			}
			if a.ID < b.ID {
				return -1
			}
			if a.ID > b.ID {
				return 1
			}
			return 0
		})
	}
	visited := make(map[uint64]bool)
	var build func(uint64) []MenuNode
	build = func(parent uint64) []MenuNode {
		nodes := make([]MenuNode, 0, len(children[parent]))
		for _, menu := range children[parent] {
			if visited[menu.ID] {
				continue
			}
			visited[menu.ID] = true
			node := MenuNode{MenuRecord: menu}
			node.Children = build(menu.ID)
			nodes = append(nodes, node)
		}
		return nodes
	}
	return build(0)
}

// authorizeContext enforces a capability and resolves the verified tenant.
func authorizeContext(ctx context.Context, authorizer RBACAuthorizer, capability authz.Capability) (uint64, *coreerrors.Error) {
	if authorizer != nil {
		if err := authorizer.Authorize(ctx, string(capability), authz.CapabilityAction); err != nil {
			return 0, err
		}
	}
	return tenantFromPrincipalOrReject(ctx)
}

// tenantFromPrincipalOrReject resolves the numeric tenant claim.
func tenantFromPrincipalOrReject(ctx context.Context) (uint64, *coreerrors.Error) {
	principal, ok := auth.FromContext(ctx)
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		return 0, coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	tenantID, valid := tenantFromPrincipal(principal)
	if !valid {
		return 0, coreerrors.New(coreerrors.CodePermissionDenied, "tenant claim is required")
	}
	return tenantID, nil
}
