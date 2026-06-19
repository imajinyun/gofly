package governance

import (
	"testing"
	"time"
)

func TestGovernanceSuiteMergesPluginsAndOverridesByRuleName(t *testing.T) {
	base := NewPlugin("base", Rule{Name: "shared", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}})
	override := NewPlugin("override", Rule{Name: "shared", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: 2 * time.Second}})
	suite, err := NewSuite(base, override)
	if err != nil {
		t.Fatal(err)
	}
	rules := suite.Rules()
	if len(rules) != 1 || rules[0].Policy.Timeout != 2*time.Second {
		t.Fatalf("suite rules = %+v, want override timeout", rules)
	}
	rules[0].Policy.Timeout = 3 * time.Second
	if got := suite.Rules()[0].Policy.Timeout; got != 2*time.Second {
		t.Fatalf("suite rules should be cloned, got timeout %s", got)
	}
}

func TestGovernanceSuiteRejectsDuplicatePluginAndInvalidRules(t *testing.T) {
	if _, err := NewSuite(NewPlugin("dup"), NewPlugin("dup")); err == nil {
		t.Fatal("NewSuite duplicate plugin succeeded, want error")
	}
	if _, err := NewSuite(NewPlugin("bad", Rule{Name: "bad", Transport: "unknown"})); err == nil {
		t.Fatal("NewSuite invalid rule succeeded, want error")
	}
}

func TestGovernanceManagerWithSuiteAndExplicitRuleOverride(t *testing.T) {
	suite := MustNewSuite(NewPlugin("defaults", Rule{
		Name:      "orders-default",
		Transport: TransportREST,
		Service:   "orders",
		Policy:    Policy{Timeout: time.Second, Headers: map[string]string{"X-Source": "plugin"}},
	}))
	manager, err := NewManager(Config{Rules: []Rule{{
		Name:      "orders-default",
		Transport: TransportREST,
		Service:   "orders",
		Policy:    Policy{Timeout: 2 * time.Second, Headers: map[string]string{"X-Source": "config"}},
	}}}, WithSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	decision := manager.RuleSet().Match(Request{Transport: TransportREST, Service: "orders"})
	if !decision.Matched || decision.Policy.Timeout != 2*time.Second || decision.Policy.Headers["X-Source"] != "config" {
		t.Fatalf("decision = %#v, want explicit config override", decision)
	}
}

func TestProductionDefaultsPlugin(t *testing.T) {
	suite := MustNewSuite(ProductionDefaultsWithConfig(ProductionDefaultsConfig{Service: "orders", RateLimit: 10, RateBurst: 20, ConcurrencyLimit: 30}))
	rules := suite.Rules()
	if len(rules) != 4 {
		t.Fatalf("production default rules = %d, want 4", len(rules))
	}
	decision := suite.RuleSet().Match(Request{Transport: TransportRPC, Service: "orders"})
	if !decision.Matched || decision.Policy.RateLimit.Rate != 10 || decision.Policy.Concurrency.Limit != 30 || !decision.Policy.Breaker.Enabled {
		t.Fatalf("rpc production decision = %#v", decision)
	}
}

func TestNormalizeProductionDefaultsConfig(t *testing.T) {
	conf := NormalizeProductionDefaultsConfig(ProductionDefaultsConfig{Service: " orders "})
	if conf.Service != "orders" || conf.RESTTimeout != 3*time.Second || conf.RPCTimeout != 3*time.Second || conf.MQTimeout != 3*time.Second {
		t.Fatalf("normalized timeouts = %+v", conf)
	}
	if conf.GatewayTimeout != 5*time.Second || conf.RetryAttempts != 2 || conf.RetryBackoff != 100*time.Millisecond {
		t.Fatalf("normalized retry/gateway = %+v", conf)
	}
	if conf.ConcurrencyLimit != 1000 || conf.RateLimit != 2000 || conf.RateBurst != 2000 || conf.MaxBodyBytes != 10<<20 || !conf.Breaker.Enabled {
		t.Fatalf("normalized policy defaults = %+v", conf)
	}
}

func TestMergeRuleSets(t *testing.T) {
	base := NewRuleSet(Rule{Name: "shared", Transport: TransportREST, Policy: Policy{Timeout: time.Second}})
	overlay := NewRuleSet(Rule{Name: "shared", Transport: TransportREST, Policy: Policy{Timeout: 2 * time.Second}})
	merged := MergeRuleSets(base, overlay)
	decision := merged.Match(Request{Transport: TransportREST})
	if !decision.Matched || decision.Policy.Timeout != 2*time.Second {
		t.Fatalf("merged decision = %#v, want overlay timeout", decision)
	}
}

func TestPluginRuleProviderSourceIsStable(t *testing.T) {
	base := StaticRuleProvider{}
	provider := pluginRuleProvider{Base: pluginRuleProvider{Base: base}}
	if got := provider.Source(); got != "static+plugins" {
		t.Fatalf("source = %q, want stable plugin suffix", got)
	}
	if got := (pluginRuleProvider{}).Source(); got != "plugins" {
		t.Fatalf("nil base source = %q, want plugins", got)
	}
}

func TestSuiteManagerCreatesManagerWithPlugins(t *testing.T) {
	suite := MustNewSuite(NewPlugin("defaults", Rule{Name: "orders", Transport: TransportREST, Service: "orders", Policy: Policy{Timeout: time.Second}}))
	m, err := suite.Manager(Config{})
	if err != nil {
		t.Fatalf("Manager: %v", err)
	}
	if decision := m.Match(Request{Transport: TransportREST, Service: "orders"}); !decision.Matched || decision.Policy.Timeout != time.Second {
		t.Fatalf("decision = %#v, want suite plugin rule", decision)
	}

	// nil suite Manager delegates to NewManager
	var nilSuite *Suite
	m2, err := nilSuite.Manager(Config{Rules: []Rule{{Name: "direct", Transport: TransportREST}}})
	if err != nil {
		t.Fatalf("nil suite Manager: %v", err)
	}
	if decision := m2.Match(Request{Transport: TransportREST}); !decision.Matched || decision.RuleName != "direct" {
		t.Fatalf("nil suite decision = %#v, want direct rule", decision)
	}
}

func TestProductionDefaultsShortcut(t *testing.T) {
	plugin := ProductionDefaults("orders")
	if plugin.Name() != "production-defaults" {
		t.Fatalf("plugin name = %q, want production-defaults", plugin.Name())
	}
	rules := plugin.Rules()
	if len(rules) != 4 {
		t.Fatalf("rules = %d, want 4", len(rules))
	}
}

func TestDefaultProductionDefaultsConfig(t *testing.T) {
	conf := DefaultProductionDefaultsConfig("orders")
	if conf.Service != "orders" || conf.RESTTimeout != 3*time.Second || conf.ConcurrencyLimit != 1000 {
		t.Fatalf("conf = %+v, want normalized defaults", conf)
	}
}
