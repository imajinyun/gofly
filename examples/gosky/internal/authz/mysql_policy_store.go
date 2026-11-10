package authz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	RoleReader = "reader"
	RoleWriter = "writer"
	RoleAdmin  = "admin"
)

var (
	ErrLastTenantAdmin           = errors.New("cannot remove the final tenant administrator")
	ErrTenantAlreadyBootstrapped = errors.New("tenant policy is already bootstrapped")
	ErrInvalidPolicyRule         = errors.New("invalid Casbin policy rule")
	ErrPolicyDomainMismatch      = errors.New("policy actor domain does not match rule domain")
)

// PolicyActor is the verified identity and correlation data of a policy
// mutation. It deliberately contains no bearer credentials or request body.
type PolicyActor struct {
	Subject   string
	Domain    string
	RequestID string
	TraceID   string
}

type policyRule struct {
	PType  string
	Fields [6]string
}

func (r policyRule) domain() string {
	switch r.PType {
	case "p":
		return r.Fields[1]
	case "g":
		return r.Fields[2]
	default:
		return ""
	}
}

func (r policyRule) values() []string {
	values := []string{r.PType}
	last := len(r.Fields) - 1
	for last >= 0 && r.Fields[last] == "" {
		last--
	}
	values = append(values, r.Fields[:last+1]...)
	return values
}

func (r policyRule) key() string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(r.PType))
	for _, field := range r.Fields {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(field))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (r policyRule) jsonValue() json.RawMessage {
	data, _ := json.Marshal(r.values())
	return data
}

// MySQLPolicyStore owns the audited policy-management write path. Casbin's
// Adapter is read-only by design, so callers cannot bypass tenant validation,
// last-admin protection, or the durable event outbox through Enforcer APIs.
type MySQLPolicyStore struct {
	db     *sql.DB
	tables MySQLPolicyTables
	// roles optionally validates tenant-managed custom role codes. A nil
	// directory keeps fixed-role-only validation for file policy mode and
	// focused unit tests.
	roles RoleDirectory
}

// RoleBinding is the tenant-scoped subject-to-role projection exposed by the
// management API. It never exposes policy event payloads or credentials.
type RoleBinding struct {
	Subject string    `json:"subject"`
	Role    string    `json:"role"`
	Domain  string    `json:"domain"`
	Created time.Time `json:"createdAt"`
}

// PolicyEvent is the operator-facing mutation audit projection. Raw request
// bodies and bearer credentials are never persisted or returned.
type PolicyEvent struct {
	ID           uint64    `json:"id"`
	Domain       string    `json:"domain"`
	Operation    string    `json:"operation"`
	ActorSubject string    `json:"actorSubject"`
	RequestID    string    `json:"requestId,omitempty"`
	Created      time.Time `json:"createdAt"`
}

func NewMySQLPolicyStore(db *sql.DB) *MySQLPolicyStore {
	return &MySQLPolicyStore{db: db, tables: DefaultMySQLPolicyTables()}
}

func NewMySQLPolicyStoreWithTables(db *sql.DB, tables MySQLPolicyTables) (*MySQLPolicyStore, error) {
	return NewMySQLPolicyStoreWithRoleDirectory(db, tables, nil)
}

// NewMySQLPolicyStoreWithRoleDirectory wires the tenant-managed role
// directory used to validate custom grouping and policy roles. The directory
// is a read-only port implemented by the RBAC repository.
func NewMySQLPolicyStoreWithRoleDirectory(db *sql.DB, tables MySQLPolicyTables, roles RoleDirectory) (*MySQLPolicyStore, error) {
	validated, err := NewMySQLPolicyTables(tables.Rules, tables.Events)
	if err != nil {
		return nil, err
	}
	return &MySQLPolicyStore{db: db, tables: validated, roles: roles}, nil
}

