package smartweb

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

const (
	// SmartWebMenuTypeCatalog and SmartWebMenuTypePage are the numeric values
	// expected by the existing SmartAdmin menu renderer.
	SmartWebMenuTypeCatalog = 1
	SmartWebMenuTypePage    = 2

	// These identifiers are deployment-owned constants. They are intentionally
	// unrelated to tenant database IDs or Casbin policy rule IDs.
	SmartWebRoleCatalogMenuID     uint64 = 810000001
	SmartWebRolePageMenuID        uint64 = 810000002
	SmartWebMenuCatalogPageMenuID uint64 = 810000003
	SmartWebOrgCatalogMenuID      uint64 = 810000004
	SmartWebEmployeePageMenuID    uint64 = 810000005
	SmartWebDepartmentPageMenuID  uint64 = 810000006
	SmartWebPositionPageMenuID    uint64 = 810000007
	SmartWebFilePageMenuID        uint64 = 810000008
	SmartWebDictPageMenuID        uint64 = 810000009
	SmartWebConfigPageMenuID      uint64 = 810000010
	SmartWebSerialPageMenuID      uint64 = 810000105
	SmartWebMessagePageMenuID     uint64 = 810000106
	SmartWebFeedbackPageMenuID    uint64 = 810000111
	SmartWebHelpDocPageMenuID     uint64 = 810000114
	SmartWebLoginLogPageMenuID    uint64 = 810000124
	SmartWebLoginFailPageMenuID   uint64 = 810000125
	SmartWebLevel3PageMenuID      uint64 = 810000126
	SmartWebOperateLogPageMenuID  uint64 = 810000130
	SmartWebCachePageMenuID       uint64 = 810000134
	SmartWebReloadPageMenuID      uint64 = 810000137
	SmartWebJobPageMenuID         uint64 = 810000145
	SmartWebEncryptPageMenuID     uint64 = 810000155
	SmartWebHeartbeatPageMenuID   uint64 = 810000156
	SmartWebChangeLogPageMenuID   uint64 = 810000157
	SmartWebCodeGenPageMenuID     uint64 = 810000158
	SmartWebNoticePageMenuID      uint64 = 810000170
	SmartWebRoleReaderID          uint64 = 810000101
	SmartWebRoleWriterID          uint64 = 810000102
	SmartWebRoleAdminID           uint64 = 810000103

	// SmartWebMenuTypePoint is the existing SmartAdmin function-point value.
	SmartWebMenuTypePoint = 3

	SmartWebCapabilityProjectReadMenuID     uint64 = 810000011
	SmartWebCapabilityProjectReadAnyMenuID  uint64 = 810000012
	SmartWebCapabilityProjectMemberMenuID   uint64 = 810000013
	SmartWebCapabilityRoleManageMenuID      uint64 = 810000014
	SmartWebCapabilityGreeterHelloMenuID    uint64 = 810000015
	SmartWebCapabilityGreeterWatchMenuID    uint64 = 810000016
	SmartWebCapabilityFileReadMenuID        uint64 = 810000071
	SmartWebCapabilityFileManageMenuID      uint64 = 810000072
	SmartWebCapabilityDictReadMenuID        uint64 = 810000081
	SmartWebCapabilityDictManageMenuID      uint64 = 810000082
	SmartWebCapabilityConfigReadMenuID      uint64 = 810000091
	SmartWebCapabilityConfigManageMenuID    uint64 = 810000092
	SmartWebCapabilitySerialReadMenuID      uint64 = 810000101
	SmartWebCapabilitySerialGenMenuID       uint64 = 810000102
	SmartWebCapabilityMessageReadMenuID     uint64 = 810000107
	SmartWebCapabilityMessageSendMenuID     uint64 = 810000108
	SmartWebCapabilityFeedbackReadMenuID    uint64 = 810000112
	SmartWebCapabilityHelpDocReadMenuID     uint64 = 810000115
	SmartWebCapabilityHelpDocManageMenuID   uint64 = 810000116
	SmartWebCapabilityLoginLogReadMenuID    uint64 = 810000127
	SmartWebCapabilityLoginLogManageMenuID  uint64 = 810000128
	SmartWebCapabilityOperateLogReadMenuID  uint64 = 810000131
	SmartWebCapabilityCacheReadMenuID       uint64 = 810000135
	SmartWebCapabilityCacheManageMenuID     uint64 = 810000136
	SmartWebCapabilityReloadReadMenuID      uint64 = 810000139
	SmartWebCapabilityReloadManageMenuID    uint64 = 810000140
	SmartWebCapabilityJobReadMenuID         uint64 = 810000146
	SmartWebCapabilityJobManageMenuID       uint64 = 810000147
	SmartWebCapabilityJobExecuteMenuID      uint64 = 810000148
	SmartWebCapabilityDevToolsReadMenuID    uint64 = 810000161
	SmartWebCapabilityChangeLogReadMenuID   uint64 = 810000162
	SmartWebCapabilityChangeLogManageMenuID uint64 = 810000163
	SmartWebCapabilityNoticeReadMenuID      uint64 = 810000171
	SmartWebCapabilityNoticeManageMenuID    uint64 = 810000172

	SmartWebProjectDataScopeType  = 1
	SmartWebProjectScopeRelated   = 1
	SmartWebProjectScopeAllTenant = 2
)

// ResponseDTO retains the Smart API response envelope exactly where the
// existing smart-web Axios interceptor expects it. Gosky-native endpoints keep
// their normal REST representations and do not use this type.
// DataTypeEncrypt marks response data as an SM4-encrypted demo envelope.
const DataTypeEncrypt = 10

