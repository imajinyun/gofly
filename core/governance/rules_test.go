package governance

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/core/kv"
)

func TestRuleSetMatchesMostSpecificRule(t *testing.T) {
	rules := NewRuleSet(
		Rule{
			Name:      "default-rest",
			Transport: TransportREST,
			Policy:    Policy{Timeout: time.Second},
		},
		Rule{
			Name:      "orders-write",
			Priority:  10,
			Transport: TransportREST,
			Service:   "orders",
			Method:    "post",
			Path:      "/v1/orders/*",
			Tags:      map[string]string{"zone": "cn"},
			Policy:    Policy{Timeout: 3 * time.Second, Retry: RetryPolicy{Attempts: 2}},
		},
	)

	decision := rules.Match(Request{
		Transport: TransportREST,
		Service:   "orders",
		Method:    "POST",
		Path:      "/v1/orders/123",
		Tags:      map[string]string{"zone": "cn"},
	})
	if !decision.Matched || decision.RuleName != "orders-write" {
		t.Fatalf("decision = %#v, want orders-write match", decision)
	}
	if decision.Policy.Timeout != 3*time.Second || decision.Policy.Retry.Attempts != 2 {
		t.Fatalf("policy = %#v, want rule policy", decision.Policy)
	}
	stats := rules.Stats()
	if len(stats) != 1 || stats[0].RuleName != "orders-write" || stats[0].Hits != 1 || stats[0].LastRequest.Service != "orders" {
		t.Fatalf("stats = %#v, want one orders-write hit", stats)
	}
}

func TestRuleSetExplainDoesNotRecordStats(t *testing.T) {
	rules := NewRuleSet(
		Rule{Name: "default", Transport: TransportREST, Policy: Policy{Timeout: time.Second}},
		Rule{Name: "orders", Priority: 10, Transport: TransportREST, Service: "orders", Method: "POST", Path: "/orders/*", Tags: map[string]string{"zone": "cn"}, Policy: Policy{MaxBodyBytes: 1024}},
	)
	explain := rules.Explain(Request{Transport: "REST", Service: "orders", Method: "post", Path: "orders/123", Tags: map[string]string{"zone": "cn"}})
	if !explain.Decision.Matched || explain.Decision.RuleName != "orders" || explain.Decision.Policy.MaxBodyBytes != 1024 {
		t.Fatalf("explain decision = %#v, want orders match", explain.Decision)
	}
	if explain.Request.Method != "POST" || explain.Request.Path != "/orders/123" {
		t.Fatalf("explain request = %#v, want normalized request", explain.Request)
	}
	if len(explain.Evaluations) != 2 || !explain.Evaluations[0].Matched || explain.Evaluations[0].Reason != "matched" {
		t.Fatalf("evaluations = %#v, want matched first rule", explain.Evaluations)
	}
	if stats := rules.Stats(); len(stats) != 0 {
		t.Fatalf("stats = %#v, want explain dry-run to avoid recording hits", stats)
	}

	miss := rules.Explain(Request{Transport: TransportREST, Service: "payments", Method: "POST", Path: "/orders/123"})
	if !miss.Decision.Matched || miss.Decision.RuleName != "default" || len(miss.Evaluations) != 2 || !strings.Contains(miss.Evaluations[0].Reason, "service mismatch") {
		t.Fatalf("miss explain = %#v, want default match and orders mismatch reason", miss)
	}
}

func TestRuleSetSnapshotIsDefensive(t *testing.T) {
	rules := NewRuleSet(Rule{
		Name:   "rpc-rule",
		Policy: Policy{Headers: map[string]string{"X-Gofly": "on"}},
	})
	snapshot := rules.Snapshot()
	snapshot[0].Policy.Headers["X-Gofly"] = "off"

	decision := rules.Match(Request{})
	if decision.Policy.Headers["X-Gofly"] != "on" {
		t.Fatalf("policy headers = %#v, want defensive copy", decision.Policy.Headers)
	}
}

