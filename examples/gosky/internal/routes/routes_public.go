package routes

import (
	"net/http"

	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/ping"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartfile"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/smartlogin"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// registerPublicRoutes installs unauthenticated endpoints: the API health
// probe and the anonymous SmartAdmin login/captcha surface.
func registerPublicRoutes(server *rest.Server, svcCtx *svc.ServiceContext) {
	api := server.Group("/api")
	api.AddRoute(rest.Route{Method: http.MethodGet, Path: "/v1/ping", Handler: ping.PingHandler(svcCtx)})

	if svcCtx.CurrentSmartWebService() == nil {
		return
	}
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/login/getCaptcha", Handler: smartlogin.CaptchaHandler(svcCtx)})
	server.AddRoute(rest.Route{Method: http.MethodPost, Path: "/login", Handler: smartlogin.LoginHandler(svcCtx)})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/login/getTwoFactorLoginFlag", Handler: smartlogin.TwoFactorLoginFlagHandler(svcCtx)})
	server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/login/sendEmailCode/{loginName}", Handler: smartlogin.SendEmailCodeHandler(svcCtx)})

	// Capability URL for <img>/public file access; authorization is the
	// HMAC signature verified inside the handler, not a bearer session.
	if svcCtx.CurrentFileService() != nil {
		server.AddRoute(rest.Route{Method: http.MethodGet, Path: "/support/file/view", Handler: smartfile.ViewHandler(svcCtx)})
	}
}
