package svc

import (
	"context"
	"sync"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/mq"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/navigation"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/twofactor"
	"github.com/imajinyun/gofly/rpc"
)

type RPCMuxDiagnosisClient interface {
	UpdateMuxDiagnosisEventExporter(rpc.RPCMuxDiagnosisEventExporter, rpc.RPCMuxDiagnosisFilter)
}

// AuthenticationService is the transport-facing contract for gosky-owned
// login, refresh, logout, JWT verification, and persisted session validation.
// The context stores the interface so HTTP route tests can exercise contracts
// without an external database while production supplies authn.Service.
type AuthenticationService interface {
	Login(context.Context, authn.LoginInput) (authn.TokenPair, error)
	Refresh(context.Context, authn.RefreshInput) (authn.TokenPair, error)
	Logout(context.Context, authn.LogoutInput) error
	ValidatePrincipal(context.Context, authn.RequestPrincipal) error
	JWTValidator() auth.Validator
}

// NavigationService returns the client-safe session projection after the
// route interceptor has established a typed principal. It deliberately does
// not expose raw Casbin policies, role graphs, or database relationship rows.
type NavigationService interface {
	Project(context.Context, authn.RequestPrincipal) (navigation.SessionProjection, *coreerrors.Error)
}

// AdminSessionService provides the admin-only, principal-scoped session
// management surface. It is independent from the public auth endpoints so a
// deployment can omit it rather than accidentally exposing a broader route.
type AdminSessionService interface {
	ListSessions(context.Context, authn.SessionListInput) (authn.SessionListResponse, error)
	RevokeSession(context.Context, authn.SessionRevokeInput) (authn.SessionRevokeResponse, error)
}

// SmartWebService owns only the HTTP compatibility behavior required by the
// unmodified SmartAdmin web client. It is intentionally separate from Gosky's
// native authentication and navigation APIs.
type SmartWebService interface {
	NewCaptcha() (smartweb.CaptchaChallenge, error)
	Login(context.Context, smartweb.LoginInput) (smartweb.LoginResultVO, error)
	LoginInfo(context.Context, authn.RequestPrincipal, string) (smartweb.LoginResultVO, error)
	Logout(context.Context, authn.RequestPrincipal, string) error
	MenuCatalog(context.Context) ([]smartweb.MenuVO, *coreerrors.Error)
	MenuTree(context.Context, bool) ([]smartweb.MenuVO, *coreerrors.Error)
	MenuAuthorizationURLs(context.Context) ([]smartweb.RequestURLVO, *coreerrors.Error)
	RejectMenuMutation(context.Context) *coreerrors.Error
	CreateMenu(context.Context, smartweb.MenuMutation) (smartweb.MenuVO, *coreerrors.Error)
	UpdateMenu(context.Context, smartweb.MenuMutation) *coreerrors.Error
	DeleteMenus(context.Context, []uint64) *coreerrors.Error
	AddRole(context.Context, smartweb.RoleAddForm) (smartweb.RoleVO, *coreerrors.Error)
	UpdateRole(context.Context, smartweb.RoleUpdateForm) *coreerrors.Error
	DeleteRole(context.Context, uint64) *coreerrors.Error
	CurrentOrganization() smartweb.Organization
	RoleDefinitions(context.Context) ([]smartweb.RoleVO, *coreerrors.Error)
	RoleMenuSelection(context.Context, uint64) (smartweb.RoleMenuSelectionVO, *coreerrors.Error)
	RoleDataScopes(context.Context) ([]smartweb.RoleDataScopeVO, *coreerrors.Error)
	RoleDataScopeSelection(context.Context, uint64) ([]smartweb.RoleDataScopeVO, *coreerrors.Error)
	RoleMembers(context.Context, uint64, uint64, uint64, string) (smartweb.RoleEmployeePageVO, *coreerrors.Error)
	TenantEmployees(context.Context, uint64, uint64, string) (smartweb.RoleEmployeePageVO, *coreerrors.Error)
	UpdateRoleMenu(context.Context, uint64, []uint64, string) *coreerrors.Error
	UpdateRoleDataScopes(context.Context, uint64, []smartweb.RoleDataScopeUpdateItem, string) *coreerrors.Error
	AddRoleMembers(context.Context, uint64, []uint64, string) *coreerrors.Error
	RemoveRoleMembers(context.Context, uint64, []uint64, string) *coreerrors.Error
}

