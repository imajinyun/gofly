//go:build integration

package interop

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/discov"
	"github.com/zeromicro/go-zero/core/proc"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/discovery/etcdv3"
	flygrpc "github.com/imajinyun/gofly/rpc/grpc"
)

func TestGoflyServerDiscoveredByZRPCClientThroughEtcd(t *testing.T) {
	etcd := newInteropEtcdServer(t)
	registrar, err := etcdv3.NewZRPCRegistrar(etcdv3.Config{
		Endpoints: []string{etcd.endpoint}, DialTimeout: time.Second, TTL: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registrar.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})

	const serviceKey = "/interop/gofly-echo"
	server := flygrpc.NewDefaultServer("127.0.0.1:0", "interop.Echo", nil, nil,
		flygrpc.WithDiscovery(registrar, discovery.Instance{Service: serviceKey}),
	)
	server.RegisterService(&echoServiceDesc, echoService{prefix: "gofly-etcd:"})
	done := make(chan error, 1)
	go func() { done <- server.Start() }()
	waitForGoflyServer(t, server)

	client, err := zrpc.NewClient(zrpc.RpcClientConf{
		Etcd:     discov.EtcdConf{Hosts: []string{etcd.endpoint}, Key: serviceKey},
		NonBlock: true, Timeout: 5000, BalancerName: "round_robin",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Conn().Close(); err != nil {
			t.Error(err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var response wrapperspb.StringValue
	if err := client.Conn().Invoke(ctx, echoMethod, wrapperspb.String("zrpc"), &response, stdgrpc.WaitForReady(true)); err != nil {
		t.Fatal(err)
	}
	if response.Value != "gofly-etcd:zrpc" {
		t.Fatalf("echo response = %q, want gofly-etcd:zrpc", response.Value)
	}
	if got := etcd.valuesWithPrefix(serviceKey + "/"); len(got) != 1 || got[serviceKey+"/101"] != server.Address() {
		t.Fatalf("zRPC etcd records = %#v, want one raw endpoint for %s", got, server.Address())
	}

	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("timed out stopping gofly server")
	}
	if got := etcd.valuesWithPrefix(serviceKey + "/"); len(got) != 0 {
		t.Fatalf("zRPC etcd records after shutdown = %#v, want none", got)
	}
}

func TestZRPCServerDiscoveredByGoflyClientThroughEtcd(t *testing.T) {
	etcd := newInteropEtcdServer(t)
	const serviceKey = "/interop/zrpc-echo"
	address := freeAddress(t)
	server, err := zrpc.NewServer(zrpc.RpcServerConf{
		ServiceConf: service.ServiceConf{Name: "zrpc-etcd-interop", Mode: service.TestMode},
		ListenOn:    address,
		Etcd:        discov.EtcdConf{Hosts: []string{etcd.endpoint}, Key: serviceKey, ID: 202},
		Health:      true,
	}, func(server *stdgrpc.Server) {
		server.RegisterService(&echoServiceDesc, echoService{prefix: "zrpc-etcd:"})
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Start()
	}()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			proc.WrapUp()
			proc.Shutdown()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("timed out stopping zRPC etcd server")
			}
		})
	}
	t.Cleanup(stop)

	waitForEtcdRecord(t, etcd, serviceKey+"/202", address)
	resolver, err := etcdv3.NewZRPCResolver(etcdv3.Config{
		Endpoints: []string{etcd.endpoint}, DialTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resolver.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client, err := flygrpc.NewDefaultClient(
		ctx, flygrpc.Target(serviceKey), "interop.Echo", nil, nil,
		flygrpc.WithDiscoveryResolver(resolver, serviceKey),
		flygrpc.WithWaitForReady(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	var response wrapperspb.StringValue
	if err := client.Invoke(ctx, echoMethod, wrapperspb.String("gofly"), &response); err != nil {
		t.Fatal(err)
	}
	if response.Value != "zrpc-etcd:gofly" {
		t.Fatalf("echo response = %q, want zrpc-etcd:gofly", response.Value)
	}

	stop()
	waitForEtcdRecord(t, etcd, serviceKey+"/202", "")
}

func waitForEtcdRecord(t *testing.T, etcd *interopEtcdServer, key, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := etcd.valuesWithPrefix(key)[key]; got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("etcd record %q = %q, want %q", key, etcd.valuesWithPrefix(key)[key], want)
}

func waitForGoflyServer(t *testing.T, server *flygrpc.Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !server.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !server.Ready() {
		t.Fatal("gofly server did not become ready")
	}
}

type interopEtcdServer struct {
	etcdserverpb.UnimplementedKVServer
	etcdserverpb.UnimplementedWatchServer
	etcdserverpb.UnimplementedLeaseServer
	etcdserverpb.UnimplementedMaintenanceServer

	endpoint string
	server   *stdgrpc.Server

	mu       sync.Mutex
	values   map[string]string
	leases   map[int64][]string
	revision int64
	nextID   int64
}

func newInteropEtcdServer(t *testing.T) *interopEtcdServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &interopEtcdServer{
		endpoint: listener.Addr().String(), server: stdgrpc.NewServer(),
		values: make(map[string]string), leases: make(map[int64][]string),
		revision: 1, nextID: 100,
	}
	etcdserverpb.RegisterKVServer(server.server, server)
	etcdserverpb.RegisterWatchServer(server.server, server)
	etcdserverpb.RegisterLeaseServer(server.server, server)
	etcdserverpb.RegisterMaintenanceServer(server.server, server)
	go func() { _ = server.server.Serve(listener) }()
	t.Cleanup(func() {
		server.server.Stop()
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	return server
}

func (s *interopEtcdServer) Status(_ context.Context, _ *etcdserverpb.StatusRequest) (*etcdserverpb.StatusResponse, error) {
	return &etcdserverpb.StatusResponse{
		Header:  &etcdserverpb.ResponseHeader{Revision: s.currentRevision()},
		Version: "3.7.1",
		Leader:  1,
	}, nil
}

func (s *interopEtcdServer) Range(_ context.Context, request *etcdserverpb.RangeRequest) (*etcdserverpb.RangeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	response := &etcdserverpb.RangeResponse{Header: &etcdserverpb.ResponseHeader{Revision: s.revision}}
	prefix := string(request.Key)
	for key, value := range s.values {
		if strings.HasPrefix(key, prefix) {
			response.Kvs = append(response.Kvs, &mvccpb.KeyValue{Key: []byte(key), Value: []byte(value), ModRevision: s.revision})
		}
	}
	response.Count = int64(len(response.Kvs))
	return response, nil
}

func (s *interopEtcdServer) Put(_ context.Context, request *etcdserverpb.PutRequest) (*etcdserverpb.PutResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	key := string(request.Key)
	s.values[key] = string(request.Value)
	if request.Lease != 0 {
		s.leases[request.Lease] = append(s.leases[request.Lease], key)
	}
	return &etcdserverpb.PutResponse{Header: &etcdserverpb.ResponseHeader{Revision: s.revision}}, nil
}

func (s *interopEtcdServer) Watch(stream etcdserverpb.Watch_WatchServer) error {
	request, err := stream.Recv()
	if err != nil {
		return err
	}
	create := request.GetCreateRequest()
	if create == nil {
		return errors.New("watch create request is required")
	}
	if err := stream.Send(&etcdserverpb.WatchResponse{
		Header: &etcdserverpb.ResponseHeader{Revision: s.currentRevision()}, WatchId: 1, Created: true,
	}); err != nil {
		return err
	}
	_, err = stream.Recv()
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (s *interopEtcdServer) LeaseGrant(_ context.Context, request *etcdserverpb.LeaseGrantRequest) (*etcdserverpb.LeaseGrantResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := request.ID
	if id == 0 {
		s.nextID++
		id = s.nextID
	}
	s.leases[id] = nil
	return &etcdserverpb.LeaseGrantResponse{
		Header: &etcdserverpb.ResponseHeader{Revision: s.revision}, ID: id, TTL: request.TTL,
	}, nil
}

func (s *interopEtcdServer) LeaseRevoke(_ context.Context, request *etcdserverpb.LeaseRevokeRequest) (*etcdserverpb.LeaseRevokeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	for _, key := range s.leases[request.ID] {
		delete(s.values, key)
	}
	delete(s.leases, request.ID)
	return &etcdserverpb.LeaseRevokeResponse{Header: &etcdserverpb.ResponseHeader{Revision: s.revision}}, nil
}

func (s *interopEtcdServer) LeaseKeepAlive(stream etcdserverpb.Lease_LeaseKeepAliveServer) error {
	for {
		request, err := stream.Recv()
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(&etcdserverpb.LeaseKeepAliveResponse{
			Header: &etcdserverpb.ResponseHeader{Revision: s.currentRevision()}, ID: request.ID, TTL: 5,
		}); err != nil {
			return err
		}
	}
}

func (s *interopEtcdServer) valuesWithPrefix(prefix string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := maps.Clone(s.values)
	for key := range values {
		if !strings.HasPrefix(key, prefix) {
			delete(values, key)
		}
	}
	return values
}

func (s *interopEtcdServer) currentRevision() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}
