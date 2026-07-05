package rpc

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestExperimentalMuxTransportMultiplexesStreamsOverOneConn(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn)
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	first, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream first: %v", err)
	}
	second, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream second: %v", err)
	}
	if first.ID() == second.ID() || first.ID()%2 != 1 || second.ID()%2 != 1 {
		t.Fatalf("client stream IDs = %d/%d, want distinct odd IDs", first.ID(), second.ID())
	}

	serverFirst, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream first: %v", err)
	}
	serverSecond, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream second: %v", err)
	}
	if serverFirst.ID() != first.ID() || serverSecond.ID() != second.ID() {
		t.Fatalf("accepted stream IDs = %d/%d, want %d/%d", serverFirst.ID(), serverSecond.ID(), first.ID(), second.ID())
	}

	if err := first.Send(ctx, Message{Service: "orders", Method: "Watch", Payload: []byte("first-a")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if err := second.Send(ctx, Message{Service: "orders", Method: "Watch", Payload: []byte("second-a")}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if err := first.Send(ctx, Message{Service: "orders", Method: "Watch", Payload: []byte("first-b")}); err != nil {
		t.Fatalf("first second Send: %v", err)
	}

	assertMuxPayload(t, serverFirst, "first-a")
	assertMuxPayload(t, serverSecond, "second-a")
	assertMuxPayload(t, serverFirst, "first-b")

	if err := serverSecond.Send(ctx, Message{Payload: []byte("reply-second")}); err != nil {
		t.Fatalf("server second Send: %v", err)
	}
	if err := serverFirst.Send(ctx, Message{Payload: []byte("reply-first")}); err != nil {
		t.Fatalf("server first Send: %v", err)
	}
	assertMuxPayload(t, second, "reply-second")
	assertMuxPayload(t, first, "reply-first")

	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.OpenedStreams != 2 || clientSnapshot.OpenFramesOut != 2 || clientSnapshot.DataFramesOut != 3 || clientSnapshot.DataFramesIn != 2 {
		t.Fatalf("client snapshot = %+v, want two opens, three outbound data frames and two inbound replies", clientSnapshot)
	}
	if serverSnapshot.AcceptedStreams != 2 || serverSnapshot.OpenFramesIn != 2 || serverSnapshot.DataFramesIn != 3 || serverSnapshot.DataFramesOut != 2 {
		t.Fatalf("server snapshot = %+v, want two accepted streams, three inbound data frames and two outbound replies", serverSnapshot)
	}
}

func TestExperimentalMuxTransportCloseAndCancelSemantics(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn)
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	closedStream, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream close: %v", err)
	}
	serverClosedStream, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream close: %v", err)
	}
	if err := serverClosedStream.Close(ctx, "done"); err != nil {
		t.Fatalf("server stream Close: %v", err)
	}
	if _, err := closedStream.Receive(muxTestTimeoutContext(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("closed stream Receive error = %v, want EOF", err)
	}
	if err := closedStream.Send(ctx, Message{Payload: []byte("after close")}); !errors.Is(err, ErrExperimentalMuxStreamClosed) {
		t.Fatalf("Send after remote close error = %v, want ErrExperimentalMuxStreamClosed", err)
	}

	canceledStream, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream cancel: %v", err)
	}
	serverCanceledStream, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream cancel: %v", err)
	}
	if err := serverCanceledStream.Cancel(ctx, "client should stop"); err != nil {
		t.Fatalf("server stream Cancel: %v", err)
	}
	if _, err := canceledStream.Receive(muxTestTimeoutContext(t)); CodeOf(err) != CodeCanceled || !strings.Contains(err.Error(), "client should stop") {
		t.Fatalf("canceled stream Receive error = %v, want CodeCanceled with reason", err)
	}

	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.CloseFramesIn != 1 || clientSnapshot.CancelFramesIn != 1 || clientSnapshot.LastCloseCode != CodeCanceled {
		t.Fatalf("client snapshot = %+v, want one close, one cancel and last cancel code", clientSnapshot)
	}
	if serverSnapshot.CloseFramesOut != 1 || serverSnapshot.CancelFramesOut != 1 || serverSnapshot.ClosedStreams != 1 || serverSnapshot.CanceledStreams != 1 {
		t.Fatalf("server snapshot = %+v, want one close and one cancel emitted", serverSnapshot)
	}
}