type ResponseDTO[T any] struct {
	Code     int    `json:"code"`
	Level    any    `json:"level"`
	Msg      string `json:"msg"`
	OK       bool   `json:"ok"`
	Data     T      `json:"data"`
	DataType int    `json:"dataType"`
}

func OK[T any](data T) ResponseDTO[T] {
	return ResponseDTO[T]{Code: 0, Level: nil, Msg: "操作成功", OK: true, Data: data, DataType: 1}
}

func Reject[T any]() ResponseDTO[T] {
	var zero T
	return ResponseDTO[T]{Code: 10001, Level: "error", Msg: "登录名或密码错误", OK: false, Data: zero, DataType: 1}
}

// MenuVO uses only the original Smart API fields. Component is a code-owned
// identifier for an existing compiled SmartAdmin view; it never comes from a
// Casbin policy row or browser input.
type MenuVO struct {
	MenuID       uint64   `json:"menuId"`
	ParentID     uint64   `json:"parentId"`
	MenuName     string   `json:"menuName"`
	MenuType     int      `json:"menuType"`
	Sort         int      `json:"sort"`
	Path         string   `json:"path"`
	FrameURL     string   `json:"frameUrl,omitempty"`
	Component    string   `json:"component"`
	FrameFlag    bool     `json:"frameFlag"`
	CacheFlag    bool     `json:"cacheFlag"`
	VisibleFlag  bool     `json:"visibleFlag"`
	DisabledFlag bool     `json:"disabledFlag"`
	PermsType    int      `json:"permsType"`
	WebPerms     string   `json:"webPerms"`
	APIPerms     string   `json:"apiPerms"`
	Icon         string   `json:"icon"`
	Children     []MenuVO `json:"children,omitempty"`
}

// LoginResultVO is the browser-safe subset of smart-api's LoginResultVO.
// Fields unused by Gosky's current identity schema retain their Smart API JSON
// names with neutral values so the unmodified Pinia store can hydrate safely.
type LoginResultVO struct {
	Token              string   `json:"token"`
	EmployeeID         uint64   `json:"employeeId"`
	UserType           int      `json:"userType"`
	LoginName          string   `json:"loginName"`
	ActualName         string   `json:"actualName"`
	Avatar             string   `json:"avatar"`
	Gender             int      `json:"gender"`
	Phone              string   `json:"phone"`
	DepartmentID       uint64   `json:"departmentId"`
	DepartmentName     string   `json:"departmentName"`
	PositionID         uint64   `json:"positionId"`
	Email              string   `json:"email"`
	DisabledFlag       bool     `json:"disabledFlag"`
	AdministratorFlag  bool     `json:"administratorFlag"`
	Remark             string   `json:"remark"`
	MenuList           []MenuVO `json:"menuList"`
	NeedUpdatePwdFlag  bool     `json:"needUpdatePwdFlag"`
	LastLoginIP        string   `json:"lastLoginIp"`
	LastLoginIPRegion  string   `json:"lastLoginIpRegion"`
	LastLoginUserAgent string   `json:"lastLoginUserAgent"`
	LastLoginTime      any      `json:"lastLoginTime"`
}

// RoleVO is the browser-safe fixed role definition shown in the existing
// SmartAdmin role view. Gosky does not represent these as mutable database
// entities: reader, writer, and admin are the only supported Casbin roles.
type RoleVO struct {
	RoleID   uint64 `json:"roleId"`
	RoleName string `json:"roleName"`
	RoleCode string `json:"roleCode"`
	Remark   string `json:"remark"`
}

// RoleMenuSelectionVO is the SmartAdmin role-tree response shape. Both the
// tree and selected IDs are code-owned and contain no raw Casbin rules.
type RoleMenuSelectionVO struct {
	MenuTreeList   []MenuVO `json:"menuTreeList"`
	SelectedMenuID []uint64 `json:"selectedMenuId"`
}

// RequestURLVO matches SmartAdmin's auxiliary request-path response shape.
// Gosky returns an empty catalog because its deployment-owned capabilities
// deliberately do not expose raw transport paths as browser permission data.
type RequestURLVO struct {
	Comment string `json:"comment"`
	Name    string `json:"name"`
	URL     string `json:"url"`
}

type DataScopeViewTypeVO struct {
	ViewType     int    `json:"viewType"`
	ViewTypeName string `json:"viewTypeName"`
}

// RoleDataScopeVO is the limited, typed data-scope surface supported by
// Gosky. It intentionally models only Project visibility rather than copying
// SmartAdmin's generic query-interceptor configuration.
type RoleDataScopeVO struct {
	DataScopeType     int                   `json:"dataScopeType"`
	DataScopeTypeName string                `json:"dataScopeTypeName"`
	DataScopeTypeDesc string                `json:"dataScopeTypeDesc"`
	ViewType          int                   `json:"viewType,omitempty"`
	ViewTypeList      []DataScopeViewTypeVO `json:"viewTypeList,omitempty"`
}

type RoleDataScopeUpdateItem struct {
	DataScopeType int `json:"dataScopeType"`
	ViewType      int `json:"viewType"`
}

// RoleEmployeeVO is the credential-free subset the existing SmartAdmin role
// table renders. IDs are Gosky user IDs, resolved only from the verified
// caller's current tenant membership.
type RoleEmployeeVO struct {
	EmployeeID        uint64   `json:"employeeId"`
	LoginName         string   `json:"loginName"`
	ActualName        string   `json:"actualName"`
	Phone             string   `json:"phone"`
	DepartmentName    string   `json:"departmentName"`
	DisabledFlag      bool     `json:"disabledFlag"`
	AdministratorFlag bool     `json:"administratorFlag"`
	CreateTime        any      `json:"createTime"`
	RoleIDList        []uint64 `json:"roleIdList"`
	RoleNameList      []string `json:"roleNameList"`
}

