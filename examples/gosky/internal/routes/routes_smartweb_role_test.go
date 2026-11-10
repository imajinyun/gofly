package routes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/examples/gosky/internal/smartweb"
	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rest"
)

// Failure-mode inventory: AUTHN-025 through AUTHN-028. The existing
// SmartAdmin role view must receive only the Gosky-owned read model, and every
// compatibility endpoint must have the same verified-admin and Casbin
// capability boundary as the menu that advertises the page.
func TestRegisterRoutesSmartWebRoleCompatibilityContract(t *testing.T) {
	policy := &routeSmartWebRoleAuthorizer{}
	compatibilityService, err := smartweb.NewService(&routeSmartWebAuthenticator{}, policy, nil, &routeSmartWebRoleManager{})
	if err != nil {
		t.Fatalf("new smart-web compatibility service: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&routeAuthenticationService{})
	serviceContext.SetSmartWebService(compatibilityService)
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	adminToken := routeSmartWebRoleToken(t, authn.RealmAdmin)
	frontToken := routeSmartWebRoleToken(t, authn.RealmFront)
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		token      string
		wantStatus int
		assert     func(*testing.T, []byte)
	}{
		{name: "missing credentials are rejected", method: http.MethodGet, path: "/role/getAll", wantStatus: http.StatusUnauthorized},
		{name: "front identity is rejected", method: http.MethodGet, path: "/role/getAll", token: frontToken, wantStatus: http.StatusUnauthorized},
		{
			name: "admin reads fixed roles", method: http.MethodGet, path: "/role/getAll", token: adminToken, wantStatus: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var response smartweb.ResponseDTO[[]smartweb.RoleVO]
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode role response: %v", err)
				}
				if !response.OK || response.Code != 0 || len(response.Data) != 3 || response.Data[0].RoleCode != authz.RoleReader || response.Data[2].RoleCode != authz.RoleAdmin {
					t.Fatalf("role response = %#v, want fixed reader/writer/admin response", response)
				}
			},
		},
		{
			name: "admin reads fixed role menu", method: http.MethodGet, path: "/role/menu/getRoleSelectedMenu/810000103", token: adminToken, wantStatus: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var response smartweb.ResponseDTO[smartweb.RoleMenuSelectionVO]
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode role menu response: %v", err)
				}
				if !response.OK || len(response.Data.MenuTreeList) != 21 || len(response.Data.SelectedMenuID) < 3 {
					t.Fatalf("role menu response = %#v, want access-control, organization, and support-page roots", response)
				}
			},
		},
		{
			name: "admin reads Gosky Project data scopes", method: http.MethodGet, path: "/dataScope/list", token: adminToken, wantStatus: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var response smartweb.ResponseDTO[[]any]
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode data-scope response: %v", err)
				}
				if !response.OK || response.Data == nil || len(response.Data) != 1 {
					t.Fatalf("data-scope response = %#v, want Project data-scope array", response)
				}
			},
		},
		{
			name: "admin reads selected Project data scope", method: http.MethodGet, path: "/role/dataScope/getRoleDataScopeList/810000103", token: adminToken, wantStatus: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var response smartweb.ResponseDTO[[]any]
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode selected data-scope response: %v", err)
				}
				if !response.OK || response.Data == nil || len(response.Data) != 1 {
					t.Fatalf("selected data-scope response = %#v, want Project selection", response)
				}
			},
		},
		{
			name: "admin reads role members", method: http.MethodPost, path: "/role/employee/queryEmployee", body: `{"roleId":810000103,"pageNum":1,"pageSize":20}`, token: adminToken, wantStatus: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var response smartweb.ResponseDTO[smartweb.RoleEmployeePageVO]
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode role employee response: %v", err)
				}
				if !response.OK || response.Data.List == nil || len(response.Data.List) != 1 || response.Data.Total != 1 {
					t.Fatalf("role employee response = %#v, want role member page", response)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, http.NoBody)
			if tt.body != "" {
				req = httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
				req.Header.Set("Content-Type", "application/json")
			}
			if tt.token != "" {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(tt.token))
			}
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.assert != nil {
				tt.assert(t, rec.Body.Bytes())
			}
		})
	}
	if policy.calls != 5 {
		t.Fatalf("role-management authorization calls = %d, want 5", policy.calls)
	}
}

