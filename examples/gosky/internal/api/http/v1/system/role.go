// Package system exposes gosky's native dynamic RBAC administration API:
// tenant users, custom roles, and tenant menu management. All authorization
// happens inside app services using the verified JWT principal.
package system

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

type roleDTO struct {
	ID     uint64 `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Remark string `json:"remark"`
	State  string `json:"state"`
	System bool   `json:"system"`
}

type createRoleRequest struct {
	Code   string `json:"code" validate:"required,max=64"`
	Name   string `json:"name" validate:"required,max=255"`
	Remark string `json:"remark" validate:"max=512"`
}

type updateRoleRequest struct {
	Name   string `json:"name" validate:"required,max=255"`
	Remark string `json:"remark" validate:"max=512"`
}

type roleMenuResponse struct {
	Role     roleDTO        `json:"role"`
	MenuTree []app.MenuNode `json:"menuTree"`
	Selected []uint64       `json:"selectedMenuIds"`
}

type replaceRoleMenusRequest struct {
	MenuIDs []uint64 `json:"menuIds"`
}

func toRoleDTO(role app.RoleRecord) roleDTO {
	return roleDTO{ID: role.ID, Code: role.Code, Name: role.Name, Remark: role.Remark, State: role.State, System: role.System}
}

func ListRolesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		roles, err := service.ListRoles(ctx.Request.Context(), ctx.Request.URL.Query().Get("includeDisabled") == "true")
		if err != nil {
			ctx.Error(err)
			return
		}
		items := make([]roleDTO, 0, len(roles))
		for _, role := range roles {
			items = append(items, toRoleDTO(role))
		}
		ctx.JSON(http.StatusOK, map[string]any{"roles": items})
	}
}

func CreateRoleHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		var request createRoleRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		role, err := service.CreateRole(ctx.Request.Context(), app.RoleInput{Code: request.Code, Name: request.Name, Remark: request.Remark})
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, toRoleDTO(role))
	}
}

func UpdateRoleHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		roleID, ok := pathID(ctx)
		if !ok {
			return
		}
		var request updateRoleRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		if err := service.UpdateRole(ctx.Request.Context(), roleID, app.RoleUpdate{Name: request.Name, Remark: request.Remark}); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"changed": true})
	}
}

func DeleteRoleHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		roleID, ok := pathID(ctx)
		if !ok {
			return
		}
		if err := service.DeleteRole(ctx.Request.Context(), roleID); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"changed": true})
	}
}

func RoleMenuHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		roleID, ok := pathID(ctx)
		if !ok {
			return
		}
		role, menus, selected, err := service.RoleMenuSelection(ctx.Request.Context(), roleID)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, roleMenuResponse{Role: toRoleDTO(role), MenuTree: app.BuildMenuTree(menus), Selected: selected})
	}
}

func ReplaceRoleMenusHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		roleID, ok := pathID(ctx)
		if !ok {
			return
		}
		var request replaceRoleMenusRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		if err := service.ReplaceRoleGrants(ctx.Request.Context(), roleID, request.MenuIDs); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"changed": true})
	}
}
