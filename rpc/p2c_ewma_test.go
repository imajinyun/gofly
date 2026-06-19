package rpc

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"
)

func TestP2CEWMABalancerImplementsInterfaces(t *testing.T) {
	var _ Balancer = (*P2CEWMABalancer)(nil)
	var _ EndpointReporter = (*P2CEWMABalancer)(nil)
}

func TestP2CEWMABalancerSingleEndpoint(t *testing.T) {
	b := NewP2CEWMABalancer()
	got, err := b.Pick(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "a" {
		t.Fatalf("pick = %q, want a", got)
	}
}

func TestP2CEWMABalancerPrefersLowerLatency(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewP2CEWMABalancer()
	b.now = func() time.Time { return now }
	b.rand = rand.New(rand.NewSource(1))
	endpoints := []string{"fast", "slow"}

	// Train EWMA: "slow" returns with high latency, "fast" with low latency.
	for i := 0; i < 50; i++ {
		b.markPickLocked("slow", now)
		now = now.Add(200 * time.Millisecond)
		b.Report(context.Background(), "slow", nil)

		b.markPickLocked("fast", now)
		now = now.Add(1 * time.Millisecond)
		b.Report(context.Background(), "fast", nil)
	}

	counts := map[string]int{}
	for i := 0; i < 200; i++ {
		got, err := b.Pick(context.Background(), endpoints)
		if err != nil {
			t.Fatal(err)
		}
		counts[got]++
		// Immediately report success with low latency to keep inflight bounded.
		now = now.Add(time.Millisecond)
		b.Report(context.Background(), got, nil)
	}
	if counts["fast"] <= counts["slow"] {
		t.Fatalf("counts = %#v, want fast >> slow", counts)
	}
}

func TestP2CEWMABalancerProbesUnmeasured(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewP2CEWMABalancer()
	b.now = func() time.Time { return now }
	b.rand = rand.New(rand.NewSource(2))
	endpoints := []string{"a", "b"}

	// With no measurements both have load 0; over many picks both get traffic.
	counts := map[string]int{}
	for i := 0; i < 100; i++ {
		got, err := b.Pick(context.Background(), endpoints)
		if err != nil {
			t.Fatal(err)
		}
		counts[got]++
		now = now.Add(time.Millisecond)
		b.Report(context.Background(), got, nil)
	}
	if counts["a"] == 0 || counts["b"] == 0 {
		t.Fatalf("counts = %#v, want both endpoints probed", counts)
	}
}

func TestP2CEWMABalancerReportDecrementsInflight(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewP2CEWMABalancer()
	b.now = func() time.Time { return now }

	if _, err := b.Pick(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	inflight := b.endpoints["a"].inflight
	b.mu.Unlock()
	if inflight != 1 {
		t.Fatalf("inflight = %d, want 1 after pick", inflight)
	}

	now = now.Add(5 * time.Millisecond)
	b.Report(context.Background(), "a", errors.New("boom"))
	b.mu.Lock()
	inflight = b.endpoints["a"].inflight
	b.mu.Unlock()
	if inflight != 0 {
		t.Fatalf("inflight = %d, want 0 after report", inflight)
	}
}

func TestP2CEWMABalancerEmptyEndpoints(t *testing.T) {
	b := NewP2CEWMABalancer()
	if _, err := b.Pick(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty endpoints")
	}
}
