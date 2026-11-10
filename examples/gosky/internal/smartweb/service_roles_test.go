package smartweb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// Failure-mode inventory: AUTHN-025, AUTHN-026, and AUTHN-028. The menu is
// an authorization projection, not an untrusted browser or Casbin-row value.
func TestService_LoginProjectsOnlyAuthorizedSmartWebMenus(t *testing.T) {
	tests := []struct {
		name          string
		policyErr     *coreerrors.Error
		wantMenuCount int
		wantErr       bool
	}{
		{name: "tenant administrator receives all pages and operation points", wantMenuCount: 26 + 67},
		{name: "tenant reader receives no role page", policyErr: coreerrors.New(coreerrors.CodePermissionDenied, "permission denied"), wantMenuCount: 0},
		{name: "unavailable policy fails closed", policyErr: coreerrors.New(coreerrors.CodeUnavailable, "authorization unavailable"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{err: tt.policyErr}, NewCaptchaService(CaptchaOptions{
				TTL:  time.Minute,
				Now:  time.Now,
				Code: func() (string, error) { return "ABCD", nil },
				ID:   func() (string, error) { return "captcha-id-000001", nil },
			}))
			if err != nil {
				t.Fatalf("new smart-web service: %v", err)
			}
			challenge, err := service.NewCaptcha()
			if err != nil {
				t.Fatalf("new captcha: %v", err)
			}
			result, err := service.Login(context.Background(), LoginInput{
				LoginName:   "admin",
				Password:    "ODRmYjQ3YmM0ZWM4NjU4NjYyNGJkZDE2N2QwYjE5NmRmNjg4ZGZiNzRlMmU5MDg4ZjNkYWE2MGM3OGQyMTQ0Mg==",
				CaptchaCode: "ABCD",
				CaptchaUUID: challenge.CaptchaUUID,
			})
			if tt.wantErr {
				if err == nil {
					t.Fatal("login error = nil, want policy failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("login: %v", err)
			}
			if len(result.MenuList) != tt.wantMenuCount {
				t.Fatalf("menu count = %d, want %d; menu=%#v", len(result.MenuList), tt.wantMenuCount, result.MenuList)
			}
			if tt.wantMenuCount == 0 {
				return
			}
			catalog, rolePage, menuPage := result.MenuList[0], result.MenuList[1], result.MenuList[2]
			if catalog.MenuType != SmartWebMenuTypeCatalog || catalog.ParentID != 0 || rolePage.MenuType != SmartWebMenuTypePage || rolePage.ParentID != catalog.MenuID || rolePage.Path != "/system/role" || rolePage.Component != "system/role/index.vue" || menuPage.MenuID != SmartWebMenuCatalogPageMenuID || menuPage.ParentID != catalog.MenuID || menuPage.Path != "/system/menu" || menuPage.Component != "system/menu/menu-list.vue" {
				t.Fatalf("smart-web role menu = %#v", result.MenuList)
			}
		})
	}
}

// Failure-mode inventory: AUTHN-034 and AUTHN-035. Menu data is a
// deployment-owned compatibility projection: it is never sourced from a
// browser request or a tenant Casbin row, and every configurable capability
// remains visible exactly once in the role tree.
func TestService_MenuCatalogIsReadOnlyAndRepresentsEveryCapability(t *testing.T) {
	service, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{}, nil)
	if err != nil {
		t.Fatalf("new smart-web service: %v", err)
	}
	ctx := auth.NewContext(context.Background(), auth.Principal{Subject: "user:admin", Claims: map[string]any{"tenant_id": "900000010"}})

	catalog, serviceErr := service.MenuCatalog(ctx)
	if serviceErr != nil {
		t.Fatalf("menu catalog: %v", serviceErr)
	}
	if len(catalog) != 26+len(authz.ManagedCapabilities()) || catalog[0].MenuID != SmartWebRoleCatalogMenuID || catalog[2].MenuID != SmartWebMenuCatalogPageMenuID {
		t.Fatalf("menu catalog = %#v, want twenty-six structural rows and all managed capability points", catalog)
	}

	for _, onlyMenu := range []bool{true, false} {
		tree, treeErr := service.MenuTree(ctx, onlyMenu)
		if treeErr != nil {
			t.Fatalf("menu tree onlyMenu=%t: %v", onlyMenu, treeErr)
		}
		ids := menuTreeIDs(tree)
		for _, id := range []uint64{SmartWebRoleCatalogMenuID, SmartWebRolePageMenuID, SmartWebMenuCatalogPageMenuID, SmartWebOrgCatalogMenuID, SmartWebEmployeePageMenuID, SmartWebDepartmentPageMenuID, SmartWebPositionPageMenuID, SmartWebFilePageMenuID} {
			if !containsUint64(ids, id) {
				t.Fatalf("menu tree onlyMenu=%t ids=%#v, want structural menu %d", onlyMenu, ids, id)
			}
		}
		for _, definition := range smartWebCapabilityMenus {
			count := countUint64(ids, definition.menuID)
			if onlyMenu && count != 0 {
				t.Fatalf("menu-only tree contains capability %d %q", definition.menuID, definition.capability)
			}
			if !onlyMenu && count != 1 {
				t.Fatalf("full menu tree contains capability %d %q %d times, want once", definition.menuID, definition.capability, count)
			}
		}
	}

	if serviceErr := service.RejectMenuMutation(ctx); serviceErr == nil || serviceErr.Code != coreerrors.CodeFailedPrecondition {
		t.Fatalf("menu mutation error = %#v, want failed precondition", serviceErr)
	}
}

