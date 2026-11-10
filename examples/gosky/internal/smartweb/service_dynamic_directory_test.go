package smartweb

import (
	"context"
	"testing"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Failure-mode inventory for the tenant directory wiring (DYNA-COMPAT):
//
//  1. DYNA-COMPAT-01: with a directory wired, /menu/query and /menu/tree must
//     project tenant rows (including tenant-created pages) instead of the
//     code-owned fallback catalog, otherwise the unchanged Menu Catalog page
//     cannot display rows the operator just created.
//  2. DYNA-COMPAT-02: the target-role permission tree must never expose
//     current-operator webPerms buttons as grantable leaves; saving one is
//     rejected by the repository transaction.
//  3. DYNA-COMPAT-03: role-permission saves for tenant-managed roles must
//     reach the directory's combined role_menus/Casbin transaction; fixed
//     roles use the same path once a directory is present.
//  4. DYNA-COMPAT-04: data-scope reads/writes and role-member removal must
//     resolve tenant-managed role IDs through the directory, not only the
//     three hardcoded fixed role IDs.
//  5. DYNA-COMPAT-05: login navigation keeps filtering every seeded operator
//     button through its own capability; the six newly seeded role/menu
//     buttons must disappear when their capability is denied, while
//     unknown tenant webPerms buttons remain directory-controlled.
func TestService_DirectoryBackedMenuCatalog(t *testing.T) {
	directory := &recordingDirectory{menus: directorySeedMenus()}
	service, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{}, nil)
	if err != nil {
		t.Fatalf("new smart-web service: %v", err)
	}
	service.SetDirectory(directory)
	ctx := auth.NewContext(context.Background(), auth.Principal{Subject: "user:admin", Claims: map[string]any{"tenant_id": "900000010"}})

	catalog, serviceErr := service.MenuCatalog(ctx)
	if serviceErr != nil {
		t.Fatalf("menu catalog: %v", serviceErr)
	}
	if len(catalog) != len(directorySeedMenus()) {
		t.Fatalf("menu catalog len = %d, want all %d tenant rows", len(catalog), len(directorySeedMenus()))
	}
	if !containsUint64(menuTreeIDs(catalog), 820000041) {
		t.Fatalf("menu catalog = %#v, want tenant-created page included", catalog)
	}

	fullTree, treeErr := service.MenuTree(ctx, false)
	if treeErr != nil {
		t.Fatalf("menu tree: %v", treeErr)
	}
	ids := menuTreeIDs(fullTree)
	if !containsUint64(ids, roleAddButtonID) || !containsUint64(ids, 820000041) {
		t.Fatalf("full tree ids = %#v, want seeded button and custom page", ids)
	}

	menuOnlyTree, menuOnlyErr := service.MenuTree(ctx, true)
	if menuOnlyErr != nil {
		t.Fatalf("menu-only tree: %v", menuOnlyErr)
	}
	for _, id := range []uint64{SmartWebCapabilityRoleManageMenuID, roleAddButtonID, menuAddButtonID} {
		if containsUint64(menuTreeIDs(menuOnlyTree), id) {
			t.Fatalf("menu-only tree contains function point %d", id)
		}
	}
	if !containsUint64(menuTreeIDs(menuOnlyTree), 820000041) {
		t.Fatalf("menu-only tree drops tenant page 820000041: %#v", menuOnlyTree)
	}
}

