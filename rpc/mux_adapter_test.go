package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/core/security"
)

type firstEndpointBalancer struct{}

func (firstEndpointBalancer) Pick(ctx context.Context, endpoints []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(endpoints) == 0 {
		return "", errors.New("no endpoint to pick")
	}
	return endpoints[0], nil
}

func withIsolatedMuxCandidateMetrics(t *testing.T) *metrics.Registry {
	t.Helper()
	old := metrics.Default
	reg := metrics.NewRegistry()
	metrics.Default = reg
	registerExperimentalMuxCandidateMetrics(reg)
	t.Cleanup(func() {
		metrics.Default = old
		registerExperimentalMuxCandidateMetrics(old)
	})
	return reg
}

type timeoutWriteConn struct {
	mu       sync.Mutex
	deadline time.Time
	closed   bool
	done     chan struct{}
}

func (c *timeoutWriteConn) Read([]byte) (int, error) {
	<-c.done
	return 0, net.ErrClosed
}

func (c *timeoutWriteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	if !deadline.IsZero() {
		return 0, muxTimeoutNetError{msg: "write timeout"}
	}
	return len(p), nil
}

func (c *timeoutWriteConn) Close() error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.done)
	}
	c.mu.Unlock()
	return nil
}

func (c *timeoutWriteConn) LocalAddr() net.Addr  { return dummyAddr("local") }
func (c *timeoutWriteConn) RemoteAddr() net.Addr { return dummyAddr("remote") }
func (c *timeoutWriteConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}
func (c *timeoutWriteConn) SetReadDeadline(time.Time) error    { return nil }
func (c *timeoutWriteConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

type muxTimeoutNetError struct{ msg string }

func (e muxTimeoutNetError) Error() string   { return e.msg }
func (e muxTimeoutNetError) Timeout() bool   { return true }
func (e muxTimeoutNetError) Temporary() bool { return true }

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }

func TestExperimentalMuxAdapterDispatchesMultipleStreams(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxClientAdapter(clientConn, WithExperimentalMuxConnectionWindow(4))
	server := NewExperimentalMuxServerAdapter(serverConn, WithExperimentalMuxConnectionWindow(4))
	defer client.Close()
	defer server.Close()

	if err := server.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, Message{Payload: append([]byte("ack:"), msg.Payload...)}); err != nil {
			return err
		}
		return stream.Close(ctx, "ok")
	}); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(serveCtx)
	}()

	first, err := client.OpenStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("OpenStream first: %v", err)
	}
	second, err := client.OpenStream(context.Background(), "/orders/Watch")
	if err != nil {
		t.Fatalf("OpenStream second: %v", err)
	}
	if err := first.Send(context.Background(), Message{Payload: []byte("first")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if err := second.Send(context.Background(), Message{Payload: []byte("second")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	assertMuxPayload(t, first, "ack:first")
	assertMuxPayload(t, second, "ack:second")
	if _, err := first.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("first terminal receive error = %v, want EOF", err)
	}
	if _, err := second.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("second terminal receive error = %v, want EOF", err)
	}

	assertEventually(t, func() bool {
		snapshot := server.Snapshot()
		return snapshot.AcceptedStreams == 2 &&
			snapshot.Transport.AcceptedStreams == 2 &&
			snapshot.Transport.ActiveStreams == 0
	}, "mux adapter accepted streams")
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.Role != "client" ||
		serverSnapshot.Role != "server" ||
		serverSnapshot.LastMethod != "orders/Watch" ||
		serverSnapshot.RejectedStreams != 0 ||
		clientSnapshot.Transport.OpenedStreams != 2 ||
		serverSnapshot.Transport.AcceptedStreams != 2 {
		t.Fatalf("adapter snapshots client=%+v server=%+v, want two dispatched streams", clientSnapshot, serverSnapshot)
	}
	diagnosis := server.DiagnosisSnapshot()
	if !diagnosis.Enabled ||
		diagnosis.Mode != "experimental_mux" ||
		diagnosis.Adapter.AcceptedStreams != 2 ||
		diagnosis.FlowControl.ConnectionWindow != 4 ||
		diagnosis.Transport.AcceptedStreams != 2 ||
		diagnosis.Keepalive.Liveness == "" {
		t.Fatalf("mux diagnosis = %+v, want adapter, flow-control and transport evidence", diagnosis)
	}
	component := server.RuntimeComponentSnapshot(context.Background())
	if component.Name != "rpc.mux.server" || component.Kind != "server" || component.Owner != "rpc" || component.Status == "" {
		t.Fatalf("mux runtime component = %+v, want rpc mux server component", component)
	}
	if _, err := json.Marshal(component.Details); err != nil {
		t.Fatalf("marshal mux runtime details: %v", err)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve returned error after cancel: %v", err)
	}
}

func TestExperimentalMuxAdapterRejectsUnknownMethod(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxClientAdapter(clientConn)
	server := NewExperimentalMuxServerAdapter(serverConn)
	defer client.Close()
	defer server.Close()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(serveCtx)
	}()

	stream, err := client.OpenStream(context.Background(), "orders/Missing")
	if err != nil {
		t.Fatalf("OpenStream missing method: %v", err)
	}
	if _, err := stream.Receive(muxTestTimeoutContext(t)); CodeOf(err) != CodeNotFound {
		t.Fatalf("Receive missing method error = %v, want CodeNotFound", err)
	}
	assertEventually(t, func() bool {
		snapshot := server.Snapshot()
		return snapshot.RejectedStreams == 1 &&
			snapshot.LastMethod == "orders/Missing" &&
			snapshot.LastError != ""
	}, "mux adapter rejected unknown method")

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve returned error after cancel: %v", err)
	}
}

func TestExperimentalMuxAdapterDefensiveBoundaries(t *testing.T) {
	var nilClient *ExperimentalMuxClientAdapter
	if _, err := nilClient.OpenStream(context.Background(), "orders/Watch"); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("nil client OpenStream error = %v, want ErrExperimentalMuxTransportClosed", err)
	}
	if err := nilClient.Close(); err != nil {
		t.Fatalf("nil client Close error = %v, want nil", err)
	}
	nilClientSnapshot := nilClient.Snapshot()
	if nilClientSnapshot.Role != "client" || !nilClientSnapshot.Transport.Closed {
		t.Fatalf("nil client snapshot = %+v, want closed client", nilClientSnapshot)
	}
	clientComponent := nilClient.RuntimeComponentSnapshot(context.Background())
	if clientComponent.Status != "closed" {
		t.Fatalf("nil client runtime status = %q, want closed", clientComponent.Status)
	}

	var nilServer *ExperimentalMuxServerAdapter
	if err := nilServer.RegisterStream("orders/Watch", func(context.Context, *ExperimentalMuxStream) error { return nil }); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("nil server RegisterStream error = %v, want ErrExperimentalMuxTransportClosed", err)
	}
	if err := nilServer.Serve(context.Background()); !errors.Is(err, ErrExperimentalMuxTransportClosed) {
		t.Fatalf("nil server Serve error = %v, want ErrExperimentalMuxTransportClosed", err)
	}
	if err := nilServer.Close(); err != nil {
		t.Fatalf("nil server Close error = %v, want nil", err)
	}
	nilServerSnapshot := nilServer.Snapshot()
	if nilServerSnapshot.Role != "server" || !nilServerSnapshot.Transport.Closed {
		t.Fatalf("nil server snapshot = %+v, want closed server", nilServerSnapshot)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	clientComponent = nilClient.RuntimeComponentSnapshot(canceled)
	if clientComponent.Status != "error" || clientComponent.Error == "" {
		t.Fatalf("canceled client component = %+v, want error status", clientComponent)
	}
	serverComponent := nilServer.RuntimeComponentSnapshot(canceled)
	if serverComponent.Status != "error" || serverComponent.Error == "" {
		t.Fatalf("canceled server component = %+v, want error status", serverComponent)
	}
}

func TestExperimentalMuxAdapterRegistrationValidation(t *testing.T) {
	_, serverConn := net.Pipe()
	server := NewExperimentalMuxServerAdapter(serverConn)
	defer server.Close()

	if err := server.RegisterStream(" ", func(context.Context, *ExperimentalMuxStream) error { return nil }); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("RegisterStream blank method error = %v, want CodeInvalidArgument", err)
	}
	if err := server.RegisterStream("orders/Watch", nil); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("RegisterStream nil handler error = %v, want CodeInvalidArgument", err)
	}
	handler := func(context.Context, *ExperimentalMuxStream) error { return nil }
	if err := server.RegisterStream("/orders/Watch/", handler); err != nil {
		t.Fatalf("RegisterStream first: %v", err)
	}
	if err := server.RegisterStream("orders/Watch", handler); CodeOf(err) != CodeAlreadyExists {
		t.Fatalf("RegisterStream duplicate error = %v, want CodeAlreadyExists", err)
	}
	if got := normalizeExperimentalMuxMethod(" /orders/Watch/ "); got != "orders/Watch" {
		t.Fatalf("normalizeExperimentalMuxMethod = %q, want orders/Watch", got)
	}
}

func TestExperimentalMuxAdapterRejectsInvalidRouteFrame(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	clientTransport := NewExperimentalMuxTransport(clientConn)
	server := NewExperimentalMuxServerAdapter(serverConn)
	defer clientTransport.Close()
	defer server.Close()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(serveCtx)
	}()

	stream, err := clientTransport.OpenStream(context.Background())
	if err != nil {
		t.Fatalf("OpenStream raw transport: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Service: "wrong", Method: "orders/Watch"}); err != nil {
		t.Fatalf("Send invalid route: %v", err)
	}
	if _, err := stream.Receive(muxTestTimeoutContext(t)); CodeOf(err) != CodeInvalidArgument {
		t.Fatalf("Receive invalid route error = %v, want CodeInvalidArgument", err)
	}
	assertEventually(t, func() bool {
		snapshot := server.Snapshot()
		return snapshot.RejectedStreams == 1 && snapshot.LastError != ""
	}, "mux adapter invalid route rejection")

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve returned error after cancel: %v", err)
	}
}

