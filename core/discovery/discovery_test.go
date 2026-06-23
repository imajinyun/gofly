package discovery

import (
	"context"
	"testing"
	"time"
)

func TestWithTagsAndOptions(t *testing.T) {
	opts := applyResolveOptions([]ResolveOption{
		WithTags(map[string]string{"env": "prod", " ": "ignored", "region": "us-east"}),
		WithVersion(" v1 "),
		WithZone(" az-a "),
		IncludeUnhealthy(),
	})
	if len(opts.tags) != 2 {
		t.Fatalf("tags = %v, want 2 entries", opts.tags)
	}
	if opts.tags["env"] != "prod" || opts.tags["region"] != "us-east" {
		t.Fatalf("tag values = %v", opts.tags)
	}
	if opts.version != "v1" || opts.zone != "az-a" || !opts.includeUnhealthy {
		t.Fatalf("options = %#v", opts)
	}

	// nil option is ignored
	beforeTags := len(opts.tags)
	applyResolveOptions([]ResolveOption{nil})
	if len(opts.tags) != beforeTags {
		t.Fatal("nil option mutated options")
	}
}

func TestNormalizeInstance(t *testing.T) {
	inst := normalizeInstance(Instance{
		Service:  " users ",
		Endpoint: " http://127.0.0.1:8080/ ",
		ID:       " ",
		Weight:   -5,
		Version:  " v1 ",
		Zone:     " az-a ",
		Status:   " healthy ",
		Tags:     map[string]string{"k": "v"},
		Metadata: map[string]string{"a": "b"},
	})
	if inst.Service != "users" || inst.Endpoint != "http://127.0.0.1:8080" || inst.ID != "http://127.0.0.1:8080" {
		t.Fatalf("normalize core fields = %#v", inst)
	}
	if inst.Weight != 0 || inst.Version != "v1" || inst.Zone != "az-a" || inst.Status != "healthy" {
		t.Fatalf("normalize secondary fields = %#v", inst)
	}
	if len(inst.Tags) != 1 || len(inst.Metadata) != 1 {
		t.Fatalf("normalize maps = %#v", inst)
	}

	// empty maps become nil
	inst2 := normalizeInstance(Instance{Service: "s", Endpoint: "e"})
	if inst2.Tags != nil || inst2.Metadata != nil {
		t.Fatalf("empty maps should be nil, got tags=%v metadata=%v", inst2.Tags, inst2.Metadata)
	}
}

func TestCloneInstances(t *testing.T) {
	if cloneInstances(nil) != nil {
		t.Fatal("clone nil should be nil")
	}
	if cloneInstances([]Instance{}) != nil {
		t.Fatal("clone empty should be nil")
	}
	in := []Instance{{Service: " users ", Endpoint: " http://127.0.0.1:8080/ "}}
	out := cloneInstances(in)
	if len(out) != 1 || out[0].Service != "users" {
		t.Fatalf("clone = %#v", out)
	}
}

func TestDiffInstancesClassifiesChanges(t *testing.T) {
	previous := []Instance{
		{Service: "orders", ID: "a", Endpoint: "http://a", Weight: 1},
		{Service: "orders", ID: "b", Endpoint: "http://b", Weight: 1},
		{Service: "orders", ID: "c", Endpoint: "http://c", Weight: 1},
	}
	current := []Instance{
		{Service: "orders", ID: "b", Endpoint: "http://b", Weight: 2},
		{Service: "orders", ID: "c", Endpoint: "http://c", Weight: 1},
		{Service: "orders", ID: "d", Endpoint: "http://d", Weight: 1},
	}
	changes := DiffInstances(previous, current)
	if len(changes.Added) != 1 || changes.Added[0].ID != "d" {
		t.Fatalf("added = %#v, want d", changes.Added)
	}
	if len(changes.Removed) != 1 || changes.Removed[0].ID != "a" {
		t.Fatalf("removed = %#v, want a", changes.Removed)
	}
	if len(changes.Updated) != 1 || changes.Updated[0].ID != "b" || changes.Updated[0].Weight != 2 {
		t.Fatalf("updated = %#v, want b weight 2", changes.Updated)
	}
	if len(changes.Unchanged) != 1 || changes.Unchanged[0].ID != "c" {
		t.Fatalf("unchanged = %#v, want c", changes.Unchanged)
	}
}

