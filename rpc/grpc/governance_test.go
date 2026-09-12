package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/discovery"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/observability/metrics"
	coretrace "github.com/imajinyun/gofly/core/observability/trace"
	coreretry "github.com/imajinyun/gofly/core/retry"
	coreruntime "github.com/imajinyun/gofly/core/runtime"

	oteltrace "go.opentelemetry.io/otel/trace"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestGovernanceUnaryClientInterceptorAppliesMetadataAndRetry(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "greeter retry",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy: governance.Policy{
			Retry:    governance.RetryPolicy{Attempts: 2, Backoff: time.Nanosecond, Statuses: []int{int(codes.Unavailable)}},
			Metadata: map[string]string{"x-governance": "enabled"},
		},
	})
	interceptor := GovernanceUnaryClientInterceptor(rules)
	attempts := 0
	err := interceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		attempts++
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok || md.Get("x-governance")[0] != "enabled" {
			t.Fatalf("metadata = %v, want x-governance", md)
		}
		if attempts == 1 {
			return status.Error(codes.Unavailable, "try again")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestAdaptiveLimitUnaryServerInterceptor(t *testing.T) {
	cpu := 950
	limiter := limit.NewAdaptiveLimiter(
		limit.WithAdaptiveLimits(1, 1),
		limit.WithAdaptiveInitialLimit(1),
		limit.WithAdaptiveCPUThreshold(900),
		limit.WithAdaptiveCPUReader(func() int { return cpu }),
	)
	interceptor := AdaptiveLimitUnaryServerInterceptor(limiter)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := interceptor(context.Background(), &struct{}{}, &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}, func(context.Context, any) (any, error) {
			close(entered)
			<-release
			return nil, nil
		})
		done <- err
	}()
	<-entered
	_, err := interceptor(context.Background(), &struct{}{}, &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}, func(context.Context, any) (any, error) {
		return nil, nil
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("adaptive rejection code = %s, want ResourceExhausted", status.Code(err))
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first call: %v", err)
	}
}

func TestGovernanceUnaryClientInterceptorAppliesCanaryMetadata(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "greeter canary",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy: governance.Policy{Canary: governance.CanaryPolicy{
			Ratio:        1,
			Service:      "greeter-gray",
			Headers:      map[string]string{"x-gray": "true"},
			MatchHeaders: map[string]string{"x-use-gray": "1"},
		}},
	})
	interceptor := GovernanceUnaryClientInterceptor(rules)
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-use-gray", "1")
	err := interceptor(ctx, "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok || md.Get(governance.HeaderCanary)[0] != "true" || md.Get(governance.HeaderCanaryService)[0] != "greeter-gray" || md.Get("x-gray")[0] != "true" {
			t.Fatalf("metadata = %v, want canary metadata", md)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestGovernanceUnaryServerInterceptorMapsOpenBreaker(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "server breaker",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy:    governance.Policy{Breaker: governance.BreakerPolicy{Enabled: true, OpenTimeout: time.Hour}},
	})
	interceptor := GovernanceUnaryServerInterceptor(rules)
	info := &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}
	for i := 0; i < 3; i++ {
		_, _ = interceptor(context.Background(), &struct{}{}, info, func(ctx context.Context, req any) (any, error) {
			return nil, errors.New("boom")
		})
	}
	_, err := interceptor(context.Background(), &struct{}{}, info, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %s, want unavailable", status.Code(err))
	}
}

func TestGovernanceUnaryServerInterceptorAppliesCanaryMetadata(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "server canary",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy: governance.Policy{Canary: governance.CanaryPolicy{
			Service:      "greeter-gray",
			Headers:      map[string]string{"x-gray": "true"},
			MatchHeaders: map[string]string{"X-Use-Gray": "1"},
		}},
	})
	interceptor := GovernanceUnaryServerInterceptor(rules)
	info := &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-use-gray", "1"))
	_, err := interceptor(ctx, &struct{}{}, info, func(ctx context.Context, req any) (any, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok || md.Get(governance.HeaderCanary)[0] != "true" || md.Get(governance.HeaderCanaryService)[0] != "greeter-gray" || md.Get("x-gray")[0] != "true" {
			t.Fatalf("metadata = %v, want canary metadata", md)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestGovernanceStreamClientInterceptorAppliesMetadataAndRetry(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "greeter stream retry",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "Subscribe",
		Policy: governance.Policy{
			Retry:    governance.RetryPolicy{Attempts: 2, Backoff: time.Nanosecond, Statuses: []int{int(codes.Unavailable)}},
			Metadata: map[string]string{"x-governance": "enabled"},
		},
	})
	interceptor := GovernanceStreamClientInterceptor(rules)
	attempts := 0
	stream, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		attempts++
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok || md.Get("x-governance")[0] != "enabled" {
			t.Fatalf("metadata = %v, want x-governance", md)
		}
		if attempts == 1 {
			return nil, status.Error(codes.Unavailable, "try again")
		}
		return &testClientStream{}, nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
	if stream == nil {
		t.Fatal("stream is nil")
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestGovernanceStreamClientInterceptorAppliesCanaryMetadata(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "greeter stream canary",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "Subscribe",
		Policy: governance.Policy{Canary: governance.CanaryPolicy{
			Ratio:        1,
			Service:      "greeter-gray",
			Headers:      map[string]string{"x-gray": "true"},
			MatchHeaders: map[string]string{"x-use-gray": "1"},
		}},
	})
	interceptor := GovernanceStreamClientInterceptor(rules)
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-use-gray", "1")
	_, err := interceptor(ctx, &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok || md.Get(governance.HeaderCanary)[0] != "true" || md.Get(governance.HeaderCanaryService)[0] != "greeter-gray" || md.Get("x-gray")[0] != "true" {
			t.Fatalf("metadata = %v, want canary metadata", md)
		}
		return &testClientStream{}, nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestGovernanceUnaryClientInterceptorEnforcesRateLimit(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client rate limit",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy:    governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}},
	})
	interceptor := GovernanceUnaryClientInterceptor(rules)
	calls := 0
	invoker := func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		calls++
		return nil
	}
	if err := interceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, invoker); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	err := interceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, invoker)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("code = %s, want resource exhausted", status.Code(err))
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestGovernanceUnaryClientInterceptorEnforcesConcurrency(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client concurrency",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy:    governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}},
	})
	interceptor := GovernanceUnaryClientInterceptor(rules)
	release := make(chan struct{})
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- interceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	err := interceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		return nil
	})
	close(release)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %s, want unavailable", status.Code(err))
	}
	if err := <-done; err != nil {
		t.Fatalf("first call error: %v", err)
	}
}

