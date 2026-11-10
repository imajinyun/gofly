package system

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

type menuRequest struct {
	ParentID   uint64 `json:"parentId"`
	Name       string `json:"name" validate:"required,max=255"`
	Type       int    `json:"type" validate:"required,oneof=1 2 3"`
	Sort       int    `json:"sort"`
	Path       string `json:"path" validate:"max=255"`
	Component  string `json:"component" validate:"max=255"`
	Capability string `json:"capability" validate:"max=64"`
	Icon       string `json:"icon" validate:"max=128"`
	Visible    bool   `json:"visible"`
}

type menuResponse struct {
	ID         uint64     `json:"id"`
	ParentID   uint64     `json:"parentId"`
	Name       string     `json:"name"`
	Type       int        `json:"type"`
	Sort       int        `json:"sort"`
	Path       string     `json:"path,omitempty"`
	Component  string     `json:"component,omitempty"`
	Capability string     `json:"capability,omitempty"`
	WebPerms   string     `json:"webPerms,omitempty"`
	Icon       string     `json:"icon,omitempty"`
	Visible    bool       `json:"visible"`
	System     bool       `json:"system"`
	Children   []menuNode `json:"children,omitempty"`
}

type menuNode = menuResponse

func toMenuResponse(menu app.MenuRecord) menuResponse {
	return menuResponse{
		ID: menu.ID, ParentID: menu.ParentID, Name: menu.Name, Type: menu.Type,
		Sort: menu.Sort, Path: menu.Path, Component: menu.Component,
		Capability: menu.Capability, WebPerms: menu.WebPerms, Icon: menu.Icon,
		Visible: menu.Visible, System: menu.System,
	}
}

func toMenuTree(nodes []app.MenuNode) []menuResponse {
	result := make([]menuResponse, 0, len(nodes))
	for _, node := range nodes {
		item := toMenuResponse(node.MenuRecord)
		if len(node.Children) > 0 {
			item.Children = toMenuTree(node.Children)
		}
		result = append(result, item)
	}
	return result
}

func toMenuInput(request menuRequest) app.MenuInput {
	return app.MenuInput{
		ParentID:   request.ParentID,
		Name:       request.Name,
		Type:       request.Type,
		Sort:       request.Sort,
		Path:       request.Path,
		Component:  request.Component,
		Capability: request.Capability,
		Icon:       request.Icon,
		Visible:    request.Visible,
	}
}

func MenuTreeHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		includeDisabled := ctx.Request.URL.Query().Get("includeDisabled") == "true"
		if includeDisabled {
			// Tree reads default to active menus; disable projection stays an
			// internal-management need served by list semantics.
			menus, err := service.ListMenus(ctx.Request.Context(), true)
			if err != nil {
				ctx.Error(err)
				return
			}
			ctx.JSON(http.StatusOK, map[string]any{"menus": menus})
			return
		}
		tree, err := service.MenuTree(ctx.Request.Context(), false)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]any{"tree": tree})
	}
}

func CreateMenuHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		var request menuRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		menu, err := service.CreateMenu(ctx.Request.Context(), toMenuInput(request))
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusCreated, toMenuResponse(menu))
	}
}

func UpdateMenuHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		menuID, ok := pathID(ctx)
		if !ok {
			return
		}
		var request menuRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		if err := service.UpdateMenu(ctx.Request.Context(), menuID, toMenuInput(request)); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"changed": true})
	}
}

func DeleteMenuHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentRBACService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "rbac management is unavailable"))
			return
		}
		menuID, ok := pathID(ctx)
		if !ok {
			return
		}
		if err := service.DeleteMenu(ctx.Request.Context(), menuID); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"changed": true})
	}
}
