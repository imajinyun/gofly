package app

import (
	"context"
	"errors"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Operate log payload bounds keep the audit table bounded even when a
// request or response carries an unexpectedly large body.
const (
	OperateUserTypeEmployee = 1
	maxOperateParamRunes    = 20000
	maxOperateResponseRunes = 8000
	maxOperateFailReason    = 1000
	maxOperateURLRunes      = 512
	maxOperateModuleRunes   = 120
	maxOperateContentRunes  = 255
)

// OperateLog is one administrator operation audit row.
type OperateLog struct {
	TenantID        uint64    `json:"-"`
	ID              uint64    `json:"operateLogId"`
	OperateUserID   *uint64   `json:"operateUserId"`
	OperateUserType int       `json:"operateUserType"`
	OperateUserName string    `json:"operateUserName"`
	Module          string    `json:"module"`
	Content         string    `json:"content"`
	URL             string    `json:"url"`
	Method          string    `json:"method"`
	Param           string    `json:"param"`
	Response        string    `json:"response"`
	IP              string    `json:"ip"`
	IPRegion        string    `json:"ipRegion"`
	UserAgent       string    `json:"userAgent"`
	SuccessFlag     bool      `json:"successFlag"`
	FailReason      string    `json:"failReason"`
	CreatedAt       time.Time `json:"createTime"`
	UpdatedAt       time.Time `json:"updateTime"`
}

// OperateLogPage is the {list,total} envelope for operate-log queries.
type OperateLogPage struct {
	List  []OperateLog `json:"list"`
	Total int          `json:"total"`
}

// OperateLogQuery carries the console filters.
type OperateLogQuery struct {
	OperateUserID   *uint64
	OperateUserType *int
	Keywords        string
	RequestKeywords string
	UserName        string
	SuccessFlag     *bool
	StartDate       string
	EndDate         string
	PageNum         uint64
	PageSize        uint64
}

// OperateLogEntry is the middleware-to-service write model. Subject is
// resolved to the operator's user id and display name when an identity
// resolver is wired.
type OperateLogEntry struct {
	TenantID    uint64
	Subject     string
	UserID      uint64
	UserName    string
	Module      string
	Content     string
	URL         string
	Method      string
	Param       string
	Response    string
	IP          string
	UserAgent   string
	SuccessFlag bool
	FailReason  string
}

// OperateLogStore is the persistence boundary.
type OperateLogStore interface {
	InsertOperateLog(ctx context.Context, entry OperateLog) error
	ListOperateLogs(ctx context.Context, tenantID uint64, query OperateLogQuery) (OperateLogPage, error)
	GetOperateLog(ctx context.Context, tenantID, id uint64) (OperateLog, error)
}

// OperateIdentityResolver resolves an operator's user id and display name from
// the authenticated subject inside a tenant.
type OperateIdentityResolver interface {
	ResolveIdentityBySubject(ctx context.Context, tenantID uint64, subject string) (LoginIdentity, bool, error)
}

// OperateLogService owns validation, authorization and payload bounding for
// operation audit rows. Writes are best-effort and never block business
// traffic.
type OperateLogService struct {
	store      OperateLogStore
	resolver   OperateIdentityResolver
	authorizer RBACAuthorizer
	now        func() time.Time
}

func NewOperateLogService(store OperateLogStore, authorizer RBACAuthorizer) *OperateLogService {
	return &OperateLogService{store: store, authorizer: authorizer, now: time.Now}
}

// SetIdentityResolver wires optional subject->operator resolution for the
// best-effort recorder.
func (s *OperateLogService) SetIdentityResolver(resolver OperateIdentityResolver) {
	if s == nil {
		return
	}
	s.resolver = resolver
}

// Record persists one row best-effort. Callers must not interpret an error
// as a business failure; the middleware logs and discards it.
func (s *OperateLogService) Record(ctx context.Context, input OperateLogEntry) error {
	if s == nil || s.store == nil {
		return nil
	}
	userID, userName := input.UserID, input.UserName
	if userID == 0 && input.Subject != "" && s.resolver != nil {
		if identity, found, err := s.resolver.ResolveIdentityBySubject(ctx, input.TenantID, input.Subject); err == nil && found {
			userID, userName = identity.UserID, identity.DisplayName
		}
	}
	entry := OperateLog{
		TenantID:        input.TenantID,
		OperateUserType: OperateUserTypeEmployee,
		OperateUserName: truncateRunes(strings.TrimSpace(userName), maxHelpDocViewerUARunes),
		Module:          truncateRunes(strings.TrimSpace(input.Module), maxOperateModuleRunes),
		Content:         truncateRunes(strings.TrimSpace(input.Content), maxOperateContentRunes),
		URL:             truncateRunes(input.URL, maxOperateURLRunes),
		Method:          input.Method,
		Param:           truncateRunes(input.Param, maxOperateParamRunes),
		Response:        truncateRunes(input.Response, maxOperateResponseRunes),
		IP:              truncateRunes(input.IP, maxHelpDocViewerIPRunes),
		UserAgent:       truncateRunes(sanitizeViewerField(input.UserAgent, maxHelpDocViewerUARunes), maxHelpDocViewerUARunes),
		SuccessFlag:     input.SuccessFlag,
		FailReason:      truncateRunes(input.FailReason, maxOperateFailReason),
		CreatedAt:       s.now(),
	}
	if userID != 0 {
		id := userID
		entry.OperateUserID = &id
	}
	return s.store.InsertOperateLog(ctx, entry)
}

// List serves the administrator operate-log console.
func (s *OperateLogService) List(ctx context.Context, query OperateLogQuery) (OperateLogPage, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportOperateLogRead)
	if err != nil {
		return OperateLogPage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListOperateLogs(ctx, tenantID, query)
	if storeErr != nil {
		return OperateLogPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list operate logs", storeErr)
	}
	return page, nil
}

// Detail returns one row within the verified tenant.
func (s *OperateLogService) Detail(ctx context.Context, id uint64) (OperateLog, *coreerrors.Error) {
	tenantID, err := authorizeContext(ctx, s.authorizer, authz.CapabilitySupportOperateLogRead)
	if err != nil {
		return OperateLog{}, err
	}
	if id == 0 {
		return OperateLog{}, coreerrors.New(coreerrors.CodeInvalidArgument, "operate log id is required")
	}
	row, storeErr := s.store.GetOperateLog(ctx, tenantID, id)
	if storeErr != nil {
		if errors.Is(storeErr, ErrOperateLogNotFound) {
			return OperateLog{}, coreerrors.New(coreerrors.CodeInvalidArgument, "数据不存在")
		}
		return OperateLog{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "get operate log", storeErr)
	}
	return row, nil
}

// ListOwn serves the account-center tab scoped to the authenticated subject.
func (s *OperateLogService) ListOwn(ctx context.Context, subject string, query OperateLogQuery) (OperateLogPage, *coreerrors.Error) {
	tenantID, err := tenantFromPrincipalOrReject(ctx)
	if err != nil {
		return OperateLogPage{}, err
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return OperateLogPage{}, coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	var userID uint64
	if s.resolver != nil {
		if identity, found, resolveErr := s.resolver.ResolveIdentityBySubject(ctx, tenantID, subject); resolveErr == nil && found {
			userID = identity.UserID
		}
	}
	userType := OperateUserTypeEmployee
	query.OperateUserID = &userID
	query.OperateUserType = &userType
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListOperateLogs(ctx, tenantID, query)
	if storeErr != nil {
		return OperateLogPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list operate logs", storeErr)
	}
	return page, nil
}

func truncateRunes(value string, max int) string {
	if runes := []rune(value); len(runes) > max {
		return string(runes[:max])
	}
	return value
}
