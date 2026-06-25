// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/limit"
	coreretry "github.com/imajinyun/gofly/core/retry"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type GovernanceOption func(*governanceOptions)

type governanceOptions struct {
	service     string
	tags        map[string]string
	breakers    sync.Map
	rateLimits  sync.Map
	concurrency sync.Map
}

type cachedRateLimiter struct {
	rate    int
	burst   int
	limiter *limit.Limiter
}

type cachedConcurrencyLimiter struct {
	limit   int
	limiter *limit.ConcurrencyLimiter
}

func WithGovernanceService(service string) GovernanceOption {
	return func(o *governanceOptions) { o.service = service }
}

func WithGovernanceTags(tags map[string]string) GovernanceOption {
	return func(o *governanceOptions) {
		o.tags = make(map[string]string, len(tags))
		for key, value := range tags {
			o.tags[key] = value
		}
	}
}

func GovernanceUnaryServerInterceptor(rules *governance.RuleSet, opts ...GovernanceOption) stdgrpc.UnaryServerInterceptor {
	o := newGovernanceOptions(opts...)
	return func(ctx context.Context, req any, info *stdgrpc.UnaryServerInfo, handler stdgrpc.UnaryHandler) (any, error) {
		request := governanceRequest(info.FullMethod, o)
		request.Headers = incomingMetadataMap(ctx)
		decision := rules.Match(request)
		runtimeKey := governanceRuntimeKey(decision, info.FullMethod)
		if limiter := o.rateLimiter(runtimeKey, decision.Policy.RateLimit); limiter != nil && !limiter.Allow() {
			return nil, coreerrors.GRPCError(coreerrors.New(coreerrors.CodeResourceExhausted, "too many requests"))
		}
		if limiter := o.concurrencyLimiter(runtimeKey, decision.Policy.Concurrency); limiter != nil {
			if !limiter.TryAcquire() {
				return nil, coreerrors.GRPCError(coreerrors.New(coreerrors.CodeUnavailable, "too many concurrent requests"))
			}
			defer limiter.Release()
		}
		ctx = applyIncomingMetadata(ctx, canaryMetadata(decision.Policy.Canary, request))
		ctx = applyIncomingMetadata(ctx, decision.Policy.Metadata)
		ctx, cancel := withPolicyTimeout(ctx, decision.Policy.Timeout)
		defer cancel()
		if decision.Policy.Breaker.Enabled {
			cb := o.breaker(decision.RuleKey, decision.Policy.Breaker)
			var resp any
			err := cb.Do(ctx, func() error {
				var err error
				resp, err = handler(ctx, req)
				return err
			})
			return resp, grpcGovernanceError(err)
		}
		return handler(ctx, req)
	}
}

func GovernanceUnaryClientInterceptor(rules *governance.RuleSet, opts ...GovernanceOption) stdgrpc.UnaryClientInterceptor {
	o := newGovernanceOptions(opts...)
	return func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, invoker stdgrpc.UnaryInvoker, callOpts ...stdgrpc.CallOption) error {
		request := governanceRequest(method, o)
		request.Headers = outgoingMetadataMap(ctx)
		decision := rules.Match(request)
		runtimeKey := governanceRuntimeKey(decision, method)
		if limiter := o.rateLimiter(runtimeKey, decision.Policy.RateLimit); limiter != nil && !limiter.Allow() {
			return coreerrors.GRPCError(coreerrors.New(coreerrors.CodeResourceExhausted, "too many requests"))
		}
		if limiter := o.concurrencyLimiter(runtimeKey, decision.Policy.Concurrency); limiter != nil {
			if !limiter.TryAcquire() {
				return coreerrors.GRPCError(coreerrors.New(coreerrors.CodeUnavailable, "too many concurrent requests"))
			}
			defer limiter.Release()
		}
		ctx = applyOutgoingMetadata(ctx, canaryMetadata(decision.Policy.Canary, request))
		ctx = applyOutgoingMetadata(ctx, decision.Policy.Metadata)
		ctx, cancel := withPolicyTimeout(ctx, decision.Policy.Timeout)
		defer cancel()
		call := func() error {
			if decision.Policy.Breaker.Enabled {
				cb := o.breaker(runtimeKey, decision.Policy.Breaker)
				return cb.Do(ctx, func() error { return invoker(ctx, method, req, reply, cc, callOpts...) })
			}
			return invoker(ctx, method, req, reply, cc, callOpts...)
		}
		if decision.Policy.Retry.Attempts > 1 {
			return grpcGovernanceError(coreretry.Do(ctx, retryPolicy(decision.Policy.Retry), call))
		}
		return grpcGovernanceError(call())
	}
}

