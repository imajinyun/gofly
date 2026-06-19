// Package governance provides request routing rules, rate limiting, circuit
// breaking, canary routing, and concurrency control for gofly services.
package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	core "github.com/gofly/gofly/core"
	"github.com/gofly/gofly/core/kv"
)

const (
	TransportREST       = "rest"
	TransportRPC        = "rpc"
	TransportGateway    = "gateway"
	TransportMQ         = "mq"
	HeaderCanary        = "X-Gofly-Canary"
	HeaderCanaryService = "X-Gofly-Canary-Service"

	defaultRuleSetEventLimit   = 32
	defaultRuleSetVersionLimit = 16
)

// RuleSet is a thread-safe collection of governance rules with versioning and events.
type RuleSet struct {
	mu             sync.RWMutex
	rules          []Rule
	stats          map[string]*ruleStats
	version        int64
	updatedAt      time.Time
	lastError      string
	events         []RuleSetEvent
	versions       []RuleSetVersion
	subscribers    map[uint64]chan RuleSetEvent
	nextSubscriber uint64
}

type ruleStats struct {
	Name        string
	Hits        int64
	LastMatched time.Time
	LastRequest Request
}

// Rule defines a governance rule with matching criteria and an applied policy.
type Rule struct {
	Name      string            `json:"name,omitempty"`
	Priority  int               `json:"priority,omitempty"`
	Transport string            `json:"transport,omitempty"`
	Service   string            `json:"service,omitempty"`
	Method    string            `json:"method,omitempty"`
	Path      string            `json:"path,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
	Policy    Policy            `json:"policy"`
}

// Policy is the set of actions applied when a rule matches.
type Policy struct {
	Timeout         time.Duration     `json:"timeout,omitempty"`
	MaxBodyBytes    int64             `json:"maxBodyBytes,omitempty"`
	Retry           RetryPolicy       `json:"retry,omitempty"`
	Breaker         BreakerPolicy     `json:"breaker,omitempty"`
	RateLimit       RateLimitPolicy   `json:"rateLimit,omitempty"`
	Concurrency     ConcurrencyPolicy `json:"concurrency,omitempty"`
	Canary          CanaryPolicy      `json:"canary,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	DisableFallback bool              `json:"disableFallback,omitempty"`
}

// RetryPolicy configures per-rule retry behavior.
type RetryPolicy struct {
	Attempts int           `json:"attempts,omitempty"`
	Backoff  time.Duration `json:"backoff,omitempty"`
	Statuses []int         `json:"statuses,omitempty"`
	Methods  []string      `json:"methods,omitempty"`
}

// BreakerPolicy configures per-rule circuit breaker settings.
type BreakerPolicy struct {
	Enabled      bool          `json:"enabled,omitempty"`
	OpenTimeout  time.Duration `json:"openTimeout,omitempty"`
	Window       time.Duration `json:"window,omitempty"`
	Buckets      int           `json:"buckets,omitempty"`
	MinRequests  int64         `json:"minRequests,omitempty"`
	FailureRatio float64       `json:"failureRatio,omitempty"`
}

// RateLimitPolicy configures per-rule token bucket rate limiting.
type RateLimitPolicy struct {
	Rate  int `json:"rate,omitempty"`
	Burst int `json:"burst,omitempty"`
}

// ConcurrencyPolicy configures per-rule concurrency limiting.
type ConcurrencyPolicy struct {
	Limit int `json:"limit,omitempty"`
}

// CanaryPolicy configures canary traffic splitting for a rule.
type CanaryPolicy struct {
	Ratio          float64           `json:"ratio,omitempty"`
	Service        string            `json:"service,omitempty"`
	Target         string            `json:"target,omitempty"`
	Targets        []string          `json:"targets,omitempty"`
	UpstreamPrefix string            `json:"upstreamPrefix,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	MatchHeaders   map[string]string `json:"matchHeaders,omitempty"`
	MatchCookies   map[string]string `json:"matchCookies,omitempty"`
}

// Request is the input matched against governance rules.
type Request struct {
	Transport string            `json:"transport,omitempty"`
	Service   string            `json:"service,omitempty"`
	Method    string            `json:"method,omitempty"`
	Path      string            `json:"path,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Cookies   map[string]string `json:"cookies,omitempty"`
}

