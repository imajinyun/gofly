package breaker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreakerStateTransitions(t *testing.T) {
	brk := New(WithFailureThreshold(2), WithOpenTimeout(time.Millisecond))
	errFail := errors.New("fail")
	_ = brk.Do(context.Background(), func() error { return errFail })
	if brk.State() != Closed {
		t.Fatalf("state after first failure = %v, want Closed", brk.State())
	}
	_ = brk.Do(context.Background(), func() error { return errFail })
	if brk.State() != Open {
		t.Fatalf("state after threshold = %v, want Open", brk.State())
	}
	time.Sleep(2 * time.Millisecond)
	if brk.State() != HalfOpen {
		t.Fatalf("state after open timeout = %v, want HalfOpen", brk.State())
	}
	if err := brk.Do(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if brk.State() != Closed {
		t.Fatalf("state after success = %v, want Closed", brk.State())
	}
}

func TestAdaptiveBreakerOpensAndRecovers(t *testing.T) {
	brk := NewAdaptive(
		WithAdaptiveMinRequests(2),
		WithAdaptiveFailureRatio(0.5),
		WithAdaptiveK(1),
		WithAdaptiveOpenTimeout(time.Millisecond),
	)
	errFail := errors.New("fail")
	_ = brk.Do(context.Background(), func() error { return errFail })
	_ = brk.Do(context.Background(), func() error { return errFail })
	if brk.State() != Open {
		t.Fatalf("state after adaptive threshold = %v, want Open", brk.State())
	}
	if err := brk.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("allow while open = %v, want ErrOpen", err)
	}
	time.Sleep(2 * time.Millisecond)
	if brk.State() != HalfOpen {
		t.Fatalf("state after open timeout = %v, want HalfOpen", brk.State())
	}
	if err := brk.Do(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if brk.State() != Closed {
		t.Fatalf("state after half-open success = %v, want Closed", brk.State())
	}
}

func TestAdaptiveBreakerSnapshot(t *testing.T) {
	brk := NewAdaptive(
		WithAdaptiveMinRequests(1),
		WithAdaptiveFailureRatio(0.1),
		WithAdaptiveK(1),
	)
	brk.MarkFailure()
	snapshot := brk.Snapshot()
	if snapshot.State != Open.String() {
		t.Fatalf("state = %q, want open", snapshot.State)
	}
	if snapshot.Requests != 1 || snapshot.Failures != 1 || snapshot.ErrorRatio != 1 {
		t.Fatalf("snapshot = %#v, want one failed request", snapshot)
	}
}

func TestBreakerSnapshot(t *testing.T) {
	brk := New(WithFailureThreshold(3), WithOpenTimeout(time.Second))
	// nil safety
	var nilBrk *Breaker
	if s := nilBrk.Snapshot(); s != (BreakerSnapshot{}) {
		t.Fatalf("nil snapshot = %#v, want zero", s)
	}

	// closed state snapshot
	snap := brk.Snapshot()
	if snap.State != Closed.String() {
		t.Fatalf("closed state = %q, want closed", snap.State)
	}
	if snap.Failures != 0 || snap.FailureThreshold != 3 || snap.OpenTimeout != time.Second {
		t.Fatalf("closed snapshot unexpected: %#v", snap)
	}

	// open state snapshot
	_ = brk.Do(context.Background(), func() error { return errors.New("boom") })
	_ = brk.Do(context.Background(), func() error { return errors.New("boom") })
	_ = brk.Do(context.Background(), func() error { return errors.New("boom") })
	snap = brk.Snapshot()
	if snap.State != Open.String() {
		t.Fatalf("open state = %q, want open", snap.State)
	}
	if snap.Failures != 3 {
		t.Fatalf("open failures = %d, want 3", snap.Failures)
	}
	if snap.OpenedAt.IsZero() {
		t.Fatal("openedAt should be set")
	}
}

func TestAdaptiveBreakerOptions(t *testing.T) {
	// Verify WithAdaptiveWindow and WithAdaptiveBuckets are applied.
	brk := NewAdaptive(
		WithAdaptiveWindow(5*time.Second),
		WithAdaptiveBuckets(5),
	)
	if brk.window != 5*time.Second {
		t.Fatalf("window = %v, want 5s", brk.window)
	}
	if len(brk.buckets) != 5 {
		t.Fatalf("buckets = %d, want 5", len(brk.buckets))
	}
	if brk.bucketSize != time.Second {
		t.Fatalf("bucketSize = %v, want 1s", brk.bucketSize)
	}
}

func TestGoogleBreakerMarkFailure(t *testing.T) {
	// MarkFailure is a no-op but should be callable without panic.
	b := NewGoogle()
	b.MarkFailure()
	b.MarkFailure()
	// No state change expected since MarkFailure does nothing.
	if b.RejectProbability() != 0 {
		t.Fatalf("reject probability should remain 0, got %f", b.RejectProbability())
	}
}