type ServiceContext struct {
	mu             sync.RWMutex
	Config         config.Config
	MQ             mq.Broker
	Authorizer     *authz.Authorizer
	Project        *app.ProjectService
	Policy         *authz.PolicyService
	RBAC           *app.RBACService
	UserAdmin      *app.UserAdminService
	Authentication AuthenticationService
	AdminSession   AdminSessionService
	Navigation     NavigationService
	SmartWeb       SmartWebService
	Files          *app.FileService
	OwnPassword    *app.OwnPasswordChange
	TwoFactor      *twofactor.Service
	Dict           *app.DictService
	ConfigParam    *app.ConfigParamService
	TableColumn    *app.TableColumnService
	SerialNumber   *app.SerialNumberService
	Message        *app.MessageService
	Feedback       *app.FeedbackService
	HelpDoc        *app.HelpDocService
	LoginProtect   *app.LoginProtectionService
	OperateLog     *app.OperateLogService
	CacheConsole   *app.CacheConsoleService
	Reload         *app.ReloadService
	Job            *app.JobService
	ChangeLog      *app.ChangeLogService
	HeartBeat      *app.HeartBeatService
	DataTracer     *app.DataTracerService
	NoticeType     *app.NoticeTypeService
	Notice         *app.NoticeService
	rpcClients     []RPCMuxDiagnosisClient
}

func NewServiceContext(c config.Config, brokers ...mq.Broker) *ServiceContext {
	var broker mq.Broker
	if len(brokers) > 0 {
		broker = brokers[0]
	}
	return &ServiceContext{Config: c, MQ: broker}
}

func (s *ServiceContext) SetAuthorizer(authorizer *authz.Authorizer) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Authorizer = authorizer
}

func (s *ServiceContext) CurrentAuthorizer() *authz.Authorizer {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Authorizer
}

func (s *ServiceContext) SetProjectService(projectService *app.ProjectService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Project = projectService
}

func (s *ServiceContext) CurrentProjectService() *app.ProjectService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Project
}

func (s *ServiceContext) SetPolicyService(policyService *authz.PolicyService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Policy = policyService
}

func (s *ServiceContext) CurrentPolicyService() *authz.PolicyService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Policy
}

func (s *ServiceContext) SetRBACService(rbacService *app.RBACService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RBAC = rbacService
}

func (s *ServiceContext) CurrentRBACService() *app.RBACService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.RBAC
}

func (s *ServiceContext) SetUserAdminService(userAdminService *app.UserAdminService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UserAdmin = userAdminService
}

func (s *ServiceContext) CurrentUserAdminService() *app.UserAdminService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.UserAdmin
}

func (s *ServiceContext) SetAuthenticationService(authenticationService AuthenticationService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Authentication = authenticationService
}

func (s *ServiceContext) CurrentAuthenticationService() AuthenticationService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Authentication
}

func (s *ServiceContext) SetAdminSessionService(adminSessionService AdminSessionService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AdminSession = adminSessionService
}

func (s *ServiceContext) CurrentAdminSessionService() AdminSessionService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AdminSession
}

func (s *ServiceContext) SetNavigationService(navigationService NavigationService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Navigation = navigationService
}

func (s *ServiceContext) CurrentNavigationService() NavigationService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Navigation
}

func (s *ServiceContext) SetSmartWebService(smartWebService SmartWebService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SmartWeb = smartWebService
}

