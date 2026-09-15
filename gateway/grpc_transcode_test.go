package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/rpc"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

type transcodeHealthServer struct {
	healthpb.UnimplementedHealthServer
	watchStarted  chan struct{}
	watchCanceled chan struct{}
}

func (transcodeHealthServer) Check(ctx context.Context, req *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if req.Service == "missing" {
		return nil, status.Error(codes.NotFound, "service not found")
	}
	if req.Service == "slow" {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	md, _ := grpcmetadata.FromIncomingContext(ctx)
	if values := md.Get("x-tenant"); len(values) != 1 || values[0] != "tenant" {
		return nil, status.Error(codes.PermissionDenied, "tenant missing")
	}
	if err := stdgrpc.SendHeader(ctx, grpcmetadata.Pairs("x-result", "accepted", "secret-bin", "private")); err != nil {
		return nil, err
	}
	stdgrpc.SetTrailer(ctx, grpcmetadata.Pairs("x-trailer", "complete"))
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func (s transcodeHealthServer) Watch(req *healthpb.HealthCheckRequest, stream healthpb.Health_WatchServer) error {
	switch req.Service {
	case "missing":
		return status.Error(codes.NotFound, "service not found")
	case "cancel", "deadline":
		if err := stream.SendHeader(grpcmetadata.Pairs("x-result", "watching")); err != nil {
			return err
		}
		if err := stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}); err != nil {
			return err
		}
		if req.Service == "cancel" && s.watchStarted != nil {
			close(s.watchStarted)
		}
		<-stream.Context().Done()
		if req.Service == "cancel" && s.watchCanceled != nil {
			close(s.watchCanceled)
		}
		return status.FromContextError(stream.Context().Err()).Err()
	}
	if err := stream.SendHeader(grpcmetadata.Pairs("x-result", "watching", "secret-bin", "private")); err != nil {
		return err
	}
	if err := stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}); err != nil {
		return err
	}
	if req.Service == "fail-after-message" {
		return status.Error(codes.DataLoss, "watch failed")
	}
	if err := stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_NOT_SERVING}); err != nil {
		return err
	}
	stream.SetTrailer(grpcmetadata.Pairs("x-trailer", "complete", "secret-bin", "private"))
	return nil
}

