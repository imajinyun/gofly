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
