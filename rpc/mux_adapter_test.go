package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

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
