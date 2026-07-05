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

func TestMemoryRegistryValidationAndSnapshotCopies(t *testing.T) {
	registry := NewMemoryRegistry()
	if _, err := registry.Register(context.Background(), Instance{Service: "", Endpoint: "http://127.0.0.1:8081"}); err == nil {
		t.Fatal("Register without service should fail")
	}
	if _, err := registry.Register(context.Background(), Instance{Service: "orders", Endpoint: ""}); err == nil {
		t.Fatal("Register without endpoint should fail")
	}

	if _, err := registry.Register(context.Background(), Instance{
		Service:  "orders",
		Endpoint: "http://127.0.0.1:8081",
		Tags:     map[string]string{"role": "primary"},
		Metadata: map[string]string{"owner": "checkout"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	snapshot := registry.Snapshot()
	snapshot["orders"][0].Tags["role"] = "mutated"
	snapshot["orders"][0].Metadata["owner"] = "mutated"
	again := registry.Snapshot()
	if again["orders"][0].Tags["role"] != "primary" || again["orders"][0].Metadata["owner"] != "checkout" {
		t.Fatalf("snapshot leaked mutable internals: %#v", again["orders"][0])
	}

	if err := registry.Deregister(context.Background(), Instance{}); err != nil {
		t.Fatalf("empty deregister should be no-op: %v", err)
	}
	if _, err := registry.Resolve(context.Background(), "missing"); !errors.Is(err, ErrNoInstances) {
		t.Fatalf("missing resolve err = %v, want ErrNoInstances", err)
	}
}

func TestMemoryRegistryRegisterSurvivesExpiredServiceMapCleanup(t *testing.T) {
	registry := NewMemoryRegistry()
	if _, err := registry.Register(context.Background(), Instance{Service: "orders", ID: "old", Endpoint: "http://old"}, WithTTL(time.Millisecond)); err != nil {
		t.Fatalf("register old: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := registry.Register(context.Background(), Instance{Service: "orders", ID: "new", Endpoint: "http://new"}); err != nil {
		t.Fatalf("register new after expired cleanup: %v", err)
	}
	instances, err := registry.Resolve(context.Background(), "orders")
	if err != nil {
		t.Fatalf("resolve after expired cleanup: %v", err)
	}
	if len(instances) != 1 || instances[0].ID != "new" {
		t.Fatalf("instances after expired cleanup = %#v, want only new", instances)
	}
}

func TestMemoryLeaseKeepAliveBoundaries(t *testing.T) {
	registry := NewMemoryRegistry()
	noTTLLease, err := registry.Register(context.Background(), Instance{Service: "orders", ID: "no-ttl", Endpoint: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatalf("register no ttl: %v", err)
	}
	if err := noTTLLease.KeepAlive(context.Background()); err != nil {
		t.Fatalf("KeepAlive without ttl = %v, want nil", err)
	}
	if expiresAt := noTTLLease.ExpiresAt(); !expiresAt.IsZero() {
		t.Fatalf("no ttl ExpiresAt = %s, want zero", expiresAt)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, err := registry.Watch(ctx, "orders")
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	<-events
	defer cancel()

	lease, err := registry.Register(context.Background(), Instance{Service: "orders", ID: "ttl", Endpoint: "http://127.0.0.1:8081"}, WithTTL(time.Hour))
	if err != nil {
		t.Fatalf("register ttl: %v", err)
	}
	<-events
	before := lease.ExpiresAt()
	if before.IsZero() {
		t.Fatal("ttl lease expiration should be set")
	}
	time.Sleep(time.Millisecond)
	if err := lease.KeepAlive(context.Background()); err != nil {
		t.Fatalf("KeepAlive: %v", err)
	}
	updated := <-events
	if updated.Type != EventUpdated || len(updated.Changes.Updated) != 1 || updated.Changes.Updated[0].ID != "ttl" {
		t.Fatalf("keepalive event = %#v, want updated ttl lease", updated)
	}
	if after := lease.ExpiresAt(); !after.After(before) {
		t.Fatalf("KeepAlive did not extend ttl: before=%s after=%s", before, after)
	}

	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := lease.KeepAlive(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("KeepAlive canceled err = %v, want context.Canceled", err)
	}

	missing := &memoryLease{
		registry: registry,
		instance: Instance{Service: "orders", ID: "missing", Endpoint: "http://missing"},
		ttl:      time.Hour,
	}
	if err := missing.KeepAlive(context.Background()); err != nil {
		t.Fatalf("missing lease KeepAlive should be no-op: %v", err)
	}
}

func TestMemoryRegistryWatchFiltersAndBackpressure(t *testing.T) {
	registry := NewMemoryRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := registry.Watch(ctx, "orders", WithTag("role", "primary"))
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	<-events

	if _, err := registry.Register(context.Background(), Instance{Service: "orders", ID: "secondary", Endpoint: "http://secondary", Tags: map[string]string{"role": "secondary"}}); err != nil {
		t.Fatalf("register secondary: %v", err)
	}
	secondary := nextMemoryDiscoveryEvent(t, events)
	if len(secondary.Instances) != 0 {
		t.Fatalf("filtered watcher instances = %#v, want none", secondary.Instances)
	}

	if _, err := registry.Register(context.Background(), Instance{Service: "orders", ID: "old", Endpoint: "http://old", Tags: map[string]string{"role": "primary"}}); err != nil {
		t.Fatalf("register old: %v", err)
	}
	if _, err := registry.Register(context.Background(), Instance{Service: "orders", ID: "new", Endpoint: "http://new", Tags: map[string]string{"role": "primary"}}); err != nil {
		t.Fatalf("register new: %v", err)
	}
	latest := nextMemoryDiscoveryEvent(t, events)
	if latest.Type != EventRegistered || latest.Instance.ID != "new" || len(latest.Instances) != 2 {
		t.Fatalf("backpressure watcher event = %#v, want latest primary registration", latest)
	}
}

func nextMemoryDiscoveryEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for memory discovery event")
	}
	return Event{}
}
