package project

import (
	"net/http"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

type memberMutationRequest struct {
	Subject string `json:"subject" validate:"required,max=255"`
	Role    string `json:"role" validate:"required,oneof=viewer editor"`
}

func ListProjectsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		projectService := svcCtx.CurrentProjectService()
		if projectService == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "project list persistence is unavailable"))
			return
		}
		response, err := projectService.List(ctx.Request.Context(), ctx.Request.URL.Query().Get("limit"), ctx.Request.URL.Query().Get("cursor"), ctx.RequestID())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

// FrontListProjectsHandler deliberately uses the relation-only service path;
// admin Casbin policy expansion is not a front-realm read grant.
func FrontListProjectsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		projectService := svcCtx.CurrentProjectService()
		if projectService == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "project list persistence is unavailable"))
			return
		}
		response, err := projectService.ListForFront(ctx.Request.Context(), ctx.Request.URL.Query().Get("limit"), ctx.Request.URL.Query().Get("cursor"), ctx.RequestID())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

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

// FrontGetProjectHandler deliberately uses the relation-only service path;
// admin Casbin policy expansion is not a front-realm read grant.
func FrontGetProjectHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		projectService := svcCtx.CurrentProjectService()
		if projectService == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "project persistence is unavailable"))
			return
		}
		response, err := projectService.GetForFront(ctx.Request.Context(), ctx.PathValue("id"), ctx.RequestID())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

func ListProjectMembersHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		projectService := svcCtx.CurrentProjectService()
		if projectService == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "project member list persistence is unavailable"))
			return
		}
		response, err := projectService.ListMembers(ctx.Request.Context(), ctx.PathValue("id"), ctx.Request.URL.Query().Get("limit"), ctx.RequestID())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

func UpsertProjectMemberHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return mutateProjectMember(svcCtx, func(service *app.ProjectService, ctx *rest.Context, request memberMutationRequest) (app.ProjectMemberMutationResponse, *coreerrors.Error) {
		return service.UpsertMember(ctx.Request.Context(), ctx.PathValue("id"), request.Subject, request.Role, ctx.RequestID())
	})
}

func RevokeProjectMemberHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return mutateProjectMember(svcCtx, func(service *app.ProjectService, ctx *rest.Context, request memberMutationRequest) (app.ProjectMemberMutationResponse, *coreerrors.Error) {
		return service.RevokeMember(ctx.Request.Context(), ctx.PathValue("id"), request.Subject, request.Role, ctx.RequestID())
	})
}

func mutateProjectMember(svcCtx *svc.ServiceContext, mutate func(*app.ProjectService, *rest.Context, memberMutationRequest) (app.ProjectMemberMutationResponse, *coreerrors.Error)) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		projectService := svcCtx.CurrentProjectService()
		if projectService == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "project member persistence is unavailable"))
			return
		}
		var request memberMutationRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		response, err := mutate(projectService, ctx, request)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}
