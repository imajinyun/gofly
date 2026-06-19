// Package governance provides request routing rules, rate limiting, circuit
// breaking, concurrency limiting and canary release policies for gofly services.
package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	core "github.com/gofly/gofly/core"
	"github.com/gofly/gofly/core/kv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const governanceTracerName = "gofly/governance"

// Config controls how the governance manager loads and watches rules.
type Config struct {
	Rules         []Rule        `json:"rules,omitempty"`
	RuleFile      string        `json:"ruleFile,omitempty"`
	RuleKey       string        `json:"ruleKey,omitempty"`
	Watch         bool          `json:"watch,omitempty"`
	WatchInterval time.Duration `json:"watchInterval,omitempty"`
}

// ManagerOption customises Manager behaviour.
type ManagerOption func(*Manager)

// Manager loads, watches and applies governance rules to incoming requests.
type Manager struct {
	conf     Config
	rules    *RuleSet
	provider RuleProvider
	plugins  []Plugin
	source   string
	tracer   oteltrace.Tracer
}

// ManagerSnapshot captures the current state of a Manager.
type ManagerSnapshot struct {
	Config      Config           `json:"config"`
	Source      string           `json:"source,omitempty"`
	Status      RuleSetStatus    `json:"status"`
	Rules       []Rule           `json:"rules,omitempty"`
	Stats       []RuleStats      `json:"stats,omitempty"`
	Diagnostics []RuleDiagnostic `json:"diagnostics,omitempty"`
	History     []RuleSetEvent   `json:"history,omitempty"`
	Watch       bool             `json:"watch"`
	Interval    time.Duration    `json:"interval,omitempty"`
}

func WithProvider(provider RuleProvider) ManagerOption {
	return func(m *Manager) {
		m.provider = provider
		m.source = providerSource(provider)
	}
}

func WithRuleSet(rules *RuleSet) ManagerOption {
	return func(m *Manager) {
		if rules != nil {
			m.rules = rules
		}
	}
}

func WithPlugin(plugin Plugin) ManagerOption {
	return WithPlugins(plugin)
}

func WithPlugins(plugins ...Plugin) ManagerOption {
	return func(m *Manager) {
		m.plugins = append(m.plugins, plugins...)
	}
}

func WithSuite(suite *Suite) ManagerOption {
	return func(m *Manager) {
		if suite == nil {
			return
		}
		m.plugins = append(m.plugins, suite.plugins...)
	}
}

func WithRuleStore(store kv.Store, key string) ManagerOption {
	return func(m *Manager) {
		if key == "" {
			key = m.conf.RuleKey
		}
		m.provider = KVRuleProvider{Store: store, Key: key}
		m.source = providerSource(m.provider)
	}
}

func WithTracer(tracer oteltrace.Tracer) ManagerOption {
	return func(m *Manager) {
		m.tracer = tracer
	}
}

func NewManager(conf Config, opts ...ManagerOption) (*Manager, error) {
	if conf.WatchInterval < 0 {
		return nil, errors.New("governance watch interval must be non-negative")
	}
	m := &Manager{conf: conf, source: "static"}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if m.rules == nil {
		pluginRules, err := rulesFromPlugins(m.plugins...)
		if err != nil {
			return nil, err
		}
		merged := MergeRules(pluginRules, conf.Rules)
		if err := ValidateRules(merged...); err != nil {
			return nil, fmt.Errorf("validate governance rules: %w", err)
		}
		m.rules = NewRuleSet(merged...)
	} else if err := ValidateRules(m.rules.Snapshot()...); err != nil {
		return nil, fmt.Errorf("validate governance rules: %w", err)
	}
	if m.provider == nil && conf.RuleFile != "" {
		m.provider = FileRuleProvider{Path: conf.RuleFile}
		m.source = providerSource(m.provider)
	}
	if m.provider != nil && len(m.plugins) > 0 {
		m.provider = pluginRuleProvider{Base: m.provider, Plugins: append([]Plugin(nil), m.plugins...)}
		m.source = providerSource(m.provider)
	}
	return m, nil
}

func MustNewManager(conf Config, opts ...ManagerOption) *Manager {
	m, err := NewManager(conf, opts...)
	if err != nil {
		panic(err)
	}
	return m
}

func (m *Manager) RuleSet() *RuleSet {
	if m == nil {
		return nil
	}
	return m.rules
}

func (m *Manager) Match(req Request) Decision {
	if m == nil || m.rules == nil {
		return Decision{}
	}
	return m.rules.Match(req)
}

func (m *Manager) MatchContext(ctx context.Context, req Request) Decision {
	decision := m.Match(req)
	m.traceDecision(ctx, "governance.match", req, decision, true)
	return decision
}

func (m *Manager) Decide(req Request) Decision {
	if m == nil || m.rules == nil {
		return Decision{}
	}
	return m.rules.Decide(req)
}

