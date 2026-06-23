// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/retry"
	"github.com/gofly/gofly/rpc/endpoint"
)

// Client is the basic RPC client interface.
type Client interface {
	Call(ctx context.Context, method string, request any, response any) error
}

// MetadataClient is a Client that also returns response metadata.
type MetadataClient interface {
	Client
	CallWithMetadata(ctx context.Context, method string, request any, response any) (metadata.MD, error)
}

// GenericClient is a Client that returns raw JSON responses.
type GenericClient interface {
	CallRaw(ctx context.Context, method string, request any) (json.RawMessage, metadata.MD, error)
}

type callResult struct {
	Payload  json.RawMessage
	Metadata metadata.MD
}

type rawResponseEnvelope struct {
	Payload      json.RawMessage `json:"payload,omitempty"`
	PayloadBytes []byte          `json:"payloadBytes,omitempty"`
	Codec        string          `json:"codec,omitempty"`
	Metadata     metadata.MD     `json:"metadata,omitempty"`
	Code         Code            `json:"code,omitempty"`
	Error        string          `json:"error,omitempty"`
}

type HTTPClient struct {
	target      string
	hc          *http.Client
	opts        clientOptions
	runtime     *ruleRuntime
	discovery   *clientDiscoveryRuntime
	watchCancel context.CancelFunc
	closeOnce   sync.Once
}

func NewClient(target string, opts ...ClientOption) (*HTTPClient, error) {
	if target == "" {
		return nil, errors.New("target is required")
	}
	o := clientOptions{codec: JSONCodec{}, timeout: 3 * time.Second, retry: 1, balancer: &RoundRobinBalancer{}}
	for _, opt := range opts {
		opt(&o)
	}
	if o.rpcPolicy != nil {
		if err := o.rpcPolicy.Validate(); err != nil {
			return nil, fmt.Errorf("configure rpc policy: %w", err)
		}
	}
	if o.resolver == nil {
		o.resolver = NewStaticResolver(target)
	}
	hc := o.httpClient
	if hc == nil {
		hc = NewHTTPClient(DefaultTransportConfig())
	}
	if o.tls != nil {
		tlsCfg, err := o.tls.ClientTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("configure rpc tls: %w", err)
		}
		if tlsCfg != nil {
			if transport, ok := hc.Transport.(*http.Transport); ok {
				transport = transport.Clone()
				transport.TLSClientConfig = tlsCfg
				hc = &http.Client{Transport: transport, Timeout: hc.Timeout, CheckRedirect: hc.CheckRedirect, Jar: hc.Jar}
			} else {
				hc = &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: hc.Timeout}
			}
		}
	}
	client := &HTTPClient{
		target:    strings.TrimRight(target, "/"),
		hc:        hc,
		opts:      o,
		runtime:   newRuleRuntime(),
		discovery: newClientDiscoveryRuntime(o.resolver),
	}
	client.startResolverWatch()
	return client, nil
}

// Close releases background resolver watches and idle HTTP transport resources.
func (c *HTTPClient) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		if c.watchCancel != nil {
			c.watchCancel()
		}
		c.closeIdleConnections()
	})
	return nil
}

func (c *HTTPClient) Call(ctx context.Context, method string, request any, response any) error {
	_, err := c.CallWithMetadata(ctx, method, request, response)
	return err
}

