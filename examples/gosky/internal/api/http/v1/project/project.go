package project

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func GetProjectHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		projectService := svcCtx.CurrentProjectService()
		if projectService == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable"))
			return
		}
		response, err := projectService.Get(ctx.Request.Context(), ctx.PathValue("id"), ctx.RequestID())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}