func TestExperimentalMuxTransportHalfCloseKeepsReceiveOpen(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn)
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	clientStream, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	serverStream, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	if err := clientStream.Send(ctx, Message{Payload: []byte("request")}); err != nil {
		t.Fatalf("client Send: %v", err)
	}
	assertMuxPayload(t, serverStream, "request")
	if err := clientStream.CloseSend(ctx, "request_done"); err != nil {
		t.Fatalf("client CloseSend: %v", err)
	}
	if err := clientStream.Send(ctx, Message{Payload: []byte("after fin")}); !errors.Is(err, ErrExperimentalMuxStreamClosed) {
		t.Fatalf("client Send after CloseSend error = %v, want ErrExperimentalMuxStreamClosed", err)
	}
	if _, err := serverStream.Receive(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("server Receive after client half-close error = %v, want EOF", err)
	}
	if err := serverStream.Send(ctx, Message{Payload: []byte("response after fin")}); err != nil {
		t.Fatalf("server Send after client half-close: %v", err)
	}
	assertMuxPayload(t, clientStream, "response after fin")
	if err := serverStream.CloseSend(ctx, "response_done"); err != nil {
		t.Fatalf("server CloseSend: %v", err)
	}
	if _, err := clientStream.Receive(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("client Receive after server half-close error = %v, want EOF", err)
	}

	assertEventually(t, func() bool {
		return client.Snapshot().ActiveStreams == 0 && server.Snapshot().ActiveStreams == 0
	}, "half-closed streams cleaned up")
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.FinFramesOut != 1 || clientSnapshot.FinFramesIn != 1 || serverSnapshot.FinFramesOut != 1 || serverSnapshot.FinFramesIn != 1 {
		t.Fatalf("fin snapshots client=%+v server=%+v, want one inbound and outbound FIN each", clientSnapshot, serverSnapshot)
	}
	if clientSnapshot.HalfClosedStreams != 2 || serverSnapshot.HalfClosedStreams != 2 {
		t.Fatalf("half-close snapshots client=%+v server=%+v, want two observed half-closes on each side", clientSnapshot, serverSnapshot)
	}
}

func TestExperimentalMuxTransportPerStreamBackpressure(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn)
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole(), WithExperimentalMuxReceiveQueueSize(1))
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	stream, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	serverStream, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	if serverStream.ID() != stream.ID() {
		t.Fatalf("accepted stream ID = %d, want %d", serverStream.ID(), stream.ID())
	}

	sendDone := make(chan error, 1)
	go func() {
		if err := stream.Send(ctx, Message{Payload: []byte("queued")}); err != nil {
			sendDone <- err
			return
		}
		sendDone <- stream.Send(ctx, Message{Payload: []byte("blocked-until-receive")})
	}()

	assertEventually(t, func() bool {
		return server.Snapshot().BackpressureEvents > 0
	}, "backpressure event")
	assertMuxPayload(t, serverStream, "queued")
	assertMuxPayload(t, serverStream, "blocked-until-receive")
	if err := <-sendDone; err != nil {
		t.Fatalf("send goroutine error: %v", err)
	}
}

func TestExperimentalMuxTransportFlowControlWaitsForWindowUpdate(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn, WithExperimentalMuxReceiveQueueSize(1))
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole(), WithExperimentalMuxReceiveQueueSize(1))
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	stream, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	serverStream, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream: %v", err)
	}
	if err := stream.Send(ctx, Message{Payload: []byte("first")}); err != nil {
		t.Fatalf("first Send: %v", err)
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- stream.Send(ctx, Message{Payload: []byte("second")})
	}()

	select {
	case err := <-secondDone:
		t.Fatalf("second Send completed before window update: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	assertEventually(t, func() bool {
		return client.Snapshot().CreditWaits > 0
	}, "credit wait")

	assertMuxPayload(t, serverStream, "first")
	if err := <-secondDone; err != nil {
		t.Fatalf("second Send after window update: %v", err)
	}
	assertMuxPayload(t, serverStream, "second")

	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.WindowFramesIn == 0 || clientSnapshot.CreditWaits == 0 {
		t.Fatalf("client snapshot = %+v, want inbound window update and credit wait", clientSnapshot)
	}
	if serverSnapshot.WindowFramesOut == 0 {
		t.Fatalf("server snapshot = %+v, want outbound window update", serverSnapshot)
	}
}

