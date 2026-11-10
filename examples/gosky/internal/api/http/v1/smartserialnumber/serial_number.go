// Package smartserialnumber exposes the unmodified SmartAdmin serial number
// contracts: /support/serialNumber/all|generate|queryRecord.
package smartserialnumber

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func serviceOf(svcCtx *svc.ServiceContext) *app.SerialNumberService {
	return svcCtx.CurrentSerialNumberService()
}

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

// AllHandler serves GET /support/serialNumber/all (definitions table).
func AllHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		items, err := service.ListDefinitions(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(items))
	}
}

// GenerateHandler serves POST /support/serialNumber/generate.
func GenerateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form app.SerialNumberGenerateInput
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "generate form is invalid"))
			return
		}
		numbers, err := service.Generate(ctx.Request.Context(), form)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(numbers))
	}
}

// QueryRecordHandler serves POST /support/serialNumber/queryRecord.
func QueryRecordHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form struct {
			SerialNumberID uint64 `json:"serialNumberId"`
			PageNum        uint64 `json:"pageNum"`
			PageSize       uint64 `json:"pageSize"`
		}
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "record query is invalid"))
			return
		}
		page, err := service.ListRecords(ctx.Request.Context(), form.SerialNumberID, form.PageNum, form.PageSize)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}
