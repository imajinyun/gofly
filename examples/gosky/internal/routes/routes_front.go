package routes

import (
	"net/http"

	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/authentication"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/project"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// registerFrontAuthRoutes installs /api/front/auth/**. Login and refresh are
// unauthenticated; logout requires a front-realm token.
func registerFrontAuthRoutes(server *rest.Server, svcCtx *svc.ServiceContext, interceptor authn.Interceptor) {
	publicAuth := server.Group("/api/front/auth")
	publicAuth.AddRoutes([]rest.Route{
		{Method: http.MethodPost, Path: "/login", Handler: authentication.LoginHandler(svcCtx, authn.RealmFront)},
		{Method: http.MethodPost, Path: "/refresh", Handler: authentication.RefreshHandler(svcCtx, authn.RealmFront)},
	})

	authedAuth := server.Group("/api/front/auth")
	authedAuth.With(rest.WithAuth(interceptor.Validator()))
	authedAuth.AddRoute(rest.Route{Method: http.MethodPost, Path: "/logout", Handler: authentication.LogoutHandler(svcCtx)})
}

// registerFrontRoutes installs authenticated /api/front/** business routes.
// The front surface is relation-only: tenant admin capability expansion is
// never a front-realm grant.
func registerFrontRoutes(server *rest.Server, svcCtx *svc.ServiceContext, interceptor authn.Interceptor) {
	front := server.Group("/api/front")
	front.With(rest.WithAuth(interceptor.Validator()))
	front.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/projects", Handler: project.FrontListProjectsHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/projects/{id}", Handler: project.FrontGetProjectHandler(svcCtx)},
	})
}
