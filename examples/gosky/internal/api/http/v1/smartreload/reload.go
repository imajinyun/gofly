// Package smartreload exposes the unmodified SmartAdmin SmartReload
// heartbeat console contracts (support/reload query, result, update).
package smartreload

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

func serviceOf(svcCtx *svc.ServiceContext) *app.ReloadService {
	return svcCtx.CurrentReloadService()
}

func unavailable(ctx *rest.Context) {
	ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
}

// QueryHandler serves GET /support/reload/query. The Java reference has no
// permission annotation; any authenticated operator may list tags.
func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		if _, ok := authn.PrincipalFromContext(ctx.Request.Context()); !ok {
			ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		items, err := service.ListItems(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(items))
	}
}

// ResultHandler serves GET /support/reload/result/{tag}.
func ResultHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		tag := strings.TrimSpace(ctx.PathValue("tag"))
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		results, err := service.ListResults(ctx.Request.Context(), tag)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(results))
	}
}

type reloadForm struct {
	Tag            string `json:"tag"`
	Identification string `json:"identification"`
	Args           string `json:"args"`
}

// UpdateHandler serves POST /support/reload/update.
func UpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		var form reloadForm
		if err := ctx.Bind(&form); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "reload form is invalid"))
			return
		}
		service := serviceOf(svcCtx)
		if service == nil {
			unavailable(ctx)
			return
		}
		err := service.UpdateItem(ctx.Request.Context(), app.ReloadForm{
			Tag:            form.Tag,
			Identification: form.Identification,
			Args:           form.Args,
		})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}
