package eventbus

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishSyncFanOut(t *testing.T) {
	bus := New()
	defer bus.Close()

	var a, b int
	if _, err := bus.Subscribe("topic", func(_ context.Context, e Event) error {
		a += e.(int)
		return nil
	}); err != nil {
		t.Fatalf("subscribe a: %v", err)
	}
	if _, err := bus.Subscribe("topic", func(_ context.Context, e Event) error {
		b += e.(int)
		return nil
	}); err != nil {
		t.Fatalf("subscribe b: %v", err)
	}

	if err := bus.Publish(context.Background(), "topic", 5); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if a != 5 || b != 5 {
		t.Fatalf("want a=b=5, got a=%d b=%d", a, b)
	}
}

func TestPublishNoSubscribers(t *testing.T) {
	bus := New()
	defer bus.Close()
	if err := bus.Publish(context.Background(), "nobody", 1); err != nil {
		t.Fatalf("publish to empty topic: %v", err)
	}
}

func TestPublishAggregatesErrors(t *testing.T) {
	bus := New()
	defer bus.Close()

	errA := errors.New("a failed")
	errB := errors.New("b failed")
	bus.Subscribe("t", func(context.Context, Event) error { return errA })
	bus.Subscribe("t", func(context.Context, Event) error { return errB })
	bus.Subscribe("t", func(context.Context, Event) error { return nil })

	err := bus.Publish(context.Background(), "t", nil)
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("want joined errA+errB, got %v", err)
	}
}

func TestPublishRecoversPanic(t *testing.T) {
	bus := New()
	defer bus.Close()

	called := false
	bus.Subscribe("t", func(context.Context, Event) error { panic("boom") })
	bus.Subscribe("t", func(context.Context, Event) error { called = true; return nil })

	err := bus.Publish(context.Background(), "t", nil)
	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PanicError, got %v", err)
	}
	if pe.Value != "boom" {
		t.Fatalf("want panic value boom, got %v", pe.Value)
	}
	if !called {
		t.Fatal("subsequent handler should still run after panic")
	}
}

func TestPublishContextCancelled(t *testing.T) {
	bus := New()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bus.Subscribe("t", func(context.Context, Event) error {
		t.Fatal("handler must not run on cancelled context")
		return nil
	})
	if err := bus.Publish(ctx, "t", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := New()
	defer bus.Close()

	var n int
	sub, _ := bus.Subscribe("t", func(context.Context, Event) error { n++; return nil })
	bus.Publish(context.Background(), "t", nil)
	sub.Unsubscribe()
	sub.Unsubscribe() // idempotent
	bus.Publish(context.Background(), "t", nil)
	if n != 1 {
		t.Fatalf("want handler called once, got %d", n)
	}
}

func TestSubscribeValidation(t *testing.T) {
	bus := New()
	defer bus.Close()
	if _, err := bus.Subscribe("", func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("want error for empty topic")
	}
	if _, err := bus.Subscribe("t", nil); err == nil {
		t.Fatal("want error for nil handler")
	}
}

func TestPublishAsyncAwaitedByClose(t *testing.T) {
	bus := New()
	var n int64
	bus.Subscribe("t", func(context.Context, Event) error {
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt64(&n, 1)
		return nil
	})
	for i := 0; i < 5; i++ {
		if err := bus.PublishAsync(context.Background(), "t", nil); err != nil {
			t.Fatalf("publish async: %v", err)
		}
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := atomic.LoadInt64(&n); got != 5 {
		t.Fatalf("want 5 async deliveries before close returns, got %d", got)
	}
}

func TestPublishAsyncConcurrentCloseIsSafe(t *testing.T) {
	bus := New()
	if _, err := bus.Subscribe("t", func(context.Context, Event) error {
		time.Sleep(time.Millisecond)
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := bus.PublishAsync(context.Background(), "t", nil); err != nil && !errors.Is(err, ErrClosed) {
				t.Errorf("publish async: %v", err)
			}
		}()
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	wg.Wait()
}

func TestClosedBus(t *testing.T) {
	bus := New()
	bus.Close()
	if err := bus.Close(); err != nil {
		t.Fatalf("double close: %v", err)
	}
	if _, err := bus.Subscribe("t", func(context.Context, Event) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed on subscribe, got %v", err)
	}
	if err := bus.Publish(context.Background(), "t", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed on publish, got %v", err)
	}
	if err := bus.PublishAsync(context.Background(), "t", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed on publish async, got %v", err)
	}
}

func TestConcurrentPublishSubscribe(t *testing.T) {
	bus := New()
	defer bus.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, _ := bus.Subscribe("t", func(context.Context, Event) error { return nil })
			bus.Publish(context.Background(), "t", nil)
			sub.Unsubscribe()
		}()
	}
	wg.Wait()
}

type orderEvent struct{ ID int }

func TestTypedHelpers(t *testing.T) {
	bus := New()
	defer bus.Close()

	var got int
	if _, err := Subscribe(bus, func(_ context.Context, e orderEvent) error {
		got = e.ID
		return nil
	}); err != nil {
		t.Fatalf("typed subscribe: %v", err)
	}
	if err := Publish(context.Background(), bus, orderEvent{ID: 42}); err != nil {
		t.Fatalf("typed publish: %v", err)
	}
	if got != 42 {
		t.Fatalf("want 42, got %d", got)
	}
}

func TestTypedTopicIsolation(t *testing.T) {
	bus := New()
	defer bus.Close()

	var calls int
	Subscribe(bus, func(context.Context, orderEvent) error { calls++; return nil })
	// Different concrete type derives a different topic; handler must not fire.
	Publish(context.Background(), bus, struct{ ID int }{ID: 1})
	if calls != 0 {
		t.Fatalf("want 0 calls for different type, got %d", calls)
	}
	Publish(context.Background(), bus, orderEvent{ID: 1})
	if calls != 1 {
		t.Fatalf("want 1 call for matching type, got %d", calls)
	}
}

func TestTopicName(t *testing.T) {
	if got := TopicName[orderEvent](); got != "eventbus.orderEvent" {
		t.Fatalf("unexpected topic name %q", got)
	}
}