func TestRuleSetReplaceAndNoMatch(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "old", Service: "old"})
	rules.Replace(Rule{Name: "new", Service: "new"})

	if decision := rules.Match(Request{Service: "old"}); decision.Matched {
		t.Fatalf("old decision = %#v, want no match after replace", decision)
	}
	if decision := rules.Match(Request{Service: "new"}); !decision.Matched || decision.RuleName != "new" {
		t.Fatalf("new decision = %#v, want new match", decision)
	}
	if stats := rules.Stats(); len(stats) != 1 || stats[0].RuleName != "new" || stats[0].Hits != 1 {
		t.Fatalf("stats = %#v, want reset stats for new rule", stats)
	}
}

func TestRuleSetLoadFromStaticProvider(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "old", Service: "old"})
	if err := rules.Load(context.Background(), StaticRuleProvider{Rules: []Rule{{Name: "new", Service: "orders", Policy: Policy{Timeout: time.Second}}}}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	decision := rules.Match(Request{Service: "orders"})
	if !decision.Matched || decision.RuleName != "new" || decision.Policy.Timeout != time.Second {
		t.Fatalf("decision = %#v, want loaded new rule", decision)
	}
	status := rules.Status()
	if status.Version != 2 || status.Rules != 1 || status.LastError != "" {
		t.Fatalf("status = %#v, want version 2 loaded", status)
	}
	if history := rules.History(); len(history) != 2 || history[1].Action != "load" || history[1].Source != "static" || !history[1].Success {
		t.Fatalf("history = %#v, want successful static load event", history)
	}

	err := rules.Load(context.Background(), RuleProviderFunc(func(context.Context) ([]Rule, error) {
		return nil, errors.New("backend unavailable")
	}))
	if err == nil || rules.Status().LastError == "" {
		t.Fatalf("Load failure err = %v status = %#v, want last error", err, rules.Status())
	}
	if history := rules.History(); len(history) != 3 || history[2].Action != "load" || history[2].Success || history[2].Error == "" {
		t.Fatalf("history = %#v, want failed load event", history)
	}
	if decision := rules.Match(Request{Service: "orders"}); !decision.Matched {
		t.Fatalf("decision after failed load = %#v, want previous rules retained", decision)
	}

	invalidErr := rules.Load(context.Background(), StaticRuleProvider{Rules: []Rule{{Name: "bad", Transport: "unknown"}}})
	if invalidErr == nil || !strings.Contains(invalidErr.Error(), "unknown transport") || rules.Status().LastError == "" {
		t.Fatalf("invalid load err = %v status = %#v, want validation error", invalidErr, rules.Status())
	}
	if decision := rules.Match(Request{Service: "orders"}); !decision.Matched {
		t.Fatalf("decision after invalid load = %#v, want previous rules retained", decision)
	}
}

