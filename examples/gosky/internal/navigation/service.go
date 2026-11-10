// Package navigation projects deployment-owned admin navigation for one
// already authenticated gosky session. It never exposes Casbin policy rows,
// role inheritance, or resource relationships to the browser.
package navigation

import (
	"context"
	"strconv"
	"strings"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/examples/gosky/internal/authn"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
)

// MenuKind keeps the browser navigation contract explicit without coupling it
// to smart-web's historical menu-table DTO.
type MenuKind string

const (
	MenuKindPage MenuKind = "page"
)

// MenuItem is a deployment-owned, client-safe navigation entry. Component is
// a stable logical key that the browser maps through its local component
// allowlist; it is never a server-provided file system path or import URL.
type MenuItem struct {
	ID        string   `json:"id"`
	Kind      MenuKind `json:"kind"`
	Title     string   `json:"title"`
	Icon      string   `json:"icon,omitempty"`
	Group     string   `json:"group,omitempty"`
	Route     string   `json:"route"`
	Component string   `json:"component"`
	Order     int      `json:"order"`
}

// SessionProjection combines a verified session identity with its visible
// browser navigation. TenantID stays a decimal string to prevent JavaScript
// integer precision loss for MySQL BIGINT UNSIGNED identifiers.
type SessionProjection struct {
	Subject    string     `json:"subject"`
	TenantID   string     `json:"tenantId"`
	Realm      string     `json:"realm"`
	Navigation []MenuItem `json:"navigation"`
	Points     []string   `json:"points"`
}

// PolicyAuthorizer is the navigation consumer's narrow view of Casbin. The
// concrete authorizer is injected by application wiring and performs all
// tenant selection from the verified JWT context.
type PolicyAuthorizer interface {
	Authorize(context.Context, string, string) *coreerrors.Error
}

type projectionEntry struct {
	capability authz.Capability
	menu       *MenuItem
	point      string
}

var adminProjectionEntries = []projectionEntry{
	{
		capability: authz.CapabilityProjectRead,
		menu: &MenuItem{
			ID:        "workspace.projects",
			Kind:      MenuKindPage,
			Title:     "Projects",
			Icon:      "ProjectOutlined",
			Group:     "workspace",
			Route:     "/projects",
			Component: "gosky.projects.list",
			Order:     100,
		},
	},
	{
		capability: authz.CapabilityAuthorizationRoleRead,
		menu: &MenuItem{
			ID:        "access-control.roles",
			Kind:      MenuKindPage,
			Title:     "Role management",
			Icon:      "SafetyCertificateOutlined",
			Group:     "access-control",
			Route:     "/access-control/roles",
			Component: "gosky.authorization.roles",
			Order:     200,
		},
	},
	{capability: authz.CapabilityProjectMemberManage, point: "projects.members.manage"},
	{capability: authz.CapabilityAuthorizationRoleManage, point: "access-control.roles.manage"},
	{capability: authz.CapabilityAuthorizationPolicyEventRead, point: "access-control.policy-events.read"},
}

// AdminService projects only the admin realm. The front realm will get its
// own explicit registry when its product surface is introduced.
type AdminService struct {
	policy PolicyAuthorizer
}

// NewAdminService builds the small, immutable projection service. Its input
// is an interface only because the service consumes the single authorization
// operation needed for policy-filtered navigation.
func NewAdminService(policy PolicyAuthorizer) *AdminService {
	return &AdminService{policy: policy}
}

// Project evaluates each code-owned navigation and point entry independently.
// A normal capability denial hides that entry; any other decision failure
// fails the complete response closed, preventing a stale partial projection.
func (s *AdminService) Project(ctx context.Context, principal authn.RequestPrincipal) (SessionProjection, *coreerrors.Error) {
	if !validAdminPrincipal(principal) {
		return SessionProjection{}, coreerrors.New(coreerrors.CodeUnauthenticated, "authenticated admin principal is required")
	}
	if s == nil || s.policy == nil {
		return SessionProjection{}, coreerrors.New(coreerrors.CodeUnavailable, "navigation authorization is unavailable")
	}
	projection := SessionProjection{
		Subject:    principal.Subject,
		TenantID:   strconv.FormatUint(principal.TenantID, 10),
		Realm:      string(principal.Realm),
		Navigation: make([]MenuItem, 0, 2),
		Points:     make([]string, 0, 3),
	}
	for _, entry := range adminProjectionEntries {
		if entry.capability != "" {
			allowed, err := s.allowed(ctx, entry.capability)
			if err != nil {
				return SessionProjection{}, err
			}
			if !allowed {
				continue
			}
		}
		if entry.menu != nil {
			projection.Navigation = append(projection.Navigation, *entry.menu)
		}
		if entry.point != "" {
			projection.Points = append(projection.Points, entry.point)
		}
	}
	return projection, nil
}

func (s *AdminService) allowed(ctx context.Context, capability authz.Capability) (bool, *coreerrors.Error) {
	err := s.policy.Authorize(ctx, string(capability), authz.CapabilityAction)
	if err == nil {
		return true, nil
	}
	if err.Code == coreerrors.CodePermissionDenied {
		return false, nil
	}
	return false, err
}

func validAdminPrincipal(principal authn.RequestPrincipal) bool {
	return strings.TrimSpace(principal.Subject) != "" && principal.TenantID > 0 && principal.Realm == authn.RealmAdmin && strings.TrimSpace(principal.SessionID) != ""
}
