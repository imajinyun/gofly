package bench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	flyrpc "github.com/imajinyun/gofly/rpc"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

type rpcBenchRequest struct {
	Name string `json:"name"`
}

type rpcBenchResponse struct {
	Message string `json:"message"`
}

func BenchmarkRPCUnary(b *testing.B) {
	b.Run("gofly_rpc", benchmarkGoflyRPCUnary)
	b.Run("grpc_go", benchmarkGRPCGoUnary)
	// Kitex is intentionally optional for this suite because gofly does not
	// carry generated Kitex fixtures yet. Downstream projects that already
	// depend on Kitex can add transport-specific benchmark rows beside this
	// package without forcing Kitex into the root module.
}

func BenchmarkRPCStreamGovernance(b *testing.B) {
	b.Run("server_stream", benchmarkGoflyRPCServerStreamGovernance)
	b.Run("client_stream", benchmarkGoflyRPCClientStreamGovernance)
	b.Run("bidi_stream", benchmarkGoflyRPCBidiStreamGovernance)
}

func BenchmarkRPCServerStreamGovernance(b *testing.B) {
	benchmarkGoflyRPCServerStreamGovernance(b)
}

func BenchmarkRPCClientStreamGovernance(b *testing.B) {
	benchmarkGoflyRPCClientStreamGovernance(b)
}

func BenchmarkRPCBidiStreamGovernance(b *testing.B) {
	benchmarkGoflyRPCBidiStreamGovernance(b)
}

func BenchmarkRPCStreamTransportOpenClose(b *testing.B) {
	server := flyrpc.NewServer()
	if err := server.RegisterService(flyrpc.ServiceDesc{Name: "streamer", Streams: []flyrpc.StreamDesc{{
		Name:       "OpenClose",
		NewMessage: func() any { return new(rpcBenchRequest) },
		Mode:       flyrpc.StreamModeServerStream,
		Handler: func(ctx context.Context, stream *flyrpc.Stream) error {
			return nil
		},
	}}}, nil); err != nil {
		b.Fatal(err)
	}
	upstream := httptest.NewServer(server)
	defer upstream.Close()

	client, err := flyrpc.NewClient(upstream.URL)
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close()

	b.ReportAllocs()
	var opened int64
	for b.Loop() {
		stream, err := client.Stream(context.Background(), "streamer/OpenClose")
		if err != nil {
			b.Fatal(err)
		}
		opened++
		if err := stream.Close(); err != nil {
			b.Fatal(err)
		}
	}
	snapshot := client.RuntimeSnapshot().Transport.Stream
	transport := client.RuntimeSnapshot().Transport
	if transport.StreamConnPolicy.Mode != "dedicated" ||
		transport.StreamConnPolicy.MaxStreamsPerConn != 1 ||
		transport.StreamConnPolicy.Reuse ||
		transport.StreamConnPolicy.Multiplexed {
		b.Fatalf("stream connection policy = %+v, want dedicated one-stream-one-conn", transport.StreamConnPolicy)
	}
	if snapshot.Active != 0 || snapshot.Dials != opened || snapshot.DedicatedConns != opened || snapshot.Closes != opened {
		b.Fatalf("stream transport snapshot = %+v, want dials/dedicated/closes=%d and no active streams", snapshot, opened)
	}
	if snapshot.LastTarget != upstream.URL ||
		snapshot.LastCloseCode != flyrpc.CodeOK ||
		snapshot.LastCloseReason != "ok" ||
		snapshot.CloseCodes[flyrpc.CodeOK] != opened ||
		snapshot.CloseReasons["ok"] != opened ||
		snapshot.LastDialedAt.IsZero() ||
		snapshot.LastClosedAt.IsZero() {
		b.Fatalf("stream transport snapshot = %+v, want ok lifecycle target, counters and timestamps", snapshot)
	}
}

