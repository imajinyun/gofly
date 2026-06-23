package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofly/gofly/core/controlplane"
	"github.com/gofly/gofly/core/governance"
)

const (
	RPCBalancerRoundRobin         = "round_robin"
	RPCBalancerWeightedRoundRobin = "weighted_round_robin"
	RPCBalancerP2C                = "p2c"
	RPCBalancerConsistentHash     = "consistent_hash"
	RPCBalancerHealth             = "health"
)

// RPCPolicy is the transport-neutral policy contract used by RPC clients,
// servers and generated manifests. It intentionally mirrors governance.Policy
// without tying future endpoint-chain work to a single rule source.
type RPCPolicy struct {
	Timeout     time.Duration            `json:"timeout,omitempty"`
	Retry       governance.RetryPolicy   `json:"retry,omitempty"`
	Hedge       RPCHedgePolicy           `json:"hedge,omitempty"`
	Fallback    RPCFallbackPolicy        `json:"fallback,omitempty"`
	Breaker     governance.BreakerPolicy `json:"breaker,omitempty"`
	LoadShedder RPCLoadShedderPolicy     `json:"loadShedder,omitempty"`
	Balancer    RPCBalancerPolicy        `json:"balancer,omitempty"`
	Metadata    map[string]string        `json:"metadata,omitempty"`
	Headers     map[string]string        `json:"headers,omitempty"`
	Methods     map[string]RPCPolicy     `json:"methods,omitempty"`
}

type RPCHedgePolicy struct {
	Enabled  bool          `json:"enabled,omitempty"`
	Delay    time.Duration `json:"delay,omitempty"`
	Attempts int           `json:"attempts,omitempty"`
}

type RPCFallbackPolicy struct {
	Enabled bool   `json:"enabled,omitempty"`
	Target  string `json:"target,omitempty"`
	Method  string `json:"method,omitempty"`
}

type RPCLoadShedderPolicy struct {
	Enabled        bool          `json:"enabled,omitempty"`
	MaxConcurrency int           `json:"maxConcurrency,omitempty"`
	MaxInflight    int           `json:"maxInflight,omitempty"`
	MinWindow      time.Duration `json:"minWindow,omitempty"`
}

type RPCBalancerPolicy struct {
	Name    string         `json:"name,omitempty"`
	Weights map[string]int `json:"weights,omitempty"`
	Key     string         `json:"key,omitempty"`
}

type RPCPolicyRuntimeSnapshot struct {
	Policy            RPCPolicy                     `json:"policy,omitempty"`
	State             RPCPolicyRuntimeState         `json:"state"`
	Cache             RPCPolicyRuntimeCacheSnapshot `json:"cache,omitempty"`
	MethodPolicyCount int                           `json:"methodPolicyCount,omitempty"`
	MethodPolicyKeys  []string                      `json:"methodPolicyKeys,omitempty"`
	Capabilities      []string                      `json:"capabilities,omitempty"`
}

type RPCRuntimeSnapshot struct {
	Target      string                      `json:"target,omitempty"`
	Codec       string                      `json:"codec,omitempty"`
	Transport   RPCHTTPTransportSnapshot    `json:"transport,omitempty"`
	Middlewares RPCEndpointChainSnapshot    `json:"middlewares,omitempty"`
	Resolver    RPCResolverRuntimeSnapshot  `json:"resolver,omitempty"`
	Balancer    string                      `json:"balancer,omitempty"`
	Policy      RPCPolicyRuntimeSnapshot    `json:"policy"`
	Discovery   RPCDiscoveryRuntimeSnapshot `json:"discovery,omitempty"`
}

type RPCEndpointChainSnapshot struct {
	Unary  int `json:"unary,omitempty"`
	Stream int `json:"stream,omitempty"`
}

type RPCHTTPTransportSnapshot struct {
	Timeout             time.Duration `json:"timeout,omitempty"`
	CloseIdleOnEndpoint bool          `json:"closeIdleOnEndpointChange"`
}

type RPCResolverRuntimeSnapshot struct {
	Type     string            `json:"type,omitempty"`
	Watch    bool              `json:"watch"`
	Snapshot *ResolverSnapshot `json:"snapshot,omitempty"`
}

type RPCDiscoveryRuntimeSnapshot struct {
	WatchEnabled   bool      `json:"watchEnabled"`
	Updates        int64     `json:"updates,omitempty"`
	LastUpdated    time.Time `json:"lastUpdated,omitempty"`
	Endpoints      []string  `json:"endpoints,omitempty"`
	Removed        []string  `json:"removed,omitempty"`
	CloseIdleCalls int64     `json:"closeIdleCalls,omitempty"`
	WatchError     string    `json:"watchError,omitempty"`
}

