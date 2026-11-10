// Package smartlogin exposes the narrow Smart API-compatible login surface
// required by the unmodified smart-web pages.
package smartlogin

import (
	"errors"
	"net/http"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartprotect"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func loginFailResponseCode(code coreerrors.Code) (int, bool) {
	switch code {
	case app.LoginFailLockedCode:
		return 30010, true
	case app.LoginFailWillLockCode:
		return 30011, true
	default:
		return 0, false
	}
}

// rejectLogin renders the SmartAdmin envelope; lock and will-lock hints from
// level-3 protection replace the default credential message.
func rejectLogin(ctx *rest.Context, err error) {
	var appErr *coreerrors.Error
	if errors.As(err, &appErr) {
		if code, ok := loginFailResponseCode(appErr.Code); ok {
			ctx.JSON(http.StatusOK, smartweb.ResponseDTO[any]{
				Code: code, Level: "error", Msg: appErr.Text, OK: false, DataType: 1,
			})
			return
		}
	}
	status := http.StatusOK
	if !errors.Is(err, authn.ErrAuthenticationRejected) {
		status = http.StatusServiceUnavailable
	}
	ctx.JSON(status, smartweb.Reject[any]())
}

func CaptchaHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		challenge, err := service.NewCaptcha()
		if err != nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(challenge))
	}
}

func LoginHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.JSON(http.StatusServiceUnavailable, smartweb.Reject[any]())
			return
		}
		var request smartweb.LoginInput
		if err := ctx.Bind(&request); err != nil {
			ctx.JSON(http.StatusOK, smartweb.Reject[any]())
			return
		}
		request.RequestID = ctx.RequestID()
		loginCtx := smartweb.WithLoginMeta(ctx.Request.Context(),
			smartprotect.MetaFromRequest(ctx.Request, request.RequestID))
		response, err := service.Login(loginCtx, request)
		if err != nil {
			rejectLogin(ctx, err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(response))
	}
}

func LoginInfoHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		accessToken, tokenOK := auth.ExtractBearer(ctx.Request.Header.Get(auth.AuthorizationHeader))
		if service == nil || !ok || !tokenOK {
			ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
			return
		}
		response, err := service.LoginInfo(ctx.Request.Context(), principal, accessToken)
		if err != nil {
			status := http.StatusUnauthorized
			if !errors.Is(err, authn.ErrAuthenticationRejected) {
				status = http.StatusServiceUnavailable
			}
			ctx.JSON(status, smartweb.Reject[any]())
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(response))
	}
}

func LogoutHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		principal, ok := authn.PrincipalFromContext(ctx.Request.Context())
		if service == nil || !ok {
			ctx.JSON(http.StatusUnauthorized, smartweb.Reject[any]())
			return
		}
		logoutCtx := smartweb.WithLoginMeta(ctx.Request.Context(),
			smartprotect.MetaFromRequest(ctx.Request, ctx.RequestID()))
		if err := service.Logout(logoutCtx, principal, ctx.RequestID()); err != nil {
			status := http.StatusUnauthorized
			if !errors.Is(err, authn.ErrAuthenticationRejected) {
				status = http.StatusServiceUnavailable
			}
			ctx.JSON(status, smartweb.Reject[any]())
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}
