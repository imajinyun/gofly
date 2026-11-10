// Package smartcache exposes the unmodified SmartAdmin in-process cache
// console contracts (support/cache names, keys, remove).
package smartcache

import (
	"net/http"

	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// NamesHandler serves GET /support/cache/names.
func NamesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentCacheConsoleService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		names, err := service.Names(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(names))
	}
}

// KeysHandler serves GET /support/cache/keys/{cacheName}.
func KeysHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		name := ctx.PathValue("cacheName")
		service := svcCtx.CurrentCacheConsoleService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		keys, err := service.Keys(ctx.Request.Context(), name)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(keys))
	}
}

// RemoveHandler serves GET /support/cache/remove/{cacheName}.
func RemoveHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		name := ctx.PathValue("cacheName")
		service := svcCtx.CurrentCacheConsoleService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		if err := service.Remove(ctx.Request.Context(), name); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}
