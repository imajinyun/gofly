package grpc

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/imajinyun/gofly/rpc"

	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

type fakeWatchResolver struct {
	endpoints []string
	err       error
	updates   chan []string
}

func (r *fakeWatchResolver) Resolve(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	return append([]string(nil), r.endpoints...), nil
}

func (r *fakeWatchResolver) Watch(ctx context.Context) (<-chan []string, error) {
	if r.updates == nil {
		r.updates = make(chan []string, 4)
	}
	return r.updates, nil
}

type fakeResolverClientConn struct {
	resolver.ClientConn
	mu            sync.Mutex
	states        []resolver.State
	errors        []error
	serviceConfig string
}

func (c *fakeResolverClientConn) UpdateState(state resolver.State) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states = append(c.states, state)
	return nil
}

func (c *fakeResolverClientConn) ReportError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, err)
}

func (c *fakeResolverClientConn) NewAddress([]resolver.Address) {}

func (c *fakeResolverClientConn) ParseServiceConfig(config string) *serviceconfig.ParseResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.serviceConfig = config
	return &serviceconfig.ParseResult{}
}

func TestGRPCResolverBuilderUpdatesStateAndWatches(t *testing.T) {
	source := &fakeWatchResolver{endpoints: []string{"127.0.0.1:9001/", "127.0.0.1:9001", "127.0.0.1:9002"}}
	builder := NewResolverBuilder(map[string]rpc.WatchResolver{"greeter": source})
	cc := &fakeResolverClientConn{}
	resolver, err := builder.Build(resolver.Target{URL: url.URL{Scheme: ResolverScheme, Path: "/greeter"}}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()

	state := waitResolverState(t, cc, 1)
	if got := addressesOf(state); len(got) != 2 || got[0] != "127.0.0.1:9001" || got[1] != "127.0.0.1:9002" {
		t.Fatalf("initial addresses = %v, want deduplicated trimmed addresses", got)
	}
	if cc.serviceConfig != roundRobinServiceConfig {
		t.Fatalf("service config = %q, want round robin config", cc.serviceConfig)
	}

	source.updates <- []string{"127.0.0.1:9003/"}
	state = waitResolverState(t, cc, 2)
	if got := addressesOf(state); len(got) != 1 || got[0] != "127.0.0.1:9003" {
		t.Fatalf("watched addresses = %v, want updated address", got)
	}
}

func TestGRPCResolverBuilderReportsResolveError(t *testing.T) {
	source := &fakeWatchResolver{err: errors.New("registry down")}
	builder := NewResolverBuilder(map[string]rpc.WatchResolver{"greeter": source})
	cc := &fakeResolverClientConn{}
	resolver, err := builder.Build(resolver.Target{URL: url.URL{Scheme: ResolverScheme, Path: "/greeter"}}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()

	deadline := time.After(time.Second)
	for {
		cc.mu.Lock()
		errorsCount := len(cc.errors)
		cc.mu.Unlock()
		if errorsCount > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for resolver error")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestGRPCRegistryResolverBuilderUsesRegistryWatch(t *testing.T) {
	registry := rpc.NewRegistry()
	builder := NewRegistryResolverBuilder(registry)
	cc := &fakeResolverClientConn{}
	resolver, err := builder.Build(resolver.Target{URL: url.URL{Scheme: ResolverScheme, Path: "/greeter"}}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()

	if err := registry.RegisterService(context.Background(), "greeter", "127.0.0.1:9101/"); err != nil {
		t.Fatal(err)
	}
	state := waitResolverState(t, cc, 1)
	if got := addressesOf(state); len(got) != 1 || got[0] != "127.0.0.1:9101" {
		t.Fatalf("registry addresses = %v, want registered endpoint", got)
	}
}

func TestGRPCTarget(t *testing.T) {
	if got := Target("/greeter/"); got != "gofly:///greeter" {
		t.Fatalf("Target = %q, want gofly:///greeter", got)
	}
}

func TestResolverOptions(t *testing.T) {
	b := NewResolverBuilder(nil, WithResolverScheme("custom"))
	if got := b.Scheme(); got != "custom" {
		t.Fatalf("Scheme = %q, want custom", got)
	}

	b2 := NewResolverBuilder(nil, WithResolverServiceConfig(`{"foo":"bar"}`))
	if b2.serviceConfig != `{"foo":"bar"}` {
		t.Fatalf("serviceConfig = %q, want custom config", b2.serviceConfig)
	}

	b3 := NewResolverBuilder(nil, WithRoundRobinResolver())
	if b3.serviceConfig != roundRobinServiceConfig {
		t.Fatalf("serviceConfig = %q, want round robin config", b3.serviceConfig)
	}

	// nil builder returns default scheme
	var nilBuilder *ResolverBuilder
	if got := nilBuilder.Scheme(); got != ResolverScheme {
		t.Fatalf("nil Scheme = %q, want %q", got, ResolverScheme)
	}
}

func TestWithServiceResolver(t *testing.T) {
	opt := WithServiceResolver("greeter", &fakeWatchResolver{})
	o := clientOptions{timeout: 5 * time.Second}
	opt(&o)
	if len(o.dialOptions) != 1 {
		t.Fatalf("expected 1 dial option, got %d", len(o.dialOptions))
	}
}

func TestWithRegistryResolver(t *testing.T) {
	registry := rpc.NewRegistry()
	opt := WithRegistryResolver(registry)
	o := clientOptions{timeout: 5 * time.Second}
	opt(&o)
	if len(o.dialOptions) != 1 {
		t.Fatalf("expected 1 dial option, got %d", len(o.dialOptions))
	}
}

func waitResolverState(t *testing.T, cc *fakeResolverClientConn, count int) resolver.State {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		cc.mu.Lock()
		if len(cc.states) >= count {
			state := cc.states[count-1]
			cc.mu.Unlock()
			return state
		}
		cc.mu.Unlock()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for resolver state %d", count)
		case <-time.After(time.Millisecond):
		}
	}
}

func addressesOf(state resolver.State) []string {
	out := make([]string, 0, len(state.Addresses))
	for _, address := range state.Addresses {
		out = append(out, address.Addr)
	}
	return out
}

func TestServiceFromTargetBranches(t *testing.T) {
	// Endpoint empty, Path present
	u := url.URL{Scheme: ResolverScheme, Path: "/greeter"}
	if got := serviceFromTarget(resolver.Target{URL: u}); got != "greeter" {
		t.Fatalf("serviceFromTarget(Path) = %q, want greeter", got)
	}
	// Endpoint and Path empty, Host present
	u2 := url.URL{Scheme: ResolverScheme, Host: "greeter"}
	if got := serviceFromTarget(resolver.Target{URL: u2}); got != "greeter" {
		t.Fatalf("serviceFromTarget(Host) = %q, want greeter", got)
	}
	// All empty
	if got := serviceFromTarget(resolver.Target{URL: url.URL{}}); got != "" {
		t.Fatalf("serviceFromTarget(empty) = %q, want empty", got)
	}
}

func TestResolverUpdateEmptyAndDuplicateEndpoints(t *testing.T) {
	source := &fakeWatchResolver{endpoints: []string{"", "  ", "127.0.0.1:9001", "127.0.0.1:9001"}}
	builder := NewResolverBuilder(map[string]rpc.WatchResolver{"greeter": source})
	cc := &fakeResolverClientConn{}
	r, err := builder.Build(resolver.Target{URL: url.URL{Scheme: ResolverScheme, Path: "/greeter"}}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	state := waitResolverState(t, cc, 1)
	if got := addressesOf(state); len(got) != 1 || got[0] != "127.0.0.1:9001" {
		t.Fatalf("addresses = %v, want single deduped address", got)
	}
}

func TestResolverUpdateNoEndpoints(t *testing.T) {
	source := &fakeWatchResolver{endpoints: []string{"", "  "}}
	builder := NewResolverBuilder(map[string]rpc.WatchResolver{"greeter": source})
	cc := &fakeResolverClientConn{}
	r, err := builder.Build(resolver.Target{URL: url.URL{Scheme: ResolverScheme, Path: "/greeter"}}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	deadline := time.After(time.Second)
	for {
		cc.mu.Lock()
		errCount := len(cc.errors)
		cc.mu.Unlock()
		if errCount > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for no-endpoints error")
		case <-time.After(time.Millisecond):
		}
	}
}

type fakeResolverClientConnUpdateErr struct {
	fakeResolverClientConn
}

func (c *fakeResolverClientConnUpdateErr) UpdateState(state resolver.State) error {
	return errors.New("update state failed")
}

func TestResolverUpdateStateError(t *testing.T) {
	source := &fakeWatchResolver{endpoints: []string{"127.0.0.1:9001"}}
	builder := NewResolverBuilder(map[string]rpc.WatchResolver{"greeter": source})
	cc := &fakeResolverClientConnUpdateErr{}
	r, err := builder.Build(resolver.Target{URL: url.URL{Scheme: ResolverScheme, Path: "/greeter"}}, cc, resolver.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	deadline := time.After(time.Second)
	for {
		cc.mu.Lock()
		errCount := len(cc.errors)
		cc.mu.Unlock()
		if errCount > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for update state error")
		case <-time.After(time.Millisecond):
		}
	}
}
