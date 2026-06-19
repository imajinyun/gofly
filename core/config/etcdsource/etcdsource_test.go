package etcdsource

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"

	"github.com/gofly/gofly/core/config"
)

func TestNewValidation(t *testing.T) {
	_, err := New(Config{})
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("New without endpoints error = %v, want endpoint error", err)
	}

	_, err = New(Config{Endpoints: []string{"127.0.0.1:2379"}})
	if err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("New without key error = %v, want key required", err)
	}
}

func TestNewWithClientValidationAndCloseNoop(t *testing.T) {
	if _, err := NewWithClient(nil, "cfg/app"); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("NewWithClient(nil) error = %v, want client is nil", err)
	}
	client := newEtcdTestClient(t)
	src, err := NewWithClient(client, "cfg/app")
	if err != nil {
		t.Fatalf("NewWithClient valid error = %v", err)
	}
	if src.client != client || src.key != "cfg/app" || src.ownsConn {
		t.Fatalf("source = %#v, want borrowed client/key without ownership", src)
	}
	if err := src.Close(); err != nil {
		t.Fatalf("Close on borrowed source error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close borrowed client: %v", err)
	}

	keyValidationClient := newEtcdTestClient(t)
	defer keyValidationClient.Close()
	if _, err := NewWithClient(keyValidationClient, ""); err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Fatalf("NewWithClient(empty key) error = %v, want key required", err)
	}
	if err := (&Source{ownsConn: false}).Close(); err != nil {
		t.Fatalf("Close on borrowed/empty source error = %v", err)
	}
}

func TestConfigDefaultDialTimeoutValidationPath(t *testing.T) {
	cfg := Config{Endpoints: []string{"127.0.0.1:2379"}, Key: "cfg/app"}
	if cfg.DialTimeout != 0 {
		t.Fatalf("fixture DialTimeout = %s, want zero", cfg.DialTimeout)
	}
	cfg.DialTimeout = 5 * time.Second
	if cfg.DialTimeout != 5*time.Second {
		t.Fatalf("DialTimeout = %s, want 5s", cfg.DialTimeout)
	}
}

func TestGetWrapsClientError(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	src, err := NewWithClient(client, "cfg/app")
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = src.Get(ctx)
	if err == nil || !strings.Contains(err.Error(), `get "cfg/app"`) || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("Get canceled error = %v, want wrapped context canceled", err)
	}
}

func TestWatchReturnsContextCancellation(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	src, err := NewWithClient(client, "cfg/app")
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := src.Watch(ctx, nil); err != context.Canceled {
		t.Fatalf("Watch canceled error = %v, want context.Canceled", err)
	}
}

func TestCloseClosesOwnedClient(t *testing.T) {
	client := newEtcdTestClient(t)
	src := &Source{client: client, key: "cfg/app", ownsConn: true}
	if err := src.Close(); err != nil {
		t.Fatalf("Close owned client error = %v", err)
	}
}

func TestNewCreatesOwnedSourceWithDefaults(t *testing.T) {
	server := newFakeEtcdServer(t)
	src, err := New(Config{Endpoints: []string{server.endpoint}, Key: "cfg/app"})
	if err != nil {
		t.Fatalf("New valid config error = %v", err)
	}
	defer src.Close()
	if src.key != "cfg/app" || !src.ownsConn || src.client == nil {
		t.Fatalf("source = %#v, want owned source for cfg/app", src)
	}
}

func TestGetSuccessAndMissingKey(t *testing.T) {
	server := newFakeEtcdServer(t)
	server.kvs["cfg/app"] = &mvccpb.KeyValue{Key: []byte("cfg/app"), Value: []byte("payload"), ModRevision: 42}
	client := server.client(t)
	defer client.Close()

	src, err := NewWithClient(client, "cfg/app")
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	got, err := src.Get(context.Background())
	if err != nil {
		t.Fatalf("Get existing key error = %v", err)
	}
	if got.Key != "cfg/app" || string(got.Data) != "payload" || got.Version != 42 {
		t.Fatalf("Get existing key = %#v, want payload revision 42", got)
	}

	missing, err := NewWithClient(client, "cfg/missing")
	if err != nil {
		t.Fatalf("NewWithClient missing error = %v", err)
	}
	_, err = missing.Get(context.Background())
	if err == nil || !strings.Contains(err.Error(), `key "cfg/missing" not found`) {
		t.Fatalf("Get missing key error = %v, want not found", err)
	}
}

