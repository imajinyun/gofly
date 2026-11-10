package smartrole

import (
	"net/http"

	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// RoleAddHandler serves POST /role/add for tenant-managed roles.
func RoleAddHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		var form smartweb.RoleAddForm
		if err := ctx.Bind(&form); err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		role, err := service.AddRole(ctx.Request.Context(), form)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(role))
	}
}

// RoleUpdateHandler serves POST /role/update.
func RoleUpdateHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		var form smartweb.RoleUpdateForm
		if err := ctx.Bind(&form); err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := service.UpdateRole(ctx.Request.Context(), form); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}

// RoleDeleteHandler serves GET /role/delete/{roleId}.
func RoleDeleteHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		roleID, err := smartWebRoleID(ctx.PathValue("id"))
		if err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		if err := service.DeleteRole(ctx.Request.Context(), roleID); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK("操作成功"))
	}
}
