package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/imajinyun/gofly/core/limit"
)

func TestAdaptiveLimitConfig(t *testing.T) {
	defaults := DefaultAdaptiveLimitConfig()
	if !defaults.Enabled || defaults.MinLimit != 16 || defaults.MaxLimit != 256 || defaults.InitialLimit != 64 || defaults.CPUThresholdPermille != 800 || defaults.Window != 10*time.Second || defaults.TargetLatency != 100*time.Millisecond || defaults.TargetErrorRatio != 0.05 || defaults.MinSamples != 20 {
		t.Fatalf("defaults=%+v", defaults)
	}
	if limiter, err := (AdaptiveLimitConfig{}).NewLimiter(nil); err != nil || limiter != nil {
		t.Fatalf("disabled limiter=%v err=%v", limiter, err)
	}
	for _, mutate := range []func(*AdaptiveLimitConfig){
		func(c *AdaptiveLimitConfig) { c.MinLimit = 0 },
		func(c *AdaptiveLimitConfig) { c.InitialLimit = c.MinLimit - 1 },
		func(c *AdaptiveLimitConfig) { c.MaxLimit = c.InitialLimit - 1 },
		func(c *AdaptiveLimitConfig) { c.CPUThresholdPermille = 1001 },
		func(c *AdaptiveLimitConfig) { c.Window = 0 },
		func(c *AdaptiveLimitConfig) { c.TargetLatency = 0 },
		func(c *AdaptiveLimitConfig) { c.TargetErrorRatio = 1.1 },
		func(c *AdaptiveLimitConfig) { c.TargetErrorRatio = math.NaN() },
		func(c *AdaptiveLimitConfig) { c.MinSamples = 0 },
	} {
		cfg := defaults
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
	if limiter, err := defaults.NewLimiter(func() int { return 0 }); err != nil || limiter == nil {
		t.Fatalf("default limiter=%v err=%v", limiter, err)
	}
}

func TestAdaptiveLimitStreamServerInterceptor(t *testing.T) {
	limiter := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	interceptor := AdaptiveLimitStreamServerInterceptor(limiter)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- interceptor(nil, testServerStream{ctx: t.Context()}, &stdgrpc.StreamServerInfo{FullMethod: "/svc/Watch", IsServerStream: true}, func(any, stdgrpc.ServerStream) error { close(entered); <-release; return io.EOF })
	}()
	<-entered
	if err := interceptor(nil, testServerStream{ctx: t.Context()}, &stdgrpc.StreamServerInfo{FullMethod: "/svc/Watch"}, func(any, stdgrpc.ServerStream) error { return nil }); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second stream err=%v", err)
	}
	close(release)
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Fatalf("first stream err=%v", err)
	}
	if limiter.Snapshot().InFlight != 0 {
		t.Fatal("stream permit was not released")
	}
	t.Run("panic releases permit", func(t *testing.T) {
		panicking := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
		func() {
			defer func() { _ = recover() }()
			_ = AdaptiveLimitStreamServerInterceptor(panicking)(nil, testServerStream{ctx: t.Context()}, &stdgrpc.StreamServerInfo{FullMethod: "/svc/Watch"}, func(any, stdgrpc.ServerStream) error { panic("test") })
		}()
		if panicking.Snapshot().InFlight != 0 {
			t.Fatal("panic leaked stream permit")
		}
	})
}

