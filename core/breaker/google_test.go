package breaker

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"
)

func TestGoogleBreakerAllowsWhenHealthy(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewGoogle()
	b.now = func() time.Time { return now }

	for i := 0; i < 100; i++ {
		if err := b.Do(context.Background(), func() error { return nil }); err != nil {
			t.Fatalf("healthy request %d rejected: %v", i, err)
		}
	}
	if p := b.RejectProbability(); p != 0 {
		t.Fatalf("reject probability = %f, want 0 when fully healthy", p)
	}
}

func TestGoogleBreakerRejectProbabilityRisesWithFailures(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewGoogle(WithGoogleK(2))
	b.now = func() time.Time { return now }
	// Force all requests through Allow regardless of probability so we can
	// drive the request/accept counters deterministically.
	b.rand = rand.New(rand.NewSource(1))

	failing := errors.New("boom")
	for i := 0; i < 200; i++ {
		_ = b.Do(context.Background(), func() error { return failing })
	}
	// With many requests and zero accepts, prob -> requests/(requests+1) ~ 1.
	if p := b.RejectProbability(); p < 0.9 {
		t.Fatalf("reject probability = %f, want >= 0.9 after sustained failures", p)
	}
}

func TestGoogleBreakerRejectProbabilityMath(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewGoogle(WithGoogleK(2))
	b.now = func() time.Time { return now }
	b.rand = rand.New(rand.NewSource(1))

	// 10 requests, 4 accepts: prob = (10 - 2*4)/(10+1) = 2/11.
	for i := 0; i < 10; i++ {
		b.currentBucketLocked(now).requests++
	}
	for i := 0; i < 4; i++ {
		b.currentBucketLocked(now).accepts++
	}
	got := b.RejectProbability()
	want := 2.0 / 11.0
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("reject probability = %f, want %f", got, want)
	}
}

func TestGoogleBreakerHighAcceptsNeverRejects(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewGoogle(WithGoogleK(2))
	b.now = func() time.Time { return now }

	// requests == accepts: prob = (R - 2R)/(R+1) < 0 -> clamped to 0.
	for i := 0; i < 50; i++ {
		b.currentBucketLocked(now).requests++
		b.currentBucketLocked(now).accepts++
	}
	for i := 0; i < 50; i++ {
		if err := b.Allow(); err != nil {
			t.Fatalf("request %d rejected with high accept ratio: %v", i, err)
		}
	}
}

func TestGoogleBreakerRecoversAfterWindow(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewGoogle(WithGoogleWindow(time.Second), WithGoogleBuckets(10))
	b.now = func() time.Time { return now }
	b.rand = rand.New(rand.NewSource(1))

	failing := errors.New("boom")
	for i := 0; i < 100; i++ {
		_ = b.Do(context.Background(), func() error { return failing })
	}
	if p := b.RejectProbability(); p <= 0 {
		t.Fatal("expected positive reject probability during failures")
	}
	// Advance past the window so all failure buckets expire.
	now = now.Add(2 * time.Second)
	if p := b.RejectProbability(); p != 0 {
		t.Fatalf("reject probability = %f, want 0 after window reset", p)
	}
}

func TestGoogleBreakerDoCallsMarkFailure(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewGoogle()
	b.now = func() time.Time { return now }
	// Force Allow to always accept so we can reach MarkFailure deterministically.
	b.rand = rand.New(rand.NewSource(1))

	// Pre-fill requests==accepts so probability stays 0 even after one failure.
	for i := 0; i < 50; i++ {
		b.currentBucketLocked(now).requests++
		b.currentBucketLocked(now).accepts++
	}

	// Allow() returns nil so Do proceeds to fn; fn errors -> MarkFailure called.
	failing := errors.New("boom")
	err := b.Do(context.Background(), func() error { return failing })
	if !errors.Is(err, failing) {
		t.Fatalf("expected failing error, got %v", err)
	}
	// MarkFailure is a no-op, so probability should still be 0.
	if p := b.RejectProbability(); p != 0 {
		t.Fatalf("reject probability = %f, want 0 after MarkFailure no-op", p)
	}
}

func TestGoogleBreakerMarkFailureIsCompatibilityNoop(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewGoogle(WithGoogleK(2))
	b.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		b.currentBucketLocked(now).requests++
		b.currentBucketLocked(now).accepts++
	}
	before := b.RejectProbability()
	b.MarkFailure()
	b.MarkFailure()
	if after := b.RejectProbability(); after != before {
		t.Fatalf("RejectProbability after explicit MarkFailure = %f, want unchanged %f", after, before)
	}
}