func TestRegisterRoutesSmartWebRoleCompatibilityDeniesCasbinFailure(t *testing.T) {
	policy := &routeSmartWebRoleAuthorizer{err: coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")}
	compatibilityService, err := smartweb.NewService(&routeSmartWebAuthenticator{}, policy, nil, &routeSmartWebRoleManager{})
	if err != nil {
		t.Fatalf("new smart-web compatibility service: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&routeAuthenticationService{})
	serviceContext.SetSmartWebService(compatibilityService)
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	req := httptest.NewRequest(http.MethodGet, "/role/getAll", nil)
	req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(routeSmartWebRoleToken(t, authn.RealmAdmin)))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if policy.calls != 1 {
		t.Fatalf("role-management authorization calls = %d, want 1", policy.calls)
	}
}

// Failure-mode inventory: AUTHN-034 and AUTHN-035. The Menu Catalog follows
// the same AdminInterceptor and authorization boundary as its advertised
// pages. Direct mutation requests fail explicitly so they cannot become an
// accidental writable tenant menu store when the browser hides its controls.
func TestRegisterRoutesSmartWebMenuCatalogCompatibility(t *testing.T) {
	policy := &routeSmartWebRoleAuthorizer{}
	manager := &routeSmartWebRoleManager{}
	compatibilityService, err := smartweb.NewService(&routeSmartWebAuthenticator{}, policy, nil, manager)
	if err != nil {
		t.Fatalf("new smart-web compatibility service: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&routeAuthenticationService{})
	serviceContext.SetSmartWebService(compatibilityService)
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)

	adminToken := routeSmartWebRoleToken(t, authn.RealmAdmin)
	frontToken := routeSmartWebRoleToken(t, authn.RealmFront)
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		token      string
		wantStatus int
		assert     func(*testing.T, []byte)
	}{
		{name: "missing credentials are rejected", method: http.MethodGet, path: "/menu/query", wantStatus: http.StatusUnauthorized},
		{name: "front identity is rejected", method: http.MethodGet, path: "/menu/query", token: frontToken, wantStatus: http.StatusUnauthorized},
		{
			name: "admin reads code-owned menu catalog", method: http.MethodGet, path: "/menu/query", token: adminToken, wantStatus: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var response smartweb.ResponseDTO[[]smartweb.MenuVO]
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode menu catalog response: %v", err)
				}
				if !response.OK || len(response.Data) != 26+len(authz.ManagedCapabilities()) || response.Data[2].Path != "/system/menu" {
					t.Fatalf("menu catalog response = %#v, want twenty-six structural rows plus capability points", response)
				}
			},
		},
		{
			name: "admin reads menu-only tree", method: http.MethodGet, path: "/menu/tree?onlyMenu=true", token: adminToken, wantStatus: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var response smartweb.ResponseDTO[[]smartweb.MenuVO]
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode menu tree response: %v", err)
				}
				if !response.OK || len(response.Data) != 21 || len(response.Data[0].Children) != 2 || len(response.Data[1].Children) != 3 || len(response.Data[2].Children) != 0 || len(response.Data[3].Children) != 0 || len(response.Data[4].Children) != 0 || len(response.Data[5].Children) != 0 || len(response.Data[6].Children) != 0 || len(response.Data[7].Children) != 0 || len(response.Data[8].Children) != 0 || len(response.Data[9].Children) != 0 || len(response.Data[10].Children) != 0 || len(response.Data[11].Children) != 0 || len(response.Data[12].Children) != 0 || len(response.Data[13].Children) != 0 || len(response.Data[14].Children) != 0 || len(response.Data[15].Children) != 0 || len(response.Data[16].Children) != 0 || len(response.Data[17].Children) != 0 || len(response.Data[18].Children) != 0 || len(response.Data[19].Children) != 0 {
					t.Fatalf("menu-only response = %#v, want catalog and support-page roots", response)
				}
			},
		},
		{
			name: "admin reads complete tree", method: http.MethodGet, path: "/menu/tree?onlyMenu=false", token: adminToken, wantStatus: http.StatusOK,
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var response smartweb.ResponseDTO[[]smartweb.MenuVO]
				if err := json.Unmarshal(body, &response); err != nil {
					t.Fatalf("decode complete menu tree response: %v", err)
				}
				if !response.OK || len(response.Data) != 21 || len(response.Data[0].Children) != 2 || len(response.Data[0].Children[0].Children) != len(authz.ManagedCapabilities()) || len(response.Data[20].Children) != 0 {
					t.Fatalf("complete menu tree = %#v, want twenty-one roots with all capability points", response)
				}
			},
		},
		{name: "admin reads no raw authorization URLs", method: http.MethodGet, path: "/menu/auth/url", token: adminToken, wantStatus: http.StatusOK},
		{name: "menu add without a tenant catalog is rejected", method: http.MethodPost, path: "/menu/add", body: `{"menuName":"Custom","menuType":2,"parentId":810000001}`, token: adminToken, wantStatus: http.StatusBadRequest, assert: assertFailedPrecondition},
		{name: "menu update without a tenant catalog is rejected", method: http.MethodPost, path: "/menu/update", body: `{"menuId":820000001,"menuName":"Custom","menuType":2,"parentId":810000001}`, token: adminToken, wantStatus: http.StatusBadRequest, assert: assertFailedPrecondition},
		{name: "menu deletion without a tenant catalog is rejected", method: http.MethodGet, path: "/menu/batchDelete?ids=820000001", token: adminToken, wantStatus: http.StatusBadRequest, assert: assertFailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestBody io.Reader = http.NoBody
			if tt.body != "" {
				requestBody = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, requestBody)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tt.token != "" {
				req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(tt.token))
			}
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.assert != nil {
				tt.assert(t, rec.Body.Bytes())
			}
		})
	}
	if len(manager.capabilities) != 0 || len(manager.added) != 0 || len(manager.removed) != 0 {
		t.Fatalf("menu mutation reached role manager: %#v", manager)
	}
}

