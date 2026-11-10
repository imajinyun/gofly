// Package smartnotice exposes the unmodified SmartAdmin OA notice board
// contracts (/oa/noticeType/** and /oa/notice/**): type management, the
// admin console CRUD, and the employee board with per-user view records.
package smartnotice

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func typeServiceOf(svcCtx *svc.ServiceContext) *app.NoticeTypeService {
	return svcCtx.CurrentNoticeTypeService()
}

func noticeServiceOf(svcCtx *svc.ServiceContext) *app.NoticeService {
	return svcCtx.CurrentNoticeService()
}

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

// viewer resolves the authenticated subject into a visibility scope.
func viewer(ctx *rest.Context, svcCtx *svc.ServiceContext) (app.IdentityScope, bool) {
	principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
	if !ok || strings.TrimSpace(principal.Subject) == "" {
		return app.IdentityScope{}, false
	}
	scope, err := noticeServiceOf(svcCtx).ResolveViewer(ctx.Request.Context(), principal.TenantID, principal.Subject)
	if err != nil || scope.UserID == 0 {
		return app.IdentityScope{}, false
	}
	scope.Admin = principal.Realm == authn.RealmAdmin
	return scope, true
}

func unauthorized(ctx *rest.Context) {
	ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
}

// ---------------- types ----------------

// GetAllTypesHandler serves GET /oa/noticeType/getAll.
func GetAllTypesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		types, err := typeServiceOf(svcCtx).ListAll(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(types))
	}
}

// AddTypeHandler serves GET /oa/noticeType/add/{name}.
func AddTypeHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if err := typeServiceOf(svcCtx).Add(ctx.Request.Context(), ctx.PathValue("name")); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// UpdateTypeHandler serves GET /oa/noticeType/update/{id}/{name}.
func UpdateTypeHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("noticeTypeId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "类型id错误"))
			return
		}
		if serviceErr := typeServiceOf(svcCtx).Update(ctx.Request.Context(), id, ctx.PathValue("name")); serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// DeleteTypeHandler serves GET /oa/noticeType/delete/{id}.
func DeleteTypeHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("noticeTypeId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "类型id错误"))
			return
		}
		if serviceErr := typeServiceOf(svcCtx).Delete(ctx.Request.Context(), id); serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// ---------------- admin notices ----------------

type noticeQueryForm struct {
	NoticeTypeID     *uint64 `json:"noticeTypeId"`
	Keywords         string  `json:"keywords"`
	DocumentNumber   string  `json:"documentNumber"`
	CreateUserName   string  `json:"createUserName"`
	DeletedFlag      *bool   `json:"deletedFlag"`
	CreateTimeBegin  string  `json:"createTimeBegin"`
	CreateTimeEnd    string  `json:"createTimeEnd"`
	PublishTimeBegin string  `json:"publishTimeBegin"`
	PublishTimeEnd   string  `json:"publishTimeEnd"`
	PageNum          uint64  `json:"pageNum"`
	PageSize         uint64  `json:"pageSize"`
}

type visibleRangeForm struct {
	DataType int    `json:"dataType"`
	DataID   uint64 `json:"dataId"`
}

type noticeForm struct {
	NoticeID             uint64             `json:"noticeId"`
	Title                string             `json:"title"`
	NoticeTypeID         uint64             `json:"noticeTypeId"`
	AllVisibleFlag       *bool              `json:"allVisibleFlag"`
	ScheduledPublishFlag *bool              `json:"scheduledPublishFlag"`
	PublishTime          string             `json:"publishTime"`
	ContentText          string             `json:"contentText"`
	ContentHTML          string             `json:"contentHtml"`
	Attachment           []string           `json:"attachment"`
	Author               string             `json:"author"`
	Source               string             `json:"source"`
	DocumentNumber       string             `json:"documentNumber"`
	VisibleRangeList     []visibleRangeForm `json:"visibleRangeList"`
}

func toNoticeInput(form noticeForm, scope app.IdentityScope) app.NoticeInput {
	ranges := make([]app.NoticeVisibleRange, 0, len(form.VisibleRangeList))
	for _, entry := range form.VisibleRangeList {
		ranges = append(ranges, app.NoticeVisibleRange{DataType: entry.DataType, DataID: entry.DataID})
	}
	return app.NoticeInput{
		ID: form.NoticeID, Title: form.Title, TypeID: form.NoticeTypeID,
		AllVisibleFlag: form.AllVisibleFlag, ScheduledPublishFlag: form.ScheduledPublishFlag,
		PublishTime: form.PublishTime, ContentText: form.ContentText, ContentHTML: form.ContentHTML,
		Attachment: form.Attachment, Author: form.Author, Source: form.Source,
		DocumentNumber: form.DocumentNumber,
		CreateUserID:   scope.UserID, CreateUserName: scope.DisplayName,
		VisibleRangeList: ranges,
	}
}