type CanaryDecision struct {
	Selected       bool              `json:"selected"`
	Service        string            `json:"service,omitempty"`
	Target         string            `json:"target,omitempty"`
	Targets        []string          `json:"targets,omitempty"`
	UpstreamPrefix string            `json:"upstreamPrefix,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
}

type Decision struct {
	Matched  bool   `json:"matched"`
	RuleKey  string `json:"ruleKey,omitempty"`
	RuleName string `json:"ruleName,omitempty"`
	Policy   Policy `json:"policy,omitempty"`
}

type RuleStats struct {
	RuleKey     string    `json:"ruleKey"`
	RuleName    string    `json:"ruleName,omitempty"`
	Hits        int64     `json:"hits"`
	LastMatched time.Time `json:"lastMatched,omitempty"`
	LastRequest Request   `json:"lastRequest,omitempty"`
}

type RuleExplain struct {
	Request     Request          `json:"request"`
	Decision    Decision         `json:"decision"`
	Evaluations []RuleEvaluation `json:"evaluations"`
}

type RuleDiff struct {
	Added     []Rule       `json:"added,omitempty"`
	Removed   []Rule       `json:"removed,omitempty"`
	Changed   []RuleChange `json:"changed,omitempty"`
	Unchanged int          `json:"unchanged,omitempty"`
}

type RuleChange struct {
	Before Rule `json:"before"`
	After  Rule `json:"after"`
}

type RuleEvaluation struct {
	RuleKey     string `json:"ruleKey"`
	RuleName    string `json:"ruleName,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	Specificity int    `json:"specificity"`
	Matched     bool   `json:"matched"`
	Reason      string `json:"reason"`
}

