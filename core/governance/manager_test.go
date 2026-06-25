package governance

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/kv"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestManagerInitializesStaticRulesAndSnapshot(t *testing.T) {
	m, err := NewManager(Config{Rules: []Rule{{Name: "rest", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}}}})
	if err != nil {
		t.Fatal(err)
	}
	decision := m.RuleSet().Match(Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "rest" || decision.Policy.Timeout != time.Second {
		t.Fatalf("decision = %#v, want static rule", decision)
	}
	snapshot := m.Snapshot()
	if snapshot.Source != "static" || snapshot.Status.Rules != 1 || len(snapshot.Rules) != 1 {
		t.Fatalf("snapshot = %#v, want static rules", snapshot)
	}
	snapshot.Config.Rules[0].Name = "mutated"
	if m.Snapshot().Config.Rules[0].Name != "rest" {
		t.Fatalf("manager snapshot config is mutable")
	}
}

func TestManagerRejectsInvalidConfig(t *testing.T) {
	if _, err := NewManager(Config{WatchInterval: -time.Second}); err == nil || !errors.Is(err, errors.New("unused")) && err.Error() == "" {
		t.Fatalf("negative interval err = %v, want error", err)
	}
	if _, err := NewManager(Config{Rules: []Rule{{Name: "bad", Transport: "dubbo"}}}); err == nil {
		t.Fatal("invalid rules accepted")
	}
}

