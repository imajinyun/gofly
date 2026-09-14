//go:build integration

package interop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
	"github.com/zeromicro/go-zero/core/proc"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	serverStreamMethod = "/interop.Streams/ServerStream"
	clientStreamMethod = "/interop.Streams/ClientStream"
	bidiStreamMethod   = "/interop.Streams/BidiStream"
)

type streamingServer interface {
	Prefix() string
}

type streamingService struct {
	prefix string
}

func (s streamingService) Prefix() string { return s.prefix }

var streamingServiceDesc = stdgrpc.ServiceDesc{
	ServiceName: "interop.Streams",
	HandlerType: (*streamingServer)(nil),
	Streams: []stdgrpc.StreamDesc{
		{StreamName: "ServerStream", Handler: serverStreamHandler, ServerStreams: true},
		{StreamName: "ClientStream", Handler: clientStreamHandler, ClientStreams: true},
		{StreamName: "BidiStream", Handler: bidiStreamHandler, ClientStreams: true, ServerStreams: true},
	},
}

func validateStreamContext(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.InvalidArgument, "incoming metadata is required")
	}
	values := md.Get("x-interop-origin")
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return status.Error(codes.InvalidArgument, "x-interop-origin metadata is required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return status.Error(codes.InvalidArgument, "stream deadline is required")
	}
	return nil
}

func serverStreamHandler(server any, stream stdgrpc.ServerStream) error {
	if err := validateStreamContext(stream.Context()); err != nil {
		return err
	}
	request := new(wrapperspb.StringValue)
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	for index := 1; index <= 3; index++ {
		response := wrapperspb.String(fmt.Sprintf("%s%s:%d", server.(streamingServer).Prefix(), request.Value, index))
		if err := stream.SendMsg(response); err != nil {
			return err
		}
	}
	return nil
}

func clientStreamHandler(server any, stream stdgrpc.ServerStream) error {
	if err := validateStreamContext(stream.Context()); err != nil {
		return err
	}
	var values []string
	for {
		request := new(wrapperspb.StringValue)
		err := stream.RecvMsg(request)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		values = append(values, request.Value)
	}
	return stream.SendMsg(wrapperspb.String(server.(streamingServer).Prefix() + strings.Join(values, "+")))
}

func bidiStreamHandler(server any, stream stdgrpc.ServerStream) error {
	if err := validateStreamContext(stream.Context()); err != nil {
		return err
	}
	for {
		request := new(wrapperspb.StringValue)
		if err := stream.RecvMsg(request); err != nil {
			return err
		}
		if err := stream.SendMsg(wrapperspb.String(server.(streamingServer).Prefix() + request.Value)); err != nil {
			return err
		}
	}
}

type streamClientConn interface {
	NewStream(context.Context, *stdgrpc.StreamDesc, string, ...stdgrpc.CallOption) (stdgrpc.ClientStream, error)
}

func newStreamContext(t *testing.T, origin string) (context.Context, context.CancelFunc) {
	t.Helper()
	outgoing := metadata.AppendToOutgoingContext(t.Context(), "x-interop-origin", origin)
	return context.WithTimeout(outgoing, 5*time.Second)
}

func runStreamingClientMatrix(t *testing.T, conn streamClientConn, prefix, origin string) {
	t.Helper()
	t.Run("server stream", func(t *testing.T) {
		ctx, cancel := newStreamContext(t, origin)
		defer cancel()
		stream, err := conn.NewStream(ctx, &streamingServiceDesc.Streams[0], serverStreamMethod)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.SendMsg(wrapperspb.String("one")); err != nil {
			t.Fatal(err)
		}
		if err := stream.CloseSend(); err != nil {
			t.Fatal(err)
		}
		for index, want := range []string{prefix + "one:1", prefix + "one:2", prefix + "one:3"} {
			response := new(wrapperspb.StringValue)
			if err := stream.RecvMsg(response); err != nil {
				t.Fatalf("response %d: %v", index, err)
			}
			if response.Value != want {
				t.Fatalf("response %d=%q want=%q", index, response.Value, want)
			}
		}
		if err := stream.RecvMsg(new(wrapperspb.StringValue)); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal receive=%v want EOF", err)
		}
	})

	t.Run("client stream", func(t *testing.T) {
		ctx, cancel := newStreamContext(t, origin)
		defer cancel()
		stream, err := conn.NewStream(ctx, &streamingServiceDesc.Streams[1], clientStreamMethod)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"a", "b", "c"} {
			if err := stream.SendMsg(wrapperspb.String(value)); err != nil {
				t.Fatal(err)
			}
		}
		if err := stream.CloseSend(); err != nil {
			t.Fatal(err)
		}
		response := new(wrapperspb.StringValue)
		if err := stream.RecvMsg(response); err != nil {
			t.Fatal(err)
		}
		if want := prefix + "a+b+c"; response.Value != want {
			t.Fatalf("response=%q want=%q", response.Value, want)
		}
		if err := stream.RecvMsg(new(wrapperspb.StringValue)); !errors.Is(err, io.EOF) {
			t.Fatalf("terminal receive=%v want EOF", err)
		}
	})

	t.Run("bidirectional", func(t *testing.T) {
		ctx, cancel := newStreamContext(t, origin)
		stream, err := conn.NewStream(ctx, &streamingServiceDesc.Streams[2], bidiStreamMethod)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		for _, value := range []string{"left", "right"} {
			if err := stream.SendMsg(wrapperspb.String(value)); err != nil {
				cancel()
				t.Fatal(err)
			}
			response := new(wrapperspb.StringValue)
			if err := stream.RecvMsg(response); err != nil {
				cancel()
				t.Fatal(err)
			}
			if want := prefix + value; response.Value != want {
				cancel()
				t.Fatalf("response=%q want=%q", response.Value, want)
			}
		}
		cancel()
		err = stream.RecvMsg(new(wrapperspb.StringValue))
		if status.Code(err) != codes.Canceled && !errors.Is(err, context.Canceled) {
			t.Fatalf("receive after cancel=%v", err)
		}
	})
}

func TestZRPCServerStreamsWithGoflyClient(t *testing.T) {
	address := freeAddress(t)
	server, err := zrpc.NewServer(zrpc.RpcServerConf{
		ServiceConf: service.ServiceConf{Name: "zrpc-stream-interop", Mode: service.TestMode},
		ListenOn:    address,
		Health:      true,
	}, func(server *stdgrpc.Server) {
		server.RegisterService(&streamingServiceDesc, streamingService{prefix: "zrpc:"})
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Start()
	}()
	t.Cleanup(func() {
		proc.Shutdown()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("timed out stopping zRPC streaming server")
		}
	})

	dialCtx, cancelDial := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelDial()
	conn, err := flygrpc.NewDefaultClient(dialCtx, address, "interop.Streams", nil, nil, flygrpc.WithWaitForReady())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	runStreamingClientMatrix(t, conn, "zrpc:", "gofly-client")
}

func TestGoflyServerStreamsWithZRPCClient(t *testing.T) {
	server := flygrpc.NewDefaultServer("127.0.0.1:0", "interop.Streams", nil, nil)
	server.RegisterService(&streamingServiceDesc, streamingService{prefix: "gofly:"})
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
			t.Error("timed out stopping gofly streaming server")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for !server.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !server.Ready() {
		t.Fatal("gofly streaming server did not become ready")
	}

	client, err := zrpc.NewClientWithTarget(server.Address())
	if err != nil {
		t.Fatal(err)
	}
	runStreamingClientMatrix(t, client.Conn(), "gofly:", "zrpc-client")
}
