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

func TestDoPolicyBoundaryCases(t *testing.T) {
	boom := errors.New("boom")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	if err := Do(ctx, Policy{Attempts: 3}, func() error {
		calls++
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Do error = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("canceled Do calls = %d, want 0", calls)
	}

	calls = 0
	if err := Do(context.Background(), Policy{Attempts: 0}, func() error {
		calls++
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("default attempts error = %v, want boom", err)
	}
	if calls != 1 {
		t.Fatalf("default attempts calls = %d, want 1", calls)
	}

	calls = 0
	if err := Do(context.Background(), Policy{Attempts: 5, Budget: MaxRetriesBudget{MaxRetries: -1}}, func() error {
		calls++
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("negative budget error = %v, want boom", err)
	}
	if calls != 1 {
		t.Fatalf("negative budget calls = %d, want 1", calls)
	}

	calls = 0
	if err := Do(context.Background(), Policy{Attempts: 5}, func() error {
		calls++
		return nil
	}); err != nil || calls != 1 {
		t.Fatalf("success Do err=%v calls=%d, want nil/1", err, calls)
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

func TestBackoffHelpersBoundaries(t *testing.T) {
	if got := FixedBackoff(7 * time.Millisecond)(99); got != 7*time.Millisecond {
		t.Fatalf("FixedBackoff = %v, want 7ms", got)
	}
	if got := ExponentialBackoff(time.Millisecond, 0)(63); got != 0 {
		t.Fatalf("ExponentialBackoff overflow without cap = %v, want 0", got)
	}
	if got := ExponentialBackoff(time.Millisecond, 2*time.Millisecond)(4); got != 2*time.Millisecond {
		t.Fatalf("ExponentialBackoff cap = %v, want 2ms", got)
	}
	if got := JitterBackoff(FixedBackoff(time.Millisecond), -0.1)(1); got != time.Millisecond {
		t.Fatalf("negative jitter = %v, want original delay", got)
	}
	for i := 0; i < 20; i++ {
		if got := JitterBackoff(FixedBackoff(time.Millisecond), 2)(1); got < 0 || got > 2*time.Millisecond {
			t.Fatalf("clamped jitter = %v, want [0,2ms]", got)
		}
	}
	if got := nextDelay(func(attempt int) time.Duration { return time.Duration(attempt) * time.Millisecond }, time.Hour, 3); got != 3*time.Millisecond {
		t.Fatalf("nextDelay func = %v, want 3ms", got)
	}
	if got := nextDelay(nil, 4*time.Millisecond, 3); got != 4*time.Millisecond {
		t.Fatalf("nextDelay fallback = %v, want 4ms", got)
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