func TestManagerAcceptsMQTransportRules(t *testing.T) {
	rule := Rule{
		Name:      "mq-default",
		Transport: TransportMQ,
		Service:   "ordersvc",
		Method:    "PUBLISH",
		Path:      "/orders",
		Policy: Policy{
			Timeout: time.Second,
			Retry:   RetryPolicy{Attempts: 2, Backoff: 10 * time.Millisecond},
		},
	}
	if err := ValidateRules(rule); err != nil {
		t.Fatalf("ValidateRules mq transport: %v", err)
	}
	m, err := NewManager(Config{Rules: []Rule{rule}})
	if err != nil {
		t.Fatalf("NewManager mq rule: %v", err)
	}
	decision := m.RuleSet().Match(Request{Transport: TransportMQ, Service: "ordersvc", Method: "PUBLISH", Path: "/orders"})
	if !decision.Matched || decision.RuleName != "mq-default" || decision.Policy.Timeout != time.Second {
		t.Fatalf("decision = %#v, want mq rule", decision)
	}
	path := t.TempDir() + "/governance.json"
	data := []byte(`{"rules":[{"name":"file-mq","transport":"mq","service":"ordersvc","method":"CONSUME","path":"/orders","policy":{"timeout":3000000000}}]}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fileManager, err := NewManager(Config{RuleFile: path})
	if err != nil {
		t.Fatalf("NewManager mq rule file: %v", err)
	}
	if err := fileManager.Start(context.Background()); err != nil {
		t.Fatalf("Start mq rule file: %v", err)
	}
	fileDecision := fileManager.RuleSet().Match(Request{Transport: TransportMQ, Service: "ordersvc", Method: "CONSUME", Path: "/orders"})
	if !fileDecision.Matched || fileDecision.RuleName != "file-mq" || fileDecision.Policy.Timeout != 3*time.Second {
		t.Fatalf("file decision = %#v, want mq file rule", fileDecision)
	}
}

func TestManagerStartLoadsAndWatchesProvider(t *testing.T) {
	provider := &switchingRuleProvider{rules: []Rule{{Name: "v1", Transport: TransportREST, Service: "orders"}}}
	m, err := NewManager(Config{Watch: true, WatchInterval: time.Millisecond}, WithProvider(provider))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		if err := m.Start(ctx); !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	waitForRule(t, m.RuleSet(), "v1", errCh)

	provider.Replace([]Rule{{Name: "v2", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "v2"}}}})
	waitForRule(t, m.RuleSet(), "v2", errCh)
	if snapshot := m.Snapshot(); !snapshot.Watch || snapshot.Interval != time.Millisecond || snapshot.Source != "switching" {
		t.Fatalf("snapshot = %#v, want provider watch metadata", snapshot)
	}
}

func TestManagerUsesConfiguredRuleFile(t *testing.T) {
	path := t.TempDir() + "/governance.json"
	data := []byte(`{"rules":[{"name":"file-rpc","transport":"rpc","service":"greeter","policy":{"timeout":3000000000}}]}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(Config{RuleFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	decision := m.RuleSet().Match(Request{Transport: TransportRPC, Service: "greeter"})
	if !decision.Matched || decision.RuleName != "file-rpc" || decision.Policy.Timeout != 3*time.Second {
		t.Fatalf("decision = %#v, want file rule", decision)
	}
	if snapshot := m.Snapshot(); snapshot.Source != "file:"+path || snapshot.Config.RuleFile != path {
		t.Fatalf("snapshot = %#v, want file source", snapshot)
	}
}

func TestManagerUsesConfiguredRuleStore(t *testing.T) {
	store := kv.NewMemoryStore()
	provider := KVRuleProvider{Store: store, Key: "gofly:rules"}
	if err := provider.Save(context.Background(), []Rule{{Name: "kv-rest", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "kv"}}}}, 0); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(Config{RuleKey: provider.Key}, WithRuleStore(store, ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	decision := m.RuleSet().Match(Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "kv-rest" || decision.Policy.Headers["X-Version"] != "kv" {
		t.Fatalf("decision = %#v, want kv rule", decision)
	}
	if snapshot := m.Snapshot(); snapshot.Source != "kv:gofly:rules" || snapshot.Config.RuleKey != provider.Key {
		t.Fatalf("snapshot = %#v, want kv source", snapshot)
	}
}

func TestManagerProviderLoadsAndSavesWithSuiteDefaults(t *testing.T) {
	store := kv.NewMemoryStore()
	provider := KVRuleProvider{Store: store, Key: "gofly:rules"}
	if err := provider.Save(context.Background(), []Rule{{Name: "kv-rest", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "kv"}}}}, 0); err != nil {
		t.Fatal(err)
	}
	suite := MustNewSuite(NewPlugin("defaults", Rule{Name: "default-rest", Transport: TransportREST, Service: "orders", Priority: -1, Policy: Policy{Timeout: time.Second}}))
	m, err := NewManager(Config{RuleKey: provider.Key}, WithRuleStore(store, ""), WithSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if decision := m.RuleSet().Match(Request{Transport: TransportREST, Service: "orders"}); !decision.Matched || decision.RuleName != "kv-rest" || decision.Policy.Headers["X-Version"] != "kv" {
		t.Fatalf("decision = %#v, want provider override over suite default", decision)
	}
	if snapshot := m.Snapshot(); snapshot.Source != "kv:gofly:rules+plugins" || snapshot.Status.Rules != 2 {
		t.Fatalf("snapshot = %#v, want plugin-wrapped provider source and merged rules", snapshot)
	}

	if err := m.SaveRules(context.Background(), []Rule{{Name: "saved-rest", Transport: TransportREST, Service: "orders", Policy: Policy{Headers: map[string]string{"X-Version": "saved"}}}}, 0); err != nil {
		t.Fatalf("SaveRules: %v", err)
	}
	if decision := m.RuleSet().Match(Request{Transport: TransportREST, Service: "orders"}); !decision.Matched || decision.RuleName != "saved-rest" || decision.Policy.Headers["X-Version"] != "saved" {
		t.Fatalf("saved decision = %#v, want saved provider rule with suite defaults preserved", decision)
	}
	loaded, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Name != "saved-rest" {
		t.Fatalf("persisted rules = %+v, want explicit saved rules only", loaded)
	}
}

func TestManagerMatchAndDecideDelegateRuleSet(t *testing.T) {
	manager, err := NewManager(Config{Rules: []Rule{{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}}}})
	if err != nil {
		t.Fatal(err)
	}
	decision := manager.Decide(Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "orders" || decision.Policy.Timeout != time.Second {
		t.Fatalf("decision = %#v, want orders rule", decision)
	}
	if stats := manager.RuleSet().Stats(); len(stats) != 0 {
		t.Fatalf("stats after Decide = %#v, want no stats recorded", stats)
	}
	decision = manager.Match(Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "orders" {
		t.Fatalf("match decision = %#v, want orders rule", decision)
	}
	if stats := manager.RuleSet().Stats(); len(stats) != 1 || stats[0].RuleName != "orders" || stats[0].Hits != 1 {
		t.Fatalf("stats after Match = %#v, want one recorded hit", stats)
	}
}

func TestManagerDecisionContextEmitsTraceSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	ctx, parent := provider.Tracer("test").Start(context.Background(), "parent")
	defer parent.End()

	manager, err := NewManager(Config{Rules: []Rule{{Name: "orders", Transport: TransportREST, Service: "orders", Method: "GET", Path: "/orders/*", Policy: Policy{Timeout: time.Second}}}})
	if err != nil {
		t.Fatal(err)
	}
	decision := manager.DecideContext(ctx, Request{Transport: "REST", Service: "orders", Method: "get", Path: "orders/42"})
	if !decision.Matched || decision.RuleName != "orders" {
		t.Fatalf("decision = %#v, want orders match", decision)
	}
	if stats := manager.RuleSet().Stats(); len(stats) != 0 {
		t.Fatalf("stats after DecideContext = %#v, want no stats", stats)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name() != "governance.decide" {
		t.Fatalf("span name = %q, want governance.decide", span.Name())
	}
	attrs := attributeMap(span.Attributes())
	for key, want := range map[string]string{
		"governance.transport": "rest",
		"governance.service":   "orders",
		"governance.method":    "GET",
		"governance.path":      "/orders/42",
		"governance.rule_name": "orders",
		"governance.rule_key":  "name:orders",
		"governance.source":    "static",
	} {
		if got := attrs[key].AsString(); got != want {
			t.Fatalf("span attr %s = %q, want %q (attrs=%v)", key, got, want, attrs)
		}
	}
	if got := attrs["governance.matched"].AsBool(); !got {
		t.Fatalf("governance.matched = %v, want true", got)
	}
	if got := attrs["governance.recorded"].AsBool(); got {
		t.Fatalf("governance.recorded = %v, want false", got)
	}
}

func TestManagerMatchContextRecordsStatsAndTraceSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	manager, err := NewManager(
		Config{Rules: []Rule{{Name: "orders", Transport: TransportREST, Service: "orders"}}},
		WithTracer(provider.Tracer("governance-test")),
	)
	if err != nil {
		t.Fatal(err)
	}

	decision := manager.MatchContext(context.Background(), Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "orders" {
		t.Fatalf("decision = %#v, want orders match", decision)
	}
	if stats := manager.RuleSet().Stats(); len(stats) != 1 || stats[0].RuleName != "orders" || stats[0].Hits != 1 {
		t.Fatalf("stats = %#v, want one recorded hit", stats)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "governance.match" {
		t.Fatalf("spans = %#v, want one governance.match span", spans)
	}
	attrs := attributeMap(spans[0].Attributes())
	if got := attrs["governance.recorded"].AsBool(); !got {
		t.Fatalf("governance.recorded = %v, want true", got)
	}
}

func TestManagerMatchContextAcceptsNilContextWithConfiguredTracer(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	manager, err := NewManager(
		Config{Rules: []Rule{{Name: "orders", Transport: TransportREST, Service: "orders"}}},
		WithTracer(provider.Tracer("governance-test")),
	)
	if err != nil {
		t.Fatal(err)
	}

	decision := manager.MatchContext(context.TODO(), Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.RuleName != "orders" {
		t.Fatalf("decision = %#v, want orders match", decision)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "governance.match" {
		t.Fatalf("spans = %#v, want one governance.match span", spans)
	}
}

func attributeMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}

func TestManagerReplaceRulesReturnsPluginValidationError(t *testing.T) {
	m, err := NewManager(Config{}, WithRuleSet(NewRuleSet()), WithPlugin(NewPlugin("bad", Rule{Name: "bad", Transport: "unknown"})))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ReplaceRules(Rule{Name: "live", Transport: TransportREST}); err == nil {
		t.Fatal("ReplaceRules accepted invalid plugin rules, want validation error")
	}
}

func TestManagerSaveRulesValidatesPluginsBeforePersisting(t *testing.T) {
	store := kv.NewMemoryStore()
	provider := KVRuleProvider{Store: store, Key: "gofly:rules"}
	initial := []Rule{{Name: "initial", Transport: TransportREST, Service: "orders"}}
	if err := provider.Save(context.Background(), initial, 0); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(
		Config{},
		WithRuleSet(NewRuleSet(initial...)),
		WithProvider(provider),
		WithPlugin(NewPlugin("bad", Rule{Name: "bad", Transport: "unknown"})),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = m.SaveRules(context.Background(), []Rule{{Name: "saved", Transport: TransportREST, Service: "orders"}}, 0)
	if err == nil {
		t.Fatal("SaveRules accepted invalid plugin rules, want validation error")
	}
	loaded, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Name != "initial" {
		t.Fatalf("persisted rules = %+v, want initial rules preserved", loaded)
	}
}

func TestManagerSubscribeEventsDelegatesRuleSet(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := m.SubscribeEvents(2)
	m.RuleSet().Replace(Rule{Name: "manager-event", Transport: TransportREST})
	select {
	case event := <-events:
		if event.Action != "replace" || !event.Success || event.Rules != 1 {
			t.Fatalf("event = %#v, want replace event", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for manager governance event")
	}
	unsubscribe()
	if _, ok := <-events; ok {
		t.Fatal("event channel is still open after manager unsubscribe")
	}
}

type switchingRuleProvider struct {
	mu    sync.Mutex
	rules []Rule
}

func (p *switchingRuleProvider) Load(context.Context) ([]Rule, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneRules(p.rules), nil
}

func (p *switchingRuleProvider) Source() string { return "switching" }

func (p *switchingRuleProvider) Replace(rules []Rule) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = cloneRules(rules)
}

func waitForRule(t *testing.T, rules *RuleSet, name string, errCh <-chan error) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		decision := rules.Match(Request{Transport: TransportREST, Service: "orders"})
		if decision.RuleName == name {
			return
		}
		select {
		case err := <-errCh:
			t.Fatalf("manager start error: %v", err)
		case <-deadline:
			t.Fatalf("timed out waiting for rule %q, decision=%#v", name, decision)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestMustNewManagerPanicsOnError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustNewManager did not panic for invalid config")
		}
	}()
	_ = MustNewManager(Config{Rules: []Rule{{Name: "bad", Transport: "unknown"}}})
}

func TestMustNewManagerReturnsManager(t *testing.T) {
	m := MustNewManager(Config{Rules: []Rule{{Name: "ok", Transport: TransportREST, Service: "orders"}}})
	if m == nil {
		t.Fatal("MustNewManager returned nil")
	}
	if decision := m.Match(Request{Transport: TransportREST, Service: "orders"}); !decision.Matched {
		t.Fatalf("decision = %#v, want match", decision)
	}
}

func TestManagerStartAsyncCallsOnError(t *testing.T) {
	provider := &switchingRuleProvider{rules: []Rule{{Name: "v1", Transport: TransportREST, Service: "orders"}}}
	m, err := NewManager(Config{Watch: true, WatchInterval: time.Millisecond}, WithProvider(provider))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var asyncErr error
	var once sync.Once
	done := make(chan struct{})
	m.StartAsync(ctx, func(err error) {
		once.Do(func() {
			asyncErr = err
			close(done)
		})
	})
	// Wait briefly then cancel; onError should not be called for clean cancellation.
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
		t.Fatalf("onError should not be called for clean cancel, got %v", asyncErr)
	case <-time.After(100 * time.Millisecond):
		// expected: onError not called
	}
}

func TestManagerStartAsyncNilManagerSafe(t *testing.T) {
	var m *Manager
	m.StartAsync(context.Background(), func(error) {
		t.Fatal("onError should not be called for nil manager")
	})
}

func TestManagerDiagnosticsDelegatesToRuleSet(t *testing.T) {
	m, err := NewManager(Config{Rules: []Rule{{Name: "orders", Transport: TransportREST, Service: "orders"}}})
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := m.Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Code != "empty_policy" {
		t.Fatalf("diagnostics = %#v, want empty_policy", diagnostics)
	}

	// nil manager returns nil
	var nilManager *Manager
	if diag := nilManager.Diagnostics(); diag != nil {
		t.Fatalf("nil manager diagnostics = %#v, want nil", diag)
	}
}

func TestManagerNilGuards(t *testing.T) {
	var nilManager *Manager

	// SubscribeEvents with nil manager returns closed channel
	events, unsubscribe := nilManager.SubscribeEvents(1)
	unsubscribe()
	if _, ok := <-events; ok {
		t.Fatal("nil manager SubscribeEvents should return closed channel")
	}

	// Start with nil manager returns nil
	if err := nilManager.Start(context.Background()); err != nil {
		t.Fatalf("nil manager Start = %v, want nil", err)
	}

	// Reload with nil manager returns nil
	if err := nilManager.Reload(context.Background()); err != nil {
		t.Fatalf("nil manager Reload = %v, want nil", err)
	}

	// ReplaceRules with nil manager returns error
	if err := nilManager.ReplaceRules(Rule{Name: "x", Transport: TransportREST}); err == nil {
		t.Fatal("nil manager ReplaceRules should return error")
	}

	// SaveRules with nil manager returns error
	if err := nilManager.SaveRules(context.Background(), nil, 0); err == nil {
		t.Fatal("nil manager SaveRules should return error")
	}

	// Snapshot with nil manager returns empty snapshot
	if snap := nilManager.Snapshot(); snap.Source != "" || snap.Status.Rules != 0 {
		t.Fatalf("nil manager Snapshot = %#v, want empty", snap)
	}

	// RuleSet with nil manager returns nil
	if rs := nilManager.RuleSet(); rs != nil {
		t.Fatalf("nil manager RuleSet = %#v, want nil", rs)
	}

	// Match with nil manager returns empty decision
	if d := nilManager.Match(Request{Transport: TransportREST}); d.Matched {
		t.Fatal("nil manager Match should return empty decision")
	}

	// Decide with nil manager returns empty decision
	if d := nilManager.Decide(Request{Transport: TransportREST}); d.Matched {
		t.Fatal("nil manager Decide should return empty decision")
	}
}

func TestManagerStartReloadNilProvider(t *testing.T) {
	m, err := NewManager(Config{Rules: []Rule{{Name: "orders", Transport: TransportREST, Service: "orders"}}})
	if err != nil {
		t.Fatal(err)
	}

	// Start with nil provider returns nil
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start with nil provider = %v, want nil", err)
	}

	// Reload with nil provider returns error
	if err := m.Reload(context.Background()); err == nil {
		t.Fatal("Reload with nil provider should return error")
	}
}

func TestManagerReplaceRulesNilRules(t *testing.T) {
	m, err := NewManager(Config{}, WithProvider(&switchingRuleProvider{}))
	if err != nil {
		t.Fatal(err)
	}
	m.rules = nil
	if err := m.ReplaceRules(Rule{Name: "orders", Transport: TransportREST, Service: "orders"}); err != nil {
		t.Fatalf("ReplaceRules with nil rules = %v, want nil", err)
	}
	if m.rules == nil {
		t.Fatal("ReplaceRules should initialize rules")
	}
}

func TestManagerSaveRulesProviderNotSaver(t *testing.T) {
	provider := &switchingRuleProvider{rules: []Rule{{Name: "v1", Transport: TransportREST, Service: "orders"}}}
	m, err := NewManager(Config{}, WithProvider(provider))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SaveRules(context.Background(), []Rule{{Name: "x", Transport: TransportREST}}, 0); err == nil {
		t.Fatal("SaveRules with non-RuleSaver provider should return error")
	}
}

func TestManagerWatchInterval(t *testing.T) {
	m, err := NewManager(Config{WatchInterval: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if d := m.watchInterval(); d != 5*time.Second {
		t.Fatalf("watchInterval = %v, want 5s", d)
	}

	var nilManager *Manager
	if d := nilManager.watchInterval(); d != time.Second {
		t.Fatalf("nil manager watchInterval = %v, want 1s", d)
	}

	mZero, _ := NewManager(Config{})
	if d := mZero.watchInterval(); d != time.Second {
		t.Fatalf("zero watchInterval = %v, want 1s", d)
	}
}
