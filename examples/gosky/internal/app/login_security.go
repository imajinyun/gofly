package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Login result values mirror LOGIN_RESULT_ENUM consumed by the unchanged UI.
const (
	LoginResultSuccess = 0
	LoginResultFail    = 1
	LoginResultLogout  = 2

	// LoginUserTypeEmployee matches UserTypeEnum.ADMIN_EMPLOYEE for support
	// console sessions.
	LoginUserTypeEmployee = 1

	// Level3ProtectConfigKey is the support_configs key holding the JSON
	// level-3 protect configuration, matching Java ConfigKeyEnum.
	Level3ProtectConfigKey = "LEVEL3_PROTECT_CONFIG"

	// LoginFailLockedCode/LoginFailWillLockCode mirror UserErrorCode in the
	// unchanged client: 30010 locked now, 30011 lock pending after this fail.
	LoginFailLockedCode   = coreerrors.Code("30010")
	LoginFailWillLockCode = coreerrors.Code("30011")

	loginLockMessage = "您已连续登录失败%d次，账号锁定%d分钟，解锁时间为：%s，请您耐心等待！"
	loginFailMessage = "登录名或密码错误！连续登录失败%d次，账号将锁定%d分钟！您还可以再尝试%d次！"
	loginTimeLayout  = "2006-01-02 15:04:05"
)

// LoginLog is one SmartAdmin login audit row.
type LoginLog struct {
	ID            uint64    `json:"loginLogId"`
	UserID        *uint64   `json:"userId"`
	UserType      int       `json:"userType"`
	UserName      string    `json:"userName"`
	LoginName     string    `json:"-"`
	LoginIP       string    `json:"loginIp"`
	LoginIPRegion string    `json:"loginIpRegion"`
	UserAgent     string    `json:"userAgent"`
	Remark        string    `json:"remark"`
	LoginDevice   string    `json:"loginDevice"`
	LoginResult   int       `json:"loginResult"`
	CreatedAt     time.Time `json:"createTime"`
}

// LoginLogPage is the {list,total} envelope for login-log queries.
type LoginLogPage struct {
	List  []LoginLog `json:"list"`
	Total int        `json:"total"`
}

// LoginLogQuery carries the console filters.
type LoginLogQuery struct {
	UserID    *uint64
	UserType  *int
	UserName  string
	IP        string
	StartDate string
	EndDate   string
	PageNum   uint64
	PageSize  uint64
}

// LoginFail is the consecutive-failure lock state row.
type LoginFail struct {
	ID                 uint64     `json:"loginFailId"`
	UserID             uint64     `json:"userId"`
	UserType           int        `json:"userType"`
	LoginName          string     `json:"loginName"`
	LoginFailCount     int        `json:"loginFailCount"`
	LockFlag           int        `json:"lockFlag"`
	LoginLockBeginTime *time.Time `json:"loginLockBeginTime"`
	CreatedAt          time.Time  `json:"createTime"`
	UpdatedAt          time.Time  `json:"updateTime"`
}

// LoginFailPage is the {list,total} envelope for fail-state queries.
type LoginFailPage struct {
	List  []LoginFail `json:"list"`
	Total int         `json:"total"`
}

// LoginFailQuery carries the fail console filters.
type LoginFailQuery struct {
	LoginName          string
	LockFlag           *bool
	LockBeginTimeBegin string
	LockBeginTimeEnd   string
	PageNum            uint64
	PageSize           uint64
}

// Level3ProtectConfig is the JSON form persisted under LEVEL3_PROTECT_CONFIG.
// Only the login-failure fields are enforced; other fields are stored
// verbatim for the unchanged configuration page.
type Level3ProtectConfig struct {
	LoginFailMaxTimes                   *int   `json:"loginFailMaxTimes"`
	LoginFailLockMinutes                *int   `json:"loginFailLockMinutes"`
	LoginActiveTimeoutMinutes           *int   `json:"loginActiveTimeoutMinutes"`
	TwoFactorLoginEnabled               *bool  `json:"twoFactorLoginEnabled"`
	PasswordComplexityEnabled           *bool  `json:"passwordComplexityEnabled"`
	RegularChangePasswordMonths         *int   `json:"regularChangePasswordMonths"`
	RegularChangePasswordNotAllowRepeat *int   `json:"regularChangePasswordNotAllowRepeatTimes"`
	FileDetectFlag                      *bool  `json:"fileDetectFlag"`
	MaxUploadFileSizeMB                 *int64 `json:"maxUploadFileSizeMb"`
}

// LoginMeta carries transport metadata recorded with an attempt.
type LoginMeta struct {
	IP        string
	UserAgent string
	Device    string
	RequestID string
}