func TestRuleSetVersionsAndRollback(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "v1", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v1"}}})
	rules.Replace(Rule{Name: "v2", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v2"}}})

	versions := rules.Versions()
	if len(versions) != 2 || versions[0].Version != 1 || versions[1].Version != 2 || versions[1].Rules[0].Name != "v2" {
		t.Fatalf("versions = %#v, want v1/v2 snapshots", versions)
	}
	versions[0].Rules[0].Name = "mutated"
	if rules.Versions()[0].Rules[0].Name != "v1" {
		t.Fatal("Versions returned mutable internal rules")
	}

	if err := rules.Rollback(1); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	decision := rules.Match(Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "v1" || decision.Policy.Headers["X-Version"] != "v1" {
		t.Fatalf("decision = %#v, want rolled back v1", decision)
	}
	status := rules.Status()
	if status.Version != 3 || status.Versions != 3 || status.LastError != "" {
		t.Fatalf("status = %#v, want rollback recorded as new version", status)
	}
	latest := rules.Versions()[2]
	if latest.Action != "rollback" || latest.Source != "version:1" || latest.Rules[0].Name != "v1" {
		t.Fatalf("latest version = %#v, want rollback snapshot", latest)
	}

	if err := rules.Rollback(99); err == nil || !strings.Contains(err.Error(), "not found") || rules.Status().LastError == "" {
		t.Fatalf("Rollback missing err = %v status = %#v, want recorded error", err, rules.Status())
	}
}

func TestRuleSetSubscribePublishesEventsAndUnsubscribes(t *testing.T) {
	rules := NewRuleSet()
	events, unsubscribe := rules.Subscribe(4)
	if status := rules.Status(); status.Subscribers != 1 {
		t.Fatalf("subscribers = %d, want 1", status.Subscribers)
	}

	rules.Replace(Rule{Name: "v1", Transport: TransportREST, Service: "orders"})
	event := waitForRuleSetEvent(t, events)
	if event.Action != "replace" || !event.Success || event.Version != 2 || event.Rules != 1 {
		t.Fatalf("event = %#v, want successful replace", event)
	}

	err := rules.ReplaceValidated(Rule{Name: "dup"}, Rule{Name: "dup"})
	if err == nil {
		t.Fatal("ReplaceValidated duplicate succeeded, want error")
	}
	event = waitForRuleSetEvent(t, events)
	if event.Action != "replace" || event.Success || !strings.Contains(event.Error, "duplicate") {
		t.Fatalf("event = %#v, want failed replace", event)
	}

	unsubscribe()
	unsubscribe()
	if _, ok := <-events; ok {
		t.Fatal("event channel is still open after unsubscribe")
	}
	if status := rules.Status(); status.Subscribers != 0 {
		t.Fatalf("subscribers = %d, want 0", status.Subscribers)
	}
}

func TestSelectCanaryMatchesHeadersCookiesAndRatio(t *testing.T) {
	httpReq := httptest.NewRequest(http.MethodGet, "/orders", nil)
	httpReq.Header.Set("X-Use-Gray", "1")
	httpReq.AddCookie(&http.Cookie{Name: "tenant", Value: "beta"})
	req := HTTPRequest(TransportREST, "orders", httpReq, nil)
	decision := SelectCanary(CanaryPolicy{
		Ratio:          1,
		Service:        "orders-gray",
		Target:         "http://127.0.0.1:8081",
		UpstreamPrefix: "/v2",
		Headers:        map[string]string{"X-Gray": "true"},
		MatchHeaders:   map[string]string{"X-Use-Gray": "1"},
		MatchCookies:   map[string]string{"tenant": "beta"},
	}, req)
	if !decision.Selected || decision.Service != "orders-gray" || decision.Target == "" || decision.UpstreamPrefix != "/v2" || decision.Headers["X-Gray"] != "true" {
		t.Fatalf("decision = %#v, want selected canary", decision)
	}
	decision.Headers["X-Gray"] = "mutated"
	again := SelectCanary(CanaryPolicy{Ratio: 1, Headers: map[string]string{"X-Gray": "true"}}, req)
	if again.Headers["X-Gray"] != "true" {
		t.Fatal("SelectCanary returned mutable policy headers")
	}
	miss := SelectCanary(CanaryPolicy{MatchHeaders: map[string]string{"X-Use-Gray": "0"}}, req)
	if miss.Selected {
		t.Fatalf("miss = %#v, want unselected canary", miss)
	}
}

func TestRuleSetHistoryKeepsRecentEvents(t *testing.T) {
	rules := NewRuleSet()
	for i := 0; i < defaultRuleSetEventLimit+5; i++ {
		rules.Replace(Rule{Name: "rule"})
	}
	history := rules.History()
	if len(history) != defaultRuleSetEventLimit {
		t.Fatalf("history len = %d, want %d", len(history), defaultRuleSetEventLimit)
	}
	if history[0].Version != 7 || history[len(history)-1].Version != int64(defaultRuleSetEventLimit+5+1) {
		t.Fatalf("history versions = first %d last %d, want recent window", history[0].Version, history[len(history)-1].Version)
	}
	history[0].Action = "mutated"
	if rules.History()[0].Action == "mutated" {
		t.Fatal("History returned mutable internal slice")
	}
}

func waitForRuleSetEvent(t *testing.T, events <-chan RuleSetEvent) RuleSetEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for governance rule event")
		return RuleSetEvent{}
	}
}

func TestValidateRulesRejectsUnsafePolicy(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		want string
	}{
		{name: "unknown transport", rule: Rule{Transport: "dubbo"}, want: "unknown transport"},
		{name: "negative timeout", rule: Rule{Policy: Policy{Timeout: -time.Second}}, want: "timeout"},
		{name: "invalid retry status", rule: Rule{Policy: Policy{Retry: RetryPolicy{Statuses: []int{99}}}}, want: "retry status"},
		{name: "invalid breaker ratio", rule: Rule{Policy: Policy{Breaker: BreakerPolicy{FailureRatio: 2}}}, want: "failure ratio"},
		{name: "invalid canary ratio", rule: Rule{Policy: Policy{Canary: CanaryPolicy{Ratio: 1.1}}}, want: "canary ratio"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRules(tt.rule)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateRules error = %v, want containing %q", err, tt.want)
			}
		})
	}
	if err := ValidateRules(Rule{Name: "dup"}, Rule{Name: "dup"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate validation error = %v, want duplicate", err)
	}
}

