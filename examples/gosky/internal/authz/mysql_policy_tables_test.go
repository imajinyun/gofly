package authz

import "testing"

func TestNewMySQLPolicyTables(t *testing.T) {
	tests := []struct {
		name        string
		rulesTable  string
		eventsTable string
		wantRules   string
		wantEvents  string
		wantError   bool
	}{
		{name: "defaults", wantRules: DefaultMySQLPolicyRulesTable, wantEvents: DefaultMySQLPolicyEventsTable},
		{name: "custom pair", rulesTable: "tenant_policy_rules", eventsTable: "tenant_policy_events", wantRules: "tenant_policy_rules", wantEvents: "tenant_policy_events"},
		{name: "partial pair", rulesTable: "tenant_policy_rules", wantError: true},
		{name: "same tables", rulesTable: "tenant_policy", eventsTable: "tenant_policy", wantError: true},
		{name: "leading whitespace", rulesTable: " tenant_policy_rules", eventsTable: "tenant_policy_events", wantError: true},
		{name: "schema qualifier", rulesTable: "gosky.tenant_policy_rules", eventsTable: "tenant_policy_events", wantError: true},
		{name: "quoted identifier", rulesTable: "tenant`policy_rules", eventsTable: "tenant_policy_events", wantError: true},
		{name: "sql punctuation", rulesTable: "tenant_policy_rules;DROP", eventsTable: "tenant_policy_events", wantError: true},
		{name: "leading digit", rulesTable: "1tenant_policy_rules", eventsTable: "tenant_policy_events", wantError: true},
		{name: "too long", rulesTable: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", eventsTable: "tenant_policy_events", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tables, err := NewMySQLPolicyTables(tt.rulesTable, tt.eventsTable)
			if tt.wantError {
				if err == nil {
					t.Fatal("NewMySQLPolicyTables error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewMySQLPolicyTables: %v", err)
			}
			if tables.Rules != tt.wantRules || tables.Events != tt.wantEvents {
				t.Fatalf("table names = %#v, want rules=%q events=%q", tables, tt.wantRules, tt.wantEvents)
			}
		})
	}
}