func TestExperimentalMuxTransportKeepalivePingPongSnapshot(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn, WithExperimentalMuxKeepalive(5*time.Millisecond, time.Second))
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	assertEventually(t, func() bool {
		clientSnapshot := client.Snapshot()
		serverSnapshot := server.Snapshot()
		return clientSnapshot.PingFramesOut > 0 &&
			clientSnapshot.PongFramesIn > 0 &&
			serverSnapshot.PingFramesIn > 0 &&
			serverSnapshot.PongFramesOut > 0
	}, "keepalive ping/pong exchange")

	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.KeepaliveInterval != 5*time.Millisecond ||
		clientSnapshot.KeepaliveIdle != time.Second ||
		clientSnapshot.Liveness != "alive" ||
		clientSnapshot.LastPingAt.IsZero() ||
		clientSnapshot.LastPongAt.IsZero() ||
		clientSnapshot.LastFrameReadAt.IsZero() ||
		clientSnapshot.LastFrameWrittenAt.IsZero() {
		t.Fatalf("client snapshot = %+v, want configured alive keepalive liveness", clientSnapshot)
	}
	if serverSnapshot.PingFramesIn != clientSnapshot.PingFramesOut ||
		serverSnapshot.PongFramesOut != clientSnapshot.PongFramesIn ||
		serverSnapshot.LastPingAt.IsZero() ||
		serverSnapshot.LastPongAt.IsZero() {
		t.Fatalf("server snapshot = %+v client snapshot = %+v, want matched keepalive frames", serverSnapshot, clientSnapshot)
	}
}

func TestExperimentalMuxTransportKeepaliveIdleTimeoutClosesTransport(t *testing.T) {
	clientConn, peerConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn, WithExperimentalMuxKeepalive(5*time.Millisecond, 25*time.Millisecond))
	defer client.Close()

	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		drainExperimentalMuxFrames(peerConn)
	}()
	t.Cleanup(func() {
		_ = peerConn.Close()
		<-peerDone
	})

	assertEventually(t, func() bool {
		snapshot := client.Snapshot()
		return snapshot.Closed &&
			snapshot.IdleTimeouts == 1 &&
			snapshot.PingFramesOut > 0 &&
			snapshot.PongFramesIn == 0 &&
			snapshot.LastCloseCode == CodeDeadlineExceeded &&
			snapshot.LastCloseReason == "keepalive_idle"
	}, "keepalive idle timeout close")

	snapshot := client.Snapshot()
	if snapshot.Liveness != "closed" || snapshot.LastPingAt.IsZero() || !snapshot.LastPongAt.IsZero() {
		t.Fatalf("client snapshot = %+v, want closed liveness with outbound ping and no pong", snapshot)
	}
}

func TestExperimentalMuxTransportMaxConcurrentStreamsRejectsLocalOpen(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn, WithExperimentalMuxMaxConcurrentStreams(1))
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	first, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream first: %v", err)
	}
	if _, err := server.AcceptStream(ctx); err != nil {
		t.Fatalf("AcceptStream first: %v", err)
	}
	second, err := client.OpenStream(ctx)
	if err == nil || second != nil || CodeOf(err) != CodeUnavailable {
		t.Fatalf("OpenStream second = stream %#v err %v, want CodeUnavailable", second, err)
	}
	if first.ID() == 0 {
		t.Fatal("first stream ID is zero")
	}

	snapshot := client.Snapshot()
	if snapshot.MaxStreams != 1 ||
		snapshot.ActiveStreams != 1 ||
		snapshot.LocalRejects != 1 ||
		snapshot.OpenFramesOut != 1 ||
		snapshot.LastCloseCode != CodeUnavailable ||
		snapshot.LastCloseReason != "max_concurrent_streams" {
		t.Fatalf("client snapshot = %+v, want one active stream and local max-stream reject", snapshot)
	}
}