func TestExperimentalMuxAdapterRecordsHandlerError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxClientAdapter(clientConn)
	server := NewExperimentalMuxServerAdapter(serverConn)
	defer client.Close()
	defer server.Close()

	handlerErr := NewError(CodeAborted, "handler aborted")
	if err := server.RegisterStream("orders/Watch", func(context.Context, *ExperimentalMuxStream) error {
		return handlerErr
	}); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(serveCtx)
	}()

	stream, err := client.OpenStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if _, err := stream.Receive(muxTestTimeoutContext(t)); CodeOf(err) != CodeAborted {
		t.Fatalf("Receive handler error = %v, want CodeAborted", err)
	}
	assertEventually(t, func() bool {
		snapshot := server.Snapshot()
		return snapshot.AcceptedStreams == 1 &&
			snapshot.HandlerErrors == 1 &&
			snapshot.LastMethod == "orders/Watch" &&
			strings.Contains(snapshot.LastError, "handler aborted")
	}, "mux adapter handler error snapshot")

	component := client.RuntimeComponentSnapshot(context.Background())
	if component.Name != "rpc.mux.client" || component.Kind != "client" || component.Owner != "rpc" || component.Status == "" {
		t.Fatalf("client runtime component = %+v, want rpc mux client component", component)
	}
	diagnosis := client.DiagnosisSnapshot()
	if diagnosis.Mode != "experimental_mux" || !diagnosis.Enabled || diagnosis.Transport.OpenedStreams != 1 {
		t.Fatalf("client diagnosis = %+v, want enabled experimental mux transport", diagnosis)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve returned error after cancel: %v", err)
	}
}

func TestHTTPClientExperimentalMuxAdapterOptIn(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	muxClient := NewExperimentalMuxClientAdapter(clientConn, WithExperimentalMuxConnectionWindow(4))
	muxServer := NewExperimentalMuxServerAdapter(serverConn, WithExperimentalMuxConnectionWindow(4))
	defer muxClient.Close()
	defer muxServer.Close()

	if err := muxServer.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, Message{Payload: append([]byte("mux:"), msg.Payload...)}); err != nil {
			return err
		}
		return stream.Close(ctx, "ok")
	}); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- muxServer.Serve(serveCtx)
	}()

	client, err := NewClient("http://127.0.0.1:1", WithExperimentalMuxClientAdapter(muxClient))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()
	stream, err := client.MuxStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("MuxStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("hello")}); err != nil {
		t.Fatalf("mux Send: %v", err)
	}
	assertMuxPayload(t, stream, "mux:hello")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("mux terminal receive = %v, want EOF", err)
	}

	assertEventually(t, func() bool {
		diagnosis := client.RuntimeSnapshot().Diagnosis.Mux
		return diagnosis.Enabled &&
			diagnosis.Mode == "experimental_mux" &&
			diagnosis.Adapter.Role == "client" &&
			diagnosis.Transport.OpenedStreams == 1 &&
			muxServer.Snapshot().AcceptedStreams == 1
	}, "HTTPClient mux diagnosis")
	if _, err := client.Stream(context.Background(), "orders/Watch"); err == nil {
		t.Fatalf("default Stream unexpectedly used mux adapter; want HTTP upgrade path to stay separate")
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve returned error after cancel: %v", err)
	}
}

func TestExperimentalMuxAdapterTCPDialerListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("tcp:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()

	client, err := DialExperimentalMuxClientAdapter(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("DialExperimentalMuxClientAdapter: %v", err)
	}
	defer client.Close()
	stream, err := client.OpenStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("hello")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertMuxPayload(t, stream, "tcp:hello")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	assertEventually(t, func() bool {
		return client.Snapshot().Transport.OpenedStreams == 1 && client.Snapshot().Transport.ActiveStreams == 0
	}, "tcp mux client snapshot")

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("ServeExperimentalMuxListener returned error: %v", err)
	}
}

func TestExperimentalMuxCandidateAdapterPolicyAndProtocol(t *testing.T) {
	reg := withIsolatedMuxCandidateMetrics(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:             "gofly-mux/candidate-test",
		KeepaliveInterval:    time.Hour,
		KeepaliveIdle:        2 * time.Hour,
		MaxFrameBytes:        128,
		MaxMessageBytes:      512,
		MaxConcurrentStreams: 3,
		ReceiveQueueSize:     2,
		ConnectionWindow:     5,
		PayloadCodec:         "gzip",
		FrameCodec:           "binary",
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxCandidateListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("candidate:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		}, cfg)
	}()

	client, err := DialExperimentalMuxCandidateClientAdapter(context.Background(), "tcp", listener.Addr().String(), cfg)
	if err != nil {
		t.Fatalf("DialExperimentalMuxCandidateClientAdapter: %v", err)
	}
	defer client.Close()
	stream, err := client.OpenStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("hello")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertMuxPayload(t, stream, "candidate:hello")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	snapshot := client.Snapshot()
	if !snapshot.Candidate.Enabled ||
		snapshot.Candidate.Protocol != cfg.Protocol ||
		snapshot.Candidate.NegotiatedProtocol != cfg.Protocol ||
		snapshot.Candidate.PayloadCodec != "gzip" ||
		snapshot.Candidate.FrameCodec != "binary" ||
		snapshot.Transport.KeepaliveInterval != cfg.KeepaliveInterval ||
		snapshot.Transport.KeepaliveIdle != cfg.KeepaliveIdle ||
		snapshot.Transport.MaxStreams != cfg.MaxConcurrentStreams ||
		snapshot.Transport.ReceiveQueueSize != cfg.ReceiveQueueSize ||
		snapshot.Transport.ConnectionWindow != cfg.ConnectionWindow ||
		snapshot.Transport.MaxMessageBytes != cfg.MaxMessageBytes {
		t.Fatalf("candidate client snapshot = %+v, want negotiated policy", snapshot)
	}
	diagnosis := client.DiagnosisSnapshot()
	if !diagnosis.Candidate.Enabled ||
		diagnosis.Candidate.Protocol != cfg.Protocol ||
		diagnosis.FlowControl.ConnectionWindow != cfg.ConnectionWindow ||
		diagnosis.Keepalive.Interval != cfg.KeepaliveInterval {
		t.Fatalf("candidate client diagnosis = %+v, want candidate policy in diagnosis", diagnosis)
	}
	custom := reg.Snapshot().Customs["gofly_rpc_mux_candidate_connections"]
	if custom.Type != metrics.MetricGauge || len(custom.Series) != 1 ||
		custom.Series[0].Labels["frame_codec"] != "binary" ||
		custom.Series[0].Labels["payload_codec"] != "gzip" ||
		custom.Series[0].Labels["downgraded"] != "false" ||
		custom.Series[0].Value != 1 {
		t.Fatalf("candidate connection metric = %#v, want binary/gzip non-downgraded gauge", custom)
	}
	var prom bytes.Buffer
	if err := reg.WritePrometheus(&prom); err != nil {
		t.Fatalf("write prometheus: %v", err)
	}
	if !strings.Contains(prom.String(), `gofly_rpc_mux_candidate_connections{frame_codec="binary",payload_codec="gzip",downgraded="false"} 1`) {
		t.Fatalf("prometheus candidate metrics missing connection labels:\n%s", prom.String())
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("ServeExperimentalMuxCandidateListener returned error: %v", err)
	}
}

func TestExperimentalMuxServerRuntimeLifecycle(t *testing.T) {
	server, err := NewExperimentalMuxServer("127.0.0.1:0", func(adapter *ExperimentalMuxServerAdapter) error {
		return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
			msg, err := stream.Receive(ctx)
			if err != nil {
				return err
			}
			if err := stream.Send(ctx, Message{Payload: append([]byte("server:"), msg.Payload...)}); err != nil {
				return err
			}
			return stream.Close(ctx, "ok")
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	client, err := DialExperimentalMuxClientAdapter(context.Background(), "tcp", server.Addr())
	if err != nil {
		t.Fatalf("DialExperimentalMuxClientAdapter: %v", err)
	}
	defer client.Close()
	stream, err := client.OpenStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("hello")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertMuxPayload(t, stream, "server:hello")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	assertEventually(t, func() bool {
		diagnosis := server.DiagnosisSnapshot()
		return diagnosis.Enabled &&
			diagnosis.Mode == "experimental_mux_server" &&
			diagnosis.Adapter.AcceptedStreams == 1 &&
			diagnosis.Transport.AcceptedStreams == 1
	}, "mux server diagnosis")
	component := server.RuntimeComponentSnapshot(context.Background())
	if component.Name != "rpc.mux.server" || component.Target == "" || component.Details == nil {
		t.Fatalf("runtime component = %+v, want mux server details", component)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if server.RuntimeComponentSnapshot(context.Background()).Status != "closed" {
		t.Fatalf("runtime component after shutdown = %+v, want closed", server.RuntimeComponentSnapshot(context.Background()))
	}
}

func TestExperimentalMuxCandidateAdapterMutualTLS(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := rpcTLSCA(t, dir)
	caFile := filepath.Join(dir, "ca.crt")
	serverCert, serverKey := rpcTLSLeaf(t, dir, "server", caCert, caKey)
	clientCert, clientKey := rpcTLSLeaf(t, dir, "client", caCert, caKey)

	server, err := NewExperimentalMuxCandidateServer("127.0.0.1:0", func(adapter *ExperimentalMuxServerAdapter) error {
		return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
			msg, err := stream.Receive(ctx)
			if err != nil {
				return err
			}
			if err := stream.Send(ctx, Message{Payload: append([]byte("mtls:"), msg.Payload...)}); err != nil {
				return err
			}
			return stream.Close(ctx, "ok")
		})
	}, ExperimentalMuxCandidateConfig{
		Protocol: "gofly-mux/mtls-test",
		TLS: security.TLSConfig{
			CertFile:     serverCert,
			KeyFile:      serverKey,
			ClientCAFile: caFile,
		},
		HandshakeTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer server.Shutdown(context.Background())

	noCert, err := DialExperimentalMuxCandidateClientAdapter(context.Background(), "tcp", server.Addr(), ExperimentalMuxCandidateConfig{
		Protocol: "gofly-mux/mtls-test",
		TLS: security.TLSConfig{
			CAFile:     caFile,
			ServerName: "svc",
		},
		HandshakeTimeout: 5 * time.Second,
	})
	if err == nil {
		_ = noCert.Close()
		t.Fatal("expected mutual TLS mux candidate dial without client cert to fail")
	}
	if phase, _, ok := experimentalMuxCandidateFailureInfo(err); !ok || phase != experimentalMuxCandidateFailureTLS {
		t.Fatalf("mutual TLS failure = %v phase=%q ok=%v, want tls phase", err, phase, ok)
	}

	client, err := DialExperimentalMuxCandidateClientAdapter(context.Background(), "tcp", server.Addr(), ExperimentalMuxCandidateConfig{
		Protocol: "gofly-mux/mtls-test",
		TLS: security.TLSConfig{
			CAFile:     caFile,
			CertFile:   clientCert,
			KeyFile:    clientKey,
			ServerName: "svc",
		},
		HandshakeTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("DialExperimentalMuxCandidateClientAdapter with client cert: %v", err)
	}
	defer client.Close()
	stream, err := client.OpenStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("hello")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertMuxPayload(t, stream, "mtls:hello")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	clientSnapshot := client.Snapshot()
	if !clientSnapshot.Candidate.TLS ||
		!clientSnapshot.Candidate.MutualTLS ||
		clientSnapshot.Candidate.NegotiatedProtocol != "gofly-mux/mtls-test" {
		t.Fatalf("client candidate snapshot = %+v, want TLS/mTLS negotiated protocol", clientSnapshot)
	}
	assertEventually(t, func() bool {
		diagnosis := server.DiagnosisSnapshot()
		return diagnosis.Candidate.TLS &&
			diagnosis.Candidate.MutualTLS &&
			diagnosis.Candidate.NegotiatedProtocol == "gofly-mux/mtls-test" &&
			diagnosis.Adapter.AcceptedStreams == 1
	}, "mux candidate server mTLS diagnosis")
}

func TestExperimentalMuxCandidateNegotiationFailureDiagnostics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := ExperimentalMuxCandidateConfig{
		Protocol:        "gofly-mux/server-protocol",
		FrameCodec:      "binary",
		PayloadCodec:    "identity",
		MaxFrameBytes:   256,
		MaxMessageBytes: 1024,
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxCandidateListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				return stream.Close(ctx, "ok")
			})
		}, serverCfg)
	}()

	clientCfg := serverCfg
	clientCfg.Protocol = "gofly-mux/client-protocol"
	adapter, err := DialExperimentalMuxCandidateClientAdapter(context.Background(), "tcp", listener.Addr().String(), clientCfg)
	if err == nil {
		_ = adapter.Close()
		t.Fatal("candidate dial with protocol mismatch succeeded, want error")
	}
	phase, peerProtocol, ok := experimentalMuxCandidateFailureInfo(err)
	if !ok || phase != experimentalMuxCandidateFailureProtocol || peerProtocol != serverCfg.Protocol {
		t.Fatalf("protocol mismatch err=%v phase=%q peer=%q ok=%v, want protocol mismatch with peer protocol", err, phase, peerProtocol, ok)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("candidate server stopped with error: %v", err)
	}
}