func TestGovernanceUnaryClientInterceptorMapsOpenBreaker(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client breaker",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy:    governance.Policy{Breaker: governance.BreakerPolicy{Enabled: true, OpenTimeout: time.Hour}},
	})
	interceptor := GovernanceUnaryClientInterceptor(rules)
	calls := 0
	fail := func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		calls++
		return status.Error(codes.Internal, "boom")
	}
	for i := 0; i < 3; i++ {
		_ = interceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, fail)
	}
	err := interceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		calls++
		return nil
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %s, want unavailable", status.Code(err))
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 before breaker opens", calls)
	}
}

func TestGovernanceUnaryClientInterceptorEnforcesTimeout(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client timeout",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy:    governance.Policy{Timeout: time.Millisecond},
	})
	interceptor := GovernanceUnaryClientInterceptor(rules)
	err := interceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("code = %s, want deadline exceeded", status.Code(err))
	}
}

func TestGovernanceStreamClientInterceptorEnforcesRateLimit(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "stream client rate limit",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "Subscribe",
		Policy:    governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}},
	})
	interceptor := GovernanceStreamClientInterceptor(rules)
	calls := 0
	streamer := func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		calls++
		return &testClientStream{}, nil
	}
	if _, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", streamer); err != nil {
		t.Fatalf("first stream error: %v", err)
	}
	_, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", streamer)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("code = %s, want resource exhausted", status.Code(err))
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestGovernanceStreamClientInterceptorEnforcesConcurrency(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "stream client concurrency",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "Subscribe",
		Policy:    governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}},
	})
	interceptor := GovernanceStreamClientInterceptor(rules)
	first, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		return &testClientStream{}, nil
	})
	if err != nil {
		t.Fatalf("first stream error: %v", err)
	}
	_, err = interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		return &testClientStream{}, nil
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %s, want unavailable", status.Code(err))
	}
	if err := first.CloseSend(); err != nil {
		t.Fatalf("close first stream: %v", err)
	}
	streamer := func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		return &fakeClientStream{ctx: ctx, recvErr: io.EOF}, nil
	}
	if _, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", streamer); status.Code(err) != codes.Unavailable {
		t.Fatalf("half-closed stream released concurrency: %v", err)
	}
	first.(*governanceClientStream).finish()
	third, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", streamer)
	if err != nil {
		t.Fatalf("third stream after release: %v", err)
	}
	if err := third.RecvMsg(nil); err != io.EOF {
		t.Fatalf("third receive = %v", err)
	}
}

func TestGovernanceStreamServerInterceptorMapsOpenBreaker(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "stream server breaker",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "Subscribe",
		Policy:    governance.Policy{Breaker: governance.BreakerPolicy{Enabled: true, OpenTimeout: time.Hour}},
	})
	interceptor := GovernanceStreamServerInterceptor(rules)
	info := &stdgrpc.StreamServerInfo{FullMethod: "/greeter.Greeter/Subscribe"}
	stream := testServerStream{ctx: context.Background()}
	for i := 0; i < 3; i++ {
		_ = interceptor(nil, stream, info, func(srv any, stream stdgrpc.ServerStream) error {
			return errors.New("boom")
		})
	}
	err := interceptor(nil, stream, info, func(srv any, stream stdgrpc.ServerStream) error {
		return nil
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %s, want unavailable", status.Code(err))
	}
}

func TestGovernanceStreamServerInterceptorAppliesCanaryMetadata(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "stream server canary",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "Subscribe",
		Policy: governance.Policy{Canary: governance.CanaryPolicy{
			Service:      "greeter-gray",
			Headers:      map[string]string{"x-gray": "true"},
			MatchHeaders: map[string]string{"X-Use-Gray": "1"},
		}},
	})
	interceptor := GovernanceStreamServerInterceptor(rules)
	info := &stdgrpc.StreamServerInfo{FullMethod: "/greeter.Greeter/Subscribe"}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-use-gray", "1"))
	err := interceptor(nil, testServerStream{ctx: ctx}, info, func(srv any, stream stdgrpc.ServerStream) error {
		md, ok := metadata.FromIncomingContext(stream.Context())
		if !ok || md.Get(governance.HeaderCanary)[0] != "true" || md.Get(governance.HeaderCanaryService)[0] != "greeter-gray" || md.Get("x-gray")[0] != "true" {
			t.Fatalf("metadata = %v, want canary metadata", md)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestGovernanceUnaryServerInterceptorEnforcesRateLimit(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "server rate limit",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy:    governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}},
	})
	interceptor := GovernanceUnaryServerInterceptor(rules)
	info := &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}
	handler := func(ctx context.Context, req any) (any, error) { return nil, nil }
	if _, err := interceptor(context.Background(), &struct{}{}, info, handler); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	_, err := interceptor(context.Background(), &struct{}{}, info, handler)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("code = %s, want resource exhausted", status.Code(err))
	}
}

