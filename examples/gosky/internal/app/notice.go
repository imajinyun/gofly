package app

import (
	"context"
	"strings"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Visible-range data types.
const (
	NoticeRangeEmployee   = 1
	NoticeRangeDepartment = 2
)

// NoticeType is a notice category.
type NoticeType struct {
	ID        uint64    `json:"noticeTypeId"`
	Name      string    `json:"noticeTypeName"`
	CreatedAt time.Time `json:"createTime"`
	UpdatedAt time.Time `json:"updateTime"`
}

// Notice is one OA notice row.
type Notice struct {
	ID                   uint64    `json:"noticeId"`
	TypeID               uint64    `json:"noticeTypeId"`
	TypeName             string    `json:"noticeTypeName"`
	Title                string    `json:"title"`
	AllVisibleFlag       bool      `json:"allVisibleFlag"`
	ScheduledPublishFlag bool      `json:"scheduledPublishFlag"`
	PublishFlag          bool      `json:"publishFlag"`
	PublishTime          time.Time `json:"publishTime"`
	Author               string    `json:"author"`
	Source               string    `json:"source"`
	DocumentNumber       string    `json:"documentNumber"`
	PageViewCount        int       `json:"pageViewCount"`
	UserViewCount        int       `json:"userViewCount"`
	DeletedFlag          bool      `json:"deletedFlag"`
	CreateUserID         *uint64   `json:"createUserId"`
	CreateUserName       string    `json:"createUserName"`
	CreatedAt            time.Time `json:"createTime"`
	UpdatedAt            time.Time `json:"updateTime"`
}

// NoticeDetail adds content and visibility ranges to a notice.
type NoticeDetail struct {
	Notice
	ContentText      string               `json:"contentText"`
	ContentHTML      string               `json:"contentHtml"`
	Attachment       []string             `json:"attachment"`
	VisibleRangeList []NoticeVisibleRange `json:"visibleRangeList"`
}

// NoticeVisibleRange is one employee/department scope entry.
type NoticeVisibleRange struct {
	DataType int    `json:"dataType"`
	DataID   uint64 `json:"dataId"`
	DataName string `json:"dataName"`
}

// NoticePage is the {list,total} envelope.
type NoticePage struct {
	List  []Notice `json:"list"`
	Total int      `json:"total"`
}

// NoticeViewRecord is one per-user view row.
type NoticeViewRecord struct {
	UserID         uint64    `json:"employeeId"`
	UserName       string    `json:"employeeName"`
	DepartmentName string    `json:"departmentName"`
	PageViewCount  int       `json:"pageViewCount"`
	FirstIP        string    `json:"firstIp"`
	FirstUserAgent string    `json:"firstUserAgent"`
	CreatedAt      time.Time `json:"createTime"`
	LastIP         string    `json:"lastIp"`
	LastUserAgent  string    `json:"lastUserAgent"`
	UpdatedAt      time.Time `json:"updateTime"`
}

// NoticeViewRecordPage is the {list,total} envelope.
type NoticeViewRecordPage struct {
	List  []NoticeViewRecord `json:"list"`
	Total int                `json:"total"`
}

// NoticeInput is the add/update payload.
type NoticeInput struct {
	ID                   uint64               `json:"noticeId"`
	Title                string               `json:"title"`
	TypeID               uint64               `json:"noticeTypeId"`
	AllVisibleFlag       *bool                `json:"allVisibleFlag"`
	ScheduledPublishFlag *bool                `json:"scheduledPublishFlag"`
	PublishTime          string               `json:"publishTime"`
	ContentText          string               `json:"contentText"`
	ContentHTML          string               `json:"contentHtml"`
	Attachment           []string             `json:"attachment"`
	Author               string               `json:"author"`
	Source               string               `json:"source"`
	DocumentNumber       string               `json:"documentNumber"`
	CreateUserID         uint64               `json:"-"`
	CreateUserName       string               `json:"-"`
	VisibleRangeList     []NoticeVisibleRange `json:"visibleRangeList"`
}

// NoticeQuery carries console filters.
type NoticeQuery struct {
	TypeID          *uint64
	Keywords        string
	DocumentNumber  string
	CreateUserName  string
	DeletedFlag     bool
	CreateTimeBegin string
	CreateTimeEnd   string
	PublishBegin    string
	PublishEnd      string
	PageNum         uint64
	PageSize        uint64
}

// EmployeeNoticeQuery carries employee-board filters.
type EmployeeNoticeQuery struct {
	Keywords     string
	TypeID       *uint64
	PublishBegin string
	PublishEnd   string
	NotViewFlag  *bool
	PageNum      uint64
	PageSize     uint64
}

// ViewRecordQuery carries view-record console filters.
type ViewRecordQuery struct {
	TenantID     uint64
	NoticeID     uint64
	DepartmentID *uint64
	Keywords     string
	PageNum      uint64
	PageSize     uint64
}

// NoticeStore is the persistence boundary.
type NoticeStore interface {
	// types
	ListTypes(ctx context.Context) ([]NoticeType, error)
	AddType(ctx context.Context, name string) (uint64, error)
	UpdateType(ctx context.Context, id uint64, name string) (bool, error)
	DeleteType(ctx context.Context, id uint64) (bool, error)
	TypeInUse(ctx context.Context, id uint64) (bool, error)

	// admin notices
	List(ctx context.Context, query NoticeQuery) (NoticePage, error)
	Get(ctx context.Context, id uint64) (NoticeDetail, error)
	Insert(ctx context.Context, input NoticeInput, publishAt time.Time) (uint64, error)
	Update(ctx context.Context, input NoticeInput, publishAt time.Time) (bool, error)
	SoftDelete(ctx context.Context, id uint64) error

	// employee board
	ResolveViewer(ctx context.Context, tenantID uint64, subject string) (IdentityScope, error)
	ListVisible(ctx context.Context, viewer IdentityScope, query EmployeeNoticeQuery) (NoticePage, error)

	// views
	RecordView(ctx context.Context, noticeID, userID uint64, ip, userAgent string) (firstView bool, pageDelta, userDelta int, err error)
	ListViewRecords(ctx context.Context, query ViewRecordQuery) (NoticeViewRecordPage, error)
}

// IdentityScope describes the viewer for visibility checks.
type IdentityScope struct {
	UserID       uint64
	DepartmentID uint64
	DisplayName  string
	Admin        bool
}

// NoticeTypeService manages notice categories.
type NoticeTypeService struct {
	store      NoticeStore
	authorizer RBACAuthorizer
}

// NewNoticeTypeService constructs the type service.
func NewNoticeTypeService(store NoticeStore, authorizer RBACAuthorizer) *NoticeTypeService {
	return &NoticeTypeService{store: store, authorizer: authorizer}
}

// ListAll returns every type. Employee board needs read access too.
func (s *NoticeTypeService) ListAll(ctx context.Context) ([]NoticeType, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeRead); err != nil {
		return nil, err
	}
	types, storeErr := s.store.ListTypes(ctx)
	if storeErr != nil {
		return nil, coreerrors.Wrap(coreerrors.CodeUnavailable, "list notice types", storeErr)
	}
	return types, nil
}