func BenchmarkRPCExperimentalMuxTransportTwoStreams(b *testing.B) {
	clientConn, serverConn := net.Pipe()
	client := flyrpc.NewExperimentalMuxTransport(clientConn)
	server := flyrpc.NewExperimentalMuxTransport(serverConn, flyrpc.WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		first, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		second, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverFirst, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverSecond, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := first.Send(ctx, flyrpc.Message{Payload: []byte("first")}); err != nil {
			b.Fatal(err)
		}
		if err := second.Send(ctx, flyrpc.Message{Payload: []byte("second")}); err != nil {
			b.Fatal(err)
		}
		if _, err := serverFirst.Receive(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := serverSecond.Receive(ctx); err != nil {
			b.Fatal(err)
		}
		if err := serverFirst.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if err := serverSecond.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if _, err := first.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
		if _, err := second.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
	}
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.OpenedStreams == 0 ||
		clientSnapshot.OpenedStreams != serverSnapshot.AcceptedStreams ||
		clientSnapshot.CloseFramesIn != serverSnapshot.CloseFramesOut ||
		clientSnapshot.WindowFramesIn != serverSnapshot.WindowFramesOut ||
		serverSnapshot.WindowFramesIn != clientSnapshot.WindowFramesOut ||
		clientSnapshot.ActiveStreams != 0 ||
		serverSnapshot.ActiveStreams != 0 {
		b.Fatalf("mux snapshots client=%+v server=%+v, want matched opened/accepted/closed/window frames and no active streams", clientSnapshot, serverSnapshot)
	}
}

func BenchmarkRPCExperimentalMuxAdapterOpenSendReceiveClose(b *testing.B) {
	clientConn, serverConn := net.Pipe()
	client := flyrpc.NewExperimentalMuxClientAdapter(clientConn)
	server := flyrpc.NewExperimentalMuxServerAdapter(serverConn)
	defer client.Close()
	defer server.Close()

	if err := server.RegisterStream("bench/Echo", func(ctx context.Context, stream *flyrpc.ExperimentalMuxStream) error {
		msg, err := stream.Receive(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(ctx, flyrpc.Message{Payload: append([]byte("ack:"), msg.Payload...)}); err != nil {
			return err
		}
		return stream.Close(ctx, "ok")
	}); err != nil {
		b.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(serveCtx)
	}()
	defer func() {
		cancel()
		if err := <-serveDone; err != nil {
			b.Fatalf("mux adapter serve stopped with error: %v", err)
		}
	}()

	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		stream, err := client.OpenStream(ctx, "bench/Echo")
		if err != nil {
			b.Fatal(err)
		}
		if err := stream.Send(ctx, flyrpc.Message{Payload: []byte("payload")}); err != nil {
			b.Fatal(err)
		}
		msg, err := stream.Receive(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if string(msg.Payload) != "ack:payload" {
			b.Fatalf("mux adapter payload = %q, want ack:payload", msg.Payload)
		}
		if _, err := stream.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatalf("mux adapter terminal receive = %v, want EOF", err)
		}
	}
	waitMuxAdapterBenchIdle(b, client, server)
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.Transport.OpenedStreams == 0 ||
		serverSnapshot.AcceptedStreams == 0 ||
		clientSnapshot.Transport.OpenedStreams != serverSnapshot.AcceptedStreams ||
		serverSnapshot.HandlerErrors != 0 ||
		serverSnapshot.RejectedStreams != 0 ||
		clientSnapshot.Transport.ActiveStreams != 0 ||
		serverSnapshot.Transport.ActiveStreams != 0 {
		b.Fatalf("mux adapter snapshots client=%+v server=%+v, want matched open/send/receive/close evidence", clientSnapshot, serverSnapshot)
	}
}

func waitMuxAdapterBenchIdle(b *testing.B, client *flyrpc.ExperimentalMuxClientAdapter, server *flyrpc.ExperimentalMuxServerAdapter) {
	b.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		clientSnapshot := client.Snapshot()
		serverSnapshot := server.Snapshot()
		if clientSnapshot.Transport.ActiveStreams == 0 && serverSnapshot.Transport.ActiveStreams == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	b.Fatalf("mux adapters did not become idle before deadline: client=%+v server=%+v", client.Snapshot(), server.Snapshot())
}

func BenchmarkRPCExperimentalMuxTransportHalfCloseLifecycle(b *testing.B) {
	clientConn, serverConn := net.Pipe()
	client := flyrpc.NewExperimentalMuxTransport(clientConn)
	server := flyrpc.NewExperimentalMuxTransport(serverConn, flyrpc.WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		clientStream, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverStream, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := clientStream.Send(ctx, flyrpc.Message{Payload: []byte("request")}); err != nil {
			b.Fatal(err)
		}
		if _, err := serverStream.Receive(ctx); err != nil {
			b.Fatal(err)
		}
		if err := clientStream.CloseSend(ctx, "request_done"); err != nil {
			b.Fatal(err)
		}
		if _, err := serverStream.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
		if err := serverStream.Send(ctx, flyrpc.Message{Payload: []byte("response")}); err != nil {
			b.Fatal(err)
		}
		if _, err := clientStream.Receive(ctx); err != nil {
			b.Fatal(err)
		}
		if err := serverStream.CloseSend(ctx, "response_done"); err != nil {
			b.Fatal(err)
		}
		if _, err := clientStream.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
	}
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.ActiveStreams != 0 ||
		serverSnapshot.ActiveStreams != 0 ||
		clientSnapshot.FinFramesIn != serverSnapshot.FinFramesOut ||
		clientSnapshot.FinFramesOut != serverSnapshot.FinFramesIn {
		b.Fatalf("mux half-close snapshots client=%+v server=%+v, want matched FIN frames and no active streams", clientSnapshot, serverSnapshot)
	}
}

func BenchmarkRPCExperimentalMuxTransportKeepaliveSnapshot(b *testing.B) {
	clientConn, serverConn := net.Pipe()
	client := flyrpc.NewExperimentalMuxTransport(clientConn, flyrpc.WithExperimentalMuxKeepalive(time.Millisecond, time.Second))
	server := flyrpc.NewExperimentalMuxTransport(serverConn, flyrpc.WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		clientSnapshot := client.Snapshot()
		serverSnapshot := server.Snapshot()
		if clientSnapshot.PingFramesOut > 0 &&
			clientSnapshot.PongFramesIn > 0 &&
			serverSnapshot.PingFramesIn > 0 &&
			serverSnapshot.PongFramesOut > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if client.Snapshot().PongFramesIn == 0 {
		b.Fatalf("keepalive snapshot = %+v, want at least one pong before benchmark", client.Snapshot())
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		stream, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverStream, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := stream.Send(ctx, flyrpc.Message{Payload: []byte("request")}); err != nil {
			b.Fatal(err)
		}
		if _, err := serverStream.Receive(ctx); err != nil {
			b.Fatal(err)
		}
		if err := serverStream.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if _, err := stream.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.KeepaliveInterval == 0 ||
		clientSnapshot.KeepaliveIdle == 0 ||
		clientSnapshot.PingFramesOut == 0 ||
		clientSnapshot.PongFramesIn == 0 ||
		serverSnapshot.PingFramesIn == 0 ||
		serverSnapshot.PongFramesOut == 0 ||
		clientSnapshot.Liveness != "alive" {
		b.Fatalf("keepalive snapshots client=%+v server=%+v, want alive ping/pong evidence", clientSnapshot, serverSnapshot)
	}
}

func BenchmarkRPCExperimentalMuxTransportMaxStreamsAdmission(b *testing.B) {
	clientConn, serverConn := net.Pipe()
	client := flyrpc.NewExperimentalMuxTransport(clientConn, flyrpc.WithExperimentalMuxMaxConcurrentStreams(2))
	server := flyrpc.NewExperimentalMuxTransport(serverConn, flyrpc.WithExperimentalMuxServerRole())
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		first, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		second, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if third, err := client.OpenStream(ctx); err == nil || third != nil || flyrpc.CodeOf(err) != flyrpc.CodeUnavailable {
			b.Fatalf("third OpenStream = stream %#v err %v, want local CodeUnavailable", third, err)
		}
		serverFirst, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverSecond, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := serverFirst.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if err := serverSecond.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if _, err := first.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
		if _, err := second.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
		waitMuxBenchIdle(b, client, server)
	}
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.MaxStreams != 2 ||
		clientSnapshot.LocalRejects == 0 ||
		clientSnapshot.OpenedStreams != serverSnapshot.AcceptedStreams ||
		clientSnapshot.ActiveStreams != 0 ||
		serverSnapshot.ActiveStreams != 0 {
		b.Fatalf("max-stream snapshots client=%+v server=%+v, want local admission rejects and no active streams", clientSnapshot, serverSnapshot)
	}
}

func BenchmarkRPCExperimentalMuxTransportConnectionWindow(b *testing.B) {
	clientConn, serverConn := net.Pipe()
	client := flyrpc.NewExperimentalMuxTransport(
		clientConn,
		flyrpc.WithExperimentalMuxReceiveQueueSize(2),
		flyrpc.WithExperimentalMuxConnectionWindow(2),
	)
	server := flyrpc.NewExperimentalMuxTransport(
		serverConn,
		flyrpc.WithExperimentalMuxServerRole(),
		flyrpc.WithExperimentalMuxReceiveQueueSize(2),
		flyrpc.WithExperimentalMuxConnectionWindow(2),
	)
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		first, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		second, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverFirst, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverSecond, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := first.Send(ctx, flyrpc.Message{Payload: []byte("first")}); err != nil {
			b.Fatal(err)
		}
		if err := second.Send(ctx, flyrpc.Message{Payload: []byte("second")}); err != nil {
			b.Fatal(err)
		}
		if _, err := serverFirst.Receive(ctx); err != nil {
			b.Fatal(err)
		}
		if _, err := serverSecond.Receive(ctx); err != nil {
			b.Fatal(err)
		}
		if err := serverFirst.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if err := serverSecond.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if _, err := first.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
		if _, err := second.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
		waitMuxBenchIdle(b, client, server)
	}
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.ConnectionWindow != 2 ||
		serverSnapshot.ConnectionWindow != 2 ||
		clientSnapshot.ConnectionWindowFramesIn != serverSnapshot.ConnectionWindowFramesOut ||
		serverSnapshot.ConnectionWindowFramesIn != clientSnapshot.ConnectionWindowFramesOut ||
		clientSnapshot.ActiveStreams != 0 ||
		serverSnapshot.ActiveStreams != 0 {
		b.Fatalf("connection window snapshots client=%+v server=%+v, want matched connection WINDOW frames and no active streams", clientSnapshot, serverSnapshot)
	}
}

func BenchmarkRPCExperimentalMuxTransportLargePayloadFragmentation(b *testing.B) {
	clientConn, serverConn := net.Pipe()
	client := flyrpc.NewExperimentalMuxTransport(
		clientConn,
		flyrpc.WithExperimentalMuxMaxFrameBytes(128),
		flyrpc.WithExperimentalMuxMaxMessageBytes(4096),
	)
	server := flyrpc.NewExperimentalMuxTransport(
		serverConn,
		flyrpc.WithExperimentalMuxServerRole(),
		flyrpc.WithExperimentalMuxMaxFrameBytes(128),
		flyrpc.WithExperimentalMuxMaxMessageBytes(4096),
	)
	defer client.Close()
	defer server.Close()

	ctx := context.Background()
	payload := []byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	b.ReportAllocs()
	for b.Loop() {
		stream, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverStream, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := stream.Send(ctx, flyrpc.Message{Payload: payload}); err != nil {
			b.Fatal(err)
		}
		msg, err := serverStream.Receive(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if string(msg.Payload) != string(payload) {
			b.Fatalf("fragmented payload = %q, want %q", msg.Payload, payload)
		}
		if err := serverStream.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if _, err := stream.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
		waitMuxBenchIdle(b, client, server)
	}
	clientSnapshot := client.Snapshot()
	serverSnapshot := server.Snapshot()
	if clientSnapshot.FragmentFramesOut == 0 ||
		serverSnapshot.FragmentFramesIn != clientSnapshot.FragmentFramesOut ||
		clientSnapshot.DataFramesOut == 0 ||
		serverSnapshot.DataFramesIn != clientSnapshot.DataFramesOut ||
		clientSnapshot.ActiveStreams != 0 ||
		serverSnapshot.ActiveStreams != 0 {
		b.Fatalf("large-payload snapshots client=%+v server=%+v, want matched fragments, data messages and no active streams", clientSnapshot, serverSnapshot)
	}
}

func BenchmarkRPCExperimentalMuxTransportDrainGoAway(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		clientConn, serverConn := net.Pipe()
		client := flyrpc.NewExperimentalMuxTransport(clientConn)
		server := flyrpc.NewExperimentalMuxTransport(serverConn, flyrpc.WithExperimentalMuxServerRole())

		stream, err := client.OpenStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		serverStream, err := server.AcceptStream(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := client.Drain(ctx, "benchmark_drain"); err != nil {
			b.Fatal(err)
		}
		if next, err := client.OpenStream(ctx); err == nil || next != nil || flyrpc.CodeOf(err) != flyrpc.CodeUnavailable {
			b.Fatalf("OpenStream after drain = stream %#v err %v, want CodeUnavailable", next, err)
		}
		if err := stream.Send(ctx, flyrpc.Message{Payload: []byte("request")}); err != nil {
			b.Fatal(err)
		}
		if _, err := serverStream.Receive(ctx); err != nil {
			b.Fatal(err)
		}
		if err := serverStream.Close(ctx, "ok"); err != nil {
			b.Fatal(err)
		}
		if _, err := stream.Receive(ctx); !errors.Is(err, io.EOF) {
			b.Fatal(err)
		}
		waitMuxBenchIdle(b, client, server)
		clientSnapshot := client.Snapshot()
		serverSnapshot := server.Snapshot()
		if !clientSnapshot.Draining ||
			clientSnapshot.GoAwayFramesOut != 1 ||
			clientSnapshot.LocalRejects != 1 ||
			clientSnapshot.DrainRejects != 1 ||
			clientSnapshot.ActiveStreams != 0 ||
			!serverSnapshot.RemoteDraining ||
			serverSnapshot.GoAwayFramesIn != 1 ||
			serverSnapshot.ActiveStreams != 0 {
			b.Fatalf("drain snapshots client=%+v server=%+v, want GOAWAY drain evidence and no active streams", clientSnapshot, serverSnapshot)
		}
		if err := client.Close(); err != nil {
			b.Fatal(err)
		}
		if err := server.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func waitMuxBenchIdle(b *testing.B, transports ...*flyrpc.ExperimentalMuxTransport) {
	b.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		idle := true
		for _, transport := range transports {
			if transport.Snapshot().ActiveStreams != 0 {
				idle = false
				break
			}
		}
		if idle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	snapshots := make([]flyrpc.ExperimentalMuxTransportSnapshot, 0, len(transports))
	for _, transport := range transports {
		snapshots = append(snapshots, transport.Snapshot())
	}
	b.Fatalf("mux transports did not become idle before deadline: %+v", snapshots)
}

func TestRPCStreamTransportEvidenceContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("rpc_stream_transport_evidence.json"))
	if err != nil {
		t.Fatalf("read stream transport evidence: %v", err)
	}
	var evidence struct {
		Schema    string `json:"schema"`
		Benchmark string `json:"benchmark"`
		Status    string `json:"status"`
		Baseline  struct {
			SampleCount       int `json:"sampleCount"`
			AllocsPerOpMedian int `json:"allocsPerOpMedian"`
		} `json:"baseline"`
		Current struct {
			SampleCount       int `json:"sampleCount"`
			AllocsPerOpMedian int `json:"allocsPerOpMedian"`
		} `json:"current"`
		Decision struct {
			Result                    string   `json:"result"`
			AllocationMode            string   `json:"allocationMode"`
			LatencyMode               string   `json:"latencyMode"`
			CandidateAllocationBudget int      `json:"candidateAllocationBudget"`
			PromotionStatus           string   `json:"promotionStatus"`
			ForbiddenClaims           []string `json:"forbiddenClaims"`
		} `json:"decision"`
	}
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("decode stream transport evidence: %v", err)
	}
	if evidence.Schema != "gofly.benchmark_rpc_stream_transport_evidence.v1" ||
		evidence.Benchmark != "BenchmarkRPCStreamTransportOpenClose" ||
		evidence.Status != "candidate-report-only" {
		t.Fatalf("stream transport evidence identity = %+v, want candidate report-only benchmark evidence", evidence)
	}
	if evidence.Baseline.SampleCount != 5 || evidence.Current.SampleCount != 3 {
		t.Fatalf("stream transport sample counts baseline=%d current=%d, want 5 and 3", evidence.Baseline.SampleCount, evidence.Current.SampleCount)
	}
	if evidence.Baseline.AllocsPerOpMedian != 117 ||
		evidence.Current.AllocsPerOpMedian != 117 ||
		evidence.Decision.CandidateAllocationBudget != 117 {
		t.Fatalf("stream transport alloc evidence = baseline %d current %d budget %d, want 117",
			evidence.Baseline.AllocsPerOpMedian,
			evidence.Current.AllocsPerOpMedian,
			evidence.Decision.CandidateAllocationBudget,
		)
	}
	if evidence.Decision.Result != "hold" ||
		evidence.Decision.AllocationMode != "candidate-report-only" ||
		evidence.Decision.LatencyMode != "report-only" ||
		evidence.Decision.PromotionStatus != "blocked" {
		t.Fatalf("stream transport promotion decision = %+v, want hold/report-only/blocked", evidence.Decision)
	}
	for _, forbidden := range []string{"Kitex transport parity", "gRPC-Go transport parity", "Tier 1 replacement claim"} {
		if !containsString(evidence.Decision.ForbiddenClaims, forbidden) {
			t.Fatalf("stream transport forbidden claims = %#v, missing %q", evidence.Decision.ForbiddenClaims, forbidden)
		}
	}
}

func TestRPCMuxAdapterEvidenceContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("rpc_mux_adapter_evidence.json"))
	if err != nil {
		t.Fatalf("read mux adapter evidence: %v", err)
	}
	var evidence struct {
		Schema    string `json:"schema"`
		Benchmark string `json:"benchmark"`
		Status    string `json:"status"`
		Baseline  struct {
			SampleCount       int `json:"sampleCount"`
			NSPerOpMedian     int `json:"nsPerOpMedian"`
			AllocsPerOpMedian int `json:"allocsPerOpMedian"`
		} `json:"baseline"`
		Current struct {
			SampleCount       int `json:"sampleCount"`
			NSPerOpMedian     int `json:"nsPerOpMedian"`
			AllocsPerOpMedian int `json:"allocsPerOpMedian"`
		} `json:"current"`
		Decision struct {
			Result          string   `json:"result"`
			AllocationMode  string   `json:"allocationMode"`
			LatencyMode     string   `json:"latencyMode"`
			PromotionStatus string   `json:"promotionStatus"`
			ForbiddenClaims []string `json:"forbiddenClaims"`
		} `json:"decision"`
	}
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("decode mux adapter evidence: %v", err)
	}
	if evidence.Schema != "gofly.benchmark_rpc_mux_adapter_evidence.v1" ||
		evidence.Benchmark != "BenchmarkRPCExperimentalMuxAdapterOpenSendReceiveClose" ||
		evidence.Status != "report-only" {
		t.Fatalf("mux adapter evidence identity = %+v, want report-only adapter benchmark evidence", evidence)
	}
	if evidence.Baseline.SampleCount != 5 || evidence.Current.SampleCount != 3 ||
		evidence.Baseline.NSPerOpMedian <= 0 ||
		evidence.Current.NSPerOpMedian <= 0 ||
		evidence.Baseline.AllocsPerOpMedian <= 0 ||
		evidence.Current.AllocsPerOpMedian <= 0 {
		t.Fatalf("mux adapter sample evidence baseline=%+v current=%+v, want committed report-only samples", evidence.Baseline, evidence.Current)
	}
	if evidence.Decision.Result != "hold" ||
		evidence.Decision.AllocationMode != "report-only" ||
		evidence.Decision.LatencyMode != "report-only" ||
		evidence.Decision.PromotionStatus != "blocked" {
		t.Fatalf("mux adapter promotion decision = %+v, want hold/report-only/blocked", evidence.Decision)
	}
	for _, forbidden := range []string{"blocking RPC mux adapter latency", "blocking RPC mux adapter allocation", "Tier 1 replacement claim"} {
		if !containsString(evidence.Decision.ForbiddenClaims, forbidden) {
			t.Fatalf("mux adapter forbidden claims = %#v, missing %q", evidence.Decision.ForbiddenClaims, forbidden)
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func benchmarkGoflyRPCServerStreamGovernance(b *testing.B) {
	benchmarkGoflyRPCStreamGovernance(b, flyrpc.StreamModeServerStream)
}

func benchmarkGoflyRPCClientStreamGovernance(b *testing.B) {
	benchmarkGoflyRPCStreamGovernance(b, flyrpc.StreamModeClientStream)
}

func benchmarkGoflyRPCBidiStreamGovernance(b *testing.B) {
	benchmarkGoflyRPCStreamGovernance(b, flyrpc.StreamModeBidiStream)
}

func benchmarkGoflyRPCStreamGovernance(b *testing.B, mode flyrpc.StreamMode) {
	// stream governance overhead is tracked separately from unary RPC because
	// stream setup, receive, close and mode-specific message flow exercise
	// different policy hooks.
	server := flyrpc.NewServer()
	if err := server.RegisterService(flyrpc.ServiceDesc{Name: "streamer", Streams: []flyrpc.StreamDesc{{
		Name:       "Watch",
		NewMessage: func() any { return new(rpcBenchRequest) },
		Mode:       mode,
		Handler: func(ctx context.Context, stream *flyrpc.Stream) error {
			return handleRPCBenchStream(mode, stream)
		},
	}}}, nil); err != nil {
		b.Fatal(err)
	}
	upstream := httptest.NewServer(server)
	defer upstream.Close()

	client, err := flyrpc.NewClient(upstream.URL)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		stream, err := client.Stream(context.Background(), "streamer/Watch")
		if err != nil {
			b.Fatal(err)
		}
		if err := exerciseRPCBenchStream(mode, stream); err != nil {
			b.Fatal(err)
		}
		_ = stream.Close()
	}
}

func handleRPCBenchStream(mode flyrpc.StreamMode, stream *flyrpc.Stream) error {
	switch mode {
	case flyrpc.StreamModeServerStream:
		var req rpcBenchRequest
		if err := stream.Recv(&req); err != nil {
			return err
		}
		if err := stream.Send(rpcBenchResponse{Message: "hello " + req.Name + ":first"}); err != nil {
			return err
		}
		return stream.Send(rpcBenchResponse{Message: "hello " + req.Name + ":second"})
	case flyrpc.StreamModeClientStream:
		names := make([]string, 0, 2)
		for len(names) < 2 {
			var req rpcBenchRequest
			if err := stream.Recv(&req); err != nil {
				return err
			}
			names = append(names, req.Name)
		}
		return stream.Send(rpcBenchResponse{Message: "hello " + names[0] + "," + names[1]})
	default:
		var req rpcBenchRequest
		if err := stream.Recv(&req); err != nil {
			return err
		}
		return stream.Send(rpcBenchResponse{Message: "hello " + req.Name})
	}
}

func exerciseRPCBenchStream(mode flyrpc.StreamMode, stream *flyrpc.Stream) error {
	switch mode {
	case flyrpc.StreamModeServerStream:
		if err := stream.Send(rpcBenchRequest{Name: "ada"}); err != nil {
			return err
		}
		for _, want := range []string{"hello ada:first", "hello ada:second"} {
			var resp rpcBenchResponse
			if err := stream.Recv(&resp); err != nil {
				return err
			}
			if resp.Message != want {
				return flyrpc.NewError(flyrpc.CodeInternal, "unexpected server stream response")
			}
		}
	case flyrpc.StreamModeClientStream:
		for _, name := range []string{"ada", "bob"} {
			if err := stream.Send(rpcBenchRequest{Name: name}); err != nil {
				return err
			}
		}
		var resp rpcBenchResponse
		if err := stream.Recv(&resp); err != nil {
			return err
		}
		if resp.Message != "hello ada,bob" {
			return flyrpc.NewError(flyrpc.CodeInternal, "unexpected client stream response")
		}
	default:
		if err := stream.Send(rpcBenchRequest{Name: "ada"}); err != nil {
			return err
		}
		var resp rpcBenchResponse
		if err := stream.Recv(&resp); err != nil {
			return err
		}
		if resp.Message != "hello ada" {
			return flyrpc.NewError(flyrpc.CodeInternal, "unexpected bidi stream response")
		}
	}
	return nil
}

func benchmarkGoflyRPCUnary(b *testing.B) {
	server := flyrpc.NewServer()
	if err := server.RegisterService(flyrpc.ServiceDesc{Name: "greeter", Methods: []flyrpc.MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(rpcBenchRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return rpcBenchResponse{Message: "hello " + req.(*rpcBenchRequest).Name}, nil
		},
	}}}, nil); err != nil {
		b.Fatal(err)
	}
	upstream := httptest.NewServer(server)
	defer upstream.Close()

	client, err := flyrpc.NewClient(upstream.URL)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		var resp rpcBenchResponse
		if err := client.Call(context.Background(), "greeter/SayHello", rpcBenchRequest{Name: "ada"}, &resp); err != nil {
			b.Fatal(err)
		}
		if resp.Message != "hello ada" {
			b.Fatalf("message = %q, want hello ada", resp.Message)
		}
	}
}

func benchmarkGRPCGoUnary(b *testing.B) {
	listener := bufconn.Listen(1024 * 1024)
	server := stdgrpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	dialer := func(context.Context, string) (net.Conn, error) { return listener.Dial() }
	conn, err := stdgrpc.NewClient(
		"passthrough:///bufnet",
		stdgrpc.WithContextDialer(dialer),
		stdgrpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	client := healthpb.NewHealthClient(conn)

	b.ReportAllocs()
	for b.Loop() {
		resp, err := client.Check(context.Background(), &healthpb.HealthCheckRequest{})
		if err != nil {
			b.Fatal(err)
		}
		if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			b.Fatalf("status = %s, want SERVING", resp.GetStatus())
		}
	}
}