// RoleEmployeePageVO preserves SmartAdmin's page envelope while returning
// only active same-tenant Gosky memberships.
type RoleEmployeePageVO struct {
	List  []RoleEmployeeVO `json:"list"`
	Total int              `json:"total"`
}

// RoleManager is the controlled, audited bridge between SmartAdmin's role
// page identifiers and Gosky's canonical Casbin and tenant-user contracts.
// It never accepts browser-supplied subject, capability, action, or domain
// values.
type RoleManager interface {
	SetProjectScope(context.Context, string, app.RoleProjectScope, string) *coreerrors.Error
	RoleCapabilities(context.Context, string) ([]authz.Capability, *coreerrors.Error)
	ReplaceRoleCapabilities(context.Context, string, []authz.Capability, string) *coreerrors.Error
	ListRoleMembers(context.Context, string, uint64, uint64, string) (app.RoleMemberPage, *coreerrors.Error)
	ListTenantUsers(context.Context, uint64, uint64, string) (app.RoleMemberPage, *coreerrors.Error)
	AddRoleMembers(context.Context, string, []uint64, string) *coreerrors.Error
	RemoveRoleMembers(context.Context, string, []uint64, string) *coreerrors.Error
}

// Directory is the tenant-managed role/menu catalog surface. It is satisfied
// by app.RBACService; all tenant selection comes from the verified context.
type Directory interface {
	ManagementNavigation(context.Context) ([]app.MenuRecord, *coreerrors.Error)
	ActiveRoles(context.Context) ([]app.RoleRecord, *coreerrors.Error)
	RoleMenuSelection(context.Context, uint64) (app.RoleRecord, []app.MenuRecord, []uint64, *coreerrors.Error)
	ReplaceRoleGrants(context.Context, uint64, []uint64) *coreerrors.Error
	ListMenus(context.Context, bool) ([]app.MenuRecord, *coreerrors.Error)
	CreateMenu(context.Context, app.MenuInput) (app.MenuRecord, *coreerrors.Error)
	UpdateMenu(context.Context, uint64, app.MenuInput) *coreerrors.Error
	DeleteMenu(context.Context, uint64) *coreerrors.Error
	CreateRole(context.Context, app.RoleInput) (app.RoleRecord, *coreerrors.Error)
	UpdateRole(context.Context, uint64, app.RoleUpdate) *coreerrors.Error
	DeleteRole(context.Context, uint64) *coreerrors.Error
}

// Organization is the SmartAdmin employee/department/position facade.
// Methods return SmartAdmin-compatible app projections; create-employee
// and password-reset also return the plaintext password (generated when
// the request supplies none) for one-time display to the operator.
type Organization interface {
	ListDepartments(context.Context) ([]app.Department, error)
	DepartmentTree(context.Context) ([]app.Department, error)
	CreateDepartment(context.Context, app.DepartmentInput) (app.Department, error)
	UpdateDepartment(context.Context, uint64, app.DepartmentInput) error
	DeleteDepartment(context.Context, uint64) error

	ListPositions(context.Context, string) ([]app.Position, error)
	CreatePosition(context.Context, app.PositionInput) (app.Position, error)
	UpdatePosition(context.Context, uint64, app.PositionInput) error
	DeletePosition(context.Context, uint64) error

	ListEmployees(context.Context, app.EmployeeQuery) (app.EmployeePage, error)
	ListAllEmployees(context.Context, bool) ([]app.Employee, error)
	EmployeesByDepartment(context.Context, uint64) ([]app.Employee, error)
	CreateEmployee(context.Context, app.EmployeeInput) (app.Employee, string, error)
	UpdateEmployee(context.Context, app.EmployeeInput) error
	SetEmployeeDisabled(context.Context, uint64, bool) error
	DeleteEmployees(context.Context, []uint64) error
	MoveEmployeesDepartment(context.Context, []uint64, uint64) error
	ResetEmployeePassword(context.Context, uint64, string) (string, error)
	UpdateEmployeeCenter(context.Context, uint64, app.EmployeeCenterInput) error
	UpdateEmployeeAvatar(context.Context, uint64, string) error
}

// Authenticator is the narrow native-auth facade needed by the compatibility
// adapter. It keeps the handler free of SQL and password-verification details.
type Authenticator interface {
	LoginSmartWeb(context.Context, authn.SmartWebLoginInput) (authn.SmartWebLoginResult, error)
	SmartWebLoginInfo(context.Context, authn.RequestPrincipal) (authn.SmartWebIdentity, error)
	Logout(context.Context, authn.LogoutInput) error
}

// PolicyAuthorizer is the narrow capability check used for browser
// navigation and compatibility reads. It derives tenant and subject only from
// the verified principal carried in context.
type PolicyAuthorizer interface {
	Authorize(context.Context, string, string) *coreerrors.Error
}

// Service coordinates captcha, Smart API request decoding, and Gosky's native
// identity/session service. It does not issue refresh tokens through this
// legacy browser surface.
type Service struct {
	authenticator Authenticator
	policy        PolicyAuthorizer
	captchas      *CaptchaService
	roles         RoleManager
	directory     Directory
	org           Organization
	twoFactor     LoginSecondFactor
	loginAuditor  LoginAuditor
}

// LoginSecondFactor verifies the optional email code during login. A nil
// verifier means single-factor login (feature disabled).
type LoginSecondFactor interface {
	Enabled() bool
	Verify(ctx context.Context, loginName, code string) *coreerrors.Error
}

// SetTwoFactor wires the email-code verifier used by login.
func (s *Service) SetTwoFactor(verifier LoginSecondFactor) {
	if s == nil {
		return
	}
	s.twoFactor = verifier
}