func (s *ServiceContext) CurrentSmartWebService() SmartWebService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SmartWeb
}

func (s *ServiceContext) UpdateConfig(c config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config = c
}

func (s *ServiceContext) RegisterRPCClient(client RPCMuxDiagnosisClient) func() {
	if s == nil || client == nil {
		return func() {}
	}
	s.mu.Lock()
	s.rpcClients = append(s.rpcClients, client)
	s.mu.Unlock()
	return func() {
		s.UnregisterRPCClient(client)
	}
}

func (s *ServiceContext) UnregisterRPCClient(client RPCMuxDiagnosisClient) {
	if s == nil || client == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, registered := range s.rpcClients {
		if registered == client {
			s.rpcClients = append(s.rpcClients[:index], s.rpcClients[index+1:]...)
			return
		}
	}
}

func (s *ServiceContext) UpdateRPCMuxDiagnosisExporters(exporter rpc.RPCMuxDiagnosisEventExporter, filter rpc.RPCMuxDiagnosisFilter) {
	if s == nil {
		return
	}
	s.mu.RLock()
	clients := append([]RPCMuxDiagnosisClient(nil), s.rpcClients...)
	s.mu.RUnlock()
	for _, client := range clients {
		client.UpdateMuxDiagnosisEventExporter(exporter, filter)
	}
}

func (s *ServiceContext) CurrentConfig() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}

// SetFileService wires the tenant file use case. It is optional: file
// compatibility routes return unavailable when it has not been configured.
func (s *ServiceContext) SetFileService(fileService *app.FileService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Files = fileService
}

// CurrentFileService returns the wired file service, if any.
func (s *ServiceContext) CurrentFileService() *app.FileService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Files
}

// SetOwnPasswordService wires the self-service credential change use case.
func (s *ServiceContext) SetOwnPasswordService(service *app.OwnPasswordChange) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OwnPassword = service
}

// CurrentOwnPasswordService returns the wired self-service password service.
func (s *ServiceContext) CurrentOwnPasswordService() *app.OwnPasswordChange {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.OwnPassword
}

// SetTwoFactorService wires the email-code second factor. It is optional;
// public login endpoints report the feature disabled when not wired.
func (s *ServiceContext) SetTwoFactorService(service *twofactor.Service) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TwoFactor = service
}

// CurrentTwoFactorService returns the wired 2FA service, if enabled.
func (s *ServiceContext) CurrentTwoFactorService() *twofactor.Service {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TwoFactor
}

// SetDictService wires the global data-dictionary use case.
func (s *ServiceContext) SetDictService(service *app.DictService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Dict = service
}

// CurrentDictService returns the wired dictionary service, if any.
func (s *ServiceContext) CurrentDictService() *app.DictService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Dict
}

// SetConfigService wires the global runtime parameter use case.
func (s *ServiceContext) SetConfigService(service *app.ConfigParamService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ConfigParam = service
}

// CurrentConfigService returns the wired parameter service, if any.
func (s *ServiceContext) CurrentConfigService() *app.ConfigParamService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ConfigParam
}

// SetTableColumnService wires the per-user table layout service.
func (s *ServiceContext) SetTableColumnService(service *app.TableColumnService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TableColumn = service
}

// CurrentTableColumnService returns the wired table column service, if any.
func (s *ServiceContext) CurrentTableColumnService() *app.TableColumnService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TableColumn
}

// SetSerialNumberService wires the DB-lock serial number generator.
func (s *ServiceContext) SetSerialNumberService(service *app.SerialNumberService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SerialNumber = service
}

// CurrentSerialNumberService returns the wired serial number service.
func (s *ServiceContext) CurrentSerialNumberService() *app.SerialNumberService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SerialNumber
}

// SetMessageService wires the in-app message service.
func (s *ServiceContext) SetMessageService(service *app.MessageService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Message = service
}

