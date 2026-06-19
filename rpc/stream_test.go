package rpc

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofly/gofly/core/auth"
	coregovernance "github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/metadata"
)

func TestRPCStreamEcho(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "Echo",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			var req helloReq
			if err := stream.Recv(&req); err != nil {
				return err
			}
			return stream.Send(helloResp{Message: "hello " + req.Name})
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/Echo")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Send(helloReq{Name: "gofly"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var resp helloResp
	if err := stream.Recv(&resp); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Message != "hello gofly" {
		t.Fatalf("response = %q, want hello gofly", resp.Message)
	}
}

func TestRPCStreamPropagatesClientMetadata(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "EchoMetadata",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			md, ok := metadata.FromContext(ctx)
			if !ok || md.Get("x-tenant") != "beta" || md.Get(metadata.RequestIDKey) != "rid-stream" {
				t.Fatalf("metadata = %#v, want propagated tenant and request id", md)
			}
			var req helloReq
			if err := stream.Recv(&req); err != nil {
				return err
			}
			return stream.Send(helloResp{Message: md.Get("x-tenant") + ":" + md.Get(metadata.RequestIDKey)})
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.Append(context.Background(), "x-tenant", "beta", metadata.RequestIDKey, "rid-stream")
	stream, err := c.Stream(ctx, "chat/EchoMetadata")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Send(helloReq{Name: "gofly"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var resp helloResp
	if err := stream.Recv(&resp); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Message != "beta:rid-stream" {
		t.Fatalf("response = %q, want beta:rid-stream", resp.Message)
	}
}

func TestRPCStreamGovernanceMatchesIncomingMetadata(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "stream-metadata-canary",
		Transport: coregovernance.TransportRPC,
		Service:   "chat",
		Method:    "Echo",
		Policy: coregovernance.Policy{
			Metadata: map[string]string{"x-policy": "enabled"},
			Canary: coregovernance.CanaryPolicy{
				MatchHeaders: map[string]string{"X-Tenant": "beta"},
				Service:      "chat-canary",
				Headers:      map[string]string{"x-lane": "blue"},
			},
		},
	})
	s := newGovernedStreamServer(t, rules, func(ctx context.Context, stream *Stream) error {
		md, ok := metadata.FromContext(ctx)
		if !ok || md.Get("x-tenant") != "beta" || md.Get("x-policy") != "enabled" || md.Get(coregovernance.HeaderCanary) != "true" || md.Get(coregovernance.HeaderCanaryService) != "chat-canary" || md.Get("x-lane") != "blue" {
			t.Fatalf("metadata = %#v, want incoming metadata plus governance canary metadata", md)
		}
		var req helloReq
		if err := stream.Recv(&req); err != nil {
			return err
		}
		return stream.Send(helloResp{Message: md.Get("x-policy") + ":" + md.Get("x-lane")})
	})
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.Append(context.Background(), "x-tenant", "beta")
	stream, err := c.Stream(ctx, "chat/Echo")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Send(helloReq{Name: "gofly"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var resp helloResp
	if err := stream.Recv(&resp); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Message != "enabled:blue" {
		t.Fatalf("response = %q, want enabled:blue", resp.Message)
	}
}

func TestRPCStreamMiddlewareChain(t *testing.T) {
	globalMiddleware := func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, stream *Stream) error {
			ctx = metadata.Append(ctx, "stream-chain", "global")
			return next(ctx, stream)
		}
	}
	methodMiddleware := func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, stream *Stream) error {
			md, _ := metadata.FromContext(ctx)
			ctx = metadata.Append(ctx, "stream-chain", md.Get("stream-chain")+":method")
			return next(ctx, stream)
		}
	}
	s := NewServer(WithServerStreamMiddleware(globalMiddleware))
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:        "EchoMiddleware",
		NewMessage:  func() any { return new(helloReq) },
		Middlewares: []StreamMiddleware{methodMiddleware},
		Handler: func(ctx context.Context, stream *Stream) error {
			md, _ := metadata.FromContext(ctx)
			if md.Get("stream-chain") != "global:method" {
				return NewError(CodeInternal, "stream middleware chain not applied")
			}
			var req helloReq
			if err := stream.Recv(&req); err != nil {
				return err
			}
			return stream.Send(helloResp{Message: md.Get("stream-chain")})
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/EchoMiddleware")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Send(helloReq{Name: "gofly"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var resp helloResp
	if err := stream.Recv(&resp); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Message != "global:method" {
		t.Fatalf("response = %q, want global:method", resp.Message)
	}
}

