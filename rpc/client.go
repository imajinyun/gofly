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
	target  string
	hc      *http.Client
	opts    clientOptions
	runtime *ruleRuntime
}

func NewClient(target string, opts ...ClientOption) (*HTTPClient, error) {
	if target == "" {
		return nil, errors.New("target is required")
	}
	o := clientOptions{codec: JSONCodec{}, timeout: 3 * time.Second, retry: 1, balancer: &RoundRobinBalancer{}}
	for _, opt := range opts {
		opt(&o)
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
	return &HTTPClient{target: strings.TrimRight(target, "/"), hc: hc, opts: o, runtime: newRuleRuntime()}, nil
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
	ctx = applyGovernanceMetadata(ctx, canaryMetadata(policy.Canary, governanceReq))
	ctx = applyGovernanceMetadata(ctx, policy.Metadata)
	timeout := c.opts.timeout
	if policy.Timeout > 0 {
		timeout = policy.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if c.opts.singleflight != nil {
		key, err := c.singleflightKey(ctx, method, request)
		if err != nil {
			return nil, err
		}
		result, _, err := c.opts.singleflight.Do(ctx, key, func(ctx context.Context) (*callResult, error) {
			return c.doCallOnce(ctx, method, request, policy, runtimeKey)
		})
		return cloneCallResult(result), err
	}
	return c.doCallOnce(ctx, method, request, policy, runtimeKey)
}

func (c *HTTPClient) doCallOnce(ctx context.Context, method string, request any, policy governance.Policy, runtimeKey string) (*callResult, error) {
	var result *callResult
	call := func() error {
		if c.opts.adaptive != nil {
			if err := c.opts.adaptive.Allow(); err != nil {
				return err
			}
			var err error
			result, err = c.post(ctx, method, request)
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
				result, err = c.post(ctx, method, request)
				return err
			})
		}
		var err error
		result, err = c.post(ctx, method, request)
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

func (c *HTTPClient) post(ctx context.Context, method string, request any) (result *callResult, err error) {
	target, err := c.pickTarget(ctx)
	if err != nil {
		return nil, NewError(CodeUnavailable, err.Error())
	}
	defer c.reportEndpoint(ctx, target, &err)
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
	reporter, ok := c.opts.balancer.(EndpointReporter)
	if !ok {
		return
	}
	if err == nil {
		reporter.Report(ctx, target, nil)
		return
	}
	reporter.Report(ctx, target, *err)
}

func (c *HTTPClient) pickTarget(ctx context.Context) (string, error) {
	endpoints, err := c.opts.resolver.Resolve(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve rpc target: %w", err)
	}
	balancer := c.opts.balancer
	if balancer == nil {
		balancer = &RoundRobinBalancer{}
	}
	target, err := balancer.Pick(ctx, endpoints)
	if err != nil {
		return "", fmt.Errorf("pick rpc target: %w", err)
	}
	return strings.TrimRight(target, "/"), nil
}
