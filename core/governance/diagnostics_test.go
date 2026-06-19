package governance

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestAnalyzeRulesReportsProductionDiagnostics(t *testing.T) {
	rules := []Rule{
		{Name: "catch-all", Priority: 100, Policy: Policy{Retry: RetryPolicy{Attempts: 2}}},
		{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}},
		{Name: "canary", Transport: TransportREST, Service: "checkout", Policy: Policy{Canary: CanaryPolicy{Ratio: 0.1}}},
	}
	diagnostics := AnalyzeRules(rules)
	if !hasDiagnostic(diagnostics, "broad_match", "catch-all") {
		t.Fatalf("diagnostics = %#v, want broad catch-all warning", diagnostics)
	}
	if !hasDiagnostic(diagnostics, "retry_without_timeout", "catch-all") {
		t.Fatalf("diagnostics = %#v, want retry without timeout warning", diagnostics)
	}
	if !hasDiagnostic(diagnostics, "shadowed_rule", "orders") {
		t.Fatalf("diagnostics = %#v, want shadowed orders warning", diagnostics)
	}
	if !hasDiagnostic(diagnostics, "canary_without_target", "canary") {
		t.Fatalf("diagnostics = %#v, want canary target warning", diagnostics)
	}
}

func TestAnalyzeRulesReportsValidationError(t *testing.T) {
	diagnostics := AnalyzeRules([]Rule{{Name: "dup"}, {Name: "dup"}})
	if !hasDiagnostic(diagnostics, "invalid_rules", "") {
		t.Fatalf("diagnostics = %#v, want invalid rule diagnostic", diagnostics)
	}
}

func TestRuleSetWritesPrometheusMetrics(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	_ = rules.Match(Request{Transport: TransportREST, Service: "orders"})
	var buf bytes.Buffer
	if err := rules.WritePrometheus(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"gofly_governance_rules 1",
		"gofly_governance_version 1",
		`gofly_governance_rule_hits_total{rule="orders",key="name:orders"} 1`,
		`gofly_governance_diagnostics{severity="info"}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prometheus output missing %q:\n%s", want, out)
		}
	}
}

func hasDiagnostic(diagnostics []RuleDiagnostic, code string, ruleName string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != code {
			continue
		}
		if ruleName == "" || diagnostic.RuleName == ruleName {
			return true
		}
	}
	return false
}
