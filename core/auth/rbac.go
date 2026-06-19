// Package auth provides request authentication primitives for gofly services,
// including JWT signing/verification, OAuth2, RBAC and API key management.
package auth

import "context"

// Requirement expresses an authorization rule against a Principal. All listed
// roles/permissions are required unless the AnyOf variants are used. An empty
// Requirement authorizes any authenticated principal.
type Requirement struct {
	// Roles that the principal must all possess.
	Roles []string
	// AnyRole is satisfied when the principal holds at least one of these roles.
	AnyRole []string
	// Permissions that the principal must all hold.
	Permissions []string
	// AnyPermission is satisfied when the principal holds at least one.
	AnyPermission []string
}

// Satisfied reports whether the principal meets the requirement.
func (r Requirement) Satisfied(p Principal) bool {
	if len(r.Roles) > 0 && !p.HasAllRoles(r.Roles...) {
		return false
	}
	if len(r.AnyRole) > 0 && !p.HasAnyRole(r.AnyRole...) {
		return false
	}
	if len(r.Permissions) > 0 && !p.HasAllPermissions(r.Permissions...) {
		return false
	}
	if len(r.AnyPermission) > 0 && !p.HasAnyPermission(r.AnyPermission...) {
		return false
	}
	return true
}

// RequireRoles builds a requirement demanding every listed role.
func RequireRoles(roles ...string) Requirement { return Requirement{Roles: roles} }

// RequireAnyRole builds a requirement satisfied by any one of the roles.
func RequireAnyRole(roles ...string) Requirement { return Requirement{AnyRole: roles} }

// RequirePermissions builds a requirement demanding every listed permission.
func RequirePermissions(perms ...string) Requirement { return Requirement{Permissions: perms} }

// RequireAnyPermission builds a requirement satisfied by any one permission.
func RequireAnyPermission(perms ...string) Requirement { return Requirement{AnyPermission: perms} }

// Authorize checks the principal in ctx against the requirement. It returns
// ErrMissingCredentials when no principal is present and ErrPermissionDenied
// when the principal does not satisfy the requirement.
func Authorize(ctx context.Context, req Requirement) error {
	principal, ok := FromContext(ctx)
	if !ok {
		return ErrMissingCredentials
	}
	if !req.Satisfied(principal) {
		return ErrPermissionDenied
	}
	return nil
}
