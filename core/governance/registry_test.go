package governance

import "testing"

func TestRegistrySnapshotsSortedAndDefensive(t *testing.T) {
	r := NewRegistry()
	r.Register("b", "limiter", "route-b", func() any { return "b" })
	r.Register("a", "breaker", "route-a", func() any { return "a" })
	r.Register("c", "breaker", "route-c", func() any { return "c" })
	r.Register("", "ignored", "", func() any { return "ignored" })

	snapshots := r.Snapshots()
	if len(snapshots) != 3 {
		t.Fatalf("snapshots len = %d, want 3", len(snapshots))
	}
	if snapshots[0].Kind != "breaker" || snapshots[0].Target != "route-a" || snapshots[0].Snapshot != "a" {
		t.Fatalf("first snapshot = %#v, want breaker route-a", snapshots[0])
	}
	if snapshots[2].Kind != "limiter" || snapshots[2].Snapshot != "b" {
		t.Fatalf("last snapshot = %#v, want limiter b", snapshots[2])
	}
}

func TestNilRegistrySafe(t *testing.T) {
	var r *Registry
	r.Register("name", "kind", "target", func() any { return nil })
	if got := r.Snapshots(); got != nil {
		t.Fatalf("nil registry snapshots = %#v, want nil", got)
	}
}