type RuleSetStatus struct {
	Version     int64     `json:"version"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	LastError   string    `json:"lastError,omitempty"`
	Rules       int       `json:"rules"`
	Stats       int       `json:"stats"`
	Events      int       `json:"events"`
	Versions    int       `json:"versions"`
	Subscribers int       `json:"subscribers"`
}

type RuleSetEvent struct {
	Version int64     `json:"version"`
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	Source  string    `json:"source,omitempty"`
	Success bool      `json:"success"`
	Rules   int       `json:"rules,omitempty"`
	Error   string    `json:"error,omitempty"`
}

type RuleSetVersion struct {
	Version int64     `json:"version"`
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	Source  string    `json:"source,omitempty"`
	Rules   []Rule    `json:"rules,omitempty"`
}

type RuleProvider interface {
	Load(context.Context) ([]Rule, error)
}

type RuleSaver interface {
	Save(context.Context, []Rule, time.Duration) error
}

type RuleProviderSource interface {
	Source() string
}

type RuleProviderFunc func(context.Context) ([]Rule, error)

type StaticRuleProvider struct {
	Rules []Rule
	Name  string
}

type KVRuleProvider struct {
	Store kv.Store
	Key   string
}

type FileRuleProvider struct {
	Path string
}

func (f RuleProviderFunc) Load(ctx context.Context) ([]Rule, error) { return f(ctx) }

func (p StaticRuleProvider) Source() string {
	if p.Name != "" {
		return p.Name
	}
	return "static"
}

func (p StaticRuleProvider) Load(ctx context.Context) ([]Rule, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return cloneRules(p.Rules), nil
}

func (p KVRuleProvider) Load(ctx context.Context) ([]Rule, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.Store == nil {
		return nil, errors.New("governance rule store is nil")
	}
	if strings.TrimSpace(p.Key) == "" {
		return nil, errors.New("governance rule key is empty")
	}
	data, err := p.Store.Get(ctx, p.Key)
	if err != nil {
		return nil, fmt.Errorf("load governance rules from kv: %w", err)
	}
	var rules []Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("decode governance rules: %w", err)
	}
	return cloneRules(rules), nil
}

func (p KVRuleProvider) Source() string {
	if p.Key != "" {
		return "kv:" + p.Key
	}
	return "kv"
}

func (p KVRuleProvider) Save(ctx context.Context, rules []Rule, ttl time.Duration) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.Store == nil {
		return errors.New("governance rule store is nil")
	}
	if strings.TrimSpace(p.Key) == "" {
		return errors.New("governance rule key is empty")
	}
	if err := ValidateRules(rules...); err != nil {
		return err
	}
	data, err := json.Marshal(cloneRules(rules))
	if err != nil {
		return fmt.Errorf("encode governance rules: %w", err)
	}
	return p.Store.Set(ctx, p.Key, data, ttl)
}

func (p FileRuleProvider) Load(ctx context.Context) ([]Rule, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Path) == "" {
		return nil, errors.New("governance rule file path is empty")
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, fmt.Errorf("read governance rule file: %w", err)
	}
	rules, err := decodeRuleFile(data)
	if err != nil {
		return nil, err
	}
	return cloneRules(rules), nil
}

func (p FileRuleProvider) Source() string {
	if p.Path != "" {
		return "file:" + p.Path
	}
	return "file"
}

func (p FileRuleProvider) Save(ctx context.Context, rules []Rule, ttl time.Duration) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(p.Path) == "" {
		return errors.New("governance rule file path is empty")
	}
	if err := ValidateRules(rules...); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cloneRules(rules), "", "  ")
	if err != nil {
		return fmt.Errorf("encode governance rules: %w", err)
	}
	data = append(data, '\n')
	// #nosec G306 -- governance rule files are operator-managed configuration, intentionally user-readable.
	if err := os.WriteFile(p.Path, data, 0o644); err != nil {
		return fmt.Errorf("write governance rule file: %w", err)
	}
	return nil
}

func decodeRuleFile(data []byte) ([]Rule, error) {
	var rules []Rule
	if err := json.Unmarshal(data, &rules); err == nil {
		return rules, nil
	}
	var conf Config
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil, fmt.Errorf("decode governance rule file: %w", err)
	}
	return conf.Rules, nil
}

func NewRuleSet(rules ...Rule) *RuleSet {
	rs := &RuleSet{}
	rs.Replace(rules...)
	return rs
}

func (r *RuleSet) Replace(rules ...Rule) {
	if r == nil {
		return
	}
	r.replace(rules, "replace", "")
}

func (r *RuleSet) replace(rules []Rule, action, source string) {
	normalized := cloneRules(rules)
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Priority != normalized[j].Priority {
			return normalized[i].Priority > normalized[j].Priority
		}
		return ruleSpecificity(normalized[i]) > ruleSpecificity(normalized[j])
	})
	r.mu.Lock()
	r.rules = normalized
	r.stats = make(map[string]*ruleStats, len(normalized))
	r.version++
	r.updatedAt = time.Now()
	r.lastError = ""
	r.appendEventLocked(RuleSetEvent{Version: r.version, At: r.updatedAt, Action: action, Source: source, Success: true, Rules: len(normalized)})
	r.appendVersionLocked(RuleSetVersion{Version: r.version, At: r.updatedAt, Action: action, Source: source, Rules: normalized})
	r.mu.Unlock()
}

func (r *RuleSet) ReplaceValidated(rules ...Rule) error {
	if r == nil {
		return nil
	}
	if err := ValidateRules(rules...); err != nil {
		r.recordFailure("replace", "", err)
		return err
	}
	r.replace(rules, "replace", "")
	return nil
}

func (r *RuleSet) Subscribe(buffer int) (<-chan RuleSetEvent, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	ch := make(chan RuleSetEvent, buffer)
	if r == nil {
		close(ch)
		return ch, func() {}
	}
	r.mu.Lock()
	if r.subscribers == nil {
		r.subscribers = make(map[uint64]chan RuleSetEvent)
	}
	r.nextSubscriber++
	id := r.nextSubscriber
	r.subscribers[id] = ch
	r.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			r.mu.Lock()
			if current, ok := r.subscribers[id]; ok {
				delete(r.subscribers, id)
				close(current)
			}
			r.mu.Unlock()
		})
	}
	return ch, cancel
}

func (r *RuleSet) Rollback(version int64) error {
	if r == nil {
		return nil
	}
	if version <= 0 {
		err := errors.New("governance rule version must be positive")
		r.recordFailure("rollback", "", err)
		return err
	}
	r.mu.RLock()
	var target []Rule
	found := false
	for _, item := range r.versions {
		if item.Version == version {
			target = cloneRules(item.Rules)
			found = true
			break
		}
	}
	r.mu.RUnlock()
	if !found {
		err := fmt.Errorf("governance rule version %d not found", version)
		r.recordFailure("rollback", fmt.Sprintf("version:%d", version), err)
		return err
	}
	r.replace(target, "rollback", fmt.Sprintf("version:%d", version))
	return nil
}

func (r *RuleSet) Match(req Request) Decision {
	return r.match(req, true)
}

func (r *RuleSet) Decide(req Request) Decision {
	return r.match(req, false)
}

func HTTPRequest(transport, service string, r *http.Request, tags map[string]string) Request {
	req := Request{Transport: transport, Service: service, Tags: tags}
	if r == nil {
		return normalizeRequest(req)
	}
	req.Method = r.Method
	req.Path = r.URL.Path
	req.Headers = headerMap(r.Header)
	req.Cookies = cookiesMap(r.Cookies())
	return normalizeRequest(req)
}

func SelectCanary(policy CanaryPolicy, req Request) CanaryDecision {
	if !canaryMatches(policy, req) {
		return CanaryDecision{}
	}
	return CanaryDecision{
		Selected:       true,
		Service:        policy.Service,
		Target:         policy.Target,
		Targets:        append([]string(nil), policy.Targets...),
		UpstreamPrefix: policy.UpstreamPrefix,
		Headers:        cloneStringMap(policy.Headers),
	}
}

func (r *RuleSet) Explain(req Request) RuleExplain {
	if r == nil {
		return RuleExplain{Request: normalizeRequest(req)}
	}
	req = normalizeRequest(req)
	r.mu.RLock()
	rules := append([]Rule(nil), r.rules...)
	r.mu.RUnlock()
	out := RuleExplain{Request: req, Evaluations: make([]RuleEvaluation, 0, len(rules))}
	for _, rule := range rules {
		matched, reason := explainRule(rule, req)
		out.Evaluations = append(out.Evaluations, RuleEvaluation{
			RuleKey:     ruleKey(rule),
			RuleName:    rule.Name,
			Priority:    rule.Priority,
			Specificity: ruleSpecificity(rule),
			Matched:     matched,
			Reason:      reason,
		})
		if matched && !out.Decision.Matched {
			out.Decision = Decision{Matched: true, RuleKey: ruleKey(rule), RuleName: rule.Name, Policy: clonePolicy(rule.Policy)}
		}
	}
	return out
}

func DiffRules(before []Rule, after []Rule) RuleDiff {
	before = cloneRules(before)
	after = cloneRules(after)
	beforeByKey := make(map[string]Rule, len(before))
	seenAfter := make(map[string]struct{}, len(after))
	for _, rule := range before {
		beforeByKey[diffRuleKey(rule)] = rule
	}
	diff := RuleDiff{}
	for _, rule := range after {
		key := diffRuleKey(rule)
		seenAfter[key] = struct{}{}
		previous, ok := beforeByKey[key]
		if !ok {
			diff.Added = append(diff.Added, rule)
			continue
		}
		if reflect.DeepEqual(normalizeRule(previous), normalizeRule(rule)) {
			diff.Unchanged++
			continue
		}
		diff.Changed = append(diff.Changed, RuleChange{Before: previous, After: rule})
	}
	for _, rule := range before {
		if _, ok := seenAfter[diffRuleKey(rule)]; !ok {
			diff.Removed = append(diff.Removed, rule)
		}
	}
	return diff
}

func (r *RuleSet) match(req Request, record bool) Decision {
	if r == nil {
		return Decision{}
	}
	req = normalizeRequestForMatch(req)
	r.mu.RLock()
	for _, rule := range r.rules {
		if ruleMatches(rule, req) {
			decision := Decision{Matched: true, RuleKey: ruleKey(rule), RuleName: rule.Name, Policy: clonePolicy(rule.Policy)}
			r.mu.RUnlock()
			if record {
				r.recordMatch(rule, req)
			}
			return decision
		}
	}
	r.mu.RUnlock()
	return Decision{}
}

func (r *RuleSet) Load(ctx context.Context, provider RuleProvider) error {
	if r == nil {
		return nil
	}
	ctx = core.Context(ctx)
	if provider == nil {
		err := errors.New("governance rule provider is nil")
		r.recordFailure("load", "", err)
		return err
	}
	source := providerSource(provider)
	rules, err := provider.Load(ctx)
	if err != nil {
		r.recordFailure("load", source, err)
		return err
	}
	if err := ValidateRules(rules...); err != nil {
		r.recordFailure("load", source, err)
		return err
	}
	r.replace(rules, "load", source)
	return nil
}

func (r *RuleSet) Watch(ctx context.Context, provider RuleProvider, interval time.Duration) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = time.Second
	}
	if err := r.Load(ctx, provider); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = r.Load(ctx, provider)
		}
	}
}

func (r *RuleSet) Snapshot() []Rule {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneRules(r.rules)
}

func (r *RuleSet) Stats() []RuleStats {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RuleStats, 0, len(r.stats))
	for key, stats := range r.stats {
		if stats == nil {
			continue
		}
		out = append(out, RuleStats{
			RuleKey:     key,
			RuleName:    stats.Name,
			Hits:        stats.Hits,
			LastMatched: stats.LastMatched,
			LastRequest: normalizeRequest(stats.LastRequest),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hits != out[j].Hits {
			return out[i].Hits > out[j].Hits
		}
		return out[i].RuleKey < out[j].RuleKey
	})
	return out
}

func (r *RuleSet) Status() RuleSetStatus {
	if r == nil {
		return RuleSetStatus{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return RuleSetStatus{
		Version:     r.version,
		UpdatedAt:   r.updatedAt,
		LastError:   r.lastError,
		Rules:       len(r.rules),
		Stats:       len(r.stats),
		Events:      len(r.events),
		Versions:    len(r.versions),
		Subscribers: len(r.subscribers),
	}
}

func (r *RuleSet) History() []RuleSetEvent {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := append([]RuleSetEvent(nil), r.events...)
	return out
}

func (r *RuleSet) Versions() []RuleSetVersion {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RuleSetVersion, len(r.versions))
	for i, item := range r.versions {
		out[i] = RuleSetVersion{
			Version: item.Version,
			At:      item.At,
			Action:  item.Action,
			Source:  item.Source,
			Rules:   cloneRules(item.Rules),
		}
	}
	return out
}

func (r *RuleSet) recordMatch(rule Rule, req Request) {
	if r == nil {
		return
	}
	key := ruleKey(rule)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stats == nil {
		r.stats = make(map[string]*ruleStats)
	}
	stats := r.stats[key]
	if stats == nil {
		stats = &ruleStats{Name: rule.Name}
		r.stats[key] = stats
	}
	stats.Name = rule.Name
	stats.Hits++
	stats.LastMatched = time.Now()
	stats.LastRequest = normalizeRequest(req)
}

func (r *RuleSet) setLastError(err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.lastError = ""
		return
	}
	r.lastError = err.Error()
}

func (r *RuleSet) recordFailure(action, source string, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	message := ""
	if err != nil {
		message = err.Error()
	}
	r.lastError = message
	r.appendEventLocked(RuleSetEvent{Version: r.version, At: time.Now(), Action: action, Source: source, Success: false, Rules: len(r.rules), Error: message})
}

func (r *RuleSet) appendEventLocked(event RuleSetEvent) {
	if event.Action == "" {
		event.Action = "unknown"
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	r.events = append(r.events, event)
	if len(r.events) > defaultRuleSetEventLimit {
		copy(r.events, r.events[len(r.events)-defaultRuleSetEventLimit:])
		r.events = r.events[:defaultRuleSetEventLimit]
	}
	r.notifySubscribersLocked(event)
}

func (r *RuleSet) notifySubscribersLocked(event RuleSetEvent) {
	for _, subscriber := range r.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (r *RuleSet) appendVersionLocked(version RuleSetVersion) {
	if version.Action == "" {
		version.Action = "unknown"
	}
	if version.At.IsZero() {
		version.At = time.Now()
	}
	version.Rules = cloneRules(version.Rules)
	r.versions = append(r.versions, version)
	if len(r.versions) > defaultRuleSetVersionLimit {
		copy(r.versions, r.versions[len(r.versions)-defaultRuleSetVersionLimit:])
		r.versions = r.versions[:defaultRuleSetVersionLimit]
	}
}

func providerSource(provider RuleProvider) string {
	if source, ok := provider.(RuleProviderSource); ok {
		return source.Source()
	}
	return "provider"
}

func ValidateRules(rules ...Rule) error {
	seenNames := make(map[string]struct{}, len(rules))
	for i, rule := range rules {
		rule = normalizeRule(rule)
		label := fmt.Sprintf("rule[%d]", i)
		if rule.Name != "" {
			label = fmt.Sprintf("rule[%d] %q", i, rule.Name)
			if _, ok := seenNames[rule.Name]; ok {
				return fmt.Errorf("%s: duplicate rule name", label)
			}
			seenNames[rule.Name] = struct{}{}
		}
		if err := validateTransport(rule.Transport); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if err := validatePolicy(rule.Policy); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	return nil
}

func validateTransport(transport string) error {
	switch transport {
	case "", TransportREST, TransportRPC, TransportGateway, TransportMQ:
		return nil
	default:
		return fmt.Errorf("unknown transport %q", transport)
	}
}

func validatePolicy(policy Policy) error {
	if policy.Timeout < 0 {
		return errors.New("timeout must be non-negative")
	}
	if policy.MaxBodyBytes < 0 {
		return errors.New("max body bytes must be non-negative")
	}
	if policy.Retry.Attempts < 0 {
		return errors.New("retry attempts must be non-negative")
	}
	if policy.Retry.Backoff < 0 {
		return errors.New("retry backoff must be non-negative")
	}
	for _, status := range policy.Retry.Statuses {
		if status < 100 || status > 599 {
			return fmt.Errorf("retry status %d is outside HTTP status range", status)
		}
	}
	if policy.Breaker.OpenTimeout < 0 {
		return errors.New("breaker open timeout must be non-negative")
	}
	if policy.Breaker.Window < 0 {
		return errors.New("breaker window must be non-negative")
	}
	if policy.Breaker.Buckets < 0 {
		return errors.New("breaker buckets must be non-negative")
	}
	if policy.Breaker.MinRequests < 0 {
		return errors.New("breaker min requests must be non-negative")
	}
	if policy.Breaker.FailureRatio < 0 || policy.Breaker.FailureRatio > 1 || math.IsNaN(policy.Breaker.FailureRatio) || math.IsInf(policy.Breaker.FailureRatio, 0) {
		return errors.New("breaker failure ratio must be between 0 and 1")
	}
	if policy.RateLimit.Rate < 0 {
		return errors.New("rate limit rate must be non-negative")
	}
	if policy.RateLimit.Burst < 0 {
		return errors.New("rate limit burst must be non-negative")
	}
	if policy.Concurrency.Limit < 0 {
		return errors.New("concurrency limit must be non-negative")
	}
	if policy.Canary.Ratio < 0 || policy.Canary.Ratio > 1 || math.IsNaN(policy.Canary.Ratio) || math.IsInf(policy.Canary.Ratio, 0) {
		return errors.New("canary ratio must be between 0 and 1")
	}
	return nil
}

func ruleMatches(rule Rule, req Request) bool {
	if rule.Transport != "" && rule.Transport != req.Transport {
		return false
	}
	if rule.Service != "" && rule.Service != req.Service {
		return false
	}
	if rule.Method != "" && rule.Method != req.Method {
		return false
	}
	if rule.Path != "" && !pathMatches(rule.Path, req.Path) {
		return false
	}
	for key, value := range rule.Tags {
		if req.Tags[key] != value {
			return false
		}
	}
	return true
}

func explainRule(rule Rule, req Request) (bool, string) {
	if rule.Transport != "" && rule.Transport != req.Transport {
		return false, fmt.Sprintf("transport mismatch: rule=%q request=%q", rule.Transport, req.Transport)
	}
	if rule.Service != "" && rule.Service != req.Service {
		return false, fmt.Sprintf("service mismatch: rule=%q request=%q", rule.Service, req.Service)
	}
	if rule.Method != "" && rule.Method != req.Method {
		return false, fmt.Sprintf("method mismatch: rule=%q request=%q", rule.Method, req.Method)
	}
	if rule.Path != "" && !pathMatches(rule.Path, req.Path) {
		return false, fmt.Sprintf("path mismatch: rule=%q request=%q", rule.Path, req.Path)
	}
	for key, value := range rule.Tags {
		if req.Tags[key] != value {
			return false, fmt.Sprintf("tag mismatch: %s rule=%q request=%q", key, value, req.Tags[key])
		}
	}
	return true, "matched"
}

func pathMatches(pattern, path string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if pattern == path {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "*"))
	}
	if pattern == "/" {
		return strings.HasPrefix(path, "/")
	}
	return false
}

func normalizeRequest(req Request) Request {
	req.Transport = strings.ToLower(strings.TrimSpace(req.Transport))
	req.Service = strings.TrimSpace(req.Service)
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	req.Path = strings.TrimSpace(req.Path)
	if req.Path != "" && !strings.HasPrefix(req.Path, "/") {
		req.Path = "/" + req.Path
	}
	req.Tags = cloneStringMap(req.Tags)
	req.Headers = cloneStringMap(req.Headers)
	req.Cookies = cloneStringMap(req.Cookies)
	return req
}

func normalizeRequestForMatch(req Request) Request {
	req.Transport = strings.ToLower(strings.TrimSpace(req.Transport))
	req.Service = strings.TrimSpace(req.Service)
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	req.Path = strings.TrimSpace(req.Path)
	if req.Path != "" && !strings.HasPrefix(req.Path, "/") {
		req.Path = "/" + req.Path
	}
	return req
}

func normalizeRule(rule Rule) Rule {
	rule.Transport = strings.ToLower(strings.TrimSpace(rule.Transport))
	rule.Service = strings.TrimSpace(rule.Service)
	rule.Method = strings.ToUpper(strings.TrimSpace(rule.Method))
	rule.Path = strings.TrimSpace(rule.Path)
	if rule.Path != "" && rule.Path != "*" && !strings.HasPrefix(rule.Path, "/") {
		rule.Path = "/" + rule.Path
	}
	rule.Tags = cloneStringMap(rule.Tags)
	rule.Policy = clonePolicy(rule.Policy)
	return rule
}

func ruleSpecificity(rule Rule) int {
	score := 0
	if rule.Transport != "" {
		score++
	}
	if rule.Service != "" {
		score++
	}
	if rule.Method != "" {
		score++
	}
	if rule.Path != "" && rule.Path != "*" {
		score += len(rule.Path)
	}
	score += len(rule.Tags)
	return score
}

func ruleKey(rule Rule) string {
	if rule.Name != "" {
		return "name:" + rule.Name
	}
	parts := []string{
		rule.Transport,
		rule.Service,
		rule.Method,
		rule.Path,
	}
	if len(rule.Tags) == 0 {
		return strings.Join(parts, "|")
	}
	tags := make([]string, 0, len(rule.Tags))
	for key, value := range rule.Tags {
		tags = append(tags, key+"="+value)
	}
	sort.Strings(tags)
	parts = append(parts, strings.Join(tags, ","))
	return strings.Join(parts, "|")
}

func diffRuleKey(rule Rule) string {
	rule = normalizeRule(rule)
	if rule.Name != "" {
		return "name:" + rule.Name
	}
	return ruleKey(rule)
}

func cloneRules(rules []Rule) []Rule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]Rule, len(rules))
	for i, rule := range rules {
		out[i] = normalizeRule(rule)
	}
	return out
}

func clonePolicy(policy Policy) Policy {
	policy.Retry.Statuses = append([]int(nil), policy.Retry.Statuses...)
	policy.Retry.Methods = append([]string(nil), policy.Retry.Methods...)
	policy.Headers = cloneStringMap(policy.Headers)
	policy.Metadata = cloneStringMap(policy.Metadata)
	policy.Canary.Targets = append([]string(nil), policy.Canary.Targets...)
	policy.Canary.Headers = cloneStringMap(policy.Canary.Headers)
	policy.Canary.MatchHeaders = cloneStringMap(policy.Canary.MatchHeaders)
	policy.Canary.MatchCookies = cloneStringMap(policy.Canary.MatchCookies)
	return policy
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func headerMap(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		out[http.CanonicalHeaderKey(key)] = values[0]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cookiesMap(cookies []*http.Cookie) map[string]string {
	if len(cookies) == 0 {
		return nil
	}
	out := make(map[string]string, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		out[cookie.Name] = cookie.Value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func canaryMatches(policy CanaryPolicy, req Request) bool {
	req = normalizeRequest(req)
	matchedPredicate := false
	for key, value := range policy.MatchHeaders {
		matchedPredicate = true
		if req.Headers[http.CanonicalHeaderKey(key)] != value && req.Headers[key] != value {
			return false
		}
	}
	for key, value := range policy.MatchCookies {
		matchedPredicate = true
		if req.Cookies[key] != value {
			return false
		}
	}
	if policy.Ratio <= 0 {
		return matchedPredicate
	}
	ratio := policy.Ratio
	if ratio > 1 {
		ratio = 1
	}
	return canaryBucket(req) < uint32(ratio*1_000_000)
}

func canaryBucket(req Request) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(req.Transport))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(req.Service))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(req.Method))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(req.Path))
	_, _ = h.Write([]byte{0})
	if value := req.Headers["X-Request-Id"]; value != "" {
		_, _ = h.Write([]byte(value))
	} else if value = req.Headers["X-Forwarded-For"]; value != "" {
		_, _ = h.Write([]byte(value))
	} else {
		tags := make([]string, 0, len(req.Tags))
		for key, value := range req.Tags {
			tags = append(tags, key+"="+value)
		}
		sort.Strings(tags)
		_, _ = h.Write([]byte(strings.Join(tags, ",")))
	}
	return h.Sum32() % 1_000_000
}
