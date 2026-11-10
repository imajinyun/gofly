// Package smartjob exposes the unmodified SmartAdmin scheduled-job
// console contracts (support/job query, add, update, enabled, execute,
// delete and execution-log query).
package smartjob

import (
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

func serviceOf(svcCtx *svc.ServiceContext) *app.JobService {
	return svcCtx.CurrentJobService()
}

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

func operatorName(ctx *rest.Context) string {
	if principal, ok := authn.PrincipalFromContext(ctx.Request.Context()); ok {
		return principal.Subject
	}
	return ""
}

// ---------------- forms ----------------

type jobQueryForm struct {
	SearchWord  string `json:"searchWord"`
	TriggerType string `json:"triggerType"`
	EnabledFlag *bool  `json:"enabledFlag"`
	DeletedFlag *bool  `json:"deletedFlag"`
	PageNum     uint64 `json:"pageNum"`
	PageSize    uint64 `json:"pageSize"`
}

func (form jobQueryForm) toQuery() app.JobQuery {
	query := app.JobQuery{
		SearchWord:  form.SearchWord,
		TriggerType: form.TriggerType,
		EnabledFlag: form.EnabledFlag,
		PageNum:     form.PageNum,
		PageSize:    form.PageSize,
	}
	if form.DeletedFlag != nil {
		query.DeletedFlag = *form.DeletedFlag
	}
	return query
}

type jobForm struct {
	JobID        uint64 `json:"jobId"`
	JobName      string `json:"jobName"`
	JobClass     string `json:"jobClass"`
	TriggerType  string `json:"triggerType"`
	TriggerValue string `json:"triggerValue"`
	Param        string `json:"param"`
	EnabledFlag  *bool  `json:"enabledFlag"`
	Remark       string `json:"remark"`
	Sort         *int   `json:"sort"`
}

func (form jobForm) toInput(operator string) app.JobInput {
	return app.JobInput{
		ID:           form.JobID,
		JobName:      form.JobName,
		JobClass:     form.JobClass,
		TriggerType:  form.TriggerType,
		TriggerValue: form.TriggerValue,
		Param:        form.Param,
		EnabledFlag:  form.EnabledFlag,
		Remark:       form.Remark,
		Sort:         form.Sort,
		UpdateName:   operator,
	}
}

// ---------------- handlers ----------------

// QueryHandler serves POST /support/job/query.
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form jobQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.List(ctx.Request.Context(), form.toQuery())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// DetailHandler serves GET /support/job/{jobId}.
func DetailHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.PathValue("jobId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "任务id错误"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		job, serviceErr := service.Detail(ctx.Request.Context(), id)
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(job))
	}
}

// AddHandler serves POST /support/job/add.
func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form jobForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.Add(ctx.Request.Context(), form.toInput(operatorName(ctx))); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// UpdateHandler serves POST /support/job/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form jobForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if err := service.Update(ctx.Request.Context(), form.toInput(operatorName(ctx))); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

type enabledForm struct {
	JobID       uint64 `json:"jobId"`
	EnabledFlag bool   `json:"enabledFlag"`
}

// UpdateEnabledHandler serves POST /support/job/update/enabled.
func UpdateEnabledHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form enabledForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		err := service.UpdateEnabled(ctx.Request.Context(), app.JobEnabledInput{
			ID:          form.JobID,
			EnabledFlag: form.EnabledFlag,
			UpdateName:  operatorName(ctx),
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

type executeForm struct {
	JobID uint64 `json:"jobId"`
	Param string `json:"param"`
}

// ExecuteHandler serves POST /support/job/execute.
func ExecuteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form executeForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "参数错误"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		err := service.Execute(ctx.Request.Context(), app.JobExecuteInput{
			ID: form.JobID, Param: form.Param, UpdateName: operatorName(ctx),
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// DeleteHandler serves GET /support/job/delete?jobId=.
func DeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, err := strconv.ParseUint(strings.TrimSpace(ctx.Request.URL.Query().Get("jobId")), 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "任务id错误"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		if serviceErr := service.Delete(ctx.Request.Context(), id, operatorName(ctx)); serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

type jobLogQueryForm struct {
	SearchWord  string  `json:"searchWord"`
	JobID       *uint64 `json:"jobId"`
	SuccessFlag *bool   `json:"successFlag"`
	StartTime   string  `json:"startTime"`
	EndTime     string  `json:"endTime"`
	PageNum     uint64  `json:"pageNum"`
	PageSize    uint64  `json:"pageSize"`
}

// LogQueryHandler serves POST /support/job/log/query.
func LogQueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form jobLogQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "查询参数错误"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListLogs(ctx.Request.Context(), app.JobLogQuery{
			SearchWord:  form.SearchWord,
			JobID:       form.JobID,
			SuccessFlag: form.SuccessFlag,
			StartDate:   form.StartTime,
			EndDate:     form.EndTime,
			PageNum:     form.PageNum,
			PageSize:    form.PageSize,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}