// LoginIdentity is the resolved active admin membership for a login name.
type LoginIdentity struct {
	UserID      uint64
	TenantID    uint64
	Subject     string
	LoginName   string
	DisplayName string
}

// LoginSecurityStore is the persistence boundary.
type LoginSecurityStore interface {
	ResolveLoginIdentity(ctx context.Context, loginName string) (LoginIdentity, bool, error)
	ResolveIdentityBySubject(ctx context.Context, tenantID uint64, subject string) (LoginIdentity, bool, error)
	InsertLoginLog(ctx context.Context, tenantID uint64, entry LoginLog) error
	ListLoginLogs(ctx context.Context, tenantID uint64, query LoginLogQuery) (LoginLogPage, error)

	GetFailState(ctx context.Context, tenantID, userID uint64) (LoginFail, bool, error)
	UpsertFailState(ctx context.Context, tenantID, userID uint64, state LoginFail) error
	DeleteFailState(ctx context.Context, tenantID, userID uint64) error
	ListLoginFails(ctx context.Context, tenantID uint64, query LoginFailQuery) (LoginFailPage, error)
	DeleteLoginFails(ctx context.Context, tenantID uint64, ids []uint64) error

	GetProtectConfig(ctx context.Context) (string, error)
	PutProtectConfig(ctx context.Context, raw string) error
}

// LoginProtectionService runs the SmartAdmin lock state machine, records
// login audit rows, and serves the support consoles. Audit-write failures
// never block an otherwise successful login.
type LoginProtectionService struct {
	store      LoginSecurityStore
	authorizer RBACAuthorizer
	now        func() time.Time
}

func NewLoginProtectionService(store LoginSecurityStore, authorizer RBACAuthorizer) *LoginProtectionService {
	return &LoginProtectionService{store: store, authorizer: authorizer, now: time.Now}
}

// protectSettings returns the effective fail-lock settings. Absent or
// incomplete configuration disables protection (maxTimes < 1).
func (s *LoginProtectionService) protectSettings(ctx context.Context) (maxTimes, lockMinutes int, err error) {
	raw, storeErr := s.store.GetProtectConfig(ctx)
	if storeErr != nil {
		return 0, 0, storeErr
	}
	cfg := DefaultLevel3ProtectConfig()
	if strings.TrimSpace(raw) != "" {
		if parseErr := jsonUnmarshalProtect(raw, &cfg); parseErr != nil {
			return 0, 0, parseErr
		}
	}
	if cfg.LoginFailMaxTimes == nil {
		return -1, 30, nil
	}
	lock := 30
	if cfg.LoginFailLockMinutes != nil && *cfg.LoginFailLockMinutes > 0 {
		lock = *cfg.LoginFailLockMinutes
	}
	return *cfg.LoginFailMaxTimes, lock, nil
}

// DefaultLevel3ProtectConfig returns the seeded defaults (protection off).
func DefaultLevel3ProtectConfig() Level3ProtectConfig {
	maxTimes := -1
	lockMinutes := 30
	activeTimeout := -1
	twoFactor := false
	passwordComplexity := true
	changeMonths := 3
	noRepeat := 3
	fileDetect := false
	var maxSize int64 = 50
	return Level3ProtectConfig{
		LoginFailMaxTimes:                   &maxTimes,
		LoginFailLockMinutes:                &lockMinutes,
		LoginActiveTimeoutMinutes:           &activeTimeout,
		TwoFactorLoginEnabled:               &twoFactor,
		PasswordComplexityEnabled:           &passwordComplexity,
		RegularChangePasswordMonths:         &changeMonths,
		RegularChangePasswordNotAllowRepeat: &noRepeat,
		FileDetectFlag:                      &fileDetect,
		MaxUploadFileSizeMB:                 &maxSize,
	}
}

// CheckLock rejects an attempt while the resolved account is inside its lock
// window. Unknown names and disabled protection pass through.
func (s *LoginProtectionService) CheckLock(ctx context.Context, loginName string) *coreerrors.Error {
	identity, found, err := s.store.ResolveLoginIdentity(ctx, loginName)
	if err != nil || !found {
		// Resolution failures fail closed for the login boundary; unknown
		// names continue to the credential check (enumeration-safe).
		if err != nil {
			return coreerrors.Wrap(coreerrors.CodeUnavailable, "resolve login identity", err)
		}
		return nil
	}
	maxTimes, lockMinutes, err := s.protectSettings(ctx)
	if err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "load protect config", err)
	}
	if maxTimes < 1 {
		return nil
	}
	state, found, err := s.store.GetFailState(ctx, identity.TenantID, identity.UserID)
	if err != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "load fail state", err)
	}
	if !found || state.LoginFailCount < maxTimes || state.LoginLockBeginTime == nil {
		return nil
	}
	now := s.now()
	unlockAt := state.LoginLockBeginTime.Add(time.Duration(lockMinutes) * time.Minute)
	if now.After(unlockAt) {
		return nil
	}
	return coreerrors.New(LoginFailLockedCode, fmt.Sprintf(loginLockMessage,
		state.LoginFailCount, lockMinutes, unlockAt.Format(loginTimeLayout)))
}