// Add creates a type.
func (s *NoticeTypeService) Add(ctx context.Context, name string) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeManage); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "类型名称不能为空且最多100字符")
	}
	if _, storeErr := s.store.AddType(ctx, name); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "add notice type", storeErr)
	}
	return nil
}

// Update renames a type.
func (s *NoticeTypeService) Update(ctx context.Context, id uint64, name string) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeManage); err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "类型id不能为空")
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "类型名称不能为空且最多100字符")
	}
	updated, storeErr := s.store.UpdateType(ctx, id, name)
	if storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "update notice type", storeErr)
	}
	if !updated {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "数据不存在")
	}
	return nil
}

// Delete removes an unused type.
func (s *NoticeTypeService) Delete(ctx context.Context, id uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeManage); err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "类型id不能为空")
	}
	inUse, storeErr := s.store.TypeInUse(ctx, id)
	if storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "check notice type", storeErr)
	}
	if inUse {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "该类型下存在通知公告，无法删除")
	}
	deleted, storeErr := s.store.DeleteType(ctx, id)
	if storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "delete notice type", storeErr)
	}
	if !deleted {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "数据不存在")
	}
	return nil
}

const noticeDateTimeLayout = "2006-01-02 15:04:05"

func validateNoticeInput(input NoticeInput, requireID bool) (time.Time, *coreerrors.Error) {
	if requireID && input.ID == 0 {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "通知id不能为空")
	}
	if strings.TrimSpace(input.Title) == "" || len([]rune(input.Title)) > 200 {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "标题不能为空且最多200字符")
	}
	if input.TypeID == 0 {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "分类不能为空")
	}
	if input.AllVisibleFlag == nil || input.ScheduledPublishFlag == nil {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "可见范围和定时发布标识不能为空")
	}
	if strings.TrimSpace(input.ContentText) == "" || strings.TrimSpace(input.ContentHTML) == "" {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "文本内容和html内容不能为空")
	}
	if strings.TrimSpace(input.Author) == "" {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "作者不能为空")
	}
	if strings.TrimSpace(input.Source) == "" {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "来源不能为空")
	}
	if len([]rune(input.DocumentNumber)) > 100 {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "文号最多100字符")
	}
	if !*input.AllVisibleFlag {
		if len(input.VisibleRangeList) == 0 {
			return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "非全部可见时必须设置可见范围")
		}
		for _, entry := range input.VisibleRangeList {
			if entry.DataType != NoticeRangeEmployee && entry.DataType != NoticeRangeDepartment {
				return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "可见范围数据类型错误")
			}
			if entry.DataID == 0 {
				return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "可见范围员工/部门id不能为空")
			}
		}
	}
	publishAt, err := time.ParseInLocation(noticeDateTimeLayout, strings.TrimSpace(input.PublishTime), time.Local)
	if err != nil {
		return time.Time{}, coreerrors.New(coreerrors.CodeInvalidArgument, "发布时间格式错误，应为 yyyy-MM-dd HH:mm:ss")
	}
	return publishAt, nil
}