func TestExperimentalMuxCandidateFramePolicyFailureDiagnostics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := ExperimentalMuxCandidateConfig{
		Protocol:        "gofly-mux/policy-test",
		FrameCodec:      "binary",
		PayloadCodec:    "identity",
		MaxFrameBytes:   256,
		MaxMessageBytes: 1024,
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxCandidateListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				return stream.Close(ctx, "ok")
			})
		}, serverCfg)
	}()

	clientCfg := serverCfg
	clientCfg.FrameCodec = "json"
	adapter, err := DialExperimentalMuxCandidateClientAdapter(context.Background(), "tcp", listener.Addr().String(), clientCfg)
	if err == nil {
		_ = adapter.Close()
		t.Fatal("candidate dial with frame policy mismatch succeeded, want error")
	}
	phase, peerProtocol, ok := experimentalMuxCandidateFailureInfo(err)
	if !ok || phase != experimentalMuxCandidateFailureFramePolicy || peerProtocol != serverCfg.Protocol {
		t.Fatalf("frame policy mismatch err=%v phase=%q peer=%q ok=%v, want frame policy mismatch", err, phase, peerProtocol, ok)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("candidate server stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerCandidateDowngradeDiagnostics(t *testing.T) {
	reg := withIsolatedMuxCandidateMetrics(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("legacy:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()
	candidateCfg := ExperimentalMuxCandidateConfig{
		Protocol:             "gofly-mux/downgrade-test",
		FrameCodec:           "binary",
		PayloadCodec:         "identity",
		AllowLegacyDowngrade: true,
	}
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{"tcp://" + listener.Addr().String()}, nil }),
		WithExperimentalMuxConnectionManagerCandidateConfig(candidateCfg),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	stream, err := client.MuxStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("MuxStream with legacy downgrade: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("probe")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertMuxPayload(t, stream, "legacy:probe")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if !diagnosis.Candidate.Enabled ||
		diagnosis.Candidate.NegotiationFailures != 1 ||
		diagnosis.Candidate.LastNegotiationPhase != experimentalMuxCandidateFailurePreface ||
		!diagnosis.Candidate.Downgraded ||
		diagnosis.Candidate.Downgrades != 1 ||
		len(diagnosis.Endpoints) != 1 ||
		!diagnosis.Endpoints[0].Adapter.Candidate.Downgraded {
		t.Fatalf("downgrade diagnosis = %+v, want candidate failure and legacy downgrade evidence", diagnosis)
	}
	customs := reg.Snapshot().Customs
	failures := customs["gofly_rpc_mux_candidate_negotiation_failures_total"]
	if failures.Type != metrics.MetricCounter || len(failures.Series) != 1 ||
		failures.Series[0].Labels["phase"] != "preface" ||
		failures.Series[0].Labels["peer_protocol"] != "unknown" ||
		failures.Series[0].Value != 1 {
		t.Fatalf("candidate failure metric = %#v, want one preface failure", failures)
	}
	downgrades := customs["gofly_rpc_mux_candidate_downgrades_total"]
	if downgrades.Type != metrics.MetricCounter || len(downgrades.Series) != 1 ||
		downgrades.Series[0].Labels["phase"] != "preface" ||
		downgrades.Series[0].Value != 1 {
		t.Fatalf("candidate downgrade metric = %#v, want one preface downgrade", downgrades)
	}
	connections := customs["gofly_rpc_mux_candidate_connections"]
	if connections.Type != metrics.MetricGauge || len(connections.Series) != 1 ||
		connections.Series[0].Labels["downgraded"] != "true" ||
		connections.Series[0].Value != 1 {
		t.Fatalf("candidate connection metric = %#v, want downgraded connection gauge", connections)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("legacy mux server stopped with error: %v", err)
	}
}

func TestExperimentalMuxCandidateDrainMetrics(t *testing.T) {
	reg := withIsolatedMuxCandidateMetrics(t)
	clientConn, serverConn := net.Pipe()
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:         "gofly-mux/drain-metrics-test",
		FrameCodec:       "binary",
		PayloadCodec:     "identity",
		ConnectionWindow: 3,
		ReceiveQueueSize: 3,
	}
	client := NewExperimentalMuxCandidateClientAdapter(clientConn, cfg)
	server := NewExperimentalMuxCandidateServerAdapter(serverConn, cfg)
	defer client.Close()
	defer server.Close()
	release := make(chan struct{})
	if err := server.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := stream.Send(ctx, Message{Payload: append([]byte("drained:"), msg.Payload...)}); err != nil {
			return err
		}
		return stream.Close(ctx, "ok")
	}); err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(context.Background())
	defer stop()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(serveCtx)
	}()
	stream, err := client.OpenStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("active")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertEventually(t, func() bool {
		return client.Snapshot().Transport.ActiveStreams == 1 && server.Snapshot().Transport.ActiveStreams == 1
	}, "candidate drain active stream")
	if err := client.Drain(context.Background(), "maintenance"); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	assertEventually(t, func() bool {
		serverSnapshot := server.Snapshot()
		return serverSnapshot.Transport.GoAwayFramesIn == 1 && serverSnapshot.Transport.RemoteDrainReason == "maintenance"
	}, "candidate server observed remote GOAWAY")

	customs := reg.Snapshot().Customs
	drains := customs["gofly_rpc_mux_candidate_drain_total"]
	if drains.Type != metrics.MetricCounter || len(drains.Series) != 2 {
		t.Fatalf("candidate drain metric = %#v, want in/out drain series", drains)
	}
	seen := map[string]bool{}
	for _, series := range drains.Series {
		if series.Labels["drain_reason"] != "maintenance" || series.Value != 1 {
			t.Fatalf("candidate drain series = %#v, want maintenance count 1", series)
		}
		seen[series.Labels["direction"]] = true
	}
	if !seen["in"] || !seen["out"] {
		t.Fatalf("candidate drain directions = %#v, want in and out", seen)
	}
	active := customs["gofly_rpc_mux_candidate_active_streams"]
	if active.Type != metrics.MetricGauge || len(active.Series) != 1 ||
		active.Series[0].Labels["drain_reason"] != "maintenance" ||
		active.Series[0].Labels["state"] != "draining" ||
		active.Series[0].Value != 1 {
		t.Fatalf("candidate active-stream metric = %#v, want one active stream during drain", active)
	}

	close(release)
	assertMuxPayload(t, stream, "drained:active")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	stop()
	if err := <-serveDone; err != nil {
		t.Fatalf("candidate mux server stopped with error: %v", err)
	}
}