type RPCPolicyRuntimeState struct {
	TimeoutEnforced     bool          `json:"timeoutEnforced"`
	EffectiveTimeout    time.Duration `json:"effectiveTimeout,omitempty"`
	RetryAttempts       int           `json:"retryAttempts,omitempty"`
	RetryBackoff        time.Duration `json:"retryBackoff,omitempty"`
	BreakerEnabled      bool          `json:"breakerEnabled"`
	Balancer            string        `json:"balancer,omitempty"`
	LoadShedderEnabled  bool          `json:"loadShedderEnabled"`
	LoadShedderLimit    int           `json:"loadShedderLimit,omitempty"`
	FallbackEnabled     bool          `json:"fallbackEnabled"`
	FallbackTarget      string        `json:"fallbackTarget,omitempty"`
	HedgeEnabled        bool          `json:"hedgeEnabled"`
	HedgeAttempts       int           `json:"hedgeAttempts,omitempty"`
	GovernanceBacked    bool          `json:"governanceBacked"`
	ExplicitPolicyBound bool          `json:"explicitPolicyBound"`
	DynamicPolicyBound  bool          `json:"dynamicPolicyBound"`
}

type RPCPolicyRuntimeCacheSnapshot struct {
	RateLimiters        int `json:"rateLimiters,omitempty"`
	ConcurrencyLimiters int `json:"concurrencyLimiters,omitempty"`
	Breakers            int `json:"breakers,omitempty"`
	Balancers           int `json:"balancers,omitempty"`
}

type RPCPolicyRuntimeSnapshotSource interface {
	PolicyRuntimeSnapshot() RPCPolicyRuntimeSnapshot
}

type RPCRuntimeSnapshotSource interface {
	RuntimeSnapshot() RPCRuntimeSnapshot
}

type RPCPolicyRuntimeContributor struct {
	Name   string
	Client RPCPolicyRuntimeSnapshotSource
}

type RPCRuntimeContributor struct {
	Name   string
	Client RPCRuntimeSnapshotSource
}

func (c RPCPolicyRuntimeContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil || c.Client == nil {
		return nil
	}
	runtimeSnapshot := c.Client.PolicyRuntimeSnapshot()
	data, err := json.Marshal(runtimeSnapshot)
	if err != nil {
		return fmt.Errorf("marshal rpc policy runtime snapshot: %w", err)
	}
	if snapshot.Configs == nil {
		snapshot.Configs = make(map[string]json.RawMessage, 1)
	}
	key := "rpc.policy.runtime"
	if strings.TrimSpace(c.Name) != "" {
		key += "." + strings.TrimSpace(c.Name)
	}
	snapshot.Configs[key] = append(json.RawMessage(nil), data...)
	if snapshot.Metadata == nil {
		snapshot.Metadata = make(map[string]string, 2)
	}
	snapshot.Metadata["rpc.policy.runtime"] = "available"
	snapshot.Metadata["rpc.policy.runtime.enforcement"] = "timeout,retry,breaker,balancer,load_shedder,fallback,hedge"
	return nil
}

func (c RPCRuntimeContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil || c.Client == nil {
		return nil
	}
	runtimeSnapshot := c.Client.RuntimeSnapshot()
	data, err := json.Marshal(runtimeSnapshot)
	if err != nil {
		return fmt.Errorf("marshal rpc runtime snapshot: %w", err)
	}
	if snapshot.Configs == nil {
		snapshot.Configs = make(map[string]json.RawMessage, 1)
	}
	key := "rpc.runtime"
	if strings.TrimSpace(c.Name) != "" {
		key += "." + strings.TrimSpace(c.Name)
	}
	snapshot.Configs[key] = append(json.RawMessage(nil), data...)
	if snapshot.Metadata == nil {
		snapshot.Metadata = make(map[string]string, 1)
	}
	snapshot.Metadata["rpc.runtime"] = "available"
	return nil
}

func (c *HTTPClient) RuntimeSnapshot() RPCRuntimeSnapshot {
	if c == nil {
		return RPCRuntimeSnapshot{}
	}
	return RPCRuntimeSnapshot{
		Target:      c.target,
		Codec:       c.opts.codec.Name(),
		Transport:   c.httpTransportSnapshot(),
		Middlewares: RPCEndpointChainSnapshot{Unary: len(c.opts.middlewares), Stream: len(c.opts.streamMiddlewares)},
		Resolver:    c.resolverRuntimeSnapshot(),
		Balancer:    c.effectiveBalancerName(RPCBalancerPolicy{}),
		Policy:      c.PolicyRuntimeSnapshot(),
		Discovery:   c.discovery.Snapshot(),
	}
}