func TestGatewayNativeGRPCTranscoding(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := stdgrpc.NewServer()
	watchStarted := make(chan struct{})
	watchCanceled := make(chan struct{})
	healthpb.RegisterHealthServer(server, transcodeHealthServer{watchStarted: watchStarted, watchCanceled: watchCanceled})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	healthFile := protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)
	healthService := healthFile.Service[0]
	healthService.Method = append(healthService.Method,
		&descriptorpb.MethodDescriptorProto{Name: proto.String("Upload"), InputType: proto.String(".grpc.health.v1.HealthCheckRequest"), OutputType: proto.String(".grpc.health.v1.HealthCheckResponse"), ClientStreaming: proto.Bool(true)},
		&descriptorpb.MethodDescriptorProto{Name: proto.String("Chat"), InputType: proto.String(".grpc.health.v1.HealthCheckRequest"), OutputType: proto.String(".grpc.health.v1.HealthCheckResponse"), ClientStreaming: proto.Bool(true), ServerStreaming: proto.Bool(true)},
	)
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{healthFile}}
	factory, err := NewGRPCTranscoderFactory(set, flygrpc.WithDialOptions(stdgrpc.WithTransportCredentials(insecure.NewCredentials()), stdgrpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() })))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := rpc.Descriptor{
		Name:    "grpc.health.v1.Health",
		Methods: []rpc.MethodDescriptor{{Name: "Check"}, {Name: "List"}},
		Streams: []rpc.StreamDescriptor{
			{Name: "Watch", Mode: rpc.StreamModeServerStream},
			{Name: "Upload", Mode: rpc.StreamModeClientStream},
			{Name: "Chat", Mode: rpc.StreamModeBidiStream},
		},
	}
	route := Route{
		Name:       "native",
		Method:     http.MethodPost,
		PathPrefix: "/native",
		Targets:    []string{"passthrough:///bufnet"},
		Header:     HeaderPolicy{AllowRequest: []string{"X-Tenant"}},
		Breaker:    BreakerConfig{Enabled: true, MinRequests: 100},
		Transcode:  TranscodeConfig{Enabled: true, Protocol: "grpc", Descriptor: descriptor.Name},
	}
	g, err := New(
		[]Route{route},
		WithDescriptors(descriptor),
		WithTranscoderFactory(factory),
		WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for _, tc := range []struct {
		name, path, body string
		want             int
	}{
		{name: "success", path: "Check", body: `{"service":"ok"}`, want: http.StatusOK},
		{name: "not found", path: "Check", body: `{"service":"missing"}`, want: http.StatusNotFound},
		{name: "invalid JSON", path: "Check", body: `{"unknown":true}`, want: http.StatusBadRequest},
		{name: "client stream rejected", path: "Upload", body: `{}`, want: http.StatusNotImplemented},
		{name: "bidirectional stream rejected", path: "Chat", body: `{}`, want: http.StatusNotImplemented},
		{name: "unknown descriptor method", path: "Unknown", body: `{}`, want: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/native/"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("X-Tenant", "tenant")
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s want=%d", rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want == http.StatusOK {
				var response map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response["status"] != "SERVING" || rec.Header().Get("X-Gofly-Md-X-Result") != "accepted" || rec.Header().Get("X-Gofly-Md-X-Trailer") != "complete" {
					t.Fatalf("response=%v headers=%v", response, rec.Header())
				}
				if rec.Header().Get("X-Gofly-Md-Secret-Bin") != "" {
					t.Fatal("binary metadata forwarded")
				}
			}
		})
	}
	t.Run("server stream uses SSE", func(t *testing.T) {
		before := g.breakerFor(route).Snapshot()
		req := httptest.NewRequest(http.MethodPost, "/native/Watch", strings.NewReader(`{"service":"ok"}`))
		req.Header.Set("X-Tenant", "tenant")
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
			t.Fatalf("content type = %q", got)
		}
		if got := rec.Header().Get("X-Gofly-Md-X-Result"); got != "watching" {
			t.Fatalf("initial metadata = %q", got)
		}
		if got := rec.Header().Get("X-Gofly-Md-Secret-Bin"); got != "" {
			t.Fatalf("binary initial metadata forwarded: %q", got)
		}
		want := "event: message\ndata: {\"status\":\"SERVING\"}\n\nevent: message\ndata: {\"status\":\"NOT_SERVING\"}\n\nevent: trailers\ndata: {\"x-trailer\":\"complete\"}\n\n"
		if got := rec.Body.String(); got != want {
			t.Fatalf("SSE body = %q, want %q", got, want)
		}
		after := g.breakerFor(route).Snapshot()
		if after.Requests != before.Requests+1 || after.Success != before.Success+1 {
			t.Fatalf("breaker after completed stream = %+v, before = %+v", after, before)
		}
	})
	t.Run("server stream errors before headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/native/Watch", strings.NewReader(`{"service":"missing"}`))
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "service not found") {
			t.Fatalf("status=%d body=%s, want mapped NotFound", rec.Code, rec.Body.String())
		}
	})
	t.Run("server stream errors after a message", func(t *testing.T) {
		before := g.breakerFor(route).Snapshot()
		req := httptest.NewRequest(http.MethodPost, "/native/Watch", strings.NewReader(`{"service":"fail-after-message"}`))
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: message") ||
			!strings.Contains(rec.Body.String(), "event: error\ndata: {\"code\":\"data_loss\",\"error\":\"rpc error: code = DataLoss desc = watch failed\"}") {
			t.Fatalf("status=%d body=%s, want message then error event", rec.Code, rec.Body.String())
		}
		after := g.breakerFor(route).Snapshot()
		if after.Requests != before.Requests+1 || after.Failures != before.Failures+1 {
			t.Fatalf("breaker after failed stream = %+v, before = %+v", after, before)
		}
		if !hasEjectedEndpoint(g.passive.Snapshot(), route.Targets[0]) {
			t.Fatalf("passive health did not record failed stream: %+v", g.passive.Snapshot())
		}
	})
	t.Run("HTTP cancellation cancels gRPC stream", func(t *testing.T) {
		before := g.breakerFor(route).Snapshot()
		ts := httptest.NewServer(g)
		t.Cleanup(ts.Close)
		ctx, cancel := context.WithCancel(t.Context())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/native/Watch", strings.NewReader(`{"service":"cancel"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-watchStarted:
		case <-time.After(time.Second):
			t.Fatal("gRPC watch did not start")
		}
		cancel()
		_ = resp.Body.Close()
		select {
		case <-watchCanceled:
		case <-time.After(time.Second):
			t.Fatal("HTTP cancellation did not cancel gRPC watch")
		}
		deadline := time.Now().Add(time.Second)
		for {
			after := g.breakerFor(route).Snapshot()
			if after.Requests == before.Requests+1 {
				if after.Success != before.Success+1 {
					t.Fatalf("breaker after downstream cancellation = %+v, before = %+v", after, before)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("stream settlement was not recorded: %+v", after)
			}
			time.Sleep(time.Millisecond)
		}
	})
	t.Run("route deadline bounds gRPC stream", func(t *testing.T) {
		deadlineRoute := route
		deadlineRoute.Timeout = 10 * time.Millisecond
		deadlineGateway, err := New([]Route{deadlineRoute}, WithDescriptors(descriptor), WithTranscoderFactory(factory))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = deadlineGateway.Close() })
		req := httptest.NewRequest(http.MethodPost, "/native/Watch", strings.NewReader(`{"service":"deadline"}`))
		rec := httptest.NewRecorder()
		deadlineGateway.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: message") ||
			!strings.Contains(rec.Body.String(), `"code":"deadline_exceeded"`) {
			t.Fatalf("status=%d body=%s, want message then deadline error event", rec.Code, rec.Body.String())
		}
	})
	generic, err := g.transcoderFor(route.Targets[0], route)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("empty protobuf payloads", func(t *testing.T) {
		ctx := metadata.NewContext(t.Context(), metadata.MD{"X-Tenant": "tenant", "invalid key": "ignored"})
		for _, request := range []any{nil, json.RawMessage(" \n"), json.RawMessage("null")} {
			payload, _, err := generic.CallRaw(ctx, "/grpc.health.v1.Health/Check", request)
			if err != nil || !strings.Contains(string(payload), "SERVING") {
				t.Fatalf("request=%#v payload=%s err=%v", request, payload, err)
			}
		}
	})
	t.Run("unsupported JSON value", func(t *testing.T) {
		if _, _, err := generic.CallRaw(t.Context(), "grpc.health.v1.Health/Check", make(chan int)); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("unsupported JSON value: got %v, want InvalidArgument", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, _, err := generic.CallRaw(ctx, "grpc.health.v1.Health/Check", json.RawMessage(`{"service":"slow"}`)); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("deadline: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if generic.(*grpcTranscoder).conn.Conn().GetState() != connectivity.Shutdown {
		t.Fatal("grpc connection not closed")
	}
	if _, err := g.transcoderFor(route.Targets[0], route); err == nil {
		t.Fatal("closed gateway reconnected")
	}
}

func TestGRPCTranscoderFactoryBoundaries(t *testing.T) {
	if _, err := NewGRPCTranscoderFactory(nil); err == nil {
		t.Fatal("nil descriptors accepted")
	}
	if _, err := NewGRPCTranscoderFactory(&descriptorpb.FileDescriptorSet{}); err == nil {
		t.Fatal("empty descriptors accepted")
	}
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto)}}
	invalid := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{set.File[0], set.File[0]}}
	if _, err := NewGRPCTranscoderFactory(invalid); err == nil {
		t.Fatal("duplicate descriptors accepted")
	}
	factory, err := NewGRPCTranscoderFactory(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, endpoint string
	}{
		{name: "HTTP target", endpoint: "http://localhost:1234"},
		{name: "HTTPS target", endpoint: "https://localhost:1234"},
		{name: "empty target", endpoint: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if client, err := factory(tc.endpoint, Route{Transcode: TranscodeConfig{Protocol: "grpc"}}); err == nil {
				_ = client.(*grpcTranscoder).Close()
				t.Fatalf("target %q accepted", tc.endpoint)
			}
		})
	}
	t.Run("HTTP RPC fallback", func(t *testing.T) {
		client, err := factory("http://localhost:1234", Route{})
		if err != nil {
			t.Fatal(err)
		}
		if _, native := client.(*grpcTranscoder); native {
			t.Fatal("HTTP RPC route used native gRPC transport")
		}
	})
	if _, err := defaultTranscoderFactory("127.0.0.1:1234", Route{Transcode: TranscodeConfig{Protocol: "grpc"}}); err == nil {
		t.Fatal("native route fell back to HTTP")
	}
	g, err := New([]Route{{PathPrefix: "/a", Targets: []string{"http://upstream"}}}, WithTranscoderFactory(func(_ string, route Route) (rpc.GenericClient, error) {
		return &fakeGenericClient{payload: json.RawMessage(`{}`), md: metadata.MD{"route": route.Name}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	first, err := g.transcoderFor("same", Route{Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.transcoderFor("same", Route{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("route-sensitive clients shared")
	}
}
