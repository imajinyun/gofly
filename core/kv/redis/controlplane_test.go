package redis

import (
	"context"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/core/controlplane"
)

func TestControlPlaneContributorAddsSanitizedDiagnostics(t *testing.T) {
	client := New(Config{Addr: "redis.internal:6379", Password: "redis-password"})
	defer client.Close()
	snapshot := controlplane.Snapshot{}
	contributor := ControlPlaneContributor{Client: client, Name: "cache"}
	if err := contributor.ContributeSnapshot(context.Background(), &snapshot); err != nil {
		t.Fatalf("ContributeSnapshot: %v", err)
	}
	data := string(snapshot.Configs["runtime.redis.cache"])
	if !strings.Contains(data, "\"topology\"") {
		t.Fatalf("diagnostics config = %s", data)
	}
	for _, secret := range []string{"redis.internal:6379", "redis-password"} {
		if strings.Contains(data, secret) {
			t.Fatalf("diagnostics leaked %q: %s", secret, data)
		}
	}
}

func TestControlPlaneContributorNilBoundaries(t *testing.T) {
	if err := (ControlPlaneContributor{}).ContributeSnapshot(context.Background(), nil); err != nil {
		t.Fatalf("nil contributor: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (ControlPlaneContributor{}).ContributeSnapshot(ctx, &controlplane.Snapshot{}); err != context.Canceled {
		t.Fatalf("canceled contributor error = %v, want context.Canceled", err)
	}
}