func TestBusPublishSubscribeAndClose(t *testing.T) {
	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, unsubscribe := bus.Subscribe(ctx, "orders", 1)
	defer unsubscribe()

	bus.Publish(Event{Type: EventAdded, Service: "orders", Instance: Instance{ID: "a", Endpoint: "http://a"}})
	select {
	case event := <-events:
		if event.Type != EventAdded || event.Instance.ID != "a" {
			t.Fatalf("event = %#v, want added a", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for bus event")
	}

	unsubscribe()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("events should close after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for unsubscribe close")
	}

	closedEvents, _ := bus.Subscribe(context.Background(), "orders", 0)
	bus.Close()
	select {
	case _, ok := <-closedEvents:
		if ok {
			t.Fatal("closed bus subscriber should be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for bus close")
	}
}

func TestInstanceMatches(t *testing.T) {
	inst := Instance{Service: "users", Endpoint: "e", Version: "v1", Zone: "az-a", Tags: map[string]string{"env": "prod"}}
	if !instanceMatches(inst, resolveOptions{}) {
		t.Fatal("empty options should match")
	}
	if instanceMatches(inst, resolveOptions{version: "v2"}) {
		t.Fatal("version mismatch should not match")
	}
	if instanceMatches(inst, resolveOptions{zone: "az-b"}) {
		t.Fatal("zone mismatch should not match")
	}
	if instanceMatches(inst, resolveOptions{tags: map[string]string{"env": "dev"}}) {
		t.Fatal("tag mismatch should not match")
	}
	if !instanceMatches(inst, resolveOptions{tags: map[string]string{"env": "prod"}}) {
		t.Fatal("tag match should match")
	}
	if instanceMatches(Instance{Status: StatusUnhealthy}, resolveOptions{}) {
		t.Fatal("unhealthy without includeUnhealthy should not match")
	}
	if !instanceMatches(Instance{Status: StatusUnhealthy}, resolveOptions{includeUnhealthy: true}) {
		t.Fatal("unhealthy with includeUnhealthy should match")
	}
}

func TestWithTTL(t *testing.T) {
	var o registerOptions
	WithTTL(5 * time.Second)(&o)
	if o.ttl != 5*time.Second {
		t.Fatalf("ttl = %v, want 5s", o.ttl)
	}
	WithTTL(-1 * time.Second)(&o)
	if o.ttl != 5*time.Second {
		t.Fatalf("ttl should not change with negative value, got %v", o.ttl)
	}
}

func TestMemoryRegistryNilGuardsAndContext(t *testing.T) {
	var nilR *MemoryRegistry
	if nilR.Snapshot() != nil {
		t.Fatal("nil Snapshot should be nil")
	}
	if nilR.Watchers("svc") != 0 {
		t.Fatal("nil Watchers should be 0")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := NewMemoryRegistry()
	if _, err := r.Register(ctx, Instance{Service: "s", Endpoint: "e"}); err == nil || ctx.Err() == nil {
		t.Fatalf("Register canceled context error = %v", err)
	}
	if err := r.Deregister(ctx, Instance{Service: "s", Endpoint: "e"}); err == nil || ctx.Err() == nil {
		t.Fatalf("Deregister canceled context error = %v", err)
	}
	if _, err := r.Resolve(ctx, "s"); err == nil || ctx.Err() == nil {
		t.Fatalf("Resolve canceled context error = %v", err)
	}
	if _, err := r.Watch(ctx, "s"); err == nil || ctx.Err() == nil {
		t.Fatalf("Watch canceled context error = %v", err)
	}
}

func TestMemoryRegistryResolveExpiredInstance(t *testing.T) {
	r := NewMemoryRegistry()
	_, err := r.Register(context.Background(), Instance{Service: "s", Endpoint: "e", ID: "i"}, WithTTL(time.Millisecond))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := r.Resolve(context.Background(), "s"); err != ErrNoInstances {
		t.Fatalf("resolve expired error = %v, want ErrNoInstances", err)
	}
}

func TestMemoryLeaseNilGuards(t *testing.T) {
	var nilL *memoryLease
	if err := nilL.KeepAlive(context.Background()); err != nil {
		t.Fatalf("nil KeepAlive = %v, want nil", err)
	}
	if err := nilL.Close(context.Background()); err != nil {
		t.Fatalf("nil Close = %v, want nil", err)
	}
	if got := nilL.Instance(); got.Service != "" || got.Endpoint != "" {
		t.Fatalf("nil Instance = %#v, want zero", got)
	}
	if got := nilL.ExpiresAt(); !got.IsZero() {
		t.Fatalf("nil ExpiresAt = %s, want zero", got)
	}
}

func TestMemoryLeaseExpiresAt(t *testing.T) {
	r := NewMemoryRegistry()
	inst := Instance{Service: "s", Endpoint: "e", ID: "i"}
	lease, err := r.Register(context.Background(), inst, WithTTL(time.Hour))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	ml := lease.(*memoryLease)
	if got := ml.ExpiresAt(); got.IsZero() {
		t.Fatal("ExpiresAt should be set")
	}

	// Deregister removes entry → ExpiresAt zero
	if err := ml.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := ml.ExpiresAt(); !got.IsZero() {
		t.Fatalf("ExpiresAt after deregister = %s, want zero", got)
	}
}

func TestMemoryRegistrySnapshotAndWatchers(t *testing.T) {
	r := NewMemoryRegistry()
	if _, err := r.Register(context.Background(), Instance{Service: "s", Endpoint: "e"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	snap := r.Snapshot()
	if len(snap) != 1 || len(snap["s"]) != 1 {
		t.Fatalf("snapshot = %v", snap)
	}
	if r.Watchers("s") != 0 {
		t.Fatalf("watchers before watch = %d", r.Watchers("s"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := r.Watch(ctx, "s")
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if r.Watchers("s") != 1 {
		t.Fatalf("watchers after watch = %d", r.Watchers("s"))
	}
	select {
	case ev := <-ch:
		if ev.Type != EventSnapshot || len(ev.Instances) != 1 {
			t.Fatalf("first event = %#v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for snapshot event")
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
	if r.Watchers("s") != 0 {
		t.Fatalf("watchers after cancel = %d", r.Watchers("s"))
	}
}

func TestMemoryRegistryWatchEventsIncludeChanges(t *testing.T) {
	r := NewMemoryRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := r.Watch(ctx, "orders")
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	<-ch

	if _, err := r.Register(context.Background(), Instance{Service: "orders", ID: "a", Endpoint: "http://a", Weight: 1}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	added := nextDiscoveryEvent(t, ch)
	if added.Type != EventRegistered || len(added.Changes.Added) != 1 || added.Changes.Added[0].ID != "a" {
		t.Fatalf("added event = %#v, want added a", added)
	}

	if _, err := r.Register(context.Background(), Instance{Service: "orders", ID: "a", Endpoint: "http://a", Weight: 2}); err != nil {
		t.Fatalf("update a: %v", err)
	}
	updated := nextDiscoveryEvent(t, ch)
	if updated.Type != EventRegistered || len(updated.Changes.Updated) != 1 || updated.Changes.Updated[0].Weight != 2 {
		t.Fatalf("updated event = %#v, want updated weight", updated)
	}

	if err := r.Deregister(context.Background(), Instance{Service: "orders", ID: "a", Endpoint: "http://a"}); err != nil {
		t.Fatalf("deregister a: %v", err)
	}
	removed := nextDiscoveryEvent(t, ch)
	if removed.Type != EventDeregister || len(removed.Changes.Removed) != 1 || removed.Changes.Removed[0].ID != "a" {
		t.Fatalf("removed event = %#v, want removed a", removed)
	}
}

func nextDiscoveryEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for discovery event")
	}
	return Event{}
}
