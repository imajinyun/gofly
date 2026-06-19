// Package governance provides request routing rules, rate limiting, circuit
// breaking, concurrency limiting and canary release policies for gofly services.
package governance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Plugin packages a reusable governance policy set. Plugins are intentionally
// transport-agnostic so the same suite can be shared by REST, RPC, Gateway and
// MQ wiring through a single Manager.
type Plugin interface {
	Name() string
	Rules() []Rule
}

// PluginFunc is a convenience Plugin implementation from a name and rule slice.
type PluginFunc struct {
	PluginName string
	RuleList   []Rule
}

// NewPlugin creates a PluginFunc from a name and rules.
func NewPlugin(name string, rules ...Rule) PluginFunc {
	return PluginFunc{PluginName: name, RuleList: cloneRules(rules)}
}

// Name returns the plugin name.
func (p PluginFunc) Name() string {
	return strings.TrimSpace(p.PluginName)
}

// Rules returns the plugin's rules.
func (p PluginFunc) Rules() []Rule {
	return cloneRules(p.RuleList)
}

// Suite combines multiple plugins into a single rule set.
type Suite struct {
	plugins []Plugin
	rules   []Rule
}

func NewSuite(plugins ...Plugin) (*Suite, error) {
	s := &Suite{}
	if err := s.Add(plugins...); err != nil {
		return nil, err
	}
	return s, nil
}

func MustNewSuite(plugins ...Plugin) *Suite {
	suite, err := NewSuite(plugins...)
	if err != nil {
		panic(err)
	}
	return suite
}

func (s *Suite) Add(plugins ...Plugin) error {
	if s == nil {
		return errors.New("governance suite is nil")
	}
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}
		name := plugin.Name()
		if name == "" {
			return errors.New("governance plugin name is required")
		}
		for _, existing := range s.plugins {
			if existing.Name() == name {
				return fmt.Errorf("governance plugin %s is duplicated", name)
			}
		}
		rules := plugin.Rules()
		if err := ValidateRules(rules...); err != nil {
			return fmt.Errorf("validate governance plugin %s: %w", name, err)
		}
		s.plugins = append(s.plugins, plugin)
		s.rules = MergeRules(s.rules, rules)
	}
	return nil
}

func (s *Suite) Rules() []Rule {
	if s == nil {
		return nil
	}
	return cloneRules(s.rules)
}

func (s *Suite) RuleSet() *RuleSet {
	return NewRuleSet(s.Rules()...)
}

func (s *Suite) Manager(conf Config, opts ...ManagerOption) (*Manager, error) {
	if s == nil {
		return NewManager(conf, opts...)
	}
	options := append([]ManagerOption{WithSuite(s)}, opts...)
	return NewManager(conf, options...)
}

func MergeRules(base []Rule, overlays ...[]Rule) []Rule {
	out := cloneRules(base)
	index := make(map[string]int, len(out))
	for i, rule := range out {
		if rule.Name != "" {
			index[rule.Name] = i
		}
	}
	for _, rules := range overlays {
		for _, rule := range rules {
			rule = cloneRules([]Rule{rule})[0]
			if rule.Name != "" {
				if i, ok := index[rule.Name]; ok {
					out[i] = rule
					continue
				}
				index[rule.Name] = len(out)
			}
			out = append(out, rule)
		}
	}
	return out
}

func MergeRuleSets(base *RuleSet, overlays ...*RuleSet) *RuleSet {
	var rules []Rule
	if base != nil {
		rules = base.Snapshot()
	}
	for _, overlay := range overlays {
		if overlay != nil {
			rules = MergeRules(rules, overlay.Snapshot())
		}
	}
	return NewRuleSet(rules...)
}

func rulesFromPlugins(plugins ...Plugin) ([]Rule, error) {
	suite, err := NewSuite(plugins...)
	if err != nil {
		return nil, err
	}
	return suite.Rules(), nil
}

type pluginRuleProvider struct {
	Base    RuleProvider
	Plugins []Plugin
}

func (p pluginRuleProvider) Load(ctx context.Context) ([]Rule, error) {
	if p.Base == nil {
		return rulesFromPlugins(p.Plugins...)
	}
	base, err := p.Base.Load(ctx)
	if err != nil {
		return nil, err
	}
	pluginRules, err := rulesFromPlugins(p.Plugins...)
	if err != nil {
		return nil, err
	}
	return MergeRules(pluginRules, base), nil
}

