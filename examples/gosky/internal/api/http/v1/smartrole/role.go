// Package smartrole exposes the SmartAdmin role-page compatibility surface.
// Browser identifiers are translated to code-owned Gosky roles/capabilities
// before a controlled policy or identity mutation can occur.
package smartrole

import (
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// ListRolesHandler returns Gosky's fixed reader, writer, and admin catalog.
// The service enforces the same authorization capability as the menu
// projection, so this handler never treats a browser role name as authority.
func ListRolesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		roles, err := service.RoleDefinitions(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(roles))
	}
}

// RoleMenuSelectionHandler returns the code-owned navigation tree and fixed
// selection for one Gosky Casbin role. The path parameter is an identifier
// only; it is parsed before reaching the service and never becomes a Casbin
// policy key.
func RoleMenuSelectionHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		roleID, err := smartWebRoleID(ctx.PathValue("id"))
		if err != nil {
			ctx.Error(err)
			return
		}
		selection, serviceErr := service.RoleMenuSelection(ctx.Request.Context(), roleID)
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(selection))
	}
}

// DataScopesHandler returns Gosky's typed Project data-scope catalog.
func DataScopesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		scopes, err := service.RoleDataScopes(ctx.Request.Context())
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(scopes))
	}
}

// RoleDataScopeSelectionHandler returns the selected Project scope for a
// known immutable Gosky role.
func RoleDataScopeSelectionHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		roleID, err := smartWebRoleID(ctx.PathValue("id"))
		if err != nil {
			ctx.Error(err)
			return
		}
		scopes, serviceErr := service.RoleDataScopeSelection(ctx.Request.Context(), roleID)
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(scopes))
	}
}

type roleMembersRequest struct {
	RoleID   uint64 `json:"roleId"`
	PageNum  uint64 `json:"pageNum"`
	PageSize uint64 `json:"pageSize"`
	Keywords string `json:"keywords"`
}

type tenantEmployeesRequest struct {
	PageNum  uint64 `json:"pageNum"`
	PageSize uint64 `json:"pageSize"`
	Keyword  string `json:"keyword"`
}

type roleMenuUpdateRequest struct {
	RoleID     uint64   `json:"roleId"`
	MenuIDList []uint64 `json:"menuIdList"`
}

type roleDataScopeUpdateRequest struct {
	RoleID            uint64                             `json:"roleId"`
	DataScopeItemList []smartweb.RoleDataScopeUpdateItem `json:"dataScopeItemList"`
}

type roleEmployeeUpdateRequest struct {
	RoleID         uint64   `json:"roleId"`
	EmployeeIDList []uint64 `json:"employeeIdList"`
}

// RoleMembersHandler lists active same-tenant identities that have the fixed
// role. It never receives a Casbin subject from the browser.
func RoleMembersHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		var request roleMembersRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "role member query is invalid"))
			return
		}
		response, err := service.RoleMembers(ctx.Request.Context(), request.RoleID, request.PageNum, request.PageSize, request.Keywords)
		if err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(response))
	}
}

func AllRoleMembersHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		roleID, err := smartWebRoleID(ctx.PathValue("id"))
		if err != nil {
			ctx.Error(err)
			return
		}
		page, serviceErr := service.RoleMembers(ctx.Request.Context(), roleID, 1, 100, "")
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page.List))
	}
}

// TenantEmployeesHandler supports SmartAdmin's existing employee picker used
// by the role-member add dialog. It is deliberately scoped to the verified
// tenant and returns no credential or cross-tenant identity data.
func TenantEmployeesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		var request tenantEmployeesRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "employee query is invalid"))
			return
		}
		page, serviceErr := service.TenantEmployees(ctx.Request.Context(), request.PageNum, request.PageSize, request.Keyword)
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(page))
	}
}

func UpdateRoleMenuHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		var request roleMenuUpdateRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "role menu update is invalid"))
			return
		}
		if err := service.UpdateRoleMenu(ctx.Request.Context(), request.RoleID, request.MenuIDList, ctx.Request.Header.Get("X-Request-ID")); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func UpdateRoleDataScopesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		var request roleDataScopeUpdateRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "role data-scope update is invalid"))
			return
		}
		if err := service.UpdateRoleDataScopes(ctx.Request.Context(), request.RoleID, request.DataScopeItemList, ctx.Request.Header.Get("X-Request-ID")); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func AddRoleEmployeesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return roleEmployeeMutationHandler(svcCtx, true)
}

func RemoveRoleEmployeesHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return roleEmployeeMutationHandler(svcCtx, false)
}

func RemoveRoleEmployeeHandler(svcCtx *svc.ServiceContext) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		roleID, roleErr := smartWebRoleID(ctx.Request.URL.Query().Get("roleId"))
		if roleErr != nil {
			ctx.Error(roleErr)
			return
		}
		employeeID, employeeErr := smartWebRoleID(ctx.Request.URL.Query().Get("employeeId"))
		if employeeErr != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "role employee update is invalid"))
			return
		}
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		if err := service.RemoveRoleMembers(ctx.Request.Context(), roleID, []uint64{employeeID}, ctx.Request.Header.Get("X-Request-ID")); err != nil {
			ctx.Error(err)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func roleEmployeeMutationHandler(svcCtx *svc.ServiceContext, add bool) rest.HandlerFunc {
	return func(ctx *rest.Context) {
		service := svcCtx.CurrentSmartWebService()
		if service == nil {
			ctx.Error(coreerrors.New(coreerrors.CodeUnavailable, "smart-web compatibility is unavailable"))
			return
		}
		var request roleEmployeeUpdateRequest
		if err := ctx.Bind(&request); err != nil {
			ctx.Error(coreerrors.New(coreerrors.CodeInvalidArgument, "role employee update is invalid"))
			return
		}
		var serviceErr *coreerrors.Error
		if add {
			serviceErr = service.AddRoleMembers(ctx.Request.Context(), request.RoleID, request.EmployeeIDList, ctx.Request.Header.Get("X-Request-ID"))
		} else {
			serviceErr = service.RemoveRoleMembers(ctx.Request.Context(), request.RoleID, request.EmployeeIDList, ctx.Request.Header.Get("X-Request-ID"))
		}
		if serviceErr != nil {
			ctx.Error(serviceErr)
			return
		}
		ctx.JSON(http.StatusOK, smartweb.OK(""))
	}
}

func smartWebRoleID(raw string) (uint64, *coreerrors.Error) {
	roleID, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil || roleID == 0 {
		return 0, coreerrors.New(coreerrors.CodeInvalidArgument, "role id is invalid")
	}
	return roleID, nil
}
