package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofly/gofly/core/discovery"
)

func TestDiscoveryResolverAdaptsInstancesAndWatch(t *testing.T) {
	registry := discovery.NewMemoryRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := registry.Register(ctx, discovery.Instance{
		Service:  "greeter",
		Endpoint: "http://127.0.0.1:8081",
		Weight:   3,
		Version:  "v1",
		Zone:     "az1",
		Tags:     map[string]string{"role": "primary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := NewDiscoveryResolver(registry, "greeter", discovery.WithVersion("v1"), discovery.WithZone("az1"))

	endpoints, err := resolver.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0] != "http://127.0.0.1:8081" {
		t.Fatalf("endpoints = %#v, want 8081", endpoints)
	}
	instances, err := resolver.ResolveInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Weight != 3 || instances[0].Tags["version"] != "v1" || instances[0].Tags["zone"] != "az1" {
		t.Fatalf("instances = %#v, want adapted weight/version/zone", instances)
	}

	updates, err := resolver.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initial := <-updates
	if len(initial) != 1 || initial[0] != "http://127.0.0.1:8081" {
		t.Fatalf("initial update = %#v, want 8081", initial)
	}
	_, err = registry.Register(ctx, discovery.Instance{Service: "greeter", Endpoint: "http://127.0.0.1:8082", Version: "v1", Zone: "az1"})
	if err != nil {
		t.Fatal(err)
	}
	update := <-updates
	if len(update) != 2 || update[0] != "http://127.0.0.1:8081" || update[1] != "http://127.0.0.1:8082" {
		t.Fatalf("update = %#v, want 8081/8082", update)
	}
}

func TestDiscoveryRegistrarAdaptsLegacyRPCRegistration(t *testing.T) {
	registry := discovery.NewMemoryRegistry()
	registrar := NewDiscoveryRegistrar(registry, discovery.WithTTL(time.Hour))
	ctx := context.Background()
	if err := registrar.RegisterService(ctx, "greeter", "http://127.0.0.1:8081/"); err != nil {
		t.Fatal(err)
	}
	resolver := NewDiscoveryResolver(registry, "greeter")
	endpoints, err := resolver.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0] != "http://127.0.0.1:8081" {
		t.Fatalf("endpoints = %#v, want normalized endpoint", endpoints)
	}
	if err := registrar.DeregisterService(ctx, "greeter", "http://127.0.0.1:8081/"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ctx); err == nil {
		t.Fatal("resolve after deregister succeeded, want error")
	}
}

func TestDiscoveryResolverNilAndConversionBoundaries_BitsUT(t *testing.T) {
	var resolver *DiscoveryResolver
	if _, err := resolver.ResolveInstances(context.Background()); !errors.Is(err, discovery.ErrNoInstances) {
		t.Fatalf("nil ResolveInstances error = %v, want ErrNoInstances", err)
	}
	if _, err := resolver.Watch(context.Background()); !errors.Is(err, discovery.ErrNoInstances) {
		t.Fatalf("nil Watch error = %v, want ErrNoInstances", err)
	}

	instances := serviceInstancesFromDiscovery([]discovery.Instance{
		{},
		{
			Endpoint: "http://127.0.0.1:8081",
			Weight:   5,
			Version:  "v1",
			Zone:     "az1",
			Status:   discovery.StatusHealthy,
			Tags:     map[string]string{"role": "primary"},
			Metadata: map[string]string{"owner": "platform"},
		},
	})
	if len(instances) != 1 {
		t.Fatalf("converted instances = %#v, want only non-empty endpoint", instances)
	}
	got := instances[0]
	if got.Weight != 5 || got.Tags["version"] != "v1" || got.Tags["zone"] != "az1" || got.Tags["status"] != discovery.StatusHealthy || got.Metadata["owner"] != "platform" {
		t.Fatalf("converted instance = %#v, want weight/tags/metadata copied", got)
	}
}

func TestDiscoveryResolverWatchFiltersAndCloses_BitsUT(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan discovery.Event, 1)
	fake := &fakeDiscoveryResolver{watch: events}
	resolver := NewDiscoveryResolver(fake, " svc ")

	updates, err := resolver.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	events <- discovery.Event{Instances: []discovery.Instance{{}, {Endpoint: "http://127.0.0.1:8081"}}}
	select {
	case got := <-updates:
		if len(got) != 1 || got[0] != "http://127.0.0.1:8081" {
			t.Fatalf("watch update = %#v, want filtered endpoint", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for filtered watch update")
	}
	cancel()
	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("updates channel still open after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch channel close")
	}
}

func TestDiscoveryRegistrarNilIsNoop_BitsUT(t *testing.T) {
	var registrar *DiscoveryRegistrar
	if err := registrar.RegisterService(context.Background(), "svc", "endpoint"); err != nil {
		t.Fatalf("nil RegisterService error = %v, want nil", err)
	}
	if err := registrar.DeregisterService(context.Background(), "svc", "endpoint"); err != nil {
		t.Fatalf("nil DeregisterService error = %v, want nil", err)
	}
	converted := discoveryInstance("svc", ServiceInstance{
		Weight:   7,
		Version:  "v2",
		Zone:     "az2",
		Status:   discovery.StatusUnhealthy,
		Tags:     map[string]string{"role": "replica"},
		Metadata: map[string]string{"owner": "rpc"},
	}, "http://127.0.0.1:8082")
	if converted.Service != "svc" || converted.Endpoint != "http://127.0.0.1:8082" || converted.Tags["role"] != "replica" || converted.Metadata["owner"] != "rpc" {
		t.Fatalf("discovery instance = %#v, want copied fields", converted)
	}
}

type fakeDiscoveryResolver struct {
	watch <-chan discovery.Event
}

func (f *fakeDiscoveryResolver) Resolve(context.Context, string, ...discovery.ResolveOption) ([]discovery.Instance, error) {
	return nil, discovery.ErrNoInstances
}

func (f *fakeDiscoveryResolver) Watch(context.Context, string, ...discovery.ResolveOption) (<-chan discovery.Event, error) {
	return f.watch, nil
}