// SetDirectory wires the dynamic tenant catalog after construction, keeping
// the historical constructor signature for test call sites.
func (s *Service) SetDirectory(directory Directory) {
	if s == nil {
		return
	}
	s.directory = directory
}

// SetOrganization wires the employee/department/position facade.
func (s *Service) SetOrganization(org Organization) {
	if s == nil {
		return
	}
	s.org = org
}

// CurrentOrganization returns the wired organization facade.
func (s *Service) CurrentOrganization() Organization {
	if s == nil {
		return nil
	}
	return s.org
}

func NewService(authenticator Authenticator, policy PolicyAuthorizer, captchas *CaptchaService, roleManagers ...RoleManager) (*Service, error) {
	if authenticator == nil {
		return nil, errors.New("smart-web authenticator is required")
	}
	if len(roleManagers) > 1 {
		return nil, errors.New("only one smart-web role manager is supported")
	}
	if captchas == nil {
		captchas = NewCaptchaService(CaptchaOptions{})
	}
	var roleManager RoleManager
	if len(roleManagers) == 1 {
		roleManager = roleManagers[0]
	}
	return &Service{authenticator: authenticator, policy: policy, captchas: captchas, roles: roleManager}, nil
}

func (s *Service) NewCaptcha() (CaptchaChallenge, error) {
	if s == nil || s.captchas == nil {
		return CaptchaChallenge{}, errors.New("smart-web captcha service is unavailable")
	}
	return s.captchas.NewChallenge()
}

func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResultVO, error) {
	if s == nil || s.authenticator == nil || s.captchas == nil {
		return LoginResultVO{}, errors.New("smart-web authentication service is unavailable")
	}
	if !s.captchas.Verify(input.CaptchaUUID, input.CaptchaCode) {
		return LoginResultVO{}, authn.ErrAuthenticationRejected
	}
	password, err := DecryptPassword(input.Password)
	if err != nil {
		return LoginResultVO{}, authn.ErrAuthenticationRejected
	}
	meta := loginMetaFromContext(ctx)
	loginName := strings.TrimSpace(input.LoginName)
	if s.loginAuditor != nil {
		if lockErr := s.loginAuditor.CheckLock(ctx, loginName); lockErr != nil {
			return LoginResultVO{}, lockErr
		}
	}
	result, err := s.authenticator.LoginSmartWeb(ctx, authn.SmartWebLoginInput{
		LoginName: loginName,
		Password:  password,
		RequestID: input.RequestID,
		TraceID:   input.TraceID,
	})
	if err != nil {
		if s.loginAuditor != nil {
			if hint, show := s.loginAuditor.RecordFailure(ctx, loginName, meta); show && hint != "" {
				return LoginResultVO{}, coreerrors.New(app.LoginFailWillLockCode, hint)
			}
		}
		return LoginResultVO{}, err
	}
	if s.twoFactor != nil && s.twoFactor.Enabled() {
		if verifyErr := s.twoFactor.Verify(ctx, loginName, strings.TrimSpace(input.EmailCode)); verifyErr != nil {
			return LoginResultVO{}, verifyErr
		}
	}
	if s.loginAuditor != nil {
		s.loginAuditor.RecordSuccess(ctx, loginName, meta)
	}
	menus, err := s.MenusForIdentity(ctx, result.Identity)
	if err != nil {
		return LoginResultVO{}, err
	}
	return loginResult(result.Identity, result.AccessToken, menus), nil
}

func (s *Service) LoginInfo(ctx context.Context, principal authn.RequestPrincipal, accessToken string) (LoginResultVO, error) {
	if s == nil || s.authenticator == nil || strings.TrimSpace(accessToken) == "" {
		return LoginResultVO{}, authn.ErrAuthenticationRejected
	}
	identity, err := s.authenticator.SmartWebLoginInfo(ctx, principal)
	if err != nil {
		return LoginResultVO{}, err
	}
	menus, err := s.MenusForIdentity(ctx, identity)
	if err != nil {
		return LoginResultVO{}, err
	}
	return loginResult(identity, accessToken, menus), nil
}

func (s *Service) Logout(ctx context.Context, principal authn.RequestPrincipal, requestID string) error {
	if s == nil || s.authenticator == nil {
		return authn.ErrAuthenticationUnavailable
	}
	if err := s.authenticator.Logout(ctx, authn.LogoutInput{Principal: principal, RequestID: requestID}); err != nil {
		return err
	}
	if s.loginAuditor != nil {
		meta := loginMetaFromContext(ctx)
		s.loginAuditor.RecordLogout(ctx, principal.Subject, principal.TenantID, meta)
	}
	return nil
}

// MenusForIdentity projects only browser pages which Gosky has implemented.
// Every structural page and operator button is checked against its own
// capability; a normal Casbin denial hides the node, while an unavailable
// authorization decision is returned so the login boundary fails closed.
// Catalogs with no visible descendant are pruned so the sidebar never shows
// an empty group.
func (s *Service) MenusForIdentity(ctx context.Context, identity authn.SmartWebIdentity) ([]MenuVO, error) {
	if s == nil || s.policy == nil {
		return make([]MenuVO, 0), nil
	}
	projected := auth.NewContext(ctx, auth.Principal{
		Subject: identity.Subject,
		Claims:  map[string]any{"tenant_id": strconv.FormatUint(identity.TenantID, 10)},
	})
	var rows []MenuVO
	if s.directory != nil {
		records, dirErr := s.directory.ManagementNavigation(projected)
		if dirErr != nil {
			return nil, dirErr
		}
		rows = make([]MenuVO, 0, len(records))
		for _, record := range records {
			rows = append(rows, menuRecordToVO(record))
		}
	} else {
		rows = smartWebManagementNavigation()
	}
	visible := make([]MenuVO, 0, len(rows))
	for _, row := range rows {
		var capability authz.Capability
		known := false
		if row.MenuType == SmartWebMenuTypePoint {
			capability, known = managementButtonCapabilities[row.WebPerms]
		} else {
			capability, known = managementPageCapabilities[row.MenuID]
		}
		if known {
			if err := s.requireCapability(projected, capability); err != nil {
				if err.Code == coreerrors.CodePermissionDenied {
					continue
				}
				return nil, err
			}
		}
		// Rows without a code-owned capability (tenant-created pages and
		// directory-granted custom buttons) keep their directory visibility.
		visible = append(visible, row)
	}
	return pruneEmptyCatalogs(visible), nil
}