func TestExperimentalMuxCandidateDrainGraceTimeoutForcesClose(t *testing.T) {
	reg := withIsolatedMuxCandidateMetrics(t)
	clientConn, serverConn := net.Pipe()
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:         "gofly-mux/drain-timeout-test",
		FrameCodec:       "binary",
		PayloadCodec:     "identity",
		ConnectionWindow: 3,
		ReceiveQueueSize: 3,
		DrainGrace:       time.Millisecond,
	}
	client := NewExperimentalMuxCandidateClientAdapter(clientConn, cfg)
	server := NewExperimentalMuxCandidateServerAdapter(serverConn, cfg)
	defer client.Close()
	defer server.Close()
	block := make(chan struct{})
	if err := server.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
		if _, err := stream.Receive(ctx); err != nil {
			return err
		}
		select {
		case <-block:
			return stream.Close(ctx, "released")
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(context.Background())
	defer stop()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(serveCtx)
	}()
	stream, err := client.OpenStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("active")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertEventually(t, func() bool {
		return client.Snapshot().Transport.ActiveStreams == 1
	}, "candidate forced close active stream")
	if err := client.Drain(context.Background(), "timeout"); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if snapshot := client.Snapshot(); !snapshot.Transport.Closed {
		t.Fatalf("client snapshot after drain timeout = %+v, want closed transport", snapshot)
	}
	if _, err := stream.Receive(muxTestTimeoutContext(t)); err == nil {
		t.Fatal("Receive after forced close succeeded, want error")
	}
	customs := reg.Snapshot().Customs
	timeouts := customs["gofly_rpc_mux_candidate_drain_timeout_total"]
	if timeouts.Type != metrics.MetricCounter || len(timeouts.Series) != 1 ||
		timeouts.Series[0].Labels["drain_reason"] != "timeout" ||
		timeouts.Series[0].Value != 1 {
		t.Fatalf("candidate drain timeout metric = %#v, want timeout count 1", timeouts)
	}
	forced := customs["gofly_rpc_mux_candidate_forced_close_total"]
	if forced.Type != metrics.MetricCounter || len(forced.Series) != 1 ||
		forced.Series[0].Labels["drain_reason"] != "timeout" ||
		forced.Series[0].Value != 1 {
		t.Fatalf("candidate forced close metric = %#v, want timeout count 1", forced)
	}
	close(block)
	stop()
	if err := <-serveDone; err != nil {
		t.Fatalf("candidate mux server stopped with error: %v", err)
	}
}

func TestExperimentalMuxCandidateCreditWaitTimeoutMetrics(t *testing.T) {
	reg := withIsolatedMuxCandidateMetrics(t)
	clientConn, serverConn := net.Pipe()
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:          "gofly-mux/credit-timeout-test",
		FrameCodec:        "binary",
		PayloadCodec:      "identity",
		ConnectionWindow:  1,
		ReceiveQueueSize:  2,
		CreditWaitTimeout: time.Millisecond,
	}
	client := NewExperimentalMuxCandidateClientAdapter(clientConn, cfg)
	server := NewExperimentalMuxCandidateServerAdapter(serverConn, cfg)
	defer client.Close()
	defer server.Close()
	hold := make(chan struct{})
	if err := server.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		return stream.Close(ctx, string(msg.Payload))
	}); err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(context.Background())
	defer stop()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(serveCtx)
	}()
	stream, err := client.OpenStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("first")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("second")}); CodeOf(err) != CodeDeadlineExceeded {
		t.Fatalf("second Send = %v, want CodeDeadlineExceeded credit wait timeout", err)
	}
	snapshot := client.Snapshot()
	if snapshot.Transport.CreditWaitTimeouts != 1 || snapshot.Transport.ConnectionWindowExhausted < 1 {
		t.Fatalf("candidate flow-control snapshot = %+v, want credit wait timeout and exhausted connection window", snapshot.Transport)
	}
	diagnosis := client.DiagnosisSnapshot()
	if diagnosis.FlowControl.CreditWaitTimeouts != 1 || diagnosis.FlowControl.ConnectionWindowExhausted < 1 {
		t.Fatalf("candidate flow-control diagnosis = %+v, want timeout/exhaustion counters", diagnosis.FlowControl)
	}
	events := reg.Snapshot().Customs["gofly_rpc_mux_candidate_flow_control_events_total"]
	if events.Type != metrics.MetricCounter || len(events.Series) != 2 {
		t.Fatalf("candidate flow-control metric = %#v, want credit timeout and window exhausted series", events)
	}
	seen := map[string]float64{}
	for _, series := range events.Series {
		seen[series.Labels["event"]] = series.Value
	}
	if seen["credit_wait_timeout"] != 1 || seen["connection_window_exhausted"] < 1 {
		t.Fatalf("candidate flow-control metric events = %#v, want credit_wait_timeout and connection_window_exhausted", seen)
	}
	close(hold)
	stop()
	if err := <-serveDone; err != nil {
		t.Fatalf("candidate mux server stopped with error: %v", err)
	}
}

func TestExperimentalMuxCandidateWriteTimeoutMetrics(t *testing.T) {
	reg := withIsolatedMuxCandidateMetrics(t)
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:     "gofly-mux/write-timeout-test",
		FrameCodec:   "binary",
		PayloadCodec: "identity",
		WriteTimeout: time.Millisecond,
	}
	client := NewExperimentalMuxCandidateClientAdapter(&timeoutWriteConn{done: make(chan struct{})}, cfg)
	defer client.Close()
	stream, err := client.OpenStream(context.Background(), "orders/Write")
	if err == nil {
		_ = stream.Close(context.Background(), "unexpected")
		t.Fatal("OpenStream succeeded, want write timeout")
	}
	snapshot := client.Snapshot()
	if snapshot.Transport.WriteTimeouts != 1 || snapshot.Transport.WriteTimeout != time.Millisecond {
		t.Fatalf("candidate write-timeout snapshot = %+v, want one write timeout", snapshot.Transport)
	}
	diagnosis := client.DiagnosisSnapshot()
	if diagnosis.FlowControl.WriteTimeouts != 1 {
		t.Fatalf("candidate write-timeout diagnosis = %+v, want write timeout counter", diagnosis.FlowControl)
	}
	events := reg.Snapshot().Customs["gofly_rpc_mux_candidate_flow_control_events_total"]
	if events.Type != metrics.MetricCounter || len(events.Series) != 1 ||
		events.Series[0].Labels["event"] != "write_timeout" ||
		events.Series[0].Value != 1 {
		t.Fatalf("candidate write-timeout metric = %#v, want write_timeout count 1", events)
	}
}

