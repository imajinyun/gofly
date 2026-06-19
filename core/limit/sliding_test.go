package limit

import (
	"testing"
	"time"
)

func TestSlidingWindowLimiterEnforcesQuota(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewSlidingWindow(3, time.Second, 10)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !l.Allow() {
			t.Fatalf("request %d rejected, want allowed within quota", i)
		}
	}
	if l.Allow() {
		t.Fatal("4th request allowed, want rejected over quota")
	}
}

func TestSlidingWindowLimiterRecoversAfterWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewSlidingWindow(2, time.Second, 10)
	l.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if !l.Allow() {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if l.Allow() {
		t.Fatal("third request should be rejected")
	}
	// Advance past the whole window: old buckets expire.
	now = now.Add(1100 * time.Millisecond)
	if !l.Allow() {
		t.Fatal("request after window should be allowed again")
	}
}

func TestSlidingWindowLimiterAvoidsBoundaryBurst(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewSlidingWindow(10, time.Second, 10)
	l.now = func() time.Time { return now }

	// Fill quota near the end of the window.
	now = now.Add(900 * time.Millisecond)
	for i := 0; i < 10; i++ {
		if !l.Allow() {
			t.Fatalf("request %d rejected during fill", i)
		}
	}
	// Cross the fixed-window boundary but stay within a sliding window of the
	// previous events: a fixed-window counter would wrongly admit a full new
	// quota here, the sliding window must still reject.
	now = now.Add(150 * time.Millisecond)
	if l.Allow() {
		t.Fatal("request just after boundary allowed, want sliding-window rejection")
	}
}

func TestSlidingWindowLimiterAllowNRespectsRemaining(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewSlidingWindow(5, time.Second, 5)
	l.now = func() time.Time { return now }

	if !l.AllowN(3) {
		t.Fatal("AllowN(3) rejected, want allowed")
	}
	if l.AllowN(3) {
		t.Fatal("AllowN(3) allowed again, want rejected (only 2 remaining)")
	}
	if !l.AllowN(2) {
		t.Fatal("AllowN(2) rejected, want allowed (exactly 2 remaining)")
	}
	if got := l.Count(); got != 5 {
		t.Fatalf("Count = %d, want 5", got)
	}
}

func TestSlidingWindowLimiterNilAllows(t *testing.T) {
	var l *SlidingWindowLimiter
	if !l.Allow() {
		t.Fatal("nil limiter should allow")
	}
	if !l.AllowN(5) {
		t.Fatal("nil AllowN should allow")
	}
	if l.Count() != 0 {
		t.Fatalf("nil Count = %d, want 0", l.Count())
	}
}

func TestSlidingWindowLimiterEdgeCases(t *testing.T) {
	// zero quota clamped to 1
	l := NewSlidingWindow(0, time.Second, 10)
	if !l.Allow() {
		t.Fatal("zero-quota limiter should allow one")
	}
	if l.Allow() {
		t.Fatal("zero-quota limiter should reject second")
	}

	// zero window clamped to 1s
	l = NewSlidingWindow(10, 0, 10)
	if l.window != time.Second {
		t.Fatalf("window = %v, want 1s", l.window)
	}

	// zero buckets clamped to 10
	l = NewSlidingWindow(10, time.Second, 0)
	if len(l.buckets) != 10 {
		t.Fatalf("buckets = %d, want 10", len(l.buckets))
	}

	// tiny window causing bucketSize <= 0
	l = NewSlidingWindow(1, time.Nanosecond, 10)
	if l.bucketSize <= 0 {
		t.Fatal("bucketSize should be > 0 after clamping")
	}

	// AllowN with n <= 0 treated as 1
	now := time.Unix(0, 0)
	l = NewSlidingWindow(5, time.Second, 5)
	l.now = func() time.Time { return now }
	if !l.AllowN(0) {
		t.Fatal("AllowN(0) should be treated as AllowN(1)")
	}
	if !l.AllowN(-3) {
		t.Fatal("AllowN(-3) should be treated as AllowN(1)")
	}
}
