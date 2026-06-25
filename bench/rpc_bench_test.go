package bench

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"

	flyrpc "github.com/gofly/gofly/rpc"

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
	// carry generated Kitex fixtures yet; see bench/README.md for the
	// extension point used by downstream projects that already depend on Kitex.
}

func BenchmarkRPCStreamGovernance(b *testing.B) {
	// stream governance overhead is tracked separately from unary RPC because
	// stream setup, receive, and close behavior exercise different policy hooks.
	server := flyrpc.NewServer()
	if err := server.RegisterService(flyrpc.ServiceDesc{Name: "streamer", Streams: []flyrpc.StreamDesc{{
		Name:       "Watch",
		NewMessage: func() any { return new(rpcBenchRequest) },
		Mode:       flyrpc.StreamModeBidiStream,
		Handler: func(ctx context.Context, stream *flyrpc.Stream) error {
			var req rpcBenchRequest
			if err := stream.Recv(&req); err != nil {
				return err
			}
			return stream.Send(rpcBenchResponse{Message: "hello " + req.Name})
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
		if err := stream.Send(rpcBenchRequest{Name: "ada"}); err != nil {
			b.Fatal(err)
		}
		var resp rpcBenchResponse
		if err := stream.Recv(&resp); err != nil {
			b.Fatal(err)
		}
		if resp.Message != "hello ada" {
			b.Fatalf("message = %q, want hello ada", resp.Message)
		}
		_ = stream.Close()
	}
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