func (c *HTTPClient) CallWithMetadata(ctx context.Context, method string, request any, response any) (metadata.MD, error) {
	ep := endpoint.Endpoint(func(ctx context.Context, req any) (any, error) {
		return c.doCall(ctx, method, req)
	})
	ep = endpoint.Chain(c.opts.middlewares...)(ep)
	result, err := ep(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	call, ok := result.(*callResult)
	if !ok {
		return nil, fmt.Errorf("unexpected rpc call result %T", result)
	}
	if response != nil {
		if err := c.unmarshalResponsePayload(call.Payload, response); err != nil {
			return nil, err
		}
	}
	return call.Metadata.Clone(), nil
}

func (c *HTTPClient) CallRaw(ctx context.Context, method string, request any) (json.RawMessage, metadata.MD, error) {
	ep := endpoint.Endpoint(func(ctx context.Context, req any) (any, error) {
		return c.doCall(ctx, method, req)
	})
	ep = endpoint.Chain(c.opts.middlewares...)(ep)
	result, err := ep(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	call, ok := result.(*callResult)
	if !ok || call == nil {
		return nil, nil, fmt.Errorf("unexpected rpc call result %T", result)
	}
	return append(json.RawMessage(nil), call.Payload...), call.Metadata.Clone(), nil
}

func (c *HTTPClient) doCall(ctx context.Context, method string, request any) (*callResult, error) {
	governanceReq := c.rpcGovernanceRequest(ctx, method)
	decision := c.governanceDecision(ctx, governanceReq)
	policy := decision.Policy
	rpcPolicy := c.effectiveRPCPolicy(policy)
	if c.opts.rpcPolicyProvider != nil {
		dynamicPolicy, err := c.opts.rpcPolicyProvider.RPCPolicy(ctx, governanceReq)
		if err != nil {
			return nil, fmt.Errorf("resolve dynamic rpc policy: %w", err)
		}
		rpcPolicy = mergeRPCPolicy(rpcPolicy, dynamicPolicy)
	}
	if err := rpcPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("apply rpc policy: %w", err)
	}
	rpcPolicy = rpcPolicyForMethod(rpcPolicy, method)
	runtimeKey := governanceRuntimeKey(decision, method)
	if limiter := c.ruleRateLimiter(runtimeKey, policy.RateLimit); limiter != nil && !limiter.Allow() {
		return nil, NewError(CodeResourceExhausted, "too many requests")
	}
	if limiter := c.ruleConcurrencyLimiter(runtimeKey, policy.Concurrency); limiter != nil {
		if !limiter.TryAcquire() {
			return nil, NewError(CodeUnavailable, "too many concurrent requests")
		}
		defer limiter.Release()
	}
	if limiter := c.ruleLoadShedderLimiter(runtimeKey, rpcPolicy.LoadShedder); limiter != nil {
		if !limiter.TryAcquire() {
			return nil, NewError(CodeResourceExhausted, "rpc load shedder rejected request")
		}
		defer limiter.Release()
	}
	ctx = applyGovernanceMetadata(ctx, canaryMetadata(policy.Canary, governanceReq))
	ctx = applyGovernanceMetadata(ctx, rpcPolicy.Metadata)
	ctx = applyGovernanceMetadata(ctx, rpcPolicy.Headers)
	timeout := c.opts.timeout
	if rpcPolicy.Timeout > 0 {
		timeout = rpcPolicy.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	execute := endpoint.Endpoint(func(ctx context.Context, req any) (any, error) {
		if c.opts.singleflight != nil {
			key, err := c.singleflightKey(ctx, method, req)
			if err != nil {
				return nil, err
			}
			result, _, err := c.opts.singleflight.Do(ctx, key, func(ctx context.Context) (*callResult, error) {
				return c.doCallOnce(ctx, method, req, rpcPolicy, runtimeKey)
			})
			return cloneCallResult(result), err
		}
		return c.doCallOnce(ctx, method, req, rpcPolicy, runtimeKey)
	})
	if rpcPolicy.Hedge.Enabled {
		execute = endpoint.HedgingMiddleware(hedgeConfigFromRPCPolicy(rpcPolicy.Hedge))(execute)
	}
	result, err := execute(ctx, request)
	if err != nil {
		return nil, err
	}
	call, ok := result.(*callResult)
	if !ok || call == nil {
		return nil, fmt.Errorf("unexpected rpc call result %T", result)
	}
	return call, nil
}

func (c *HTTPClient) doCallOnce(ctx context.Context, method string, request any, policy RPCPolicy, runtimeKey string) (*callResult, error) {
	var result *callResult
	call := func() error {
		if c.opts.adaptive != nil {
			if err := c.opts.adaptive.Allow(); err != nil {
				return err
			}
			var err error
			result, err = c.postWithPolicy(ctx, method, request, policy)
			if isRetryable(err) {
				c.opts.adaptive.MarkFailure()
			} else {
				c.opts.adaptive.MarkSuccess()
			}
			return err
		}
		if c.opts.breaker != nil {
			return c.opts.breaker.Do(ctx, func() error {
				var err error
				result, err = c.postWithPolicy(ctx, method, request, policy)
				return err
			})
		}
		var err error
		result, err = c.postWithPolicy(ctx, method, request, policy)
		return err
	}
	fn := func() error {
		if brk := c.ruleBreaker(runtimeKey, policy.Breaker); brk != nil {
			if err := brk.Allow(); err != nil {
				return err
			}
			err := call()
			if err != nil {
				brk.MarkFailure()
			} else {
				brk.MarkSuccess()
			}
			return err
		}
		return call()
	}
	retryPolicy := c.opts.retryPolicy
	if retryPolicy.Attempts <= 0 {
		retryPolicy.Attempts = c.opts.retry
	}
	if policy.Retry.Attempts > 0 {
		retryPolicy.Attempts = policy.Retry.Attempts
	}
	if policy.Retry.Backoff > 0 {
		retryPolicy.Backoff = policy.Retry.Backoff
	}
	if retryPolicy.Backoff <= 0 && retryPolicy.BackoffFunc == nil {
		retryPolicy.Backoff = 10 * time.Millisecond
	}
	if retryPolicy.ShouldRetry == nil {
		retryPolicy.ShouldRetry = isRetryable
	}
	err := retry.Do(ctx, retryPolicy, fn)
	err = normalizeContextError(ctx, err)
	if errors.Is(err, breaker.ErrOpen) {
		return nil, NewError(CodeUnavailable, err.Error())
	}
	return result, err
}

func (c *HTTPClient) effectiveRPCPolicy(policy governance.Policy) RPCPolicy {
	if c == nil || c.opts.rpcPolicy == nil {
		return RPCPolicyFromGovernance(policy)
	}
	return mergeRPCPolicy(*c.opts.rpcPolicy, RPCPolicyFromGovernance(policy))
}

func rpcPolicyForMethod(policy RPCPolicy, method string) RPCPolicy {
	methodPolicy, ok := matchRPCMethodPolicy(policy.Methods, method)
	policy.Methods = nil
	if !ok {
		return policy
	}
	methodPolicy.Methods = nil
	return mergeRPCPolicy(policy, methodPolicy)
}

func matchRPCMethodPolicy(methods map[string]RPCPolicy, method string) (RPCPolicy, bool) {
	if len(methods) == 0 {
		return RPCPolicy{}, false
	}
	for _, candidate := range rpcMethodPolicyCandidates(method) {
		if policy, ok := methods[candidate]; ok {
			return policy, true
		}
	}
	return RPCPolicy{}, false
}

func rpcMethodPolicyCandidates(method string) []string {
	trimmed := strings.Trim(strings.TrimSpace(method), "/")
	service, rpcMethod := splitRPCMethod(trimmed)
	candidates := make([]string, 0, 4)
	if service != "" && rpcMethod != "" {
		candidates = append(candidates, service+"/"+rpcMethod, "/"+service+"/"+rpcMethod)
	}
	if trimmed != "" {
		candidates = append(candidates, trimmed)
	}
	if rpcMethod != "" {
		candidates = append(candidates, rpcMethod)
	}
	return uniqueRPCMethodPolicyCandidates(candidates)
}

func uniqueRPCMethodPolicyCandidates(candidates []string) []string {
	if len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func (c *HTTPClient) governanceDecision(ctx context.Context, req governance.Request) governance.Decision {
	if c == nil || c.opts.rules == nil {
		return governance.Decision{}
	}
	if c.opts.manager != nil {
		return c.opts.manager.MatchContext(ctx, req)
	}
	return c.opts.rules.Match(req)
}

func (c *HTTPClient) rpcGovernanceRequest(ctx context.Context, method string) governance.Request {
	service, rpcMethod := splitRPCMethod(method)
	tags := cloneStringMap(c.opts.governanceTags)
	if tags == nil {
		tags = make(map[string]string, 2)
	}
	tags["rpc.service"] = service
	tags["rpc.method"] = rpcMethod
	return governance.Request{
		Transport: governance.TransportRPC,
		Service:   service,
		Method:    rpcMethod,
		Path:      "/" + strings.TrimPrefix(method, "/"),
		Tags:      tags,
		Headers:   metadataHeaderMap(ctx),
	}
}

func metadataHeaderMap(ctx context.Context) map[string]string {
	md, ok := metadata.FromContext(ctx)
	if !ok || len(md) == 0 {
		return nil
	}
	out := make(map[string]string, len(md)*2)
	for key, value := range md {
		out[key] = value
		out[http.CanonicalHeaderKey(key)] = value
	}
	return out
}

func (c *HTTPClient) ruleRateLimiter(key string, policy governance.RateLimitPolicy) *limit.Limiter {
	if c == nil || c.runtime == nil || policy.Rate <= 0 && policy.Burst <= 0 {
		return nil
	}
	rate := policy.Rate
	burst := policy.Burst
	if burst <= 0 {
		burst = rate
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	cached := c.runtime.rateLimits[key]
	if cached != nil && cached.rate == rate && cached.burst == burst {
		return cached.limiter
	}
	limiter := limit.New(rate, burst)
	c.runtime.rateLimits[key] = &cachedRateLimiter{rate: rate, burst: burst, limiter: limiter}
	return limiter
}

func (c *HTTPClient) ruleConcurrencyLimiter(key string, policy governance.ConcurrencyPolicy) *limit.ConcurrencyLimiter {
	if c == nil || c.runtime == nil || policy.Limit <= 0 {
		return nil
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	cached := c.runtime.concurrency[key]
	if cached != nil && cached.limit == policy.Limit {
		return cached.limiter
	}
	limiter := limit.NewConcurrency(policy.Limit)
	c.runtime.concurrency[key] = &cachedConcurrencyLimiter{limit: policy.Limit, limiter: limiter}
	return limiter
}

func (c *HTTPClient) ruleLoadShedderLimiter(key string, policy RPCLoadShedderPolicy) *limit.ConcurrencyLimiter {
	if c == nil || c.runtime == nil || !policy.Enabled {
		return nil
	}
	limitValue := policy.MaxConcurrency
	if limitValue <= 0 {
		limitValue = policy.MaxInflight
	}
	if limitValue <= 0 {
		return nil
	}
	return c.ruleConcurrencyLimiter(key+":rpc-policy-load-shedder", governance.ConcurrencyPolicy{Limit: limitValue})
}

func (c *HTTPClient) ruleBreaker(key string, policy governance.BreakerPolicy) *breaker.AdaptiveBreaker {
	if c == nil || c.runtime == nil || !policy.Enabled {
		return nil
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	cached := c.runtime.breakers[key]
	if cached != nil && cached.policy == policy {
		return cached.breaker
	}
	brk := adaptiveBreakerFromPolicy(policy)
	c.runtime.breakers[key] = &cachedBreaker{policy: policy, breaker: brk}
	return brk
}

func (c *HTTPClient) ruleBalancer(key string, policy RPCBalancerPolicy) Balancer {
	if c == nil || c.runtime == nil || strings.TrimSpace(policy.Name) == "" {
		return nil
	}
	signature := rpcBalancerPolicySignature(policy)
	cacheKey := key + ":rpc-policy-balancer"
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	cached := c.runtime.balancers[cacheKey]
	if cached != nil && cached.signature == signature {
		return cached.balancer
	}
	balancer := newRPCPolicyBalancer(policy)
	c.runtime.balancers[cacheKey] = &cachedPolicyBalancer{signature: signature, balancer: balancer}
	return balancer
}

func newRPCPolicyBalancer(policy RPCBalancerPolicy) Balancer {
	switch strings.TrimSpace(policy.Name) {
	case RPCBalancerWeightedRoundRobin:
		return NewWeightedRoundRobinBalancer(policy.Weights)
	case RPCBalancerP2C:
		return NewP2CBalancer()
	case RPCBalancerConsistentHash:
		return NewConsistentHashBalancer(WithConsistentHashKey(policy.Key))
	case RPCBalancerHealth:
		return NewHealthBalancer()
	case RPCBalancerRoundRobin:
		fallthrough
	default:
		return &RoundRobinBalancer{}
	}
}

func rpcBalancerPolicySignature(policy RPCBalancerPolicy) string {
	data, err := json.Marshal(policy)
	if err != nil {
		return policy.Name
	}
	return string(data)
}

func hedgeConfigFromRPCPolicy(policy RPCHedgePolicy) endpoint.HedgeConfig {
	maxHedges := 1
	if policy.Attempts > 1 {
		maxHedges = policy.Attempts - 1
	}
	return endpoint.HedgeConfig{Delay: policy.Delay, MaxHedges: maxHedges}
}

func applyGovernanceMetadata(ctx context.Context, values map[string]string) context.Context {
	if len(values) == 0 {
		return ctx
	}
	md, _ := metadata.FromContext(ctx)
	if md == nil {
		md = metadata.MD{}
	}
	for key, value := range values {
		md[key] = value
	}
	return metadata.NewContext(ctx, md)
}

func splitRPCMethod(method string) (string, string) {
	method = strings.Trim(method, "/")
	service, rpcMethod, ok := strings.Cut(method, "/")
	if !ok {
		return "", method
	}
	return service, rpcMethod
}

func (c *HTTPClient) postWithPolicy(ctx context.Context, method string, request any, policy RPCPolicy) (*callResult, error) {
	result, err := c.post(ctx, method, request, policy.Balancer, "")
	if err == nil || !policy.Fallback.Enabled || ctx.Err() != nil {
		return result, err
	}
	fallbackMethod := strings.TrimSpace(policy.Fallback.Method)
	if fallbackMethod == "" {
		fallbackMethod = method
	}
	return c.post(ctx, fallbackMethod, request, RPCBalancerPolicy{}, policy.Fallback.Target)
}

func (c *HTTPClient) post(ctx context.Context, method string, request any, policy RPCBalancerPolicy, fixedTarget string) (result *callResult, err error) {
	target, balancer, err := c.pickTarget(ctx, policy, fixedTarget)
	if err != nil {
		return nil, NewError(CodeUnavailable, err.Error())
	}
	defer c.reportEndpointWithBalancer(ctx, balancer, target, &err)
	payload, err := c.opts.codec.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	md, _ := metadata.FromContext(ctx)
	reqEnv := requestEnvelope{Metadata: md, Codec: c.opts.codec.Name()}
	if c.opts.codec.Name() == "json" {
		reqEnv.Payload = payload
	} else {
		reqEnv.PayloadBytes = payload
	}
	body, err := json.Marshal(reqEnv)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target+"/rpc/"+strings.TrimPrefix(method, "/"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, normalizeContextError(ctx, err)
		}
		return nil, NewError(CodeUnavailable, "send request: "+err.Error())
	}
	defer resp.Body.Close()
	var env rawResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if env.Error != "" {
		return nil, NewError(env.Code, env.Error)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return nil, NewError(CodeUnavailable, fmt.Sprintf("rpc status %d", resp.StatusCode))
	}
	payload = env.Payload
	if len(env.PayloadBytes) > 0 {
		payload = env.PayloadBytes
	}
	return &callResult{Payload: append(json.RawMessage(nil), payload...), Metadata: env.Metadata}, nil
}

func (c *HTTPClient) unmarshalResponsePayload(payload json.RawMessage, response any) error {
	if response == nil {
		return nil
	}
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	if err := c.opts.codec.Unmarshal(payload, response); err != nil {
		return fmt.Errorf("unmarshal response payload: %w", err)
	}
	return nil
}

func (c *HTTPClient) singleflightKey(ctx context.Context, method string, request any) (string, error) {
	if c.opts.singleflightKey != nil {
		return c.opts.singleflightKey(ctx, method, request)
	}
	payload, err := c.opts.codec.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("marshal singleflight key: %w", err)
	}
	md, _ := metadata.FromContext(ctx)
	key, err := json.Marshal(struct {
		Method   string      `json:"method"`
		Payload  []byte      `json:"payload"`
		Metadata metadata.MD `json:"metadata,omitempty"`
	}{Method: method, Payload: payload, Metadata: md})
	if err != nil {
		return "", fmt.Errorf("marshal singleflight key envelope: %w", err)
	}
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:]), nil
}

func cloneCallResult(result *callResult) *callResult {
	if result == nil {
		return nil
	}
	return &callResult{Payload: append(json.RawMessage(nil), result.Payload...), Metadata: result.Metadata.Clone()}
}

func (c *HTTPClient) reportEndpoint(ctx context.Context, target string, err *error) {
	c.reportEndpointWithBalancer(ctx, c.opts.balancer, target, err)
}

func (c *HTTPClient) reportEndpointWithBalancer(ctx context.Context, balancer Balancer, target string, err *error) {
	reporter, ok := balancer.(EndpointReporter)
	if !ok {
		return
	}
	if err == nil {
		reporter.Report(ctx, target, nil)
		return
	}
	reporter.Report(ctx, target, *err)
}

func (c *HTTPClient) pickTarget(ctx context.Context, policy RPCBalancerPolicy, fixedTarget string) (string, Balancer, error) {
	fixedTarget = strings.TrimRight(strings.TrimSpace(fixedTarget), "/")
	if fixedTarget != "" {
		return fixedTarget, nil, nil
	}
	endpoints, err := c.opts.resolver.Resolve(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("resolve rpc target: %w", err)
	}
	balancer := c.opts.balancer
	if policyBalancer := c.ruleBalancer("client", policy); policyBalancer != nil {
		balancer = policyBalancer
	}
	if balancer == nil {
		balancer = &RoundRobinBalancer{}
	}
	if strings.TrimSpace(policy.Key) != "" && strings.TrimSpace(policy.Name) == RPCBalancerConsistentHash {
		ctx = ContextWithHashKey(ctx, rpcPolicyHashKey(ctx, policy.Key))
	}
	target, err := balancer.Pick(ctx, endpoints)
	if err != nil {
		return "", balancer, fmt.Errorf("pick rpc target: %w", err)
	}
	return strings.TrimRight(target, "/"), balancer, nil
}

func (c *HTTPClient) startResolverWatch() {
	if c == nil || c.opts.resolver == nil {
		return
	}
	watcher, ok := c.opts.resolver.(WatchResolver)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	updates, err := watcher.Watch(ctx)
	if err != nil {
		cancel()
		c.discovery.recordWatchError(err)
		return
	}
	c.watchCancel = cancel
	initial, err := watcher.Resolve(context.Background())
	if err != nil {
		c.discovery.recordWatchError(err)
	}
	c.discovery.recordUpdate(initial, nil)
	go c.watchResolverUpdates(ctx, normalizeEndpoints(initial), updates)
}

func (c *HTTPClient) watchResolverUpdates(ctx context.Context, previous []string, updates <-chan []string) {
	for {
		select {
		case <-ctx.Done():
			return
		case endpoints, ok := <-updates:
			if !ok {
				return
			}
			endpoints = normalizeEndpoints(endpoints)
			removed := removedEndpoints(previous, endpoints)
			c.discovery.recordUpdate(endpoints, removed)
			if len(removed) > 0 {
				c.closeIdleConnections()
				c.discovery.recordCloseIdle()
			}
			previous = endpoints
		}
	}
}

func (c *HTTPClient) closeIdleConnections() {
	if c == nil || c.hc == nil {
		return
	}
	c.hc.CloseIdleConnections()
}

func removedEndpoints(previous []string, current []string) []string {
	if len(previous) == 0 {
		return nil
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, endpoint := range current {
		currentSet[endpoint] = struct{}{}
	}
	removed := make([]string, 0)
	for _, endpoint := range previous {
		if _, ok := currentSet[endpoint]; !ok {
			removed = append(removed, endpoint)
		}
	}
	return removed
}

func rpcPolicyHashKey(ctx context.Context, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if md, ok := metadata.FromContext(ctx); ok {
		if value := md.Get(key); value != "" {
			return value
		}
	}
	return key
}
