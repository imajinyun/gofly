package smartweb

import (
	"context"
	"testing"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Failure-mode inventory: AUTHN-029 through AUTHN-033. The compatibility
// layer accepts only code-owned IDs; the role manager receives canonical role
// and capability values, never browser-provided policy strings.
func TestService_RoleManagementMapsSmartAdminContractsToCanonicalValues(t *testing.T) {
	manager := &recordingRoleManager{
		capabilities: map[string][]authz.Capability{
			authz.RoleReader: {authz.CapabilityProjectRead},
			authz.RoleAdmin:  {authz.CapabilityProjectReadAny, authz.CapabilityAuthorizationRoleManage},
		},
		employees: map[string][]app.TenantUser{
			authz.RoleAdmin: {{UserID: 7, LoginName: "admin", DisplayName: "Gosky Admin"}},
		},
	}
	service, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{}, nil, manager)
	if err != nil {
		t.Fatalf("new smart-web service: %v", err)
	}
	ctx := auth.NewContext(context.Background(), auth.Principal{Subject: "user:admin", Claims: map[string]any{"tenant_id": "900000010"}})

	selection, serviceErr := service.RoleMenuSelection(ctx, SmartWebRoleAdminID)
	if serviceErr != nil {
		t.Fatalf("role menu selection: %v", serviceErr)
	}
	if !containsUint64(selection.SelectedMenuID, SmartWebCapabilityProjectReadAnyMenuID) || !containsUint64(selection.SelectedMenuID, SmartWebCapabilityRoleManageMenuID) {
		t.Fatalf("selected capability menu IDs = %#v, want canonical admin capabilities", selection.SelectedMenuID)
	}
	if serviceErr := service.UpdateRoleMenu(ctx, SmartWebRoleReaderID, []uint64{SmartWebCapabilityGreeterWatchMenuID}, "request-greeter-watch"); serviceErr != nil {
		t.Fatalf("update greeter watch capability: %v", serviceErr)
	}
	if got, want := manager.replacedCapabilities, []authz.Capability{authz.CapabilityGreeterWatch}; !sameCapabilities(got, want) {
		t.Fatalf("greeter watch capabilities = %#v, want %#v", got, want)
	}
	readerSelection, serviceErr := service.RoleMenuSelection(ctx, SmartWebRoleReaderID)
	if serviceErr != nil {
		t.Fatalf("reader role menu selection: %v", serviceErr)
	}
	if !containsUint64(readerSelection.SelectedMenuID, SmartWebCapabilityGreeterWatchMenuID) || countUint64(menuTreeIDs(readerSelection.MenuTreeList), SmartWebCapabilityGreeterWatchMenuID) != 1 {
		t.Fatalf("greeter watch selection = %#v, want one selectable tree leaf", readerSelection)
	}

	if serviceErr := service.UpdateRoleMenu(ctx, SmartWebRoleReaderID, []uint64{SmartWebRoleCatalogMenuID, SmartWebRolePageMenuID, SmartWebCapabilityProjectReadMenuID}, "request-menu"); serviceErr != nil {
		t.Fatalf("update role menu: %v", serviceErr)
	}
	if got, want := manager.replacedRole, authz.RoleReader; got != want {
		t.Fatalf("replaced role = %q, want %q", got, want)
	}
	if got, want := manager.replacedCapabilities, []authz.Capability{authz.CapabilityProjectRead}; !sameCapabilities(got, want) {
		t.Fatalf("replaced capabilities = %#v, want %#v", got, want)
	}
	if serviceErr := service.UpdateRoleMenu(ctx, SmartWebRoleReaderID, []uint64{999999999}, "request-invalid-menu"); serviceErr == nil || serviceErr.Code != coreerrors.CodeInvalidArgument {
		t.Fatalf("unknown menu update error = %#v, want invalid argument", serviceErr)
	}

	scopes, serviceErr := service.RoleDataScopes(ctx)
	if serviceErr != nil {
		t.Fatalf("role data scopes: %v", serviceErr)
	}
	if len(scopes) != 1 || scopes[0].DataScopeType != SmartWebProjectDataScopeType || len(scopes[0].ViewTypeList) != 2 {
		t.Fatalf("data scopes = %#v, want one Project scope with two views", scopes)
	}
	selectedScopes, serviceErr := service.RoleDataScopeSelection(ctx, SmartWebRoleAdminID)
	if serviceErr != nil {
		t.Fatalf("selected data scopes: %v", serviceErr)
	}
	if len(selectedScopes) != 1 || selectedScopes[0].ViewType != SmartWebProjectScopeAllTenant {
		t.Fatalf("selected data scopes = %#v, want all-tenant Project visibility", selectedScopes)
	}
	if serviceErr := service.UpdateRoleDataScopes(ctx, SmartWebRoleReaderID, []RoleDataScopeUpdateItem{{DataScopeType: SmartWebProjectDataScopeType, ViewType: SmartWebProjectScopeAllTenant}}, "request-scope"); serviceErr != nil {
		t.Fatalf("update data scope: %v", serviceErr)
	}
	if manager.scope != app.RoleProjectScopeAllTenant {
		t.Fatalf("mapped Project scope = %q", manager.scope)
	}

	members, serviceErr := service.RoleMembers(ctx, SmartWebRoleAdminID, 1, 20, "gosky")
	if serviceErr != nil {
		t.Fatalf("role members: %v", serviceErr)
	}
	if members.Total != 1 || len(members.List) != 1 || members.List[0].EmployeeID != 7 {
		t.Fatalf("role members = %#v, want tenant member projection", members)
	}
	if serviceErr := service.AddRoleMembers(ctx, SmartWebRoleAdminID, []uint64{8, 9}, "request-add"); serviceErr != nil {
		t.Fatalf("add role members: %v", serviceErr)
	}
	if got, want := manager.addedEmployeeIDs, []uint64{8, 9}; !sameUint64s(got, want) {
		t.Fatalf("added employee IDs = %#v, want %#v", got, want)
	}
	if serviceErr := service.RemoveRoleMembers(ctx, SmartWebRoleAdminID, []uint64{8}, "request-remove"); serviceErr != nil {
		t.Fatalf("remove role members: %v", serviceErr)
	}
	if got, want := manager.removedEmployeeIDs, []uint64{8}; !sameUint64s(got, want) {
		t.Fatalf("removed employee IDs = %#v, want %#v", got, want)
	}
}