func menuTreeIDs(menus []MenuVO) []uint64 {
	ids := make([]uint64, 0, len(menus))
	for _, menu := range menus {
		ids = append(ids, menu.MenuID)
		ids = append(ids, menuTreeIDs(menu.Children)...)
	}
	return ids
}

func countUint64(values []uint64, want uint64) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

// Failure-mode inventory: AUTHN-027. Only the fixed Casbin role set can be
// projected; a caller cannot select arbitrary role or navigation identifiers.
func TestService_RolePageReadModelIsFixedAndAuthorizationBound(t *testing.T) {
	service, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{}, nil)
	if err != nil {
		t.Fatalf("new smart-web service: %v", err)
	}
	ctx := auth.NewContext(context.Background(), auth.Principal{Subject: "user:admin", Claims: map[string]any{"tenant_id": "900000010"}})
	roles, roleErr := service.RoleDefinitions(ctx)
	if roleErr != nil {
		t.Fatalf("role definitions: %v", roleErr)
	}
	if len(roles) != 3 || roles[0].RoleCode != authz.RoleReader || roles[1].RoleCode != authz.RoleWriter || roles[2].RoleCode != authz.RoleAdmin {
		t.Fatalf("roles = %#v, want fixed reader/writer/admin definitions", roles)
	}
	selection, roleErr := service.RoleMenuSelection(ctx, SmartWebRoleAdminID)
	if roleErr != nil {
		t.Fatalf("role menu selection: %v", roleErr)
	}
	if len(selection.MenuTreeList) != 21 || len(selection.SelectedMenuID) == 0 {
		t.Fatalf("role menu selection = %#v, want access-control, organization, and support-page roots", selection)
	}
	if !containsUint64(menuTreeIDs(selection.MenuTreeList), SmartWebEmployeePageMenuID) {
		t.Fatalf("role menu selection = %#v, want organization pages in the structural tree", selection)
	}
	if _, err := service.RoleMenuSelection(ctx, 999999999); err == nil {
		t.Fatal("unknown role selection error = nil, want invalid argument")
	}

	denied, err := NewService(roleMenuAuthenticator{}, roleMenuAuthorizer{err: coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")}, nil)
	if err != nil {
		t.Fatalf("new denied smart-web service: %v", err)
	}
	if _, err := denied.RoleDefinitions(ctx); err == nil || err.Code != coreerrors.CodePermissionDenied {
		t.Fatalf("denied role definitions error = %#v, want permission denied", err)
	}
}

type roleMenuAuthenticator struct{}

func (roleMenuAuthenticator) LoginSmartWeb(context.Context, authn.SmartWebLoginInput) (authn.SmartWebLoginResult, error) {
	return authn.SmartWebLoginResult{AccessToken: "access-token", Identity: authn.SmartWebIdentity{UserID: 1, TenantID: 900000010, Subject: "user:admin", LoginName: "admin"}}, nil
}

func (roleMenuAuthenticator) SmartWebLoginInfo(context.Context, authn.RequestPrincipal) (authn.SmartWebIdentity, error) {
	return authn.SmartWebIdentity{}, errors.New("not used")
}

func (roleMenuAuthenticator) Logout(context.Context, authn.LogoutInput) error { return nil }

type roleMenuAuthorizer struct{ err *coreerrors.Error }

func (a roleMenuAuthorizer) Authorize(context.Context, string, string) *coreerrors.Error {
	if a.err == nil {
		return nil
	}
	return a.err
}