func TestKVRuleProviderLoadAndWatch(t *testing.T) {
	store := kv.NewMemoryStore()
	provider := KVRuleProvider{Store: store, Key: "gofly:rules"}
	writeRules := func(rules []Rule) {
		if err := provider.Save(context.Background(), rules, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	writeRules([]Rule{{Name: "rest-v1", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v1"}}}})
	rules := NewRuleSet()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		err := rules.Watch(ctx, provider, time.Millisecond)
		if !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()

	deadline := time.After(time.Second)
	for rules.Status().Rules == 0 {
		select {
		case err := <-errCh:
			t.Fatalf("Watch returned error: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for initial rules")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	writeRules([]Rule{{Name: "rest-v2", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v2"}}}})
	deadline = time.After(time.Second)
	for {
		decision := rules.Match(Request{Transport: TransportREST, Service: "orders"})
		if decision.RuleName == "rest-v2" && decision.Policy.Headers["X-Version"] == "v2" {
			return
		}
		select {
		case err := <-errCh:
			t.Fatalf("Watch returned error: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for watched rules")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestKVRuleProviderSaveValidatesAndPersistsRules(t *testing.T) {
	store := kv.NewMemoryStore()
	provider := KVRuleProvider{Store: store, Key: "gofly:rules"}
	if err := provider.Save(context.Background(), []Rule{{Name: "bad", Policy: Policy{Canary: CanaryPolicy{Ratio: -0.1}}}}, time.Minute); err == nil {
		t.Fatal("Save invalid rules succeeded, want validation error")
	}
	if ok, err := store.Exists(context.Background(), provider.Key); err != nil || ok {
		t.Fatalf("Exists invalid save = %v, %v; want false, nil", ok, err)
	}
	want := []Rule{{Name: "ok", Transport: TransportGateway, Path: "/api/*", Policy: Policy{MaxBodyBytes: 1024}}}
	if err := provider.Save(context.Background(), want, time.Minute); err != nil {
		t.Fatalf("Save valid rules: %v", err)
	}
	got, err := provider.Load(context.Background())
	if err != nil {
		t.Fatalf("Load saved rules: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ok" || got[0].Path != "/api/*" || got[0].Policy.MaxBodyBytes != 1024 {
		t.Fatalf("loaded rules = %#v, want saved normalized rules", got)
	}
}

func TestFileRuleProviderSaveBranches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	provider := FileRuleProvider{Path: path}

	// Save valid rules
	if err := provider.Save(context.Background(), []Rule{{Name: "ok", Transport: TransportREST}}, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Save empty path
	empty := FileRuleProvider{Path: "   "}
	if err := empty.Save(context.Background(), []Rule{{Name: "ok", Transport: TransportREST}}, 0); err == nil {
		t.Fatal("Save empty path succeeded, want error")
	}

	// Save invalid rules
	if err := provider.Save(context.Background(), []Rule{{Name: "bad", Transport: "unknown"}}, 0); err == nil {
		t.Fatal("Save invalid rules succeeded, want error")
	}

	// Save cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := provider.Save(ctx, []Rule{{Name: "ok", Transport: TransportREST}}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save canceled error = %v, want context.Canceled", err)
	}
}

func TestRuleSetDiagnosticsAndSetLastError(t *testing.T) {
	// Diagnostics on nil RuleSet
	var nilRules *RuleSet
	if diag := nilRules.Diagnostics(); diag != nil {
		t.Fatalf("nil diagnostics = %#v, want nil", diag)
	}

	// Diagnostics on empty RuleSet
	rules := NewRuleSet(Rule{Name: "empty", Transport: TransportREST})
	diagnostics := rules.Diagnostics()
	if !hasDiagnostic(diagnostics, "empty_policy", "empty") {
		t.Fatalf("diagnostics = %#v, want empty_policy", diagnostics)
	}

	// setLastError nil-safe
	nilRules.setLastError(errors.New("test"))
	nilRules.setLastError(nil)
}

func TestPathCoversAndPathMatches(t *testing.T) {
	for _, tc := range []struct {
		base      string
		candidate string
		want      bool
	}{
		{"", "/anything", true},
		{"*", "/anything", true},
		{"/", "/anything", true},
		{"/api", "/api", true},
		{"/api", "/api/v1", false},
		{"/api/*", "/api/v1", true},
		{"/api/*", "/api", false},
		{"/api/*", "/other", false},
	} {
		if got := pathCovers(tc.base, tc.candidate); got != tc.want {
			t.Fatalf("pathCovers(%q, %q) = %v, want %v", tc.base, tc.candidate, got, tc.want)
		}
	}

	for _, tc := range []struct {
		pattern string
		path    string
		want    bool
	}{
		{"", "/anything", true},
		{"*", "/anything", true},
		{"/", "/anything", true},
		{"/", "no-leading-slash", false},
		{"/api", "/api", true},
		{"/api", "/api/v1", false},
		{"/api/*", "/api/v1", true},
		{"/api/*", "/api", false},
		{"/api/*", "/other", false},
	} {
		if got := pathMatches(tc.pattern, tc.path); got != tc.want {
			t.Fatalf("pathMatches(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestRuleKey(t *testing.T) {
	if got := ruleKey(Rule{Name: "orders"}); got != "name:orders" {
		t.Fatalf("ruleKey named = %q, want name:orders", got)
	}
	if got := ruleKey(Rule{Transport: TransportREST, Service: "orders"}); got != "rest|orders||" {
		t.Fatalf("ruleKey unnamed = %q, want rest|orders||", got)
	}
	if got := ruleKey(Rule{Transport: TransportREST, Service: "orders", Tags: map[string]string{"zone": "cn", "env": "prod"}}); got != "rest|orders|||env=prod,zone=cn" {
		t.Fatalf("ruleKey with tags = %q, want rest|orders|||env=prod,zone=cn", got)
	}
}

func TestRuleCovers(t *testing.T) {
	base := Rule{Transport: TransportREST, Service: "orders", Method: "GET", Path: "/api/*", Tags: map[string]string{"zone": "cn"}}
	for _, tc := range []struct {
		candidate Rule
		want      bool
	}{
		{Rule{Transport: TransportREST, Service: "orders", Method: "GET", Path: "/api/v1", Tags: map[string]string{"zone": "cn"}}, true},
		{Rule{Transport: TransportRPC, Service: "orders", Method: "GET", Path: "/api/v1", Tags: map[string]string{"zone": "cn"}}, false},
		{Rule{Transport: TransportREST, Service: "payments", Method: "GET", Path: "/api/v1", Tags: map[string]string{"zone": "cn"}}, false},
		{Rule{Transport: TransportREST, Service: "orders", Method: "POST", Path: "/api/v1", Tags: map[string]string{"zone": "cn"}}, false},
		{Rule{Transport: TransportREST, Service: "orders", Method: "GET", Path: "/other", Tags: map[string]string{"zone": "cn"}}, false},
		{Rule{Transport: TransportREST, Service: "orders", Method: "GET", Path: "/api/v1", Tags: map[string]string{"zone": "us"}}, false},
		{Rule{Transport: TransportREST, Service: "orders", Method: "GET", Path: "/api/v1"}, false},
	} {
		if got := ruleCovers(base, tc.candidate); got != tc.want {
			t.Fatalf("ruleCovers(%+v, %+v) = %v, want %v", base, tc.candidate, got, tc.want)
		}
	}
}

func TestRuleSetNilGuards(t *testing.T) {
	var nilRules *RuleSet

	if got := nilRules.Snapshot(); got != nil {
		t.Fatalf("nil Snapshot = %#v, want nil", got)
	}
	if got := nilRules.Stats(); got != nil {
		t.Fatalf("nil Stats = %#v, want nil", got)
	}
	if got := nilRules.Status(); got != (RuleSetStatus{}) {
		t.Fatalf("nil Status = %#v, want empty", got)
	}
	if got := nilRules.History(); got != nil {
		t.Fatalf("nil History = %#v, want nil", got)
	}
	if got := nilRules.Versions(); got != nil {
		t.Fatalf("nil Versions = %#v, want nil", got)
	}
	if got := nilRules.Match(Request{}); got.Matched {
		t.Fatal("nil Match should return empty decision")
	}
	if got := nilRules.Decide(Request{}); got.Matched {
		t.Fatal("nil Decide should return empty decision")
	}
	if err := nilRules.ReplaceValidated(); err != nil {
		t.Fatalf("nil ReplaceValidated = %v, want nil", err)
	}
	if err := nilRules.Rollback(1); err != nil {
		t.Fatalf("nil Rollback = %v, want nil", err)
	}
	if err := nilRules.Load(context.Background(), nil); err != nil {
		t.Fatalf("nil Load = %v, want nil", err)
	}
	if err := nilRules.Watch(context.Background(), nil, 0); err != nil {
		t.Fatalf("nil Watch = %v, want nil", err)
	}
	if got := nilRules.Explain(Request{}); got.Decision.Matched {
		t.Fatal("nil Explain should return unmatched decision")
	}

	// Subscribe on nil RuleSet returns closed channel
	events, unsubscribe := nilRules.Subscribe(1)
	unsubscribe()
	if _, ok := <-events; ok {
		t.Fatal("nil Subscribe should return closed channel")
	}
}

func TestRuleSetRecordMatchNilGuard(t *testing.T) {
	var nilRules *RuleSet
	nilRules.recordMatch(Rule{Name: "orders"}, Request{Transport: TransportREST})
	nilRules.recordFailure("test", "src", errors.New("err"))
}

func TestStaticRuleProviderSource(t *testing.T) {
	p := StaticRuleProvider{Rules: []Rule{{Name: "orders"}}}
	if got := p.Source(); got != "static" {
		t.Fatalf("Source = %q, want static", got)
	}
	p2 := StaticRuleProvider{Rules: []Rule{{Name: "orders"}}, Name: "custom"}
	if got := p2.Source(); got != "custom" {
		t.Fatalf("Source = %q, want custom", got)
	}
}

func TestProviderSourceFallback(t *testing.T) {
	p := RuleProviderFunc(func(context.Context) ([]Rule, error) { return nil, nil })
	if got := providerSource(p); got != "provider" {
		t.Fatalf("providerSource = %q, want provider", got)
	}
}

func TestKVRuleProviderNilStoreAndEmptyKey(t *testing.T) {
	p := KVRuleProvider{Store: nil, Key: "test"}
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("Load nil store should error")
	}
	if err := p.Save(context.Background(), []Rule{{Name: "ok", Transport: TransportREST}}, 0); err == nil {
		t.Fatal("Save nil store should error")
	}

	p2 := KVRuleProvider{Store: kv.NewMemoryStore(), Key: "   "}
	if _, err := p2.Load(context.Background()); err == nil {
		t.Fatal("Load empty key should error")
	}
	if err := p2.Save(context.Background(), []Rule{{Name: "ok", Transport: TransportREST}}, 0); err == nil {
		t.Fatal("Save empty key should error")
	}
}

func TestFileRuleProviderEmptyPath(t *testing.T) {
	p := FileRuleProvider{Path: "   "}
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("Load empty path should error")
	}
	if err := p.Save(context.Background(), []Rule{{Name: "ok", Transport: TransportREST}}, 0); err == nil {
		t.Fatal("Save empty path should error")
	}
}

func TestRuleProviderFuncLoad(t *testing.T) {
	p := RuleProviderFunc(func(context.Context) ([]Rule, error) {
		return []Rule{{Name: "ok", Transport: TransportREST}}, nil
	})
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("Load = %#v", got)
	}
}

func TestDiffRuleKey(t *testing.T) {
	if got := diffRuleKey(Rule{Name: "orders"}); got != "name:orders" {
		t.Fatalf("diffRuleKey named = %q, want name:orders", got)
	}
	if got := diffRuleKey(Rule{Transport: TransportREST, Service: "orders"}); got != "rest|orders||" {
		t.Fatalf("diffRuleKey unnamed with empty name = %q, want rest|orders||", got)
	}
}

func TestNormalizeRequestForMatch(t *testing.T) {
	req := normalizeRequestForMatch(Request{Transport: "  REST  ", Service: "orders", Method: "  get  ", Path: "orders/1"})
	if req.Transport != "rest" || req.Method != "GET" || req.Path != "/orders/1" {
		t.Fatalf("normalizeRequestForMatch = %#v", req)
	}
}

func TestCanaryMatchesRatioOnly(t *testing.T) {
	req := Request{Transport: TransportREST, Service: "orders", Method: "GET", Path: "/orders"}
	// Ratio=1 without predicates should match
	if !canaryMatches(CanaryPolicy{Ratio: 1}, req) {
		t.Fatal("canaryMatches ratio=1 should match")
	}
	// Ratio=0 without predicates should not match
	if canaryMatches(CanaryPolicy{Ratio: 0}, req) {
		t.Fatal("canaryMatches ratio=0 should not match")
	}
	// Ratio > 1 is clamped to 1
	if !canaryMatches(CanaryPolicy{Ratio: 2}, req) {
		t.Fatal("canaryMatches ratio=2 should match (clamped)")
	}
}

func TestWritePrometheusNilWriter(t *testing.T) {
	rules := NewRuleSet(Rule{Name: "orders", Transport: TransportREST, Service: "orders"})
	if err := rules.WritePrometheus(nil); err != nil {
		t.Fatalf("WritePrometheus(nil) = %v, want nil", err)
	}
}

func TestAnalyzeRulesBroadMatchAndRetryWithoutTimeout(t *testing.T) {
	// broad_match: empty rule matches everything
	rules := []Rule{{Name: "broad"}}
	diagnostics := AnalyzeRules(rules)
	if !hasDiagnostic(diagnostics, "broad_match", "broad") {
		t.Fatalf("diagnostics = %#v, want broad_match", diagnostics)
	}

	// retry_without_timeout
	rules = []Rule{{Name: "retry", Transport: TransportREST, Policy: Policy{Retry: RetryPolicy{Attempts: 2}}}}
	diagnostics = AnalyzeRules(rules)
	if !hasDiagnostic(diagnostics, "retry_without_timeout", "retry") {
		t.Fatalf("diagnostics = %#v, want retry_without_timeout", diagnostics)
	}

	// canary_without_target
	rules = []Rule{{Name: "canary", Transport: TransportREST, Policy: Policy{Canary: CanaryPolicy{Ratio: 0.5}}}}
	diagnostics = AnalyzeRules(rules)
	if !hasDiagnostic(diagnostics, "canary_without_target", "canary") {
		t.Fatalf("diagnostics = %#v, want canary_without_target", diagnostics)
	}
}

func TestRuleSetWatchCanceledContext(t *testing.T) {
	rules := NewRuleSet()
	provider := StaticRuleProvider{Rules: []Rule{{Name: "ok", Transport: TransportREST}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rules.Watch(ctx, provider, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("Watch canceled context = %v, want context.Canceled", err)
	}
}

func TestEventFilterMatch(t *testing.T) {
	f := eventFilter{Action: "replace", SuccessSet: true, Success: true, MinVersionSet: true, MinVersion: 1}
	if !f.match(RuleSetEvent{Action: "replace", Success: true, Version: 2}) {
		t.Fatal("match should return true")
	}
	if f.match(RuleSetEvent{Action: "rollback", Success: true, Version: 2}) {
		t.Fatal("match wrong action should return false")
	}
	if f.match(RuleSetEvent{Action: "replace", Success: false, Version: 2}) {
		t.Fatal("match wrong success should return false")
	}
	if f.match(RuleSetEvent{Action: "replace", Success: true, Version: 1}) {
		t.Fatal("match wrong version should return false")
	}
}

func TestEventFilterFilterEvents(t *testing.T) {
	events := []RuleSetEvent{
		{Action: "replace", Version: 1},
		{Action: "replace", Version: 2},
		{Action: "rollback", Version: 3},
	}
	f := eventFilter{LimitSet: true, Limit: 1}
	got := f.filterEvents(events)
	if len(got) != 1 || got[0].Version != 3 {
		t.Fatalf("filterEvents = %#v, want last 1", got)
	}

	f = eventFilter{LimitSet: true, Limit: 0}
	if got := f.filterEvents(events); got != nil {
		t.Fatalf("filterEvents limit=0 = %#v, want nil", got)
	}
}

func TestHTTPRequestNilRequest(t *testing.T) {
	req := HTTPRequest(TransportREST, "orders", nil, nil)
	if req.Transport != "rest" || req.Service != "orders" {
		t.Fatalf("HTTPRequest nil = %#v", req)
	}
}

func TestCookiesMapNilAndEmpty(t *testing.T) {
	if got := cookiesMap(nil); got != nil {
		t.Fatalf("cookiesMap nil = %#v", got)
	}
	if got := cookiesMap([]*http.Cookie{{Name: ""}}); got != nil {
		t.Fatalf("cookiesMap empty name = %#v", got)
	}
	if got := cookiesMap([]*http.Cookie{nil}); got != nil {
		t.Fatalf("cookiesMap nil cookie = %#v", got)
	}
}

func TestHeaderMapEmpty(t *testing.T) {
	if got := headerMap(nil); got != nil {
		t.Fatalf("headerMap nil = %#v", got)
	}
	if got := headerMap(http.Header{"X-Empty": {}}); got != nil {
		t.Fatalf("headerMap empty values = %#v", got)
	}
}

func TestCloneStringMapEmpty(t *testing.T) {
	if got := cloneStringMap(nil); got != nil {
		t.Fatalf("cloneStringMap nil = %#v", got)
	}
	if got := cloneStringMap(map[string]string{}); got != nil {
		t.Fatalf("cloneStringMap empty = %#v", got)
	}
}

func TestCloneRulesEmpty(t *testing.T) {
	if got := cloneRules(nil); got != nil {
		t.Fatalf("cloneRules nil = %#v", got)
	}
	if got := cloneRules([]Rule{}); got != nil {
		t.Fatalf("cloneRules empty = %#v", got)
	}
}

func TestValidatePolicyBreakerAndCanaryEdgeCases(t *testing.T) {
	if err := validatePolicy(Policy{Breaker: BreakerPolicy{FailureRatio: math.NaN()}}); err == nil {
		t.Fatal("validatePolicy NaN failure ratio should error")
	}
	if err := validatePolicy(Policy{Breaker: BreakerPolicy{FailureRatio: math.Inf(1)}}); err == nil {
		t.Fatal("validatePolicy Inf failure ratio should error")
	}
	if err := validatePolicy(Policy{Canary: CanaryPolicy{Ratio: math.NaN()}}); err == nil {
		t.Fatal("validatePolicy NaN canary ratio should error")
	}
	if err := validatePolicy(Policy{Canary: CanaryPolicy{Ratio: math.Inf(1)}}); err == nil {
		t.Fatal("validatePolicy Inf canary ratio should error")
	}
}