// pruneEmptyCatalogs keeps a catalog only while at least one visible
// descendant page or function point remains underneath it.
func pruneEmptyCatalogs(menus []MenuVO) []MenuVO {
	byID := make(map[uint64]MenuVO, len(menus))
	hasDescendant := make(map[uint64]bool)
	for _, menu := range menus {
		byID[menu.MenuID] = menu
	}
	for _, menu := range menus {
		if menu.MenuType == SmartWebMenuTypeCatalog {
			continue
		}
		parentID := menu.ParentID
		for parentID != 0 {
			parent, exists := byID[parentID]
			if !exists {
				break
			}
			hasDescendant[parentID] = true
			parentID = parent.ParentID
		}
	}
	result := make([]MenuVO, 0, len(menus))
	for _, menu := range menus {
		if menu.MenuType == SmartWebMenuTypeCatalog && !hasDescendant[menu.MenuID] {
			continue
		}
		result = append(result, menu)
	}
	return result
}

// RoleDefinitions returns the immutable Gosky Casbin role catalog after the
// current verified admin has passed the same capability boundary that made the
// page visible.
func (s *Service) RoleDefinitions(ctx context.Context) ([]RoleVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return nil, err
	}
	roles := []RoleVO{
		{RoleID: SmartWebRoleReaderID, RoleName: "Reader", RoleCode: authz.RoleReader, Remark: "Read-only tenant role"},
		{RoleID: SmartWebRoleWriterID, RoleName: "Writer", RoleCode: authz.RoleWriter, Remark: "Explicit Project write permissions"},
		{RoleID: SmartWebRoleAdminID, RoleName: "Administrator", RoleCode: authz.RoleAdmin, Remark: "Explicit tenant administration permissions"},
	}
	if s.directory == nil {
		return roles, nil
	}
	dynamic, err := s.directory.ActiveRoles(ctx)
	if err != nil {
		return nil, err
	}
	for _, role := range dynamic {
		if role.System {
			continue
		}
		roles = append(roles, roleRecordVO(role))
	}
	return roles, nil
}

// MenuCatalog returns the complete SmartAdmin menu catalog rendered by the
// unchanged Menu Catalog page. With a tenant directory wired it is the flat
// active tenant catalog (including operator-created rows); deployments
// without a directory see the code-owned fallback. Both read models are
// guarded by the same capability as Role Management.
func (s *Service) MenuCatalog(ctx context.Context) ([]MenuVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return nil, err
	}
	if s.directory != nil {
		records, err := s.directory.ListMenus(ctx, true)
		if err != nil {
			return nil, err
		}
		return menuRecordsFlat(records), nil
	}
	return smartWebCapabilityCatalog(), nil
}

// MenuTree returns the menu catalog as the parent/children shape used by the
// menu editor's parent selector. The onlyMenu projection omits all function
// points, matching SmartAdmin's menu editor contract.
func (s *Service) MenuTree(ctx context.Context, onlyMenu bool) ([]MenuVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return nil, err
	}
	if s.directory != nil {
		records, err := s.directory.ListMenus(ctx, true)
		if err != nil {
			return nil, err
		}
		return menuRecordsToTree(records, onlyMenu), nil
	}
	return smartWebMenuTree(onlyMenu), nil
}

// MenuAuthorizationURLs returns an empty compatibility response because
// Gosky does not support selecting transport URLs in the menu editor.
func (s *Service) MenuAuthorizationURLs(ctx context.Context) ([]RequestURLVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return nil, err
	}
	return make([]RequestURLVO, 0), nil
}

// RejectMenuMutation preserves the historical failure for deployments without
// a tenant catalog. With a directory it must not be called: the mutation
// methods below serve the legacy endpoints instead.
func (s *Service) RejectMenuMutation(ctx context.Context) *coreerrors.Error {
	if err := s.requireRoleRead(ctx); err != nil {
		return err
	}
	return coreerrors.New(coreerrors.CodeFailedPrecondition, "gosky menu catalog is deployment-owned and read-only")
}

// MenuMutation is the SmartAdmin menu add/update form. Only catalog/page
// fields are editable; a tenant cannot invent capabilities or operator
// buttons, and seeded rows stay immutable.
type MenuMutation struct {
	MenuID        uint64 `json:"menuId"`
	ParentID      uint64 `json:"parentId"`
	ContextMenuID uint64 `json:"contextMenuId"`
	MenuName      string `json:"menuName"`
	MenuType      int    `json:"menuType"`
	Sort          int    `json:"sort"`
	Path          string `json:"path"`
	FrameURL      string `json:"frameUrl"`
	Component     string `json:"component"`
	Icon          string `json:"icon"`
	WebPerms      string `json:"webPerms"`
	APIPerms      string `json:"apiPerms"`
	VisibleFlag   bool   `json:"visibleFlag"`
	DisabledFlag  bool   `json:"disabledFlag"`
	FrameFlag     bool   `json:"frameFlag"`
	CacheFlag     bool   `json:"cacheFlag"`
}

