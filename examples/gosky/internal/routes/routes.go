// Package routes wires gosky's HTTP surface. Routes are organized by
// identity realm:
//
//   - public: unauthenticated health and SmartAdmin login endpoints;
//   - admin:  /api/admin/** plus the root-level SmartAdmin compatibility
//     surface, both behind the admin-realm interceptor;
//   - front:  /api/front/** behind the front-realm interceptor.
//
// Within a realm, unauthenticated endpoints (login/refresh) use a separate
// prefix group without the inherited auth option; every business route is
// registered on a group that requires its realm interceptor.
package routes

import (
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// RegisterRoutes installs the complete gosky HTTP surface on the server.
func RegisterRoutes(server *rest.Server, svcCtx *svc.ServiceContext) {
	registerPublicRoutes(server, svcCtx)

	authenticationService := svcCtx.CurrentAuthenticationService()
	if authenticationService == nil {
		// The identity and business surfaces both require persisted
		// authentication; without it only public routes exist.
		return
	}
	adminInterceptor := authn.NewAdminInterceptor(authenticationService.JWTValidator(), authenticationService)
	frontInterceptor := authn.NewFrontInterceptor(authenticationService.JWTValidator(), authenticationService)

	registerAdminAuthRoutes(server, svcCtx, adminInterceptor)
	registerFrontAuthRoutes(server, svcCtx, frontInterceptor)
	registerSmartWebCompatibilityRoutes(server, svcCtx, adminInterceptor)

	// Capability-based business routes additionally require an authorizer.
	if svcCtx.CurrentAuthorizer() == nil {
		return
	}
	registerFrontRoutes(server, svcCtx, frontInterceptor)
	registerAdminRoutes(server, svcCtx, adminInterceptor)
}
