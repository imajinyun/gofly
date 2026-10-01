package routes

import (
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/ping"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/project"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

func RegisterRoutes(server *rest.Server, svcCtx *svc.ServiceContext) {
	api := server.Group("/api")
	api.AddRoute(rest.Route{Method: "GET", Path: "/v1/ping", Handler: ping.PingHandler(svcCtx)})
	authorizer := svcCtx.CurrentAuthorizer()
	if authorizer == nil {
		return
	}
	api.AddRoute(
		rest.Route{Method: "GET", Path: "/v1/projects/{id}", Handler: project.GetProjectHandler(svcCtx)},
		rest.WithAuth(authorizer.JWTValidator()),
	)
}
