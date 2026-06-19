package rpc

import (
	"context"
	"errors"
	"testing"
)

func TestResolverFuncAndStaticResolverHardeningContracts(t *testing.T) {
	called := false
	resolver := ResolverFunc(func(ctx context.Context) ([]string, error) {
		called = true
		return []string{" http://a/ ", "http://a", "http://b/"}, nil
	})
	endpoints, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !called || len(endpoints) != 3 {
		t.Fatalf("resolver called=%v endpoints=%#v, want direct function result", called, endpoints)
	}

	static := NewStaticResolver(" http://a/ ", "http://a", "", " http://b ")
	endpoints, err = static.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 || endpoints[0] != "http://a" || endpoints[1] != "http://b" {
		t.Fatalf("static endpoints = %#v, want trimmed unique endpoints", endpoints)
	}
	if _, err := (StaticResolver{}).Resolve(context.Background()); err == nil {
		t.Fatal("empty static resolver succeeded, want error")
	}
}

func TestCachedResolverConstructionAndSnapshotBoundaries(t *testing.T) {
	if _, err := NewCachedResolver(context.Background(), nil); err == nil {
		t.Fatal("NewCachedResolver nil source succeeded, want error")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewCachedResolver(canceled, hardeningWatchResolver{resolveErr: errors.New("source failed")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("NewCachedResolver canceled error = %v, want context.Canceled", err)
	}

	watchErr := errors.New("watch failed")
	if _, err := NewCachedResolver(context.Background(), hardeningWatchResolver{watchErr: watchErr}); !errors.Is(err, watchErr) {
		t.Fatalf("NewCachedResolver watch error = %v, want %v", err, watchErr)
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	updates := make(chan []string, 1)
	cached, err := NewCachedResolver(ctx, hardeningWatchResolver{endpoints: []string{"http://a/", "http://b"}, updates: updates})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cached.Snapshot()
	if len(snapshot.Endpoints) != 2 || snapshot.Endpoints[0] != "http://a" {
		t.Fatalf("snapshot = %#v, want normalized endpoints", snapshot)
	}
	snapshot.Endpoints[0] = "mutated"
	resolved, err := cached.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0] != "http://a" {
		t.Fatalf("resolved endpoints = %#v, want snapshot mutation not to leak", resolved)
	}
	var nilCached *CachedResolver
	if got := nilCached.Snapshot(); len(got.Endpoints) != 0 || got.Error != "" {
		t.Fatalf("nil snapshot = %#v, want zero snapshot", got)
	}
}

func TestRegistryResolverHardeningBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	registry := NewRegistry()
	if err := registry.RegisterInstance(ctx, "svc", ServiceInstance{Endpoint: "http://a"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("RegisterInstance canceled error = %v, want context.Canceled", err)
	}
	if err := registry.RegisterInstance(context.Background(), " ", ServiceInstance{Endpoint: "http://a"}); err != nil {
		t.Fatalf("RegisterInstance empty service error = %v, want nil", err)
	}
	if err := registry.DeregisterService(ctx, "svc", "http://a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeregisterService canceled error = %v, want context.Canceled", err)
	}
	if _, err := registry.ResolveInstances(ctx, "svc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveInstances canceled error = %v, want context.Canceled", err)
	}

	var nilRegistry *Registry
	if nilRegistry.Discovery() == nil {
		t.Fatal("nil registry Discovery returned nil")
	}
	if err := nilRegistry.RegisterInstance(context.Background(), "svc", ServiceInstance{Endpoint: "http://a", Weight: 2, Tags: map[string]string{"role": "primary"}}); err != nil {
		t.Fatalf("nil registry RegisterInstance: %v", err)
	}

	if err := registry.RegisterInstance(context.Background(), "svc", ServiceInstance{Endpoint: "http://a/", Weight: 2, Version: "v1", Zone: "az1"}); err != nil {
		t.Fatal(err)
	}
	resolver := registry.Resolver("svc").(registryResolver)
	instances, err := resolver.ResolveInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Endpoint != "http://a" || instances[0].Weight != 2 {
		t.Fatalf("instances = %#v, want normalized registered instance", instances)
	}
}

type hardeningWatchResolver struct {
	endpoints  []string
	resolveErr error
	updates    chan []string
	watchErr   error
}

func (r hardeningWatchResolver) Resolve(ctx context.Context) ([]string, error) {
	if r.resolveErr != nil {
		return nil, r.resolveErr
	}
	return append([]string(nil), r.endpoints...), nil
}

func (r hardeningWatchResolver) Watch(ctx context.Context) (<-chan []string, error) {
	if r.watchErr != nil {
		return nil, r.watchErr
	}
	if r.updates != nil {
		return r.updates, nil
	}
	return make(chan []string), nil
}