func (m MenuMutation) input() app.MenuInput {
	parent := m.ParentID
	if parent == 0 && m.MenuType == SmartWebMenuTypePoint {
		// Points attach to the "function point context menu" page.
		parent = m.ContextMenuID
	}
	return app.MenuInput{
		ParentID:  parent,
		Name:      m.MenuName,
		Type:      m.MenuType,
		Sort:      m.Sort,
		Path:      m.Path,
		FrameURL:  m.FrameURL,
		Component: m.Component,
		Icon:      m.Icon,
		WebPerms:  m.WebPerms,
		Visible:   m.VisibleFlag,
		Disabled:  m.DisabledFlag,
		FrameFlag: m.FrameFlag,
		CacheFlag: m.CacheFlag,
		// apiPerms are intentionally dropped: backend capabilities stay
		// deployment-owned and are never derived from browser input.
	}
}

// CreateMenu serves /menu/add through the tenant catalog. Custom menus may be
// catalogs or pages; function points must be seeded because only deployment
// capabilities are valid grants.
func (s *Service) CreateMenu(ctx context.Context, mutation MenuMutation) (MenuVO, *coreerrors.Error) {
	if err := s.requireCapability(ctx, authz.CapabilitySystemMenuManage); err != nil {
		return MenuVO{}, err
	}
	if s.directory == nil {
		return MenuVO{}, s.RejectMenuMutation(ctx)
	}
	record, err := s.directory.CreateMenu(ctx, mutation.input())
	if err != nil {
		return MenuVO{}, err
	}
	return menuRecordToVO(record), nil
}

func (s *Service) UpdateMenu(ctx context.Context, mutation MenuMutation) *coreerrors.Error {
	if err := s.requireCapability(ctx, authz.CapabilitySystemMenuManage); err != nil {
		return err
	}
	if mutation.MenuID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "menu update is invalid")
	}
	if s.directory == nil {
		return s.RejectMenuMutation(ctx)
	}
	return s.directory.UpdateMenu(ctx, mutation.MenuID, mutation.input())
}

// DeleteMenus serves /menu/batchDelete. Every identifier must delete
// successfully; the service rejects the whole request on the first failure.
func (s *Service) DeleteMenus(ctx context.Context, menuIDs []uint64) *coreerrors.Error {
	if err := s.requireCapability(ctx, authz.CapabilitySystemMenuManage); err != nil {
		return err
	}
	if s.directory == nil {
		return s.RejectMenuMutation(ctx)
	}
	if len(menuIDs) == 0 || len(menuIDs) > 100 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "menu delete request is invalid")
	}
	seen := make(map[uint64]struct{}, len(menuIDs))
	for _, menuID := range menuIDs {
		if menuID == 0 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "menu delete request is invalid")
		}
		if _, exists := seen[menuID]; exists {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "menu delete request is invalid")
		}
		seen[menuID] = struct{}{}
		if err := s.directory.DeleteMenu(ctx, menuID); err != nil {
			return err
		}
	}
	return nil
}

// RoleMenuSelection returns the fixed capability tree for one known role. It
// rejects an unknown browser role ID rather than treating it as a policy key.
func (s *Service) RoleMenuSelection(ctx context.Context, roleID uint64) (RoleMenuSelectionVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return RoleMenuSelectionVO{}, err
	}
	if s.directory != nil {
		_, records, selected, err := s.directory.RoleMenuSelection(ctx, roleID)
		if err != nil {
			return RoleMenuSelectionVO{}, err
		}
		// Current-operator webPerms buttons are never target-role grants and
		// the repository rejects them, so they must not render as selectable
		// leaves for the role being edited.
		grantable := make([]app.MenuRecord, 0, len(records))
		for _, record := range records {
			if record.Type == SmartWebMenuTypePoint && record.WebPerms != "" {
				continue
			}
			grantable = append(grantable, record)
		}
		echo := structuralRecordIDs(grantable)
		echo = append(echo, selected...)
		return RoleMenuSelectionVO{MenuTreeList: menuRecordsToTree(grantable, false), SelectedMenuID: echo}, nil
	}
	if !smartWebRoleID(roleID) {
		return RoleMenuSelectionVO{}, coreerrors.New(coreerrors.CodeInvalidArgument, "unknown gosky role")
	}
	role := smartWebRoleCode(roleID)
	capabilities, managerErr := s.roleCapabilities(ctx, role)
	if managerErr != nil {
		return RoleMenuSelectionVO{}, managerErr
	}
	selected := smartWebStructuralMenuIDs()
	selected = append(selected, capabilityMenuIDs(capabilities)...)
	return RoleMenuSelectionVO{MenuTreeList: smartWebMenuTree(false), SelectedMenuID: selected}, nil
}

// RoleDataScopes keeps SmartAdmin's role-page bootstrap sequence successful.
// Gosky's Project ABAC uses explicit tenant/project relationships instead of
// SmartAdmin's generic data-scope model, so there is no mutable scope catalog.
func (s *Service) RoleDataScopes(ctx context.Context) ([]RoleDataScopeVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return nil, err
	}
	return []RoleDataScopeVO{{
		DataScopeType:     SmartWebProjectDataScopeType,
		DataScopeTypeName: "Projects",
		DataScopeTypeDesc: "Related Projects permits only owner or active collaborator access. All tenant Projects grants tenant-wide Project read access.",
		ViewTypeList: []DataScopeViewTypeVO{
			{ViewType: SmartWebProjectScopeRelated, ViewTypeName: "Related Projects"},
			{ViewType: SmartWebProjectScopeAllTenant, ViewTypeName: "All tenant Projects"},
		},
	}}, nil
}

