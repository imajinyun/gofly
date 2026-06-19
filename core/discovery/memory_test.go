package discovery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryRegistryResolveFiltersAndClonesInstances(t *testing.T) {
	registry := NewMemoryRegistry()
	ctx := context.Background()
	_, err := registry.Register(ctx, Instance{
		Service:  "orders",
		Endpoint: "http://127.0.0.1:8081/",
		Weight:   2,
		Version:  "v1",
		Zone:     "az1",
		Status:   StatusHealthy,
		Tags:     map[string]string{"role": "primary"},
		Metadata: map[string]string{"owner": "checkout"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Register(ctx, Instance{Service: "orders", Endpoint: "http://127.0.0.1:8082", Version: "v2", Zone: "az2", Tags: map[string]string{"role": "secondary"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Register(ctx, Instance{Service: "orders", Endpoint: "http://127.0.0.1:8083", Version: "v1", Zone: "az1", Status: StatusUnhealthy, Tags: map[string]string{"role": "primary"}})
	if err != nil {
		t.Fatal(err)
	}

	instances, err := registry.Resolve(ctx, "orders", WithVersion("v1"), WithZone("az1"), WithTag("role", "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Endpoint != "http://127.0.0.1:8081" || instances[0].Weight != 2 {
		t.Fatalf("instances = %#v, want only healthy v1 primary", instances)
	}
	instances[0].Tags["role"] = "mutated"
	instances[0].Metadata["owner"] = "mutated"

	again, err := registry.Resolve(ctx, "orders", WithVersion("v1"), WithZone("az1"), WithTag("role", "primary"))
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Tags["role"] != "primary" || again[0].Metadata["owner"] != "checkout" {
		t.Fatalf("instance was not cloned: %#v", again[0])
	}

	all, err := registry.Resolve(ctx, "orders", IncludeUnhealthy(), WithVersion("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all v1 instances = %#v, want healthy + unhealthy", all)
	}
}

func TestMemoryRegistryWatchLeaseAndTTL(t *testing.T) {
	registry := NewMemoryRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := registry.Watch(ctx, "orders")
	if err != nil {
		t.Fatal(err)
	}
	initial := <-events
	if initial.Type != EventSnapshot || len(initial.Instances) != 0 {
		t.Fatalf("initial event = %#v, want empty snapshot", initial)
	}

	lease, err := registry.Register(ctx, Instance{Service: "orders", Endpoint: "http://127.0.0.1:8081"}, WithTTL(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	registered := <-events
	if registered.Type != EventRegistered || registered.Instance.Endpoint != "http://127.0.0.1:8081" || len(registered.Instances) != 1 {
		t.Fatalf("registered event = %#v, want one instance", registered)
	}
	if lease.ExpiresAt().IsZero() {
		t.Fatal("lease expiration is zero")
	}
	if err := lease.Close(ctx); err != nil {
		t.Fatal(err)
	}
	deregistered := <-events
	if deregistered.Type != EventDeregister || len(deregistered.Instances) != 0 {
		t.Fatalf("deregistered event = %#v, want no instances", deregistered)
	}

	_, err = registry.Register(ctx, Instance{Service: "orders", Endpoint: "http://127.0.0.1:8082"}, WithTTL(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	<-events
	time.Sleep(10 * time.Millisecond)
	_, err = registry.Resolve(ctx, "orders")
	if !errors.Is(err, ErrNoInstances) {
		t.Fatalf("resolve expired err = %v, want ErrNoInstances", err)
	}
	expired := <-events
	if expired.Type != EventExpired || expired.Instance.Endpoint != "http://127.0.0.1:8082" {
		t.Fatalf("expired event = %#v, want expired 8082", expired)
	}

	cancel()
	for range events {
	}
	if watchers := registry.Watchers("orders"); watchers != 0 {
		t.Fatalf("watchers = %d, want 0", watchers)
	}
}
