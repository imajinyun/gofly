// Package authentication exposes gosky-owned admin/front login session APIs.
package authentication

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

const defaultSessionListLimit uint = 20

type loginRequest struct {
	LoginName  string `json:"loginName" validate:"required,max=255"`
	TenantCode string `json:"tenantCode" validate:"required,max=64"`
	Password   string `json:"password" validate:"required,max=1024"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken" validate:"required,max=1024"`
}

// LoginHandler registers an explicit public exception beneath exactly one
// business realm. It does not infer realm from a request header or body.
func LoginHandler(svcCtx *svc.ServiceContext, realm authn.Realm) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentAuthenticationService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "authentication service is unavailable"))
			return
		}
		var request loginRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		response, err := service.Login(ctx.Request.Context(), authn.LoginInput{
			Realm:      realm,
			LoginName:  request.LoginName,
			TenantCode: request.TenantCode,
			Password:   request.Password,
			RequestID:  ctx.RequestID(),
		})
		if err != nil {
			ctx.Error(authenticationError(err))
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

// RefreshHandler consumes a realm-bound opaque refresh token without applying
// bearer authentication to this explicitly public route.
func RefreshHandler(svcCtx *svc.ServiceContext, realm authn.Realm) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentAuthenticationService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "authentication service is unavailable"))
			return
		}
		var request refreshRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(err)
			return
		}
		response, err := service.Refresh(ctx.Request.Context(), authn.RefreshInput{
			Realm:        realm,
			RefreshToken: request.RefreshToken,
			RequestID:    ctx.RequestID(),
		})
		if err != nil {
			ctx.Error(authenticationError(err))
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

// LogoutHandler only accepts the server-established request principal; clients
// cannot pass a different session ID in a body or query parameter.
func LogoutHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentAuthenticationService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "authentication service is unavailable"))
			return
		}
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeUnauthenticated, "authentication required"))
			return
		}
		if err := service.Logout(ctx.Request.Context(), authn.LogoutInput{Principal: principal, RequestID: ctx.RequestID()}); err != nil {
			ctx.Error(authenticationError(err))
			return
		}
		ctx.JSON(http.StatusOK, map[string]bool{"revoked": true})
	}
}

// SessionHandler returns the verified session's client-safe navigation
// projection. It relies exclusively on the principal established by the exact
// admin realm interceptor; neither headers nor query parameters can select a
// different user, tenant, or navigation scope.
func SessionHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentNavigationService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "session navigation is unavailable"))
			return
		}
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeUnauthenticated, "authentication required"))
			return
		}
		response, err := service.Project(ctx.Request.Context(), principal)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

// ListSessionsHandler returns active sessions only for the server-established
// admin principal. The query has no subject, tenant, or realm selectors.
func ListSessionsHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentAdminSessionService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "admin session management is unavailable"))
			return
		}
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeUnauthenticated, "authentication required"))
			return
		}
		limit, err := parseSessionListLimit(ctx.Query("limit"))
		if err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "session list limit must be between 1 and 100"))
			return
		}
		response, err := service.ListSessions(ctx.Request.Context(), authn.SessionListInput{Principal: principal, Limit: limit, RequestID: ctx.RequestID()})
		if err != nil {
			ctx.Error(sessionManagementError(err))
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

// RevokeSessionHandler targets an opaque session ID while the server derives
// every authorization scope field from the verified admin principal.
func RevokeSessionHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentAdminSessionService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "admin session management is unavailable"))
			return
		}
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		if !ok {
			ctx.Error(coreerrors.New(coreerrors.CodeUnauthenticated, "authentication required"))
			return
		}
		response, err := service.RevokeSession(ctx.Request.Context(), authn.SessionRevokeInput{Principal: principal, SessionID: ctx.PathValue("id"), RequestID: ctx.RequestID()})
		if err != nil {
			ctx.Error(sessionManagementError(err))
			return
		}
		ctx.JSON(http.StatusOK, response)
	}
}

func parseSessionListLimit(raw string) (uint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultSessionListLimit, nil
	}
	parsed, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || parsed < 1 || parsed > 100 {
		return 0, errors.New("session list limit is invalid")
	}
	return uint(parsed), nil
}

func authenticationError(err error) error {
	if errors.Is(err, authn.ErrAuthenticationRejected) {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "authentication rejected")
	}
	return coreerrors.Wrap(coreerrors.CodeUnavailable, "authentication service is unavailable", err)
}

func sessionManagementError(err error) error {
	if errors.Is(err, authn.ErrInvalidSessionRequest) {
		return coreerrors.New(coreerrors.CodeInvalidArgument, "session management request is invalid")
	}
	return authenticationError(err)
}