func TestExperimentalMuxTransportMaxConcurrentStreamsRejectsRemoteOpen(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn)
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole(), WithExperimentalMuxMaxConcurrentStreams(1))
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	first, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream first: %v", err)
	}
	serverFirst, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream first: %v", err)
	}
	if serverFirst.ID() != first.ID() {
		t.Fatalf("accepted stream ID = %d, want %d", serverFirst.ID(), first.ID())
	}
	second, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream second should be locally admitted before remote rejection: %v", err)
	}
	if _, err := second.Receive(muxTestTimeoutContext(t)); CodeOf(err) != CodeUnavailable || !strings.Contains(err.Error(), "max_concurrent_streams") {
		t.Fatalf("second Receive error = %v, want remote CodeUnavailable max_concurrent_streams", err)
	}

	assertEventually(t, func() bool {
		return server.Snapshot().RemoteRejects == 1 && client.Snapshot().CancelFramesIn == 1
	}, "remote max-stream rejection snapshot")
	serverSnapshot := server.Snapshot()
	clientSnapshot := client.Snapshot()
	if serverSnapshot.MaxStreams != 1 ||
		serverSnapshot.ActiveStreams != 1 ||
		serverSnapshot.RemoteRejects != 1 ||
		serverSnapshot.CancelFramesOut != 1 ||
		serverSnapshot.LastCloseCode != CodeUnavailable ||
		serverSnapshot.LastCloseReason != "max_concurrent_streams" {
		t.Fatalf("server snapshot = %+v, want one active stream and remote max-stream reject", serverSnapshot)
	}
	if clientSnapshot.CanceledStreams != 1 ||
		clientSnapshot.CancelFramesIn != 1 ||
		clientSnapshot.LastCloseCode != CodeUnavailable ||
		clientSnapshot.LastCloseReason != "max_concurrent_streams" {
		t.Fatalf("client snapshot = %+v, want inbound cancel for remote max-stream reject", clientSnapshot)
	}
}

func TestExperimentalMuxTransportDrainRejectsNewLocalStreamsAndLetsActiveFinish(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn)
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	stream, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream active: %v", err)
	}
	serverStream, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream active: %v", err)
	}
	if err := client.Drain(ctx, "rolling_restart"); err != nil {
		t.Fatalf("client Drain: %v", err)
	}
	if next, err := client.OpenStream(ctx); err == nil || next != nil || CodeOf(err) != CodeUnavailable || !strings.Contains(err.Error(), "draining") {
		t.Fatalf("OpenStream during local drain = stream %#v err %v, want CodeUnavailable draining", next, err)
	}

	if err := stream.Send(ctx, Message{Payload: []byte("active request")}); err != nil {
		t.Fatalf("active Send after drain: %v", err)
	}
	assertMuxPayload(t, serverStream, "active request")
	if err := serverStream.Close(ctx, "ok"); err != nil {
		t.Fatalf("server active Close: %v", err)
	}
	if _, err := stream.Receive(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("active Receive after server close error = %v, want EOF", err)
	}

	assertEventually(t, func() bool {
		return server.Snapshot().RemoteDraining && client.Snapshot().ActiveStreams == 0
	}, "remote drain snapshot and active stream cleanup")
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if !clientSnapshot.Draining ||
		clientSnapshot.DrainReason != "rolling_restart" ||
		clientSnapshot.GoAwayFramesOut != 1 ||
		clientSnapshot.LocalRejects != 1 ||
		clientSnapshot.DrainRejects != 1 ||
		clientSnapshot.Liveness != "draining" {
		t.Fatalf("client snapshot = %+v, want draining local GOAWAY reject state", clientSnapshot)
	}
	if !serverSnapshot.RemoteDraining ||
		serverSnapshot.RemoteDrainReason != "rolling_restart" ||
		serverSnapshot.GoAwayFramesIn != 1 {
		t.Fatalf("server snapshot = %+v, want remote draining state", serverSnapshot)
	}
}

func TestExperimentalMuxTransportRemoteGoAwayRejectsNewLocalStreams(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn)
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	stream, err := client.OpenStream(ctx)
	if err != nil {
		t.Fatalf("OpenStream active: %v", err)
	}
	serverStream, err := server.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("AcceptStream active: %v", err)
	}
	if err := server.Drain(ctx, "deploying"); err != nil {
		t.Fatalf("server Drain: %v", err)
	}
	assertEventually(t, func() bool {
		return client.Snapshot().RemoteDraining
	}, "client observed remote GOAWAY")
	if next, err := client.OpenStream(ctx); err == nil || next != nil || CodeOf(err) != CodeUnavailable || !strings.Contains(err.Error(), "peer_draining") {
		t.Fatalf("OpenStream after remote GOAWAY = stream %#v err %v, want CodeUnavailable peer_draining", next, err)
	}

	if err := stream.Send(ctx, Message{Payload: []byte("final request")}); err != nil {
		t.Fatalf("active Send after remote GOAWAY: %v", err)
	}
	assertMuxPayload(t, serverStream, "final request")
	if err := serverStream.Send(ctx, Message{Payload: []byte("final response")}); err != nil {
		t.Fatalf("server active Send after drain: %v", err)
	}
	assertMuxPayload(t, stream, "final response")
	if err := stream.Close(ctx, "done"); err != nil {
		t.Fatalf("active stream Close: %v", err)
	}

	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if !clientSnapshot.RemoteDraining ||
		clientSnapshot.RemoteDrainReason != "deploying" ||
		clientSnapshot.GoAwayFramesIn != 1 ||
		clientSnapshot.LocalRejects != 1 ||
		clientSnapshot.DrainRejects != 1 {
		t.Fatalf("client snapshot = %+v, want remote GOAWAY local reject state", clientSnapshot)
	}
	if !serverSnapshot.Draining ||
		serverSnapshot.DrainReason != "deploying" ||
		serverSnapshot.GoAwayFramesOut != 1 {
		t.Fatalf("server snapshot = %+v, want local draining state", serverSnapshot)
	}
}