// NoticeService is the admin/employee notice facade.
type NoticeService struct {
	store      NoticeStore
	authorizer RBACAuthorizer
	now        func() time.Time
}

// NewNoticeService constructs the notice service.
func NewNoticeService(store NoticeStore, authorizer RBACAuthorizer) *NoticeService {
	return &NoticeService{store: store, authorizer: authorizer, now: time.Now}
}

// List serves the admin console.
func (s *NoticeService) List(ctx context.Context, query NoticeQuery) (NoticePage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeRead); err != nil {
		return NoticePage{}, err
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.List(ctx, query)
	if storeErr != nil {
		return NoticePage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list notices", storeErr)
	}
	return page, nil
}

// Detail returns one notice for the management console.
func (s *NoticeService) Detail(ctx context.Context, id uint64) (NoticeDetail, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeRead); err != nil {
		return NoticeDetail{}, err
	}
	detail, storeErr := s.store.Get(ctx, id)
	if storeErr != nil {
		return NoticeDetail{}, mapNoticeError(storeErr)
	}
	if detail.DeletedFlag {
		return NoticeDetail{}, coreerrors.New(coreerrors.CodeInvalidArgument, "通知公告不存在")
	}
	return detail, nil
}

// Add creates a notice.
func (s *NoticeService) Add(ctx context.Context, input NoticeInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeManage); err != nil {
		return err
	}
	publishAt, verr := validateNoticeInput(input, false)
	if verr != nil {
		return verr
	}
	if _, storeErr := s.store.Insert(ctx, input, publishAt); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "add notice", storeErr)
	}
	return nil
}

// Update modifies a notice.
func (s *NoticeService) Update(ctx context.Context, input NoticeInput) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeManage); err != nil {
		return err
	}
	publishAt, verr := validateNoticeInput(input, true)
	if verr != nil {
		return verr
	}
	updated, storeErr := s.store.Update(ctx, input, publishAt)
	if storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "update notice", storeErr)
	}
	if !updated {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "通知公告不存在")
	}
	return nil
}

// Delete soft-deletes a notice.
func (s *NoticeService) Delete(ctx context.Context, id uint64) *coreerrors.Error {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeManage); err != nil {
		return err
	}
	if id == 0 {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "通知id不能为空")
	}
	if storeErr := s.store.SoftDelete(ctx, id); storeErr != nil {
		return coreerrors.Wrap(coreerrors.CodeUnavailable, "delete notice", storeErr)
	}
	return nil
}

