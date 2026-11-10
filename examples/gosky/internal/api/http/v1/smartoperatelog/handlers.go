package smartoperatelog

import (
	"net/http"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func serviceOf(svcCtx *svc.ServiceContext) *app.OperateLogService {
	return svcCtx.CurrentOperateLogService()
}

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

type operateLogQueryForm struct {
	OperateUserID   *uint64 `json:"operateUserId"`
	OperateUserType *int    `json:"operateUserType"`
	Keywords        string  `json:"keywords"`
	RequestKeywords string  `json:"requestKeywords"`
	UserName        string  `json:"userName"`
	SuccessFlag     *bool   `json:"successFlag"`
	StartDate       string  `json:"startDate"`
	EndDate         string  `json:"endDate"`
	PageNum         uint64  `json:"pageNum"`
	PageSize        uint64  `json:"pageSize"`
}

func (form operateLogQueryForm) toQuery() app.OperateLogQuery {
	return app.OperateLogQuery{
		OperateUserID:   form.OperateUserID,
		OperateUserType: form.OperateUserType,
		Keywords:        form.Keywords,
		RequestKeywords: form.RequestKeywords,
		UserName:        form.UserName,
		SuccessFlag:     form.SuccessFlag,
		StartDate:       form.StartDate,
		EndDate:         form.EndDate,
		PageNum:         form.PageNum,
		PageSize:        form.PageSize,
	}
}

// QueryHandler serves POST /support/operateLog/page/query.
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form operateLogQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "operate log query is invalid"))
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

// DetailHandler serves GET /support/operateLog/detail/{id}.
func DetailHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		id, ok := parseOperateID(ctx.PathValue("operateLogId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "operate log id is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		row, err := service.Detail(ctx.Request.Context(), id)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(row))
	}
}

// QueryOwnHandler serves POST /support/operateLog/page/query/login.
func QueryOwnHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		if !ok || strings.TrimSpace(principal.Subject) == "" {
			ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
			return
		}
		var form operateLogQueryForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "operate log query is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		page, err := service.ListOwn(ctx.Request.Context(), principal.Subject, form.toQuery())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}