func TestDefaultServerAdaptiveLimiterSnapshot(t *testing.T) {
	limiter := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	server := NewDefaultServer("127.0.0.1:0", "svc", nil, nil, WithAdaptiveLimiter(limiter))
	defer server.GRPCServer().Stop()
	if !slices.Contains(server.unaryNames, "adaptive_limit") || !slices.Contains(server.streamNames, "adaptive_limit") {
		t.Fatalf("adaptive middleware missing: unary=%v stream=%v", server.unaryNames, server.streamNames)
	}
	snapshot := server.RuntimeSnapshot(t.Context())
	if len(snapshot.Components) != 1 {
		t.Fatalf("components=%+v", snapshot.Components)
	}
	governance, ok := snapshot.Components[0].Governance.(map[string]any)
	if !ok || governance["adaptiveLimit"] == nil {
		t.Fatalf("governance snapshot=%#v", snapshot.Components[0].Governance)
	}
	token, err := limiter.Allow()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AdaptiveLimitUnaryServerInterceptor(limiter)(t.Context(), nil, &stdgrpc.UnaryServerInfo{FullMethod: "/svc/Call"}, func(context.Context, any) (any, error) { return nil, nil }); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("saturated call err=%v", err)
	}
	token.Done(true)
	snapshot = server.RuntimeSnapshot(t.Context())
	governance = snapshot.Components[0].Governance.(map[string]any)
	adaptive := governance["adaptiveLimit"].(limit.AdaptiveSnapshot)
	if adaptive.InFlight != 0 || adaptive.Passes != 1 || adaptive.Drops != 1 {
		t.Fatalf("adaptive snapshot=%+v", adaptive)
	}
	recorder := httptest.NewRecorder()
	newAdminServer("127.0.0.1:0", nil, nil, server).Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/runtime", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("runtime status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var adminSnapshot struct {
		Components []struct {
			Governance map[string]json.RawMessage `json:"governance"`
		} `json:"components"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&adminSnapshot); err != nil {
		t.Fatal(err)
	}
	var exposed limit.AdaptiveSnapshot
	if len(adminSnapshot.Components) != 1 || json.Unmarshal(adminSnapshot.Components[0].Governance["adaptiveLimit"], &exposed) != nil || exposed.Passes != 1 || exposed.Drops != 1 {
		t.Fatalf("admin adaptive snapshot=%+v body=%s", exposed, recorder.Body.String())
	}
}

func TestNewServerAdaptiveLimiterSnapshot(t *testing.T) {
	limiter := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	server := NewServer(WithAdaptiveLimiter(limiter))
	defer server.GRPCServer().Stop()
	snapshot := server.RuntimeSnapshot(t.Context())
	if len(snapshot.Components) != 1 {
		t.Fatalf("components=%+v", snapshot.Components)
	}
	governance := snapshot.Components[0].Governance.(map[string]any)
	if governance["adaptiveLimit"] == nil {
		t.Fatalf("governance snapshot=%#v", governance)
	}
}

func TestWithAdaptiveLimiterIsIdempotent(t *testing.T) {
	first := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	second := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(2, 2), limit.WithAdaptiveInitialLimit(2))
	server := NewServer(WithAdaptiveLimiter(first), WithAdaptiveLimiter(second))
	defer server.GRPCServer().Stop()
	if server.adaptiveLimiter != first {
		t.Fatal("repeated option replaced the first adaptive limiter")
	}
	if got := countAdaptiveLayers(server.unaryNames); got != 1 {
		t.Fatalf("adaptive unary layers=%d want=1", got)
	}
	if got := countAdaptiveLayers(server.streamNames); got != 1 {
		t.Fatalf("adaptive stream layers=%d want=1", got)
	}
}

func countAdaptiveLayers(names []string) int {
	count := 0
	for _, name := range names {
		if name == "adaptive_limit" {
			count++
		}
	}
	return count
}

func TestDefaultServerKeepsAdaptiveLimiterOptIn(t *testing.T) {
	server := NewDefaultServer("127.0.0.1:0", "svc", nil, nil, WithAdaptiveLimiter(nil))
	defer server.GRPCServer().Stop()
	if slices.Contains(server.unaryNames, "adaptive_limit") || slices.Contains(server.streamNames, "adaptive_limit") {
		t.Fatalf("direct default server enabled adaptive admission: unary=%v stream=%v", server.unaryNames, server.streamNames)
	}
	governance := server.RuntimeSnapshot(t.Context()).Components[0].Governance.(map[string]any)
	if _, exists := governance["adaptiveLimit"]; exists {
		t.Fatalf("direct default snapshot exposed adaptive limiter: %#v", governance)
	}
}

func TestAdaptiveLimitSharedAcrossUnaryAndStream(t *testing.T) {
	limiter := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	unary := AdaptiveLimitUnaryServerInterceptor(limiter)
	stream := AdaptiveLimitStreamServerInterceptor(limiter)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := unary(t.Context(), nil, &stdgrpc.UnaryServerInfo{FullMethod: "/svc/Call"}, func(context.Context, any) (any, error) { close(entered); <-release; return nil, nil })
		done <- err
	}()
	<-entered
	if err := stream(nil, testServerStream{ctx: t.Context()}, &stdgrpc.StreamServerInfo{FullMethod: "/svc/Watch"}, func(any, stdgrpc.ServerStream) error { return nil }); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("stream admitted while unary held shared permit: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if limiter.Snapshot().InFlight != 0 {
		t.Fatal("unary completion leaked shared permit")
	}
}

func TestDefaultServerAdaptiveLimiterAcrossUnaryAndBidiStream(t *testing.T) {
	limiter := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	server := NewDefaultServer("", "adaptive.Service", nil, nil, WithAdaptiveLimiter(limiter), WithHealth(false))
	streamEntered := make(chan struct{})
	desc := stdgrpc.ServiceDesc{
		ServiceName: "adaptive.Service",
		HandlerType: (*interface{})(nil),
		Methods: []stdgrpc.MethodDesc{{MethodName: "Call", Handler: func(_ any, ctx context.Context, decode func(any) error, interceptor stdgrpc.UnaryServerInterceptor) (any, error) {
			request := new(emptypb.Empty)
			if err := decode(request); err != nil {
				return nil, err
			}
			handler := func(context.Context, any) (any, error) { return &emptypb.Empty{}, nil }
			return interceptor(ctx, request, &stdgrpc.UnaryServerInfo{FullMethod: "/adaptive.Service/Call"}, handler)
		}}},
		Streams: []stdgrpc.StreamDesc{{StreamName: "Watch", ClientStreams: true, ServerStreams: true, Handler: func(_ any, stream stdgrpc.ServerStream) error {
			close(streamEntered)
			<-stream.Context().Done()
			return stream.Context().Err()
		}}},
	}
	server.RegisterService(&desc, struct{}{})
	listener := bufconn.Listen(1024 * 1024)
	done := make(chan error, 1)
	go func() { done <- server.GRPCServer().Serve(listener) }()
	t.Cleanup(func() {
		server.GRPCServer().Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	conn, err := stdgrpc.NewClient("passthrough:///bufnet", stdgrpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), stdgrpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	streamCtx, cancelStream := context.WithCancel(t.Context())
	stream, err := conn.NewStream(streamCtx, &desc.Streams[0], "/adaptive.Service/Watch")
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.SendMsg(&emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	<-streamEntered
	if err := conn.Invoke(t.Context(), "/adaptive.Service/Call", &emptypb.Empty{}, &emptypb.Empty{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("unary admitted while bidi stream held shared permit: %v", err)
	}
	cancelStream()
	_ = stream.RecvMsg(&emptypb.Empty{})
	deadline := time.Now().Add(time.Second)
	for limiter.Snapshot().InFlight != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if limiter.Snapshot().InFlight != 0 {
		t.Fatal("canceled bidi stream leaked shared permit")
	}
	if err := conn.Invoke(t.Context(), "/adaptive.Service/Call", &emptypb.Empty{}, &emptypb.Empty{}); err != nil {
		t.Fatalf("unary after stream cancellation: %v", err)
	}
}