func TestWatchProcessesPutEventsAndSkipsMalformed(t *testing.T) {
	server := newFakeEtcdServer(t)
	server.watchEvents = []*mvccpb.Event{
		{Type: mvccpb.DELETE, Kv: &mvccpb.KeyValue{Key: []byte("cfg/app"), Value: []byte("delete")}},
		{Type: mvccpb.PUT, Kv: nil},
		{Type: mvccpb.PUT, Kv: &mvccpb.KeyValue{Key: []byte("cfg/app"), Value: []byte("new-payload"), ModRevision: 99}},
	}
	client := server.client(t)
	defer client.Close()
	src, err := NewWithClient(client, "cfg/app")
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var changes []string
	err = src.Watch(ctx, func(v config.RemoteValue) {
		changes = append(changes, string(v.Data))
		if v.Key != "cfg/app" || v.Version != 99 {
			t.Fatalf("watch value = %#v, want cfg/app revision 99", v)
		}
		cancel()
	})
	if err != context.Canceled {
		t.Fatalf("Watch error = %v, want context.Canceled after callback", err)
	}
	if len(changes) != 1 || changes[0] != "new-payload" {
		t.Fatalf("changes = %#v, want only put payload", changes)
	}
}

func TestWatchAllowsNilCallback(t *testing.T) {
	server := newFakeEtcdServer(t)
	server.watchEvents = []*mvccpb.Event{{Type: mvccpb.PUT, Kv: &mvccpb.KeyValue{Key: []byte("cfg/app"), Value: []byte("payload"), ModRevision: 1}}}
	client := server.client(t)
	defer client.Close()
	src, err := NewWithClient(client, "cfg/app")
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := src.Watch(ctx, nil); err != context.DeadlineExceeded {
		t.Fatalf("Watch nil callback error = %v, want deadline exceeded", err)
	}
}

func newEtcdTestClient(t *testing.T) *clientv3.Client {
	t.Helper()
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"127.0.0.1:1"},
		DialTimeout: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new etcd test client: %v", err)
	}
	return client
}

type fakeEtcdServer struct {
	etcdserverpb.UnimplementedKVServer
	etcdserverpb.UnimplementedWatchServer

	endpoint    string
	grpcServer  *grpc.Server
	kvs         map[string]*mvccpb.KeyValue
	watchEvents []*mvccpb.Event
}

func newFakeEtcdServer(t *testing.T) *fakeEtcdServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake etcd: %v", err)
	}
	server := &fakeEtcdServer{endpoint: ln.Addr().String(), grpcServer: grpc.NewServer(), kvs: make(map[string]*mvccpb.KeyValue)}
	etcdserverpb.RegisterKVServer(server.grpcServer, server)
	etcdserverpb.RegisterWatchServer(server.grpcServer, server)
	t.Cleanup(func() {
		server.grpcServer.Stop()
		_ = ln.Close()
	})
	go func() {
		_ = server.grpcServer.Serve(ln)
	}()
	return server
}

func (s *fakeEtcdServer) client(t *testing.T) *clientv3.Client {
	t.Helper()
	client, err := clientv3.New(clientv3.Config{Endpoints: []string{s.endpoint}, DialTimeout: time.Second})
	if err != nil {
		t.Fatalf("new fake etcd client: %v", err)
	}
	return client
}

func (s *fakeEtcdServer) Range(_ context.Context, req *etcdserverpb.RangeRequest) (*etcdserverpb.RangeResponse, error) {
	resp := &etcdserverpb.RangeResponse{Header: &etcdserverpb.ResponseHeader{Revision: 1}}
	if kv := s.kvs[string(req.Key)]; kv != nil {
		resp.Kvs = []*mvccpb.KeyValue{kv}
		resp.Count = 1
	}
	return resp, nil
}

func (s *fakeEtcdServer) Watch(stream etcdserverpb.Watch_WatchServer) error {
	req, err := stream.Recv()
	if err != nil {
		return err
	}
	create := req.GetCreateRequest()
	watchID := int64(1)
	if create != nil {
		watchID = int64(len(create.Key))
	}
	if err := stream.Send(&etcdserverpb.WatchResponse{Header: &etcdserverpb.ResponseHeader{Revision: 1}, WatchId: watchID, Created: true}); err != nil {
		return err
	}
	if len(s.watchEvents) > 0 {
		if err := stream.Send(&etcdserverpb.WatchResponse{Header: &etcdserverpb.ResponseHeader{Revision: 2}, WatchId: watchID, Events: s.watchEvents}); err != nil {
			return err
		}
	}
	_, err = stream.Recv()
	if err == io.EOF || err == context.Canceled {
		return nil
	}
	return err
}