func TestRPCClientStreamMiddlewareChain(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "EchoClientMiddleware",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			md, _ := metadata.FromContext(ctx)
			if md.Get("client-stream-chain") != "client:stream" {
				return NewError(CodeInternal, "client stream middleware chain not applied")
			}
			var req helloReq
			if err := stream.Recv(&req); err != nil {
				return err
			}
			return stream.Send(helloResp{Message: md.Get("client-stream-chain")})
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	first := func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			ctx = metadata.Append(ctx, "client-stream-chain", "client")
			return next(ctx, method)
		}
	}
	second := func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			md, _ := metadata.FromContext(ctx)
			ctx = metadata.Append(ctx, "client-stream-chain", md.Get("client-stream-chain")+":stream")
			return next(ctx, method)
		}
	}
	c, err := NewClient(ts.URL, WithClientStreamMiddleware(first), WithClientStreamMiddleware(second))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/EchoClientMiddleware")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Send(helloReq{Name: "gofly"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var resp helloResp
	if err := stream.Recv(&resp); err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Message != "client:stream" {
		t.Fatalf("response = %q, want client:stream", resp.Message)
	}
}

func newMiddlewareTestStream(t *testing.T) *Stream {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	rw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))
	return newStream(client, rw, JSONCodec{})
}

func TestRPCStreamRequestIDMiddlewareAddsAndPreservesID(t *testing.T) {
	stream := newMiddlewareTestStream(t)
	defer stream.Close()

	var generated string
	mw := StreamRequestIDMiddleware()(func(ctx context.Context, stream *Stream) error {
		generated = metadata.RequestIDFromContext(ctx)
		return nil
	})
	if err := mw(context.Background(), stream); err != nil {
		t.Fatalf("StreamRequestIDMiddleware generated call: %v", err)
	}
	if generated == "" {
		t.Fatal("StreamRequestIDMiddleware did not generate request id")
	}

	var preserved string
	mw = StreamRequestIDMiddleware()(func(ctx context.Context, stream *Stream) error {
		preserved = metadata.RequestIDFromContext(ctx)
		return nil
	})
	ctx := metadata.Append(context.Background(), metadata.RequestIDKey, "rid-stream-existing")
	if err := mw(ctx, stream); err != nil {
		t.Fatalf("StreamRequestIDMiddleware preserved call: %v", err)
	}
	if preserved != "rid-stream-existing" {
		t.Fatalf("request id = %q, want rid-stream-existing", preserved)
	}
}

func TestRPCClientStreamMaxConcurrencyReleasesOnClose(t *testing.T) {
	var opened int
	mw := ClientStreamMaxConcurrencyMiddleware(1)(func(ctx context.Context, method string) (*Stream, error) {
		opened++
		return newMiddlewareTestStream(t), nil
	})

	first, err := mw(context.Background(), "chat/Echo")
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if _, err := mw(context.Background(), "chat/Echo"); CodeOf(err) != CodeUnavailable {
		_ = first.Close()
		t.Fatalf("second stream error = %v code=%s, want unavailable", err, CodeOf(err))
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first stream: %v", err)
	}
	third, err := mw(context.Background(), "chat/Echo")
	if err != nil {
		t.Fatalf("third stream after close: %v", err)
	}
	defer third.Close()
	if opened != 2 {
		t.Fatalf("opened streams = %d, want 2", opened)
	}
}

func TestRPCClientStreamBearerTokenMiddlewareAddsMetadata(t *testing.T) {
	var token string
	mw := ClientStreamBearerTokenMiddleware("secret")(func(ctx context.Context, method string) (*Stream, error) {
		md, _ := metadata.FromContext(ctx)
		token = md.Get(auth.MetadataKey)
		return nil, nil
	})
	if _, err := mw(context.Background(), "chat/Echo"); err != nil {
		t.Fatalf("ClientStreamBearerTokenMiddleware: %v", err)
	}
	if token != auth.BearerValue("secret") {
		t.Fatalf("metadata token = %q, want bearer secret", token)
	}

	called := false
	empty := ClientStreamBearerTokenMiddleware("")(func(ctx context.Context, method string) (*Stream, error) {
		called = true
		md, _ := metadata.FromContext(ctx)
		if md.Get(auth.MetadataKey) != "" {
			t.Fatalf("empty token metadata = %#v, want no auth token", md)
		}
		return nil, nil
	})
	if _, err := empty(context.Background(), "chat/Echo"); err != nil {
		t.Fatalf("empty ClientStreamBearerTokenMiddleware: %v", err)
	}
	if !called {
		t.Fatal("empty token middleware did not call next")
	}
}

