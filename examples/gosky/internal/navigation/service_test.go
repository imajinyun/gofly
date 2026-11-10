package navigation

import (
	"context"
	"testing"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

func TestAdminService_Project(t *testing.T) {
	tests := []struct {
		name          string
		allowed       map[string]bool
		unavailable   bool
		wantMenuIDs   []string
		wantPointIDs  []string
		wantErrorCode coreerrors.Code
	}{
		{
			name:        "reader receives projects",
			allowed:     map[string]bool{string(authz.CapabilityProjectRead): true},
			wantMenuIDs: []string{"workspace.projects"},
		},
		{
			name: "admin receives role management and related points",
			allowed: map[string]bool{
				string(authz.CapabilityProjectRead):                  true,
				string(authz.CapabilityProjectMemberManage):          true,
				string(authz.CapabilityAuthorizationRoleManage):      true,
				string(authz.CapabilityAuthorizationRoleRead):        true,
				string(authz.CapabilityAuthorizationPolicyEventRead): true,
			},
			wantMenuIDs:  []string{"workspace.projects", "access-control.roles"},
			wantPointIDs: []string{"projects.members.manage", "access-control.roles.manage", "access-control.policy-events.read"},
		},
		{
			name:          "policy unavailability fails closed",
			allowed:       map[string]bool{string(authz.CapabilityProjectRead): true},
			unavailable:   true,
			wantErrorCode: coreerrors.CodeUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewAdminService(navigationPolicyAuthorizer{allowed: tt.allowed, unavailable: tt.unavailable})
			projection, err := service.Project(context.Background(), authn.RequestPrincipal{
				Subject:   "reader-demo",
				TenantID:  1001,
				Realm:     authn.RealmAdmin,
				SessionID: "session-1",
			})
			if tt.wantErrorCode != "" {
				if err == nil || err.Code != tt.wantErrorCode {
					t.Fatalf("Project() error = %#v, want code %q", err, tt.wantErrorCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Project() error = %v", err)
			}
			if projection.Subject != "reader-demo" || projection.TenantID != "1001" || projection.Realm != string(authn.RealmAdmin) {
				t.Fatalf("identity projection = %#v", projection)
			}
			if got := menuIDs(projection.Navigation); !sameStrings(got, tt.wantMenuIDs) {
				t.Fatalf("menu IDs = %#v, want %#v", got, tt.wantMenuIDs)
			}
			if !sameStrings(projection.Points, tt.wantPointIDs) {
				t.Fatalf("point IDs = %#v, want %#v", projection.Points, tt.wantPointIDs)
			}
		})
	}
}

func TestAdminService_ProjectReturnsIndependentProjection(t *testing.T) {
	service := NewAdminService(navigationPolicyAuthorizer{allowed: map[string]bool{string(authz.CapabilityProjectRead): true}})
	principal := authn.RequestPrincipal{Subject: "reader-demo", TenantID: 1001, Realm: authn.RealmAdmin, SessionID: "session-1"}

	first, err := service.Project(context.Background(), principal)
	if err != nil {
		t.Fatalf("first Project() error = %v", err)
	}
	first.Navigation[0].ID = "mutated"
	second, err := service.Project(context.Background(), principal)
	if err != nil {
		t.Fatalf("second Project() error = %v", err)
	}
	if got := menuIDs(second.Navigation); !sameStrings(got, []string{"workspace.projects"}) {
		t.Fatalf("second menu IDs = %#v, want immutable registry output", got)
	}
}

type navigationPolicyAuthorizer struct {
	allowed     map[string]bool
	unavailable bool
}

func (a navigationPolicyAuthorizer) Authorize(_ context.Context, resource, _ string) *coreerrors.Error {
	if a.unavailable {
		return coreerrors.New(coreerrors.CodeUnavailable, "authorization policy is unavailable")
	}
	if a.allowed[resource] {
		return nil
	}
	return coreerrors.New(coreerrors.CodePermissionDenied, "permission denied")
}

func menuIDs(items []MenuItem) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

func sameStrings(got, want []string) bool {
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