func TestGovernanceUnaryServerInterceptorEnforcesConcurrency(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "server concurrency",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy:    governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}},
	})
	interceptor := GovernanceUnaryServerInterceptor(rules)
	info := &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}
	release := make(chan struct{})
	entered := make(chan struct{})
	go func() {
		_, _ = interceptor(context.Background(), &struct{}{}, info, func(ctx context.Context, req any) (any, error) {
			close(entered)
			<-release
			return nil, nil
		})
	}()
	<-entered
	_, err := interceptor(context.Background(), &struct{}{}, info, func(ctx context.Context, req any) (any, error) {
		return nil, nil
	})
	close(release)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %s, want unavailable", status.Code(err))
	}
}

func TestGovernanceStreamServerInterceptorEnforcesRateLimit(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "stream server rate limit",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "Subscribe",
		Policy:    governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}},
	})
	interceptor := GovernanceStreamServerInterceptor(rules)
	info := &stdgrpc.StreamServerInfo{FullMethod: "/greeter.Greeter/Subscribe"}
	stream := testServerStream{ctx: context.Background()}
	handler := func(srv any, stream stdgrpc.ServerStream) error { return nil }
	if err := interceptor(nil, stream, info, handler); err != nil {
		t.Fatalf("first call error: %v", err)
	}
	err := interceptor(nil, stream, info, handler)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("code = %s, want resource exhausted", status.Code(err))
	}
}

func TestGovernanceStreamServerInterceptorEnforcesConcurrency(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "stream server concurrency",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "Subscribe",
		Policy:    governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}},
	})
	interceptor := GovernanceStreamServerInterceptor(rules)
	info := &stdgrpc.StreamServerInfo{FullMethod: "/greeter.Greeter/Subscribe"}
	stream := testServerStream{ctx: context.Background()}
	release := make(chan struct{})
	entered := make(chan struct{})
	go func() {
		_ = interceptor(nil, stream, info, func(srv any, stream stdgrpc.ServerStream) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	err := interceptor(nil, stream, info, func(srv any, stream stdgrpc.ServerStream) error {
		return nil
	})
	close(release)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %s, want unavailable", status.Code(err))
	}
}

func TestNormalizeGRPCErrorConvertsCoreError(t *testing.T) {
	err := normalizeGRPCError(coreerrors.New(coreerrors.CodeNotFound, "missing"))
	if got := status.Code(err); got != codes.NotFound {
		t.Fatalf("code = %s, want %s", got, codes.NotFound)
	}
	if got := status.Convert(err).Message(); got != "missing" {
		t.Fatalf("message = %q, want missing", got)
	}
}

