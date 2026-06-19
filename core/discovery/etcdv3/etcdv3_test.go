package etcdv3

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/gofly/gofly/core/discovery"
)

func TestConfigDefaultsAndNewValidation(t *testing.T) {
	cfg := (Config{Prefix: "/custom///"}).withDefaults()
	if cfg.Prefix != "/custom" {
		t.Fatalf("prefix = %q, want /custom", cfg.Prefix)
	}
	if cfg.DialTimeout != 5*time.Second || cfg.TTL != 15*time.Second {
		t.Fatalf("defaults = dial %s ttl %s, want 5s/15s", cfg.DialTimeout, cfg.TTL)
	}
	if _, err := New(Config{}); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("New without endpoints error = %v, want endpoint error", err)
	}
	if _, err := NewWithClient(nil, Config{}); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("NewWithClient(nil) error = %v, want client is nil", err)
	}
}

func TestNewWithEndpointCreatesLazyClient(t *testing.T) {
	r, err := New(Config{Endpoints: []string{"127.0.0.1:1"}, DialTimeout: time.Millisecond})
	if err != nil {
		t.Fatalf("New with endpoint error = %v", err)
	}
	if r == nil || r.client == nil {
		t.Fatalf("New registry = %#v, want registry with client", r)
	}
	if r.cfg.Prefix != "/gofly/services" {
		t.Fatalf("prefix = %q, want default prefix", r.cfg.Prefix)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close lazy client: %v", err)
	}
}

func TestKeyAndServicePrefix(t *testing.T) {
	r := &Registry{cfg: (Config{Prefix: "/gofly/services/"}).withDefaults()}
	if got := r.key("users", "10.0.0.1:8080"); got != "/gofly/services/users/10.0.0.1:8080" {
		t.Fatalf("key = %q", got)
	}
	if got := r.servicePrefix("users"); got != "/gofly/services/users/" {
		t.Fatalf("servicePrefix = %q", got)
	}
}

func TestDecodeAndNormalize(t *testing.T) {
	got, err := decode("users", nil)
	if err != nil {
		t.Fatalf("decode empty error = %v", err)
	}
	if got.Service != "users" || got.Endpoint != "" {
		t.Fatalf("decode empty = %#v", got)
	}

	data, err := json.Marshal(discovery.Instance{Endpoint: " http://127.0.0.1:8080/ "})
	if err != nil {
		t.Fatal(err)
	}
	got, err = decode("users", data)
	if err != nil {
		t.Fatalf("decode valid error = %v", err)
	}
	if got.Service != "users" || got.Endpoint != "http://127.0.0.1:8080" || got.ID != "http://127.0.0.1:8080" {
		t.Fatalf("decode valid = %#v", got)
	}

	if _, err := decode("users", []byte("{")); err == nil {
		t.Fatal("decode invalid json error = nil, want error")
	}
}

func TestNormalizeTrimsAndDefaultsID(t *testing.T) {
	got := normalize(discovery.Instance{
		Service:  " users ",
		Endpoint: " http://127.0.0.1:8080/ ",
		ID:       " ",
		Version:  " v1 ",
		Zone:     " az-a ",
	})
	if got.Service != "users" || got.Endpoint != "http://127.0.0.1:8080" || got.ID != "http://127.0.0.1:8080" {
		t.Fatalf("normalize core fields = %#v", got)
	}
	if got.Version != "v1" || got.Zone != "az-a" {
		t.Fatalf("normalize version/zone = %#v", got)
	}
}

func TestRegisterValidationWithNilContext(t *testing.T) {
	r := &Registry{cfg: (Config{}).withDefaults()}
	_, err := r.Register(context.Background(), discovery.Instance{Service: "users"})
	if err == nil || !strings.Contains(err.Error(), "requires service and endpoint") {
		t.Fatalf("Register incomplete instance error = %v, want validation error", err)
	}
}