func GovernanceStreamServerInterceptor(rules *governance.RuleSet, opts ...GovernanceOption) stdgrpc.StreamServerInterceptor {
	o := newGovernanceOptions(opts...)
	return func(srv any, stream stdgrpc.ServerStream, info *stdgrpc.StreamServerInfo, handler stdgrpc.StreamHandler) error {
		ctx := stream.Context()
		request := governanceRequest(info.FullMethod, o)
		request.Headers = incomingMetadataMap(ctx)
		decision := rules.Match(request)
		runtimeKey := governanceRuntimeKey(decision, info.FullMethod)
		if limiter := o.rateLimiter(runtimeKey, decision.Policy.RateLimit); limiter != nil && !limiter.Allow() {
			return coreerrors.GRPCError(coreerrors.New(coreerrors.CodeResourceExhausted, "too many requests"))
		}
		if limiter := o.concurrencyLimiter(runtimeKey, decision.Policy.Concurrency); limiter != nil {
			if !limiter.TryAcquire() {
				return coreerrors.GRPCError(coreerrors.New(coreerrors.CodeUnavailable, "too many concurrent streams"))
			}
			defer limiter.Release()
		}
		ctx = applyIncomingMetadata(ctx, canaryMetadata(decision.Policy.Canary, request))
		ctx = applyIncomingMetadata(ctx, decision.Policy.Metadata)
		ctx, cancel := withPolicyTimeout(ctx, decision.Policy.Timeout)
		defer cancel()

		call := func() error {
			return handler(srv, &governanceServerStream{ServerStream: stream, ctx: ctx})
		}
		if decision.Policy.Breaker.Enabled {
			cb := o.breaker(runtimeKey, decision.Policy.Breaker)
			return grpcGovernanceError(cb.Do(ctx, call))
		}
		return call()
	}
}

func GovernanceStreamClientInterceptor(rules *governance.RuleSet, opts ...GovernanceOption) stdgrpc.StreamClientInterceptor {
	o := newGovernanceOptions(opts...)
	return func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, streamer stdgrpc.Streamer, callOpts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		request := governanceRequest(method, o)
		request.Headers = outgoingMetadataMap(ctx)
		decision := rules.Match(request)
		runtimeKey := governanceRuntimeKey(decision, method)
		if limiter := o.rateLimiter(runtimeKey, decision.Policy.RateLimit); limiter != nil && !limiter.Allow() {
			return nil, coreerrors.GRPCError(coreerrors.New(coreerrors.CodeResourceExhausted, "too many requests"))
		}
		releaseConcurrency := func() {}
		if limiter := o.concurrencyLimiter(runtimeKey, decision.Policy.Concurrency); limiter != nil {
			if !limiter.TryAcquire() {
				return nil, coreerrors.GRPCError(coreerrors.New(coreerrors.CodeUnavailable, "too many concurrent streams"))
			}
			releaseConcurrency = limiter.Release
		}
		ctx = applyOutgoingMetadata(ctx, canaryMetadata(decision.Policy.Canary, request))
		ctx = applyOutgoingMetadata(ctx, decision.Policy.Metadata)
		ctx, cancel := withPolicyTimeout(ctx, decision.Policy.Timeout)

		call := func() (stdgrpc.ClientStream, error) {
			if decision.Policy.Breaker.Enabled {
				cb := o.breaker(runtimeKey, decision.Policy.Breaker)
				var stream stdgrpc.ClientStream
				err := cb.Do(ctx, func() error {
					var err error
					stream, err = streamer(ctx, desc, cc, method, callOpts...)
					return err
				})
				return stream, err
			}
			return streamer(ctx, desc, cc, method, callOpts...)
		}

		var stream stdgrpc.ClientStream
		var err error
		if decision.Policy.Retry.Attempts > 1 {
			err = coreretry.Do(ctx, retryPolicy(decision.Policy.Retry), func() error {
				stream, err = call()
				return err
			})
		} else {
			stream, err = call()
		}
		if err != nil {
			cancel()
			releaseConcurrency()
			return nil, grpcGovernanceError(err)
		}
		return &governanceClientStream{ClientStream: stream, cancel: cancel, release: releaseConcurrency}, nil
	}
}

type governanceServerStream struct {
	stdgrpc.ServerStream
	ctx context.Context
}

func (s *governanceServerStream) Context() context.Context { return s.ctx }

type governanceClientStream struct {
	stdgrpc.ClientStream
	cancel  context.CancelFunc
	release func()
	once    sync.Once
}

func (s *governanceClientStream) finish() {
	s.once.Do(func() {
		if s.release != nil {
			s.release()
		}
		s.cancel()
	})
}

func (s *governanceClientStream) CloseSend() error {
	err := s.ClientStream.CloseSend()
	s.finish()
	return err
}

func (s *governanceClientStream) RecvMsg(m any) error {
	err := s.ClientStream.RecvMsg(m)
	if err != nil {
		s.finish()
	}
	return err
}

func (s *governanceClientStream) SendMsg(m any) error {
	err := s.ClientStream.SendMsg(m)
	if err != nil {
		s.finish()
	}
	return err
}