// QueryHandler serves POST /oa/notice/query.
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form noticeQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		page, err := noticeServiceOf(svcCtx).List(ctx.Request.Context(), app.NoticeQuery{
			TypeID: form.NoticeTypeID, Keywords: form.Keywords, DocumentNumber: form.DocumentNumber,
			CreateUserName: form.CreateUserName, DeletedFlag: form.DeletedFlag != nil && *form.DeletedFlag,
			CreateTimeBegin: form.CreateTimeBegin, CreateTimeEnd: form.CreateTimeEnd,
			PublishBegin: form.PublishTimeBegin, PublishEnd: form.PublishTimeEnd,
			PageNum: form.PageNum, PageSize: form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// AddHandler serves POST /oa/notice/add.
func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form noticeForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		scope, ok := viewer(ctx, svcCtx)
		if !ok {
			unauthorized(ctx)
			return
		}
		if err := noticeServiceOf(svcCtx).Add(ctx.Request.Context(), toNoticeInput(form, scope)); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// UpdateHandler serves POST /oa/notice/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form noticeForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		scope, ok := viewer(ctx, svcCtx)
		if !ok {
			unauthorized(ctx)
			return
		}
		if err := noticeServiceOf(svcCtx).Update(ctx.Request.Context(), toNoticeInput(form, scope)); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// UpdateFormHandler serves GET /oa/notice/getUpdateVO/{id}.
func UpdateFormHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("noticeId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "通知id错误"))
			return
		}
		detail, serviceErr := noticeServiceOf(svcCtx).Detail(ctx.Request.Context(), id)
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(detail))
	}
}

// DeleteHandler serves GET /oa/notice/delete/{id}.
func DeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("noticeId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "通知id错误"))
			return
		}
		if serviceErr := noticeServiceOf(svcCtx).Delete(ctx.Request.Context(), id); serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// ---------------- employee board ----------------

type employeeNoticeQueryForm struct {
	Keywords         string  `json:"keywords"`
	NoticeTypeID     *uint64 `json:"noticeTypeId"`
	PublishTimeBegin string  `json:"publishTimeBegin"`
	PublishTimeEnd   string  `json:"publishTimeEnd"`
	NotViewFlag      *bool   `json:"notViewFlag"`
	PageNum          uint64  `json:"pageNum"`
	PageSize         uint64  `json:"pageSize"`
}

// EmployeeQueryHandler serves POST /oa/notice/employee/query.
func EmployeeQueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form employeeNoticeQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		scope, ok := viewer(ctx, svcCtx)
		if !ok {
			unauthorized(ctx)
			return
		}
		page, err := noticeServiceOf(svcCtx).ListVisible(ctx.Request.Context(), scope, app.EmployeeNoticeQuery{
			Keywords: form.Keywords, TypeID: form.NoticeTypeID,
			PublishBegin: form.PublishTimeBegin, PublishEnd: form.PublishTimeEnd,
			NotViewFlag: form.NotViewFlag, PageNum: form.PageNum, PageSize: form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// ViewHandler serves GET /oa/notice/employee/view/{noticeId}.
func ViewHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("noticeId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "通知id错误"))
			return
		}
		scope, ok := viewer(ctx, svcCtx)
		if !ok {
			unauthorized(ctx)
			return
		}
		detail, serviceErr := noticeServiceOf(svcCtx).View(
			ctx.Request.Context(), scope, id,
			clientIP(ctx), userAgent(ctx))
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(detail))
	}
}

type viewRecordQueryForm struct {
	NoticeID     uint64  `json:"noticeId"`
	DepartmentID *uint64 `json:"departmentId"`
	Keywords     string  `json:"keywords"`
	PageNum      uint64  `json:"pageNum"`
	PageSize     uint64  `json:"pageSize"`
}

// ViewRecordHandler serves POST /oa/notice/employee/queryViewRecord.
func ViewRecordHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form viewRecordQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		if !ok {
			unauthorized(ctx)
			return
		}
		page, err := noticeServiceOf(svcCtx).ListViewRecords(ctx.Request.Context(), app.ViewRecordQuery{
			TenantID: principal.TenantID,
			NoticeID: form.NoticeID, DepartmentID: form.DepartmentID, Keywords: form.Keywords,
			PageNum: form.PageNum, PageSize: form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

func clientIP(ctx *rest.Context) string {
	if forwarded := strings.TrimSpace(ctx.Request.Header.Get("X-Forwarded-For")); forwarded != "" {
		if comma := strings.IndexByte(forwarded, ','); comma > 0 {
			return strings.TrimSpace(forwarded[:comma])
		}
		return forwarded
	}
	if realIP := strings.TrimSpace(ctx.Request.Header.Get("X-Real-Ip")); realIP != "" {
		return realIP
	}
	host := ctx.Request.RemoteAddr
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(parsed, "[]")
	}
	return strings.Trim(host, "[]")
}

func userAgent(ctx *rest.Context) string {
	return ctx.Request.UserAgent()
}