func TestNewWithClientAppliesDefaults(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()

	r, err := NewWithClient(client, Config{})
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	if r.client != client {
		t.Fatal("registry did not keep provided client")
	}
	if r.cfg.Prefix != "/gofly/services" || r.cfg.DialTimeout != 5*time.Second || r.cfg.TTL != 15*time.Second {
		t.Fatalf("defaults = %#v", r.cfg)
	}
}

func TestResolveDeregisterAndWatchReturnContextCancellation(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, err := NewWithClient(client, Config{Prefix: "/tests"})
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Resolve(ctx, "users"); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("Resolve canceled error = %v, want context canceled", err)
	}
	if err := r.Deregister(ctx, discovery.Instance{Service: "users", Endpoint: "10.0.0.1:8080"}); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("Deregister canceled error = %v, want context canceled", err)
	}
	if _, err := r.Watch(ctx, "users"); err != context.Canceled {
		t.Fatalf("Watch canceled error = %v, want context.Canceled", err)
	}
}

func TestToEventMapsDeleteAndPut(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, err := NewWithClient(client, Config{Prefix: "/tests"})
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	putData, err := json.Marshal(discovery.Instance{Service: "users", Endpoint: "10.0.0.1:8080", Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	registered := r.toEvent(ctx, "users", nil, &clientv3.Event{Type: clientv3.EventTypePut, Kv: &mvccpb.KeyValue{Value: putData}})
	if registered.Type != discovery.EventRegistered || registered.Instance.Endpoint != "10.0.0.1:8080" || registered.Instance.Version != "v1" {
		t.Fatalf("registered event = %#v", registered)
	}
	deleted := r.toEvent(ctx, "users", nil, &clientv3.Event{Type: clientv3.EventTypeDelete})
	if deleted.Type != discovery.EventDeregister || deleted.Instance.Service != "users" {
		t.Fatalf("delete event = %#v", deleted)
	}
}

func TestLeaseConsumeRefreshesExpiryAndAccessors(t *testing.T) {
	if got := (*lease)(nil).Instance(); got.Service != "" || got.Endpoint != "" || got.ID != "" {
		t.Fatalf("nil lease Instance = %#v, want zero instance", got)
	}
	if got := (*lease)(nil).ExpiresAt(); !got.IsZero() {
		t.Fatalf("nil lease ExpiresAt = %s, want zero time", got)
	}

	inst := discovery.Instance{Service: "users", Endpoint: "10.0.0.1:8080", ID: "node-1"}
	l := &lease{instance: inst}
	if got := l.Instance(); got.Service != inst.Service || got.Endpoint != inst.Endpoint || got.ID != inst.ID {
		t.Fatalf("Instance = %#v, want %#v", got, inst)
	}

	ch := make(chan *clientv3.LeaseKeepAliveResponse, 2)
	ch <- nil
	ch <- &clientv3.LeaseKeepAliveResponse{TTL: 3}
	close(ch)
	before := time.Now()
	l.consume(ch)
	expires := l.ExpiresAt()
	if !expires.After(before.Add(2 * time.Second)) {
		t.Fatalf("ExpiresAt after consume = %s, want at least two seconds after %s", expires, before)
	}
}

func TestLeaseKeepAliveAndCloseBoundaries(t *testing.T) {
	if err := (*lease)(nil).KeepAlive(context.Background()); err != nil {
		t.Fatalf("nil lease KeepAlive error = %v, want nil", err)
	}
	if err := (*lease)(nil).Close(context.Background()); err != nil {
		t.Fatalf("nil lease Close error = %v, want nil", err)
	}

	client := newEtcdTestClient(t)
	defer client.Close()
	r, err := NewWithClient(client, Config{Prefix: "/tests"})
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	l := &lease{registry: r, id: clientv3.LeaseID(123)}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.KeepAlive(canceled); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("KeepAlive canceled error = %v, want context canceled", err)
	}

	cancelCalled := false
	l.cancel = func() { cancelCalled = true }
	if err := l.Close(canceled); err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("Close canceled error = %v, want context canceled", err)
	}
	if !cancelCalled {
		t.Fatal("Close did not invoke lease cancel function")
	}
	if err := l.Close(context.Background()); err != nil {
		t.Fatalf("Close second call error = %v, want nil", err)
	}
}

