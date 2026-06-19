// Package governance provides request routing rules, rate limiting, circuit
// breaking, concurrency limiting and canary release policies for gofly services.
package governance

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// Diagnostic severity levels.
const (
	DiagnosticInfo  = "info"
	DiagnosticWarn  = "warn"
	DiagnosticError = "error"
)

// RuleDiagnostic records a single issue found during rule analysis.
type RuleDiagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Index    int    `json:"index,omitempty"`
	RuleKey  string `json:"ruleKey,omitempty"`
	RuleName string `json:"ruleName,omitempty"`
}

// AnalyzeRules validates and diagnoses a set of governance rules.
func AnalyzeRules(rules []Rule) []RuleDiagnostic {
	rules = cloneRules(rules)
	out := make([]RuleDiagnostic, 0)
	if err := ValidateRules(rules...); err != nil {
		out = append(out, RuleDiagnostic{Severity: DiagnosticError, Code: "invalid_rules", Message: err.Error()})
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		return ruleSpecificity(rules[i]) > ruleSpecificity(rules[j])
	})
	for i, rule := range rules {
		out = append(out, analyzeRule(i, rule)...)
		for j := 0; j < i; j++ {
			if ruleCovers(rules[j], rule) {
				out = append(out, diagnosticForRule(DiagnosticWarn, "shadowed_rule", fmt.Sprintf("rule is shadowed by earlier rule %q", displayRuleName(rules[j])), i, rule))
				break
			}
		}
	}
	return out
}

func (r *RuleSet) Diagnostics() []RuleDiagnostic {
	if r == nil {
		return nil
	}
	return AnalyzeRules(r.Snapshot())
}

func (m *Manager) Diagnostics() []RuleDiagnostic {
	if m == nil || m.rules == nil {
		return nil
	}
	return m.rules.Diagnostics()
}

func (r *RuleSet) WritePrometheus(w io.Writer) error {
	if w == nil {
		return nil
	}
	status := r.Status()
	if _, err := fmt.Fprintln(w, "# HELP gofly_governance_rules Current governance rule count."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_governance_rules gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "gofly_governance_rules %d\n", status.Rules); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_governance_version Current governance rule-set version."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_governance_version gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "gofly_governance_version %d\n", status.Version); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_governance_rule_hits_total Total governance rule matches."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_governance_rule_hits_total counter"); err != nil {
		return err
	}
	for _, stat := range r.Stats() {
		if _, err := fmt.Fprintf(w, "gofly_governance_rule_hits_total{rule=\"%s\",key=\"%s\"} %d\n", prometheusLabel(stat.RuleName), prometheusLabel(stat.RuleKey), stat.Hits); err != nil {
			return err
		}
	}
	diagnostics := r.Diagnostics()
	if _, err := fmt.Fprintln(w, "# HELP gofly_governance_diagnostics Current governance diagnostics by severity."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_governance_diagnostics gauge"); err != nil {
		return err
	}
	counts := map[string]int{DiagnosticInfo: 0, DiagnosticWarn: 0, DiagnosticError: 0}
	for _, diagnostic := range diagnostics {
		counts[diagnostic.Severity]++
	}
	for _, severity := range []string{DiagnosticInfo, DiagnosticWarn, DiagnosticError} {
		if _, err := fmt.Fprintf(w, "gofly_governance_diagnostics{severity=\"%s\"} %d\n", severity, counts[severity]); err != nil {
			return err
		}
	}
	return nil
}

func analyzeRule(index int, rule Rule) []RuleDiagnostic {
	out := make([]RuleDiagnostic, 0, 4)
	if rule.Transport == "" && rule.Service == "" && rule.Method == "" && (rule.Path == "" || rule.Path == "*") && len(rule.Tags) == 0 {
		out = append(out, diagnosticForRule(DiagnosticWarn, "broad_match", "rule matches every governance request", index, rule))
	}
	if reflect.DeepEqual(rule.Policy, Policy{}) {
		out = append(out, diagnosticForRule(DiagnosticInfo, "empty_policy", "rule does not configure any policy", index, rule))
	}
	if rule.Policy.Retry.Attempts > 1 && rule.Policy.Timeout == 0 {
		out = append(out, diagnosticForRule(DiagnosticWarn, "retry_without_timeout", "retry policy has multiple attempts but no timeout", index, rule))
	}
	canary := rule.Policy.Canary
	if canary.Ratio > 0 && canary.Service == "" && canary.Target == "" && canary.UpstreamPrefix == "" && len(canary.Targets) == 0 && len(canary.Headers) == 0 {
		out = append(out, diagnosticForRule(DiagnosticWarn, "canary_without_target", "canary ratio is configured without target service, upstream prefix or headers", index, rule))
	}
	return out
}

func diagnosticForRule(severity, code, message string, index int, rule Rule) RuleDiagnostic {
	return RuleDiagnostic{Severity: severity, Code: code, Message: message, Index: index, RuleKey: ruleKey(rule), RuleName: rule.Name}
}

func ruleCovers(base Rule, candidate Rule) bool {
	if base.Transport != "" && base.Transport != candidate.Transport {
		return false
	}
	if base.Service != "" && base.Service != candidate.Service {
		return false
	}
	if base.Method != "" && base.Method != candidate.Method {
		return false
	}
	if !pathCovers(base.Path, candidate.Path) {
		return false
	}
	for key, value := range base.Tags {
		if candidate.Tags[key] != value {
			return false
		}
	}
	return true
}

func pathCovers(base string, candidate string) bool {
	if base == "" || base == "*" || base == "/" {
		return true
	}
	if base == candidate {
		return true
	}
	if strings.HasSuffix(base, "*") {
		return strings.HasPrefix(candidate, strings.TrimSuffix(base, "*"))
	}
	return false
}

func displayRuleName(rule Rule) string {
	if rule.Name != "" {
		return rule.Name
	}
	return ruleKey(rule)
}

func prometheusLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return strings.ReplaceAll(s, "\"", "\\\"")
}
