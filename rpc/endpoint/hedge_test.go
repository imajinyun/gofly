package endpoint

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestHedgingMiddlewareLaunchesBackupAndReturnsFastSuccess(t *testing.T) {
	var calls atomic.Int64
	hedger := NewHedger(HedgeConfig{Delay: 5 * time.Millisecond, MaxHedges: 1})
	ep := hedger.Middleware()(func(ctx context.Context, req any) (any, error) {
		if calls.Add(1) == 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return "slow", nil
			}
		}
		return "fast", nil
	})
	got, err := ep(context.Background(), "req")
	if err != nil {
		t.Fatalf("endpoint returned error: %v", err)
	}
	if got != "fast" {
		t.Fatalf("response = %v, want fast", got)
	}
	snapshot := hedger.Snapshot()
	if snapshot.Primary != 1 || snapshot.Hedges != 1 || snapshot.Wins != 1 {
		t.Fatalf("unexpected hedge snapshot: %+v", snapshot)
	}
}

func TestHedgingMiddlewareSkipsBackupWhenPrimaryFast(t *testing.T) {
	var calls atomic.Int64
	hedger := NewHedger(HedgeConfig{Delay: time.Hour, MaxHedges: 1})
	ep := hedger.Middleware()(func(ctx context.Context, req any) (any, error) {
		calls.Add(1)
		return "ok", nil
	})
	got, err := ep(context.Background(), "req")
	if err != nil || got != "ok" {
		t.Fatalf("response = %v err=%v, want ok nil", got, err)
	}
	if calls.Load() != 1 || hedger.Snapshot().Hedges != 0 {
		t.Fatalf("calls=%d snapshot=%+v, want no hedge", calls.Load(), hedger.Snapshot())
	}
}

func TestHedgingMiddlewareTriesHedgeAfterPrimaryError(t *testing.T) {
	var calls atomic.Int64
	hedger := NewHedger(HedgeConfig{Delay: time.Hour, MaxHedges: 1})
	ep := hedger.Middleware()(func(ctx context.Context, req any) (any, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("primary failed")
		}
		return "hedge", nil
	})
	got, err := ep(context.Background(), "req")
	if err != nil || got != "hedge" {
		t.Fatalf("response = %v err=%v, want hedge nil", got, err)
	}
	if snapshot := hedger.Snapshot(); snapshot.Errors != 1 || snapshot.Hedges != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestHedgingMiddlewareContextCancellation(t *testing.T) {
	hedger := NewHedger(HedgeConfig{Delay: 5 * time.Millisecond, MaxHedges: 1})
	ep := hedger.Middleware()(func(ctx context.Context, req any) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := ep(ctx, "req"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}
}

func TestHedgingMiddlewareFunctionUsesCloneForBackup(t *testing.T) {
	type request struct {
		Value string
	}

	seen := make(chan string, 2)
	var calls atomic.Int64
	ep := HedgingMiddleware(HedgeConfig{
		Delay:     time.Hour,
		MaxHedges: 1,
		Clone: func(req any) any {
			cloned := *req.(*request)
			cloned.Value = "cloned"
			return &cloned
		},
	})(func(ctx context.Context, req any) (any, error) {
		seen <- req.(*request).Value
		if calls.Add(1) == 1 {
			return nil, errors.New("primary failed")
		}
		return req.(*request).Value, nil
	})

	got, err := ep(context.Background(), &request{Value: "original"})
	if err != nil || got != "cloned" {
		t.Fatalf("response = %v err=%v, want cloned nil", got, err)
	}
	if first, second := <-seen, <-seen; first != "original" || second != "cloned" {
		t.Fatalf("seen requests = [%q %q], want [original cloned]", first, second)
	}
}

func TestHedgingMiddlewareReturnsLastErrorWhenAllAttemptsFail(t *testing.T) {
	firstErr := errors.New("first failed")
	secondErr := errors.New("second failed")
	var calls atomic.Int64
	ep := HedgingMiddleware(HedgeConfig{Delay: time.Hour, MaxHedges: 1})(func(ctx context.Context, req any) (any, error) {
		if calls.Add(1) == 1 {
			return nil, firstErr
		}
		return nil, secondErr
	})

	got, err := ep(context.Background(), "req")
	if got != nil || !errors.Is(err, secondErr) {
		t.Fatalf("response = %v err=%v, want nil secondErr", got, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want primary plus one hedge", calls.Load())
	}
}
