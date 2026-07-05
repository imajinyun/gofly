package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
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
