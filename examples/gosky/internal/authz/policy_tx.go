package authz

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// This file exposes transaction-scoped policy primitives for the RBAC
// repository's combined role-menu transaction. They accept an existing
// transaction so role_menus rows, Casbin rules, the policy event outbox, and
// the admin audit log commit atomically across the same MySQL database.
// Application services must not call these directly; they operate on raw
// transactions and therefore belong to the persistence layer.

// ReplaceManagedCapabilitiesInTx rewrites a role's managed canonical
// capability policies within an existing transaction. Only deployment-owned
// capabilities may be written; unknown policies for the role are untouched.
// It performs no last-administrator check and no commit: the caller must run
// EnsurePermissionAdministratorInTx before committing whenever grants may have
// narrowed.
func (s *MySQLPolicyStore) ReplaceManagedCapabilitiesInTx(ctx context.Context, tx *sql.Tx, actor PolicyActor, role, domain string, capabilities []Capability) (bool, error) {
	role = strings.TrimSpace(role)
	domain = strings.TrimSpace(domain)
	if role == "" || domain == "" {
		return false, ErrInvalidPolicyRule
	}
	if err := validateActorDomain(actor, domain); err != nil {
		return false, err
	}
	desired := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if !isManagedCapability(capability) {
			return false, ErrInvalidPolicyRule
		}
		desired[capability] = struct{}{}
	}

	rows, err := tx.QueryContext(ctx, s.tables.query(`SELECT v0, v1, v2, v3, v4, v5 FROM {{rules}} WHERE ptype = 'p' AND domain = ? AND v0 = ? FOR UPDATE`), domain, role)
	if err != nil {
		return false, fmt.Errorf("lock role capability policies: %w", err)
	}
	current := make(map[Capability]policyRule)
	for rows.Next() {
		var rule policyRule
		rule.PType = "p"
		if err := rows.Scan(&rule.Fields[0], &rule.Fields[1], &rule.Fields[2], &rule.Fields[3], &rule.Fields[4], &rule.Fields[5]); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("scan role capability policy: %w", err)
		}
		capability := Capability(rule.Fields[2])
		if rule.Fields[3] == CapabilityAction && isManagedCapability(capability) {
			current[capability] = rule
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("iterate role capability policies: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close role capability policies: %w", err)
	}

	changed := false
	for capability, currentRule := range current {
		if _, keep := desired[capability]; keep {
			continue
		}
		deleted, deleteErr := deleteRule(ctx, tx, s.tables, currentRule)
		if deleteErr != nil {
			return false, deleteErr
		}
		if deleted {
			changed = true
			if eventErr := insertPolicyEvent(ctx, tx, s.tables, actor, currentRule, "remove_policy", false); eventErr != nil {
				return false, eventErr
			}
		}
	}
	for capability := range desired {
		if _, exists := current[capability]; exists {
			continue
		}
		rule := policyRule{PType: "p", Fields: [6]string{role, domain, string(capability), CapabilityAction}}
		inserted, insertErr := insertRule(ctx, tx, s.tables, rule)
		if insertErr != nil {
			return false, insertErr
		}
		if inserted {
			changed = true
			if eventErr := insertPolicyEvent(ctx, tx, s.tables, actor, rule, "add_policy", true); eventErr != nil {
				return false, eventErr
			}
		}
	}
	return changed, nil
}

// EnsurePermissionAdministratorInTx runs the effective-administrator guard on
// uncommitted transaction state. The transaction must be rolled back when it
// returns ErrLastTenantAdmin.
func (s *MySQLPolicyStore) EnsurePermissionAdministratorInTx(ctx context.Context, tx *sql.Tx, domain string) error {
	return s.ensurePermissionAdministrator(ctx, tx, domain)
}

// RemoveCapabilityFromRolesInTx deletes every direct EXECUTE policy for one
// capability in the domain (all roles). It is used when a tenant-defined
// function-point menu carrying that capability is deleted. The caller must
// run the administrator guard before committing.
func (s *MySQLPolicyStore) RemoveCapabilityFromRolesInTx(ctx context.Context, tx *sql.Tx, actor PolicyActor, domain, capability string) (bool, error) {
	domain = strings.TrimSpace(domain)
	capability = strings.TrimSpace(capability)
	if domain == "" || capability == "" {
		return false, ErrInvalidPolicyRule
	}
	if err := validateActorDomain(actor, domain); err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, s.tables.query(`SELECT v0, v1, v2, v3, v4, v5 FROM {{rules}} WHERE ptype = 'p' AND domain = ? AND v2 = ? AND v3 = ? FOR UPDATE`), domain, capability, CapabilityAction)
	if err != nil {
		return false, fmt.Errorf("lock capability policies for deletion: %w", err)
	}
	var removals []policyRule
	for rows.Next() {
		var rule policyRule
		rule.PType = "p"
		if err := rows.Scan(&rule.Fields[0], &rule.Fields[1], &rule.Fields[2], &rule.Fields[3], &rule.Fields[4], &rule.Fields[5]); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("scan capability policy for deletion: %w", err)
		}
		removals = append(removals, rule)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("iterate capability policies for deletion: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close capability policies for deletion: %w", err)
	}
	changed := false
	for _, rule := range removals {
		deleted, deleteErr := deleteRule(ctx, tx, s.tables, rule)
		if deleteErr != nil {
			return false, deleteErr
		}
		if deleted {
			changed = true
			if eventErr := insertPolicyEvent(ctx, tx, s.tables, actor, rule, "remove_policy", false); eventErr != nil {
				return false, eventErr
			}
		}
	}
	return changed, nil
}

