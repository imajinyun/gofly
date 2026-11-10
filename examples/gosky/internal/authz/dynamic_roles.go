package authz

import (
	"context"
	"strings"
)

// RoleDirectory bridges the dynamic, tenant-managed roles table into the
// authorization package without importing a persistence implementation. The
// concrete repository lives in internal/model/repo and queries the same
// verified domain; request input never selects a role code.
type RoleDirectory interface {
	// ActiveRoleCodes returns the custom role codes that currently exist and
	// are active in the domain. The reserved reader/writer/admin codes are
	// managed by Casbin policy and need not be returned.
	ActiveRoleCodes(ctx context.Context, domain string) (map[string]struct{}, error)
}

// IsReservedRoleCode reports whether code is one of the fixed, code-owned
// tenant roles. Custom role codes must never collide with these values.
func IsReservedRoleCode(code string) bool {
	return validRole(strings.TrimSpace(code))
}

// SetRoleDirectory wires the tenant-managed role lookup after construction,
// breaking the repository <-> policy-store construction cycle. It must be
// called during startup before the store serves requests.
func (s *MySQLPolicyStore) SetRoleDirectory(directory RoleDirectory) {
	if s == nil {
		return
	}
	s.roles = directory
}

// activeRoleSet fetches the domain's custom roles once per mutation so batch
// validation does not perform a lookup for every rule. A nil directory keeps
// the historical fixed-role-only behavior for file policy mode and tests.
func (s *MySQLPolicyStore) activeRoleSet(ctx context.Context, domain string) (map[string]struct{}, error) {
	if s == nil || s.roles == nil {
		return nil, nil
	}
	active, err := s.roles.ActiveRoleCodes(ctx, strings.TrimSpace(domain))
	if err != nil {
		return nil, err
	}
	return active, nil
}

// assignableRole reports whether role may appear as a policy/grouping role
// position in the domain: it is a reserved code or an active tenant role.
func assignableRole(role string, active map[string]struct{}) bool {
	if validRole(strings.TrimSpace(role)) {
		return true
	}
	if role == "" {
		return false
	}
	_, ok := active[role]
	return ok
}

// roleCodedSubject reports whether value collides with any role position and
// therefore cannot identify a human subject in a grouping rule.
func roleCodedSubject(value string, active map[string]struct{}) bool {
	return assignableRole(value, active)
}