func (c *HTTPClient) PolicyRuntimeSnapshot() RPCPolicyRuntimeSnapshot {
	if c == nil {
		return RPCPolicyRuntimeSnapshot{}
	}
	policy := c.effectiveRPCPolicy(governance.Policy{})
	state := c.policyRuntimeState(policy)
	return RPCPolicyRuntimeSnapshot{
		Policy:            cloneRPCPolicy(policy),
		State:             state,
		Cache:             c.policyRuntimeCacheSnapshot(),
		MethodPolicyCount: len(policy.Methods),
		MethodPolicyKeys:  rpcMethodPolicyKeys(policy.Methods),
		Capabilities:      rpcPolicyRuntimeCapabilities(),
	}
}

func (c *HTTPClient) httpTransportSnapshot() RPCHTTPTransportSnapshot {
	if c == nil || c.hc == nil {
		return RPCHTTPTransportSnapshot{}
	}
	snapshot := RPCHTTPTransportSnapshot{CloseIdleOnEndpoint: c.watchCancel != nil}
	snapshot.Timeout = c.hc.Timeout
	return snapshot
}

func (c *HTTPClient) resolverRuntimeSnapshot() RPCResolverRuntimeSnapshot {
	if c == nil || c.opts.resolver == nil {
		return RPCResolverRuntimeSnapshot{}
	}
	snapshot := RPCResolverRuntimeSnapshot{
		Type:  fmt.Sprintf("%T", c.opts.resolver),
		Watch: c.watchCancel != nil,
	}
	if source, ok := c.opts.resolver.(interface{ Snapshot() ResolverSnapshot }); ok {
		resolverSnapshot := source.Snapshot()
		snapshot.Snapshot = &resolverSnapshot
	}
	return snapshot
}

type clientDiscoveryRuntime struct {
	mu             sync.Mutex
	watchEnabled   bool
	updates        int64
	lastUpdated    time.Time
	endpoints      []string
	removed        []string
	closeIdleCalls int64
	watchErr       error
}

func newClientDiscoveryRuntime(resolver Resolver) *clientDiscoveryRuntime {
	_, watchEnabled := resolver.(WatchResolver)
	return &clientDiscoveryRuntime{watchEnabled: watchEnabled}
}

func (r *clientDiscoveryRuntime) recordUpdate(endpoints []string, removed []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints = append([]string(nil), endpoints...)
	r.removed = append([]string(nil), removed...)
	r.updates++
	r.lastUpdated = time.Now()
	r.watchErr = nil
}

func (r *clientDiscoveryRuntime) recordWatchError(err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.watchErr = err
}

func (r *clientDiscoveryRuntime) recordCloseIdle() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeIdleCalls++
}

