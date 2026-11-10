// Package authorization provides tenant-scoped Casbin policy management APIs.
package authorization

import (
	"net/http"
	"strconv"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

type roleRequest struct {
	// Role accepts reserved codes and active tenant-managed role codes; the
	// policy store validates existence against the roles table.
	Subject string `json:"subject" validate:"required,max=255"`
	Role    string `json:"role" validate:"required,max=64"`
}

func ListRolesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentPolicyService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "policy management is unavailable"))
			return
		}
		roles, err := service.ListRoles(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]any{"roles": roles})
	}
}

func GrantRoleHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return mutateRole(svcCtx, func(service *authz.PolicyService, ctx *rest.Context, request roleRequest) (bool, *coreerrors.Error) {
		return service.GrantRole(ctx.Request.Context(), request.Subject, request.Role, ctx.RequestID())
	})
}

func RevokeRoleHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return mutateRole(svcCtx, func(service *authz.PolicyService, ctx *rest.Context, request roleRequest) (bool, *coreerrors.Error) {
		return service.RevokeRole(ctx.Request.Context(), request.Subject, request.Role, ctx.RequestID())
	})
}

func ListPolicyEventsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentPolicyService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "policy management is unavailable"))
			return
		}
		limit := 50
		if raw := ctx.Request.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "event limit must be between 1 and 100"))
				return
			}
			limit = parsed
		}
		events, err := service.ListEvents(ctx.Request.Context(), limit)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]any{"events": events})
	}
}

func mutateRole(svcCtx *svc.ServiceContext, mutate func(*authz.PolicyService, *rest.Context, roleRequest) (bool, *coreerrors.Error)) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentPolicyService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "policy management is unavailable"))
			return
		}
		var request roleRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		changed, err := mutate(service, ctx, request)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"changed": changed})
	}
}
