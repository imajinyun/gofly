package rpc

import (
	"context"
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