// RoleDataScopeSelection projects the role's direct Project capabilities into
// the supported scope choices. Both fixed and tenant-managed roles are
// resolved through the directory; inherited capabilities are not edited here.
func (s *Service) RoleDataScopeSelection(ctx context.Context, roleID uint64) ([]RoleDataScopeVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return nil, err
	}
	role, roleErr := s.roleCode(ctx, roleID)
	if roleErr != nil {
		return nil, roleErr
	}
	capabilities, managerErr := s.roleCapabilities(ctx, role)
	if managerErr != nil {
		return nil, managerErr
	}
	viewType := SmartWebProjectScopeRelated
	if containsCapability(capabilities, authz.CapabilityProjectReadAny) {
		viewType = SmartWebProjectScopeAllTenant
	}
	return []RoleDataScopeVO{{DataScopeType: SmartWebProjectDataScopeType, ViewType: viewType}}, nil
}

// RoleMembers lists active identities with direct bindings to a fixed role.
// The role manager resolves subjects within the verified caller's tenant.
func (s *Service) RoleMembers(ctx context.Context, roleID, pageNum, pageSize uint64, keywords string) (RoleEmployeePageVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return RoleEmployeePageVO{}, err
	}
	if s.roles == nil {
		return RoleEmployeePageVO{}, coreerrors.New(coreerrors.CodeUnavailable, "smart-web role management is unavailable")
	}
	role, err := s.roleCode(ctx, roleID)
	if err != nil {
		return RoleEmployeePageVO{}, err
	}
	page, err := s.roles.ListRoleMembers(ctx, role, pageNum, pageSize, keywords)
	if err != nil {
		return RoleEmployeePageVO{}, err
	}
	return roleEmployeePage(page, role), nil
}

// roleCode maps a fixed SmartAdmin role ID to its canonical code, or resolves
// a tenant-managed role ID through the directory.
func (s *Service) roleCode(ctx context.Context, roleID uint64) (string, *coreerrors.Error) {
	if code := smartWebRoleCode(roleID); code != "" {
		return code, nil
	}
	if s.directory == nil {
		return "", coreerrors.New(coreerrors.CodeInvalidArgument, "unknown gosky role")
	}
	roles, err := s.directory.ActiveRoles(ctx)
	if err != nil {
		return "", err
	}
	for _, role := range roles {
		if role.ID == roleID {
			return role.Code, nil
		}
	}
	return "", coreerrors.New(coreerrors.CodeInvalidArgument, "unknown gosky role")
}

func (s *Service) UpdateRoleMenu(ctx context.Context, roleID uint64, menuIDs []uint64, requestID string) *coreerrors.Error {
	if err := s.requireCapability(ctx, authz.CapabilityAuthorizationRoleManage); err != nil {
		return err
	}
	if roleID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role menu update is invalid")
	}
	// With a tenant directory the combined role_menus/Casbin transaction is
	// the authority for both fixed and tenant-managed roles; the repository
	// validates every menu identifier and rejects operator buttons.
	if s.directory != nil {
		return s.directory.ReplaceRoleGrants(ctx, roleID, menuIDs)
	}
	if !smartWebRoleID(roleID) || s.roles == nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role menu update is invalid")
	}
	capabilities, err := capabilitiesFromMenuIDs(menuIDs)
	if err != nil {
		return err
	}
	return s.roles.ReplaceRoleCapabilities(ctx, smartWebRoleCode(roleID), capabilities, requestID)
}

func (s *Service) UpdateRoleDataScopes(ctx context.Context, roleID uint64, items []RoleDataScopeUpdateItem, requestID string) *coreerrors.Error {
	if err := s.requireCapability(ctx, authz.CapabilityAuthorizationRoleScopeManage); err != nil {
		return err
	}
	if s.roles == nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "smart-web role management is unavailable")
	}
	role, roleErr := s.roleCode(ctx, roleID)
	if roleErr != nil {
		return roleErr
	}
	if len(items) != 1 || items[0].DataScopeType != SmartWebProjectDataScopeType {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role data-scope update is invalid")
	}
	var scope app.RoleProjectScope
	switch items[0].ViewType {
	case SmartWebProjectScopeRelated:
		scope = app.RoleProjectScopeRelated
	case SmartWebProjectScopeAllTenant:
		scope = app.RoleProjectScopeAllTenant
	default:
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role data-scope view is invalid")
	}
	return s.roles.SetProjectScope(ctx, role, scope, requestID)
}

func (s *Service) AddRoleMembers(ctx context.Context, roleID uint64, employeeIDs []uint64, requestID string) *coreerrors.Error {
	if err := s.requireCapability(ctx, authz.CapabilityAuthorizationRoleMemberAdd); err != nil {
		return err
	}
	if s.roles == nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role employee update is invalid")
	}
	role, err := s.roleCode(ctx, roleID)
	if err != nil {
		return err
	}
	return s.roles.AddRoleMembers(ctx, role, employeeIDs, requestID)
}

func (s *Service) TenantEmployees(ctx context.Context, pageNum, pageSize uint64, keywords string) (RoleEmployeePageVO, *coreerrors.Error) {
	if err := s.requireRoleRead(ctx); err != nil {
		return RoleEmployeePageVO{}, err
	}
	if s.roles == nil {
		return RoleEmployeePageVO{}, coreerrors.New(coreerrors.CodeUnavailable, "smart-web role management is unavailable")
	}
	page, err := s.roles.ListTenantUsers(ctx, pageNum, pageSize, keywords)
	if err != nil {
		return RoleEmployeePageVO{}, err
	}
	return roleEmployeePage(page, ""), nil
}

