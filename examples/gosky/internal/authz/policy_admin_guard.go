package authz

import (
	"context"
	"database/sql"
	"fmt"
)

// ensurePermissionAdministrator checks actual direct grants across each
// subject's explicitly assigned roles. No role name or inheritance grants a
// recovery bypass. The transaction rolls back its changes if none remain.
func (s *MySQLPolicyStore) ensurePermissionAdministrator(ctx context.Context, tx *sql.Tx, domain string) error {
	active, err := s.activeRoleSet(ctx, domain)
	if err != nil {
		return fmt.Errorf("load active tenant roles: %w", err)
	}
	return s.ensurePermissionAdministratorWithRoles(ctx, tx, domain, active)
}

// ensurePermissionAdministratorWithRoles shares the guard with transactions
// that already resolved the tenant's dynamic role set. Both fixed roles and
// active custom roles participate in the effective-administrator calculation.
func (s *MySQLPolicyStore) ensurePermissionAdministratorWithRoles(ctx context.Context, tx *sql.Tx, domain string, active map[string]struct{}) error {
	rows, err := tx.QueryContext(ctx, s.tables.query(`SELECT ptype,v0,v1,v2,v3 FROM {{rules}} WHERE domain=? FOR UPDATE`), domain)
	if err != nil {
		return fmt.Errorf("lock tenant permission administrators: %w", err)
	}
	defer func() { _ = rows.Close() }()
	grants := make(map[string]map[Capability]bool)
	bindings := make(map[string][]string)
	for rows.Next() {
		var kind, v0, v1, v2, v3 string
		if err := rows.Scan(&kind, &v0, &v1, &v2, &v3); err != nil {
			return fmt.Errorf("scan tenant permission administrator: %w", err)
		}
		if kind == "g" && !roleCodedSubject(v0, active) && assignableRole(v1, active) {
			bindings[v0] = append(bindings[v0], v1)
		}
		if kind == "p" && assignableRole(v0, active) && v3 == CapabilityAction {
			if grants[v0] == nil {
				grants[v0] = make(map[Capability]bool)
			}
			grants[v0][Capability(v2)] = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read tenant permission administrators: %w", err)
	}
	for _, roles := range bindings {
		complete := true
		for _, required := range []Capability{CapabilityAuthorizationRoleRead, CapabilityAuthorizationRoleManage, CapabilityAuthorizationRoleMemberAdd, CapabilityAuthorizationRoleMemberRemove} {
			allowed := false
			for _, role := range roles {
				allowed = allowed || grants[role][required]
			}
			complete = complete && allowed
		}
		if complete {
			return nil
		}
	}
	return ErrLastTenantAdmin
}
