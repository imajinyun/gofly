package retry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoHonorsRetryBudget(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	err := Do(context.Background(), Policy{
		Attempts:    5,
		Budget:      MaxRetriesBudget{MaxRetries: 1},
		ShouldRetry: func(error) bool { return true },
	}, func() error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestDoStopsWhenShouldRetryRejectsError(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	err := Do(context.Background(), Policy{
		Attempts:    5,
		ShouldRetry: func(error) bool { return false },
	}, func() error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDoReturnsContextErrorDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int64
	errCh := make(chan error, 1)
	go func() {
		errCh <- Do(ctx, Policy{Attempts: 3, Backoff: time.Hour}, func() error {
			calls.Add(1)
			return errors.New("temporary")
		})
	}()
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Do did not return after context cancellation")
	}
}

func TestExponentialBackoff(t *testing.T) {
	backoff := ExponentialBackoff(time.Millisecond, 5*time.Millisecond)
	if got := backoff(1); got != time.Millisecond {
		t.Fatalf("attempt 1 = %v, want 1ms", got)
	}
	if got := backoff(3); got != 4*time.Millisecond {
		t.Fatalf("attempt 3 = %v, want 4ms", got)
	}
	if got := backoff(10); got != 5*time.Millisecond {
		t.Fatalf("attempt 10 = %v, want capped 5ms", got)
	}
}

func TestBackoffBoundaryCases(t *testing.T) {
	if got := ExponentialBackoff(0, time.Second)(3); got != 0 {
		t.Fatalf("ExponentialBackoff zero base = %v, want 0", got)
	}
	if got := ExponentialBackoff(time.Millisecond, 0)(0); got != time.Millisecond {
		t.Fatalf("ExponentialBackoff attempt 0 = %v, want 1ms", got)
	}
	if got := JitterBackoff(nil, 0.5)(1); got != 0 {
		t.Fatalf("JitterBackoff nil next = %v, want 0", got)
	}
}

func TestJitterBackoffBounds(t *testing.T) {
	backoff := JitterBackoff(FixedBackoff(100*time.Millisecond), 0.5)
	for i := 0; i < 20; i++ {
		d := backoff(1)
		if d < 50*time.Millisecond || d > 150*time.Millisecond {
			t.Fatalf("jittered backoff = %v, want within [50ms,150ms]", d)
		}
	}
}