func (s *Service) RemoveRoleMembers(ctx context.Context, roleID uint64, employeeIDs []uint64, requestID string) *coreerrors.Error {
	if err := s.requireCapability(ctx, authz.CapabilityAuthorizationRoleMemberRemove); err != nil {
		return err
	}
	if s.roles == nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "smart-web role management is unavailable")
	}
	role, roleErr := s.roleCode(ctx, roleID)
	if roleErr != nil {
		return roleErr
	}
	return s.roles.RemoveRoleMembers(ctx, role, employeeIDs, requestID)
}

func (s *Service) requireRoleRead(ctx context.Context) *coreerrors.Error {
	if s == nil || s.policy == nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "smart-web role authorization is unavailable")
	}
	return s.requireCapability(ctx, authz.CapabilityAuthorizationRoleRead)
}

func (s *Service) roleCapabilities(ctx context.Context, role string) ([]authz.Capability, *coreerrors.Error) {
	if role == "" {
		return nil, coreerrors.New(coreerrors.CodeInvalidArgument, "unknown gosky role")
	}
	if s.roles == nil {
		return defaultRoleCapabilities(role), nil
	}
	capabilities, err := s.roles.RoleCapabilities(ctx, role)
	if err != nil {
		return nil, err
	}
	return normalizeCapabilities(capabilities), nil
}

func smartWebRoleID(roleID uint64) bool {
	return roleID == SmartWebRoleReaderID || roleID == SmartWebRoleWriterID || roleID == SmartWebRoleAdminID
}

func smartWebRoleCode(roleID uint64) string {
	switch roleID {
	case SmartWebRoleReaderID:
		return authz.RoleReader
	case SmartWebRoleWriterID:
		return authz.RoleWriter
	case SmartWebRoleAdminID:
		return authz.RoleAdmin
	default:
		return ""
	}
}

// LoginInput follows the pre-existing smart-web form, including compatibility
// fields that Gosky currently validates only for presence/shape at this edge.
type LoginInput struct {
	LoginName   string `json:"loginName"`
	Password    string `json:"password"`
	LoginDevice int    `json:"loginDevice"`
	CaptchaCode string `json:"captchaCode"`
	CaptchaUUID string `json:"captchaUuid"`
	EmailCode   string `json:"emailCode"`
	RequestID   string `json:"-"`
	TraceID     string `json:"-"`
}

func loginResult(identity authn.SmartWebIdentity, token string, menus []MenuVO) LoginResultVO {
	return LoginResultVO{
		Token:             token,
		EmployeeID:        identity.UserID,
		UserType:          1,
		LoginName:         identity.LoginName,
		ActualName:        identity.LoginName,
		MenuList:          menus,
		AdministratorFlag: false,
	}
}

func (s *Service) requireCapability(ctx context.Context, capability authz.Capability) *coreerrors.Error {
	if s == nil || s.policy == nil {
		return coreerrors.New(coreerrors.CodeUnavailable, "smart-web role authorization is unavailable")
	}
	return s.policy.Authorize(ctx, string(capability), authz.CapabilityAction)
}

// RoleAddForm is the SmartAdmin /role/add payload.
type RoleAddForm struct {
	RoleName string `json:"roleName"`
	RoleCode string `json:"roleCode"`
	Remark   string `json:"remark"`
}

// RoleUpdateForm is the SmartAdmin /role/update payload.
type RoleUpdateForm struct {
	RoleID   uint64 `json:"roleId"`
	RoleName string `json:"roleName"`
	RoleCode string `json:"roleCode"`
	Remark   string `json:"remark"`
}

// AddRole serves POST /role/add: create a tenant-managed role.
func (s *Service) AddRole(ctx context.Context, form RoleAddForm) (RoleVO, *coreerrors.Error) {
	if err := s.requireCapability(ctx, authz.CapabilityAuthorizationRoleManage); err != nil {
		return RoleVO{}, err
	}
	if s.directory == nil {
		return RoleVO{}, coreerrors.New(coreerrors.CodeFailedPrecondition, "role management is unavailable")
	}
	record, err := s.directory.CreateRole(ctx, app.RoleInput{
		Code: form.RoleCode, Name: form.RoleName, Remark: form.Remark,
	})
	if err != nil {
		return RoleVO{}, err
	}
	return roleRecordVO(record), nil
}

// UpdateRole serves POST /role/update on a tenant-managed role; the
// code is immutable and fixed roles are read-only.
func (s *Service) UpdateRole(ctx context.Context, form RoleUpdateForm) *coreerrors.Error {
	if err := s.requireCapability(ctx, authz.CapabilityAuthorizationRoleManage); err != nil {
		return err
	}
	if s.directory == nil || form.RoleID == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "role update is invalid")
	}
	if smartWebRoleID(form.RoleID) {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "seeded gosky role is read-only")
	}
	return s.directory.UpdateRole(ctx, form.RoleID, app.RoleUpdate{
		Name: form.RoleName, Remark: form.Remark,
	})
}

// DeleteRole serves GET /role/delete/{roleId}; only tenant-managed
// roles with no subject bindings may be deleted.
func (s *Service) DeleteRole(ctx context.Context, roleID uint64) *coreerrors.Error {
	if err := s.requireCapability(ctx, authz.CapabilityAuthorizationRoleManage); err != nil {
		return err
	}
	if s.directory == nil {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "role management is unavailable")
	}
	if smartWebRoleID(roleID) {
		return coreerrors.New(coreerrors.CodeFailedPrecondition, "seeded gosky role is read-only")
	}
	return s.directory.DeleteRole(ctx, roleID)
}
