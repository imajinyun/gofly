package rpc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConsistentHashBalancerStableByContextKey(t *testing.T) {
	balancer := NewConsistentHashBalancer(WithConsistentHashReplicas(16))
	ctx := ContextWithHashKey(context.Background(), "tenant-a")
	endpoints := []string{"http://127.0.0.1:8081", "http://127.0.0.1:8082", "http://127.0.0.1:8083"}

	first, err := balancer.Pick(ctx, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := balancer.Pick(ctx, endpoints)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("pick %d = %q, want stable endpoint %q", i, got, first)
		}
	}
}

func TestConsistentHashBalancerFallsBackToRoundRobinWithoutKey(t *testing.T) {
	balancer := NewConsistentHashBalancer()
	endpoints := []string{"a", "b"}
	first, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	second, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("fallback picks = %q/%q, want round-robin rotation", first, second)
	}
}

func TestWeightedRoundRobinBalancerHonorsWeights(t *testing.T) {
	balancer := NewWeightedRoundRobinBalancer(map[string]int{"a": 3, "b": 1})
	counts := map[string]int{}
	for i := 0; i < 8; i++ {
		got, err := balancer.Pick(context.Background(), []string{"a", "b"})
		if err != nil {
			t.Fatal(err)
		}
		counts[got]++
	}
	if counts["a"] != 6 || counts["b"] != 2 {
		t.Fatalf("counts = %#v, want a:b = 6:2", counts)
	}
}

func TestHealthBalancerPickBoundaries(t *testing.T) {
	b := NewHealthBalancer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Pick(ctx, []string{"a"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Pick canceled error = %v, want context.Canceled", err)
	}
	if _, err := b.Pick(context.Background(), nil); err == nil {
		t.Fatal("Pick empty endpoints: want error")
	}
}

func TestP2CBalancerPickBoundaries(t *testing.T) {
	b := NewP2CBalancer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Pick(ctx, []string{"a"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Pick canceled error = %v, want context.Canceled", err)
	}
	if _, err := b.Pick(context.Background(), nil); err == nil {
		t.Fatal("Pick empty endpoints: want error")
	}
}

func TestHashKeyFromContextNil(t *testing.T) {
	var nilCtx context.Context
	if got := HashKeyFromContext(nilCtx); got != "" {
		t.Fatalf("HashKeyFromContext(nil) = %q, want empty", got)
	}
}

func TestConsistentHashBalancerFixedKeyAndNilContext(t *testing.T) {
	ctx := ContextWithHashKey(nil, "tenant-a")
	if got := HashKeyFromContext(ctx); got != "tenant-a" {
		t.Fatalf("hash key = %q, want tenant-a", got)
	}

	balancer := NewConsistentHashBalancer(WithConsistentHashReplicas(-1), WithConsistentHashKey("tenant-fixed"))
	endpoints := []string{" http://a/ ", "http://b", "http://c"}
	first, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		got, err := balancer.Pick(context.Background(), endpoints)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("pick %d = %q, want fixed-key endpoint %q", i, got, first)
		}
	}
}

func TestBalancerNormalizationHelpers(t *testing.T) {
	endpoints := normalizeEndpoints([]string{" http://a/ ", "http://a", "", " http://b// "})
	if len(endpoints) != 2 || endpoints[0] != "http://a" || endpoints[1] != "http://b" {
		t.Fatalf("endpoints = %#v, want trimmed unique endpoints", endpoints)
	}
	weights := normalizeWeights(map[string]int{" http://a/ ": 2, "http://b": 0, " ": 10})
	if len(weights) != 1 || weights["http://a"] != 2 {
		t.Fatalf("weights = %#v, want only positive normalized endpoint weight", weights)
	}
}

func TestHealthBalancerRecoversEjectedEndpointAfterDuration(t *testing.T) {
	b := NewHealthBalancer(WithHealthFailureThreshold(1), WithHealthEjectionDuration(time.Hour))
	endpoints := []string{"bad", "good"}
	b.Report(context.Background(), "bad", NewError(CodeUnavailable, "down"))
	if got, err := b.Pick(context.Background(), endpoints); err != nil || got != "good" {
		t.Fatalf("pick while ejected = %q/%v, want good", got, err)
	}
	b.mu.Lock()
	b.endpoints["bad"].ejectedAt = time.Now().Add(-time.Hour)
	b.mu.Unlock()
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		got, err := b.Pick(context.Background(), endpoints)
		if err != nil {
			t.Fatal(err)
		}
		seen[got] = true
	}
	if !seen["bad"] {
		t.Fatalf("seen endpoints after recovery = %#v, want bad endpoint recovered", seen)
	}
}
