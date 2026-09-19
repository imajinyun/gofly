package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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
	watchStarted     chan struct{}
	watchCanceled    chan struct{}
	watchUnavailable *atomic.Int64
}

type transcodeUploadService interface {
	UploadMarker()
}

type transcodeUploadServer struct {
	received     chan string
	canceled     chan struct{}
	chatCanceled chan struct{}
	once         sync.Once
	chatOnce     sync.Once
	failures     atomic.Int64
	chatFailures atomic.Int64
	chatCancels  atomic.Int64
}

func (*transcodeUploadServer) UploadMarker() {}

func (s *transcodeUploadServer) upload(stream stdgrpc.ServerStream) error {
	md, _ := grpcmetadata.FromIncomingContext(stream.Context())
	if values := md.Get("x-tenant"); len(values) != 1 || values[0] != "tenant" {
		return status.Error(codes.PermissionDenied, "tenant missing")
	}
	count := 0
	for {
		request := new(healthpb.HealthCheckRequest)
		err := stream.RecvMsg(request)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(stream.Context().Err(), context.Canceled) && s.canceled != nil {
				s.once.Do(func() { close(s.canceled) })
			}
			return err
		}
		count++
		if request.Service == "fail" {
			s.failures.Add(1)
			return status.Error(codes.DataLoss, "upload failed")
		}
		if request.Service == "deadline" {
			<-stream.Context().Done()
			return status.FromContextError(stream.Context().Err()).Err()
		}
		if s.received != nil {
			s.received <- request.Service
		}
	}
	if count == 0 {
		return status.Error(codes.InvalidArgument, "upload requires messages")
	}
	if err := stream.SetHeader(grpcmetadata.Pairs("x-result", "uploaded", "secret-bin", "private")); err != nil {
		return err
	}
	stream.SetTrailer(grpcmetadata.Pairs("x-count", fmt.Sprint(count), "secret-bin", "private"))
	return stream.SendMsg(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING})
}

func (s *transcodeUploadServer) chat(stream stdgrpc.ServerStream) error {
	if err := stream.SendHeader(grpcmetadata.Pairs("x-result", "chatting", "secret-bin", "private")); err != nil {
		return err
	}
	holdAfterHalfClose := false
	for {
		request := new(healthpb.HealthCheckRequest)
		err := stream.RecvMsg(request)
		if errors.Is(err, io.EOF) {
			if holdAfterHalfClose {
				<-stream.Context().Done()
				s.chatCancels.Add(1)
				return status.FromContextError(stream.Context().Err()).Err()
			}
			stream.SetTrailer(grpcmetadata.Pairs("x-chat", "complete", "secret-bin", "private"))
			return nil
		}
		if err != nil {
			if errors.Is(stream.Context().Err(), context.Canceled) && s.chatCanceled != nil {
				s.chatCancels.Add(1)
				s.chatOnce.Do(func() { close(s.chatCanceled) })
			}
			return err
		}
		if request.Service == "fail" {
			s.chatFailures.Add(1)
			return status.Error(codes.DataLoss, "chat failed")
		}
		if request.Service == "hold" {
			holdAfterHalfClose = true
		}
		responseStatus := healthpb.HealthCheckResponse_SERVING
		if request.Service == "second" {
			responseStatus = healthpb.HealthCheckResponse_NOT_SERVING
		}
		if err := stream.SendMsg(&healthpb.HealthCheckResponse{Status: responseStatus}); err != nil {
			return err
		}
	}
}

func transcodeUploadHandler(server any, stream stdgrpc.ServerStream) error {
	return server.(*transcodeUploadServer).upload(stream)
}

func transcodeChatHandler(server any, stream stdgrpc.ServerStream) error {
	return server.(*transcodeUploadServer).chat(stream)
}

