package smartdevtools

import (
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartcompat"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// ---------------- heartbeat ----------------

type heartbeatQueryForm struct {
	Keywords  string `json:"keywords"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	PageNum   uint64 `json:"pageNum"`
	PageSize  uint64 `json:"pageSize"`
}

// HeartBeatHandler serves POST /support/heartBeat/query.
func HeartBeatHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form heartbeatQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		service := svcCtx.CurrentHeartBeatService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		page, err := service.List(ctx.Request.Context(), app.HeartBeatQuery{
			Keywords:  form.Keywords,
			StartDate: form.StartDate,
			EndDate:   form.EndDate,
			PageNum:   form.PageNum,
			PageSize:  form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// ---------------- change log ----------------

type changeLogQueryForm struct {
	Type            *int   `json:"type"`
	Keyword         string `json:"keyword"`
	PublicDateBegin string `json:"publicDateBegin"`
	PublicDateEnd   string `json:"publicDateEnd"`
	PageNum         uint64 `json:"pageNum"`
	PageSize        uint64 `json:"pageSize"`
}

type changeLogForm struct {
	ChangeLogID   uint64 `json:"changeLogId"`
	UpdateVersion string `json:"updateVersion"`
	Type          int    `json:"type"`
	PublishAuthor string `json:"publishAuthor"`
	PublicDate    string `json:"publicDate"`
	Content       string `json:"content"`
	Link          string `json:"link"`
}

func changeLogServiceOf(svcCtx *svc.ServiceContext) *app.ChangeLogService {
	return svcCtx.CurrentChangeLogService()
}

// ChangeLogQueryHandler serves POST /support/changeLog/queryPage.
func ChangeLogQueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form changeLogQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		service := changeLogServiceOf(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		page, err := service.List(ctx.Request.Context(), app.ChangeLogQuery{
			Type:            form.Type,
			Keyword:         form.Keyword,
			PublicDateBegin: form.PublicDateBegin,
			PublicDateEnd:   form.PublicDateEnd,
			PageNum:         form.PageNum,
			PageSize:        form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// ChangeLogDetailHandler serves GET /support/changeLog/getDetail/{id}.
func ChangeLogDetailHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("changeLogId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "id错误"))
			return
		}
		service := changeLogServiceOf(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		row, serviceErr := service.Detail(ctx.Request.Context(), id)
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(row))
	}
}

// ChangeLogAddHandler serves POST /support/changeLog/add.
func ChangeLogAddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form changeLogForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		service := changeLogServiceOf(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		if err := service.Add(ctx.Request.Context(), toChangeLogInput(form)); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// ChangeLogUpdateHandler serves POST /support/changeLog/update.
func ChangeLogUpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form changeLogForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		service := changeLogServiceOf(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		if err := service.Update(ctx.Request.Context(), toChangeLogInput(form)); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// ChangeLogDeleteHandler serves GET /support/changeLog/delete/{id}.
func ChangeLogDeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("changeLogId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "id错误"))
			return
		}
		service := changeLogServiceOf(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		if serviceErr := service.Delete(ctx.Request.Context(), id); serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// ChangeLogBatchDeleteHandler serves POST /support/changeLog/batchDelete.
func ChangeLogBatchDeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		ids, err := smartcompat.BindUint64Array(ctx)
		if err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		service := changeLogServiceOf(svcCtx)
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		if err := service.DeleteBatch(ctx.Request.Context(), ids); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func toChangeLogInput(form changeLogForm) app.ChangeLogInput {
	return app.ChangeLogInput{
		ID:            form.ChangeLogID,
		UpdateVersion: form.UpdateVersion,
		Type:          form.Type,
		PublishAuthor: form.PublishAuthor,
		PublicDate:    form.PublicDate,
		Content:       form.Content,
		Link:          form.Link,
	}
}

// ---------------- data tracer ----------------

type dataTracerQueryForm struct {
	Type     *int   `json:"type"`
	DataID   uint64 `json:"dataId"`
	Keywords string `json:"keywords"`
	PageNum  uint64 `json:"pageNum"`
	PageSize uint64 `json:"pageSize"`
}

// DataTracerHandler serves POST /support/dataTracer/query.
func DataTracerHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form dataTracerQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		service := svcCtx.CurrentDataTracerService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		page, err := service.List(ctx.Request.Context(), app.DataTracerQuery{
			Type:     form.Type,
			DataID:   form.DataID,
			Keywords: form.Keywords,
			PageNum:  form.PageNum,
			PageSize: form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}
