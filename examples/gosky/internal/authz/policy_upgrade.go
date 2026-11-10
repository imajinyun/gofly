package authz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// UpgradeTenantExplicitPermissions converts existing role inheritance once.
// It is an operator action, never startup reconciliation. The removed legacy
// edges are the conversion marker: later retries cannot restore revoked grants.
func (s *MySQLPolicyStore) UpgradeTenantExplicitPermissions(ctx context.Context, actor PolicyActor) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("policy database is nil")
	}
	domain := strings.TrimSpace(actor.Domain)
	if err := validateActorDomain(actor, domain); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return false, fmt.Errorf("begin explicit permission conversion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, s.tables.query(`SELECT ptype,v0,v1,v2,v3,v4,v5 FROM {{rules}} WHERE domain=? FOR UPDATE`), domain)
	if err != nil {
		return false, fmt.Errorf("lock legacy tenant policy: %w", err)
	}
	roles := []string{RoleReader, RoleWriter, RoleAdmin}
	grants := make(map[string]map[Capability]bool)
	for _, role := range roles {
		grants[role] = make(map[Capability]bool)
	}
	var edges []policyRule
	for rows.Next() {
		var rule policyRule
		if err := rows.Scan(&rule.PType, &rule.Fields[0], &rule.Fields[1], &rule.Fields[2], &rule.Fields[3], &rule.Fields[4], &rule.Fields[5]); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("scan legacy policy: %w", err)
		}
		if rule.PType == "g" && validRole(rule.Fields[0]) && validRole(rule.Fields[1]) {
			edges = append(edges, rule)
		}
		if rule.PType == "p" && validRole(rule.Fields[0]) && rule.Fields[3] == CapabilityAction && isManagedCapability(Capability(rule.Fields[2])) {
			grants[rule.Fields[0]][Capability(rule.Fields[2])] = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("read legacy policies: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if len(edges) == 0 {
		return false, nil
	}
	// The fixed graph contains only three roles; three passes also terminate
	// cycles and materialize their prior effective union deterministically.
	for range roles {
		for _, edge := range edges {
			for capability := range grants[edge.Fields[1]] {
				grants[edge.Fields[0]][capability] = true
			}
		}
	}
	for _, role := range roles {
		if grants[role][CapabilityAuthorizationRoleManage] {
			for _, capability := range []Capability{CapabilityAuthorizationRoleRead, CapabilityAuthorizationRoleScopeManage,
				CapabilityAuthorizationRoleMemberAdd, CapabilityAuthorizationRoleMemberRemove, CapabilityAuthorizationPolicyEventRead} {
				grants[role][capability] = true
			}
		}
		if grants[role][CapabilityProjectReadAny] {
			grants[role][CapabilityProjectRead] = true
		}
		if grants[role][CapabilityProjectMemberManage] {
			grants[role][CapabilityProjectManageAny] = true
		}
		for _, capability := range ManagedCapabilities() {
			if !grants[role][capability] {
				continue
			}
			rule := policyRule{PType: "p", Fields: [6]string{role, domain, string(capability), CapabilityAction}}
			added, err := insertRule(ctx, tx, s.tables, rule)
			if err != nil {
				return false, err
			}
			if added {
				if err := insertPolicyEvent(ctx, tx, s.tables, actor, rule, "add_policy", true); err != nil {
					return false, err
				}
			}
		}
	}
	for _, edge := range edges {
		if _, err := tx.ExecContext(ctx, s.tables.query(`DELETE FROM {{rules}} WHERE ptype='g' AND domain=? AND v0=? AND v1=? AND v2=?`), domain, edge.Fields[0], edge.Fields[1], domain); err != nil {
			return false, fmt.Errorf("remove legacy role edge: %w", err)
		}
		if err := insertPolicyEvent(ctx, tx, s.tables, actor, edge, "remove_grouping", false); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit explicit permission conversion: %w", err)
	}
	return true, nil
}