func TestRPCClientStreamMiddlewareSuiteAuth(t *testing.T) {
	s := NewServer(WithServerStreamMiddleware(StreamServerAuthMiddleware(func(ctx context.Context, token string) (context.Context, error) {
		if token != "secret" {
			return ctx, errors.New("invalid token")
		}
		return ctx, nil
	})))
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "Auth",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			return stream.Send(helloResp{Message: "ok"})
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	unauthorized, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedStream, err := unauthorized.Stream(context.Background(), "chat/Auth")
	if err != nil {
		t.Fatalf("unauthorized Stream: %v", err)
	}
	defer unauthorizedStream.Close()
	if err := unauthorizedStream.Recv(&helloResp{}); CodeOf(err) != CodeUnauthenticated {
		t.Fatalf("unauthorized Recv error = %v, want unauthenticated", err)
	}
	authorized, err := NewClient(ts.URL, WithClientSuite(GovernanceSuite("chat", GovernanceConfig{ClientToken: "secret"})))
	if err != nil {
		t.Fatal(err)
	}
	authorizedStream, err := authorized.Stream(context.Background(), "chat/Auth")
	if err != nil {
		t.Fatalf("authorized Stream: %v", err)
	}
	defer authorizedStream.Close()
	var resp helloResp
	if err := authorizedStream.Recv(&resp); err != nil {
		t.Fatalf("authorized Recv: %v", err)
	}
	if resp.Message != "ok" {
		t.Fatalf("response = %q, want ok", resp.Message)
	}
}

func TestRPCStreamRecoverMiddleware(t *testing.T) {
	s := NewServer(WithServerStreamMiddleware(StreamRecoverMiddleware()))
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "Panic",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			panic("boom")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/Panic")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Recv(&helloResp{}); CodeOf(err) != CodeInternal {
		t.Fatalf("Recv error = %v, want internal recovered panic", err)
	}
}

func TestRPCStreamServerError(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "Fail",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			return NewError(CodeInvalidArgument, "bad stream")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/Fail")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	var resp helloResp
	if err := stream.Recv(&resp); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("Recv error = %v, want invalid_argument", err)
	}
}

func TestRPCStreamCodecMismatch(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "Echo",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			var req helloReq
			return stream.Recv(&req)
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithCodec(namedJSONCodec{name: "custom-json"}))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/Echo")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Send(helloReq{Name: "gofly"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := stream.Recv(&helloResp{}); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("Recv error = %v, want invalid_argument codec mismatch", err)
	}
}

func TestRPCStreamClientOperationTimeout(t *testing.T) {
	entered := make(chan struct{})
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "Wait",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			close(entered)
			<-ctx.Done()
			return nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithClientStreamTimeout(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/Wait")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream handler to start")
	}
	if err := stream.Recv(&helloResp{}); CodeOf(err) != CodeDeadlineExceeded {
		t.Fatalf("Recv error = %v, want deadline_exceeded", err)
	}
}

func TestRPCStreamMissingAndOversizedFrame(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Stream(context.Background(), "missing/Stream"); CodeOf(err) != CodeUnavailable {
		t.Fatalf("missing stream error = %v, want unavailable", err)
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	stream := newStream(client, bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client)), JSONCodec{})
	stream.maxFrame = 4
	go func() {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], 5)
		_, _ = server.Write(header[:])
		_, _ = server.Write([]byte("12345"))
	}()
	var msg helloReq
	if err := stream.Recv(&msg); err == nil {
		t.Fatal("Recv oversized frame succeeded, want error")
	}
}

func TestRPCStreamContextCancellation(t *testing.T) {
	s := NewServer()
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, err := c.Stream(ctx, "chat/Echo"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stream canceled = %v, want DeadlineExceeded", err)
	}
}

func TestRPCStreamGovernanceRuntimeRateLimit(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "stream-rate",
		Transport: coregovernance.TransportRPC,
		Service:   "chat",
		Method:    "Echo",
		Policy: coregovernance.Policy{
			RateLimit: coregovernance.RateLimitPolicy{Rate: 1, Burst: 1},
		},
	})
	s := newGovernedStreamServer(t, rules, func(ctx context.Context, stream *Stream) error {
		return nil
	})

	first := newStreamUpgradeRequest("/rpc/stream/chat/Echo")
	firstRec := httptest.NewRecorder()
	s.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, body = %s, want hijack unsupported after allowed token", firstRec.Code, firstRec.Body.String())
	}

	second := newStreamUpgradeRequest("/rpc/stream/chat/Echo")
	secondRec := httptest.NewRecorder()
	s.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, body = %s, want 429", secondRec.Code, secondRec.Body.String())
	}
}

