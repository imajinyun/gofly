package schedule

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerRunsAndStops(t *testing.T) {
	s := New()
	var runs atomic.Int64
	if err := s.Add(Job{
		Name:       "heartbeat",
		Interval:   10 * time.Millisecond,
		RunOnStart: true,
		Handler: func(ctx context.Context) error {
			runs.Add(1)
			return nil
		},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool { return runs.Load() >= 2 })
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	snapshot := s.Snapshot()
	if snapshot.Started {
		t.Fatal("expected scheduler to be stopped")
	}
	if got := snapshot.Jobs["heartbeat"].Runs; got < 2 {
		t.Fatalf("heartbeat runs = %d, want >= 2", got)
	}
	afterStop := runs.Load()
	time.Sleep(30 * time.Millisecond)
	if got := runs.Load(); got != afterStop {
		t.Fatalf("runs after stop = %d, want %d", got, afterStop)
	}
}

func TestSchedulerNoOverlapSkips(t *testing.T) {
	s := New()
	var maxConcurrent atomic.Int64
	var running atomic.Int64
	if err := s.Add(Job{
		Name:      "slow",
		Interval:  5 * time.Millisecond,
		NoOverlap: true,
		Handler: func(ctx context.Context) error {
			current := running.Add(1)
			defer running.Add(-1)
			if current > maxConcurrent.Load() {
				maxConcurrent.Store(current)
			}
			time.Sleep(30 * time.Millisecond)
			return nil
		},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool { return s.Snapshot().Jobs["slow"].Skipped > 0 })
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := maxConcurrent.Load(); got > 1 {
		t.Fatalf("max concurrent runs = %d, want <= 1", got)
	}
}

func TestSchedulerTimeoutErrorAndPanicAccounting(t *testing.T) {
	s := New()
	if err := s.Add(Job{
		Name:       "timeout",
		Interval:   time.Hour,
		RunOnStart: true,
		Timeout:    10 * time.Millisecond,
		Handler: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}); err != nil {
		t.Fatalf("Add timeout: %v", err)
	}
	if err := s.Add(Job{
		Name:       "panic",
		Interval:   time.Hour,
		RunOnStart: true,
		Handler: func(ctx context.Context) error {
			panic("boom")
		},
	}); err != nil {
		t.Fatalf("Add panic: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		jobs := s.Snapshot().Jobs
		return jobs["timeout"].Errors == 1 && jobs["panic"].Panics == 1
	})
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	jobs := s.Snapshot().Jobs
	if jobs["timeout"].LastError == "" {
		t.Fatalf("missing timeout error snapshot: %+v", jobs["timeout"])
	}
	if jobs["panic"].LastError == "" {
		t.Fatalf("missing panic error snapshot: %+v", jobs["panic"])
	}
}

func TestSchedulerValidationAndRemove(t *testing.T) {
	s := New()
	if err := s.Add(Job{}); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("Add invalid = %v, want ErrInvalidJob", err)
	}
	job := Job{Name: "once", Interval: time.Second, Handler: func(ctx context.Context) error { return nil }}
	if err := s.Add(job); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(job); !errors.Is(err, ErrJobExists) {
		t.Fatalf("Add duplicate = %v, want ErrJobExists", err)
	}
	if err := s.Remove("once"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s.Remove("once"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("Remove missing = %v, want ErrJobNotFound", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}
