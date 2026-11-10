package routes

import (
	"net/http"

	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/authentication"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/authorization"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/project"
	"github.com/imajinyun/gofly/examples/gosky/internal/api/http/v1/system"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// registerAdminAuthRoutes installs /api/admin/auth/**. Login and refresh are
// unauthenticated; logout and the session projections require an admin token.
func registerAdminAuthRoutes(server *rest.Server, svcCtx *svc.ServiceContext, interceptor authn.Interceptor) {
	publicAuth := server.Group("/api/admin/auth")
	publicAuth.AddRoutes([]rest.Route{
		{Method: http.MethodPost, Path: "/login", Handler: authentication.LoginHandler(svcCtx, authn.RealmAdmin)},
		{Method: http.MethodPost, Path: "/refresh", Handler: authentication.RefreshHandler(svcCtx, authn.RealmAdmin)},
	})

	authedAuth := server.Group("/api/admin/auth")
	authedAuth.With(rest.WithAuth(interceptor.Validator()))
	authedAuth.AddRoute(rest.Route{Method: http.MethodPost, Path: "/logout", Handler: authentication.LogoutHandler(svcCtx)})
	if svcCtx.CurrentAdminSessionService() != nil {
		authedAuth.AddRoutes([]rest.Route{
			{Method: http.MethodGet, Path: "/sessions", Handler: authentication.ListSessionsHandler(svcCtx)},
			{Method: http.MethodDelete, Path: "/sessions/{id}", Handler: authentication.RevokeSessionHandler(svcCtx)},
		})
	}
	if svcCtx.CurrentNavigationService() != nil {
		authedAuth.AddRoute(rest.Route{Method: http.MethodGet, Path: "/session", Handler: authentication.SessionHandler(svcCtx)})
	}
}

// registerAdminRoutes installs every authenticated /api/admin/** business
// surface: projects, native policy administration, and dynamic RBAC.
func registerAdminRoutes(server *rest.Server, svcCtx *svc.ServiceContext, interceptor authn.Interceptor) {
	admin := server.Group("/api/admin")
	admin.With(rest.WithAuth(interceptor.Validator()))

	admin.AddRoutes([]rest.Route{
		{Method: http.MethodGet, Path: "/projects", Handler: project.ListProjectsHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/projects/{id}", Handler: project.GetProjectHandler(svcCtx)},
		{Method: http.MethodGet, Path: "/projects/{id}/members", Handler: project.ListProjectMembersHandler(svcCtx)},
		{Method: http.MethodPut, Path: "/projects/{id}/members", Handler: project.UpsertProjectMemberHandler(svcCtx)},
		{Method: http.MethodDelete, Path: "/projects/{id}/members", Handler: project.RevokeProjectMemberHandler(svcCtx)},
		{Method: http.MethodPut, Path: "/projects/{id}", Handler: project.UpdateProjectHandler(svcCtx)},
		{Method: http.MethodDelete, Path: "/projects/{id}", Handler: project.DeleteProjectHandler(svcCtx)},
	})

	if svcCtx.CurrentPolicyService() != nil {
		admin.AddRoutes([]rest.Route{
			{Method: http.MethodGet, Path: "/authorization/roles", Handler: authorization.ListRolesHandler(svcCtx)},
			{Method: http.MethodPost, Path: "/authorization/roles", Handler: authorization.GrantRoleHandler(svcCtx)},
			{Method: http.MethodDelete, Path: "/authorization/roles", Handler: authorization.RevokeRoleHandler(svcCtx)},
			{Method: http.MethodGet, Path: "/authorization/policy-events", Handler: authorization.ListPolicyEventsHandler(svcCtx)},
		})
	}

	registerSystemRoutes(admin, svcCtx)
}

// registerSystemRoutes wires the native dynamic RBAC administration surface
// onto the authenticated admin group. Services enforce deployment-owned
// capabilities after JWT validation; routes stay unavailable when identity or
// policy storage is not configured.
func registerSystemRoutes(admin *rest.RouterGroup, svcCtx *svc.ServiceContext) {
	if svcCtx.CurrentRBACService() != nil {
		admin.AddRoutes([]rest.Route{
			{Method: http.MethodGet, Path: "/system/roles", Handler: system.ListRolesHandler(svcCtx)},
			{Method: http.MethodPost, Path: "/system/roles", Handler: system.CreateRoleHandler(svcCtx)},
			{Method: http.MethodPatch, Path: "/system/roles/{id}", Handler: system.UpdateRoleHandler(svcCtx)},
			{Method: http.MethodDelete, Path: "/system/roles/{id}", Handler: system.DeleteRoleHandler(svcCtx)},
			{Method: http.MethodGet, Path: "/system/roles/{id}/menus", Handler: system.RoleMenuHandler(svcCtx)},
			{Method: http.MethodPut, Path: "/system/roles/{id}/menus", Handler: system.ReplaceRoleMenusHandler(svcCtx)},
			{Method: http.MethodGet, Path: "/system/menus", Handler: system.MenuTreeHandler(svcCtx)},
			{Method: http.MethodPost, Path: "/system/menus", Handler: system.CreateMenuHandler(svcCtx)},
			{Method: http.MethodPut, Path: "/system/menus/{id}", Handler: system.UpdateMenuHandler(svcCtx)},
			{Method: http.MethodDelete, Path: "/system/menus/{id}", Handler: system.DeleteMenuHandler(svcCtx)},
		})
	}
	if svcCtx.CurrentUserAdminService() != nil {
		admin.AddRoutes([]rest.Route{
			{Method: http.MethodGet, Path: "/system/users", Handler: system.ListUsersHandler(svcCtx)},
			{Method: http.MethodPost, Path: "/system/users", Handler: system.CreateUserHandler(svcCtx)},
			{Method: http.MethodPatch, Path: "/system/users/{id}/state", Handler: system.SetUserStateHandler(svcCtx)},
			{Method: http.MethodPost, Path: "/system/users/{id}/reset-password", Handler: system.ResetPasswordHandler(svcCtx)},
		})
	}
}