func TestService_DirectoryBackedRoleGrants(t *testing.T) {
	const dynamicRoleID uint64 = 910000201
	directory := &recordingDirectory{
		menus: directorySeedMenus(),
		roles: []app.RoleRecord{{ID: dynamicRoleID, Code: "ops", Name: "Operations", State: "active"}},
		selection: map[uint64]directorySelection{
			dynamicRoleID: {records: directorySeedMenus(), selected: []uint64{SmartWebCapabilityRoleManageMenuID}},
		},
	}
	manager := &recordingRoleManager{
		capabilities: map[string][]authz.Capability{
			"ops":           {authz.CapabilityAuthorizationRoleManage},
			authz.RoleAdmin: {authz.CapabilityAuthorizationRoleManage},
		},
	}
	service, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{}, nil, manager)
	if err != nil {
		t.Fatalf("new smart-web service: %v", err)
	}
	service.SetDirectory(directory)
	ctx := auth.NewContext(context.Background(), auth.Principal{Subject: "user:admin", Claims: map[string]any{"tenant_id": "900000010"}})

	selection, serviceErr := service.RoleMenuSelection(ctx, dynamicRoleID)
	if serviceErr != nil {
		t.Fatalf("dynamic role selection: %v", serviceErr)
	}
	treeIDs := menuTreeIDs(selection.MenuTreeList)
	if !containsUint64(treeIDs, SmartWebCapabilityRoleManageMenuID) {
		t.Fatalf("target-role tree = %#v, want granted capability leaf", treeIDs)
	}
	for _, id := range []uint64{roleMenuUpdateButtonID, roleAddButtonID, roleUpdateButtonID, roleDeleteButtonID, menuAddButtonID, menuUpdateButtonID, menuDeleteButtonID} {
		if containsUint64(treeIDs, id) {
			t.Fatalf("target-role tree leaks current-operator button %d", id)
		}
	}
	if !containsUint64(selection.SelectedMenuID, SmartWebCapabilityRoleManageMenuID) {
		t.Fatalf("dynamic role selected ids = %#v", selection.SelectedMenuID)
	}

	menuIDs := []uint64{SmartWebRoleCatalogMenuID, SmartWebRolePageMenuID, SmartWebCapabilityRoleManageMenuID}
	if err := service.UpdateRoleMenu(ctx, dynamicRoleID, menuIDs, "request-dynamic-grants"); err != nil {
		t.Fatalf("update dynamic role grants: %v", err)
	}
	if directory.replacedRoleID != dynamicRoleID || !sameUint64s(directory.replacedMenuIDs, menuIDs) {
		t.Fatalf("directory grants = (%d, %#v), want (%d, %#v)", directory.replacedRoleID, directory.replacedMenuIDs, dynamicRoleID, menuIDs)
	}
	if manager.replacedRole != "" {
		t.Fatalf("fixed role manager invoked for dynamic role %q", manager.replacedRole)
	}

	if err := service.UpdateRoleMenu(ctx, SmartWebRoleAdminID, menuIDs, "request-fixed-grants"); err != nil {
		t.Fatalf("update fixed role grants through directory: %v", err)
	}
	if directory.replacedRoleID != SmartWebRoleAdminID {
		t.Fatalf("directory grants role id = %d, want fixed admin %d", directory.replacedRoleID, SmartWebRoleAdminID)
	}
	if manager.replacedRole != "" {
		t.Fatalf("fixed role manager must not run alongside directory: %q", manager.replacedRole)
	}
}

func TestService_DirectoryBackedDynamicRoleScopeAndMembers(t *testing.T) {
	const dynamicRoleID uint64 = 910000202
	directory := &recordingDirectory{
		menus: directorySeedMenus(),
		roles: []app.RoleRecord{{ID: dynamicRoleID, Code: "auditor", Name: "Auditor", State: "active"}},
	}
	manager := &recordingRoleManager{
		capabilities: map[string][]authz.Capability{
			"auditor": {authz.CapabilityProjectReadAny},
		},
		employees: map[string][]app.TenantUser{
			"auditor": {{UserID: 8, LoginName: "auditor-user", DisplayName: "Auditor User"}},
		},
	}
	service, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{}, nil, manager)
	if err != nil {
		t.Fatalf("new smart-web service: %v", err)
	}
	service.SetDirectory(directory)
	ctx := auth.NewContext(context.Background(), auth.Principal{Subject: "user:admin", Claims: map[string]any{"tenant_id": "900000010"}})

	scopes, scopeErr := service.RoleDataScopeSelection(ctx, dynamicRoleID)
	if scopeErr != nil {
		t.Fatalf("dynamic role data scope selection: %v", scopeErr)
	}
	if len(scopes) != 1 || scopes[0].ViewType != SmartWebProjectScopeAllTenant {
		t.Fatalf("dynamic role scopes = %#v, want all-tenant Project read", scopes)
	}
	if err := service.UpdateRoleDataScopes(ctx, dynamicRoleID,
		[]RoleDataScopeUpdateItem{{DataScopeType: SmartWebProjectDataScopeType, ViewType: SmartWebProjectScopeRelated}}, "request-dynamic-scope"); err != nil {
		t.Fatalf("update dynamic role scope: %v", err)
	}
	if manager.scope != app.RoleProjectScopeRelated || manager.scopeRole != "auditor" {
		t.Fatalf("scope write = (%q, %q), want auditor related", manager.scopeRole, manager.scope)
	}

	members, membersErr := service.RoleMembers(ctx, dynamicRoleID, 1, 20, "")
	if membersErr != nil {
		t.Fatalf("dynamic role members: %v", membersErr)
	}
	if members.Total != 1 || members.List[0].EmployeeID != 8 {
		t.Fatalf("dynamic role members = %#v", members)
	}
	if err := service.RemoveRoleMembers(ctx, dynamicRoleID, []uint64{8}, "request-dynamic-remove"); err != nil {
		t.Fatalf("remove dynamic role members: %v", err)
	}
	if manager.removedRole != "auditor" || !sameUint64s(manager.removedEmployeeIDs, []uint64{8}) {
		t.Fatalf("member removal = (%q, %#v), want auditor [8]", manager.removedRole, manager.removedEmployeeIDs)
	}
}

