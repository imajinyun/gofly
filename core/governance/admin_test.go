package governance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/core/kv"
	coreruntime "github.com/gofly/gofly/core/runtime"
)

func TestAdminServesSnapshotExplainAndRuleReplacement(t *testing.T) {
	registry := NewRegistry()
	registry.Register("limiter", "adaptive_limiter", "GET /orders", func() any {
		return map[string]int{"rate": 10}
	})
	rules := NewRuleSet(Rule{
		Name:      "orders-v1",
		Transport: TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/orders/*",
		Policy:    Policy{Headers: map[string]string{"X-Version": "v1"}},
	})
	_ = rules.Match(Request{Transport: TransportREST, Service: "orders", Method: http.MethodGet, Path: "/orders/1"})
	admin := NewAdmin(rules, registry,
		WithAdminPathPrefix("/admin/governance"),
		WithAdminDefaultRequest(Request{Transport: TransportREST, Service: "orders"}),
	)

	snapshotRec := httptest.NewRecorder()
	admin.ServeHTTP(snapshotRec, httptest.NewRequest(http.MethodGet, "/admin/governance/snapshot", nil))
	if snapshotRec.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d", snapshotRec.Code)
	}
	var snapshot AdminSnapshot
	if err := json.NewDecoder(snapshotRec.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Components) != 1 || len(snapshot.Rules) != 1 || len(snapshot.RuleStats) != 1 || snapshot.RuleStatus.Rules != 1 || len(snapshot.Diagnostics) != 0 {
		t.Fatalf("snapshot = %#v, want components/rules/stats/status", snapshot)
	}

	runtimeRegistry := coreruntime.NewRegistry()
	runtimeRegistry.Register("rest.server", "server", func(context.Context) coreruntime.ComponentSnapshot {
		return coreruntime.ComponentSnapshot{Name: "rest.server", Kind: "server", Owner: "rest", Status: "ok"}
	})
	runtimeAdmin := NewAdmin(rules, registry,
		WithAdminPathPrefix("/admin/governance"),
		WithAdminRuntimeRegistry(runtimeRegistry),
	)
	runtimeRec := httptest.NewRecorder()
	runtimeAdmin.ServeHTTP(runtimeRec, httptest.NewRequest(http.MethodGet, "/admin/governance/runtime", nil))
	if runtimeRec.Code != http.StatusOK {
		t.Fatalf("runtime status = %d, want 200", runtimeRec.Code)
	}
	var runtimeSnapshot coreruntime.Snapshot
	if err := json.NewDecoder(runtimeRec.Body).Decode(&runtimeSnapshot); err != nil {
		t.Fatal(err)
	}
	if len(runtimeSnapshot.Components) != 1 || runtimeSnapshot.Components[0].Name != "rest.server" {
		t.Fatalf("runtime snapshot = %#v, want rest server component", runtimeSnapshot)
	}

	explainRec := httptest.NewRecorder()
	admin.ServeHTTP(explainRec, httptest.NewRequest(http.MethodGet, "/admin/governance/explain?method=get&path=/orders/1", nil))
	if explainRec.Code != http.StatusOK {
		t.Fatalf("explain status = %d", explainRec.Code)
	}
	var explain RuleExplain
	if err := json.NewDecoder(explainRec.Body).Decode(&explain); err != nil {
		t.Fatal(err)
	}
	if !explain.Decision.Matched || explain.Decision.RuleName != "orders-v1" || explain.Request.Service != "orders" {
		t.Fatalf("explain = %#v, want default request and v1 match", explain)
	}

	nextRules := []Rule{{Name: "orders-v2", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}}}
	body, err := json.Marshal(nextRules)
	if err != nil {
		t.Fatal(err)
	}
	replaceRec := httptest.NewRecorder()
	admin.ServeHTTP(replaceRec, httptest.NewRequest(http.MethodPut, "/admin/governance/rules", bytes.NewReader(body)))
	if replaceRec.Code != http.StatusOK {
		t.Fatalf("replace status = %d, body = %s", replaceRec.Code, replaceRec.Body.String())
	}
	decision := rules.Match(Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "orders-v2" || decision.Policy.Timeout != time.Second {
		t.Fatalf("decision = %#v, want replaced v2 rule", decision)
	}

	versionsRec := httptest.NewRecorder()
	admin.ServeHTTP(versionsRec, httptest.NewRequest(http.MethodGet, "/admin/governance/versions", nil))
	if versionsRec.Code != http.StatusOK {
		t.Fatalf("versions status = %d", versionsRec.Code)
	}
	var versions []RuleSetVersion
	if err := json.NewDecoder(versionsRec.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Rules[0].Name != "orders-v1" || versions[1].Rules[0].Name != "orders-v2" {
		t.Fatalf("versions = %#v, want v1/v2 snapshots", versions)
	}

	rollbackRec := httptest.NewRecorder()
	admin.ServeHTTP(rollbackRec, httptest.NewRequest(http.MethodPost, "/admin/governance/rollback?version=1", nil))
	if rollbackRec.Code != http.StatusOK {
		t.Fatalf("rollback status = %d body = %s", rollbackRec.Code, rollbackRec.Body.String())
	}
	decision = rules.Match(Request{Transport: TransportREST, Service: "orders", Method: http.MethodGet, Path: "/orders/1"})
	if !decision.Matched || decision.RuleName != "orders-v1" || decision.Policy.Headers["X-Version"] != "v1" {
		t.Fatalf("decision after rollback = %#v, want v1", decision)
	}

	invalidRec := httptest.NewRecorder()
	admin.ServeHTTP(invalidRec, httptest.NewRequest(http.MethodPut, "/admin/governance/rules", strings.NewReader(`[{"name":"dup"},{"name":"dup"}]`)))
	if invalidRec.Code != http.StatusBadRequest || !strings.Contains(invalidRec.Body.String(), "duplicate") {
		t.Fatalf("invalid status = %d body = %s", invalidRec.Code, invalidRec.Body.String())
	}
}

func TestAdminRollbackGuardsExpectedVersionRequireSafeAndForce(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}})
	rules.Replace(
		Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}},
		Rule{Name: "payments", Transport: TransportREST, Service: "payments", Policy: Policy{Timeout: time.Second}},
	)
	admin := NewAdmin(rules, nil)

	staleRec := httptest.NewRecorder()
	admin.ServeHTTP(staleRec, httptest.NewRequest(http.MethodPost, "/rollback?version=1&expectedVersion=1", nil))
	if staleRec.Code != http.StatusConflict || !strings.Contains(staleRec.Body.String(), "version mismatch") {
		t.Fatalf("stale rollback status = %d body = %s, want conflict", staleRec.Code, staleRec.Body.String())
	}

	unsafeRec := httptest.NewRecorder()
	admin.ServeHTTP(unsafeRec, httptest.NewRequest(http.MethodPost, "/rollback?version=1&expectedVersion=2&requireSafe=1", nil))
	if unsafeRec.Code != http.StatusConflict || !strings.Contains(unsafeRec.Body.String(), "rollback plan is not safe") {
		t.Fatalf("unsafe rollback status = %d body = %s, want conflict", unsafeRec.Code, unsafeRec.Body.String())
	}
	if decision := rules.Match(Request{Transport: TransportREST, Service: "payments"}); !decision.Matched || decision.RuleName != "payments" {
		t.Fatalf("payments decision after rejected rollback = %#v, want retained", decision)
	}

	forceRec := httptest.NewRecorder()
	admin.ServeHTTP(forceRec, httptest.NewRequest(http.MethodPost, "/rollback?version=1&expectedVersion=2&requireSafe=1&force=1", nil))
	if forceRec.Code != http.StatusOK {
		t.Fatalf("force rollback status = %d body = %s", forceRec.Code, forceRec.Body.String())
	}
	if decision := rules.Match(Request{Transport: TransportREST, Service: "payments"}); decision.Matched {
		t.Fatalf("payments decision after forced rollback = %#v, want removed", decision)
	}
}

func TestAdminRollbackRejectsInvalidBodyExpectedVersion(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}})
	admin := NewAdmin(rules, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rollback", strings.NewReader(`{"version":1,"expectedVersion":-1}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid expectedVersion") {
		t.Fatalf("rollback status = %d body = %s, want invalid expectedVersion", rec.Code, rec.Body.String())
	}
}

func TestAdminDiagnosticsAndMetricsEndpoints(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	_ = rules.Match(Request{Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)

	diagnosticsRec := httptest.NewRecorder()
	admin.ServeHTTP(diagnosticsRec, httptest.NewRequest(http.MethodGet, "/diagnostics", nil))
	if diagnosticsRec.Code != http.StatusOK {
		t.Fatalf("diagnostics status = %d body = %s", diagnosticsRec.Code, diagnosticsRec.Body.String())
	}
	var diagnostics []RuleDiagnostic
	if err := json.NewDecoder(diagnosticsRec.Body).Decode(&diagnostics); err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(diagnostics, "empty_policy", "orders") {
		t.Fatalf("diagnostics = %#v, want empty policy info", diagnostics)
	}

	metricsRec := httptest.NewRecorder()
	admin.ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metricsRec.Code != http.StatusOK || !strings.Contains(metricsRec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics status=%d content-type=%q body=%s", metricsRec.Code, metricsRec.Header().Get("Content-Type"), metricsRec.Body.String())
	}
	if !strings.Contains(metricsRec.Body.String(), "gofly_governance_rule_hits_total") {
		t.Fatalf("metrics body = %s", metricsRec.Body.String())
	}
}

func TestRenderAdminMetricsReturnsCompleteBuffer(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	_ = rules.Match(Request{Transport: TransportREST, Service: "orders"})
	data, err := renderAdminMetrics(rules)
	if err == nil {
		if !strings.Contains(string(data), "gofly_governance_rules") || !strings.Contains(string(data), "gofly_governance_rule_hits_total") {
			t.Fatalf("metrics data = %s, want complete prometheus payload", data)
		}
		return
	}
	t.Fatalf("renderAdminMetrics error = %v", err)
}

func TestAdminPlanEndpointReturnsDiffDiagnosticsAndImpact(t *testing.T) {
	rules := NewRuleSet(
		Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}},
		Rule{Name: "legacy", Transport: TransportREST, Service: "legacy", Policy: Policy{Timeout: time.Second}},
	)
	admin := NewAdmin(rules, nil)
	body := `{"persist":true,"ttl":1000000000,"rules":[` +
		`{"name":"orders","transport":"rest","service":"orders","policy":{"timeout":2000000000}},` +
		`{"name":"payments","transport":"rest","service":"payments","policy":{"retry":{"attempts":2}}}` +
		`]}`
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/plan", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("plan status = %d body = %s", rec.Code, rec.Body.String())
	}
	var plan RulePlan
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if !plan.OK || plan.Safe || plan.Risk != "medium" || !plan.Persist || plan.TTL != time.Second {
		t.Fatalf("plan = %#v, want ok medium-risk persisted unsafe plan", plan)
	}
	if plan.Impact.Added != 1 || plan.Impact.Removed != 1 || plan.Impact.Changed != 1 || plan.Impact.Warnings != 1 || plan.Impact.ReviewItems != 3 {
		t.Fatalf("plan impact = %#v, want added/removed/changed/warning review counts", plan.Impact)
	}
	if len(plan.Diff.Added) != 1 || plan.Diff.Added[0].Name != "payments" || len(plan.Diff.Removed) != 1 || plan.Diff.Removed[0].Name != "legacy" || len(plan.Diff.Changed) != 1 {
		t.Fatalf("plan diff = %#v, want payments added, legacy removed, orders changed", plan.Diff)
	}
	if !hasDiagnostic(plan.Diagnostics, "retry_without_timeout", "payments") {
		t.Fatalf("plan diagnostics = %#v, want retry_without_timeout", plan.Diagnostics)
	}
}

func TestAdminPlanEndpointReturnsInvalidRulePlanWithoutApplying(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/plan", strings.NewReader(`[{"name":"dup"},{"name":"dup"}]`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("plan status = %d body = %s", rec.Code, rec.Body.String())
	}
	var plan RulePlan
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.OK || plan.Safe || plan.Risk != "high" || plan.ValidationError == "" || plan.Impact.Errors == 0 {
		t.Fatalf("invalid plan = %#v, want high-risk validation error", plan)
	}
	if decision := rules.Match(Request{Transport: TransportREST, Service: "orders"}); !decision.Matched || decision.RuleName != "orders" {
		t.Fatalf("decision after plan = %#v, want current rules unchanged", decision)
	}
}

func TestAdminPlanEndpointHandlesEmptyRulesAndRejectsMissingRulesField(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)

	missingRec := httptest.NewRecorder()
	admin.ServeHTTP(missingRec, httptest.NewRequest(http.MethodPost, "/plan", strings.NewReader(`{}`)))
	if missingRec.Code != http.StatusBadRequest || !strings.Contains(missingRec.Body.String(), "rules field is required") {
		t.Fatalf("missing rules status = %d body = %s, want decode error", missingRec.Code, missingRec.Body.String())
	}

	clearRec := httptest.NewRecorder()
	admin.ServeHTTP(clearRec, httptest.NewRequest(http.MethodPost, "/plan", strings.NewReader(`[]`)))
	if clearRec.Code != http.StatusOK {
		t.Fatalf("empty rules plan status = %d body = %s", clearRec.Code, clearRec.Body.String())
	}
	var clearPlan RulePlan
	if err := json.NewDecoder(clearRec.Body).Decode(&clearPlan); err != nil {
		t.Fatal(err)
	}
	if !clearPlan.OK || clearPlan.Safe || clearPlan.Risk != "medium" || clearPlan.Rules != 0 || clearPlan.Impact.Removed != 1 {
		t.Fatalf("empty rules plan = %#v, want medium-risk clear-all plan", clearPlan)
	}

	var nilAdmin *Admin
	zeroPlan := nilAdmin.PlanRules(nil, false, 0)
	if !zeroPlan.OK || !zeroPlan.Safe || zeroPlan.Risk != "none" || zeroPlan.Impact.ReviewItems != 0 {
		t.Fatalf("nil admin empty plan = %#v, want safe no-op plan", zeroPlan)
	}
}

func TestAdminPlanMarksWarningOnlyPlansUnsafe(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}})
	admin := NewAdmin(rules, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/plan", strings.NewReader(`[{"name":"orders","transport":"rest","service":"orders","policy":{"retry":{"attempts":2}}}]`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("warning plan status = %d body = %s", rec.Code, rec.Body.String())
	}
	var plan RulePlan
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if !plan.OK || plan.Safe || plan.Risk != "medium" || plan.Impact.Warnings != 1 {
		t.Fatalf("warning plan = %#v, want unsafe medium-risk plan", plan)
	}
}

func TestAdminRulesEndpointRejectsStaleExpectedVersion(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/rules?expectedVersion=0", strings.NewReader(`[{"name":"payments","transport":"rest","service":"payments"}]`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "version mismatch") {
		t.Fatalf("stale update status = %d body = %s, want conflict", rec.Code, rec.Body.String())
	}
	decision := rules.Match(Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "orders" {
		t.Fatalf("decision = %#v, want original rules retained", decision)
	}
}

func TestAdminRulesEndpointRequireSafeAndForce(t *testing.T) {
	rules := NewRuleSet(
		Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}},
		Rule{Name: "legacy", Transport: TransportREST, Service: "legacy", Policy: Policy{Timeout: time.Second}},
	)
	admin := NewAdmin(rules, nil)
	body := `[{"name":"orders","transport":"rest","service":"orders","policy":{"timeout":2000000000}}]`

	unsafeRec := httptest.NewRecorder()
	admin.ServeHTTP(unsafeRec, httptest.NewRequest(http.MethodPut, "/rules?requireSafe=1", strings.NewReader(body)))
	if unsafeRec.Code != http.StatusConflict || !strings.Contains(unsafeRec.Body.String(), "not safe") {
		t.Fatalf("requireSafe status = %d body = %s, want conflict", unsafeRec.Code, unsafeRec.Body.String())
	}
	if decision := rules.Match(Request{Transport: TransportREST, Service: "legacy"}); !decision.Matched || decision.RuleName != "legacy" {
		t.Fatalf("legacy decision after rejected update = %#v, want retained", decision)
	}
	guardedEventsRec := httptest.NewRecorder()
	admin.ServeHTTP(guardedEventsRec, httptest.NewRequest(http.MethodGet, "/events?since=1", nil))
	if guardedEventsRec.Code != http.StatusOK {
		t.Fatalf("guarded events status = %d body = %s", guardedEventsRec.Code, guardedEventsRec.Body.String())
	}
	var guardedEvents []RuleSetEvent
	if err := json.NewDecoder(guardedEventsRec.Body).Decode(&guardedEvents); err != nil {
		t.Fatal(err)
	}
	if len(guardedEvents) != 0 {
		t.Fatalf("guarded events = %#v, want no events for rejected update", guardedEvents)
	}

	forceRec := httptest.NewRecorder()
	admin.ServeHTTP(forceRec, httptest.NewRequest(http.MethodPut, "/rules?requireSafe=1&force=1&expectedVersion=1", strings.NewReader(body)))
	if forceRec.Code != http.StatusOK {
		t.Fatalf("force update status = %d body = %s", forceRec.Code, forceRec.Body.String())
	}
	if decision := rules.Match(Request{Transport: TransportREST, Service: "legacy"}); decision.Matched {
		t.Fatalf("legacy decision after force update = %#v, want removed", decision)
	}
}

func TestAdminRulesEndpointRejectsMissingRulesFieldAndInvalidGuards(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)

	missingRec := httptest.NewRecorder()
	admin.ServeHTTP(missingRec, httptest.NewRequest(http.MethodPut, "/rules", strings.NewReader(`{"persist":true}`)))
	if missingRec.Code != http.StatusBadRequest || !strings.Contains(missingRec.Body.String(), "rules field is required") {
		t.Fatalf("missing rules status = %d body = %s, want decode error", missingRec.Code, missingRec.Body.String())
	}

	invalidGuardRec := httptest.NewRecorder()
	admin.ServeHTTP(invalidGuardRec, httptest.NewRequest(http.MethodPut, "/rules?requireSafe=maybe", strings.NewReader(`[]`)))
	if invalidGuardRec.Code != http.StatusBadRequest || !strings.Contains(invalidGuardRec.Body.String(), `invalid requireSafe`) {
		t.Fatalf("invalid guard status = %d body = %s, want guard error", invalidGuardRec.Code, invalidGuardRec.Body.String())
	}

	invalidTTLRec := httptest.NewRecorder()
	admin.ServeHTTP(invalidTTLRec, httptest.NewRequest(http.MethodPut, "/rules?ttl=-1s", strings.NewReader(`[]`)))
	if invalidTTLRec.Code != http.StatusBadRequest || !strings.Contains(invalidTTLRec.Body.String(), `invalid ttl`) {
		t.Fatalf("invalid ttl status = %d body = %s, want ttl error", invalidTTLRec.Code, invalidTTLRec.Body.String())
	}

	invalidPersistRec := httptest.NewRecorder()
	admin.ServeHTTP(invalidPersistRec, httptest.NewRequest(http.MethodPut, "/rules?persist=maybe", strings.NewReader(`[]`)))
	if invalidPersistRec.Code != http.StatusBadRequest || !strings.Contains(invalidPersistRec.Body.String(), `invalid persist`) {
		t.Fatalf("invalid persist status = %d body = %s, want persist error", invalidPersistRec.Code, invalidPersistRec.Body.String())
	}

	invalidBodyTTLRec := httptest.NewRecorder()
	admin.ServeHTTP(invalidBodyTTLRec, httptest.NewRequest(http.MethodPut, "/rules", strings.NewReader(`{"ttl":-1,"rules":[]}`)))
	if invalidBodyTTLRec.Code != http.StatusBadRequest || !strings.Contains(invalidBodyTTLRec.Body.String(), `invalid ttl`) {
		t.Fatalf("invalid body ttl status = %d body = %s, want ttl error", invalidBodyTTLRec.Code, invalidBodyTTLRec.Body.String())
	}

	nullRulesRec := httptest.NewRecorder()
	admin.ServeHTTP(nullRulesRec, httptest.NewRequest(http.MethodPut, "/rules", strings.NewReader(`{"rules":null}`)))
	if nullRulesRec.Code != http.StatusBadRequest || !strings.Contains(nullRulesRec.Body.String(), `rules field must be an array`) {
		t.Fatalf("null rules status = %d body = %s, want rules array error", nullRulesRec.Code, nullRulesRec.Body.String())
	}

	nanTTLRec := httptest.NewRecorder()
	admin.ServeHTTP(nanTTLRec, httptest.NewRequest(http.MethodPut, "/rules?ttl=NaN", strings.NewReader(`[]`)))
	if nanTTLRec.Code != http.StatusBadRequest || !strings.Contains(nanTTLRec.Body.String(), `invalid ttl`) {
		t.Fatalf("nan ttl status = %d body = %s, want ttl error", nanTTLRec.Code, nanTTLRec.Body.String())
	}

	if decision := rules.Match(Request{Transport: TransportREST, Service: "orders"}); !decision.Matched || decision.RuleName != "orders" {
		t.Fatalf("decision = %#v, want original rules retained", decision)
	}
}

func TestAdminValidateEndpointReportsSafeFlag(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}})
	admin := NewAdmin(rules, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader(`[{"name":"orders","transport":"rest","service":"orders","policy":{"retry":{"attempts":2}}}]`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("validate status = %d body = %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["safe"] != false || out["risk"] != "medium" {
		t.Fatalf("validate response = %#v, want ok unsafe medium risk", out)
	}
}

func TestAdminManagerOverridesExplicitRuleSet(t *testing.T) {
	stale := NewRuleSet(Rule{Name: "stale", Transport: TransportREST, Service: "orders"})
	manager, err := NewManager(Config{Rules: []Rule{{Name: "live", Transport: TransportREST, Service: "orders"}}})
	if err != nil {
		t.Fatal(err)
	}

	admin := NewAdmin(stale, nil, WithAdminManager(manager))
	decision := admin.Explain(httptest.NewRequest(http.MethodGet, "/?transport=rest&service=orders", nil)).Decision
	if !decision.Matched || decision.RuleName != "live" {
		t.Fatalf("admin decision = %#v, want manager rule", decision)
	}
}

func TestAdminAuthorization(t *testing.T) {
	admin := NewAdmin(NewRuleSet(), nil, WithAdminAuthorization(
		func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer secret" },
		func(w http.ResponseWriter) { writeAdminError(w, http.StatusUnauthorized, "denied") },
	))

	denied := httptest.NewRecorder()
	admin.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/snapshot", nil))
	if denied.Code != http.StatusUnauthorized || !strings.Contains(denied.Body.String(), "denied") {
		t.Fatalf("denied status = %d body = %s", denied.Code, denied.Body.String())
	}

	allowedReq := httptest.NewRequest(http.MethodGet, "/snapshot", nil)
	allowedReq.Header.Set("Authorization", "Bearer secret")
	allowed := httptest.NewRecorder()
	admin.ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status = %d", allowed.Code)
	}
}

func TestAdminManagerReloadValidateAndPersistRules(t *testing.T) {
	store := kv.NewMemoryStore()
	provider := KVRuleProvider{Store: store, Key: "gofly:rules"}
	if err := provider.Save(context.Background(), []Rule{{Name: "kv-v1", Transport: TransportREST, Service: "orders"}}, 0); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{RuleKey: provider.Key}, WithRuleStore(store, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	admin := NewAdmin(nil, nil, WithAdminManager(manager))

	validateRec := httptest.NewRecorder()
	admin.ServeHTTP(validateRec, httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader(`{"rules":[{"name":"kv-v2","transport":"rest","service":"orders"}]}`)))
	if validateRec.Code != http.StatusOK || !strings.Contains(validateRec.Body.String(), `"ok":true`) {
		t.Fatalf("validate status = %d body = %s", validateRec.Code, validateRec.Body.String())
	}

	persistRec := httptest.NewRecorder()
	admin.ServeHTTP(persistRec, httptest.NewRequest(http.MethodPut, "/rules?persist=true&ttl=1s", strings.NewReader(`[{"name":"kv-v2","transport":"rest","service":"orders","policy":{"headers":{"X-Version":"v2"}}}]`)))
	if persistRec.Code != http.StatusOK {
		t.Fatalf("persist status = %d body = %s", persistRec.Code, persistRec.Body.String())
	}
	decision := manager.RuleSet().Match(Request{Transport: TransportREST, Service: "orders"})
	if decision.RuleName != "kv-v2" || decision.Policy.Headers["X-Version"] != "v2" {
		t.Fatalf("decision = %#v, want persisted v2", decision)
	}
	loaded, err := provider.Load(context.Background())
	if err != nil || len(loaded) != 1 || loaded[0].Name != "kv-v2" {
		t.Fatalf("loaded = %#v err = %v, want persisted provider rules", loaded, err)
	}

	if err := provider.Save(context.Background(), []Rule{{Name: "kv-v3", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v3"}}}}, 0); err != nil {
		t.Fatal(err)
	}
	reloadRec := httptest.NewRecorder()
	admin.ServeHTTP(reloadRec, httptest.NewRequest(http.MethodPost, "/reload", nil))
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("reload status = %d body = %s", reloadRec.Code, reloadRec.Body.String())
	}
	decision = manager.RuleSet().Match(Request{Transport: TransportREST, Service: "orders"})
	if decision.RuleName != "kv-v3" || decision.Policy.Headers["X-Version"] != "v3" {
		t.Fatalf("decision after reload = %#v, want provider v3", decision)
	}
}

func TestAdminEventsEndpointReturnsHistoryAndStreamsUpdates(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders-v1", Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)

	historyRec := httptest.NewRecorder()
	admin.ServeHTTP(historyRec, httptest.NewRequest(http.MethodGet, "/events", nil))
	if historyRec.Code != http.StatusOK {
		t.Fatalf("history status = %d body = %s", historyRec.Code, historyRec.Body.String())
	}
	var history []RuleSetEvent
	if err := json.NewDecoder(historyRec.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Action != "replace" || !history[0].Success || history[0].Rules != 1 {
		t.Fatalf("history = %#v, want initial replace event", history)
	}

	server := httptest.NewServer(admin)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events?stream=1&history=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "application/x-ndjson") {
		t.Fatalf("stream content-type = %q, want ndjson", contentType)
	}

	reader := bufio.NewReader(resp.Body)
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var first RuleSetEvent
	if err := json.Unmarshal([]byte(firstLine), &first); err != nil {
		t.Fatal(err)
	}
	if first.Action != "replace" || first.Version != 1 {
		t.Fatalf("first streamed event = %#v, want history replace version 1", first)
	}

	if err := rules.ReplaceValidated(Rule{Name: "orders-v2", Transport: TransportREST, Service: "orders"}); err != nil {
		t.Fatal(err)
	}
	secondLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var second RuleSetEvent
	if err := json.Unmarshal([]byte(secondLine), &second); err != nil {
		t.Fatal(err)
	}
	if second.Action != "replace" || second.Version != 2 || second.Rules != 1 {
		t.Fatalf("second streamed event = %#v, want live replace version 2", second)
	}
}

func TestAdminEventsEndpointFiltersHistory(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders-v1", Transport: TransportREST, Service: "orders"})
	if err := rules.ReplaceValidated(Rule{Name: "dup"}, Rule{Name: "dup"}); err == nil {
		t.Fatal("ReplaceValidated duplicate succeeded, want error")
	}
	rules.Replace(Rule{Name: "orders-v2", Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)

	failedRec := httptest.NewRecorder()
	admin.ServeHTTP(failedRec, httptest.NewRequest(http.MethodGet, "/events?action=replace&failed=1", nil))
	if failedRec.Code != http.StatusOK {
		t.Fatalf("failed events status = %d body = %s", failedRec.Code, failedRec.Body.String())
	}
	var failed []RuleSetEvent
	if err := json.NewDecoder(failedRec.Body).Decode(&failed); err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].Success || failed[0].Error == "" {
		t.Fatalf("failed events = %#v, want one failed replace", failed)
	}

	limitedRec := httptest.NewRecorder()
	admin.ServeHTTP(limitedRec, httptest.NewRequest(http.MethodGet, "/events?since=1&limit=1", nil))
	if limitedRec.Code != http.StatusOK {
		t.Fatalf("limited events status = %d body = %s", limitedRec.Code, limitedRec.Body.String())
	}
	var limited []RuleSetEvent
	if err := json.NewDecoder(limitedRec.Body).Decode(&limited); err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].Version != 2 || !limited[0].Success {
		t.Fatalf("limited events = %#v, want latest successful event after version 1", limited)
	}

	emptyRec := httptest.NewRecorder()
	admin.ServeHTTP(emptyRec, httptest.NewRequest(http.MethodGet, "/events?limit=0", nil))
	if emptyRec.Code != http.StatusOK {
		t.Fatalf("empty events status = %d body = %s", emptyRec.Code, emptyRec.Body.String())
	}
	var empty []RuleSetEvent
	if err := json.NewDecoder(emptyRec.Body).Decode(&empty); err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty events = %#v, want no events for limit=0", empty)
	}
}

func TestAdminEventsEndpointWaitsForFilteredEvent(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders-v1", Transport: TransportREST, Service: "orders"})
	server := httptest.NewServer(NewAdmin(rules, nil))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events?wait=1&action=replace&success=1&timeout=1s", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		done <- resp
	}()
	time.Sleep(10 * time.Millisecond)
	rules.Replace(Rule{Name: "orders-v2", Transport: TransportREST, Service: "orders"})

	select {
	case err := <-errCh:
		t.Fatal(err)
	case resp := <-done:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("wait status = %d", resp.StatusCode)
		}
		var event RuleSetEvent
		if err := json.NewDecoder(resp.Body).Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Action != "replace" || !event.Success || event.Version != 2 {
			t.Fatalf("wait event = %#v, want successful live replace", event)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestAdminEventsEndpointWaitReturnsMatchingHistoryAfterSince(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders-v1", Transport: TransportREST, Service: "orders"})
	rules.Replace(Rule{Name: "orders-v2", Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events?wait=1&since=1&action=replace&success=1&timeout=1s", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("wait history status = %d body = %s", rec.Code, rec.Body.String())
	}
	var event RuleSetEvent
	if err := json.NewDecoder(rec.Body).Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.Version != 2 || event.Action != "replace" || !event.Success {
		t.Fatalf("wait history event = %#v, want existing version 2 replace", event)
	}
}

func TestAdminEventsEndpointWaitTimeoutReturnsNoContent(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders-v1", Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events?wait=1&action=rollback&timeout=1ms", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("wait timeout status = %d body = %s, want 204", rec.Code, rec.Body.String())
	}
}

func TestAdminEventsEndpointRejectsInvalidFilters(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders-v1", Transport: TransportREST, Service: "orders"}), nil)
	for _, path := range []string{
		"/events?success=maybe",
		"/events?success=1&failed=1",
		"/events?since=-1",
		"/events?limit=-1",
		"/events?wait=1&timeout=-1s",
		"/events?wait=maybe",
		"/events?watch=maybe",
		"/events?stream=1&history=maybe",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
		})
	}

	timeoutRec := httptest.NewRecorder()
	admin.ServeHTTP(timeoutRec, httptest.NewRequest(http.MethodGet, "/events?wait=1&timeout=bad", nil))
	if timeoutRec.Code != http.StatusBadRequest || !strings.Contains(timeoutRec.Body.String(), `invalid timeout`) {
		t.Fatalf("timeout status = %d body = %s, want timeout context", timeoutRec.Code, timeoutRec.Body.String())
	}

	nanTimeoutRec := httptest.NewRecorder()
	admin.ServeHTTP(nanTimeoutRec, httptest.NewRequest(http.MethodGet, "/events?wait=1&timeout=NaN", nil))
	if nanTimeoutRec.Code != http.StatusBadRequest || !strings.Contains(nanTimeoutRec.Body.String(), `invalid timeout`) {
		t.Fatalf("nan timeout status = %d body = %s, want timeout context", nanTimeoutRec.Code, nanTimeoutRec.Body.String())
	}
}

func TestAdminDiffEndpointComparesVersionsAndPostedRules(t *testing.T) {
	rules := NewRuleSet(
		Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v1"}}},
		Rule{Name: "legacy", Transport: TransportREST, Service: "legacy"},
	)
	if err := rules.ReplaceValidated(
		Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v2"}}},
		Rule{Name: "payments", Transport: TransportREST, Service: "payments"},
	); err != nil {
		t.Fatal(err)
	}
	admin := NewAdmin(rules, nil)

	versionRec := httptest.NewRecorder()
	admin.ServeHTTP(versionRec, httptest.NewRequest(http.MethodGet, "/diff?version=1", nil))
	if versionRec.Code != http.StatusOK {
		t.Fatalf("version diff status = %d body = %s", versionRec.Code, versionRec.Body.String())
	}
	var versionDiff RuleDiff
	if err := json.NewDecoder(versionRec.Body).Decode(&versionDiff); err != nil {
		t.Fatal(err)
	}
	if len(versionDiff.Added) != 1 || versionDiff.Added[0].Name != "payments" {
		t.Fatalf("version diff added = %#v, want payments", versionDiff.Added)
	}
	if len(versionDiff.Removed) != 1 || versionDiff.Removed[0].Name != "legacy" {
		t.Fatalf("version diff removed = %#v, want legacy", versionDiff.Removed)
	}
	if len(versionDiff.Changed) != 1 || versionDiff.Changed[0].Before.Policy.Headers["X-Version"] != "v1" || versionDiff.Changed[0].After.Policy.Headers["X-Version"] != "v2" {
		t.Fatalf("version diff changed = %#v, want orders v1 -> v2", versionDiff.Changed)
	}

	posted := []Rule{
		{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v2"}}},
		{Name: "payments", Transport: TransportREST, Service: "payments"},
		{Name: "inventory", Transport: TransportREST, Service: "inventory"},
	}
	body, err := json.Marshal(rulesUpdateRequest{Rules: posted})
	if err != nil {
		t.Fatal(err)
	}
	postRec := httptest.NewRecorder()
	admin.ServeHTTP(postRec, httptest.NewRequest(http.MethodPost, "/diff", bytes.NewReader(body)))
	if postRec.Code != http.StatusOK {
		t.Fatalf("post diff status = %d body = %s", postRec.Code, postRec.Body.String())
	}
	var postDiff RuleDiff
	if err := json.NewDecoder(postRec.Body).Decode(&postDiff); err != nil {
		t.Fatal(err)
	}
	if postDiff.Unchanged != 2 || len(postDiff.Added) != 1 || postDiff.Added[0].Name != "inventory" || len(postDiff.Changed) != 0 || len(postDiff.Removed) != 0 {
		t.Fatalf("post diff = %#v, want two unchanged and inventory added", postDiff)
	}

	notFoundRec := httptest.NewRecorder()
	admin.ServeHTTP(notFoundRec, httptest.NewRequest(http.MethodGet, "/diff?version=99", nil))
	if notFoundRec.Code != http.StatusNotFound || !strings.Contains(notFoundRec.Body.String(), "not found") {
		t.Fatalf("missing version status = %d body = %s", notFoundRec.Code, notFoundRec.Body.String())
	}
}

func TestAdminComponentsStatusStatsHistoryEndpoints(t *testing.T) {
	registry := NewRegistry()
	registry.Register("comp", "limiter", "GET /orders", func() any { return map[string]int{"rate": 10} })
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	_ = rules.Match(Request{Transport: TransportREST, Service: "orders"})
	admin := NewAdmin(rules, registry)

	// Components
	compRec := httptest.NewRecorder()
	admin.ServeHTTP(compRec, httptest.NewRequest(http.MethodGet, "/components", nil))
	if compRec.Code != http.StatusOK {
		t.Fatalf("components status = %d body = %s", compRec.Code, compRec.Body.String())
	}
	var comps []ComponentSnapshot
	if err := json.NewDecoder(compRec.Body).Decode(&comps); err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Kind != "limiter" {
		t.Fatalf("components = %#v, want one limiter", comps)
	}

	// Status
	statusRec := httptest.NewRecorder()
	admin.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/status", nil))
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status status = %d body = %s", statusRec.Code, statusRec.Body.String())
	}
	var status RuleSetStatus
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Rules != 1 {
		t.Fatalf("status = %#v, want 1 rule", status)
	}

	// Stats
	statsRec := httptest.NewRecorder()
	admin.ServeHTTP(statsRec, httptest.NewRequest(http.MethodGet, "/stats", nil))
	if statsRec.Code != http.StatusOK {
		t.Fatalf("stats status = %d body = %s", statsRec.Code, statsRec.Body.String())
	}
	var stats []RuleStats
	if err := json.NewDecoder(statsRec.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Hits != 1 {
		t.Fatalf("stats = %#v, want one hit", stats)
	}

	// History
	historyRec := httptest.NewRecorder()
	admin.ServeHTTP(historyRec, httptest.NewRequest(http.MethodGet, "/history", nil))
	if historyRec.Code != http.StatusOK {
		t.Fatalf("history status = %d body = %s", historyRec.Code, historyRec.Body.String())
	}
	var history []RuleSetEvent
	if err := json.NewDecoder(historyRec.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %#v, want one event", history)
	}

	// Nil admin handlers return 503
	var nilAdmin *Admin
	nilCompRec := httptest.NewRecorder()
	nilAdmin.ServeHTTP(nilCompRec, httptest.NewRequest(http.MethodGet, "/components", nil))
	if nilCompRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil admin components status = %d, want 503", nilCompRec.Code)
	}

	// Method not allowed
	methodRec := httptest.NewRecorder()
	admin.ServeHTTP(methodRec, httptest.NewRequest(http.MethodPost, "/components", nil))
	if methodRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("components POST status = %d, want 405", methodRec.Code)
	}
}

func TestAdminNilRulesHandlersReturnEmpty(t *testing.T) {
	admin := NewAdmin(nil, nil)

	for _, path := range []string{"/status", "/stats", "/history", "/diagnostics"} {
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
	}
}

func TestRollbackVersionExtractsVersion(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/rollback?version=3", nil)
	v, err := rollbackVersion(req)
	if err != nil {
		t.Fatalf("rollbackVersion: %v", err)
	}
	if v != 3 {
		t.Fatalf("version = %d, want 3", v)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/rollback?version=bad", nil)
	if _, err := rollbackVersion(badReq); err == nil {
		t.Fatal("rollbackVersion bad version succeeded, want error")
	}
}

func TestAdminManagerEndpointsNilManager(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/snapshot", ""},
		{http.MethodGet, "/diagnostics", ""},
		{http.MethodGet, "/metrics", ""},
		{http.MethodPost, "/validate", `[{"name":"x","transport":"rest"}]`},
		{http.MethodGet, "/explain", ""},
		{http.MethodGet, "/versions", ""},
		{http.MethodGet, "/diff?version=1", ""},
		{http.MethodPost, "/reload", ""},
		{http.MethodPost, "/plan", `[{"name":"x","transport":"rest"}]`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			admin.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, body))
			if rec.Code == http.StatusServiceUnavailable {
				t.Fatalf("%s status = %d, want not 503", tc.path, rec.Code)
			}
		})
	}
}

func TestAdminHandleVersionDiffZeroVersion(t *testing.T) {
	rules := NewRuleSet(
		Rule{Name: "orders", Transport: TransportREST, Service: "orders"},
	)
	admin := NewAdmin(rules, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/diff?version=0", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("diff version=0 status = %d, want 400", rec.Code)
	}
}

func TestAdminHandleVersionsNilRules(t *testing.T) {
	admin := NewAdmin(nil, nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/versions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("versions status = %d", rec.Code)
	}
	var versions []RuleSetVersion
	if err := json.NewDecoder(rec.Body).Decode(&versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Fatalf("versions = %#v, want empty", versions)
	}
}

func TestAdminHandleReloadNilManager(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/reload", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reload without manager status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminHandleSnapshotMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/snapshot", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("snapshot POST status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleDiagnosticsMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/diagnostics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("diagnostics POST status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleMetricsMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("metrics POST status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleValidateMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/validate", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("validate GET status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleExplainMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/explain", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("explain POST status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleHistoryMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/history", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("history POST status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleStatsMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/stats", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("stats POST status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleStatusMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/status", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status POST status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleVersionsMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/versions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("versions POST status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleDiffMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/diff", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("diff DELETE status = %d, want 405", rec.Code)
	}
}

func TestAdminHandlePlanMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plan", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("plan GET status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleRollbackMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rollback", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("rollback GET status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleReloadMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/reload", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("reload GET status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleRulesMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/rules", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("rules DELETE status = %d, want 405", rec.Code)
	}
}

func TestAdminHandleEventsMethodNotAllowed(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/events", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("events POST status = %d, want 405", rec.Code)
	}
}

func TestAdminCleanAdminPath(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/", "/"},
		{"/admin", "/admin"},
		{"admin", "/admin"},
		{"/admin/", "/admin"},
		{"  /admin/  ", "/admin"},
		{"///", "/"},
	} {
		got := cleanAdminPath(tc.input)
		if got != tc.want {
			t.Fatalf("cleanAdminPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestAdminParseAdminTags(t *testing.T) {
	if got := parseAdminTags(""); got != nil {
		t.Fatalf("parseAdminTags(\"\") = %#v, want nil", got)
	}
	if got := parseAdminTags("a=1,b=2"); len(got) != 2 || got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("parseAdminTags(\"a=1,b=2\") = %#v", got)
	}
	if got := parseAdminTags("a=1,novalue,nospace= , =x"); len(got) != 2 || got["a"] != "1" || got["nospace"] != "" {
		t.Fatalf("parseAdminTags(\"a=1,novalue,nospace= , =x\") = %#v", got)
	}
	if got := parseAdminTags("novalue"); got != nil {
		t.Fatalf("parseAdminTags(\"novalue\") = %#v, want nil", got)
	}
}

func TestAdminRenderAdminMetricsNilRules(t *testing.T) {
	data, err := renderAdminMetrics(nil)
	if err != nil {
		t.Fatalf("renderAdminMetrics(nil) error = %v", err)
	}
	if data != nil {
		t.Fatalf("renderAdminMetrics(nil) = %v, want nil", data)
	}
}

func TestAdminRulesForVersionNilAdmin(t *testing.T) {
	var nilAdmin *Admin
	if _, ok := nilAdmin.rulesForVersion(1); ok {
		t.Fatal("nil admin rulesForVersion should return false")
	}
}

func TestAdminAdminQueryVersionNilRequest(t *testing.T) {
	if _, err := adminQueryVersion(nil); err == nil {
		t.Fatal("adminQueryVersion(nil) should return error")
	}
}

func TestAdminReplaceRulesNilAdmin(t *testing.T) {
	var nilAdmin *Admin
	if err := nilAdmin.ReplaceRules(nil); err == nil {
		t.Fatal("nil admin ReplaceRules should return error")
	}
}

func TestAdminSaveRulesNilAdmin(t *testing.T) {
	var nilAdmin *Admin
	if err := nilAdmin.SaveRules(context.Background(), nil, 0); err == nil {
		t.Fatal("nil admin SaveRules should return error")
	}
}

func TestAdminReloadRulesNilAdmin(t *testing.T) {
	var nilAdmin *Admin
	if err := nilAdmin.ReloadRules(context.Background()); err == nil {
		t.Fatal("nil admin ReloadRules should return error")
	}
}

func TestAdminRollbackRulesNilAdmin(t *testing.T) {
	var nilAdmin *Admin
	if err := nilAdmin.RollbackRules(1); err == nil {
		t.Fatal("nil admin RollbackRules should return error")
	}
}

func TestAdminRelativePath(t *testing.T) {
	admin := NewAdmin(nil, nil, WithAdminPathPrefix("/admin/governance"))
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/admin/governance", "/"},
		{"/admin/governance/snapshot", "/snapshot"},
		{"/other", "/other"},
	} {
		if got := admin.relativePath(tc.path); got != tc.want {
			t.Fatalf("relativePath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestAdminHandleDiffPostInvalidRules(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/diff", strings.NewReader(`[{"name":"dup"},{"name":"dup"}]`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("diff invalid rules status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminHandleValidateInvalidRules(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader(`[{"name":"dup"},{"name":"dup"}]`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("validate invalid rules status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminHandlePlanInvalidRules(t *testing.T) {
	admin := NewAdmin(NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}), nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/plan", strings.NewReader(`[{"name":"dup"},{"name":"dup"}]`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("plan invalid rules status = %d body = %s", rec.Code, rec.Body.String())
	}
	var plan RulePlan
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.OK || plan.Safe || plan.Risk != "high" {
		t.Fatalf("plan = %#v, want invalid high-risk plan", plan)
	}
}