func TestRPCStreamGovernanceRuntimeConcurrencyLimit(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "stream-concurrency",
		Transport: coregovernance.TransportRPC,
		Service:   "chat",
		Method:    "Echo",
		Policy: coregovernance.Policy{
			Concurrency: coregovernance.ConcurrencyPolicy{Limit: 1},
		},
	})
	entered := make(chan struct{})
	release := make(chan struct{})
	s := newGovernedStreamServer(t, rules, func(ctx context.Context, stream *Stream) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return nil
	})
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/Echo")
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	defer stream.Close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first stream to enter handler")
	}

	second := newStreamUpgradeRequest("/rpc/stream/chat/Echo")
	secondRec := httptest.NewRecorder()
	s.ServeHTTP(secondRec, second)
	if secondRec.Code != http.StatusServiceUnavailable {
		close(release)
		t.Fatalf("second status = %d, body = %s, want 503", secondRec.Code, secondRec.Body.String())
	}
	close(release)
}

func TestRPCStreamGovernanceRuntimeBreaker(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "stream-breaker",
		Transport: coregovernance.TransportRPC,
		Service:   "chat",
		Method:    "Fail",
		Policy: coregovernance.Policy{
			Breaker: coregovernance.BreakerPolicy{Enabled: true, MinRequests: 1, FailureRatio: 0.1, OpenTimeout: time.Second},
		},
	})
	s := newGovernedStreamServerWithName(t, rules, "Fail", func(ctx context.Context, stream *Stream) error {
		return NewError(CodeInternal, "boom")
	})
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	first, err := c.Stream(context.Background(), "chat/Fail")
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if err := first.Recv(&helloResp{}); CodeOf(err) != CodeInternal {
		t.Fatalf("first recv = %v, want internal", err)
	}
	_ = first.Close()
	second, err := c.Stream(context.Background(), "chat/Fail")
	if err != nil {
		t.Fatalf("second stream: %v", err)
	}
	if err := second.Recv(&helloResp{}); CodeOf(err) != CodeInternal {
		t.Fatalf("second recv = %v, want internal", err)
	}
	_ = second.Close()
	if _, err := c.Stream(context.Background(), "chat/Fail"); CodeOf(err) != CodeUnavailable {
		t.Fatalf("third stream = %v, want unavailable", err)
	}
}

func TestRPCStreamGovernanceRuntimeTimeoutAndMetadata(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "stream-timeout-metadata",
		Transport: coregovernance.TransportRPC,
		Service:   "chat",
		Method:    "Echo",
		Policy: coregovernance.Policy{
			Timeout: 5 * time.Millisecond,
			Metadata: map[string]string{
				"x-policy": "enabled",
			},
			Canary: coregovernance.CanaryPolicy{
				Ratio:   1,
				Service: "chat-canary",
				Headers: map[string]string{"x-canary-group": "blue"},
			},
		},
	})
	s := newGovernedStreamServer(t, rules, func(ctx context.Context, stream *Stream) error {
		md, ok := metadata.FromContext(ctx)
		if !ok || md.Get("x-policy") != "enabled" || md.Get(coregovernance.HeaderCanary) != "true" || md.Get(coregovernance.HeaderCanaryService) != "chat-canary" || md.Get("x-canary-group") != "blue" {
			t.Fatalf("metadata = %#v, want governance policy and canary metadata", md)
		}
		<-ctx.Done()
		return nil
	})
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := c.Stream(context.Background(), "chat/Echo")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Recv(&helloResp{}); CodeOf(err) != CodeDeadlineExceeded {
		t.Fatalf("Recv = %v, want deadline_exceeded", err)
	}
}

func newGovernedStreamServer(t *testing.T, rules *coregovernance.RuleSet, handler func(context.Context, *Stream) error) *HTTPServer {
	t.Helper()
	return newGovernedStreamServerWithName(t, rules, "Echo", handler)
}

func newGovernedStreamServerWithName(t *testing.T, rules *coregovernance.RuleSet, name string, handler func(context.Context, *Stream) error) *HTTPServer {
	t.Helper()
	s := NewServer(WithServerRuleSet(rules))
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       name,
		NewMessage: func() any { return new(helloReq) },
		Handler:    handler,
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func newStreamUpgradeRequest(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Upgrade", streamUpgradeToken)
	req.Header.Set("Connection", "Upgrade")
	return req
}

type namedJSONCodec struct {
	name string
}

func (c namedJSONCodec) Name() string { return c.name }

func (namedJSONCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (namedJSONCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
