//go:build integration

package interop

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/proc"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/wrapperspb"

	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
)

const echoMethod = "/interop.Echo/Call"

type echoServer interface {
	Call(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)
}

type echoService struct {
	prefix string
}

func (s echoService) Call(_ context.Context, request *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	return wrapperspb.String(s.prefix + request.GetValue()), nil
}

var echoServiceDesc = stdgrpc.ServiceDesc{
	ServiceName: "interop.Echo",
	HandlerType: (*echoServer)(nil),
	Methods: []stdgrpc.MethodDesc{{
		MethodName: "Call",
		Handler: func(server any, ctx context.Context, decode func(any) error, interceptor stdgrpc.UnaryServerInterceptor) (any, error) {
			request := new(wrapperspb.StringValue)
			if err := decode(request); err != nil {
				return nil, err
			}
			if interceptor == nil {
				return server.(echoServer).Call(ctx, request)
			}
			info := &stdgrpc.UnaryServerInfo{Server: server, FullMethod: echoMethod}
			handler := func(ctx context.Context, request any) (any, error) {
				return server.(echoServer).Call(ctx, request.(*wrapperspb.StringValue))
			}
			return interceptor(ctx, request, info, handler)
		},
	}},
}

func TestZRPCServerWithGoflyClient(t *testing.T) {
	address := freeAddress(t)
	server, err := zrpc.NewServer(zrpc.RpcServerConf{
		ServiceConf: service.ServiceConf{Name: "zrpc-interop", Mode: service.TestMode},
		ListenOn:    address, Health: true,
	}, func(server *stdgrpc.Server) { server.RegisterService(&echoServiceDesc, echoService{prefix: "zrpc:"}) })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); server.Start() }()
	t.Cleanup(func() {
		proc.Shutdown()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("timed out stopping zRPC server")
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := flygrpc.NewDefaultClient(ctx, address, "grpc.health.v1.Health", nil, nil, flygrpc.WithWaitForReady())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	response, err := grpc_health_v1.NewHealthClient(conn.Conn()).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("zRPC health response=%v err=%v", response, err)
	}
	var echo wrapperspb.StringValue
	if err := conn.Invoke(ctx, echoMethod, wrapperspb.String("gofly"), &echo); err != nil || echo.Value != "zrpc:gofly" {
		t.Fatalf("zRPC echo response=%q err=%v", echo.Value, err)
	}
}

func TestGoflyServerWithZRPCClient(t *testing.T) {
	server := flygrpc.NewDefaultServer("127.0.0.1:0", "grpc.health.v1.Health", nil, nil)
	server.RegisterService(&echoServiceDesc, echoService{prefix: "gofly:"})
	done := make(chan error, 1)
	go func() { done <- server.Start() }()
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("timed out stopping gofly server")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for !server.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !server.Ready() {
		t.Fatal("gofly server did not become ready")
	}

	client, err := zrpc.NewClientWithTarget(server.Address())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response, err := grpc_health_v1.NewHealthClient(client.Conn()).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("gofly health response=%v err=%v", response, err)
	}
	var echo wrapperspb.StringValue
	if err := client.Conn().Invoke(ctx, echoMethod, wrapperspb.String("zrpc"), &echo); err != nil || echo.Value != "gofly:zrpc" {
		t.Fatalf("gofly echo response=%q err=%v", echo.Value, err)
	}
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