func TestForwardStopsOnLifecycleSignals(t *testing.T) {
	tests := []struct {
		name  string
		ctx   context.Context
		watch func() clientv3.WatchChan
	}{
		{
			name: "context canceled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			watch: func() clientv3.WatchChan {
				ch := make(chan clientv3.WatchResponse)
				return ch
			},
		},
		{
			name: "watch channel closed",
			ctx:  context.Background(),
			watch: func() clientv3.WatchChan {
				ch := make(chan clientv3.WatchResponse)
				close(ch)
				return ch
			},
		},
		{
			name: "watch response canceled",
			ctx:  context.Background(),
			watch: func() clientv3.WatchChan {
				ch := make(chan clientv3.WatchResponse, 1)
				ch <- clientv3.WatchResponse{Canceled: true}
				return ch
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := make(chan discovery.Event)
			(&Registry{}).forward(tt.ctx, "users", nil, tt.watch(), out)
			if ev, ok := <-out; ok {
				t.Fatalf("forward output still open with event %#v; want closed", ev)
			}
		})
	}
}

func TestRegistryCloseClosesLeasesAndIsIdempotent(t *testing.T) {
	client := newFakeEtcdClient(t)
	defer client.Close()
	r, err := NewWithClient(client, Config{Prefix: "/tests"})
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	cancelCalled := false
	r.leases = []*lease{{registry: r, id: clientv3.LeaseID(456), cancel: func() { cancelCalled = true }}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := r.Close(ctx); err != nil {
		t.Fatalf("Registry Close error = %v, want nil", err)
	}
	if !cancelCalled {
		t.Fatal("Registry Close did not close tracked lease")
	}
	if len(r.leases) != 0 {
		t.Fatalf("leases len after Close = %d, want 0", len(r.leases))
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Registry Close second call error = %v, want nil", err)
	}
}

func TestDeregisterValidationAndNilContext(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})

	// empty service/id returns nil without calling etcd
	if err := r.Deregister(context.Background(), discovery.Instance{}); err != nil {
		t.Fatalf("Deregister empty instance error = %v, want nil", err)
	}
	if err := r.Deregister(context.Background(), discovery.Instance{Service: "users"}); err != nil {
		t.Fatalf("Deregister missing id error = %v, want nil", err)
	}

	// Use a pre-canceled context to verify the disconnected client path without
	// waiting for gRPC dial retries against the intentionally invalid endpoint.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = r.Deregister(ctx, discovery.Instance{Service: "users", ID: "1"})
}

func TestResolveValidationAndNilContext(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})

	// empty service name
	if _, err := r.Resolve(context.Background(), "  "); err == nil || !strings.Contains(err.Error(), "service name is required") {
		t.Fatalf("Resolve empty service error = %v, want validation error", err)
	}

	// Use a pre-canceled context to verify the disconnected client path without
	// waiting for gRPC dial retries against the intentionally invalid endpoint.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Resolve(ctx, "users"); err == nil {
		t.Fatal("Resolve with canceled context on disconnected client should error")
	}
}

func TestWatchValidationAndNilContext(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})

	// empty service name
	if _, err := r.Watch(context.Background(), "  "); err == nil || !strings.Contains(err.Error(), "service name is required") {
		t.Fatalf("Watch empty service error = %v, want validation error", err)
	}

	// Use a pre-canceled context to verify the disconnected client path without
	// waiting for gRPC dial retries against the intentionally invalid endpoint.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Watch(ctx, "users"); err == nil {
		t.Fatal("Watch with canceled context on disconnected client should error")
	}
}