func newGovernanceOptions(opts ...GovernanceOption) *governanceOptions {
	o := &governanceOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

func governanceRuntimeKey(decision governance.Decision, fallback string) string {
	if decision.RuleKey != "" {
		return decision.RuleKey
	}
	if decision.RuleName != "" {
		return "name:" + decision.RuleName
	}
	return fallback
}

func governanceRequest(fullMethod string, o *governanceOptions) governance.Request {
	service, method := splitFullMethod(fullMethod)
	if o.service != "" {
		service = o.service
	}
	return governance.Request{Transport: governance.TransportRPC, Service: service, Method: method, Path: fullMethod, Tags: o.tags}
}

func splitFullMethod(fullMethod string) (string, string) {
	fullMethod = strings.Trim(fullMethod, "/")
	if fullMethod == "" {
		return "", ""
	}
	parts := strings.Split(fullMethod, "/")
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[len(parts)-2], parts[len(parts)-1]
}

func (o *governanceOptions) breaker(key string, policy governance.BreakerPolicy) *breaker.Breaker {
	if key == "" {
		key = "default"
	}
	value, _ := o.breakers.LoadOrStore(key, breaker.New(breaker.WithOpenTimeout(policy.OpenTimeout)))
	return value.(*breaker.Breaker)
}

func (o *governanceOptions) rateLimiter(key string, policy governance.RateLimitPolicy) *limit.Limiter {
	if policy.Rate <= 0 && policy.Burst <= 0 {
		return nil
	}
	if key == "" {
		key = "default"
	}
	rate := policy.Rate
	burst := policy.Burst
	if burst <= 0 {
		burst = rate
	}
	if cached, ok := o.rateLimits.Load(key); ok {
		entry := cached.(*cachedRateLimiter)
		if entry.rate == rate && entry.burst == burst {
			return entry.limiter
		}
	}
	entry := &cachedRateLimiter{rate: rate, burst: burst, limiter: limit.New(rate, burst)}
	actual, _ := o.rateLimits.LoadOrStore(key, entry)
	stored := actual.(*cachedRateLimiter)
	if stored.rate != rate || stored.burst != burst {
		o.rateLimits.Store(key, entry)
		return entry.limiter
	}
	return stored.limiter
}

func (o *governanceOptions) concurrencyLimiter(key string, policy governance.ConcurrencyPolicy) *limit.ConcurrencyLimiter {
	if policy.Limit <= 0 {
		return nil
	}
	if key == "" {
		key = "default"
	}
	if cached, ok := o.concurrency.Load(key); ok {
		entry := cached.(*cachedConcurrencyLimiter)
		if entry.limit == policy.Limit {
			return entry.limiter
		}
	}
	entry := &cachedConcurrencyLimiter{limit: policy.Limit, limiter: limit.NewConcurrency(policy.Limit)}
	actual, _ := o.concurrency.LoadOrStore(key, entry)
	stored := actual.(*cachedConcurrencyLimiter)
	if stored.limit != policy.Limit {
		o.concurrency.Store(key, entry)
		return entry.limiter
	}
	return stored.limiter
}

func withPolicyTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func retryPolicy(policy governance.RetryPolicy) coreretry.Policy {
	return coreretry.Policy{Attempts: policy.Attempts, Backoff: policy.Backoff, ShouldRetry: func(err error) bool {
		if len(policy.Statuses) == 0 {
			return defaultRetryable(err)
		}
		code := int(coreerrors.GRPCStatus(coreerrors.CodeOf(err)))
		for _, retryCode := range policy.Statuses {
			if retryCode == code {
				return true
			}
		}
		return false
	}}
}

func applyIncomingMetadata(ctx context.Context, values map[string]string) context.Context {
	if len(values) == 0 {
		return ctx
	}
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	for key, value := range values {
		md.Set(key, value)
	}
	return metadata.NewIncomingContext(ctx, md)
}

func applyOutgoingMetadata(ctx context.Context, values map[string]string) context.Context {
	if len(values) == 0 {
		return ctx
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	for key, value := range values {
		md.Set(key, value)
	}
	return metadata.NewOutgoingContext(ctx, md)
}

func incomingMetadataMap(ctx context.Context) map[string]string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	return metadataMap(md)
}

func outgoingMetadataMap(ctx context.Context) map[string]string {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return nil
	}
	return metadataMap(md)
}

func metadataMap(md metadata.MD) map[string]string {
	if len(md) == 0 {
		return nil
	}
	out := make(map[string]string, len(md))
	for key, values := range md {
		if len(values) > 0 {
			out[key] = values[0]
			out[http.CanonicalHeaderKey(key)] = values[0]
		}
	}
	return out
}

func canaryMetadata(policy governance.CanaryPolicy, req governance.Request) map[string]string {
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

func grpcGovernanceError(err error) error {
	if err == nil {
		return nil
	}
	if err == breaker.ErrOpen {
		return coreerrors.GRPCError(coreerrors.New(coreerrors.CodeUnavailable, err.Error()))
	}
	return coreerrors.GRPCError(err)
}