// RecordFailure increments the consecutive failure count for the resolved
// account and returns the user-facing hint (lock or remaining attempts).
// Unknown names and disabled protection yield an empty hint.
func (s *LoginProtectionService) RecordFailure(ctx context.Context, loginName string, meta LoginMeta) (string, *coreerrors.Error) {
	identity, found, err := s.store.ResolveLoginIdentity(ctx, loginName)
	if err != nil {
		return "", coreerrors.Wrap(coreerrors.CodeUnavailable, "resolve login identity", err)
	}
	s.writeLoginLog(ctx, identity, found, LoginResultFail, "密码错误", meta)
	if !found {
		return "", nil
	}
	maxTimes, lockMinutes, err := s.protectSettings(ctx)
	if err != nil {
		return "", coreerrors.Wrap(coreerrors.CodeUnavailable, "load protect config", err)
	}
	if maxTimes < 1 {
		return "", nil
	}
	state, _, err := s.store.GetFailState(ctx, identity.TenantID, identity.UserID)
	if err != nil {
		return "", coreerrors.Wrap(coreerrors.CodeUnavailable, "load fail state", err)
	}
	state.LoginFailCount++
	locked := state.LoginFailCount >= maxTimes
	state.LockFlag = 0
	var begin *time.Time
	if locked {
		t := s.now()
		begin = &t
		state.LockFlag = 1
	}
	state.LoginLockBeginTime = begin
	state.LoginName = identity.LoginName
	state.UserType = LoginUserTypeEmployee
	if err := s.store.UpsertFailState(ctx, identity.TenantID, identity.UserID, state); err != nil {
		return "", coreerrors.Wrap(coreerrors.CodeUnavailable, "record login failure", err)
	}
	if locked {
		return fmt.Sprintf(loginLockMessage, state.LoginFailCount, lockMinutes, begin.Add(time.Duration(lockMinutes)*time.Minute).Format(loginTimeLayout)), nil
	}
	return fmt.Sprintf(loginFailMessage, maxTimes, lockMinutes, maxTimes-state.LoginFailCount), nil
}

// RecordSuccess clears the failure state and writes the success audit row.
func (s *LoginProtectionService) RecordSuccess(ctx context.Context, loginName string, meta LoginMeta) {
	identity, found, err := s.store.ResolveLoginIdentity(ctx, loginName)
	if err != nil || !found {
		return
	}
	_ = s.store.DeleteFailState(ctx, identity.TenantID, identity.UserID)
	s.writeLoginLog(ctx, identity, true, LoginResultSuccess, "", meta)
}

// RecordLogout writes a logout audit row for the authenticated principal;
// the user id, login name and display name are resolved from the membership.
func (s *LoginProtectionService) RecordLogout(ctx context.Context, subject string, tenantID uint64, meta LoginMeta) {
	if strings.TrimSpace(subject) == "" {
		return
	}
	identity, found, err := s.store.ResolveIdentityBySubject(ctx, tenantID, subject)
	if err != nil || !found {
		return
	}
	userID := identity.UserID
	entry := LoginLog{
		UserID:      &userID,
		UserType:    LoginUserTypeEmployee,
		UserName:    identity.DisplayName,
		LoginName:   identity.LoginName,
		LoginIP:     sanitizeViewerField(meta.IP, maxHelpDocViewerIPRunes),
		UserAgent:   sanitizeViewerField(meta.UserAgent, maxHelpDocViewerUARunes),
		LoginDevice: sanitizeViewerField(meta.Device, 64),
		LoginResult: LoginResultLogout,
		CreatedAt:   s.now(),
	}
	_ = s.store.InsertLoginLog(ctx, tenantID, entry)
}