func (s *MySQLPolicyStore) ListRoleBindings(ctx context.Context, domain string) ([]RoleBinding, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("policy database is nil")
	}
	if strings.TrimSpace(domain) == "" {
		return nil, ErrInvalidPolicyRule
	}
	rows, err := s.db.QueryContext(ctx, s.tables.query(`SELECT v0, v1, v2, created_at FROM {{rules}} WHERE ptype = 'g' AND domain = ? AND v0 NOT IN ('reader','writer','admin') ORDER BY v0, v1`), domain)
	if err != nil {
		return nil, fmt.Errorf("list Casbin role bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	bindings := make([]RoleBinding, 0)
	for rows.Next() {
		var binding RoleBinding
		if err := rows.Scan(&binding.Subject, &binding.Role, &binding.Domain, &binding.Created); err != nil {
			return nil, fmt.Errorf("scan Casbin role binding: %w", err)
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Casbin role bindings: %w", err)
	}
	return bindings, nil
}

// ListRoleCapabilities returns direct canonical capability policies for one
// fixed role. It intentionally excludes role inheritance and unknown custom
// policies so callers cannot project arbitrary Casbin data into a browser.
func (s *MySQLPolicyStore) ListRoleCapabilities(ctx context.Context, domain, role string) ([]Capability, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("policy database is nil")
	}
	if strings.TrimSpace(domain) == "" || strings.TrimSpace(role) == "" {
		return nil, ErrInvalidPolicyRule
	}
	active, err := s.activeRoleSet(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("load active tenant roles: %w", err)
	}
	if !assignableRole(role, active) {
		return nil, ErrInvalidPolicyRule
	}
	rows, err := s.db.QueryContext(ctx, s.tables.query(`SELECT v2 FROM {{rules}} WHERE ptype = 'p' AND domain = ? AND v0 = ? AND v3 = ? ORDER BY v2`), domain, role, CapabilityAction)
	if err != nil {
		return nil, fmt.Errorf("list Casbin role capabilities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	capabilities := make([]Capability, 0)
	for rows.Next() {
		var capability Capability
		if err := rows.Scan(&capability); err != nil {
			return nil, fmt.Errorf("scan Casbin role capability: %w", err)
		}
		if isManagedCapability(capability) {
			capabilities = append(capabilities, capability)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Casbin role capabilities: %w", err)
	}
	return capabilities, nil
}

func (s *MySQLPolicyStore) ListEvents(ctx context.Context, domain string, limit int) ([]PolicyEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("policy database is nil")
	}
	if strings.TrimSpace(domain) == "" || limit < 1 || limit > 100 {
		return nil, ErrInvalidPolicyRule
	}
	rows, err := s.db.QueryContext(ctx, s.tables.query(`SELECT event_id, domain, operation, actor_subject, request_id, created_at FROM {{events}} WHERE domain = ? ORDER BY event_id DESC LIMIT ?`), domain, limit)
	if err != nil {
		return nil, fmt.Errorf("list Casbin policy events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	events := make([]PolicyEvent, 0)
	for rows.Next() {
		var event PolicyEvent
		if err := rows.Scan(&event.ID, &event.Domain, &event.Operation, &event.ActorSubject, &event.RequestID, &event.Created); err != nil {
			return nil, fmt.Errorf("scan Casbin policy event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Casbin policy events: %w", err)
	}
	return events, nil
}

func (s *MySQLPolicyStore) GrantRole(ctx context.Context, actor PolicyActor, subject, role, domain string) (bool, error) {
	rule := policyRule{PType: "g", Fields: [6]string{subject, role, domain}}
	active, err := s.activeRoleSet(ctx, domain)
	if err != nil {
		return false, fmt.Errorf("load active tenant roles: %w", err)
	}
	if err := validateGroupingRule(actor, rule, active); err != nil {
		return false, err
	}
	return s.mutate(ctx, actor, rule, "add_grouping", true)
}

// GrantRoles atomically adds one fixed role for all supplied subjects. It is
// used for compatibility batch actions after every employee ID has already
// been resolved to an active member in the verified tenant.
func (s *MySQLPolicyStore) GrantRoles(ctx context.Context, actor PolicyActor, subjects []string, role, domain string) (bool, error) {
	return s.mutateRoleBindings(ctx, actor, subjects, role, domain, true)
}

// BootstrapTenant initializes one tenant exactly once. It is for the separate
// operator command, never an HTTP request: after a tenant has an admin, all
// ordinary role changes must go through PolicyService and verified JWT auth.
func (s *MySQLPolicyStore) BootstrapTenant(ctx context.Context, actor PolicyActor, initialAdmin string) error {
	if s == nil || s.db == nil {
		return errors.New("policy database is nil")
	}
	domain := strings.TrimSpace(actor.Domain)
	if strings.TrimSpace(actor.Subject) == "" || domain == "" || (strings.TrimSpace(initialAdmin) == "" || validRole(strings.TrimSpace(initialAdmin))) {
		return ErrInvalidPolicyRule
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fmt.Errorf("begin tenant policy bootstrap: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, s.tables.query(`SELECT rule_id FROM {{rules}} WHERE ptype = 'g' AND v1 = ? AND v2 = ? FOR UPDATE`), RoleAdmin, domain)
	if err != nil {
		return fmt.Errorf("lock tenant bootstrap state: %w", err)
	}
	if rows.Next() {
		_ = rows.Close()
		return ErrTenantAlreadyBootstrapped
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate tenant bootstrap state: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close tenant bootstrap state: %w", err)
	}
	rules := []struct {
		rule      policyRule
		operation string
	}{
		{rule: policyRule{PType: "g", Fields: [6]string{strings.TrimSpace(initialAdmin), RoleAdmin, domain}}, operation: "add_grouping"},
	}
	for _, rule := range tenantCapabilityPolicies(domain) {
		rules = append(rules, struct {
			rule      policyRule
			operation string
		}{rule: rule, operation: "add_policy"})
	}
	for _, item := range rules {
		changed, insertErr := insertRule(ctx, tx, s.tables, item.rule)
		if insertErr != nil {
			return insertErr
		}
		if !changed {
			return fmt.Errorf("bootstrap policy rule already exists: %w", ErrTenantAlreadyBootstrapped)
		}
		if eventErr := insertPolicyEvent(ctx, tx, s.tables, actor, item.rule, item.operation, true); eventErr != nil {
			return eventErr
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tenant policy bootstrap: %w", err)
	}
	return nil
}

// UpgradeTenantCapabilities replaces only the known pre-registry transport
// policies for one tenant with their canonical capability equivalents. The
// operation is idempotent and writes remove/add audit events in the same
// transaction, so operators can upgrade an existing tenant without recreating
// role bindings or bypassing watcher propagation.
func (s *MySQLPolicyStore) UpgradeTenantCapabilities(ctx context.Context, actor PolicyActor) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("policy database is nil")
	}
	domain := strings.TrimSpace(actor.Domain)
	if err := validateActorDomain(actor, domain); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return false, fmt.Errorf("begin tenant capability upgrade: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, s.tables.query(`SELECT v0, v1, v2, v3, v4, v5 FROM {{rules}} WHERE ptype = 'p' AND domain = ? FOR UPDATE`), domain)
	if err != nil {
		return false, fmt.Errorf("lock tenant capability policies: %w", err)
	}
	legacyRules := make([]policyRule, 0)
	for rows.Next() {
		var rule policyRule
		rule.PType = "p"
		if err := rows.Scan(&rule.Fields[0], &rule.Fields[1], &rule.Fields[2], &rule.Fields[3], &rule.Fields[4], &rule.Fields[5]); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("scan tenant capability policy: %w", err)
		}
		if _, ok := canonicalPoliciesForLegacyPolicy(rule); ok {
			legacyRules = append(legacyRules, rule)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, fmt.Errorf("iterate tenant capability policies: %w", err)
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close tenant capability policy rows: %w", err)
	}

	changed := false
	for _, legacy := range legacyRules {
		canonicalRules, _ := canonicalPoliciesForLegacyPolicy(legacy)
		removed, removeErr := deleteRule(ctx, tx, s.tables, legacy)
		if removeErr != nil {
			return false, removeErr
		}
		if removed {
			changed = true
			if eventErr := insertPolicyEvent(ctx, tx, s.tables, actor, legacy, "remove_policy", false); eventErr != nil {
				return false, eventErr
			}
		}
		for _, canonical := range canonicalRules {
			added, addErr := insertRule(ctx, tx, s.tables, canonical)
			if addErr != nil {
				return false, addErr
			}
			if added {
				changed = true
				if eventErr := insertPolicyEvent(ctx, tx, s.tables, actor, canonical, "add_policy", true); eventErr != nil {
					return false, eventErr
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit tenant capability upgrade: %w", err)
	}
	return changed, nil
}

// tenantCapabilityPolicies is the code-owned baseline for every bootstrapped
// tenant. These grants are used only at bootstrap; upgrades never restore
// defaults over explicitly edited policy.
func tenantCapabilityPolicies(domain string) []policyRule {
	var rules []policyRule
	for _, role := range []string{RoleReader, RoleWriter, RoleAdmin} {
		for _, capability := range DefaultRoleCapabilities(role) {
			rules = append(rules, policyRule{PType: "p", Fields: [6]string{role, domain, string(capability), CapabilityAction}})
		}
	}
	return rules
}

func canonicalPoliciesForLegacyPolicy(rule policyRule) ([]policyRule, bool) {
	if rule.PType != "p" || !validRole(rule.Fields[0]) {
		return nil, false
	}
	switch {
	case rule.Fields[2] == "/api/v1/projects/:id" && rule.Fields[3] == "GET":
		return []policyRule{
			{PType: "p", Fields: [6]string{rule.Fields[0], rule.Fields[1], string(CapabilityProjectRead), CapabilityAction}},
			{PType: "p", Fields: [6]string{RoleAdmin, rule.Fields[1], string(CapabilityProjectReadAny), CapabilityAction}},
		}, true
	case rule.Fields[2] == "rpc:greeter/SayHello" && rule.Fields[3] == "CALL":
		return []policyRule{{PType: "p", Fields: [6]string{rule.Fields[0], rule.Fields[1], string(CapabilityGreeterHello), CapabilityAction}}}, true
	case rule.Fields[2] == "rpc:greeter/Watch" && rule.Fields[3] == "STREAM":
		return []policyRule{{PType: "p", Fields: [6]string{rule.Fields[0], rule.Fields[1], string(CapabilityGreeterWatch), CapabilityAction}}}, true
	case rule.Fields[2] == "/api/v1/authorization/roles" && rule.Fields[3] == "MANAGE":
		return []policyRule{{PType: "p", Fields: [6]string{rule.Fields[0], rule.Fields[1], string(CapabilityAuthorizationRoleManage), CapabilityAction}}}, true
	default:
		return nil, false
	}
}

func (s *MySQLPolicyStore) RevokeRole(ctx context.Context, actor PolicyActor, subject, role, domain string) (bool, error) {
	rule := policyRule{PType: "g", Fields: [6]string{subject, role, domain}}
	active, err := s.activeRoleSet(ctx, domain)
	if err != nil {
		return false, fmt.Errorf("load active tenant roles: %w", err)
	}
	if err := validateGroupingRule(actor, rule, active); err != nil {
		return false, err
	}
	return s.mutate(ctx, actor, rule, "remove_grouping", false)
}

// RevokeRoles atomically removes one fixed role for all supplied subjects and
// preserves the final tenant-admin invariant across the whole batch.
func (s *MySQLPolicyStore) RevokeRoles(ctx context.Context, actor PolicyActor, subjects []string, role, domain string) (bool, error) {
	return s.mutateRoleBindings(ctx, actor, subjects, role, domain, false)
}

func (s *MySQLPolicyStore) mutateRoleBindings(ctx context.Context, actor PolicyActor, subjects []string, role, domain string, add bool) (bool, error) {
	if s == nil || s.db == nil || len(subjects) == 0 || len(subjects) > 100 || strings.TrimSpace(role) == "" || strings.TrimSpace(domain) == "" {
		return false, ErrInvalidPolicyRule
	}
	if err := validateActorDomain(actor, domain); err != nil {
		return false, err
	}
	active, err := s.activeRoleSet(ctx, domain)
	if err != nil {
		return false, fmt.Errorf("load active tenant roles: %w", err)
	}
	if !assignableRole(role, active) {
		return false, ErrInvalidPolicyRule
	}
	seen := make(map[string]struct{}, len(subjects))
	rules := make([]policyRule, 0, len(subjects))
	for _, subject := range subjects {
		rule := policyRule{PType: "g", Fields: [6]string{strings.TrimSpace(subject), role, domain}}
		if err := validateGroupingRule(actor, rule, active); err != nil {
			return false, err
		}
		if _, duplicate := seen[rule.Fields[0]]; duplicate {
			return false, ErrInvalidPolicyRule
		}
		seen[rule.Fields[0]] = struct{}{}
		rules = append(rules, rule)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return false, fmt.Errorf("begin role-binding batch mutation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if !add && role == RoleAdmin {
		rows, queryErr := tx.QueryContext(ctx, s.tables.query(`SELECT v0 FROM {{rules}} WHERE ptype = 'g' AND v1 = ? AND v2 = ? FOR UPDATE`), RoleAdmin, domain)
		if queryErr != nil {
			return false, fmt.Errorf("lock tenant admin bindings: %w", queryErr)
		}
		admins := make(map[string]struct{})
		for rows.Next() {
			var subject string
			if err := rows.Scan(&subject); err != nil {
				_ = rows.Close()
				return false, fmt.Errorf("scan tenant admin binding: %w", err)
			}
			admins[subject] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("iterate tenant admin bindings: %w", err)
		}
		if err := rows.Close(); err != nil {
			return false, fmt.Errorf("close tenant admin bindings: %w", err)
		}
		remaining := len(admins)
		for _, rule := range rules {
			if _, exists := admins[rule.Fields[0]]; exists {
				remaining--
			}
		}
		if remaining < 1 {
			return false, ErrLastTenantAdmin
		}
	}
	changed := false
	operation := "remove_grouping"
	if add {
		operation = "add_grouping"
	}
	for _, rule := range rules {
		var ruleChanged bool
		if add {
			ruleChanged, err = insertRule(ctx, tx, s.tables, rule)
		} else {
			ruleChanged, err = deleteRule(ctx, tx, s.tables, rule)
		}
		if err != nil {
			return false, err
		}
		if !ruleChanged {
			continue
		}
		changed = true
		if eventErr := insertPolicyEvent(ctx, tx, s.tables, actor, rule, operation, add); eventErr != nil {
			return false, eventErr
		}
	}
	if !add {
		if err := s.ensurePermissionAdministrator(ctx, tx, domain); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit role-binding batch mutation: %w", err)
	}
	return changed, nil
}

func (s *MySQLPolicyStore) GrantPermission(ctx context.Context, actor PolicyActor, subject, domain, object, action string) (bool, error) {
	rule := policyRule{PType: "p", Fields: [6]string{subject, domain, object, action}}
	active, err := s.activeRoleSet(ctx, domain)
	if err != nil {
		return false, fmt.Errorf("load active tenant roles: %w", err)
	}
	if err := validatePermissionRule(actor, rule, active); err != nil {
		return false, err
	}
	return s.mutate(ctx, actor, rule, "add_policy", true)
}

func (s *MySQLPolicyStore) RevokePermission(ctx context.Context, actor PolicyActor, subject, domain, object, action string) (bool, error) {
	rule := policyRule{PType: "p", Fields: [6]string{subject, domain, object, action}}
	active, err := s.activeRoleSet(ctx, domain)
	if err != nil {
		return false, fmt.Errorf("load active tenant roles: %w", err)
	}
	if err := validatePermissionRule(actor, rule, active); err != nil {
		return false, err
	}
	return s.mutate(ctx, actor, rule, "remove_policy", false)
}

// ReplaceRoleCapabilities makes a fixed role's managed canonical capability
// set exactly match capabilities. Unknown custom policies for the role remain
// untouched. The final role-management capability cannot be removed, avoiding
// a tenant-wide self-lockout through the compatibility UI.
func (s *MySQLPolicyStore) ReplaceRoleCapabilities(ctx context.Context, actor PolicyActor, role, domain string, capabilities []Capability) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("policy database is nil")
	}
	if strings.TrimSpace(role) == "" || strings.TrimSpace(domain) == "" {
		return false, ErrInvalidPolicyRule
	}
	if err := validateActorDomain(actor, domain); err != nil {
		return false, err
	}
	active, err := s.activeRoleSet(ctx, domain)
	if err != nil {
		return false, fmt.Errorf("load active tenant roles: %w", err)
	}
	if !assignableRole(role, active) {
		return false, ErrInvalidPolicyRule
	}
	desired := make(map[Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if !isManagedCapability(capability) {
			return false, ErrInvalidPolicyRule
		}
		desired[capability] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return false, fmt.Errorf("begin role capability mutation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

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

	if _, currentlyManaged := current[CapabilityAuthorizationRoleManage]; currentlyManaged {
		if _, retained := desired[CapabilityAuthorizationRoleManage]; !retained {
			managementRows, queryErr := tx.QueryContext(ctx, s.tables.query(`SELECT v0 FROM {{rules}} WHERE ptype = 'p' AND domain = ? AND v2 = ? AND v3 = ? FOR UPDATE`), domain, CapabilityAuthorizationRoleManage, CapabilityAction)
			if queryErr != nil {
				return false, fmt.Errorf("lock role-management policies: %w", queryErr)
			}
			remaining := false
			for managementRows.Next() {
				var policyRole string
				if err := managementRows.Scan(&policyRole); err != nil {
					_ = managementRows.Close()
					return false, fmt.Errorf("scan role-management policy: %w", err)
				}
				if policyRole != role {
					remaining = true
				}
			}
			if err := managementRows.Err(); err != nil {
				_ = managementRows.Close()
				return false, fmt.Errorf("iterate role-management policies: %w", err)
			}
			if err := managementRows.Close(); err != nil {
				return false, fmt.Errorf("close role-management policies: %w", err)
			}
			if !remaining {
				return false, ErrLastTenantAdmin
			}
		}
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
	if err := s.ensurePermissionAdministrator(ctx, tx, domain); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit role capability mutation: %w", err)
	}
	return changed, nil
}

func (s *MySQLPolicyStore) mutate(ctx context.Context, actor PolicyActor, rule policyRule, operation string, add bool) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("policy database is nil")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return false, fmt.Errorf("begin policy mutation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// The last-administrator invariant runs against post-mutation state via
	// ensurePermissionAdministrator, including tenant-managed custom roles.
	var changed bool
	if add {
		changed, err = insertRule(ctx, tx, s.tables, rule)
	} else {
		changed, err = deleteRule(ctx, tx, s.tables, rule)
	}
	if err != nil {
		return false, err
	}
	if !changed {
		if !add {
			if err := s.ensurePermissionAdministrator(ctx, tx, rule.domain()); err != nil {
				return false, err
			}
		}
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit unchanged policy mutation: %w", err)
		}
		return false, nil
	}
	if err := insertPolicyEvent(ctx, tx, s.tables, actor, rule, operation, add); err != nil {
		return false, err
	}
	if !add {
		if err := s.ensurePermissionAdministrator(ctx, tx, rule.domain()); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit policy mutation: %w", err)
	}
	return true, nil
}

func validateGroupingRule(actor PolicyActor, rule policyRule, active map[string]struct{}) error {
	if rule.PType != "g" || strings.TrimSpace(rule.Fields[0]) == "" || strings.TrimSpace(rule.Fields[2]) == "" {
		return ErrInvalidPolicyRule
	}
	if roleCodedSubject(rule.Fields[0], active) || !assignableRole(rule.Fields[1], active) {
		return ErrInvalidPolicyRule
	}
	return validateActorDomain(actor, rule.domain())
}

func validatePermissionRule(actor PolicyActor, rule policyRule, active map[string]struct{}) error {
	if rule.PType != "p" || !assignableRole(rule.Fields[0], active) || strings.TrimSpace(rule.Fields[1]) == "" || strings.TrimSpace(rule.Fields[2]) == "" || strings.TrimSpace(rule.Fields[3]) == "" {
		return ErrInvalidPolicyRule
	}
	return validateActorDomain(actor, rule.domain())
}

func validateActorDomain(actor PolicyActor, domain string) error {
	if strings.TrimSpace(actor.Subject) == "" || strings.TrimSpace(actor.Domain) == "" || strings.TrimSpace(domain) == "" {
		return ErrInvalidPolicyRule
	}
	if actor.Domain != domain {
		return ErrPolicyDomainMismatch
	}
	return nil
}

func validRole(role string) bool {
	return role == RoleReader || role == RoleWriter || role == RoleAdmin
}

func isManagedCapability(capability Capability) bool {
	for _, managed := range ManagedCapabilities() {
		if capability == managed {
			return true
		}
	}
	return false
}

func insertRule(ctx context.Context, tx *sql.Tx, tables MySQLPolicyTables, rule policyRule) (bool, error) {
	result, err := tx.ExecContext(ctx, tables.query(`INSERT IGNORE INTO {{rules}} (rule_key, ptype, domain, v0, v1, v2, v3, v4, v5) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		rule.key(), rule.PType, rule.domain(), rule.Fields[0], rule.Fields[1], rule.Fields[2], rule.Fields[3], rule.Fields[4], rule.Fields[5])
	if err != nil {
		return false, fmt.Errorf("insert Casbin rule: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect inserted Casbin rule: %w", err)
	}
	return changed == 1, nil
}

func deleteRule(ctx context.Context, tx *sql.Tx, tables MySQLPolicyTables, rule policyRule) (bool, error) {
	result, err := tx.ExecContext(ctx, tables.query(`DELETE FROM {{rules}} WHERE rule_key = ?`), rule.key())
	if err != nil {
		return false, fmt.Errorf("delete Casbin rule: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect deleted Casbin rule: %w", err)
	}
	return changed == 1, nil
}

func insertPolicyEvent(ctx context.Context, tx *sql.Tx, tables MySQLPolicyTables, actor PolicyActor, rule policyRule, operation string, add bool) error {
	changeID, err := randomChangeID()
	if err != nil {
		return err
	}
	var before any
	var after any
	if add {
		after = rule.jsonValue()
	} else {
		before = rule.jsonValue()
	}
	_, err = tx.ExecContext(ctx, tables.query(`INSERT INTO {{events}} (change_id, domain, operation, rule_ptype, rule_v0, rule_v1, rule_v2, rule_v3, rule_v4, rule_v5, before_rule, after_rule, actor_subject, actor_domain, request_id, trace_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		changeID, rule.domain(), operation, rule.PType, rule.Fields[0], rule.Fields[1], rule.Fields[2], rule.Fields[3], rule.Fields[4], rule.Fields[5], before, after, actor.Subject, actor.Domain, actor.RequestID, actor.TraceID)
	if err != nil {
		return fmt.Errorf("insert Casbin policy event: %w", err)
	}
	return nil
}

func randomChangeID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate policy change id: %w", err)
	}
	return hex.EncodeToString(data), nil
}