func (p pluginRuleProvider) Save(ctx context.Context, rules []Rule, ttl time.Duration) error {
	saver, ok := p.Base.(RuleSaver)
	if !ok || saver == nil {
		return errors.New("governance rule provider does not support save")
	}
	return saver.Save(ctx, rules, ttl)
}

func (p pluginRuleProvider) Source() string {
	if p.Base == nil {
		return "plugins"
	}
	base := providerSource(p.Base)
	if strings.HasSuffix(base, "+plugins") {
		return base
	}
	return base + "+plugins"
}

type ProductionDefaultsConfig struct {
	Service          string
	RESTTimeout      time.Duration
	RPCTimeout       time.Duration
	GatewayTimeout   time.Duration
	MQTimeout        time.Duration
	RetryAttempts    int
	RetryBackoff     time.Duration
	ConcurrencyLimit int
	RateLimit        int
	RateBurst        int
	MaxBodyBytes     int64
	Breaker          BreakerPolicy
}

func ProductionDefaults(service string) Plugin {
	return ProductionDefaultsWithConfig(ProductionDefaultsConfig{Service: service})
}

func DefaultProductionDefaultsConfig(service string) ProductionDefaultsConfig {
	return NormalizeProductionDefaultsConfig(ProductionDefaultsConfig{Service: service})
}

func NormalizeProductionDefaultsConfig(conf ProductionDefaultsConfig) ProductionDefaultsConfig {
	service := strings.TrimSpace(conf.Service)
	conf.Service = service
	if conf.RESTTimeout <= 0 {
		conf.RESTTimeout = 3 * time.Second
	}
	if conf.RPCTimeout <= 0 {
		conf.RPCTimeout = 3 * time.Second
	}
	if conf.GatewayTimeout <= 0 {
		conf.GatewayTimeout = 5 * time.Second
	}
	if conf.MQTimeout <= 0 {
		conf.MQTimeout = 3 * time.Second
	}
	if conf.RetryAttempts <= 0 {
		conf.RetryAttempts = 2
	}
	if conf.RetryBackoff <= 0 {
		conf.RetryBackoff = 100 * time.Millisecond
	}
	if conf.ConcurrencyLimit <= 0 {
		conf.ConcurrencyLimit = 1000
	}
	if conf.RateLimit <= 0 {
		conf.RateLimit = 2000
	}
	if conf.RateBurst <= 0 {
		conf.RateBurst = conf.RateLimit
	}
	if conf.MaxBodyBytes <= 0 {
		conf.MaxBodyBytes = 10 << 20
	}
	if !conf.Breaker.Enabled {
		conf.Breaker = BreakerPolicy{Enabled: true, OpenTimeout: 5 * time.Second, Window: 30 * time.Second, Buckets: 10, MinRequests: 20, FailureRatio: 0.5}
	}
	return conf
}

func ProductionDefaultsWithConfig(conf ProductionDefaultsConfig) Plugin {
	conf = NormalizeProductionDefaultsConfig(conf)
	return NewPlugin("production-defaults", productionDefaultRules(conf)...)
}

func productionDefaultRules(conf ProductionDefaultsConfig) []Rule {
	return []Rule{
		productionDefaultRule("production-rest", TransportREST, conf.Service, conf.RESTTimeout, conf, true),
		productionDefaultRule("production-rpc", TransportRPC, conf.Service, conf.RPCTimeout, conf, false),
		productionDefaultRule("production-gateway", TransportGateway, conf.Service, conf.GatewayTimeout, conf, true),
		productionDefaultRule("production-mq", TransportMQ, conf.Service, conf.MQTimeout, conf, false),
	}
}

func productionDefaultRule(name, transport, service string, timeout time.Duration, conf ProductionDefaultsConfig, bodyLimit bool) Rule {
	policy := Policy{
		Timeout:     timeout,
		Retry:       RetryPolicy{Attempts: conf.RetryAttempts, Backoff: conf.RetryBackoff},
		Breaker:     conf.Breaker,
		RateLimit:   RateLimitPolicy{Rate: conf.RateLimit, Burst: conf.RateBurst},
		Concurrency: ConcurrencyPolicy{Limit: conf.ConcurrencyLimit},
	}
	if bodyLimit {
		policy.MaxBodyBytes = conf.MaxBodyBytes
	}
	return Rule{Name: name, Priority: -1000, Transport: transport, Service: service, Policy: policy}
}