func TestExperimentalMuxTransportContextAndFrameBoundaries(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewExperimentalMuxTransport(clientConn, WithExperimentalMuxMaxFrameBytes(8))
	server := NewExperimentalMuxTransport(serverConn, WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.OpenStream(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenStream canceled error = %v, want context.Canceled", err)
	}
	if _, err := client.AcceptStream(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcceptStream canceled error = %v, want context.Canceled", err)
	}

	stream, err := client.OpenStream(context.Background())
	if !errors.Is(err, ErrFrameTooLarge) || stream != nil {
		t.Fatalf("OpenStream oversized frame = stream %#v err %v, want ErrFrameTooLarge", stream, err)
	}
}

func TestExperimentalMuxFrameCodecRejectsMalformedFrames(t *testing.T) {
	if _, err := encodeExperimentalMuxFrame(experimentalMuxFrame{}); err == nil {
		t.Fatal("encode zero stream ID succeeded, want error")
	}
	encoded, err := encodeExperimentalMuxFrame(experimentalMuxFrame{typ: experimentalMuxFrameData, streamID: 7, code: CodeOK, reason: "ok", payload: []byte("body")})
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	decoded, err := decodeExperimentalMuxFrame(encoded)
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if decoded.typ != experimentalMuxFrameData || decoded.streamID != 7 || decoded.code != CodeOK || decoded.reason != "ok" || string(decoded.payload) != "body" {
		t.Fatalf("decoded frame = %+v, want data stream 7", decoded)
	}
	if _, err := decodeExperimentalMuxFrame([]byte{2}); err == nil {
		t.Fatal("decode unsupported/truncated frame succeeded, want error")
	}
	mutated := append([]byte(nil), encoded...)
	mutated[0] = 99
	if _, err := decodeExperimentalMuxFrame(mutated); err == nil {
		t.Fatal("decode unsupported version succeeded, want error")
	}
	if _, err := decodeExperimentalMuxFrame(append(encoded, 0)); err == nil {
		t.Fatal("decode trailing bytes succeeded, want error")
	}
	if encoded, err := encodeExperimentalMuxFrame(experimentalMuxFrame{typ: experimentalMuxFramePing}); err != nil {
		t.Fatalf("encode ping control frame: %v", err)
	} else if decoded, err := decodeExperimentalMuxFrame(encoded); err != nil || decoded.streamID != 0 || decoded.typ != experimentalMuxFramePing {
		t.Fatalf("decode ping control frame = %+v err %v, want stream id zero ping", decoded, err)
	}
	if _, err := encodeExperimentalMuxFrame(experimentalMuxFrame{typ: experimentalMuxFramePing, streamID: 7}); err == nil {
		t.Fatal("encode ping control frame with stream ID succeeded, want error")
	}
}

func assertMuxPayload(t *testing.T, stream *ExperimentalMuxStream, want string) {
	t.Helper()
	msg, err := stream.Receive(muxTestTimeoutContext(t))
	if err != nil {
		t.Fatalf("Receive stream %d: %v", stream.ID(), err)
	}
	if string(msg.Payload) != want {
		t.Fatalf("Receive stream %d payload = %q, want %q", stream.ID(), msg.Payload, want)
	}
}

func muxTestTimeoutContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func assertEventually(t *testing.T, fn func() bool, name string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s did not become true before deadline", name)
}

func drainExperimentalMuxFrames(conn net.Conn) {
	defer conn.Close()
	var header [4]byte
	for {
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(header[:])
		if length == 0 {
			return
		}
		if _, err := io.CopyN(io.Discard, conn, int64(length)); err != nil {
			return
		}
	}
}
