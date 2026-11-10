package smartweb

import (
	"context"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Failure-mode inventory for the organization page batch:
//
//  1. ORG-NAV-01: an operator holding only system.user.manage must see the
//     Organization catalog, its three pages, and the ten organization buttons;
//     the Access Control catalog must stay hidden instead of rendering an
//     empty or forbidden group.
//  2. ORG-NAV-02: an operator holding only authorization.role.read sees the
//     Access Control catalog and its pages but no organization surface;
//     catalogs without visible descendants are pruned.
//  3. ORG-NAV-03: a fully denied operator receives an empty navigation list,
//     not an error, so the login boundary stays usable.
//  4. ORG-NAV-04: a fully allowed administrator receives both catalogs, all
//     five pages, and all 21 operator buttons exactly once.
//  5. ORG-NAV-05: the role-employee projection carries phone and department
//     columns for the unchanged role page and never fabricates a fixed-role
//     tag for a tenant-managed role code.
func TestService_OrganizationNavigationCapabilityGating(t *testing.T) {
	identity := authn.SmartWebIdentity{Subject: "user:operator", TenantID: 900000010, LoginName: "operator"}
	ctx := context.Background()

	cases := []struct {
		name      string
		allowed   map[authz.Capability]bool
		wantIDs   []uint64
		forbidIDs []uint64
		wantCount int
		wantErr   bool
	}{
		{
			name:      "user manager sees only organization",
			allowed:   map[authz.Capability]bool{authz.CapabilitySystemUserManage: true},
			wantCount: 1 + 3 + 10,
			wantIDs: []uint64{
				SmartWebOrgCatalogMenuID, SmartWebEmployeePageMenuID, SmartWebDepartmentPageMenuID, SmartWebPositionPageMenuID,
				employeeAddButtonID, employeeUpdateButtonID, employeeDeleteButtonID, employeeDepartmentButtonID,
				employeeDisabledButtonID, employeeResetPwdButtonID,
				departmentQueryButtonID, departmentAddButtonID, departmentUpdateButtonID, departmentDeleteButtonID,
			},
			forbidIDs: []uint64{
				SmartWebRoleCatalogMenuID, SmartWebRolePageMenuID, SmartWebMenuCatalogPageMenuID,
				roleAddButtonID, menuAddButtonID, SmartWebFilePageMenuID, fileQueryButtonID,
			},
		},
		{
			name:      "role reader sees only access control pages",
			allowed:   map[authz.Capability]bool{authz.CapabilityAuthorizationRoleRead: true},
			wantCount: 3,
			wantIDs:   []uint64{SmartWebRoleCatalogMenuID, SmartWebRolePageMenuID, SmartWebMenuCatalogPageMenuID},
			forbidIDs: []uint64{SmartWebOrgCatalogMenuID, SmartWebEmployeePageMenuID, roleAddButtonID, employeeAddButtonID, SmartWebFilePageMenuID},
		},
		{
			name:      "denied operator gets empty navigation",
			allowed:   map[authz.Capability]bool{},
			wantCount: 0,
		},
		{
			name: "administrator sees every structural node and button",
			allowed: map[authz.Capability]bool{
				authz.CapabilityAuthorizationRoleRead: true, authz.CapabilityAuthorizationRoleManage: true,
				authz.CapabilityAuthorizationRoleScopeManage:  true,
				authz.CapabilityAuthorizationRoleMemberAdd:    true,
				authz.CapabilityAuthorizationRoleMemberRemove: true,
				authz.CapabilitySystemMenuManage:              true,
				authz.CapabilitySystemUserManage:              true,
				authz.CapabilitySupportFileRead:               true,
				authz.CapabilitySupportFileManage:             true,
			},
			wantCount: 30,
			wantIDs: []uint64{
				SmartWebRoleCatalogMenuID, SmartWebRolePageMenuID, SmartWebMenuCatalogPageMenuID,
				SmartWebOrgCatalogMenuID, SmartWebEmployeePageMenuID, SmartWebDepartmentPageMenuID, SmartWebPositionPageMenuID,
				SmartWebFilePageMenuID, fileQueryButtonID,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, err := NewService(roleMenuAuthenticator{}, capabilityAuthorizer{allowed: tc.allowed}, nil)
			if err != nil {
				t.Fatalf("new service: %v", err)
			}
			menus, navErr := service.MenusForIdentity(ctx, identity)
			if navErr != nil != tc.wantErr {
				t.Fatalf("navigation error = %v, wantErr %v", navErr, tc.wantErr)
			}
			if len(menus) != tc.wantCount {
				t.Fatalf("navigation count = %d, want %d; menus=%#v", len(menus), tc.wantCount, menus)
			}
			ids := menuTreeIDs(menus)
			for _, id := range tc.wantIDs {
				if !containsUint64(ids, id) {
					t.Fatalf("navigation %#v missing %d", ids, id)
				}
			}
			for _, id := range tc.forbidIDs {
				if containsUint64(ids, id) {
					t.Fatalf("navigation %#v must not contain %d", ids, id)
				}
			}
			if len(ids) != len(menus) {
				t.Fatalf("duplicate menu ids in navigation: %#v", menus)
			}
		})
	}
}

func TestService_UnavailablePolicyFailsNavigationClosed(t *testing.T) {
	service, err := NewService(roleMenuAuthenticator{}, capabilityAuthorizer{
		err: coreerrors.New(coreerrors.CodeUnavailable, "authorization unavailable"),
	}, nil)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	identity := authn.SmartWebIdentity{Subject: "user:operator", TenantID: 900000010}
	if menus, navErr := service.MenusForIdentity(context.Background(), identity); navErr == nil || menus != nil {
		t.Fatalf("navigation = (%#v, %v), want fail-closed error", menus, navErr)
	}
}

func TestRoleEmployeePageProjectsOrganizationColumns(t *testing.T) {
	created := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	page := app.RoleMemberPage{
		Users: []app.TenantUser{{
			UserID: 42, Subject: "user:42", LoginName: "luna", DisplayName: "Luna Li",
			Phone: "13800000000", DepartmentName: "Platform", CreatedAt: created,
		}},
		Total: 1,
	}

	projection := roleEmployeePage(page, authz.RoleWriter)
	if len(projection.List) != 1 || projection.Total != 1 {
		t.Fatalf("projection = %#v", projection)
	}
	item := projection.List[0]
	if item.Phone != "13800000000" || item.DepartmentName != "Platform" || item.ActualName != "Luna Li" {
		t.Fatalf("employee projection = %#v, want phone and department columns", item)
	}
	if item.DisabledFlag {
		t.Fatalf("active binding projected as disabled: %#v", item)
	}
	if len(item.RoleIDList) != 1 || item.RoleIDList[0] != SmartWebRoleWriterID || len(item.RoleNameList) != 1 || item.RoleNameList[0] != "Writer" {
		t.Fatalf("fixed role tags = %#v/%#v", item.RoleIDList, item.RoleNameList)
	}

	dynamic := roleEmployeePage(app.RoleMemberPage{Users: []app.TenantUser{{UserID: 7, LoginName: "ops", DisplayName: "Ops"}}, Total: 1}, "ops_team")
	if len(dynamic.List[0].RoleIDList) != 0 || len(dynamic.List[0].RoleNameList) != 0 {
		t.Fatalf("dynamic role produced fabricated tags: %#v", dynamic.List[0])
	}
}