// CurrentMessageService returns the wired in-app message service.
func (s *ServiceContext) CurrentMessageService() *app.MessageService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Message
}

// SetFeedbackService wires the employee feedback service.
func (s *ServiceContext) SetFeedbackService(service *app.FeedbackService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Feedback = service
}

// CurrentFeedbackService returns the wired feedback service.
func (s *ServiceContext) CurrentFeedbackService() *app.FeedbackService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Feedback
}

// SetHelpDocService wires the help document service.
func (s *ServiceContext) SetHelpDocService(service *app.HelpDocService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HelpDoc = service
}

// CurrentHelpDocService returns the wired help document service.
func (s *ServiceContext) CurrentHelpDocService() *app.HelpDocService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.HelpDoc
}

// SetLoginProtectionService wires the login protection service.
func (s *ServiceContext) SetLoginProtectionService(service *app.LoginProtectionService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LoginProtect = service
}

// CurrentLoginProtectionService returns the wired login protection service.
func (s *ServiceContext) CurrentLoginProtectionService() *app.LoginProtectionService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LoginProtect
}

// SetOperateLogService wires the operation audit service.
func (s *ServiceContext) SetOperateLogService(service *app.OperateLogService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OperateLog = service
}

// CurrentOperateLogService returns the wired operation audit service.
func (s *ServiceContext) CurrentOperateLogService() *app.OperateLogService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.OperateLog
}

// SetCacheConsoleService wires the cache console service.
func (s *ServiceContext) SetCacheConsoleService(service *app.CacheConsoleService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CacheConsole = service
}

// CurrentCacheConsoleService returns the wired cache console service.
func (s *ServiceContext) CurrentCacheConsoleService() *app.CacheConsoleService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CacheConsole
}

// SetReloadService wires the SmartReload heartbeat service.
func (s *ServiceContext) SetReloadService(service *app.ReloadService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Reload = service
}

// CurrentReloadService returns the wired SmartReload heartbeat service.
func (s *ServiceContext) CurrentReloadService() *app.ReloadService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Reload
}

// SetJobService wires the scheduled job service.
func (s *ServiceContext) SetJobService(service *app.JobService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Job = service
}

// CurrentJobService returns the wired scheduled job service.
func (s *ServiceContext) CurrentJobService() *app.JobService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Job
}

// SetChangeLogService wires the change log service.
func (s *ServiceContext) SetChangeLogService(service *app.ChangeLogService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ChangeLog = service
}

// CurrentChangeLogService returns the wired change log service.
func (s *ServiceContext) CurrentChangeLogService() *app.ChangeLogService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ChangeLog
}

// SetHeartBeatService wires the heartbeat service.
func (s *ServiceContext) SetHeartBeatService(service *app.HeartBeatService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HeartBeat = service
}

// CurrentHeartBeatService returns the wired heartbeat service.
func (s *ServiceContext) CurrentHeartBeatService() *app.HeartBeatService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.HeartBeat
}

// SetDataTracerService wires the data tracer service.
func (s *ServiceContext) SetDataTracerService(service *app.DataTracerService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DataTracer = service
}

// CurrentDataTracerService returns the wired data tracer service.
func (s *ServiceContext) CurrentDataTracerService() *app.DataTracerService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.DataTracer
}

// SetNoticeTypeService wires the notice type service.
func (s *ServiceContext) SetNoticeTypeService(service *app.NoticeTypeService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.NoticeType = service
}

// CurrentNoticeTypeService returns the wired notice type service.
func (s *ServiceContext) CurrentNoticeTypeService() *app.NoticeTypeService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.NoticeType
}

// SetNoticeService wires the notice service.
func (s *ServiceContext) SetNoticeService(service *app.NoticeService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Notice = service
}

// CurrentNoticeService returns the wired notice service.
func (s *ServiceContext) CurrentNoticeService() *app.NoticeService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Notice
}