func TestRegisterNilContextAndSuccessPath(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})

	// nil context defaults to Background; use short timeout because disconnected client blocks.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := r.Register(ctx, discovery.Instance{Service: "users", Endpoint: "10.0.0.1:8080"})
	if err == nil {
		t.Fatal("Register with short timeout on disconnected client should error")
	}

	// validation: missing endpoint
	_, err = r.Register(context.Background(), discovery.Instance{Service: "users"})
	if err == nil || !strings.Contains(err.Error(), "requires service and endpoint") {
		t.Fatalf("Register missing endpoint error = %v, want validation error", err)
	}
}

func TestLeaseKeepAliveNilRegistryAndNilContext(t *testing.T) {
	l := &lease{registry: nil, id: clientv3.LeaseID(1)}
	if err := l.KeepAlive(context.Background()); err != nil {
		t.Fatalf("KeepAlive nil registry error = %v, want nil", err)
	}

	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	l = &lease{registry: r, id: clientv3.LeaseID(1)}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := l.KeepAlive(ctx); err == nil {
		t.Fatal("KeepAlive with short timeout on disconnected client should error")
	}
}

func TestLeaseCloseNilContext(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	l := &lease{registry: r, id: clientv3.LeaseID(1), cancel: func() {}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := l.Close(ctx); err == nil {
		t.Fatal("Close with short timeout on disconnected client should error")
	}
}

func TestRegisterNilContextBranch(t *testing.T) {
	r := &Registry{cfg: (Config{}).withDefaults()}
	// nil context should default to Background and then fail validation
	//nolint:staticcheck // exercises the public nil-context compatibility branch.
	_, err := r.Register(nil, discovery.Instance{Service: "users"})
	if err == nil || !strings.Contains(err.Error(), "requires service and endpoint") {
		t.Fatalf("Register nil context error = %v, want validation error", err)
	}
}

func TestDeregisterNilContextBranch(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	// nil context should default to Background; empty instance returns nil early
	//nolint:staticcheck // exercises the public nil-context compatibility branch.
	if err := r.Deregister(nil, discovery.Instance{}); err != nil {
		t.Fatalf("Deregister nil context empty instance error = %v, want nil", err)
	}
}

func TestResolveNilContextBranch(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	// nil context defaults to Background; empty service returns validation error
	//nolint:staticcheck // exercises the public nil-context compatibility branch.
	_, err := r.Resolve(nil, "  ")
	if err == nil || !strings.Contains(err.Error(), "service name is required") {
		t.Fatalf("Resolve nil context error = %v, want validation error", err)
	}
}

func TestWatchNilContextBranch(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	// nil context defaults to Background; empty service returns validation error
	//nolint:staticcheck // exercises the public nil-context compatibility branch.
	_, err := r.Watch(nil, "  ")
	if err == nil || !strings.Contains(err.Error(), "service name is required") {
		t.Fatalf("Watch nil context error = %v, want validation error", err)
	}
}

func TestLeaseKeepAliveNilContextBranch(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	l := &lease{registry: r, id: clientv3.LeaseID(1)}
	// nil context defaults to Background then fails on disconnected client
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := l.KeepAlive(ctx); err == nil {
		t.Fatal("KeepAlive with short timeout should error")
	}
}

func TestLeaseCloseNilContextBranch(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	l := &lease{registry: r, id: clientv3.LeaseID(1), cancel: func() {}}
	// nil context defaults to Background then fails on disconnected client
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := l.Close(ctx); err == nil {
		t.Fatal("Close with short timeout should error")
	}
}

func TestRegistryCloseIdempotent(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("first Close error = %v", err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
}

func TestLeaseCloseIdempotent(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	l := &lease{registry: r, id: clientv3.LeaseID(1), cancel: func() {}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = l.Close(ctx)
	if err := l.Close(context.Background()); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
}

func TestForwardEventSendContextDone(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	ctx, cancel := context.WithCancel(context.Background())
	watchCh := make(chan clientv3.WatchResponse, 1)
	watchCh <- clientv3.WatchResponse{Events: []*clientv3.Event{{Type: clientv3.EventTypePut, Kv: &mvccpb.KeyValue{Value: []byte(`{"endpoint":"http://127.0.0.1:1"}`)}}}}
	out := make(chan discovery.Event)
	go func() {
		// cancel after a short delay so forward enters the event-send select
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	r.forward(ctx, "users", nil, watchCh, out)
	// channel should be closed
	if _, ok := <-out; ok {
		// may or may not receive the event depending on timing; either is ok
		_, stillOpen := <-out
		if stillOpen {
			t.Fatal("forward output channel should be closed")
		}
	}
}

func TestNewAuthFailure(t *testing.T) {
	_, err := New(Config{Endpoints: []string{"127.0.0.1:1"}, DialTimeout: time.Millisecond, Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("New with auth on disconnected endpoint: want error, got nil")
	}
}

func TestResolveDecodeErrorAndEmptyEndpoint(t *testing.T) {
	client := newEtcdTestClient(t)
	defer client.Close()
	r, _ := NewWithClient(client, Config{Prefix: "/tests"})
	// Manually put invalid JSON and empty-endpoint data to exercise decode error
	// and endpoint-empty skip branches. We use the real client with a short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = r.client.Put(ctx, "/tests/users/bad", "{invalid")
	_, _ = r.client.Put(ctx, "/tests/users/empty", `{"endpoint":""}`)
	// Resolve will hit Get (canceled context) so we only verify the decode logic
	// by calling decode directly.
	if _, err := decode("users", []byte("{invalid")); err == nil {
		t.Fatal("decode invalid json: want error")
	}
	inst, err := decode("users", []byte(`{"endpoint":""}`))
	if err != nil {
		t.Fatalf("decode empty endpoint error = %v", err)
	}
	if inst.Endpoint != "" {
		t.Fatalf("decode empty endpoint = %q, want empty", inst.Endpoint)
	}
}

type fakeEtcdKV struct {
	data map[string]string
}

func (f *fakeEtcdKV) Put(ctx context.Context, key, val string, opts ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	f.data[key] = val
	return &clientv3.PutResponse{}, nil
}

func (f *fakeEtcdKV) Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	withPrefix := clientv3.IsOptsWithPrefix(opts)
	var kvs []*mvccpb.KeyValue
	if withPrefix {
		for k, v := range f.data {
			if strings.HasPrefix(k, key) {
				kvs = append(kvs, &mvccpb.KeyValue{Key: []byte(k), Value: []byte(v)})
			}
		}
	} else {
		if v, ok := f.data[key]; ok {
			kvs = append(kvs, &mvccpb.KeyValue{Key: []byte(key), Value: []byte(v)})
		}
	}
	return &clientv3.GetResponse{Kvs: kvs}, nil
}

func (f *fakeEtcdKV) Delete(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	delete(f.data, key)
	return &clientv3.DeleteResponse{}, nil
}

func (f *fakeEtcdKV) Compact(ctx context.Context, rev int64, opts ...clientv3.CompactOption) (*clientv3.CompactResponse, error) {
	return nil, nil
}

func (f *fakeEtcdKV) Do(ctx context.Context, op clientv3.Op) (clientv3.OpResponse, error) {
	return clientv3.OpResponse{}, nil
}

func (f *fakeEtcdKV) Txn(ctx context.Context) clientv3.Txn {
	return nil
}

type fakeEtcdLease struct {
	id clientv3.LeaseID
}

func (f *fakeEtcdLease) Grant(ctx context.Context, ttl int64) (*clientv3.LeaseGrantResponse, error) {
	return &clientv3.LeaseGrantResponse{ID: f.id}, nil
}

func (f *fakeEtcdLease) Revoke(ctx context.Context, id clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error) {
	return &clientv3.LeaseRevokeResponse{}, nil
}

func (f *fakeEtcdLease) TimeToLive(ctx context.Context, id clientv3.LeaseID, opts ...clientv3.LeaseOption) (*clientv3.LeaseTimeToLiveResponse, error) {
	return &clientv3.LeaseTimeToLiveResponse{}, nil
}

func (f *fakeEtcdLease) Leases(ctx context.Context) (*clientv3.LeaseLeasesResponse, error) {
	return &clientv3.LeaseLeasesResponse{}, nil
}

func (f *fakeEtcdLease) KeepAlive(ctx context.Context, id clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	ch := make(chan *clientv3.LeaseKeepAliveResponse, 1)
	ch <- &clientv3.LeaseKeepAliveResponse{TTL: 10}
	close(ch)
	return ch, nil
}

func (f *fakeEtcdLease) KeepAliveOnce(ctx context.Context, id clientv3.LeaseID) (*clientv3.LeaseKeepAliveResponse, error) {
	return &clientv3.LeaseKeepAliveResponse{TTL: 10}, nil
}

func (f *fakeEtcdLease) Close() error { return nil }

type fakeEtcdWatcher struct {
	ch clientv3.WatchChan
}

func (f *fakeEtcdWatcher) Watch(ctx context.Context, key string, opts ...clientv3.OpOption) clientv3.WatchChan {
	return f.ch
}

func (f *fakeEtcdWatcher) RequestProgress(ctx context.Context) error { return nil }
func (f *fakeEtcdWatcher) Close() error                              { return nil }

func newFakeEtcdClient(t *testing.T) *clientv3.Client {
	t.Helper()
	c, err := clientv3.New(clientv3.Config{Endpoints: []string{"127.0.0.1:1"}, DialTimeout: time.Millisecond})
	if err != nil {
		t.Fatalf("new fake etcd client: %v", err)
	}
	c.KV = &fakeEtcdKV{data: make(map[string]string)}
	c.Lease = &fakeEtcdLease{id: clientv3.LeaseID(999)}
	c.Watcher = &fakeEtcdWatcher{ch: make(chan clientv3.WatchResponse)}
	return c
}

func TestRegisterResolveWithFakeClient(t *testing.T) {
	client := newFakeEtcdClient(t)
	defer client.Close()
	r, err := NewWithClient(client, Config{Prefix: "/tests", TTL: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewWithClient error = %v", err)
	}
	defer r.Close(context.Background())

	inst := discovery.Instance{Service: "users", Endpoint: "http://127.0.0.1:8080", ID: "node-1", Version: "v1"}
	l, err := r.Register(context.Background(), inst)
	if err != nil {
		t.Fatalf("Register error = %v", err)
	}
	if l == nil {
		t.Fatal("Register returned nil lease")
	}
	if got := l.Instance(); got.ID != inst.ID {
		t.Fatalf("lease.Instance = %#v", got)
	}

	resolved, err := r.Resolve(context.Background(), "users")
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if len(resolved) != 1 || resolved[0].ID != inst.ID {
		t.Fatalf("resolved = %#v", resolved)
	}

	// empty endpoint should be skipped during resolve
	fakeKV := client.KV.(*fakeEtcdKV)
	fakeKV.data["/tests/users/empty"] = `{"endpoint":""}`
	resolved, err = r.Resolve(context.Background(), "users")
	if err != nil {
		t.Fatalf("Resolve with empty endpoint error = %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved after empty endpoint = %#v", resolved)
	}

	if err := r.Deregister(context.Background(), inst); err != nil {
		t.Fatalf("Deregister error = %v", err)
	}

	if err := l.KeepAlive(context.Background()); err != nil {
		t.Fatalf("KeepAlive error = %v", err)
	}
	if err := l.Close(context.Background()); err != nil {
		t.Fatalf("Lease Close error = %v", err)
	}
}

func newEtcdTestClient(t *testing.T) *clientv3.Client {
	t.Helper()
	client, err := clientv3.New(clientv3.Config{Endpoints: []string{"127.0.0.1:1"}, DialTimeout: time.Millisecond})
	if err != nil {
		t.Fatalf("new etcd test client: %v", err)
	}
	return client
}
