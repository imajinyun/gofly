package syncx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupDoDeduplicatesConcurrentCalls(t *testing.T) {
	var g Group[int]
	var calls atomic.Int64
	start := make(chan struct{})
	entered := make(chan struct{})
	const workers = 2
	results := make(chan int, workers)
	shared := make(chan bool, workers)
	var wg sync.WaitGroup
	runWorker := func(worker int) {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			value, wasShared, err := g.Do(context.Background(), "same", func(ctx context.Context) (int, error) {
				calls.Add(1)
				if worker == 0 {
					close(entered)
				}
				<-start
				return 7, nil
			})
			if err != nil {
				t.Errorf("Do error: %v", err)
				return
			}
			results <- value
			shared <- wasShared
		}(worker)
	}
	runWorker(0)
	<-entered
	runWorker(1)
	waitUntilShared(t, &g, "same")
	close(start)
	wg.Wait()
	close(results)
	close(shared)

	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	for value := range results {
		if value != 7 {
			t.Fatalf("value = %d, want 7", value)
		}
	}
	if !anyShared(shared) {
		t.Fatal("no call reported shared result")
	}
}

func waitUntilShared[T any](t *testing.T, g *Group[T], key string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		shared := g.calls[key] != nil && g.calls[key].shared
		g.mu.Unlock()
		if shared {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for shared singleflight waiter")
}

func TestGroupDoReturnsErrorToWaiters(t *testing.T) {
	var g Group[int]
	wantErr := errors.New("boom")
	start := make(chan struct{})
	entered := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _, _ = g.Do(context.Background(), "same", func(ctx context.Context) (int, error) {
			close(entered)
			<-start
			return 0, wantErr
		})
	}()
	<-entered
	waiterDone := make(chan error, 1)
	go func() {
		_, _, err := g.Do(context.Background(), "same", func(ctx context.Context) (int, error) {
			return 1, nil
		})
		waiterDone <- err
	}()
	time.Sleep(10 * time.Millisecond)
	close(start)
	<-firstDone
	if err := <-waiterDone; !errors.Is(err, wantErr) {
		t.Fatalf("waiter error = %v, want %v", err, wantErr)
	}
}

func TestGroupDoWaiterCanCancel(t *testing.T) {
	var g Group[int]
	start := make(chan struct{})
	entered := make(chan struct{})
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		_, _, _ = g.Do(context.Background(), "same", func(ctx context.Context) (int, error) {
			close(entered)
			<-start
			return 1, nil
		})
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan struct {
		shared bool
		err    error
	}, 1)
	go func() {
		_, shared, err := g.Do(ctx, "same", func(ctx context.Context) (int, error) {
			return 2, nil
		})
		waiterDone <- struct {
			shared bool
			err    error
		}{shared: shared, err: err}
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	got := <-waiterDone
	shared, err := got.shared, got.err
	if !shared || !errors.Is(err, context.Canceled) {
		t.Fatalf("shared=%v err=%v, want shared canceled", shared, err)
	}
	close(start)
	<-leaderDone
}

func TestGroupDoEmptyKeyDoesNotShare(t *testing.T) {
	var g Group[int]
	var calls atomic.Int64
	for i := 0; i < 3; i++ {
		value, shared, err := g.Do(context.Background(), "", func(context.Context) (int, error) {
			return int(calls.Add(1)), nil
		})
		if err != nil {
			t.Fatalf("Do empty key #%d error: %v", i, err)
		}
		if shared {
			t.Fatalf("Do empty key #%d shared=true, want false", i)
		}
		if value != i+1 {
			t.Fatalf("Do empty key #%d value=%d, want %d", i, value, i+1)
		}
	}
}

func TestGroupForgetAllowsNewLeader(t *testing.T) {
	var g Group[int]
	startFirst := make(chan struct{})
	enteredFirst := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _, _ = g.Do(context.Background(), "same", func(context.Context) (int, error) {
			close(enteredFirst)
			<-startFirst
			return 1, nil
		})
	}()
	<-enteredFirst
	g.Forget("same")
	value, shared, err := g.Do(context.Background(), "same", func(context.Context) (int, error) {
		return 2, nil
	})
	if err != nil || shared || value != 2 {
		t.Fatalf("Do after Forget = value %d shared %v err %v, want new non-shared leader value 2", value, shared, err)
	}
	close(startFirst)
	<-firstDone
}

func anyShared(values <-chan bool) bool {
	for value := range values {
		if value {
			return true
		}
	}
	return false
}
