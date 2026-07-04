package bench

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
	if snapshot.Active != 0 || snapshot.Dials != opened || snapshot.Closes != opened {
		b.Fatalf("stream transport snapshot = %+v, want dials/closes=%d and no active streams", snapshot, opened)
	}
	if snapshot.LastTarget != upstream.URL || snapshot.LastDialedAt.IsZero() || snapshot.LastClosedAt.IsZero() {
		b.Fatalf("stream transport snapshot = %+v, want lifecycle target and timestamps", snapshot)
	}
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