func TestOTelUnaryServerInjectsTraceparent(t *testing.T) {
	interceptor := OTelUnaryServerInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("traceparent", "00-11111111111111111111111111111111-2222222222222222-01"))
	_, err := interceptor(ctx, &struct{}{}, &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}, func(ctx context.Context, req any) (any, error) {
		if !oteltrace.SpanContextFromContext(ctx).IsValid() {
			t.Fatal("expected valid span context in handler")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestOTelStreamServerInjectsTraceparent(t *testing.T) {
	interceptor := OTelStreamServerInterceptor()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("traceparent", "00-11111111111111111111111111111111-2222222222222222-01"))
	err := interceptor(nil, testServerStream{ctx: ctx}, &stdgrpc.StreamServerInfo{FullMethod: "/greeter.Greeter/Subscribe"}, func(srv any, stream stdgrpc.ServerStream) error {
		if !oteltrace.SpanContextFromContext(stream.Context()).IsValid() {
			t.Fatal("expected valid span context in stream handler")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestOTelUnaryClientInjectsTraceparent(t *testing.T) {
	interceptor := OTelUnaryClientInterceptor()
	ctx, span := oteltrace.SpanFromContext(context.Background()).TracerProvider().Tracer("test").Start(context.Background(), "client-call", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	defer span.End()
	err := interceptor(ctx, "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok || len(md.Get("traceparent")) == 0 {
			t.Fatal("expected traceparent in outgoing metadata")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestGRPCAdminManagerLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(`{"rules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := governance.NewManager(governance.Config{RuleFile: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	server := NewDefaultServer("127.0.0.1:0", "health", governance.NewRuleSet(), nil,
		WithGovernanceManager(manager), WithRules(governance.NewRuleSet()),
		WithAdminAuthorization(func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer test" }),
	)
	listener := bufconn.Listen(1024 * 1024)
	done := make(chan error, 1)
	go func() { done <- server.GRPCServer().Serve(listener) }()
	t.Cleanup(func() {
		server.GRPCServer().Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	conn, err := stdgrpc.NewClient("passthrough:///bufnet", stdgrpc.WithTransportCredentials(insecure.NewCredentials()), stdgrpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := healthpb.NewHealthClient(conn)
	admin := newAdminServer("", server.rules, nil, server).Handler
	for _, path := range []string{"/healthz", "/readyz", "/startupz"} {
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("public probe %s required credentials", path)
		}
	}
	for _, path := range []string{"/runtime", "/metrics", "/governance/rules", "/governance/reload"} {
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", path, rec.Code)
		}
	}
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/governance/rules", strings.NewReader(`{"persist":true,"rules":[{"name":"live","method":"Check","policy":{"rateLimit":{"rate":1,"burst":1}}}]}`))
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update=%d %s", rec.Code, rec.Body.String())
	}
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("live rule not applied: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "live") {
		t.Fatalf("rules not persisted: %s %v", data, err)
	}
	if err := manager.RuleSet().ReplaceValidated(); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/governance/reload", nil)
	req.Header.Set("Authorization", "Bearer test")
	rec = httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(manager.RuleSet().Snapshot()) != 1 {
		t.Fatalf("reload=%d %s", rec.Code, rec.Body.String())
	}
	unsafe := NewDefaultServer("127.0.0.1:0", "unsafe", nil, nil, WithAdminAddr("0.0.0.0:0"))
	defer unsafe.GRPCServer().Stop()
	if err := unsafe.Start(); err == nil {
		t.Fatal("unprotected external admin accepted")
	}
}

func TestNewDefaultServerWiresAdminAndInterceptors(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "default rate limit",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
		Policy:    governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}},
	})
	server := NewDefaultServer(":0", "greeter", rules, nil,
		WithHealth(true),
		WithAdminAddr(":0"),
	)
	if server == nil {
		t.Fatal("expected non-nil server")
	}
	if server.GRPCServer() == nil {
		t.Fatal("expected non-nil grpc server")
	}
	if server.Address() != ":0" {
		t.Fatalf("address = %q, want :0", server.Address())
	}
}

func TestGRPCServerDiscoveryAndHealthLifecycle(t *testing.T) {
	registry := discovery.NewMemoryRegistry()
	server := NewDefaultServer("127.0.0.1:0", "greeter.Greeter", nil, nil,
		WithDiscovery(registry, discovery.Instance{Service: "greeter.Greeter"}),
	)
	before, err := server.Health().Check(context.Background(), &healthpb.HealthCheckRequest{Service: "greeter.Greeter"})
	if err != nil || before.Status != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health before start = %v, %v; want NOT_SERVING", before, err)
	}

	started := make(chan error, 1)
	go func() { started <- server.Start() }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		instances, resolveErr := registry.Resolve(context.Background(), "greeter.Greeter")
		if resolveErr == nil && len(instances) == 1 {
			host, port, splitErr := net.SplitHostPort(instances[0].Endpoint)
			if splitErr != nil || host != "127.0.0.1" || port == "0" {
				t.Fatalf("registered endpoint = %q, want loopback with allocated port", instances[0].Endpoint)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	after, err := server.Health().Check(context.Background(), &healthpb.HealthCheckRequest{Service: "greeter.Greeter"})
	if err != nil || after.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health after start = %v, %v; want SERVING", after, err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("start returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for grpc server to stop")
	}
	stopped, err := server.Health().Check(context.Background(), &healthpb.HealthCheckRequest{Service: "greeter.Greeter"})
	if err != nil || stopped.Status != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health after shutdown = %v, %v; want NOT_SERVING", stopped, err)
	}
	if _, err := registry.Resolve(context.Background(), "greeter.Greeter"); !errors.Is(err, discovery.ErrNoInstances) {
		t.Fatalf("resolve after shutdown = %v, want ErrNoInstances", err)
	}
}

func TestGRPCServerDiscoveryRequiresService(t *testing.T) {
	server := NewServer(
		WithAddress("127.0.0.1:0"),
		WithDiscovery(discovery.NewMemoryRegistry(), discovery.Instance{}),
	)
	err := server.Start()
	if err == nil || !strings.Contains(err.Error(), "grpc discovery service is required") {
		t.Fatalf("Start error = %v, want missing service error", err)
	}
}

func TestGRPCAdminServerExposesHealthMetricsAndGovernance(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "admin visible rule",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
	})
	server := NewDefaultServer("127.0.0.1:0", "greeter", rules, nil, WithAdminAddr("127.0.0.1:0"))
	started := make(chan error, 1)
	go func() { started <- server.Start() }()
	defer func() { _ = server.Shutdown(context.Background()) }()

	adminURL := waitForGRPCAdminURL(t, server)
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/healthz", want: "ok"},
		{path: "/readyz", want: "ok"},
		{path: "/startupz", want: "ok"},
		{path: "/metrics", want: "gofly_requests_total"},
		{path: "/governance/rules", want: "admin visible rule"},
		{path: "/runtime", want: "rpc.grpc.server"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(adminURL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), tc.want) {
				t.Fatalf("GET %s status=%d body=%s, want status 200 containing %q", tc.path, resp.StatusCode, data, tc.want)
			}
		})
	}

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("start returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for grpc server to stop")
	}
}

func TestGRPCAdminRuntimeExplainsDefaultInterceptorChain(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "runtime visible rule",
		Transport: governance.TransportRPC,
		Service:   "greeter.Greeter",
		Method:    "SayHello",
	})
	server := NewDefaultServer("127.0.0.1:0", "greeter", rules, nil, WithAdminAddr("127.0.0.1:0"))
	started := make(chan error, 1)
	go func() { started <- server.Start() }()
	defer func() { _ = server.Shutdown(context.Background()) }()

	adminURL := waitForGRPCAdminURL(t, server)
	resp, err := http.Get(adminURL + "/runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runtime status = %d, want 200", resp.StatusCode)
	}
	var snapshot coreruntime.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode runtime: %v", err)
	}
	if len(snapshot.Components) != 1 {
		t.Fatalf("runtime components = %#v, want one grpc component", snapshot.Components)
	}
	component := snapshot.Components[0]
	if component.Name != "rpc.grpc.server" || component.Middleware == nil {
		t.Fatalf("runtime component = %#v, want grpc server middleware snapshot", component)
	}
	unary := component.Middleware.Unary
	stream := component.Middleware.Stream
	if len(unary) != 4 || unary[0].Name != "recover" || unary[3].Name != "governance" {
		t.Fatalf("unary chain = %#v, want recover/observability/otel/governance", unary)
	}
	if len(stream) != 4 || stream[0].Name != "recover" || stream[3].Name != "governance" {
		t.Fatalf("stream chain = %#v, want recover/observability/otel/governance", stream)
	}

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("start returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for grpc server to stop")
	}
}

func waitForGRPCAdminURL(t *testing.T, server *Server) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if addr := server.AdminAddress(); addr != "" {
			return "http://" + addr
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for grpc admin address")
	return ""
}

func TestDefaultRetryableRecognizesCoreError(t *testing.T) {
	if !defaultRetryable(coreerrors.New(coreerrors.CodeUnavailable, "try again")) {
		t.Fatal("core unavailable error should be retryable")
	}
	if defaultRetryable(coreerrors.New(coreerrors.CodeInvalidArgument, "bad request")) {
		t.Fatal("core invalid argument error should not be retryable")
	}
	if defaultRetryable(nil) {
		t.Fatal("nil error should not be retryable")
	}
	if defaultRetryable(context.Canceled) {
		t.Fatal("context.Canceled should not be retryable")
	}
	if defaultRetryable(context.DeadlineExceeded) {
		t.Fatal("context.DeadlineExceeded should not be retryable")
	}
}

func TestGRPCStatusCodeMapsCodes(t *testing.T) {
	if got := grpcStatusCode(codes.OK); got != 200 {
		t.Fatalf("grpcStatusCode(OK) = %d, want 200", got)
	}
	if got := grpcStatusCode(codes.NotFound); got != 404 {
		t.Fatalf("grpcStatusCode(NotFound) = %d, want 404", got)
	}
}

func TestContextWithIncomingTrace(t *testing.T) {
	// no metadata -> unchanged
	ctx := contextWithIncomingTrace(context.Background())
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}

	// traceparent present -> new context
	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs(coretrace.TraceParentHeader, "00-11111111111111111111111111111111-2222222222222222-01"))
	next := contextWithIncomingTrace(ctx)
	if next == ctx {
		t.Fatal("expected new context with trace")
	}
}

func TestBearerFromIncomingAcceptsLegacyTokenMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("app", "orders", "token", "secret"))
	if got := bearerFromIncoming(ctx); got != "secret" {
		t.Fatalf("bearerFromIncoming = %q, want legacy token", got)
	}
}

type testServerStream struct {
	stdgrpc.ServerStream
	ctx context.Context
}

func (s testServerStream) Context() context.Context { return s.ctx }

type testClientStream struct {
	stdgrpc.ClientStream
}

func (s *testClientStream) CloseSend() error { return nil }

func TestGovernanceClientStreamLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sendErr error
	}{
		{name: "half close"},
		{name: "send EOF", sendErr: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			inner := &fakeClientStream{ctx: ctx, sendErr: tc.sendErr}
			released := 0
			stream := &governanceClientStream{ClientStream: inner, cancel: cancel, release: func() { released++ }, serverStreams: true}
			if err := stream.CloseSend(); err != nil {
				t.Fatal(err)
			}
			if err := stream.SendMsg(nil); !errors.Is(err, tc.sendErr) {
				t.Fatalf("send = %v", err)
			}
			if ctx.Err() != nil || released != 0 {
				t.Fatalf("half-closed stream canceled=%v released=%d", ctx.Err(), released)
			}
			if err := stream.RecvMsg(nil); err != nil {
				t.Fatal(err)
			}
			inner.recvErr = io.EOF
			if err := stream.RecvMsg(nil); err != io.EOF {
				t.Fatalf("receive = %v", err)
			}
			_ = stream.RecvMsg(nil)
			if released != 1 {
				t.Fatalf("released = %d, want 1", released)
			}
		})
	}
	t.Run("cancellation without receive", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rules := governance.NewRuleSet(governance.Rule{Policy: governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}}})
		interceptor := GovernanceStreamClientInterceptor(rules)
		streamer := func(ctx context.Context, _ *stdgrpc.StreamDesc, _ *stdgrpc.ClientConn, _ string, _ ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
			return &fakeClientStream{ctx: ctx}, nil
		}
		if _, err := interceptor(ctx, &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/svc/Watch", streamer); err != nil {
			t.Fatal(err)
		}
		cancel()
		deadline := time.Now().Add(time.Second)
		for {
			stream, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/svc/Watch", streamer)
			if err == nil {
				stream.(*governanceClientStream).finish()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("canceled stream still holds concurrency: %v", err)
			}
			time.Sleep(time.Millisecond)
		}
	})
}

func TestGovernanceBreakerPolicy(t *testing.T) {
	t.Run("configured threshold", func(t *testing.T) {
		o := newGovernanceOptions()
		cb := o.breaker("rule", governance.BreakerPolicy{Enabled: true, Window: time.Minute, Buckets: 6, MinRequests: 5, FailureRatio: 0.8, OpenTimeout: time.Hour})
		for i := 0; i < 5; i++ {
			if err := cb.Do(context.Background(), func() error { return errors.New("down") }); errors.Is(err, breaker.ErrOpen) {
				t.Fatalf("breaker opened after %d failures, want 5", i)
			}
		}
		if err := cb.Do(context.Background(), func() error { return nil }); !errors.Is(err, breaker.ErrOpen) {
			t.Fatalf("breaker = %v, want open", err)
		}
	})
	t.Run("policy update", func(t *testing.T) {
		o := newGovernanceOptions()
		policy := governance.BreakerPolicy{Enabled: true, OpenTimeout: time.Hour}
		cb := o.breaker("rule", policy)
		for i := 0; i < 3; i++ {
			_ = cb.Do(context.Background(), func() error { return errors.New("down") })
		}
		policy.MinRequests = 10
		if err := o.breaker("rule", policy).Do(context.Background(), func() error { return nil }); err != nil {
			t.Fatalf("updated policy reused open breaker: %v", err)
		}
	})
}

func TestClientStreamObservabilityLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name          string
		serverStreams bool
		err           error
		cancel        bool
	}{
		{name: "server stream EOF", serverStreams: true, err: io.EOF},
		{name: "client stream response"},
		{name: "server failure", serverStreams: true, err: status.Error(codes.Unavailable, "down")},
		{name: "caller cancellation", serverStreams: true, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			})
			ctx, parent := provider.Tracer("test").Start(context.Background(), "parent")
			defer parent.End()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			registry := metrics.NewRegistry()
			observation := ObservabilityStreamClientInterceptor("svc", registry, nil)
			trace := OTelStreamClientInterceptor()
			inner := &fakeClientStream{recvErr: tc.err}
			stream, err := observation(ctx, &stdgrpc.StreamDesc{ServerStreams: tc.serverStreams}, nil, "/svc/Stream", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
				return trace(ctx, desc, cc, method, func(ctx context.Context, _ *stdgrpc.StreamDesc, _ *stdgrpc.ClientConn, _ string, _ ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
					inner.ctx = ctx
					return inner, nil
				}, opts...)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.CloseSend(); err != nil {
				t.Fatal(err)
			}
			if len(recorder.Ended()) != 0 || registry.Snapshot().InFlight != 1 {
				t.Fatal("half-close completed observation")
			}
			if tc.cancel {
				cancel()
				deadline := time.Now().Add(time.Second)
				for len(recorder.Ended()) == 0 || registry.Snapshot().InFlight != 0 {
					if time.Now().After(deadline) {
						t.Fatal("cancellation did not complete observation")
					}
					time.Sleep(time.Millisecond)
				}
			} else {
				if err := stream.RecvMsg(nil); !errors.Is(err, tc.err) {
					t.Fatalf("receive = %v, want %v", err, tc.err)
				}
				_ = stream.RecvMsg(nil)
			}
			ended := recorder.Ended()
			if len(ended) != 1 || registry.Snapshot().InFlight != 0 {
				t.Fatalf("ended spans=%d metrics=%+v", len(ended), registry.Snapshot())
			}
			wantError := tc.cancel || tc.err != nil && tc.err != io.EOF
			if (ended[0].Status().Code == otelcodes.Error) != wantError {
				t.Fatalf("span status = %v", ended[0].Status())
			}
		})
	}
}

func TestRecoveryStreamServerInterceptor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		panics bool
		err    error
	}{
		{name: "success"},
		{name: "handler error", err: status.Error(codes.InvalidArgument, "bad input")},
		{name: "panic", panics: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RecoveryStreamServerInterceptor(nil)(nil, testServerStream{ctx: context.Background()}, &stdgrpc.StreamServerInfo{FullMethod: "/svc/Stream"}, func(any, stdgrpc.ServerStream) error {
				if tc.panics {
					panic("private diagnostic")
				}
				return tc.err
			})
			if tc.panics {
				if status.Code(err) != codes.Internal || strings.Contains(err.Error(), "private diagnostic") {
					t.Fatalf("panic result = %v", err)
				}
			} else if !errors.Is(err, tc.err) {
				t.Fatalf("result = %v, want %v", err, tc.err)
			}
		})
	}
	server := NewDefaultServer("127.0.0.1:0", "svc", nil, nil)
	if !slices.Contains(server.streamNames, "recover") {
		t.Fatalf("stream chain = %v", server.streamNames)
	}
}

func TestDefaultGRPCStreamingLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name          string
		clientStreams bool
		serverStreams bool
		panics        bool
	}{
		{name: "server streaming", serverStreams: true},
		{name: "client streaming", clientStreams: true},
		{name: "bidirectional", clientStreams: true, serverStreams: true},
		{name: "panic recovery", clientStreams: true, serverStreams: true, panics: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener := bufconn.Listen(1024 * 1024)
			rules := governance.NewRuleSet(governance.Rule{Policy: governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}, Timeout: time.Second}})
			server := NewDefaultServer("", "test.Streams", rules, nil, WithHealth(false))
			desc := stdgrpc.StreamDesc{StreamName: "Exchange", ClientStreams: tc.clientStreams, ServerStreams: tc.serverStreams}
			desc.Handler = func(_ any, stream stdgrpc.ServerStream) error {
				for {
					err := stream.RecvMsg(&emptypb.Empty{})
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						return err
					}
				}
				if tc.panics {
					panic("private diagnostic")
				}
				if err := stream.SendMsg(&emptypb.Empty{}); err != nil {
					return err
				}
				if tc.serverStreams {
					return stream.SendMsg(&emptypb.Empty{})
				}
				return nil
			}
			server.RegisterService(&stdgrpc.ServiceDesc{ServiceName: "test.Streams", HandlerType: (*interface{})(nil), Streams: []stdgrpc.StreamDesc{desc}}, struct{}{})
			done := make(chan error, 1)
			go func() { done <- server.GRPCServer().Serve(listener) }()
			t.Cleanup(func() {
				server.GRPCServer().Stop()
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			conn, err := NewDefaultClient(t.Context(), "passthrough:///bufnet", "test.Streams", rules, nil,
				WithDialOptions(stdgrpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), stdgrpc.WithTransportCredentials(insecure.NewCredentials())),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for range 2 {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				stream, err := conn.Conn().NewStream(ctx, &desc, "/test.Streams/Exchange")
				if err != nil {
					t.Fatal(err)
				}
				if err := stream.SendMsg(&emptypb.Empty{}); err != nil {
					t.Fatal(err)
				}
				if err := stream.CloseSend(); err != nil {
					t.Fatal(err)
				}
				err = stream.RecvMsg(&emptypb.Empty{})
				if tc.panics {
					if status.Code(err) != codes.Internal {
						t.Fatalf("panic status = %v", err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("receive after half-close: %v", err)
				}
				if tc.serverStreams {
					if err := stream.RecvMsg(&emptypb.Empty{}); err != nil {
						t.Fatal(err)
					}
					if err := stream.RecvMsg(&emptypb.Empty{}); err != io.EOF {
						t.Fatalf("terminal receive = %v", err)
					}
				}
			}
		})
	}
}

func TestDefaultGRPCOptionsApplyOnce(t *testing.T) {
	rules := governance.NewRuleSet()
	registry := metrics.NewRegistry()
	serverCalls := 0
	server := NewDefaultServer("", "svc", nil, nil, nil, WithRules(rules), WithMetricsRegistry(registry), func(*serverOptions) { serverCalls++ })
	defer server.GRPCServer().Stop()
	if serverCalls != 1 || server.rules != rules || server.registry != registry || !slices.Contains(server.unaryNames, "governance") || !slices.Contains(server.streamNames, "governance") {
		t.Fatalf("server options calls=%d rules=%p chain=%v", serverCalls, server.rules, server.unaryNames)
	}
	clientCalls := 0
	conn, err := NewDefaultClient(t.Context(), "127.0.0.1:1", "svc", nil, registry, nil, WithClientRules(rules), func(*clientOptions) { clientCalls++ })
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if clientCalls != 1 {
		t.Fatalf("client option called %d times", clientCalls)
	}
}

func TestGovernanceOptions(t *testing.T) {
	o := &governanceOptions{}
	WithGovernanceService("greeter")(o)
	if o.service != "greeter" {
		t.Fatalf("service = %q, want greeter", o.service)
	}
	WithGovernanceTags(map[string]string{"env": "test"})(o)
	if o.tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", o.tags)
	}
}

type fakeClientStream struct {
	stdgrpc.ClientStream
	sendErr      error
	recvErr      error
	closeSendErr error
	headerErr    error
	ctx          context.Context
}

func (f *fakeClientStream) Context() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
}
func (f *fakeClientStream) SendMsg(m any) error          { return f.sendErr }
func (f *fakeClientStream) RecvMsg(m any) error          { return f.recvErr }
func (f *fakeClientStream) CloseSend() error             { return f.closeSendErr }
func (f *fakeClientStream) Header() (metadata.MD, error) { return nil, f.headerErr }
func (f *fakeClientStream) Trailer() metadata.MD         { return nil }

func TestGovernanceClientStreamRecvMsgAndSendMsg(t *testing.T) {
	inner := &fakeClientStream{}
	cs := &governanceClientStream{ClientStream: inner, cancel: func() {}, release: func() {}}
	if err := cs.SendMsg(&struct{}{}); err != nil {
		t.Fatalf("SendMsg error: %v", err)
	}
	if err := cs.RecvMsg(&struct{}{}); err != nil {
		t.Fatalf("RecvMsg error: %v", err)
	}

	// error paths trigger finish
	inner.sendErr = errors.New("send failed")
	if err := cs.SendMsg(&struct{}{}); err == nil {
		t.Fatal("expected send error")
	}

	inner2 := &fakeClientStream{recvErr: errors.New("recv failed")}
	cs2 := &governanceClientStream{ClientStream: inner2, cancel: func() {}, release: func() {}}
	if err := cs2.RecvMsg(&struct{}{}); err == nil {
		t.Fatal("expected recv error")
	}
}

func TestInterceptorClientInterceptors(t *testing.T) {
	// RetryUnaryClientInterceptor
	retryInterceptor := RetryUnaryClientInterceptor(coreretry.Policy{Attempts: 2, Backoff: time.Nanosecond})
	attempts := 0
	err := retryInterceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		attempts++
		if attempts == 1 {
			return status.Error(codes.Unavailable, "try again")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retry interceptor error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	// BreakerUnaryClientInterceptor
	cb := breaker.New()
	breakerInterceptor := BreakerUnaryClientInterceptor(cb)
	calls := 0
	err = breakerInterceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("breaker interceptor error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// FallbackUnaryClientInterceptor
	fallbackInterceptor := FallbackUnaryClientInterceptor(func(ctx context.Context, method string, req any, reply any, err error) error {
		return status.Error(codes.NotFound, "fallback")
	})
	err = fallbackInterceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		return status.Error(codes.Internal, "boom")
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("fallback code = %s, want NotFound", status.Code(err))
	}

	// nil fallback returns original error
	err = FallbackUnaryClientInterceptor(nil)(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		return status.Error(codes.Internal, "boom")
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("nil fallback code = %s, want Internal", status.Code(err))
	}

	// TimeoutUnaryClientInterceptor with positive timeout
	timeoutInterceptor := TimeoutUnaryClientInterceptor(time.Hour)
	err = timeoutInterceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		if _, ok := ctx.Deadline(); !ok {
			return status.Error(codes.DeadlineExceeded, "expected deadline")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("timeout interceptor error: %v", err)
	}

	// TimeoutUnaryClientInterceptor with zero timeout (noop)
	noopTimeoutInterceptor := TimeoutUnaryClientInterceptor(0)
	err = noopTimeoutInterceptor(context.Background(), "/greeter.Greeter/SayHello", &struct{}{}, &struct{}{}, nil, func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, opts ...stdgrpc.CallOption) error {
		return nil
	})
	if err != nil {
		t.Fatalf("noop timeout interceptor error: %v", err)
	}
}

func TestServerOptionsAndHealth(t *testing.T) {
	server := NewServer(
		WithServerOptions(stdgrpc.ConnectionTimeout(time.Second)),
		WithReflection(true),
		WithStopTimeout(5*time.Second),
	)
	if server == nil {
		t.Fatal("expected non-nil server")
	}
	if server.Health() == nil {
		t.Fatal("expected non-nil health server")
	}
}

func TestServerRegisterService(t *testing.T) {
	server := NewServer(WithAddress("127.0.0.1:0"), WithHealth(false))
	// grpc.Server.RegisterService panics on nil sd or impl, and requires
	// sd.Methods to have non-nil Handlers. Use the health service desc
	// which is known valid.
	server.RegisterService(&healthpb.Health_ServiceDesc, health.NewServer())
}

func TestOTelStreamClientInterceptorAndStreamMethods(t *testing.T) {
	interceptor := OTelStreamClientInterceptor()

	// Error path: streamer returns error -> span ends immediately
	_, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		return nil, status.Error(codes.Unavailable, "stream failed")
	})
	if err == nil {
		t.Fatal("expected stream error")
	}

	// Success path: returns wrapped otelClientStream
	inner := &fakeClientStream{}
	stream, err := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		return inner, nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
	if stream == nil {
		t.Fatal("expected non-nil stream")
	}

	// Exercise otelClientStream methods
	ocs := stream.(*otelClientStream)
	if ocs.Context() == nil {
		t.Fatal("expected non-nil context")
	}
	if err := ocs.SendMsg(&struct{}{}); err != nil {
		t.Fatalf("SendMsg error: %v", err)
	}
	if err := ocs.RecvMsg(&struct{}{}); err != nil {
		t.Fatalf("RecvMsg error: %v", err)
	}
	if err := ocs.CloseSend(); err != nil {
		t.Fatalf("CloseSend error: %v", err)
	}
	if _, err := ocs.Header(); err != nil {
		t.Fatalf("Header error: %v", err)
	}
	_ = ocs.Trailer()
	if err := ocs.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Error paths on otelClientStream
	innerErr := &fakeClientStream{sendErr: errors.New("send failed"), recvErr: errors.New("recv failed")}
	stream2, _ := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		return innerErr, nil
	})
	_ = stream2.SendMsg(&struct{}{})
	_ = stream2.RecvMsg(&struct{}{})
}

func TestTraceFromIncoming(t *testing.T) {
	// no metadata
	if got := traceFromIncoming(context.Background()); got != "" {
		t.Fatalf("traceFromIncoming = %q, want empty", got)
	}

	// grpc-trace-bin fallback
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("grpc-trace-bin", "some-bin"))
	if got := traceFromIncoming(ctx); got != "some-bin" {
		t.Fatalf("traceFromIncoming = %q, want some-bin", got)
	}
}

func TestNewGovernanceOptionsNilOpt(t *testing.T) {
	o := newGovernanceOptions(nil, WithGovernanceService("svc"))
	if o.service != "svc" {
		t.Fatalf("service = %q, want svc", o.service)
	}
}

func TestGovernanceRuntimeKeyBranches(t *testing.T) {
	if got := governanceRuntimeKey(governance.Decision{RuleKey: "key1"}, "fallback"); got != "key1" {
		t.Fatalf("runtimeKey = %q, want key1", got)
	}
	if got := governanceRuntimeKey(governance.Decision{RuleName: "name1"}, "fallback"); got != "name:name1" {
		t.Fatalf("runtimeKey = %q, want name:name1", got)
	}
	if got := governanceRuntimeKey(governance.Decision{}, "fallback"); got != "fallback" {
		t.Fatalf("runtimeKey = %q, want fallback", got)
	}
}

func TestOTelClientStreamCloseSendAndHeaderError(t *testing.T) {
	interceptor := OTelStreamClientInterceptor()

	// CloseSend error path
	innerCloseSend := &fakeClientStream{closeSendErr: errors.New("close send failed")}
	stream, _ := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		return innerCloseSend, nil
	})
	if err := stream.CloseSend(); err == nil {
		t.Fatal("expected CloseSend error")
	}

	// Header error path
	innerHeader := &fakeClientStream{headerErr: errors.New("header failed")}
	stream2, _ := interceptor(context.Background(), &stdgrpc.StreamDesc{ServerStreams: true}, nil, "/greeter.Greeter/Subscribe", func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		return innerHeader, nil
	})
	if _, err := stream2.Header(); err == nil {
		t.Fatal("expected Header error")
	}
}

func TestRecoveryUnaryServerInterceptorRecoversPanic(t *testing.T) {
	interceptor := RecoveryUnaryServerInterceptor(nil)
	_, err := interceptor(context.Background(), &struct{}{}, &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}, func(ctx context.Context, req any) (any, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error after panic recovery")
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %s, want Internal", status.Code(err))
	}
}

func TestObservabilityUnaryServerInterceptorDefaultsAndCoreError(t *testing.T) {
	// nil registry and logger use defaults
	interceptor := ObservabilityUnaryServerInterceptor("svc", nil, nil)
	_, err := interceptor(context.Background(), &struct{}{}, &stdgrpc.UnaryServerInfo{FullMethod: "/greeter.Greeter/SayHello"}, func(ctx context.Context, req any) (any, error) {
		return nil, coreerrors.New(coreerrors.CodeNotFound, "missing")
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %s, want NotFound", status.Code(err))
	}
}

func TestObservabilityStreamServerInterceptorDefaults(t *testing.T) {
	interceptor := ObservabilityStreamServerInterceptor("svc", nil, nil)
	err := interceptor(nil, testServerStream{ctx: context.Background()}, &stdgrpc.StreamServerInfo{FullMethod: "/greeter.Greeter/Subscribe"}, func(srv any, stream stdgrpc.ServerStream) error {
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestInjectTraceOutgoingBranches(t *testing.T) {
	// Branch 1: coretrace.FromContext exists
	ctx1 := coretrace.NewContext(context.Background(), coretrace.SpanContext{TraceID: "11111111111111111111111111111111", SpanID: "2222222222222222"})
	ctx1 = metadata.NewOutgoingContext(ctx1, metadata.Pairs("existing", "value"))
	out1 := injectTraceOutgoing(ctx1)
	md1, ok := metadata.FromOutgoingContext(out1)
	if !ok || len(md1.Get("traceparent")) == 0 {
		t.Fatal("expected traceparent in outgoing metadata from coretrace context")
	}
	if len(md1.Get("existing")) == 0 {
		t.Fatal("expected existing metadata preserved")
	}

	// Branch 2: otel span context valid and convertible
	ctx2, span := oteltrace.SpanFromContext(context.Background()).TracerProvider().Tracer("test").Start(context.Background(), "test")
	defer span.End()
	out2 := injectTraceOutgoing(ctx2)
	md2, ok := metadata.FromOutgoingContext(out2)
	if !ok || len(md2.Get("traceparent")) == 0 {
		t.Fatal("expected traceparent from otel span context")
	}

	// Branch 3: no trace context -> starts new span
	out3 := injectTraceOutgoing(context.Background())
	md3, ok := metadata.FromOutgoingContext(out3)
	if !ok || len(md3.Get("traceparent")) == 0 {
		t.Fatal("expected traceparent from newly started span")
	}
}

var _ = oteltrace.SpanFromContext