func TestExperimentalMuxConnectionManagerCandidateConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:         "gofly-mux/manager-candidate-test",
		ReceiveQueueSize: 2,
		ConnectionWindow: 3,
		FrameCodec:       "binary",
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxCandidateListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("manager:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		}, cfg)
	}()

	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{"tcp://" + listener.Addr().String()}, nil }),
		WithExperimentalMuxConnectionManagerCandidateConfig(cfg),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	stream, err := client.MuxStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("MuxStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("hello")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertMuxPayload(t, stream, "manager:hello")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if !diagnosis.Candidate.Enabled ||
		diagnosis.Candidate.Protocol != cfg.Protocol ||
		diagnosis.Candidate.FrameCodec != "binary" ||
		len(diagnosis.Endpoints) != 1 ||
		!diagnosis.Endpoints[0].Adapter.Candidate.Enabled ||
		diagnosis.Endpoints[0].Adapter.Transport.ConnectionWindow != cfg.ConnectionWindow {
		t.Fatalf("manager candidate diagnosis = %+v, want candidate policy on manager and endpoint", diagnosis)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("ServeExperimentalMuxCandidateListener returned error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerResolverBalancerIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	startMuxEchoListener := func(name string, listener net.Listener) <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- ServeExperimentalMuxListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
				return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
					msg, err := stream.Receive(ctx)
					if err != nil {
						return err
					}
					if err := stream.Send(ctx, Message{Payload: append([]byte(name+":"), msg.Payload...)}); err != nil {
						return err
					}
					return stream.Close(ctx, "ok")
				})
			})
		}()
		return done
	}
	firstDone := startMuxEchoListener("first", firstListener)
	secondDone := startMuxEchoListener("second", secondListener)

	firstEndpoint := "tcp://" + firstListener.Addr().String()
	secondEndpoint := "tcp://" + secondListener.Addr().String()
	resolver := &mutableResolver{endpoints: []string{firstEndpoint, secondEndpoint}}
	manager, err := NewExperimentalMuxConnectionManager(
		resolver,
		WithExperimentalMuxConnectionManagerIdleTimeout(time.Nanosecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	firstStream, err := client.MuxStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("first MuxStream: %v", err)
	}
	if err := firstStream.Send(context.Background(), Message{Payload: []byte("a")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	assertMuxPayload(t, firstStream, "first:a")
	if _, err := firstStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("first terminal receive = %v, want EOF", err)
	}
	secondStream, err := client.MuxStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("second MuxStream: %v", err)
	}
	if err := secondStream.Send(context.Background(), Message{Payload: []byte("b")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	assertMuxPayload(t, secondStream, "second:b")
	if _, err := secondStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("second terminal receive = %v, want EOF", err)
	}

	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if !diagnosis.Enabled || diagnosis.Mode != "experimental_mux_manager" || len(diagnosis.Endpoints) != 2 {
		t.Fatalf("mux manager diagnosis = %+v, want two resolver-balanced endpoints", diagnosis)
	}
	resolver.endpoints = []string{secondEndpoint}
	if err := manager.SyncResolver(context.Background()); err != nil {
		t.Fatalf("SyncResolver: %v", err)
	}
	synced := manager.Snapshot()
	if len(synced.Endpoints) != 1 || synced.Endpoints[0].Endpoint != secondEndpoint {
		t.Fatalf("manager snapshot after resolver sync = %+v, want only second endpoint", synced)
	}
	time.Sleep(time.Millisecond)
	if err := manager.CloseIdle(context.Background()); err != nil {
		t.Fatalf("CloseIdle: %v", err)
	}
	if snapshot := manager.Snapshot(); len(snapshot.Endpoints) != 0 || snapshot.CloseReasons["idle"] != 1 || snapshot.DrainReasons["idle"] != 1 {
		t.Fatalf("manager snapshot after CloseIdle = %+v, want no cached adapters and idle drain evidence", snapshot)
	}
	cancel()
	if err := <-firstDone; err != nil {
		t.Fatalf("first mux listener stopped with error: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerMaxStreamsPerConnOpensEndpointPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 2)
	releaseHandlers := make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				requests <- string(msg.Payload)
				select {
				case <-releaseHandlers:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("ack:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()

	endpoint := "tcp://" + listener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{endpoint}, nil }),
		WithExperimentalMuxConnectionManagerIdleTimeout(time.Nanosecond),
		WithExperimentalMuxConnectionManagerMaxStreamsPerConn(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	firstStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("first MuxStream: %v", err)
	}
	if err := firstStream.Send(context.Background(), Message{Payload: []byte("first")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if got := <-requests; got != "first" {
		t.Fatalf("first handler payload = %q, want first", got)
	}
	firstSnapshot := manager.Snapshot()
	if len(firstSnapshot.Endpoints) != 1 ||
		firstSnapshot.MaxStreamsPerConn != 1 ||
		firstSnapshot.Endpoints[0].Adapter.Transport.ActiveStreams != 1 {
		t.Fatalf("manager snapshot after first stream = %+v, want one active pooled connection", firstSnapshot)
	}

	secondStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("second MuxStream: %v", err)
	}
	if err := secondStream.Send(context.Background(), Message{Payload: []byte("second")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if got := <-requests; got != "second" {
		t.Fatalf("second handler payload = %q, want second", got)
	}
	pooled := manager.Snapshot()
	if len(pooled.Endpoints) != 2 {
		t.Fatalf("manager snapshot after second stream = %+v, want two pooled adapters for one endpoint", pooled)
	}
	for i, endpointSnapshot := range pooled.Endpoints {
		if endpointSnapshot.Endpoint != endpoint || endpointSnapshot.Adapter.Transport.ActiveStreams != 1 {
			t.Fatalf("pooled endpoint[%d] = %+v, want same endpoint with one active stream", i, endpointSnapshot)
		}
	}
	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if !diagnosis.Enabled || diagnosis.MaxStreamsPerConn != 1 || len(diagnosis.Endpoints) != 2 {
		t.Fatalf("mux manager diagnosis = %+v, want two pooled adapters and max stream setting", diagnosis)
	}

	close(releaseHandlers)
	assertMuxPayload(t, firstStream, "ack:first")
	assertMuxPayload(t, secondStream, "ack:second")
	for name, stream := range map[string]*ExperimentalMuxStream{"first": firstStream, "second": secondStream} {
		if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
			t.Fatalf("%s terminal receive = %v, want EOF", name, err)
		}
	}
	assertEventually(t, func() bool {
		snapshot := manager.Snapshot()
		if len(snapshot.Endpoints) != 2 {
			return false
		}
		for _, endpointSnapshot := range snapshot.Endpoints {
			if endpointSnapshot.Adapter.Transport.ActiveStreams != 0 {
				return false
			}
		}
		return true
	}, "mux manager pooled streams drained")
	time.Sleep(time.Millisecond)
	if err := manager.CloseIdle(context.Background()); err != nil {
		t.Fatalf("CloseIdle: %v", err)
	}
	if snapshot := manager.Snapshot(); len(snapshot.Endpoints) != 0 ||
		snapshot.ClosedAdapters != 2 ||
		snapshot.CloseReasons["idle"] != 2 ||
		snapshot.DrainReasons["idle"] != 2 {
		t.Fatalf("manager snapshot after idle close = %+v, want both pooled adapters closed", snapshot)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerDiagnosisFiltersEndpointFlowControl(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := ExperimentalMuxCandidateConfig{
		Protocol:          "gofly-mux/manager-flow-filter-test",
		FrameCodec:        "binary",
		PayloadCodec:      "identity",
		ConnectionWindow:  1,
		ReceiveQueueSize:  2,
		CreditWaitTimeout: time.Millisecond,
	}
	firstHold := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- ServeExperimentalMuxCandidateListener(ctx, firstListener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				select {
				case <-firstHold:
				case <-ctx.Done():
					return ctx.Err()
				}
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				return stream.Close(ctx, string(msg.Payload))
			})
		}, cfg)
	}()
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- ServeExperimentalMuxCandidateListener(ctx, secondListener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("second:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		}, cfg)
	}()
	firstEndpoint := "tcp://" + firstListener.Addr().String()
	secondEndpoint := "tcp://" + secondListener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) {
			return []string{firstEndpoint, secondEndpoint}, nil
		}),
		WithExperimentalMuxConnectionManagerCandidateConfig(cfg),
		WithExperimentalMuxConnectionManagerBalancer(firstEndpointBalancer{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	holdStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("MuxStream first endpoint: %v", err)
	}
	if err := holdStream.Send(context.Background(), Message{Payload: []byte("first")}); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if err := holdStream.Send(context.Background(), Message{Payload: []byte("blocked")}); CodeOf(err) != CodeDeadlineExceeded {
		t.Fatalf("blocked send = %v, want CodeDeadlineExceeded", err)
	}
	manager.mu.Lock()
	manager.health[firstEndpoint] = &muxEndpointHealth{ejectedAt: time.Now(), reason: "test_skip", cooldownUntil: time.Now().Add(time.Second)}
	manager.mu.Unlock()
	watchStream, err := client.MuxStream(context.Background(), "orders/Watch")
	if err != nil {
		t.Fatalf("MuxStream second endpoint: %v", err)
	}
	if err := watchStream.Send(context.Background(), Message{Payload: []byte("ok")}); err != nil {
		t.Fatalf("second send: %v", err)
	}
	assertMuxPayload(t, watchStream, "second:ok")
	if _, err := watchStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("second terminal receive = %v, want EOF", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?endpoint="+url.QueryEscape(firstEndpoint)+"&flowControlEvent=credit-wait-timeout", nil)
	rec := httptest.NewRecorder()
	client.DiagnosisHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("diagnosis status = %d body=%s", rec.Code, rec.Body.String())
	}
	var probe RPCDiagnosisProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
		t.Fatalf("decode diagnosis: %v\n%s", err, rec.Body.String())
	}
	if !probe.Matched || probe.Endpoint != firstEndpoint ||
		probe.FlowControl != "credit_wait_timeout" ||
		len(probe.Diagnosis.Mux.Manager.Endpoints) != 1 ||
		probe.Diagnosis.Mux.Manager.Endpoints[0].Endpoint != firstEndpoint ||
		len(probe.Diagnosis.Mux.Manager.FlowControl.Events) != 1 ||
		probe.Diagnosis.Mux.Manager.FlowControl.Events[0].Event != "credit_wait_timeout" ||
		probe.Diagnosis.Mux.Manager.FlowControl.Events[0].Count < 1 {
		t.Fatalf("filtered manager diagnosis = %+v, want first endpoint credit_wait_timeout event", probe)
	}
	if probe.Diagnosis.Mux.Manager.FlowControl.ConnectionWindowExhausted != 0 ||
		probe.Diagnosis.Mux.Manager.FlowControl.WriteTimeouts != 0 {
		t.Fatalf("filtered manager flow-control = %+v, want unrelated counters hidden", probe.Diagnosis.Mux.Manager.FlowControl)
	}
	firstConnectionID := probe.Diagnosis.Mux.Manager.Endpoints[0].ConnectionID
	firstPoolSlot := probe.Diagnosis.Mux.Manager.Endpoints[0].PoolSlot
	if firstConnectionID == "" || firstPoolSlot != 1 {
		t.Fatalf("first endpoint connection fields = %+v, want stable connection id and pool slot", probe.Diagnosis.Mux.Manager.Endpoints[0])
	}
	req = httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?connectionId="+url.QueryEscape(firstConnectionID)+"&flowControlEvent=credit-wait-timeout", nil)
	rec = httptest.NewRecorder()
	client.DiagnosisHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("connection diagnosis status = %d body=%s", rec.Code, rec.Body.String())
	}
	var connectionProbe RPCDiagnosisProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &connectionProbe); err != nil {
		t.Fatalf("decode connection diagnosis: %v\n%s", err, rec.Body.String())
	}
	if !connectionProbe.Matched ||
		connectionProbe.ConnectionID != firstConnectionID ||
		len(connectionProbe.Diagnosis.Mux.Manager.Endpoints) != 1 ||
		connectionProbe.Diagnosis.Mux.Manager.Endpoints[0].ConnectionID != firstConnectionID ||
		len(connectionProbe.Diagnosis.Mux.Manager.FlowControl.Events) != 1 ||
		connectionProbe.Diagnosis.Mux.Manager.FlowControl.Events[0].Event != "credit_wait_timeout" {
		t.Fatalf("connection filtered diagnosis = %+v, want first connection credit_wait_timeout event", connectionProbe)
	}
	req = httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?endpoint="+url.QueryEscape(secondEndpoint)+"&flowControlEvent=credit-wait-timeout", nil)
	rec = httptest.NewRecorder()
	client.DiagnosisHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second diagnosis status = %d body=%s", rec.Code, rec.Body.String())
	}
	var secondProbe RPCDiagnosisProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &secondProbe); err != nil {
		t.Fatalf("decode second diagnosis: %v\n%s", err, rec.Body.String())
	}
	if !secondProbe.Matched ||
		len(secondProbe.Diagnosis.Mux.Manager.Endpoints) != 1 ||
		secondProbe.Diagnosis.Mux.Manager.Endpoints[0].Endpoint != secondEndpoint ||
		len(secondProbe.Diagnosis.Mux.Manager.FlowControl.Events) != 0 {
		t.Fatalf("second filtered diagnosis = %+v, want second endpoint without flow-control event", secondProbe)
	}
	secondConnectionID := secondProbe.Diagnosis.Mux.Manager.Endpoints[0].ConnectionID
	if secondConnectionID == "" || secondConnectionID == firstConnectionID ||
		secondProbe.Diagnosis.Mux.Manager.Endpoints[0].PoolSlot != 1 {
		t.Fatalf("second endpoint connection fields = %+v, want distinct connection id and endpoint-local pool slot", secondProbe.Diagnosis.Mux.Manager.Endpoints[0])
	}
	req = httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?endpoint="+url.QueryEscape(secondEndpoint)+"&poolSlot=1&flowControlEvent=credit-wait-timeout", nil)
	rec = httptest.NewRecorder()
	client.DiagnosisHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pool slot diagnosis status = %d body=%s", rec.Code, rec.Body.String())
	}
	var poolSlotProbe RPCDiagnosisProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &poolSlotProbe); err != nil {
		t.Fatalf("decode pool slot diagnosis: %v\n%s", err, rec.Body.String())
	}
	if !poolSlotProbe.Matched ||
		poolSlotProbe.PoolSlot != 1 ||
		len(poolSlotProbe.Diagnosis.Mux.Manager.Endpoints) != 1 ||
		poolSlotProbe.Diagnosis.Mux.Manager.Endpoints[0].ConnectionID != secondConnectionID ||
		len(poolSlotProbe.Diagnosis.Mux.Manager.FlowControl.Events) != 0 {
		t.Fatalf("pool slot filtered diagnosis = %+v, want second endpoint slot without credit event", poolSlotProbe)
	}
	req = httptest.NewRequest(http.MethodGet, "/rpc/diagnosis?endpoint="+url.QueryEscape(secondEndpoint)+"&connectionId=missing&flowControlEvent=credit-wait-timeout", nil)
	rec = httptest.NewRecorder()
	client.DiagnosisHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("missing connection diagnosis status = %d body=%s", rec.Code, rec.Body.String())
	}
	var missingConnectionProbe RPCDiagnosisProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &missingConnectionProbe); err != nil {
		t.Fatalf("decode missing connection diagnosis: %v\n%s", err, rec.Body.String())
	}
	if missingConnectionProbe.Matched ||
		len(missingConnectionProbe.Diagnosis.Mux.Manager.Endpoints) != 0 ||
		len(missingConnectionProbe.Diagnosis.Mux.Manager.FlowControl.Events) != 0 {
		t.Fatalf("missing connection diagnosis = %+v, want no match even when endpoint exists", missingConnectionProbe)
	}

	close(firstHold)
	cancel()
	if err := <-firstDone; err != nil {
		t.Fatalf("first mux listener stopped with error: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerMaxConnsPerEndpointRejectsWhenPoolFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 1)
	releaseHandler := make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				requests <- string(msg.Payload)
				select {
				case <-releaseHandler:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("ack:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()

	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{"tcp://" + listener.Addr().String()}, nil }),
		WithExperimentalMuxConnectionManagerMaxStreamsPerConn(1),
		WithExperimentalMuxConnectionManagerMaxConnsPerEndpoint(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	stream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("first MuxStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("active")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if got := <-requests; got != "active" {
		t.Fatalf("handler payload = %q, want active", got)
	}
	second, err := client.MuxStream(context.Background(), "orders/Hold")
	if err == nil || second != nil || CodeOf(err) != CodeUnavailable {
		t.Fatalf("second MuxStream = stream %#v err %v, want CodeUnavailable pool exhaustion", second, err)
	}
	snapshot := manager.Snapshot()
	if snapshot.MaxConnsPerEndpoint != 1 || snapshot.PoolExhaustions != 1 || len(snapshot.Endpoints) != 1 {
		t.Fatalf("manager snapshot after pool exhaustion = %+v, want one endpoint and one exhaustion", snapshot)
	}
	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if diagnosis.PoolExhaustions != 1 || diagnosis.MaxConnsPerEndpoint != 1 {
		t.Fatalf("mux manager diagnosis after pool exhaustion = %+v, want pool exhaustion evidence", diagnosis)
	}

	close(releaseHandler)
	assertMuxPayload(t, stream, "ack:active")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerSkipsEndpointAfterPoolExhaustion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	firstRequests := make(chan string, 1)
	releaseFirst := make(chan struct{})
	startFirst := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- ServeExperimentalMuxListener(ctx, firstListener, func(adapter *ExperimentalMuxServerAdapter) error {
				return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
					msg, err := stream.Receive(ctx)
					if err != nil {
						return err
					}
					firstRequests <- string(msg.Payload)
					select {
					case <-releaseFirst:
					case <-ctx.Done():
						return ctx.Err()
					}
					if err := stream.Send(ctx, Message{Payload: append([]byte("first:"), msg.Payload...)}); err != nil {
						return err
					}
					return stream.Close(ctx, "ok")
				})
			})
		}()
		return done
	}
	startSecond := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- ServeExperimentalMuxListener(ctx, secondListener, func(adapter *ExperimentalMuxServerAdapter) error {
				return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
					msg, err := stream.Receive(ctx)
					if err != nil {
						return err
					}
					if err := stream.Send(ctx, Message{Payload: append([]byte("second:"), msg.Payload...)}); err != nil {
						return err
					}
					return stream.Close(ctx, "ok")
				})
			})
		}()
		return done
	}
	firstDone := startFirst()
	secondDone := startSecond()

	firstEndpoint := "tcp://" + firstListener.Addr().String()
	secondEndpoint := "tcp://" + secondListener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{firstEndpoint, secondEndpoint}, nil }),
		WithExperimentalMuxConnectionManagerBalancer(firstEndpointBalancer{}),
		WithExperimentalMuxConnectionManagerMaxStreamsPerConn(1),
		WithExperimentalMuxConnectionManagerMaxConnsPerEndpoint(1),
		WithExperimentalMuxConnectionManagerHealthFailureThreshold(1),
		WithExperimentalMuxConnectionManagerHealthEjectionDuration(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	firstStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("first MuxStream: %v", err)
	}
	if err := firstStream.Send(context.Background(), Message{Payload: []byte("active")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if got := <-firstRequests; got != "active" {
		t.Fatalf("first handler payload = %q, want active", got)
	}
	if adapter, err := manager.adapter(context.Background(), firstEndpoint); err == nil || adapter != nil || CodeOf(err) != CodeUnavailable {
		t.Fatalf("pool exhaustion adapter = adapter %#v err %v, want CodeUnavailable", adapter, err)
	}
	snapshot := manager.Snapshot()
	if snapshot.EndpointEjections != 1 ||
		len(snapshot.Health) != 1 ||
		snapshot.Health[0].Endpoint != firstEndpoint ||
		!snapshot.Health[0].Ejected ||
		snapshot.Health[0].Reason != "pool_exhausted" {
		t.Fatalf("manager snapshot after pool exhaustion = %+v, want first endpoint ejected", snapshot)
	}

	secondStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("second MuxStream after ejection: %v", err)
	}
	if err := secondStream.Send(context.Background(), Message{Payload: []byte("fresh")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	assertMuxPayload(t, secondStream, "second:fresh")
	if _, err := secondStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("second terminal receive = %v, want EOF", err)
	}

	close(releaseFirst)
	assertMuxPayload(t, firstStream, "first:active")
	if _, err := firstStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("first terminal receive = %v, want EOF", err)
	}
	cancel()
	if err := <-firstDone; err != nil {
		t.Fatalf("first mux listener stopped with error: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerRetriesOpenBeforeStreamAfterPoolExhaustion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	firstRequests := make(chan string, 1)
	releaseFirst := make(chan struct{})
	startFirst := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- ServeExperimentalMuxListener(ctx, firstListener, func(adapter *ExperimentalMuxServerAdapter) error {
				return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
					msg, err := stream.Receive(ctx)
					if err != nil {
						return err
					}
					firstRequests <- string(msg.Payload)
					select {
					case <-releaseFirst:
					case <-ctx.Done():
						return ctx.Err()
					}
					if err := stream.Send(ctx, Message{Payload: append([]byte("first:"), msg.Payload...)}); err != nil {
						return err
					}
					return stream.Close(ctx, "ok")
				})
			})
		}()
		return done
	}
	startSecond := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- ServeExperimentalMuxListener(ctx, secondListener, func(adapter *ExperimentalMuxServerAdapter) error {
				return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
					msg, err := stream.Receive(ctx)
					if err != nil {
						return err
					}
					if err := stream.Send(ctx, Message{Payload: append([]byte("second:"), msg.Payload...)}); err != nil {
						return err
					}
					return stream.Close(ctx, "ok")
				})
			})
		}()
		return done
	}
	firstDone := startFirst()
	secondDone := startSecond()

	firstEndpoint := "tcp://" + firstListener.Addr().String()
	secondEndpoint := "tcp://" + secondListener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{firstEndpoint, secondEndpoint}, nil }),
		WithExperimentalMuxConnectionManagerBalancer(firstEndpointBalancer{}),
		WithExperimentalMuxConnectionManagerMaxStreamsPerConn(1),
		WithExperimentalMuxConnectionManagerMaxConnsPerEndpoint(1),
		WithExperimentalMuxConnectionManagerHealthFailureThreshold(1),
		WithExperimentalMuxConnectionManagerHealthEjectionDuration(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	firstStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("first MuxStream: %v", err)
	}
	if err := firstStream.Send(context.Background(), Message{Payload: []byte("active")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if got := <-firstRequests; got != "active" {
		t.Fatalf("first handler payload = %q, want active", got)
	}
	secondStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("second MuxStream should retry on second endpoint: %v", err)
	}
	if err := secondStream.Send(context.Background(), Message{Payload: []byte("fresh")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	assertMuxPayload(t, secondStream, "second:fresh")
	if _, err := secondStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("second terminal receive = %v, want EOF", err)
	}
	snapshot := manager.Snapshot()
	if snapshot.PoolExhaustions != 1 ||
		snapshot.EndpointEjections != 1 ||
		snapshot.OpenRetries != 1 ||
		snapshot.LastRetriedFrom != firstEndpoint ||
		snapshot.LastRetriedTo != secondEndpoint ||
		snapshot.RetryReasons["pool_exhausted"] != 1 ||
		len(snapshot.Health) != 1 ||
		snapshot.Health[0].Endpoint != firstEndpoint ||
		snapshot.Health[0].Reason != "pool_exhausted" {
		t.Fatalf("manager snapshot after retry = %+v, want pool exhaustion and first endpoint health evidence", snapshot)
	}
	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if diagnosis.OpenRetries != 1 ||
		diagnosis.LastRetriedFrom != firstEndpoint ||
		diagnosis.LastRetriedTo != secondEndpoint ||
		diagnosis.RetryReasons["pool_exhausted"] != 1 {
		t.Fatalf("mux manager diagnosis after retry = %+v, want retry endpoint and reason evidence", diagnosis)
	}

	close(releaseFirst)
	assertMuxPayload(t, firstStream, "first:active")
	if _, err := firstStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("first terminal receive = %v, want EOF", err)
	}
	cancel()
	if err := <-firstDone; err != nil {
		t.Fatalf("first mux listener stopped with error: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerSkipsEndpointAfterDialFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	badListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	badEndpoint := "tcp://" + badListener.Addr().String()
	if err := badListener.Close(); err != nil {
		t.Fatalf("close bad listener: %v", err)
	}
	goodListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	goodDone := make(chan error, 1)
	go func() {
		goodDone <- ServeExperimentalMuxListener(ctx, goodListener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Echo", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("good:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()
	goodEndpoint := "tcp://" + goodListener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{badEndpoint, goodEndpoint}, nil }),
		WithExperimentalMuxConnectionManagerHealthFailureThreshold(1),
		WithExperimentalMuxConnectionManagerHealthEjectionDuration(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	stream, err := client.MuxStream(context.Background(), "orders/Echo")
	if err != nil {
		t.Fatalf("MuxStream should retry after dial failure: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("fresh")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertMuxPayload(t, stream, "good:fresh")
	if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal receive = %v, want EOF", err)
	}
	snapshot := manager.Snapshot()
	if snapshot.DialFailures != 1 ||
		snapshot.EndpointEjections != 1 ||
		snapshot.OpenRetries != 1 ||
		snapshot.LastRetriedFrom != badEndpoint ||
		snapshot.LastRetriedTo != goodEndpoint ||
		snapshot.RetryReasons["dial_failure"] != 1 ||
		len(snapshot.Health) != 1 ||
		snapshot.Health[0].Endpoint != badEndpoint ||
		!snapshot.Health[0].Ejected ||
		snapshot.Health[0].Reason != "dial_failure" {
		t.Fatalf("manager snapshot after dial failure = %+v, want bad endpoint ejected", snapshot)
	}
	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if diagnosis.OpenRetries != 1 ||
		diagnosis.LastRetriedFrom != badEndpoint ||
		diagnosis.LastRetriedTo != goodEndpoint ||
		diagnosis.RetryReasons["dial_failure"] != 1 {
		t.Fatalf("mux manager diagnosis after dial retry = %+v, want retry endpoint and reason evidence", diagnosis)
	}

	cancel()
	if err := <-goodDone; err != nil {
		t.Fatalf("good mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerDoesNotReplayAfterStreamOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- ServeExperimentalMuxListener(ctx, firstListener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/FailAfterOpen", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				if _, err := stream.Receive(ctx); err != nil {
					return err
				}
				return NewError(CodeUnavailable, "failed after stream opened")
			})
		})
	}()
	secondAccepted := make(chan struct{}, 1)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- ServeExperimentalMuxListener(ctx, secondListener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/FailAfterOpen", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				secondAccepted <- struct{}{}
				return stream.Close(ctx, "unexpected")
			})
		})
	}()

	firstEndpoint := "tcp://" + firstListener.Addr().String()
	secondEndpoint := "tcp://" + secondListener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{firstEndpoint, secondEndpoint}, nil }),
		WithExperimentalMuxConnectionManagerMaxOpenRetries(1),
		WithExperimentalMuxConnectionManagerOpenRetryReasons("dial_failure", "pool_exhausted", "open_stream"),
		WithExperimentalMuxConnectionManagerHealthFailureThreshold(1),
		WithExperimentalMuxConnectionManagerHealthEjectionDuration(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	stream, err := client.MuxStream(context.Background(), "orders/FailAfterOpen")
	if err != nil {
		t.Fatalf("MuxStream: %v", err)
	}
	if err := stream.Send(context.Background(), Message{Payload: []byte("request")}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := stream.Receive(muxTestTimeoutContext(t)); CodeOf(err) != CodeUnavailable {
		t.Fatalf("Receive = %v, want CodeUnavailable", err)
	}
	select {
	case <-secondAccepted:
		t.Fatal("second endpoint received a replay after stream opened")
	default:
	}
	snapshot := manager.Snapshot()
	if snapshot.OpenRetries != 0 ||
		snapshot.RetryReasons["open_stream"] != 0 ||
		snapshot.EndpointEjections != 0 {
		t.Fatalf("manager snapshot after post-open failure = %+v, want no open retry replay", snapshot)
	}

	cancel()
	if err := <-firstDone; err != nil {
		t.Fatalf("first mux listener stopped with error: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerEndpointHealthBackoffCooldown(t *testing.T) {
	endpoint := "tcp://127.0.0.1:1"
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{endpoint, "tcp://127.0.0.1:2"}, nil }),
		WithExperimentalMuxConnectionManagerHealthFailureThreshold(1),
		WithExperimentalMuxConnectionManagerHealthEjectionDuration(10*time.Millisecond),
		WithExperimentalMuxConnectionManagerHealthBackoffMultiplier(2),
		WithExperimentalMuxConnectionManagerHealthMaxCooldown(25*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	now := time.Unix(100, 0)
	manager.mu.Lock()
	manager.recordEndpointFailureAtLocked(endpoint, "dial_failure", NewError(CodeUnavailable, "first"), now)
	firstHealthy := manager.healthyEndpointsLocked([]string{endpoint, "tcp://127.0.0.1:2"}, now)
	manager.mu.Unlock()
	if len(firstHealthy) != 1 || firstHealthy[0] != "tcp://127.0.0.1:2" {
		t.Fatalf("healthy after first failure = %v, want only second endpoint", firstHealthy)
	}
	snapshot := manager.Snapshot()
	if snapshot.HealthBackoffMultiplier != 2 ||
		snapshot.HealthMaxCooldown != 25*time.Millisecond ||
		len(snapshot.Health) != 1 ||
		snapshot.Health[0].Cooldown != 10*time.Millisecond ||
		!snapshot.Health[0].CooldownUntil.Equal(now.Add(10*time.Millisecond)) {
		t.Fatalf("snapshot after first failure = %+v, want 10ms cooldown", snapshot)
	}

	manager.mu.Lock()
	manager.recordEndpointFailureAtLocked(endpoint, "dial_failure", NewError(CodeUnavailable, "second"), now.Add(time.Millisecond))
	manager.mu.Unlock()
	snapshot = manager.Snapshot()
	if snapshot.Health[0].Cooldown != 20*time.Millisecond ||
		!snapshot.Health[0].CooldownUntil.Equal(now.Add(time.Millisecond).Add(20*time.Millisecond)) ||
		snapshot.EndpointEjections != 2 {
		t.Fatalf("snapshot after second failure = %+v, want doubled cooldown", snapshot)
	}

	manager.mu.Lock()
	manager.recordEndpointFailureAtLocked(endpoint, "dial_failure", NewError(CodeUnavailable, "third"), now.Add(2*time.Millisecond))
	manager.mu.Unlock()
	snapshot = manager.Snapshot()
	if snapshot.Health[0].Cooldown != 25*time.Millisecond ||
		!snapshot.Health[0].CooldownUntil.Equal(now.Add(2*time.Millisecond).Add(25*time.Millisecond)) {
		t.Fatalf("snapshot after third failure = %+v, want capped cooldown", snapshot)
	}

	manager.mu.Lock()
	recovered := manager.healthyEndpointsLocked([]string{endpoint, "tcp://127.0.0.1:2"}, now.Add(28*time.Millisecond))
	manager.mu.Unlock()
	if len(recovered) != 2 || manager.Snapshot().EndpointRecoveries != 1 {
		t.Fatalf("healthy after cooldown = %v snapshot=%+v, want recovered endpoint", recovered, manager.Snapshot())
	}
}

func TestExperimentalMuxConnectionManagerOpenRetryPolicyCanDisableRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	badListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	badEndpoint := "tcp://" + badListener.Addr().String()
	if err := badListener.Close(); err != nil {
		t.Fatalf("close bad listener: %v", err)
	}
	goodListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	goodDone := make(chan error, 1)
	go func() {
		goodDone <- ServeExperimentalMuxListener(ctx, goodListener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Echo", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("good:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()
	goodEndpoint := "tcp://" + goodListener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{badEndpoint, goodEndpoint}, nil }),
		WithExperimentalMuxConnectionManagerMaxOpenRetries(0),
		WithExperimentalMuxConnectionManagerHealthFailureThreshold(1),
		WithExperimentalMuxConnectionManagerHealthEjectionDuration(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if stream, err := client.MuxStream(context.Background(), "orders/Echo"); err == nil || stream != nil || CodeOf(err) != CodeUnavailable {
		t.Fatalf("MuxStream with retry disabled = stream %#v err %v, want CodeUnavailable", stream, err)
	}
	snapshot := manager.Snapshot()
	if snapshot.MaxOpenRetries != 0 ||
		snapshot.OpenRetries != 0 ||
		snapshot.DialFailures != 1 ||
		snapshot.EndpointEjections != 1 {
		t.Fatalf("manager snapshot with retry disabled = %+v, want dial failure without open retry", snapshot)
	}

	cancel()
	if err := <-goodDone; err != nil {
		t.Fatalf("good mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerJanitorClosesExcessIdlePool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan string, 2)
	releaseHandlers := make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Echo", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				requests <- string(msg.Payload)
				select {
				case <-releaseHandlers:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("ack:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()

	endpoint := "tcp://" + listener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{endpoint}, nil }),
		WithExperimentalMuxConnectionManagerMaxStreamsPerConn(1),
		WithExperimentalMuxConnectionManagerMaxIdleConnsPerEndpoint(1),
		WithExperimentalMuxConnectionManagerJanitorInterval(5*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	first, err := client.MuxStream(context.Background(), "orders/Echo")
	if err != nil {
		t.Fatalf("first MuxStream: %v", err)
	}
	if err := first.Send(context.Background(), Message{Payload: []byte("first")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	second, err := client.MuxStream(context.Background(), "orders/Echo")
	if err != nil {
		t.Fatalf("second MuxStream: %v", err)
	}
	if err := second.Send(context.Background(), Message{Payload: []byte("second")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	gotRequests := map[string]bool{<-requests: true, <-requests: true}
	if !gotRequests["first"] || !gotRequests["second"] {
		t.Fatalf("handler payloads = %#v, want first and second", gotRequests)
	}
	close(releaseHandlers)
	assertMuxPayload(t, first, "ack:first")
	assertMuxPayload(t, second, "ack:second")
	for name, stream := range map[string]*ExperimentalMuxStream{"first": first, "second": second} {
		if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
			t.Fatalf("%s terminal receive = %v, want EOF", name, err)
		}
	}
	assertEventually(t, func() bool {
		snapshot := manager.Snapshot()
		return len(snapshot.Endpoints) == 1 &&
			snapshot.ClosedAdapters == 1 &&
			snapshot.CloseReasons["max_idle"] == 1 &&
			snapshot.DrainReasons["max_idle"] == 1 &&
			snapshot.JanitorRuns > 0 &&
			snapshot.MaxIdleConnsPerEndpoint == 1 &&
			snapshot.JanitorInterval == 5*time.Millisecond
	}, "mux manager janitor closed excess idle connection")

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerEvictsUnhealthyConnectionBeforePoolLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeExperimentalMuxListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
			return adapter.RegisterStream("orders/Echo", func(ctx context.Context, stream *ExperimentalMuxStream) error {
				msg, err := stream.Receive(ctx)
				if err != nil {
					return err
				}
				if err := stream.Send(ctx, Message{Payload: append([]byte("ack:"), msg.Payload...)}); err != nil {
					return err
				}
				return stream.Close(ctx, "ok")
			})
		})
	}()

	endpoint := "tcp://" + listener.Addr().String()
	manager, err := NewExperimentalMuxConnectionManager(
		ResolverFunc(func(context.Context) ([]string, error) { return []string{endpoint}, nil }),
		WithExperimentalMuxConnectionManagerMaxConnsPerEndpoint(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	first, err := client.MuxStream(context.Background(), "orders/Echo")
	if err != nil {
		t.Fatalf("first MuxStream: %v", err)
	}
	if err := first.Send(context.Background(), Message{Payload: []byte("first")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	assertMuxPayload(t, first, "ack:first")
	if _, err := first.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("first terminal receive = %v, want EOF", err)
	}
	manager.mu.Lock()
	if len(manager.adapters[endpoint]) != 1 {
		t.Fatalf("manager adapters for %s = %d, want one", endpoint, len(manager.adapters[endpoint]))
	}
	closedAdapter := manager.adapters[endpoint][0].adapter
	manager.mu.Unlock()
	if err := closedAdapter.Close(); err != nil {
		t.Fatalf("close cached adapter: %v", err)
	}

	second, err := client.MuxStream(context.Background(), "orders/Echo")
	if err != nil {
		t.Fatalf("second MuxStream after unhealthy eviction: %v", err)
	}
	if err := second.Send(context.Background(), Message{Payload: []byte("second")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	assertMuxPayload(t, second, "ack:second")
	if _, err := second.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("second terminal receive = %v, want EOF", err)
	}
	snapshot := manager.Snapshot()
	if snapshot.UnhealthyAdapters != 1 ||
		snapshot.CloseReasons["unhealthy"] != 1 ||
		snapshot.PoolExhaustions != 0 ||
		len(snapshot.Endpoints) != 1 ||
		snapshot.Endpoints[0].Adapter.Transport.Closed {
		t.Fatalf("manager snapshot after unhealthy eviction = %+v, want one healthy replacement connection", snapshot)
	}

	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerResolverRemovalDrainsActivePoolBeforeIdleClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	firstRequests := make(chan string, 1)
	releaseFirst := make(chan struct{})
	startFirst := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- ServeExperimentalMuxListener(ctx, firstListener, func(adapter *ExperimentalMuxServerAdapter) error {
				return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
					msg, err := stream.Receive(ctx)
					if err != nil {
						return err
					}
					firstRequests <- string(msg.Payload)
					select {
					case <-releaseFirst:
					case <-ctx.Done():
						return ctx.Err()
					}
					if err := stream.Send(ctx, Message{Payload: append([]byte("first:"), msg.Payload...)}); err != nil {
						return err
					}
					return stream.Close(ctx, "ok")
				})
			})
		}()
		return done
	}
	startSecond := func() <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- ServeExperimentalMuxListener(ctx, secondListener, func(adapter *ExperimentalMuxServerAdapter) error {
				return adapter.RegisterStream("orders/Hold", func(ctx context.Context, stream *ExperimentalMuxStream) error {
					msg, err := stream.Receive(ctx)
					if err != nil {
						return err
					}
					if err := stream.Send(ctx, Message{Payload: append([]byte("second:"), msg.Payload...)}); err != nil {
						return err
					}
					return stream.Close(ctx, "ok")
				})
			})
		}()
		return done
	}
	firstDone := startFirst()
	secondDone := startSecond()

	firstEndpoint := "tcp://" + firstListener.Addr().String()
	secondEndpoint := "tcp://" + secondListener.Addr().String()
	resolver := &mutableResolver{endpoints: []string{firstEndpoint, secondEndpoint}}
	manager, err := NewExperimentalMuxConnectionManager(
		resolver,
		WithExperimentalMuxConnectionManagerIdleTimeout(time.Nanosecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	firstStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("first MuxStream: %v", err)
	}
	if err := firstStream.Send(context.Background(), Message{Payload: []byte("active")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if got := <-firstRequests; got != "active" {
		t.Fatalf("first handler payload = %q, want active", got)
	}
	resolver.endpoints = []string{secondEndpoint}
	if err := manager.SyncResolver(context.Background()); err != nil {
		t.Fatalf("SyncResolver: %v", err)
	}
	draining := manager.Snapshot()
	if len(draining.Endpoints) != 1 ||
		draining.Endpoints[0].Endpoint != firstEndpoint ||
		!draining.Endpoints[0].Retired ||
		draining.Endpoints[0].Adapter.Transport.ActiveStreams != 1 ||
		draining.RetiredAdapters != 1 ||
		draining.ClosedAdapters != 0 ||
		draining.CloseReasons["resolver_update"] != 0 ||
		draining.DrainReasons["resolver_update"] != 1 {
		t.Fatalf("manager snapshot after active resolver removal = %+v, want retired draining active adapter", draining)
	}
	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if diagnosis.RetiredAdapters != 1 ||
		len(diagnosis.Endpoints) != 1 ||
		!diagnosis.Endpoints[0].Retired {
		t.Fatalf("mux manager diagnosis after active resolver removal = %+v, want retired adapter evidence", diagnosis)
	}

	secondStream, err := client.MuxStream(context.Background(), "orders/Hold")
	if err != nil {
		t.Fatalf("second MuxStream: %v", err)
	}
	if err := secondStream.Send(context.Background(), Message{Payload: []byte("fresh")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	assertMuxPayload(t, secondStream, "second:fresh")
	if _, err := secondStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("second terminal receive = %v, want EOF", err)
	}
	afterFresh := manager.Snapshot()
	if len(afterFresh.Endpoints) != 2 {
		t.Fatalf("manager snapshot after fresh stream = %+v, want retired first and active second adapters", afterFresh)
	}

	close(releaseFirst)
	assertMuxPayload(t, firstStream, "first:active")
	if _, err := firstStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("first terminal receive = %v, want EOF", err)
	}
	assertEventually(t, func() bool {
		snapshot := manager.Snapshot()
		for _, endpoint := range snapshot.Endpoints {
			if endpoint.Endpoint == firstEndpoint {
				return endpoint.Retired && endpoint.Adapter.Transport.ActiveStreams == 0
			}
		}
		return false
	}, "retired mux adapter drained active stream")
	time.Sleep(time.Millisecond)
	if err := manager.CloseIdle(context.Background()); err != nil {
		t.Fatalf("CloseIdle: %v", err)
	}
	closed := manager.Snapshot()
	if closed.RetiredAdapters != 0 ||
		closed.ClosedAdapters != 2 ||
		closed.CloseReasons["idle"] != 2 ||
		closed.DrainReasons["resolver_update"] != 1 {
		t.Fatalf("manager snapshot after retired idle close = %+v, want retired adapter closed after active drain", closed)
	}

	cancel()
	if err := <-firstDone; err != nil {
		t.Fatalf("first mux listener stopped with error: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second mux listener stopped with error: %v", err)
	}
}

func TestExperimentalMuxConnectionManagerWatchRemovesEndpoints(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	startMuxEchoListener := func(name string, listener net.Listener) <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- ServeExperimentalMuxListener(ctx, listener, func(adapter *ExperimentalMuxServerAdapter) error {
				return adapter.RegisterStream("orders/Watch", func(ctx context.Context, stream *ExperimentalMuxStream) error {
					msg, err := stream.Receive(ctx)
					if err != nil {
						return err
					}
					if err := stream.Send(ctx, Message{Payload: append([]byte(name+":"), msg.Payload...)}); err != nil {
						return err
					}
					return stream.Close(ctx, "ok")
				})
			})
		}()
		return done
	}
	firstDone := startMuxEchoListener("first", firstListener)
	secondDone := startMuxEchoListener("second", secondListener)

	firstEndpoint := "tcp://" + firstListener.Addr().String()
	secondEndpoint := "tcp://" + secondListener.Addr().String()
	updates := make(chan []string, 2)
	resolver := &mutableWatchResolver{
		mutableResolver: mutableResolver{endpoints: []string{firstEndpoint, secondEndpoint}},
		updates:         updates,
	}
	manager, err := NewExperimentalMuxConnectionManager(resolver)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	client, err := NewClient("http://unused", WithExperimentalMuxConnectionManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, want := range []string{"first:a", "second:b"} {
		stream, err := client.MuxStream(context.Background(), "orders/Watch")
		if err != nil {
			t.Fatalf("MuxStream %s: %v", want, err)
		}
		if err := stream.Send(context.Background(), Message{Payload: []byte(want[len(want)-1:])}); err != nil {
			t.Fatalf("Send %s: %v", want, err)
		}
		assertMuxPayload(t, stream, want)
		if _, err := stream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal receive %s = %v, want EOF", want, err)
		}
	}
	if snapshot := manager.Snapshot(); len(snapshot.Endpoints) != 2 {
		t.Fatalf("manager snapshot before watch update = %+v, want two endpoints", snapshot)
	}
	updates <- []string{secondEndpoint}
	assertEventually(t, func() bool {
		snapshot := manager.Snapshot()
		return len(snapshot.Endpoints) == 1 &&
			snapshot.Endpoints[0].Endpoint == secondEndpoint &&
			snapshot.WatchUpdates == 1 &&
			snapshot.ClosedAdapters == 1 &&
			snapshot.CloseReasons["resolver_update"] == 1 &&
			snapshot.DrainReasons["resolver_update"] == 1 &&
			len(snapshot.Removed) == 1 &&
			snapshot.Removed[0] == firstEndpoint &&
			!snapshot.LastUpdated.IsZero()
	}, "mux manager watch removed stale endpoint")
	diagnosis := client.RuntimeSnapshot().Diagnosis.Mux.Manager
	if diagnosis.WatchUpdates != 1 ||
		diagnosis.ClosedAdapters != 1 ||
		diagnosis.CloseReasons["resolver_update"] != 1 ||
		diagnosis.DrainReasons["resolver_update"] != 1 ||
		len(diagnosis.Removed) != 1 ||
		diagnosis.Removed[0] != firstEndpoint ||
		diagnosis.LastUpdated.IsZero() {
		t.Fatalf("mux manager diagnosis after watch = %+v, want watch removal evidence", diagnosis)
	}

	cancel()
	if err := <-firstDone; err != nil {
		t.Fatalf("first mux listener stopped with error: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second mux listener stopped with error: %v", err)
	}
}
