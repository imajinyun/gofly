package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	corebreaker "github.com/imajinyun/gofly/core/breaker"
	coregovernance "github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/core/observability/trace"
	"github.com/imajinyun/gofly/rpc/endpoint"
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

func TestRPCClientStreamMethodPolicyMetadataAndTimeout(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "chat", Streams: []StreamDesc{{
		Name:       "PolicyStream",
		NewMessage: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, stream *Stream) error {
			md, ok := metadata.FromContext(ctx)
			if !ok || md.Get("x-stream-policy") != "method" || md.Get("x-stream-header") != "enabled" {
				t.Fatalf("metadata = %#v, want method rpc policy metadata and header", md)
			}
			time.Sleep(50 * time.Millisecond)
			return stream.Send(helloResp{Message: "late"})
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithRPCPolicy(RPCPolicy{
		Timeout: 200 * time.Millisecond,
		Methods: map[string]RPCPolicy{
			"chat/PolicyStream": {
				Timeout:  5 * time.Millisecond,
				Metadata: map[string]string{"x-stream-policy": "method"},
				Headers:  map[string]string{"x-stream-header": "enabled"},
			},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}

	stream, err := c.Stream(context.Background(), "chat/PolicyStream")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	if err := stream.Recv(&helloResp{}); CodeOf(err) != CodeDeadlineExceeded {
		t.Fatalf("Recv = %v, want deadline_exceeded from method stream policy", err)
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

func TestRPCStreamMetadataHeaderBoundaries(t *testing.T) {
	var buf bytes.Buffer
	rw := bufio.NewReadWriter(bufio.NewReader(&buf), bufio.NewWriter(&buf))
	ctx := metadata.Append(context.Background(), "x-tenant", "beta", "Host", "ignored", "Upgrade", "ignored")
	if err := writeStreamMetadataHeaders(rw, ctx); err != nil {
		t.Fatalf("writeStreamMetadataHeaders error = %v", err)
	}
	if err := rw.Flush(); err != nil {
		t.Fatalf("flush metadata headers: %v", err)
	}
	written := buf.String()
	if !strings.Contains(written, "x-tenant: beta\r\n") {
		t.Fatalf("metadata headers = %q, want x-tenant", written)
	}
	if strings.Contains(strings.ToLower(written), "host:") || strings.Contains(strings.ToLower(written), "upgrade:") {
		t.Fatalf("metadata headers = %q, want reserved headers skipped", written)
	}

	badKey := metadata.Append(context.Background(), "bad key", "value")
	if err := writeStreamMetadataHeaders(bufio.NewReadWriter(bufio.NewReader(&bytes.Buffer{}), bufio.NewWriter(io.Discard)), badKey); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("bad key error = %v, want invalid_argument", err)
	}
	badValue := metadata.Append(context.Background(), "x-tenant", "bad\r\nvalue")
	if err := writeStreamMetadataHeaders(bufio.NewReadWriter(bufio.NewReader(&bytes.Buffer{}), bufio.NewWriter(io.Discard)), badValue); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("bad value error = %v, want invalid_argument", err)
	}

	md := streamMetadataFromHeader(http.Header{"X-Tenant": {"beta"}, "Connection": {"Upgrade"}})
	if md.Get("X-Tenant") != "beta" || md.Get("x-tenant") != "beta" {
		t.Fatalf("streamMetadataFromHeader = %#v, want canonical and lowercase keys", md)
	}
	if onlyReserved := streamMetadataFromHeader(http.Header{"Host": {"example.com"}}); onlyReserved != nil {
		t.Fatalf("reserved-only metadata = %#v, want nil", onlyReserved)
	}

	for _, key := range []string{"x-tenant", "traceparent", "x_test"} {
		if !validStreamHeaderKey(key) {
			t.Fatalf("validStreamHeaderKey(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"", "bad key", "bad:key"} {
		if validStreamHeaderKey(key) {
			t.Fatalf("validStreamHeaderKey(%q) = true, want false", key)
		}
	}
}

func TestRPCStreamPureFrameAndCloseBoundaries(t *testing.T) {
	var nilStream *Stream
	if err := nilStream.Send(helloReq{Name: "gofly"}); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("nil Send error = %v, want ErrStreamClosed", err)
	}
	if err := nilStream.Close(); err != nil {
		t.Fatalf("nil Close error = %v, want nil", err)
	}

	client, server := net.Pipe()
	clientStream := newStream(client, bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client)), nil)
	serverStream := newStream(server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), JSONCodec{})
	defer clientStream.Close()
	defer serverStream.Close()
	if clientStream.codec.Name() != "json" {
		t.Fatalf("default stream codec = %q, want json", clientStream.codec.Name())
	}
	go func() { _ = clientStream.SendError("", "boom") }()
	if err := serverStream.Recv(&helloResp{}); CodeOf(err) != CodeInternal {
		t.Fatalf("Recv SendError error = %v, want internal", err)
	}

	zeroClient, zeroServer := net.Pipe()
	zeroStream := newStream(zeroClient, bufio.NewReadWriter(bufio.NewReader(zeroClient), bufio.NewWriter(zeroClient)), JSONCodec{})
	defer zeroStream.Close()
	defer zeroServer.Close()
	go func() {
		var header [4]byte
		_, _ = zeroServer.Write(header[:])
	}()
	if err := zeroStream.Recv(&helloReq{}); err == nil || !strings.Contains(err.Error(), "invalid rpc stream frame size") {
		t.Fatalf("zero frame Recv error = %v, want invalid frame size", err)
	}

	hookStream := newMiddlewareTestStream(t)
	runs := 0
	hookStream.onClose(func() { runs++ })
	if err := hookStream.Close(); err != nil {
		t.Fatalf("hook stream close error = %v", err)
	}
	if err := hookStream.Close(); err != nil {
		t.Fatalf("hook stream close twice error = %v", err)
	}
	hookStream.onClose(func() { runs++ })
	if runs != 2 {
		t.Fatalf("close hook runs = %d, want once before close and immediately after close", runs)
	}
}

func TestRPCStreamEnvelopeErrorBoundaries(t *testing.T) {
	marshalStream := newStream(newNoopConn(), bufio.NewReadWriter(bufio.NewReader(&bytes.Buffer{}), bufio.NewWriter(io.Discard)), errCodec{})
	if err := marshalStream.Send(helloReq{Name: "gofly"}); err == nil || !strings.Contains(err.Error(), "marshal stream message") {
		t.Fatalf("Send marshal error = %v, want wrapped marshal error", err)
	}

	invalidClient, invalidServer := net.Pipe()
	invalidStream := newStream(invalidClient, bufio.NewReadWriter(bufio.NewReader(invalidClient), bufio.NewWriter(invalidClient)), JSONCodec{})
	defer invalidStream.Close()
	defer invalidServer.Close()
	go func() {
		payload := []byte("{")
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
		_, _ = invalidServer.Write(header[:])
		_, _ = invalidServer.Write(payload)
	}()
	if err := invalidStream.Recv(&helloReq{}); err == nil || !strings.Contains(err.Error(), "unexpected end") {
		t.Fatalf("Recv invalid JSON error = %v, want JSON decode error", err)
	}

	deadlineErr := errors.New("deadline failed")
	writeDeadlineStream := newStream(deadlineErrConn{Conn: newNoopConn(), writeErr: deadlineErr}, bufio.NewReadWriter(bufio.NewReader(&bytes.Buffer{}), bufio.NewWriter(io.Discard)), JSONCodec{})
	writeDeadlineStream.writeTimeout = time.Millisecond
	if err := writeDeadlineStream.writeEnvelope(streamEnvelope{Code: CodeOK}); !errors.Is(err, deadlineErr) {
		t.Fatalf("writeEnvelope deadline error = %v, want %v", err, deadlineErr)
	}

	readDeadlineStream := newStream(deadlineErrConn{Conn: newNoopConn(), readErr: deadlineErr}, bufio.NewReadWriter(bufio.NewReader(&bytes.Buffer{}), bufio.NewWriter(io.Discard)), JSONCodec{})
	readDeadlineStream.readTimeout = time.Millisecond
	if _, err := readDeadlineStream.readEnvelope(); !errors.Is(err, deadlineErr) {
		t.Fatalf("readEnvelope deadline error = %v, want %v", err, deadlineErr)
	}

	if got := normalizeStreamTimeout(timeoutNetError{}, "read"); CodeOf(got) != CodeDeadlineExceeded {
		t.Fatalf("normalizeStreamTimeout timeout = %v, want deadline_exceeded", got)
	}
	plainErr := errors.New("plain")
	if got := normalizeStreamTimeout(plainErr, "read"); !errors.Is(got, plainErr) {
		t.Fatalf("normalizeStreamTimeout plain = %v, want original non-rpc error", got)
	}
}

func TestRPCStreamContextAndWaitBoundaries(t *testing.T) {
	var nilCtx context.Context
	if err := streamContextError(nilCtx); err != nil {
		t.Fatalf("nil context error = %v, want nil", err)
	}
	if err := streamContextError(context.Background()); err != nil {
		t.Fatalf("background context error = %v, want nil", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := streamContextError(canceled); err != nil && CodeOf(err) != CodeCanceled {
		t.Fatalf("canceled stream context error = %v, want nil or canceled", err)
	}
	deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := streamContextError(deadline); err != nil && CodeOf(err) != CodeDeadlineExceeded {
		t.Fatalf("deadline stream context error = %v, want nil or deadline_exceeded", err)
	}

	waitStreamHandler(make(chan error), 0)
	done := make(chan error, 1)
	done <- nil
	waitStreamHandler(done, time.Second)
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

func TestRPCClientStreamMaxConcurrencyReleasesOnNextError(t *testing.T) {
	opened := 0
	mw := ClientStreamMaxConcurrencyMiddleware(1)(func(context.Context, string) (*Stream, error) {
		opened++
		return nil, errors.New("open failed")
	})
	if _, err := mw(context.Background(), "chat/Echo"); err == nil {
		t.Fatal("first stream open succeeded, want error")
	}
	if _, err := mw(context.Background(), "chat/Echo"); err == nil {
		t.Fatal("second stream open succeeded, want error after released slot")
	}
	if opened != 2 {
		t.Fatalf("opened = %d, want 2", opened)
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

func TestRPCStreamObservabilityMiddlewareBoundaries(t *testing.T) {
	stream := newMiddlewareTestStream(t)
	defer stream.Close()

	traceParent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	traceCtx := metadata.Append(context.Background(), trace.TraceParentHeader, traceParent)
	serverTraceCalled := false
	serverTrace := StreamTraceMiddleware("orders")(func(ctx context.Context, stream *Stream) error {
		serverTraceCalled = true
		if sc, ok := trace.FromContext(ctx); !ok || sc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("server stream trace context = %#v ok=%v", sc, ok)
		}
		return nil
	})
	if err := serverTrace(traceCtx, stream); err != nil {
		t.Fatalf("StreamTraceMiddleware error = %v", err)
	}
	if !serverTraceCalled {
		t.Fatal("StreamTraceMiddleware did not call next")
	}

	clientTraceCalled := false
	clientTrace := ClientStreamTraceMiddlewareWithSampler("orders", trace.NeverSampler())(func(ctx context.Context, method string) (*Stream, error) {
		clientTraceCalled = true
		if method != "orders/Watch" {
			t.Fatalf("method = %q, want orders/Watch", method)
		}
		if sc, ok := trace.FromContext(ctx); !ok || sc.TraceID == "" || sc.Sampled {
			t.Fatalf("client stream trace context = %#v ok=%v, want unsampled trace", sc, ok)
		}
		return newMiddlewareTestStream(t), nil
	})
	clientStream, err := clientTrace(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("ClientStreamTraceMiddleware error = %v", err)
	}
	defer clientStream.Close()
	if !clientTraceCalled {
		t.Fatal("ClientStreamTraceMiddleware did not call next")
	}

	reg := metrics.NewRegistry()
	serverMetrics := StreamMetricsMiddleware("", reg)(func(context.Context, *Stream) error { return NewError(CodeInvalidArgument, "bad") })
	if err := serverMetrics(context.Background(), stream); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("StreamMetricsMiddleware error = %v, want invalid_argument", err)
	}
	if snap := reg.Snapshot(); snap.Requests != 1 || snap.Statuses[http.StatusBadRequest] != 1 || snap.Errors != 0 || snap.InFlight != 0 {
		t.Fatalf("stream metrics snapshot = %#v, want one completed invalid-argument stream", snap)
	}

	clientReg := metrics.NewRegistry()
	clientMetrics := ClientStreamMetricsMiddleware("client-stream", clientReg)(func(context.Context, string) (*Stream, error) {
		return newMiddlewareTestStream(t), nil
	})
	metricStream, err := clientMetrics(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("ClientStreamMetricsMiddleware error = %v", err)
	}
	if snap := clientReg.Snapshot(); snap.InFlight != 1 {
		t.Fatalf("client metrics in-flight before close = %d, want 1", snap.InFlight)
	}
	if err := metricStream.Close(); err != nil {
		t.Fatalf("metric stream close error = %v", err)
	}
	if snap := clientReg.Snapshot(); snap.Requests != 1 || snap.InFlight != 0 {
		t.Fatalf("client metrics snapshot after close = %#v, want one completed stream", snap)
	}

	if err := StreamLoggingMiddleware("")(func(context.Context, *Stream) error { return nil })(context.Background(), stream); err != nil {
		t.Fatalf("StreamLoggingMiddleware error = %v", err)
	}
	loggingStream, err := ClientStreamLoggingMiddleware("")(func(context.Context, string) (*Stream, error) { return newMiddlewareTestStream(t), nil })(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("ClientStreamLoggingMiddleware error = %v", err)
	}
	if err := loggingStream.Close(); err != nil {
		t.Fatalf("logging stream close error = %v", err)
	}
}

func TestRPCClientStreamRequestIDAndBreakerBoundaries(t *testing.T) {
	var generated string
	mw := ClientStreamRequestIDMiddleware()(func(ctx context.Context, method string) (*Stream, error) {
		generated = metadata.RequestIDFromContext(ctx)
		return nil, nil
	})
	if _, err := mw(context.Background(), "orders/Watch"); err != nil {
		t.Fatalf("ClientStreamRequestIDMiddleware generated error = %v", err)
	}
	if generated == "" {
		t.Fatal("ClientStreamRequestIDMiddleware did not generate request id")
	}

	var preserved string
	mw = ClientStreamRequestIDMiddleware()(func(ctx context.Context, method string) (*Stream, error) {
		preserved = metadata.RequestIDFromContext(ctx)
		return nil, nil
	})
	ctx := metadata.Append(context.Background(), metadata.RequestIDKey, "rid-client-stream")
	if _, err := mw(ctx, "orders/Watch"); err != nil {
		t.Fatalf("ClientStreamRequestIDMiddleware preserve error = %v", err)
	}
	if preserved != "rid-client-stream" {
		t.Fatalf("preserved request id = %q, want rid-client-stream", preserved)
	}

	nilBreakerCalled := false
	nilBreaker := ClientStreamBreakerMiddleware(nil)(func(context.Context, string) (*Stream, error) {
		nilBreakerCalled = true
		return nil, errors.New("upstream")
	})
	if _, err := nilBreaker(context.Background(), "orders/Watch"); err == nil || !nilBreakerCalled {
		t.Fatalf("nil breaker err=%v called=%v, want delegated upstream error", err, nilBreakerCalled)
	}

	brk := corebreaker.New(corebreaker.WithFailureThreshold(1), corebreaker.WithOpenTimeout(time.Hour))
	guarded := ClientStreamBreakerMiddleware(brk)(func(context.Context, string) (*Stream, error) {
		return nil, errors.New("first failure")
	})
	if _, err := guarded(context.Background(), "orders/Watch"); err == nil {
		t.Fatal("first guarded stream succeeded, want failure")
	}
	if _, err := guarded(context.Background(), "orders/Watch"); CodeOf(err) != CodeUnavailable {
		t.Fatalf("open breaker stream error = %v, want unavailable", err)
	}
}

func TestRPCUnaryAndClientStreamMiddlewareBoundaries(t *testing.T) {
	ctx, _ := trace.Start(context.Background(), "")
	unaryOK := endpoint.Endpoint(func(context.Context, any) (any, error) { return "ok", nil })
	if resp, err := LoggingMiddleware("")(unaryOK)(ctx, "req"); err != nil || resp != "ok" {
		t.Fatalf("LoggingMiddleware success resp=%v err=%v, want ok nil", resp, err)
	}
	unaryErr := errors.New("unary failed")
	if _, err := LoggingMiddlewareWithSampler("orders", trace.AlwaysSampler())(func(context.Context, any) (any, error) { return "partial", unaryErr })(ctx, "req"); !errors.Is(err, unaryErr) {
		t.Fatalf("LoggingMiddleware error = %v, want %v", err, unaryErr)
	}

	reg := metrics.NewRegistry()
	if _, err := MetricsMiddleware("orders", reg)(func(context.Context, any) (any, error) { return nil, NewError(CodeInternal, "boom") })(ctx, nil); CodeOf(err) != CodeInternal {
		t.Fatalf("MetricsMiddleware error = %v, want internal", err)
	}
	if snap := reg.Snapshot(); snap.Requests != 1 || snap.Errors != 1 || snap.InFlight != 0 {
		t.Fatalf("metrics snapshot = %#v, want one failed completed unary call", snap)
	}

	limited := AdaptiveLimitMiddleware(nil)(unaryOK)
	if resp, err := limited(context.Background(), "req"); err != nil || resp != "ok" {
		t.Fatalf("AdaptiveLimitMiddleware success resp=%v err=%v", resp, err)
	}
	adaptiveBreaker := AdaptiveBreakerMiddleware(nil)
	if _, err := adaptiveBreaker(func(context.Context, any) (any, error) { return nil, errors.New("fail") })(context.Background(), nil); err == nil {
		t.Fatal("AdaptiveBreakerMiddleware failure path returned nil")
	}
	if resp, err := adaptiveBreaker(unaryOK)(context.Background(), nil); err != nil || resp != "ok" {
		t.Fatalf("AdaptiveBreakerMiddleware success resp=%v err=%v", resp, err)
	}

	clientReg := metrics.NewRegistry()
	clientMetricsErr := ClientStreamMetricsMiddleware("client", clientReg)(func(context.Context, string) (*Stream, error) {
		return nil, NewError(CodeUnavailable, "dial failed")
	})
	if _, err := clientMetricsErr(context.Background(), "orders/Watch"); CodeOf(err) != CodeUnavailable {
		t.Fatalf("ClientStreamMetricsMiddleware error = %v, want unavailable", err)
	}
	if snap := clientReg.Snapshot(); snap.Requests != 1 || snap.Errors != 1 || snap.InFlight != 0 {
		t.Fatalf("client metrics snapshot = %#v, want one failed completed client stream", snap)
	}

	clientLogErr := errors.New("open failed")
	if _, err := ClientStreamLoggingMiddlewareWithSampler("client", trace.AlwaysSampler())(func(context.Context, string) (*Stream, error) { return nil, clientLogErr })(ctx, "orders/Watch"); !errors.Is(err, clientLogErr) {
		t.Fatalf("ClientStreamLoggingMiddleware error = %v, want %v", err, clientLogErr)
	}
	adaptiveLimitErr := ClientStreamAdaptiveLimitMiddleware(nil)(func(context.Context, string) (*Stream, error) { return nil, clientLogErr })
	if _, err := adaptiveLimitErr(context.Background(), "orders/Watch"); !errors.Is(err, clientLogErr) {
		t.Fatalf("ClientStreamAdaptiveLimitMiddleware error = %v, want %v", err, clientLogErr)
	}
	adaptiveLimitOK := ClientStreamAdaptiveLimitMiddleware(nil)(func(context.Context, string) (*Stream, error) { return newMiddlewareTestStream(t), nil })
	limitStream, err := adaptiveLimitOK(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("ClientStreamAdaptiveLimitMiddleware success: %v", err)
	}
	_ = limitStream.Close()

	adaptiveStreamBreakerErr := ClientStreamAdaptiveBreakerMiddleware(corebreaker.NewAdaptive())(func(context.Context, string) (*Stream, error) { return nil, clientLogErr })
	if _, err := adaptiveStreamBreakerErr(context.Background(), "orders/Watch"); !errors.Is(err, clientLogErr) {
		t.Fatalf("ClientStreamAdaptiveBreakerMiddleware error = %v, want %v", err, clientLogErr)
	}
	adaptiveStreamBreakerOK := ClientStreamAdaptiveBreakerMiddleware(nil)(func(context.Context, string) (*Stream, error) { return newMiddlewareTestStream(t), nil })
	breakerStream, err := adaptiveStreamBreakerOK(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("ClientStreamAdaptiveBreakerMiddleware success: %v", err)
	}
	_ = breakerStream.Close()
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

type errCodec struct{}

func (errCodec) Name() string { return "err" }

func (errCodec) Marshal(any) ([]byte, error) { return nil, errors.New("marshal failed") }

func (errCodec) Unmarshal([]byte, any) error { return nil }

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

type deadlineErrConn struct {
	net.Conn
	readErr  error
	writeErr error
}

func (c deadlineErrConn) SetReadDeadline(time.Time) error  { return c.readErr }
func (c deadlineErrConn) SetWriteDeadline(time.Time) error { return c.writeErr }

type noopConn struct{}

func newNoopConn() noopConn { return noopConn{} }

func (noopConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (noopConn) Write(p []byte) (int, error)      { return len(p), nil }
func (noopConn) Close() error                     { return nil }
func (noopConn) LocalAddr() net.Addr              { return noopAddr("local") }
func (noopConn) RemoteAddr() net.Addr             { return noopAddr("remote") }
func (noopConn) SetDeadline(time.Time) error      { return nil }
func (noopConn) SetReadDeadline(time.Time) error  { return nil }
func (noopConn) SetWriteDeadline(time.Time) error { return nil }

type noopAddr string

func (a noopAddr) Network() string { return string(a) }
func (a noopAddr) String() string  { return string(a) }