// ResolveViewer maps an authenticated subject to the user/department scope
// used by the employee board visibility checks.
func (s *NoticeService) ResolveViewer(ctx context.Context, tenantID uint64, subject string) (IdentityScope, *coreerrors.Error) {
	scope, err := s.store.ResolveViewer(ctx, tenantID, subject)
	if err != nil {
		return IdentityScope{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "resolve notice viewer", err)
	}
	if scope.UserID == 0 {
		return IdentityScope{}, coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	return scope, nil
}

// ListVisible returns the employee board.
func (s *NoticeService) ListVisible(ctx context.Context, viewer IdentityScope, query EmployeeNoticeQuery) (NoticePage, *coreerrors.Error) {
	if viewer.UserID == 0 {
		return NoticePage{}, coreerrors.New(coreerrors.CodeUnauthenticated, "subject is required")
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListVisible(ctx, viewer, query)
	if storeErr != nil {
		return NoticePage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list visible notices", storeErr)
	}
	return page, nil
}

// View records an employee view and returns the detail.
func (s *NoticeService) View(ctx context.Context, viewer IdentityScope, id uint64, ip, userAgent string) (NoticeDetail, *coreerrors.Error) {
	if id == 0 {
		return NoticeDetail{}, coreerrors.New(coreerrors.CodeInvalidArgument, "通知id不能为空")
	}
	detail, storeErr := s.store.Get(ctx, id)
	if storeErr != nil {
		return NoticeDetail{}, mapNoticeError(storeErr)
	}
	if detail.DeletedFlag || !detail.PublishFlag {
		return NoticeDetail{}, coreerrors.New(coreerrors.CodeInvalidArgument, "通知公告不存在")
	}
	if !detail.AllVisibleFlag && !visibleTo(detail.VisibleRangeList, viewer) {
		return NoticeDetail{}, coreerrors.New(coreerrors.CodePermissionDenied, "对不起，您没有权限查看内容")
	}
	_, _, _, viewErr := s.store.RecordView(ctx, id, viewer.UserID, truncateRunes(ip, maxHelpDocViewerIPRunes), truncateRunes(userAgent, maxHelpDocViewerUARunes))
	if viewErr != nil {
		return NoticeDetail{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "record notice view", viewErr)
	}
	// Reload to pick up incremented counters.
	reloaded, storeErr := s.store.Get(ctx, id)
	if storeErr != nil {
		return NoticeDetail{}, mapNoticeError(storeErr)
	}
	return reloaded, nil
}

// ListViewRecords returns per-employee view statistics.
func (s *NoticeService) ListViewRecords(ctx context.Context, query ViewRecordQuery) (NoticeViewRecordPage, *coreerrors.Error) {
	if _, err := authorizeContext(ctx, s.authorizer, authz.CapabilityOANoticeRead); err != nil {
		return NoticeViewRecordPage{}, err
	}
	if query.NoticeID == 0 {
		return NoticeViewRecordPage{}, coreerrors.New(coreerrors.CodeInvalidArgument, "通知公告id不能为空")
	}
	query.PageNum, query.PageSize = NormalizePage(query.PageNum, query.PageSize)
	page, storeErr := s.store.ListViewRecords(ctx, query)
	if storeErr != nil {
		return NoticeViewRecordPage{}, coreerrors.Wrap(coreerrors.CodeUnavailable, "list view records", storeErr)
	}
	return page, nil
}

func visibleTo(ranges []NoticeVisibleRange, viewer IdentityScope) bool {
	if viewer.Admin {
		return true
	}
	for _, entry := range ranges {
		switch entry.DataType {
		case NoticeRangeEmployee:
			if entry.DataID == viewer.UserID {
				return true
			}
		case NoticeRangeDepartment:
			if entry.DataID == viewer.DepartmentID && viewer.DepartmentID != 0 {
				return true
			}
		}
	}
	return false
}

func mapNoticeError(err error) *coreerrors.Error {
	if err == ErrNoticeNotFound {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "通知公告不存在")
	}
	return coreerrors.Wrap(coreerrors.CodeUnavailable, "get notice", err)
}
