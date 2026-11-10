// Package smartdict exposes the unmodified SmartAdmin dictionary contracts:
// /support/dict/** and /support/dict/dictData/**.
package smartdict

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

func serviceOf(svcCtx *svc.ServiceContext) *app.DictService { return svcCtx.CurrentDictService() }

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

// ---- dictionaries ----

func QueryPageHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form struct {
			Keywords     string `json:"keywords"`
			DisabledFlag *bool  `json:"disabledFlag"`
			PageNum      uint64 `json:"pageNum"`
			PageSize     uint64 `json:"pageSize"`
		}
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict query is invalid"))
			return
		}
		page, serviceErr := service.ListDicts(ctx.Request.Context(), app.DictQuery{
			Keywords:     form.Keywords,
			DisabledFlag: form.DisabledFlag,
			PageNum:      form.PageNum,
			PageSize:     form.PageSize,
		})
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

func GetAllHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		items, err := service.ListAllDicts(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(items))
	}
}

func AddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form app.DictInput
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict form is invalid"))
			return
		}
		dict, err := service.CreateDict(ctx.Request.Context(), form)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(dict))
	}
}

func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form app.DictInput
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict form is invalid"))
			return
		}
		if err := service.UpdateDict(ctx.Request.Context(), form); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func BatchDeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		ids, err := smartcompat.BindUint64Array(ctx)
		if err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict id list is invalid"))
			return
		}
		if err := service.DeleteDicts(ctx.Request.Context(), ids); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func UpdateDisabledHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		id, ok := parseID(ctx.PathValue("dictId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict id is invalid"))
			return
		}
		if err := service.ToggleDict(ctx.Request.Context(), id); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

// ---- dictionary values ----

func QueryDataHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		id, ok := parseID(ctx.PathValue("dictId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict id is invalid"))
			return
		}
		items, err := service.ListDictData(ctx.Request.Context(), id)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(items))
	}
}

func GetAllDataHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			// The post-login dict bootstrap must not break the console
			// before the dictionary feature is configured: return an empty
			// list with an OK envelope.
			ctx.JSON(http.StatusOK, smartweb.OK(make([]app.DictData, 0)))
			return
		}
		items, err := service.AllActiveDictData(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(items))
	}
}

func AddDataHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form app.DictDataInput
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict data form is invalid"))
			return
		}
		data, err := service.CreateDictData(ctx.Request.Context(), form)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(data))
	}
}

func UpdateDataHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		var form app.DictDataInput
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict data form is invalid"))
			return
		}
		if err := service.UpdateDictData(ctx.Request.Context(), form); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func BatchDeleteDataHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		ids, err := smartcompat.BindUint64Array(ctx)
		if err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict data id list is invalid"))
			return
		}
		if err := service.DeleteDictData(ctx.Request.Context(), ids); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func UpdateDataDisabledHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		id, ok := parseID(ctx.PathValue("dictDataId"))
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "dict data id is invalid"))
			return
		}
		if err := service.ToggleDictData(ctx.Request.Context(), id); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func parseID(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	id, err := strconv.ParseUint(value, 10, 64)
	return id, err == nil && id != 0
}