func assertFailedPrecondition(t *testing.T, body []byte) {
	t.Helper()
	var response struct {
		Code coreerrors.Code `json:"code"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode failed-precondition response: %v", err)
	}
	if response.Code != coreerrors.CodeFailedPrecondition {
		t.Fatalf("error code = %q, want %q; body=%s", response.Code, coreerrors.CodeFailedPrecondition, body)
	}
}

// Failure-mode inventory: AUTHN-029 through AUTHN-033. SmartAdmin mutation
// payloads are preserved, while only code-owned role/capability/employee IDs
// reach the compatibility manager.
func TestRegisterRoutesSmartWebRoleManagementMutations(t *testing.T) {
	policy := &routeSmartWebRoleAuthorizer{}
	manager := &routeSmartWebRoleManager{}
	compatibilityService, err := smartweb.NewService(&routeSmartWebAuthenticator{}, policy, nil, manager)
	if err != nil {
		t.Fatalf("new smart-web compatibility service: %v", err)
	}
	serviceContext := svc.NewServiceContext(config.Config{})
	serviceContext.SetAuthenticationService(&routeAuthenticationService{})
	serviceContext.SetSmartWebService(compatibilityService)
	server := rest.MustNewServer(rest.Config{})
	RegisterRoutes(server, serviceContext)
	token := routeSmartWebRoleToken(t, authn.RealmAdmin)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{name: "update fixed role capabilities", method: http.MethodPost, path: "/role/menu/updateRoleMenu", body: `{"roleId":810000101,"menuIdList":[810000001,810000002,810000011]}`, wantStatus: http.StatusOK},
		{name: "reject unknown capability menu", method: http.MethodPost, path: "/role/menu/updateRoleMenu", body: `{"roleId":810000101,"menuIdList":[999999999]}`, wantStatus: http.StatusBadRequest},
		{name: "update Project data scope", method: http.MethodPost, path: "/role/dataScope/updateRoleDataScopeList", body: `{"roleId":810000101,"dataScopeItemList":[{"dataScopeType":1,"viewType":2}]}`, wantStatus: http.StatusOK},
		{name: "add tenant employees", method: http.MethodPost, path: "/role/employee/batchAddRoleEmployee", body: `{"roleId":810000103,"employeeIdList":[7,8]}`, wantStatus: http.StatusOK},
		{name: "remove tenant employees", method: http.MethodPost, path: "/role/employee/batchRemoveRoleEmployee", body: `{"roleId":810000103,"employeeIdList":[8]}`, wantStatus: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(auth.AuthorizationHeader, auth.BearerValue(token))
			rec := httptest.NewRecorder()
			server.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
	if manager.scope != app.RoleProjectScopeAllTenant {
		t.Fatalf("mapped Project scope = %q", manager.scope)
	}
	if got, want := manager.added, []uint64{7, 8}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("added employees = %#v, want %#v", got, want)
	}
	if got, want := manager.removed, []uint64{8}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("removed employees = %#v, want %#v", got, want)
	}
}

type routeSmartWebRoleAuthorizer struct {
	err   *coreerrors.Error
	calls int
}

func (a *routeSmartWebRoleAuthorizer) Authorize(ctx context.Context, capability, action string) *coreerrors.Error {
	a.calls++
	principal, ok := auth.FromContext(ctx)
	if !ok || principal.Subject != "admin-demo" || principal.Claims["tenant_id"] != "1001" {
		return coreerrors.New(coreerrors.CodeUnauthenticated, "missing authenticated principal")
	}
	if !slices.Contains([]authz.Capability{authz.CapabilityAuthorizationRoleRead, authz.CapabilityAuthorizationRoleManage, authz.CapabilityAuthorizationRoleScopeManage, authz.CapabilityAuthorizationRoleMemberAdd, authz.CapabilityAuthorizationRoleMemberRemove, authz.CapabilitySystemMenuManage}, authz.Capability(capability)) || action != authz.CapabilityAction {
		return coreerrors.New(coreerrors.CodeUnavailable, "unexpected role capability")
	}
	return a.err
}

type routeSmartWebRoleManager struct {
	scope        app.RoleProjectScope
	capabilities []authz.Capability
	added        []uint64
	removed      []uint64
}

func (*routeSmartWebRoleManager) RoleCapabilities(_ context.Context, role string) ([]authz.Capability, *coreerrors.Error) {
	if role == authz.RoleAdmin {
		return []authz.Capability{authz.CapabilityProjectReadAny, authz.CapabilityAuthorizationRoleManage}, nil
	}
	return []authz.Capability{authz.CapabilityProjectRead}, nil
}

func (m *routeSmartWebRoleManager) ReplaceRoleCapabilities(_ context.Context, _ string, capabilities []authz.Capability, _ string) *coreerrors.Error {
	m.capabilities = append([]authz.Capability(nil), capabilities...)
	return nil
}

func (*routeSmartWebRoleManager) ListRoleMembers(context.Context, string, uint64, uint64, string) (app.RoleMemberPage, *coreerrors.Error) {
	return app.RoleMemberPage{Users: []app.TenantUser{{UserID: 1, LoginName: "admin", DisplayName: "Admin"}}, Total: 1}, nil
}

func (*routeSmartWebRoleManager) ListTenantUsers(context.Context, uint64, uint64, string) (app.RoleMemberPage, *coreerrors.Error) {
	return app.RoleMemberPage{Users: []app.TenantUser{{UserID: 1, LoginName: "admin", DisplayName: "Admin"}}, Total: 1}, nil
}

func (m *routeSmartWebRoleManager) AddRoleMembers(_ context.Context, _ string, employeeIDs []uint64, _ string) *coreerrors.Error {
	m.added = append([]uint64(nil), employeeIDs...)
	return nil
}

func (m *routeSmartWebRoleManager) RemoveRoleMembers(_ context.Context, _ string, employeeIDs []uint64, _ string) *coreerrors.Error {
	m.removed = append([]uint64(nil), employeeIDs...)
	return nil
}

func routeSmartWebRoleToken(t *testing.T, realm authn.Realm) string {
	t.Helper()
	return sessionNavigationToken(t, realm)
}

func (m *routeSmartWebRoleManager) SetProjectScope(_ context.Context, _ string, scope app.RoleProjectScope, _ string) *coreerrors.Error {
	m.scope = scope
	return nil
}
