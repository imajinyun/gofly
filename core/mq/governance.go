// Package mq provides message queue abstractions and governance (circuit
// breaker, rate limit, retry, trace) for Kafka, RabbitMQ and Redis Streams.
package mq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	core "github.com/imajinyun/gofly/core"
	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/observability"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/core/observability/trace"
	"github.com/imajinyun/gofly/core/retry"
)

const (
	mqMethodPublish = "PUBLISH"
	mqMethodConsume = "CONSUME"
)

// GovernanceOption customises MQ governance wiring.
type GovernanceOption func(*governanceOptions)

type governanceOptions struct {
	service      string
	tags         map[string]string
	manager      *governance.Manager
	rules        *governance.RuleSet
	registry     *metrics.Registry
	trace        bool
	traceSampler trace.Sampler
	log          bool
	logSampler   trace.Sampler
	timeout      time.Duration
	retryPolicy  retry.Policy
	breaker      *breaker.AdaptiveBreaker
	runtime      *mqRuleRuntime
}

type GovernanceBroker struct {
	next Broker
	opts governanceOptions
}

type mqRuleRuntime struct {
	mu          sync.Mutex
	rateLimits  map[string]*mqCachedRateLimiter
	concurrency map[string]*mqCachedConcurrencyLimiter
	breakers    map[string]*mqCachedBreaker
}

type mqCachedRateLimiter struct {
	rate    int
	burst   int
	limiter *limit.Limiter
}

type mqCachedConcurrencyLimiter struct {
	limit   int
	limiter *limit.ConcurrencyLimiter
}

type mqCachedBreaker struct {
	policy  governance.BreakerPolicy
	breaker *breaker.AdaptiveBreaker
}

var _ Broker = (*GovernanceBroker)(nil)

func NewGovernanceBroker(next Broker, opts ...GovernanceOption) (*GovernanceBroker, error) {
	if next == nil {
		return nil, errors.New("mq governance broker is nil")
	}
	o := governanceOptions{service: "mq", registry: metrics.Default, runtime: newMQRuleRuntime()}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.runtime == nil {
		o.runtime = newMQRuleRuntime()
	}
	return &GovernanceBroker{next: next, opts: o}, nil
}

func WithGovernanceService(service string) GovernanceOption {
	return func(o *governanceOptions) {
		if service != "" {
			o.service = service
		}
	}
}

func WithGovernanceTags(tags map[string]string) GovernanceOption {
	return func(o *governanceOptions) { o.tags = cloneStringMap(tags) }
}

func WithGovernanceRuleSet(rules *governance.RuleSet) GovernanceOption {
	return func(o *governanceOptions) { o.rules = rules }
}

func WithGovernanceManager(manager *governance.Manager) GovernanceOption {
	return func(o *governanceOptions) {
		o.manager = manager
		if manager != nil {
			o.rules = manager.RuleSet()
		}
	}
}

func WithGovernanceSuite(suite *governance.Suite) GovernanceOption {
	return func(o *governanceOptions) {
		if o.manager != nil {
			return
		}
		if suite != nil {
			o.rules = governance.MergeRuleSets(o.rules, suite.RuleSet())
		}
	}
}

func WithGovernanceMetrics(registry *metrics.Registry) GovernanceOption {
	return func(o *governanceOptions) {
		if registry != nil {
			o.registry = registry
		}
	}
}

func WithGovernanceTrace(enabled bool) GovernanceOption {
	return func(o *governanceOptions) { o.trace = enabled }
}

func WithGovernanceTraceSampler(sampler trace.Sampler) GovernanceOption {
	return func(o *governanceOptions) { o.traceSampler = sampler }
}

func WithGovernanceLog(enabled bool) GovernanceOption {
	return func(o *governanceOptions) { o.log = enabled }
}

func WithGovernanceLogSampler(sampler trace.Sampler) GovernanceOption {
	return func(o *governanceOptions) { o.logSampler = sampler }
}

func WithGovernanceTimeout(timeout time.Duration) GovernanceOption {
	return func(o *governanceOptions) {
		if timeout > 0 {
			o.timeout = timeout
		}
	}
}

func WithGovernanceRetryPolicy(policy retry.Policy) GovernanceOption {
	return func(o *governanceOptions) { o.retryPolicy = policy }
}

func WithGovernanceBreaker(brk *breaker.AdaptiveBreaker) GovernanceOption {
	return func(o *governanceOptions) { o.breaker = brk }
}