func TestService_DirectoryNavigationFiltersNewOperatorButtons(t *testing.T) {
	directory := &recordingDirectory{
		navMenus: []app.MenuRecord{
			{ID: SmartWebRoleCatalogMenuID, Name: "Access Control", Type: SmartWebMenuTypeCatalog, Sort: 200, Visible: true, System: true},
			{ID: SmartWebRolePageMenuID, ParentID: SmartWebRoleCatalogMenuID, Name: "Role Management", Type: SmartWebMenuTypePage, Sort: 201, Path: "/system/role", Component: "system/role/index.vue", Visible: true, System: true},
			{ID: SmartWebMenuCatalogPageMenuID, ParentID: SmartWebRoleCatalogMenuID, Name: "Menu Catalog", Type: SmartWebMenuTypePage, Sort: 202, Path: "/system/menu", Component: "system/menu/menu-list.vue", Visible: true, System: true},
			{ID: roleAddButtonID, ParentID: SmartWebRolePageMenuID, Name: "Add role", Type: SmartWebMenuTypePoint, Sort: 208, WebPerms: "system:role:add", Visible: true, System: true},
			{ID: menuAddButtonID, ParentID: SmartWebMenuCatalogPageMenuID, Name: "Add menu", Type: SmartWebMenuTypePoint, Sort: 301, WebPerms: "system:menu:add", Visible: true, System: true},
			{ID: 820000999, ParentID: SmartWebMenuCatalogPageMenuID, Name: "Tenant button", Type: SmartWebMenuTypePoint, Sort: 302, WebPerms: "biz:custom:button", Visible: true},
		},
	}
	identity := authn.SmartWebIdentity{Subject: "user:admin", TenantID: 900000010, LoginName: "admin"}

	allowed, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{}, nil)
	if err != nil {
		t.Fatalf("new allowed service: %v", err)
	}
	allowed.SetDirectory(directory)
	ctx := auth.NewContext(context.Background(), auth.Principal{Subject: identity.Subject, Claims: map[string]any{"tenant_id": "900000010"}})
	menus, navErr := allowed.MenusForIdentity(ctx, identity)
	if navErr != nil {
		t.Fatalf("allowed navigation: %v", navErr)
	}
	ids := menuTreeIDs(menus)
	for _, id := range []uint64{roleAddButtonID, menuAddButtonID, 820000999} {
		if !containsUint64(ids, id) {
			t.Fatalf("allowed navigation ids = %#v, want button %d", ids, id)
		}
	}

	denied, err := NewService(roleMenuAuthenticator{}, capabilityAuthorizer{
		denied: map[authz.Capability]bool{authz.CapabilityAuthorizationRoleManage: true},
	}, nil)
	if err != nil {
		t.Fatalf("new denied service: %v", err)
	}
	denied.SetDirectory(directory)
	menus, navErr = denied.MenusForIdentity(ctx, identity)
	if navErr != nil {
		t.Fatalf("denied navigation: %v", navErr)
	}
	ids = menuTreeIDs(menus)
	if containsUint64(ids, roleAddButtonID) {
		t.Fatalf("role add button leaked without role manage: %#v", ids)
	}
	if !containsUint64(ids, menuAddButtonID) {
		t.Fatalf("menu add button hidden for menu-capable operator: %#v", ids)
	}
	if !containsUint64(ids, 820000999) {
		t.Fatalf("tenant button must remain directory controlled: %#v", ids)
	}
}

// capabilityAuthorizer models the policy decision port. When allowed is set,
// every capability outside the set is denied; otherwise only the denied set
// is rejected. err simulates an unavailable decision and must fail closed.
type capabilityAuthorizer struct {
	denied  map[authz.Capability]bool
	allowed map[authz.Capability]bool
	err     *coreerrors.Error
}

func (a capabilityAuthorizer) Authorize(_ context.Context, capability string, _ string) *coreerrors.Error {
	if a.err != nil {
		return a.err
	}
	requested := authz.Capability(capability)
	if a.allowed != nil {
		if !a.allowed[requested] {
			return coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
		}
		return nil
	}
	if a.denied[requested] {
		return coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
	}
	return nil
}