type recordingRoleManager struct {
	scope                app.RoleProjectScope
	scopeRole            string
	capabilities         map[string][]authz.Capability
	employees            map[string][]app.TenantUser
	replacedRole         string
	replacedCapabilities []authz.Capability
	addedRole            string
	addedEmployeeIDs     []uint64
	removedRole          string
	removedEmployeeIDs   []uint64
}

func (m *recordingRoleManager) RoleCapabilities(_ context.Context, role string) ([]authz.Capability, *coreerrors.Error) {
	return append([]authz.Capability(nil), m.capabilities[role]...), nil
}

func (m *recordingRoleManager) ReplaceRoleCapabilities(_ context.Context, role string, capabilities []authz.Capability, _ string) *coreerrors.Error {
	m.replacedRole = role
	m.replacedCapabilities = append([]authz.Capability(nil), capabilities...)
	m.capabilities[role] = append([]authz.Capability(nil), capabilities...)
	return nil
}

func (m *recordingRoleManager) ListRoleMembers(_ context.Context, role string, _, _ uint64, _ string) (app.RoleMemberPage, *coreerrors.Error) {
	list := append([]app.TenantUser(nil), m.employees[role]...)
	return app.RoleMemberPage{Users: list, Total: len(list)}, nil
}

func (*recordingRoleManager) ListTenantUsers(context.Context, uint64, uint64, string) (app.RoleMemberPage, *coreerrors.Error) {
	return app.RoleMemberPage{Users: []app.TenantUser{{UserID: 7, LoginName: "admin", DisplayName: "Gosky Admin"}}, Total: 1}, nil
}

func (m *recordingRoleManager) AddRoleMembers(_ context.Context, role string, employeeIDs []uint64, _ string) *coreerrors.Error {
	m.addedRole = role
	m.addedEmployeeIDs = append([]uint64(nil), employeeIDs...)
	return nil
}

func (m *recordingRoleManager) RemoveRoleMembers(_ context.Context, role string, employeeIDs []uint64, _ string) *coreerrors.Error {
	m.removedRole = role
	m.removedEmployeeIDs = append([]uint64(nil), employeeIDs...)
	return nil
}

func containsUint64(values []uint64, want uint64) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sameCapabilities(got, want []authz.Capability) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func sameUint64s(got, want []uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func (m *recordingRoleManager) SetProjectScope(_ context.Context, role string, scope app.RoleProjectScope, _ string) *coreerrors.Error {
	m.scopeRole = role
	m.scope = scope
	return nil
}