func (m *Manager) DecideContext(ctx context.Context, req Request) Decision {
	decision := m.Decide(req)
	m.traceDecision(ctx, "governance.decide", req, decision, false)
	return decision
}

func (m *Manager) traceDecision(ctx context.Context, name string, req Request, decision Decision, recorded bool) {
	if m == nil {
		return
	}
	ctx = core.Context(ctx)
	tracer := m.tracer
	if tracer == nil {
		if span := oteltrace.SpanFromContext(ctx); span.SpanContext().IsValid() {
			tracer = span.TracerProvider().Tracer(governanceTracerName)
		} else {
			tracer = otel.Tracer(governanceTracerName)
		}
	}
	req = normalizeRequestForMatch(req)
	_, span := tracer.Start(ctx, name, oteltrace.WithSpanKind(oteltrace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(
		attribute.String("governance.source", m.source),
		attribute.String("governance.transport", req.Transport),
		attribute.String("governance.service", req.Service),
		attribute.String("governance.method", req.Method),
		attribute.String("governance.path", req.Path),
		attribute.Bool("governance.matched", decision.Matched),
		attribute.Bool("governance.recorded", recorded),
	)
	if decision.RuleKey != "" {
		span.SetAttributes(attribute.String("governance.rule_key", decision.RuleKey))
	}
	if decision.RuleName != "" {
		span.SetAttributes(attribute.String("governance.rule_name", decision.RuleName))
	}
	if decision.Matched {
		span.SetStatus(codes.Ok, "matched")
	}
}

func (m *Manager) SubscribeEvents(buffer int) (<-chan RuleSetEvent, func()) {
	if m == nil || m.rules == nil {
		ch := make(chan RuleSetEvent)
		close(ch)
		return ch, func() {}
	}
	return m.rules.Subscribe(buffer)
}

func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.provider == nil {
		return nil
	}
	if m.conf.Watch {
		return m.rules.Watch(ctx, m.provider, m.watchInterval())
	}
	return m.rules.Load(ctx, m.provider)
}

func (m *Manager) Reload(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.provider == nil {
		return errors.New("governance rule provider is nil")
	}
	return m.rules.Load(ctx, m.provider)
}

func (m *Manager) ReplaceRules(rules ...Rule) error {
	if m == nil {
		return errors.New("governance manager is nil")
	}
	if m.rules == nil {
		m.rules = NewRuleSet()
	}
	pluginRules, err := m.pluginRules()
	if err != nil {
		return err
	}
	merged := MergeRules(pluginRules, rules)
	if err := m.rules.ReplaceValidated(merged...); err != nil {
		return err
	}
	m.conf.Rules = cloneRules(rules)
	m.source = "static"
	return nil
}

func (m *Manager) SaveRules(ctx context.Context, rules []Rule, ttl time.Duration) error {
	if m == nil {
		return errors.New("governance manager is nil")
	}
	saver, ok := m.provider.(RuleSaver)
	if !ok || saver == nil {
		return errors.New("governance rule provider does not support save")
	}
	if m.rules == nil {
		m.rules = NewRuleSet()
	}
	pluginRules, err := m.pluginRules()
	if err != nil {
		return err
	}
	merged := MergeRules(pluginRules, rules)
	if err := ValidateRules(merged...); err != nil {
		return err
	}
	if err := saver.Save(ctx, rules, ttl); err != nil {
		return err
	}
	if err := m.rules.ReplaceValidated(merged...); err != nil {
		return err
	}
	m.conf.Rules = nil
	m.source = providerSource(m.provider)
	return nil
}

func (m *Manager) StartAsync(ctx context.Context, onError func(error)) {
	if m == nil || m.provider == nil {
		return
	}
	go func() {
		if err := m.Start(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
	}()
}

func (m *Manager) Snapshot() ManagerSnapshot {
	if m == nil || m.rules == nil {
		return ManagerSnapshot{}
	}
	return ManagerSnapshot{
		Config:      cloneConfig(m.conf),
		Source:      m.source,
		Status:      m.rules.Status(),
		Rules:       m.rules.Snapshot(),
		Stats:       m.rules.Stats(),
		Diagnostics: m.rules.Diagnostics(),
		History:     m.rules.History(),
		Watch:       m.conf.Watch,
		Interval:    m.watchInterval(),
	}
}

func (m *Manager) watchInterval() time.Duration {
	if m == nil || m.conf.WatchInterval <= 0 {
		return time.Second
	}
	return m.conf.WatchInterval
}

func (m *Manager) pluginRules() ([]Rule, error) {
	if m == nil || len(m.plugins) == 0 {
		return nil, nil
	}
	rules, err := rulesFromPlugins(m.plugins...)
	if err != nil {
		return nil, err
	}
	return rules, nil
}

func cloneConfig(conf Config) Config {
	conf.Rules = cloneRules(conf.Rules)
	return conf
}
