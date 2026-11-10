// Package smartmenu exposes the SmartAdmin Menu Catalog surface. Catalog
// reads and role grants come from the tenant catalog; seeded rows and the
// capability vocabulary stay deployment-owned.
package smartmenu

import (
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func QueryHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := smartWebService(svcCtx, ctx)
		if service == nil {
			return
		}
		menus, err := service.MenuCatalog(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(menus))
	}
}

func TreeHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := smartWebService(svcCtx, ctx)
		if service == nil {
			return
		}
		onlyMenu, err := strconv.ParseBool(strings.TrimSpace(ctx.Request.URL.Query().Get("onlyMenu")))
		if err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "onlyMenu must be true or false"))
			return
		}
		menus, serviceErr := service.MenuTree(ctx.Request.Context(), onlyMenu)
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(menus))
	}
}

func AuthorizationURLsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := smartWebService(svcCtx, ctx)
		if service == nil {
			return
		}
		urls, err := service.MenuAuthorizationURLs(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(urls))
	}
}

func RejectMutationHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := smartWebService(svcCtx, ctx)
		if service == nil {
			return
		}
		ctx.Error(service.RejectMenuMutation(ctx.Request.Context()))
	}
}

// AddMenuHandler serves the historical /menu/add endpoint through the
// tenant-managed catalog. Seeded rows and free-form capabilities stay
// immutable and are rejected before any write.
func AddMenuHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := smartWebService(svcCtx, ctx)
		if service == nil {
			return
		}
		var mutation smartweb.MenuMutation
		if err := ctx.Bind(&mutation); err != nil {
			ctx.Error(err)
			return
		}
		menu, err := service.CreateMenu(ctx.Request.Context(), mutation)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(menu))
	}
}

func UpdateMenuHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := smartWebService(svcCtx, ctx)
		if service == nil {
			return
		}
		var mutation smartweb.MenuMutation
		if err := ctx.Bind(&mutation); err != nil {
			ctx.Error(err)
			return
		}
		if err := service.UpdateMenu(ctx.Request.Context(), mutation); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(map[string]bool{"changed": true}))
	}
}

func DeleteMenuHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := smartWebService(svcCtx, ctx)
		if service == nil {
			return
		}
		ids, ok := parseMenuIDs(ctx)
		if !ok {
			return
		}
		if err := service.DeleteMenus(ctx.Request.Context(), ids); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(map[string]bool{"changed": true}))
	}
}

func smartWebService(svcCtx *svc.ServiceContext, ctx *rest.Context) svc.SmartWebService {
	service := svcCtx.CurrentSmartWebService()
	if service == nil {
		ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
	}
	return service
}

// parseMenuIDs accepts the unmodified SmartAdmin batch-delete form
// (?menuIdList=1,2,3) plus the earlier internal aliases (?ids=1,2 and
// ?menuId=1,2) for callers converging during rollout. Unknown or empty
// values fail with 400.
func parseMenuIDs(ctx *rest.Context) ([]uint64, bool) {
	raw := ctx.Request.URL.Query().Get("menuIdList")
	if raw == "" {
		raw = ctx.Request.URL.Query().Get("ids")
	}
	if raw == "" {
		raw = ctx.Request.URL.Query().Get("menuId")
	}
	if raw == "" {
		ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "menu ids are required"))
		return nil, false
	}
	parts := strings.Split(raw, ",")
	ids := make([]uint64, 0, len(parts))
	seen := make(map[uint64]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "menu ids are required"))
			return nil, false
		}
		id, err := strconv.ParseUint(part, 10, 64)
		if err != nil || id == 0 {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "menu id must be a positive integer"))
			return nil, false
		}
		if _, exists := seen[id]; exists {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "menu ids must not repeat"))
			return nil, false
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, true
}