// RoleBindingCountInTx counts active subject-to-role grouping rules for one
// role in the domain, using only transaction state.
func (s *MySQLPolicyStore) RoleBindingCountInTx(ctx context.Context, tx *sql.Tx, domain, role string) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, s.tables.query(`SELECT COUNT(*) FROM {{rules}} WHERE ptype = 'g' AND domain = ? AND v1 = ?`), domain, role).Scan(&count); err != nil {
		return 0, fmt.Errorf("count role bindings: %w", err)
	}
	return count, nil
}

// DeleteRoleBindingsInTx removes only the subject-to-role grouping rules for
// a role, keeping its capability template. It is used when a custom role is
// disabled, which must stop authorization immediately.
func (s *MySQLPolicyStore) DeleteRoleBindingsInTx(ctx context.Context, tx *sql.Tx, actor PolicyActor, role, domain string) (bool, error) {
	role = strings.TrimSpace(role)
	domain = strings.TrimSpace(domain)
	if role == "" || domain == "" || validRole(role) {
		return false, ErrInvalidPolicyRule
	}
	if err := validateActorDomain(actor, domain); err != nil {
		return false, err
	}
	changed := false
	if err := s.deleteRulesInTx(ctx, tx, actor, "g", role, domain, &changed, 1, 2); err != nil {
		return false, err
	}
	return changed, nil
}

// DeleteRolePolicyInTx removes every grouping rule targeting the role and
// every direct policy rule owned by the role in the domain, recording one
// removal event per changed row. Reserved role codes are rejected: their
// policy baseline is deployment-owned.
func (s *MySQLPolicyStore) DeleteRolePolicyInTx(ctx context.Context, tx *sql.Tx, actor PolicyActor, role, domain string) (bool, error) {
	role = strings.TrimSpace(role)
	domain = strings.TrimSpace(domain)
	if role == "" || domain == "" {
		return false, ErrInvalidPolicyRule
	}
	if validRole(role) {
		return false, ErrInvalidPolicyRule
	}
	if err := validateActorDomain(actor, domain); err != nil {
		return false, err
	}
	changed := false
	if err := s.deleteRulesInTx(ctx, tx, actor, "g", role, domain, &changed, 1, 2); err != nil {
		return false, err
	}
	if err := s.deleteRulesInTx(ctx, tx, actor, "p", role, domain, &changed, 0, 1); err != nil {
		return false, err
	}
	return changed, nil
}

// deleteRulesInTx locks and deletes rules of one ptype where the role and
// domain positions match, emitting a removal event per row. rolePosition is
// the field index of the role code; domainPosition is the field index of
// domain.
func (s *MySQLPolicyStore) deleteRulesInTx(ctx context.Context, tx *sql.Tx, actor PolicyActor, ptype, role, domain string, changed *bool, rolePosition, domainPosition int) error {
	rows, err := tx.QueryContext(ctx, s.tables.query(`SELECT v0, v1, v2, v3, v4, v5 FROM {{rules}} WHERE ptype = ? AND domain = ? FOR UPDATE`), ptype, domain)
	if err != nil {
		return fmt.Errorf("lock role rules for deletion: %w", err)
	}
	var removals []policyRule
	for rows.Next() {
		var rule policyRule
		rule.PType = ptype
		if err := rows.Scan(&rule.Fields[0], &rule.Fields[1], &rule.Fields[2], &rule.Fields[3], &rule.Fields[4], &rule.Fields[5]); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan role rule for deletion: %w", err)
		}
		if rule.Fields[rolePosition] == role && rule.Fields[domainPosition] == domain {
			removals = append(removals, rule)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate role rules for deletion: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close role rules for deletion: %w", err)
	}
	operation := "remove_policy"
	if ptype == "g" {
		operation = "remove_grouping"
	}
	for _, rule := range removals {
		deleted, deleteErr := deleteRule(ctx, tx, s.tables, rule)
		if deleteErr != nil {
			return deleteErr
		}
		if deleted {
			*changed = true
			if eventErr := insertPolicyEvent(ctx, tx, s.tables, actor, rule, operation, false); eventErr != nil {
				return eventErr
			}
		}
	}
	return nil
}