type directorySelection struct {
	records  []app.MenuRecord
	selected []uint64
}

type recordingDirectory struct {
	menus           []app.MenuRecord
	roles           []app.RoleRecord
	navMenus        []app.MenuRecord
	selection       map[uint64]directorySelection
	replacedRoleID  uint64
	replacedMenuIDs []uint64
}

func (d *recordingDirectory) ManagementNavigation(context.Context) ([]app.MenuRecord, *coreerrors.Error) {
	return d.navMenus, nil
}

func (d *recordingDirectory) ActiveRoles(context.Context) ([]app.RoleRecord, *coreerrors.Error) {
	return d.roles, nil
}

func (d *recordingDirectory) RoleMenuSelection(_ context.Context, roleID uint64) (app.RoleRecord, []app.MenuRecord, []uint64, *coreerrors.Error) {
	selection, ok := d.selection[roleID]
	if !ok {
		return app.RoleRecord{}, nil, nil, coreerrors.New(coreerrors.CodeNotFound, "role not found")
	}
	return app.RoleRecord{ID: roleID}, selection.records, append([]uint64(nil), selection.selected...), nil
}

func (d *recordingDirectory) ReplaceRoleGrants(_ context.Context, roleID uint64, menuIDs []uint64) *coreerrors.Error {
	d.replacedRoleID = roleID
	d.replacedMenuIDs = append([]uint64(nil), menuIDs...)
	return nil
}

func (d *recordingDirectory) ListMenus(_ context.Context, _ bool) ([]app.MenuRecord, *coreerrors.Error) {
	return d.menus, nil
}

func (*recordingDirectory) CreateMenu(_ context.Context, input app.MenuInput) (app.MenuRecord, *coreerrors.Error) {
	return app.MenuRecord{Name: input.Name}, nil
}

func (*recordingDirectory) UpdateMenu(context.Context, uint64, app.MenuInput) *coreerrors.Error {
	return nil
}
func (*recordingDirectory) DeleteMenu(context.Context, uint64) *coreerrors.Error { return nil }
func (*recordingDirectory) CreateRole(_ context.Context, input app.RoleInput) (app.RoleRecord, *coreerrors.Error) {
	return app.RoleRecord{Code: input.Code, Name: input.Name}, nil
}
func (*recordingDirectory) UpdateRole(context.Context, uint64, app.RoleUpdate) *coreerrors.Error {
	return nil
}
func (*recordingDirectory) DeleteRole(context.Context, uint64) *coreerrors.Error { return nil }

func directorySeedMenus() []app.MenuRecord {
	return []app.MenuRecord{
		{ID: SmartWebRoleCatalogMenuID, Name: "Access Control", Type: SmartWebMenuTypeCatalog, Sort: 200, Visible: true, System: true},
		{ID: SmartWebRolePageMenuID, ParentID: SmartWebRoleCatalogMenuID, Name: "Role Management", Type: SmartWebMenuTypePage, Sort: 201, Path: "/system/role", Component: "system/role/index.vue", Visible: true, System: true},
		{ID: SmartWebMenuCatalogPageMenuID, ParentID: SmartWebRoleCatalogMenuID, Name: "Menu Catalog", Type: SmartWebMenuTypePage, Sort: 202, Path: "/system/menu", Component: "system/menu/menu-list.vue", Visible: true, System: true},
		{ID: SmartWebCapabilityRoleManageMenuID, ParentID: SmartWebRolePageMenuID, Name: "Edit role permissions", Type: SmartWebMenuTypePoint, Sort: 214, Capability: string(authz.CapabilityAuthorizationRoleManage), Visible: true, System: true},
		{ID: roleAddButtonID, ParentID: SmartWebRolePageMenuID, Name: "Add role", Type: SmartWebMenuTypePoint, Sort: 208, WebPerms: "system:role:add", Visible: true, System: true},
		{ID: menuAddButtonID, ParentID: SmartWebMenuCatalogPageMenuID, Name: "Add menu", Type: SmartWebMenuTypePoint, Sort: 301, WebPerms: "system:menu:add", Visible: true, System: true},
		{ID: 820000041, ParentID: SmartWebMenuCatalogPageMenuID, Name: "Tenant Operations", Type: SmartWebMenuTypePage, Sort: 310, Path: "/system/tenant-ops", Component: "system/tenant-ops/index.vue", Visible: true},
	}
}
