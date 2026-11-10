// Package smartconfig exposes the unmodified SmartAdmin parameter contracts:
// /support/config/query|add|update|queryByKey.
package smartconfig

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func serviceOf(svcCtx *svc.ServiceContext) *app.ConfigParamService {
	return svcCtx.CurrentConfigService()
}

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

// QueryHandler serves POST /support/config/query.
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form app.ConfigParamQuery
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "config query is invalid"))
			return
		}
		page, err := service.List(ctx.Request.Context(), form)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

// AddHandler serves POST /support/config/add.
func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form app.ConfigParamInput
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "config form is invalid"))
			return
		}
		param, err := service.Create(ctx.Request.Context(), form)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(param))
	}
}

// UpdateHandler serves POST /support/config/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form app.ConfigParamInput
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "config form is invalid"))
			return
		}
		if err := service.Update(ctx.Request.Context(), form); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// QueryByKeyHandler serves GET /support/config/queryByKey?configKey=.
func QueryByKeyHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		param, err := service.GetByKey(ctx.Request.Context(), ctx.Request.URL.Query().Get("configKey"))
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(param))
	}
}