func (b *GovernanceBroker) Publish(ctx context.Context, msg Message) error {
	ctx = core.Context(ctx)
	msg = cloneMessage(msg)
	msg.Headers = b.mergeContextMetadata(ctx, msg.Headers)
	req := b.request(mqMethodPublish, msg.Topic, "", msg.Headers)
	decision := b.decision(req)
	policy := decision.Policy
	msg.Headers = mergeHeaders(msg.Headers, canaryHeaders(policy.Canary, req), policy.Headers, policy.Metadata)
	ctx = b.prepareContext(ctx, mqMethodPublish, msg.Topic, "", msg.Headers)
	if b.opts.trace {
		var sc trace.SpanContext
		ctx, sc = observability.StartTrace(ctx, msg.Headers[trace.TraceParentHeader], b.operationName(mqMethodPublish, msg.Topic, ""), b.opts.traceSampler)
		msg.Headers = mergeHeaders(msg.Headers, map[string]string{
			trace.TraceParentHeader: trace.TraceParent(sc),
			trace.TraceIDKey:        sc.TraceID,
			trace.SpanIDKey:         sc.SpanID,
		})
	}
	runtimeKey := mqGovernanceRuntimeKey(decision, mqMethodPublish, msg.Topic, "")
	start := time.Now()
	err := b.run(ctx, runtimeKey, policy, mqMethodPublish, msg.Topic, "", func(ctx context.Context) error {
		return b.next.Publish(ctx, msg)
	})
	b.record(mqMethodPublish, msg.Topic, "", time.Since(start), err)
	b.log(ctx, mqMethodPublish, msg.Topic, "", err)
	return err
}

func (b *GovernanceBroker) Subscribe(ctx context.Context, topic, group string, handler Handler, opts ...SubscribeOption) (Unsubscriber, error) {
	if handler == nil {
		return nil, errors.New("message handler is nil")
	}
	wrapped := func(ctx context.Context, msg Message) error {
		return b.consume(ctx, topic, group, handler, msg)
	}
	return b.next.Subscribe(ctx, topic, group, wrapped, opts...)
}

func (b *GovernanceBroker) Close(ctx context.Context) error {
	return b.next.Close(ctx)
}

func (b *GovernanceBroker) Snapshot() BrokerStats {
	if b == nil || b.next == nil {
		return BrokerStats{}
	}
	snapshotter, ok := b.next.(interface{ Snapshot() BrokerStats })
	if !ok {
		return BrokerStats{}
	}
	return snapshotter.Snapshot()
}

func (b *GovernanceBroker) consume(ctx context.Context, topic, group string, handler Handler, msg Message) error {
	ctx = core.Context(ctx)
	msg = cloneMessage(msg)
	req := b.request(mqMethodConsume, topic, group, msg.Headers)
	decision := b.decision(req)
	policy := decision.Policy
	msg.Headers = mergeHeaders(msg.Headers, canaryHeaders(policy.Canary, req), policy.Headers, policy.Metadata)
	ctx = b.prepareContext(ctx, mqMethodConsume, topic, group, msg.Headers)
	if b.opts.trace {
		var sc trace.SpanContext
		ctx, sc = observability.StartTrace(ctx, msg.Headers[trace.TraceParentHeader], b.operationName(mqMethodConsume, topic, group), b.opts.traceSampler)
		msg.Headers = mergeHeaders(msg.Headers, map[string]string{
			trace.TraceParentHeader: trace.TraceParent(sc),
			trace.TraceIDKey:        sc.TraceID,
			trace.SpanIDKey:         sc.SpanID,
		})
		ctx = b.prepareContext(ctx, mqMethodConsume, topic, group, msg.Headers)
	}
	runtimeKey := mqGovernanceRuntimeKey(decision, mqMethodConsume, topic, group)
	start := time.Now()
	err := b.run(ctx, runtimeKey, policy, mqMethodConsume, topic, group, func(ctx context.Context) error {
		return handler(ctx, cloneMessage(msg))
	})
	b.record(mqMethodConsume, topic, group, time.Since(start), err)
	b.log(ctx, mqMethodConsume, topic, group, err)
	return err
}

