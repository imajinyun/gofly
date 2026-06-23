package runtime

import (
	"context"
	"testing"
)

func TestRegistrySnapshotSortsAndSurvivesPanic(t *testing.T) {
	reg := NewRegistry()
	reg.Register("z", "server", func(context.Context) ComponentSnapshot {
		return ComponentSnapshot{Name: "z", Kind: "server", Status: "ok"}
	}, WithOwner("rest"), WithTarget("b"))
	reg.Register("panic", "server", func(context.Context) ComponentSnapshot {
		panic("boom")
	}, WithOwner("rest"), WithTarget("a"))
	reg.Register("client", "client", func(context.Context) ComponentSnapshot {
		return ComponentSnapshot{
			Name:   "client",
			Kind:   "client",
			Owner:  "rpc",
			Target: "orders",
			Middleware: &MiddlewareSnapshot{Unary: []MiddlewareLayer{{
				Name:   "retry",
				Source: "policy",
				Order:  0,
			}}},
		}
	})

	snapshot := reg.Snapshot(context.Background())
	if len(snapshot.Components) != 3 {
		t.Fatalf("components = %d, want 3", len(snapshot.Components))
	}
	if snapshot.Components[0].Name != "panic" || snapshot.Components[0].Owner != "rest" {
		t.Fatalf("first component = %#v, want rest panic sorted first", snapshot.Components[0])
	}
	if snapshot.Components[0].Status != "error" || snapshot.Components[0].Error != "boom" {
		t.Fatalf("panic component = %#v, want captured error", snapshot.Components[0])
	}
	if snapshot.Components[2].Name != "client" || snapshot.Components[2].Owner != "rpc" {
		t.Fatalf("last component = %#v, want rpc client sorted last", snapshot.Components[2])
	}
	if got := snapshot.Components[2].Middleware.Unary[0].Name; got != "retry" {
		t.Fatalf("middleware name = %q, want retry", got)
	}
}

func TestNilRegistrySnapshotIsSafe(t *testing.T) {
	var reg *Registry
	snapshot := reg.Snapshot(nil)
	if len(snapshot.Components) != 0 || snapshot.GeneratedAt.IsZero() {
		t.Fatalf("nil snapshot = %#v, want generated empty snapshot", snapshot)
	}
}