var transcodeUploadServiceDesc = stdgrpc.ServiceDesc{
	ServiceName: "gateway.test.Streams",
	HandlerType: (*transcodeUploadService)(nil),
	Streams: []stdgrpc.StreamDesc{
		{StreamName: "Upload", Handler: transcodeUploadHandler, ClientStreams: true},
		{StreamName: "Chat", Handler: transcodeChatHandler, ClientStreams: true, ServerStreams: true},
	},
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
	case "unavailable-before-headers":
		if s.watchUnavailable != nil {
			s.watchUnavailable.Add(1)
		}
		return status.Error(codes.Unavailable, "watch unavailable before headers")
	case "unavailable-after-headers":
		if err := stream.SendHeader(grpcmetadata.Pairs("x-result", "watching")); err != nil {
			return err
		}
		if s.watchUnavailable != nil {
			s.watchUnavailable.Add(1)
		}
		return status.Error(codes.Unavailable, "watch unavailable after headers")
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
	if req.Service == "mapping-fail-after-message" {
		if err := stream.Send(&healthpb.HealthCheckResponse{}); err != nil {
			return err
		}
		return stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING})
	}
	if req.Service == "mapping-limit-after-message" {
		if err := stream.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}); err != nil {
			return err
		}
		return stream.Send(&healthpb.HealthCheckResponse{})
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
	watchUnavailable := new(atomic.Int64)
	healthpb.RegisterHealthServer(server, transcodeHealthServer{watchStarted: watchStarted, watchCanceled: watchCanceled, watchUnavailable: watchUnavailable})
	uploadReceived := make(chan string, 8)
	uploadCanceled := make(chan struct{})
	chatCanceled := make(chan struct{})
	uploadService := &transcodeUploadServer{received: uploadReceived, canceled: uploadCanceled, chatCanceled: chatCanceled}
	server.RegisterService(&transcodeUploadServiceDesc, uploadService)
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
		&descriptorpb.MethodDescriptorProto{Name: proto.String("Chat"), InputType: proto.String(".grpc.health.v1.HealthCheckRequest"), OutputType: proto.String(".grpc.health.v1.HealthCheckResponse"), ClientStreaming: proto.Bool(true), ServerStreaming: proto.Bool(true)},
	)
	streamFile := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("gateway_test_streams.proto"),
		Package:    proto.String("gateway.test"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{healthFile.GetName()},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("Streams"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{Name: proto.String("Upload"), InputType: proto.String(".grpc.health.v1.HealthCheckRequest"), OutputType: proto.String(".grpc.health.v1.HealthCheckResponse"), ClientStreaming: proto.Bool(true)},
				{Name: proto.String("Chat"), InputType: proto.String(".grpc.health.v1.HealthCheckRequest"), OutputType: proto.String(".grpc.health.v1.HealthCheckResponse"), ClientStreaming: proto.Bool(true), ServerStreaming: proto.Bool(true)},
			},
		}},
	}
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{healthFile, streamFile}}
	factory, err := NewGRPCTranscoderFactory(set, flygrpc.WithDialOptions(stdgrpc.WithTransportCredentials(insecure.NewCredentials()), stdgrpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() })))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := rpc.Descriptor{
		Name:    "grpc.health.v1.Health",
		Methods: []rpc.MethodDescriptor{{Name: "Check"}, {Name: "List"}},
		Streams: []rpc.StreamDescriptor{
			{Name: "Watch", Mode: rpc.StreamModeServerStream},
			{Name: "Chat", Mode: rpc.StreamModeBidiStream},
		},
	}
	uploadDescriptor := rpc.Descriptor{Name: "gateway.test.Streams", Streams: []rpc.StreamDescriptor{{Name: "Upload", Mode: rpc.StreamModeClientStream}, {Name: "Chat", Mode: rpc.StreamModeBidiStream}}}
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
		WithDescriptors(descriptor, uploadDescriptor),
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
	uploadRoute := route
	uploadRoute.Name = "native-upload"
	uploadRoute.PathPrefix = "/native-upload"
	uploadRoute.Transcode.Descriptor = uploadDescriptor.Name
	uploadRoute.Retry = RetryPolicy{Attempts: 3, Methods: []string{http.MethodPost}, Statuses: []int{http.StatusInternalServerError}}
	uploadGateway, err := New(
		[]Route{uploadRoute},
		WithDescriptors(uploadDescriptor),
		WithTranscoderFactory(factory),
		WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = uploadGateway.Close() })
	t.Run("client stream incrementally consumes NDJSON", func(t *testing.T) {
		ts := httptest.NewServer(uploadGateway)
		t.Cleanup(ts.Close)
		reader, writer := io.Pipe()
		t.Cleanup(func() { _ = writer.Close() })
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/native-upload/Upload", reader)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/x-ndjson")
		request.Header.Set("X-Tenant", "tenant")
		responseCh := make(chan *http.Response, 1)
		errorCh := make(chan error, 1)
		go func() {
			response, requestErr := http.DefaultClient.Do(request)
			if requestErr != nil {
				errorCh <- requestErr
				return
			}
			responseCh <- response
		}()
		if _, err := io.WriteString(writer, "{\"service\":\"first\"}\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-uploadReceived:
			if got != "first" {
				t.Fatalf("first streamed message = %q", got)
			}
		case err := <-errorCh:
			t.Fatal(err)
		case <-time.After(time.Second):
			t.Fatal("first NDJSON message was buffered until request EOF")
		}
		if _, err := io.WriteString(writer, "{\"service\":\"second\"}\n"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		var response *http.Response
		select {
		case response = <-responseCh:
		case err := <-errorCh:
			t.Fatal(err)
		case <-time.After(time.Second):
			t.Fatal("client-streaming response timed out")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "SERVING") {
			t.Fatalf("status=%d body=%s", response.StatusCode, body)
		}
		if response.Header.Get("X-Gofly-Md-X-Result") != "uploaded" || response.Header.Get("X-Gofly-Md-X-Count") != "2" {
			t.Fatalf("metadata headers = %v", response.Header)
		}
	})
	t.Run("client stream propagates cancellation", func(t *testing.T) {
		ts := httptest.NewServer(uploadGateway)
		t.Cleanup(ts.Close)
		reader, writer := io.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/native-upload/Upload", reader)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/x-ndjson")
		request.Header.Set("X-Tenant", "tenant")
		done := make(chan error, 1)
		go func() {
			response, requestErr := http.DefaultClient.Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
			done <- requestErr
		}()
		if _, err := io.WriteString(writer, "{\"service\":\"cancel\"}\n"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-uploadReceived:
		case <-time.After(5 * time.Second):
			t.Fatal("client stream did not receive message before cancellation")
		}
		before := uploadGateway.breakerFor(uploadRoute).Snapshot()
		cancel()
		select {
		case <-uploadCanceled:
		case <-time.After(5 * time.Second):
			t.Fatal("HTTP cancellation did not cancel client stream")
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("HTTP client did not return after cancellation")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			after := uploadGateway.breakerFor(uploadRoute).Snapshot()
			if after.Requests == before.Requests+1 {
				if after.Success != before.Success+1 {
					t.Fatalf("breaker after upload cancellation = %+v, before = %+v", after, before)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("upload cancellation settlement missing: %+v", after)
			}
			time.Sleep(time.Millisecond)
		}
	})
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		want        int
	}{
		{name: "content type required", contentType: "application/json", body: `{"service":"one"}`, want: http.StatusUnsupportedMediaType},
		{name: "malformed frame", contentType: "application/x-ndjson", body: "{\"service\":\"one\"}\n{bad}\n", want: http.StatusBadRequest},
		{name: "empty stream", contentType: "application/x-ndjson", body: " \n\n", want: http.StatusBadRequest},
		{name: "frame too large", contentType: "application/x-ndjson", body: "{\"service\":\"" + strings.Repeat("a", grpcClientStreamMaxFrameBytes) + "\"}\n", want: http.StatusRequestEntityTooLarge},
	} {
		t.Run("client stream "+tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/native-upload/Upload", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			request.Header.Set("X-Tenant", "tenant")
			recorder := httptest.NewRecorder()
			uploadGateway.ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status=%d body=%s want=%d", recorder.Code, recorder.Body.String(), tc.want)
			}
		})
	}
	t.Run("client stream protobuf mapping error is local", func(t *testing.T) {
		beforeBreaker := uploadGateway.breakerFor(uploadRoute).Snapshot()
		beforePassive := uploadGateway.passive.Snapshot()
		request := httptest.NewRequest(http.MethodPost, "/native-upload/Upload", strings.NewReader("{\"unknown\":true}\n"))
		request.Header.Set("Content-Type", "application/x-ndjson")
		recorder := httptest.NewRecorder()
		uploadGateway.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
		}
		if after := uploadGateway.breakerFor(uploadRoute).Snapshot(); after.Requests != beforeBreaker.Requests {
			t.Fatalf("local mapping error changed breaker: before=%+v after=%+v", beforeBreaker, after)
		}
		if after := uploadGateway.passive.Snapshot(); !reflect.DeepEqual(after, beforePassive) {
			t.Fatalf("local mapping error changed passive health: before=%+v after=%+v", beforePassive, after)
		}
	})
	t.Run("client stream upstream failure is not retried", func(t *testing.T) {
		beforeFailures := uploadService.failures.Load()
		beforeBreaker := uploadGateway.breakerFor(uploadRoute).Snapshot()
		request := httptest.NewRequest(http.MethodPost, "/native-upload/Upload", strings.NewReader("{\"service\":\"fail\"}\n"))
		request.Header.Set("Content-Type", "application/x-ndjson")
		request.Header.Set("X-Tenant", "tenant")
		recorder := httptest.NewRecorder()
		uploadGateway.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "upload failed") {
			t.Fatalf("status=%d body=%s, want mapped DataLoss", recorder.Code, recorder.Body.String())
		}
		if failures := uploadService.failures.Load() - beforeFailures; failures != 1 {
			t.Fatalf("client stream failed calls = %d, want exactly one without replay", failures)
		}
		if after := uploadGateway.breakerFor(uploadRoute).Snapshot(); after.Failures != beforeBreaker.Failures+1 {
			t.Fatalf("upstream failure did not update breaker: before=%+v after=%+v", beforeBreaker, after)
		}
		if !hasEjectedEndpoint(uploadGateway.passive.Snapshot(), uploadRoute.Targets[0]) {
			t.Fatalf("passive health did not record client stream failure: %+v", uploadGateway.passive.Snapshot())
		}
	})
	t.Run("client stream route deadline", func(t *testing.T) {
		deadlineRoute := uploadRoute
		deadlineRoute.Timeout = 10 * time.Millisecond
		deadlineGateway, err := New([]Route{deadlineRoute}, WithDescriptors(uploadDescriptor), WithTranscoderFactory(factory))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = deadlineGateway.Close() })
		request := httptest.NewRequest(http.MethodPost, "/native-upload/Upload", strings.NewReader("{\"service\":\"deadline\"}\n"))
		request.Header.Set("Content-Type", "application/x-ndjson")
		request.Header.Set("X-Tenant", "tenant")
		recorder := httptest.NewRecorder()
		deadlineGateway.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusGatewayTimeout || !strings.Contains(recorder.Body.String(), "deadline_exceeded") {
			t.Fatalf("status=%d body=%s, want route deadline", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("bidirectional stream interleaves messages and half closes", func(t *testing.T) {
		bidiRoute := uploadRoute
		bidiRoute.Name = "native-chat"
		bidiRoute.Method = http.MethodGet
		bidiRoute.PathPrefix = "/native-chat"
		bidiGateway, err := New([]Route{bidiRoute}, WithDescriptors(uploadDescriptor), WithTranscoderFactory(factory))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bidiGateway.Close() })
		server := httptest.NewServer(bidiGateway)
		t.Cleanup(server.Close)

		conn, rw := dialGatewayWebSocket(t, server.URL, "/native-chat/Chat", "Sec-WebSocket-Protocol: gofly.grpc.bidi.v1")
		defer conn.Close()
		writeGatewayClientFrame(t, rw, 1, []byte("{\"type\":\"message\",\"data\":{\"service\":\"first\"}}"))
		messageType, payload := readGatewayServerFrame(t, rw)
		if messageType != 1 || !strings.Contains(string(payload), "\"type\":\"headers\"") || !strings.Contains(string(payload), "\"x-result\":\"chatting\"") {
			t.Fatalf("first websocket envelope type=%d payload=%s, want headers", messageType, payload)
		}
		messageType, payload = readGatewayServerFrame(t, rw)
		if messageType != 1 || string(payload) != "{\"type\":\"message\",\"data\":{\"status\":\"SERVING\"}}" {
			t.Fatalf("first response type=%d payload=%s", messageType, payload)
		}
		writeGatewayClientFrame(t, rw, 1, []byte("{\"type\":\"message\",\"data\":{\"service\":\"second\"}}"))
		messageType, payload = readGatewayServerFrame(t, rw)
		if messageType != 1 || string(payload) != "{\"type\":\"message\",\"data\":{\"status\":\"NOT_SERVING\"}}" {
			t.Fatalf("second response type=%d payload=%s", messageType, payload)
		}
		writeGatewayClientFrame(t, rw, 1, []byte("{\"type\":\"half_close\"}"))
		messageType, payload = readGatewayServerFrame(t, rw)
		if messageType != 1 || !strings.Contains(string(payload), "\"type\":\"trailers\"") || !strings.Contains(string(payload), "\"x-chat\":\"complete\"") {
			t.Fatalf("trailers type=%d payload=%s", messageType, payload)
		}
		messageType, payload = readGatewayServerFrame(t, rw)
		if messageType != 1 || string(payload) != "{\"type\":\"complete\"}" {
			t.Fatalf("complete type=%d payload=%s", messageType, payload)
		}
	})
	t.Run("bidirectional stream requires subprotocol", func(t *testing.T) {
		bidiRoute := uploadRoute
		bidiRoute.Name = "native-chat-subprotocol"
		bidiRoute.Method = http.MethodGet
		bidiRoute.PathPrefix = "/native-chat-subprotocol"
		bidiGateway, err := New([]Route{bidiRoute}, WithDescriptors(uploadDescriptor), WithTranscoderFactory(factory))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bidiGateway.Close() })
		server := httptest.NewServer(bidiGateway)
		t.Cleanup(server.Close)
		u, err := url.Parse(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := fmt.Fprintf(conn, "GET /native-chat-subprotocol/Chat HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: Z29mbHk=\r\n\r\n", u.Host); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400 without bidi subprotocol", response.StatusCode)
		}
	})
	t.Run("bidirectional stream maps local and upstream errors", func(t *testing.T) {
		bidiRoute := uploadRoute
		bidiRoute.Name = "native-chat-errors"
		bidiRoute.Method = http.MethodGet
		bidiRoute.PathPrefix = "/native-chat-errors"
		bidiRoute.Breaker.MinRequests = 100
		bidiGateway, err := New([]Route{bidiRoute}, WithDescriptors(uploadDescriptor), WithTranscoderFactory(factory), WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bidiGateway.Close() })
		server := httptest.NewServer(bidiGateway)
		t.Cleanup(server.Close)

		before := bidiGateway.breakerFor(bidiRoute).Snapshot()
		conn, rw := dialGatewayWebSocket(t, server.URL, "/native-chat-errors/Chat", "Sec-WebSocket-Protocol: gofly.grpc.bidi.v1")
		writeGatewayClientFrame(t, rw, 1, []byte("{\"type\":\"message\",\"data\":{\"unknown\":true}}"))
		_, payload := readGatewayServerFrame(t, rw)
		if strings.Contains(string(payload), "\"type\":\"headers\"") {
			_, payload = readGatewayServerFrame(t, rw)
		}
		if !strings.Contains(string(payload), "\"type\":\"error\"") || !strings.Contains(string(payload), "invalid protobuf JSON request") {
			t.Fatalf("local error envelope=%s", payload)
		}
		_ = conn.Close()
		if after := bidiGateway.breakerFor(bidiRoute).Snapshot(); after.Requests != before.Requests+1 || after.Success != before.Success+1 {
			t.Fatalf("local error changed breaker as upstream failure: before=%+v after=%+v", before, after)
		}

		before = bidiGateway.breakerFor(bidiRoute).Snapshot()
		beforeFailures := uploadService.chatFailures.Load()
		conn, rw = dialGatewayWebSocket(t, server.URL, "/native-chat-errors/Chat", "Sec-WebSocket-Protocol: gofly.grpc.bidi.v1")
		writeGatewayClientFrame(t, rw, 1, []byte("{\"type\":\"message\",\"data\":{\"service\":\"fail\"}}"))
		_, payload = readGatewayServerFrame(t, rw)
		if strings.Contains(string(payload), "\"type\":\"headers\"") {
			_, payload = readGatewayServerFrame(t, rw)
		}
		if !strings.Contains(string(payload), "\"type\":\"error\"") || !strings.Contains(string(payload), "chat failed") {
			t.Fatalf("upstream error envelope=%s", payload)
		}
		_ = conn.Close()
		if failures := uploadService.chatFailures.Load() - beforeFailures; failures != 1 {
			t.Fatalf("bidirectional failed calls = %d, want exactly one without replay", failures)
		}
		deadline := time.Now().Add(time.Second)
		for {
			after := bidiGateway.breakerFor(bidiRoute).Snapshot()
			if after.Requests == before.Requests+1 {
				if after.Failures != before.Failures+1 {
					t.Fatalf("upstream failure breaker: before=%+v after=%+v", before, after)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("upstream failure was not settled: %+v", after)
			}
			time.Sleep(time.Millisecond)
		}
		if !hasEjectedEndpoint(bidiGateway.passive.Snapshot(), bidiRoute.Targets[0]) {
			t.Fatalf("passive health did not record bidirectional failure: %+v", bidiGateway.passive.Snapshot())
		}
	})
	t.Run("bidirectional stream disconnect cancels upstream", func(t *testing.T) {
		bidiRoute := uploadRoute
		bidiRoute.Name = "native-chat-cancel"
		bidiRoute.Method = http.MethodGet
		bidiRoute.PathPrefix = "/native-chat-cancel"
		bidiGateway, err := New([]Route{bidiRoute}, WithDescriptors(uploadDescriptor), WithTranscoderFactory(factory))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bidiGateway.Close() })
		server := httptest.NewServer(bidiGateway)
		t.Cleanup(server.Close)
		before := bidiGateway.breakerFor(bidiRoute).Snapshot()
		conn, rw := dialGatewayWebSocket(t, server.URL, "/native-chat-cancel/Chat", "Sec-WebSocket-Protocol: gofly.grpc.bidi.v1")
		writeGatewayClientFrame(t, rw, 1, []byte("{\"type\":\"message\",\"data\":{\"service\":\"first\"}}"))
		_, _ = readGatewayServerFrame(t, rw)
		_, _ = readGatewayServerFrame(t, rw)
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-chatCanceled:
		case <-time.After(time.Second):
			t.Fatal("websocket disconnect did not cancel upstream bidirectional stream")
		}
		deadline := time.Now().Add(time.Second)
		for {
			after := bidiGateway.breakerFor(bidiRoute).Snapshot()
			if after.Requests == before.Requests+1 {
				if after.Success != before.Success+1 {
					t.Fatalf("disconnect poisoned breaker: before=%+v after=%+v", before, after)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("disconnect settlement missing: %+v", after)
			}
			time.Sleep(time.Millisecond)
		}
	})
	t.Run("bidirectional stream half close still observes disconnect", func(t *testing.T) {
		bidiRoute := uploadRoute
		bidiRoute.Name = "native-chat-half-close-cancel"
		bidiRoute.Method = http.MethodGet
		bidiRoute.PathPrefix = "/native-chat-half-close-cancel"
		bidiGateway, err := New([]Route{bidiRoute}, WithDescriptors(uploadDescriptor), WithTranscoderFactory(factory))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = bidiGateway.Close() })
		server := httptest.NewServer(bidiGateway)
		t.Cleanup(server.Close)
		before := uploadService.chatCancels.Load()
		conn, rw := dialGatewayWebSocket(t, server.URL, "/native-chat-half-close-cancel/Chat", "Sec-WebSocket-Protocol: gofly.grpc.bidi.v1")
		writeGatewayClientFrame(t, rw, 1, []byte("{\"type\":\"message\",\"data\":{\"service\":\"hold\"}}"))
		_, _ = readGatewayServerFrame(t, rw)
		_, _ = readGatewayServerFrame(t, rw)
		writeGatewayClientFrame(t, rw, 1, []byte("{\"type\":\"half_close\"}"))
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for uploadService.chatCancels.Load() == before {
			if time.Now().After(deadline) {
				t.Fatal("websocket disconnect after half close did not cancel upstream stream")
			}
			time.Sleep(time.Millisecond)
		}
	})
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
	t.Run("server stream first receive failure is not retried", func(t *testing.T) {
		for _, service := range []string{"unavailable-before-headers", "unavailable-after-headers"} {
			t.Run(service, func(t *testing.T) {
				retryRoute := route
				retryRoute.Name = "native-stream-no-retry-" + service
				retryRoute.PathPrefix = "/native-stream-no-retry"
				retryRoute.Retry = RetryPolicy{Attempts: 3, Methods: []string{http.MethodPost}}
				retryGateway, err := New(
					[]Route{retryRoute},
					WithDescriptors(descriptor),
					WithTranscoderFactory(factory),
					WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}),
				)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = retryGateway.Close() })
				beforeCalls := watchUnavailable.Load()
				beforeBreaker := retryGateway.breakerFor(retryRoute).Snapshot()
				req := httptest.NewRequest(http.MethodPost, "/native-stream-no-retry/Watch", strings.NewReader(`{"service":"`+service+`"}`))
				rec := httptest.NewRecorder()
				retryGateway.ServeHTTP(rec, req)
				if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "watch unavailable") {
					t.Fatalf("status=%d body=%s, want mapped Unavailable", rec.Code, rec.Body.String())
				}
				if calls := watchUnavailable.Load() - beforeCalls; calls != 1 {
					t.Fatalf("server stream calls = %d, want exactly one after stream creation", calls)
				}
				afterBreaker := retryGateway.breakerFor(retryRoute).Snapshot()
				if afterBreaker.Failures != beforeBreaker.Failures+1 {
					t.Fatalf("first receive failure breaker: before=%+v after=%+v", beforeBreaker, afterBreaker)
				}
				if !hasEjectedEndpoint(retryGateway.passive.Snapshot(), retryRoute.Targets[0]) {
					t.Fatalf("passive health did not record first receive failure: %+v", retryGateway.passive.Snapshot())
				}
			})
		}
	})
	t.Run("server stream maps first response before committing SSE", func(t *testing.T) {
		mappingRoute := route
		mappingRoute.Name = "native-stream-first-mapping"
		mappingRoute.PathPrefix = "/native-stream-first-mapping"
		profile := TranscodeProfile{
			Descriptor:       descriptor.Name,
			DescriptorMethod: "Watch",
			ResponseMappings: []TranscodePayloadMapping{
				{Source: "body.status", Target: "data"},
				{Source: "body.status", Target: "data.value"},
			},
		}
		mappingGateway, err := New(
			[]Route{mappingRoute},
			WithDescriptors(descriptor),
			WithTranscodeProfiles(profile),
			WithTranscoderFactory(factory),
			WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mappingGateway.Close() })
		beforeBreaker := mappingGateway.breakerFor(mappingRoute).Snapshot()
		beforePassive := mappingGateway.passive.Snapshot()
		req := httptest.NewRequest(http.MethodPost, "/native-stream-first-mapping/Watch", strings.NewReader(`{"service":"ok"}`))
		rec := httptest.NewRecorder()
		mappingGateway.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "event: ") || !strings.Contains(rec.Body.String(), "conflicts with existing scalar") {
			t.Fatalf("status=%d body=%s, want pre-SSE mapping error", rec.Code, rec.Body.String())
		}
		afterBreaker := mappingGateway.breakerFor(mappingRoute).Snapshot()
		if afterBreaker.Requests != beforeBreaker.Requests+1 || afterBreaker.Success != beforeBreaker.Success+1 {
			t.Fatalf("local first-frame mapping error changed upstream health: before=%+v after=%+v", beforeBreaker, afterBreaker)
		}
		if after := mappingGateway.passive.Snapshot(); len(after) != 1 || after[0].Failures != 0 || after[0].Ejected {
			t.Fatalf("local first-frame mapping error poisoned passive health: before=%+v after=%+v", beforePassive, after)
		}
		runtime := mappingGateway.RuntimeSnapshot()
		if len(runtime.Routes) != 1 || runtime.Routes[0].TranscodeRuntime.LastErrorStage != "response" {
			t.Fatalf("first-frame mapping runtime = %+v", runtime.Routes)
		}
	})
	t.Run("server stream records later response mapping errors", func(t *testing.T) {
		mappingRoute := route
		mappingRoute.Name = "native-stream-later-mapping"
		mappingRoute.PathPrefix = "/native-stream-later-mapping"
		profile := TranscodeProfile{
			Descriptor:       descriptor.Name,
			DescriptorMethod: "Watch",
			ResponseMappings: []TranscodePayloadMapping{
				{Source: "body.status", Target: "data"},
				{Source: "body.status", Target: "data.value"},
			},
		}
		mappingGateway, err := New(
			[]Route{mappingRoute},
			WithDescriptors(descriptor),
			WithTranscodeProfiles(profile),
			WithTranscoderFactory(factory),
			WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mappingGateway.Close() })
		beforeBreaker := mappingGateway.breakerFor(mappingRoute).Snapshot()
		beforePassive := mappingGateway.passive.Snapshot()
		req := httptest.NewRequest(http.MethodPost, "/native-stream-later-mapping/Watch", strings.NewReader(`{"service":"mapping-fail-after-message"}`))
		rec := httptest.NewRecorder()
		mappingGateway.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: message\ndata: {}") || !strings.Contains(rec.Body.String(), "event: error") {
			t.Fatalf("status=%d body=%s, want message then mapping error", rec.Code, rec.Body.String())
		}
		afterBreaker := mappingGateway.breakerFor(mappingRoute).Snapshot()
		if afterBreaker.Requests != beforeBreaker.Requests+1 || afterBreaker.Success != beforeBreaker.Success+1 {
			t.Fatalf("local later-frame mapping error changed upstream health: before=%+v after=%+v", beforeBreaker, afterBreaker)
		}
		if after := mappingGateway.passive.Snapshot(); len(after) != 1 || after[0].Failures != 0 || after[0].Ejected {
			t.Fatalf("local later-frame mapping error poisoned passive health: before=%+v after=%+v", beforePassive, after)
		}
		runtime := mappingGateway.RuntimeSnapshot()
		if len(runtime.Routes) != 1 || runtime.Routes[0].TranscodeRuntime.LastErrorStage != "response" || !strings.Contains(runtime.Routes[0].TranscodeRuntime.LastError, "conflicts with existing scalar") {
			t.Fatalf("later-frame mapping runtime = %+v", runtime.Routes)
		}
	})
	t.Run("server stream rejects an oversized first response before SSE", func(t *testing.T) {
		limitRoute := route
		limitRoute.Name = "native-stream-first-limit"
		limitRoute.PathPrefix = "/native-stream-first-limit"
		profile := TranscodeProfile{
			Descriptor:       descriptor.Name,
			DescriptorMethod: "Watch",
			ResponseMappings: []TranscodePayloadMapping{{Source: "body.missing", Target: "data", Default: strings.Repeat("a", grpcServerStreamMaxFrameBytes+1)}},
		}
		limitGateway, err := New([]Route{limitRoute}, WithDescriptors(descriptor), WithTranscodeProfiles(profile), WithTranscoderFactory(factory), WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = limitGateway.Close() })
		before := limitGateway.breakerFor(limitRoute).Snapshot()
		req := httptest.NewRequest(http.MethodPost, "/native-stream-first-limit/Watch", strings.NewReader("{\"service\":\"ok\"}"))
		rec := httptest.NewRecorder()
		limitGateway.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests || strings.Contains(rec.Body.String(), "event: ") || !strings.Contains(rec.Body.String(), "server stream response exceeds limits") {
			t.Fatalf("status=%d body=%s, want pre-SSE ResourceExhausted", rec.Code, rec.Body.String())
		}
		after := limitGateway.breakerFor(limitRoute).Snapshot()
		if after.Requests != before.Requests+1 || after.Success != before.Success+1 {
			t.Fatalf("local first-response limit changed upstream health: before=%+v after=%+v", before, after)
		}
		if health := limitGateway.passive.Snapshot(); len(health) != 1 || health[0].Failures != 0 || health[0].Ejected {
			t.Fatalf("local first-response limit poisoned passive health: %+v", health)
		}
	})
	t.Run("server stream rejects an oversized later response as SSE error", func(t *testing.T) {
		limitRoute := route
		limitRoute.Name = "native-stream-later-limit"
		limitRoute.PathPrefix = "/native-stream-later-limit"
		profile := TranscodeProfile{
			Descriptor:       descriptor.Name,
			DescriptorMethod: "Watch",
			ResponseMappings: []TranscodePayloadMapping{{Source: "body.status", Target: "data", Default: strings.Repeat("a", grpcServerStreamMaxFrameBytes+1)}},
		}
		limitGateway, err := New([]Route{limitRoute}, WithDescriptors(descriptor), WithTranscodeProfiles(profile), WithTranscoderFactory(factory), WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = limitGateway.Close() })
		before := limitGateway.breakerFor(limitRoute).Snapshot()
		req := httptest.NewRequest(http.MethodPost, "/native-stream-later-limit/Watch", strings.NewReader("{\"service\":\"mapping-limit-after-message\"}"))
		rec := httptest.NewRecorder()
		limitGateway.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: message") || !strings.Contains(rec.Body.String(), "resource_exhausted") {
			t.Fatalf("status=%d body=%s, want message then ResourceExhausted SSE error", rec.Code, rec.Body.String())
		}
		after := limitGateway.breakerFor(limitRoute).Snapshot()
		if after.Requests != before.Requests+1 || after.Success != before.Success+1 {
			t.Fatalf("local later-response limit changed upstream health: before=%+v after=%+v", before, after)
		}
		if health := limitGateway.passive.Snapshot(); len(health) != 1 || health[0].Failures != 0 || health[0].Ejected {
			t.Fatalf("local later-response limit poisoned passive health: %+v", health)
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

func TestDecodeNDJSONStreamLimits(t *testing.T) {
	assertLimit := func(t *testing.T, body string, capacity int, wantCode rpc.Code) {
		t.Helper()
		frames := make(chan clientStreamFrame, capacity)
		decodeNDJSONStream(t.Context(), strings.NewReader(body), nil, frames)
		var terminal error
		for frame := range frames {
			if frame.err != nil {
				terminal = frame.err
			}
		}
		if terminal == nil || rpc.CodeOf(terminal) != wantCode {
			t.Fatalf("terminal error = %v, want %s", terminal, wantCode)
		}
	}

	t.Run("message count", func(t *testing.T) {
		var body strings.Builder
		body.Grow((grpcClientStreamMaxMessages + 1) * 3)
		for range grpcClientStreamMaxMessages + 1 {
			body.WriteString("{}\n")
		}
		assertLimit(t, body.String(), grpcClientStreamMaxMessages+1, rpc.CodeResourceExhausted)
	})

	t.Run("total payload bytes", func(t *testing.T) {
		frame := "{\"service\":\"" + strings.Repeat("a", grpcClientStreamMaxFrameBytes/2) + "\"}"
		frameCount := grpcClientStreamMaxBodyBytes/len(frame) + 1
		body := strings.Repeat(frame+"\n", frameCount)
		assertLimit(t, body, frameCount+1, rpc.CodeResourceExhausted)
	})
}

func TestServerStreamLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limits  serverStreamLimits
		payload []byte
	}{
		{name: "message count", limits: serverStreamLimits{messages: grpcServerStreamMaxMessages}, payload: []byte("{}")},
		{name: "total payload bytes", limits: serverStreamLimits{bytes: grpcServerStreamMaxBodyBytes - 1}, payload: []byte("{}")},
		{name: "frame bytes", payload: make([]byte, grpcServerStreamMaxFrameBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.limits.accept(tc.payload) {
				t.Fatalf("accepted payload bytes=%d with limits=%+v", len(tc.payload), tc.limits)
			}
		})
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

func TestBidirectionalStreamLimits(t *testing.T) {
	tests := []struct {
		name       string
		count      int
		totalBytes int
		frameBytes int
		want       bool
	}{
		{name: "within all limits", count: grpcClientStreamMaxMessages, totalBytes: grpcClientStreamMaxBodyBytes, frameBytes: grpcClientStreamMaxFrameBytes},
		{name: "message count exceeded", count: grpcClientStreamMaxMessages + 1, want: true},
		{name: "aggregate payload exceeded", totalBytes: grpcClientStreamMaxBodyBytes + 1, want: true},
		{name: "frame exceeded", frameBytes: grpcClientStreamMaxFrameBytes + 1, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bidirectionalStreamLimitExceeded(tc.count, tc.totalBytes, tc.frameBytes); got != tc.want {
				t.Fatalf("bidirectionalStreamLimitExceeded(%d, %d, %d) = %t, want %t", tc.count, tc.totalBytes, tc.frameBytes, got, tc.want)
			}
		})
	}
}
