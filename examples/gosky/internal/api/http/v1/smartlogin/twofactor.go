package smartlogin

import (
	"net/http"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// TwoFactorLoginFlagHandler serves GET /login/getTwoFactorLoginFlag.
func TwoFactorLoginFlagHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentTwoFactorService()
		ctx.JSON(http.StatusOK, smartweb.OK(service != nil && service.Enabled()))
	}
}

// SendEmailCodeHandler serves GET /login/sendEmailCode/{loginName}.
// Enumeration-safe: when 2FA is disabled the response still carries the
// success envelope (the unchanged UI starts a countdown), while no code is
// issued or mailed.
func SendEmailCodeHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		loginName := strings.TrimSpace(ctx.PathValue("loginName"))
		service := svcCtx.CurrentTwoFactorService()
		if service == nil || !service.Enabled() {
			// Feature disabled: return a clear failed-precondition so the
			// UI does not claim an email was sent.
			ctx.Error(coreerrors.New(coreerrors.CodeFailedPrecondition, "email verification is not enabled"))
			return
		}
		if err := service.SendCode(ctx.Request.Context(), loginName); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}