func (s *LoginProtectionService) writeLoginLog(ctx context.Context, identity LoginIdentity, found bool, result int, remark string, meta LoginMeta) {
	entry := LoginLog{
		UserType:    LoginUserTypeEmployee,
		UserName:    identity.DisplayName,
		LoginName:   identity.LoginName,
		LoginIP:     sanitizeViewerField(meta.IP, maxHelpDocViewerIPRunes),
		UserAgent:   sanitizeViewerField(meta.UserAgent, maxHelpDocViewerUARunes),
		LoginDevice: sanitizeViewerField(meta.Device, 64),
		Remark:      remark,
		LoginResult: result,
		CreatedAt:   s.now(),
	}
	tenantID := uint64(0)
	if found {
		id := identity.UserID
		entry.UserID = &id
		tenantID = identity.TenantID
	}
	_ = s.store.InsertLoginLog(ctx, tenantID, entry)
}

// ---------------- support console ----------------

// ListLogs serves the administrator login-log console.
func (s *LoginProtectionService) ListLogs(ctx context.Context, query LoginLogQuery) (LoginLogPage, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportLoginLogRead)
	if err != nil {
		return LoginLogPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListLoginLogs(ctx, tenantID, query)
	if storeErr != nil {
		return LoginLogPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list login logs", storeErr)
	}
	return page, nil
}

// ListOwnLogs serves the account-center tab scoped to one user.
func (s *LoginProtectionService) ListOwnLogs(ctx context.Context, principalSubject string, query LoginLogQuery) (LoginLogPage, *coreerrors.Error) {
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return LoginLogPage{}, err
	}
	subject := strings.TrimSpace(principalSubject)
	if subject == "" {
		return LoginLogPage{}, coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	identity, found, resolveErr := s.store.ResolveIdentityBySubject(ctx, tenantID, subject)
	if resolveErr != nil {
		return LoginLogPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "resolve login identity", resolveErr)
	}
	if !found {
		return LoginLogPage{List: make([]LoginLog, 0), Total: 0}, nil
	}
	userType := LoginUserTypeEmployee
	query.UserID = &identity.UserID
	query.UserType = &userType
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListLoginLogs(ctx, tenantID, query)
	if storeErr != nil {
		return LoginLogPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list login logs", storeErr)
	}
	return page, nil
}

// ListFails serves the lock-state console.
func (s *LoginProtectionService) ListFails(ctx context.Context, tenantID uint64, query LoginFailQuery) (LoginFailPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportLoginLogManage); err != nil {
		return LoginFailPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListLoginFails(ctx, tenantID, query)
	if storeErr != nil {
		return LoginFailPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list login fail states", storeErr)
	}
	return page, nil
}

// DeleteFails removes lock-state rows by id within the verified tenant.
func (s *LoginProtectionService) DeleteFails(ctx context.Context, ids []uint64) *coreerrors.Error {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportLoginLogManage)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 500 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "too many ids")
	}
	for _, id := range ids {
		if id == 0 {
			return coreerrors.New(coreerrors.CodeInvalidArgument, "login fail id is invalid")
		}
	}
	if storeErr := s.store.DeleteLoginFails(ctx, tenantID, ids); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "delete login fail states", storeErr)
	}
	return nil
}

// GetProtectConfig returns the raw level-3 configuration JSON string.
func (s *LoginProtectionService) GetProtectConfig(ctx context.Context) (string, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportLoginLogManage); err != nil {
		return "", err
	}
	raw, storeErr := s.store.GetProtectConfig(ctx)
	if storeErr != nil {
		return "", coreerrors.Wrap(coreerrors.CodeUnavailable, "load protect config", storeErr)
	}
	return raw, nil
}

// UpdateProtectConfig validates and persists the level-3 configuration JSON.
func (s *LoginProtectionService) UpdateProtectConfig(ctx context.Context, cfg Level3ProtectConfig) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportLoginLogManage); err != nil {
		return err
	}
	if cfg.LoginFailMaxTimes == nil || cfg.LoginFailLockMinutes == nil ||
		cfg.LoginActiveTimeoutMinutes == nil || cfg.TwoFactorLoginEnabled == nil ||
		cfg.PasswordComplexityEnabled == nil || cfg.RegularChangePasswordMonths == nil ||
		cfg.RegularChangePasswordNotAllowRepeat == nil || cfg.FileDetectFlag == nil || cfg.MaxUploadFileSizeMB == nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "三级等保配置不完整")
	}
	if *cfg.LoginFailLockMinutes < 1 || *cfg.LoginFailLockMinutes > 7*24*60 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "锁定时间必须在 1 分钟到 7 天之间")
	}
	if *cfg.MaxUploadFileSizeMB < 1 || *cfg.MaxUploadFileSizeMB > 10240 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "文件大小限制不合法")
	}
	raw, marshalErr := jsonMarshalProtect(cfg)
	if marshalErr != nil {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "protect config is invalid")
	}
	if storeErr := s.store.PutProtectConfig(ctx, raw); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "save protect config", storeErr)
	}
	return nil
}
