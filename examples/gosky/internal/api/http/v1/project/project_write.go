package project

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func UpdateProjectHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentProjectService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable"))
			return
		}
		var input struct {
			Name        string `json:"name" validate:"required,max=255"`
			Description string `json:"description" validate:"max=10000"`
		}
		if err := ctx.Bind(&input); err != nil {
			ctx.Error(err)
			return
		}
		result, err := service.Update(ctx.Request.Context(), ctx.PathValue("id"), input.Name, input.Description, ctx.RequestID())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, result)
	}
}

func DeleteProjectHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentProjectService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable"))
			return
		}
		result, err := service.Delete(ctx.Request.Context(), ctx.PathValue("id"), ctx.RequestID())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, result)
	}
}