func (r *clientDiscoveryRuntime) Snapshot() RPCDiscoveryRuntimeSnapshot {
	if r == nil {
		return RPCDiscoveryRuntimeSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := RPCDiscoveryRuntimeSnapshot{
		WatchEnabled:   r.watchEnabled,
		Updates:        r.updates,
		LastUpdated:    r.lastUpdated,
		Endpoints:      append([]string(nil), r.endpoints...),
		Removed:        append([]string(nil), r.removed...),
		CloseIdleCalls: r.closeIdleCalls,
	}
	if r.watchErr != nil {
		snapshot.WatchError = r.watchErr.Error()
	}
	return snapshot
}

func (c *HTTPClient) policyRuntimeState(policy RPCPolicy) RPCPolicyRuntimeState {
	timeout := c.opts.timeout
	if policy.Timeout > 0 {
		timeout = policy.Timeout
	}
	retryAttempts := c.opts.retry
	retryBackoff := c.opts.retryPolicy.Backoff
	if c.opts.retryPolicy.Attempts > 0 {
		retryAttempts = c.opts.retryPolicy.Attempts
	}
	if policy.Retry.Attempts > 0 {
		retryAttempts = policy.Retry.Attempts
	}
	if policy.Retry.Backoff > 0 {
		retryBackoff = policy.Retry.Backoff
	}
	return RPCPolicyRuntimeState{
		TimeoutEnforced:     timeout > 0,
		EffectiveTimeout:    timeout,
		RetryAttempts:       retryAttempts,
		RetryBackoff:        retryBackoff,
		BreakerEnabled:      policy.Breaker.Enabled || c.opts.breaker != nil || c.opts.adaptive != nil,
		Balancer:            c.effectiveBalancerName(policy.Balancer),
		LoadShedderEnabled:  policy.LoadShedder.Enabled,
		LoadShedderLimit:    rpcLoadShedderLimit(policy.LoadShedder),
		FallbackEnabled:     policy.Fallback.Enabled,
		FallbackTarget:      policy.Fallback.Target,
		HedgeEnabled:        policy.Hedge.Enabled,
		HedgeAttempts:       policy.Hedge.Attempts,
		GovernanceBacked:    c.opts.rules != nil || c.opts.manager != nil,
		ExplicitPolicyBound: c.opts.rpcPolicy != nil,
		DynamicPolicyBound:  c.opts.rpcPolicyProvider != nil,
	}
}

func (c *HTTPClient) policyRuntimeCacheSnapshot() RPCPolicyRuntimeCacheSnapshot {
	if c == nil || c.runtime == nil {
		return RPCPolicyRuntimeCacheSnapshot{}
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	return RPCPolicyRuntimeCacheSnapshot{
		RateLimiters:        len(c.runtime.rateLimits),
		ConcurrencyLimiters: len(c.runtime.concurrency),
		Breakers:            len(c.runtime.breakers),
		Balancers:           len(c.runtime.balancers),
	}
}

func (c *HTTPClient) effectiveBalancerName(policy RPCBalancerPolicy) string {
	if strings.TrimSpace(policy.Name) != "" {
		return strings.TrimSpace(policy.Name)
	}
	switch c.opts.balancer.(type) {
	case *WeightedRoundRobinBalancer:
		return RPCBalancerWeightedRoundRobin
	case *P2CBalancer, *P2CEWMABalancer:
		return RPCBalancerP2C
	case *ConsistentHashBalancer:
		return RPCBalancerConsistentHash
	case *HealthBalancer:
		return RPCBalancerHealth
	default:
		return RPCBalancerRoundRobin
	}
}

func rpcLoadShedderLimit(policy RPCLoadShedderPolicy) int {
	if policy.MaxConcurrency > 0 {
		return policy.MaxConcurrency
	}
	return policy.MaxInflight
}

func rpcPolicyRuntimeCapabilities() []string {
	return []string{"timeout", "retry", "breaker", "balancer", "load_shedder", "fallback", "hedge", "method_policy", "dynamic_policy", "endpoint_chain", "kitex_interceptor", "observability_interceptor"}
}

func RPCPolicyFromGovernance(policy governance.Policy) RPCPolicy {
	return RPCPolicy{
		Timeout:  policy.Timeout,
		Retry:    policy.Retry,
		Breaker:  policy.Breaker,
		Metadata: cloneRPCPolicyStringMap(policy.Metadata),
		Headers:  cloneRPCPolicyStringMap(policy.Headers),
	}
}

func cloneRPCPolicy(policy RPCPolicy) RPCPolicy {
	policy.Metadata = cloneRPCPolicyStringMap(policy.Metadata)
	policy.Headers = cloneRPCPolicyStringMap(policy.Headers)
	policy.Balancer.Weights = cloneRPCPolicyIntMap(policy.Balancer.Weights)
	policy.Retry.Statuses = append([]int(nil), policy.Retry.Statuses...)
	policy.Retry.Methods = append([]string(nil), policy.Retry.Methods...)
	policy.Methods = cloneRPCPolicyMethods(policy.Methods)
	return policy
}

func mergeRPCPolicy(base RPCPolicy, override RPCPolicy) RPCPolicy {
	merged := cloneRPCPolicy(base)
	if override.Timeout > 0 {
		merged.Timeout = override.Timeout
	}
	if override.Retry.Attempts > 0 {
		merged.Retry.Attempts = override.Retry.Attempts
	}
	if override.Retry.Backoff > 0 {
		merged.Retry.Backoff = override.Retry.Backoff
	}
	if len(override.Retry.Statuses) > 0 {
		merged.Retry.Statuses = append([]int(nil), override.Retry.Statuses...)
	}
	if len(override.Retry.Methods) > 0 {
		merged.Retry.Methods = append([]string(nil), override.Retry.Methods...)
	}
	if override.Breaker.Enabled {
		merged.Breaker = override.Breaker
	}
	if override.Hedge.Enabled || override.Hedge.Delay > 0 || override.Hedge.Attempts > 0 {
		merged.Hedge = override.Hedge
	}
	if override.Fallback.Enabled || strings.TrimSpace(override.Fallback.Target) != "" || strings.TrimSpace(override.Fallback.Method) != "" {
		merged.Fallback = override.Fallback
	}
	if override.LoadShedder.Enabled || override.LoadShedder.MaxConcurrency > 0 || override.LoadShedder.MaxInflight > 0 || override.LoadShedder.MinWindow > 0 {
		merged.LoadShedder = override.LoadShedder
	}
	if strings.TrimSpace(override.Balancer.Name) != "" || len(override.Balancer.Weights) > 0 || strings.TrimSpace(override.Balancer.Key) != "" {
		merged.Balancer = override.Balancer
		merged.Balancer.Weights = cloneRPCPolicyIntMap(override.Balancer.Weights)
	}
	merged.Metadata = mergeRPCPolicyStringMap(merged.Metadata, override.Metadata)
	merged.Headers = mergeRPCPolicyStringMap(merged.Headers, override.Headers)
	merged.Methods = mergeRPCPolicyMethods(merged.Methods, override.Methods)
	return merged
}

func (p RPCPolicy) Validate() error {
	if p.Timeout < 0 {
		return errors.New("rpc policy timeout must be non-negative")
	}
	if p.Retry.Attempts < 0 {
		return errors.New("rpc policy retry attempts must be non-negative")
	}
	if p.Retry.Backoff < 0 {
		return errors.New("rpc policy retry backoff must be non-negative")
	}
	if p.Hedge.Delay < 0 {
		return errors.New("rpc policy hedge delay must be non-negative")
	}
	if p.Hedge.Attempts < 0 {
		return errors.New("rpc policy hedge attempts must be non-negative")
	}
	if p.Hedge.Enabled && p.Hedge.Attempts == 1 {
		return errors.New("rpc policy hedge attempts must be zero or greater than one")
	}
	if p.Fallback.Enabled && strings.TrimSpace(p.Fallback.Target) == "" {
		return errors.New("rpc policy fallback target is required when fallback is enabled")
	}
	if p.Breaker.OpenTimeout < 0 {
		return errors.New("rpc policy breaker open timeout must be non-negative")
	}
	if p.Breaker.Window < 0 {
		return errors.New("rpc policy breaker window must be non-negative")
	}
	if p.LoadShedder.MaxConcurrency < 0 {
		return errors.New("rpc policy load shedder max concurrency must be non-negative")
	}
	if p.LoadShedder.MaxInflight < 0 {
		return errors.New("rpc policy load shedder max inflight must be non-negative")
	}
	if p.LoadShedder.MinWindow < 0 {
		return errors.New("rpc policy load shedder min window must be non-negative")
	}
	if err := validateRPCBalancerPolicy(p.Balancer); err != nil {
		return err
	}
	for method, policy := range p.Methods {
		if strings.TrimSpace(method) == "" {
			return errors.New("rpc policy method key is empty")
		}
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("rpc policy method %q: %w", method, err)
		}
	}
	return nil
}

func validateRPCBalancerPolicy(policy RPCBalancerPolicy) error {
	name := strings.TrimSpace(policy.Name)
	if name == "" {
		return nil
	}
	switch name {
	case RPCBalancerRoundRobin, RPCBalancerWeightedRoundRobin, RPCBalancerP2C, RPCBalancerConsistentHash, RPCBalancerHealth:
	default:
		return fmt.Errorf("rpc policy balancer %q is unsupported", policy.Name)
	}
	for endpoint, weight := range policy.Weights {
		if strings.TrimSpace(endpoint) == "" {
			return errors.New("rpc policy balancer weight endpoint is empty")
		}
		if weight < 0 {
			return fmt.Errorf("rpc policy balancer weight for %q must be non-negative", endpoint)
		}
	}
	return nil
}

func cloneRPCPolicyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneRPCPolicyIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneRPCPolicyMethods(in map[string]RPCPolicy) map[string]RPCPolicy {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]RPCPolicy, len(in))
	for key, value := range in {
		cloned := cloneRPCPolicy(value)
		cloned.Methods = nil
		out[key] = cloned
	}
	return out
}

func mergeRPCPolicyMethods(base map[string]RPCPolicy, override map[string]RPCPolicy) map[string]RPCPolicy {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := cloneRPCPolicyMethods(base)
	if out == nil {
		out = make(map[string]RPCPolicy, len(override))
	}
	for key, value := range override {
		if existing, ok := out[key]; ok {
			out[key] = mergeRPCPolicy(existing, value)
			continue
		}
		out[key] = cloneRPCPolicy(value)
	}
	return out
}

func rpcMethodPolicyKeys(methods map[string]RPCPolicy) []string {
	if len(methods) == 0 {
		return nil
	}
	keys := make([]string, 0, len(methods))
	for key := range methods {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeRPCPolicyStringMap(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := cloneRPCPolicyStringMap(base)
	if out == nil {
		out = make(map[string]string, len(override))
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}