func (b *GovernanceBroker) run(ctx context.Context, runtimeKey string, policy governance.Policy, method, topic, group string, fn func(context.Context) error) error {
	if limiter := b.ruleRateLimiter(runtimeKey, policy.RateLimit); limiter != nil && !limiter.Allow() {
		return fmt.Errorf("%w: mq %s %s too many requests", ErrOverloaded, method, topic)
	}
	if limiter := b.ruleConcurrencyLimiter(runtimeKey, policy.Concurrency); limiter != nil {
		if !limiter.TryAcquire() {
			return fmt.Errorf("%w: mq %s %s too many concurrent operations", ErrOverloaded, method, topic)
		}
		defer limiter.Release()
	}
	timeout := b.opts.timeout
	if policy.Timeout > 0 {
		timeout = policy.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	call := func() error {
		if brk := b.ruleBreaker(runtimeKey, policy.Breaker); brk != nil {
			return brk.Do(ctx, func() error { return fn(ctx) })
		}
		if b.opts.breaker != nil {
			return b.opts.breaker.Do(ctx, func() error { return fn(ctx) })
		}
		return fn(ctx)
	}
	retryPolicy := b.effectiveRetry(policy.Retry)
	err := retry.Do(ctx, retryPolicy, call)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (b *GovernanceBroker) request(method, topic, group string, headers map[string]string) governance.Request {
	tags := cloneStringMap(b.opts.tags)
	if tags == nil {
		tags = make(map[string]string, 3)
	}
	tags["mq.topic"] = topic
	if group != "" {
		tags["mq.group"] = group
	}
	return governance.Request{
		Transport: governance.TransportMQ,
		Service:   b.opts.service,
		Method:    method,
		Path:      "/" + topic,
		Tags:      tags,
		Headers:   canonicalHeaders(headers),
	}
}

func (b *GovernanceBroker) decision(req governance.Request) governance.Decision {
	if b == nil || b.opts.rules == nil {
		return governance.Decision{}
	}
	return b.opts.rules.Match(req)
}

func (b *GovernanceBroker) prepareContext(ctx context.Context, method, topic, group string, headers map[string]string) context.Context {
	md := metadata.MD(cloneStringMap(headers))
	if md == nil {
		md = metadata.MD{}
	}
	md["mq.method"] = method
	md["mq.topic"] = topic
	if group != "" {
		md["mq.group"] = group
	}
	return metadata.NewContext(ctx, md)
}

func (b *GovernanceBroker) mergeContextMetadata(ctx context.Context, headers map[string]string) map[string]string {
	md, ok := metadata.FromContext(ctx)
	if !ok || len(md) == 0 {
		return cloneStringMap(headers)
	}
	out := cloneStringMap(headers)
	if out == nil {
		out = make(map[string]string, len(md))
	}
	for key, value := range md {
		if _, exists := out[key]; !exists {
			out[key] = value
		}
	}
	return out
}

func (b *GovernanceBroker) effectiveRetry(policy governance.RetryPolicy) retry.Policy {
	retryPolicy := b.opts.retryPolicy
	if retryPolicy.Attempts <= 0 {
		retryPolicy.Attempts = 1
	}
	if policy.Attempts > 0 {
		retryPolicy.Attempts = policy.Attempts
	}
	if policy.Backoff > 0 {
		retryPolicy.Backoff = policy.Backoff
	}
	if retryPolicy.ShouldRetry == nil {
		retryPolicy.ShouldRetry = shouldRetryMQ
	}
	return retryPolicy
}

func (b *GovernanceBroker) record(method, topic, group string, duration time.Duration, err error) {
	registry := b.opts.registry
	if registry == nil {
		return
	}
	registry.Observe(b.operationName(method, topic, group), mqStatus(err), duration)
}

func (b *GovernanceBroker) log(ctx context.Context, method, topic, group string, err error) {
	if !b.opts.log {
		return
	}
	attrs := []any{"method", method, "topic", topic, "group", group, "status", mqStatus(err)}
	attrs = append(attrs, observability.TraceAttrs(ctx)...)
	if err != nil {
		attrs = append(attrs, "error", err.Error())
		slog.WarnContext(ctx, "mq operation", attrs...)
		return
	}
	if observability.ShouldLog(ctx, b.opts.logSampler, http.StatusOK) {
		slog.InfoContext(ctx, "mq operation", attrs...)
	}
}

func (b *GovernanceBroker) operationName(method, topic, group string) string {
	if group == "" {
		return "mq." + method + "." + topic
	}
	return "mq." + method + "." + topic + "." + group
}

func (b *GovernanceBroker) ruleRateLimiter(key string, policy governance.RateLimitPolicy) *limit.Limiter {
	if b == nil || b.opts.runtime == nil || policy.Rate <= 0 && policy.Burst <= 0 {
		return nil
	}
	rate := policy.Rate
	burst := policy.Burst
	if burst <= 0 {
		burst = rate
	}
	b.opts.runtime.mu.Lock()
	defer b.opts.runtime.mu.Unlock()
	cached := b.opts.runtime.rateLimits[key]
	if cached != nil && cached.rate == rate && cached.burst == burst {
		return cached.limiter
	}
	limiter := limit.New(rate, burst)
	b.opts.runtime.rateLimits[key] = &mqCachedRateLimiter{rate: rate, burst: burst, limiter: limiter}
	return limiter
}

func (b *GovernanceBroker) ruleConcurrencyLimiter(key string, policy governance.ConcurrencyPolicy) *limit.ConcurrencyLimiter {
	if b == nil || b.opts.runtime == nil || policy.Limit <= 0 {
		return nil
	}
	b.opts.runtime.mu.Lock()
	defer b.opts.runtime.mu.Unlock()
	cached := b.opts.runtime.concurrency[key]
	if cached != nil && cached.limit == policy.Limit {
		return cached.limiter
	}
	limiter := limit.NewConcurrency(policy.Limit)
	b.opts.runtime.concurrency[key] = &mqCachedConcurrencyLimiter{limit: policy.Limit, limiter: limiter}
	return limiter
}

func (b *GovernanceBroker) ruleBreaker(key string, policy governance.BreakerPolicy) *breaker.AdaptiveBreaker {
	if b == nil || b.opts.runtime == nil || !policy.Enabled {
		return nil
	}
	b.opts.runtime.mu.Lock()
	defer b.opts.runtime.mu.Unlock()
	cached := b.opts.runtime.breakers[key]
	if cached != nil && cached.policy == policy {
		return cached.breaker
	}
	brk := mqAdaptiveBreakerFromPolicy(policy)
	b.opts.runtime.breakers[key] = &mqCachedBreaker{policy: policy, breaker: brk}
	return brk
}

func newMQRuleRuntime() *mqRuleRuntime {
	return &mqRuleRuntime{
		rateLimits:  make(map[string]*mqCachedRateLimiter),
		concurrency: make(map[string]*mqCachedConcurrencyLimiter),
		breakers:    make(map[string]*mqCachedBreaker),
	}
}

func mqAdaptiveBreakerFromPolicy(policy governance.BreakerPolicy) *breaker.AdaptiveBreaker {
	opts := make([]breaker.AdaptiveOption, 0, 4)
	if policy.OpenTimeout > 0 {
		opts = append(opts, breaker.WithAdaptiveOpenTimeout(policy.OpenTimeout))
	}
	if policy.Window > 0 {
		opts = append(opts, breaker.WithAdaptiveWindow(policy.Window))
	}
	if policy.Buckets > 0 {
		opts = append(opts, breaker.WithAdaptiveBuckets(policy.Buckets))
	}
	if policy.MinRequests > 0 {
		opts = append(opts, breaker.WithAdaptiveMinRequests(policy.MinRequests))
	}
	if policy.FailureRatio > 0 {
		opts = append(opts, breaker.WithAdaptiveFailureRatio(policy.FailureRatio))
	}
	return breaker.NewAdaptive(opts...)
}

func mqGovernanceRuntimeKey(decision governance.Decision, method, topic, group string) string {
	if decision.RuleKey != "" {
		return decision.RuleKey
	}
	if decision.RuleName != "" {
		return "name:" + decision.RuleName
	}
	if group == "" {
		return method + ":" + topic
	}
	return method + ":" + topic + ":" + group
}

func mergeHeaders(base map[string]string, overlays ...map[string]string) map[string]string {
	out := cloneStringMap(base)
	for _, values := range overlays {
		for key, value := range values {
			if out == nil {
				out = make(map[string]string)
			}
			out[key] = value
		}
	}
	return out
}

func canaryHeaders(policy governance.CanaryPolicy, req governance.Request) map[string]string {
	decision := governance.SelectCanary(policy, req)
	if !decision.Selected {
		return nil
	}
	values := make(map[string]string, len(decision.Headers)+2)
	values[governance.HeaderCanary] = "true"
	if decision.Service != "" {
		values[governance.HeaderCanaryService] = decision.Service
	}
	for key, value := range decision.Headers {
		values[key] = value
	}
	return values
}

func canonicalHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers)*2)
	for key, value := range headers {
		out[key] = value
		out[http.CanonicalHeaderKey(key)] = value
	}
	return out
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

func shouldRetryMQ(err error) bool {
	return err != nil &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, ErrInvalidTopic) &&
		!errors.Is(err, ErrInvalidGroup) &&
		!errors.Is(err, ErrOverloaded) &&
		!errors.Is(err, ErrClosed) &&
		!errors.Is(err, breaker.ErrOpen)
}

func mqStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return 499
	}
	if errors.Is(err, ErrInvalidTopic) || errors.Is(err, ErrInvalidGroup) {
		return http.StatusBadRequest
	}
	if errors.Is(err, ErrOverloaded) {
		return http.StatusTooManyRequests
	}
	if errors.Is(err, breaker.ErrOpen) || errors.Is(err, ErrClosed) {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}
