package limit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConcurrencyLimiterTryAcquire(t *testing.T) {
	limiter := NewConcurrency(1)
	if !limiter.TryAcquire() {
		t.Fatal("first acquire = false, want true")
	}
	if limiter.TryAcquire() {
		t.Fatal("second acquire = true, want false")
	}
	if got := limiter.InFlight(); got != 1 {
		t.Fatalf("in flight = %d, want 1", got)
	}
	limiter.Release()
	if !limiter.TryAcquire() {
		t.Fatal("acquire after release = false, want true")
	}
	limiter.Release()
}

func TestConcurrencyLimiterAcquireHonorsContext(t *testing.T) {
	limiter := NewConcurrency(1)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := limiter.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire error = %v, want deadline exceeded", err)
	}
	limiter.Release()
}

func TestConcurrencyLimiterNilGuard(t *testing.T) {
	var l *ConcurrencyLimiter
	if !l.TryAcquire() {
		t.Fatal("nil TryAcquire should return true")
	}
	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("nil Acquire should return nil, got %v", err)
	}
	l.Release() // should not panic
	if l.InFlight() != 0 {
		t.Fatalf("nil InFlight = %d, want 0", l.InFlight())
	}
}

func TestConcurrencyLimiterZeroMax(t *testing.T) {
	l := NewConcurrency(0)
	if !l.TryAcquire() {
		t.Fatal("zero-max limiter should allow one")
	}
	if l.TryAcquire() {
		t.Fatal("zero-max limiter should reject second")
	}
}
