package authz

import (
	"fmt"
	"strings"
)

const (
	// DefaultMySQLPolicyRulesTable is the migration-owned rule table name.
	DefaultMySQLPolicyRulesTable = "casbin_rules"
	// DefaultMySQLPolicyEventsTable is the migration-owned audit/outbox table name.
	DefaultMySQLPolicyEventsTable = "casbin_policy_events"

	maxMySQLIdentifierLength = 64
)

// MySQLPolicyTables is the validated, deployment-owned table pair for one
// policy store. Rules and events stay coupled because every policy mutation
// must write both tables in the same transaction.
type MySQLPolicyTables struct {
	Rules  string
	Events string
}

// DefaultMySQLPolicyTables returns the names created by gosky's standard
// migration pair.
func DefaultMySQLPolicyTables() MySQLPolicyTables {
	return MySQLPolicyTables{Rules: DefaultMySQLPolicyRulesTable, Events: DefaultMySQLPolicyEventsTable}
}

// NewMySQLPolicyTables accepts either no names (the standard migration pair)
// or a complete, simple-identifier pair. Schema-qualified names and quoting
// are deliberately unsupported: dynamic SQL identifiers cannot be bound as
// parameters, so a narrow identifier grammar keeps the SQL construction safe.
func NewMySQLPolicyTables(rulesTable, eventsTable string) (MySQLPolicyTables, error) {
	if rulesTable != strings.TrimSpace(rulesTable) || eventsTable != strings.TrimSpace(eventsTable) {
		return MySQLPolicyTables{}, fmt.Errorf("authorization mysql policy table names cannot contain leading or trailing whitespace")
	}
	if rulesTable == "" && eventsTable == "" {
		return DefaultMySQLPolicyTables(), nil
	}
	if rulesTable == "" || eventsTable == "" {
		return MySQLPolicyTables{}, fmt.Errorf("authorization mysql rulesTable and eventsTable must be configured together")
	}
	if rulesTable == eventsTable {
		return MySQLPolicyTables{}, fmt.Errorf("authorization mysql rulesTable and eventsTable must differ")
	}
	if !isSafeMySQLIdentifier(rulesTable) || !isSafeMySQLIdentifier(eventsTable) {
		return MySQLPolicyTables{}, fmt.Errorf("authorization mysql policy table names must be 1-%d ASCII identifiers starting with a letter", maxMySQLIdentifierLength)
	}
	return MySQLPolicyTables{Rules: rulesTable, Events: eventsTable}, nil
}

func (t MySQLPolicyTables) rulesSQL() string {
	return "`" + t.Rules + "`"
}

func (t MySQLPolicyTables) eventsSQL() string {
	return "`" + t.Events + "`"
}

// query fills only deployment-owned identifiers in a literal SQL template.
// Store/adapter constructors validate and copy this pair. Values always remain
// bound parameters; request data never supplies a template or an identifier.
func (t MySQLPolicyTables) query(template string) string {
	return strings.NewReplacer("{{rules}}", t.rulesSQL(), "{{events}}", t.eventsSQL()).Replace(template)
}

func isSafeMySQLIdentifier(value string) bool {
	if len(value) == 0 || len(value) > maxMySQLIdentifierLength {
		return false
	}
	if !isASCIIAlpha(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isASCIIAlpha(value[index]) && !isASCIIDigit(value[index]) && value[index] != '_' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isASCIIDigit(value byte) bool {
	return value >= '0' && value <= '9'
}
