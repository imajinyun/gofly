package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestMemoryStore(now func() time.Time) *MemoryStore {
	s := NewMemoryStore()
	if now != nil {
		s.now = now
	}
	return s
}

func TestMemoryStoreEnqueueValidation(t *testing.T) {
	s := NewMemoryStore()
	if _, err := s.Enqueue(context.Background(), Message{}); !errors.Is(err, ErrEmptyTopic) {
		t.Fatalf("Enqueue empty topic err = %v, want ErrEmptyTopic", err)
	}
}

func TestMemoryStoreFetchLeasesRecords(t *testing.T) {
	now := time.Now()
	clock := now
	s := newTestMemoryStore(func() time.Time { return clock })

	id, err := s.Enqueue(context.Background(), Message{Topic: "orders", Body: []byte("a")})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	got, err := s.Fetch(context.Background(), 10, time.Minute)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 || got[0].ID != id || got[0].Attempts != 1 {
		t.Fatalf("Fetch = %+v, want one leased record with attempts=1", got)
	}

	// Within the visibility window the record must not be re-fetched.
	if again, _ := s.Fetch(context.Background(), 10, time.Minute); len(again) != 0 {
		t.Fatalf("Fetch within lease returned %d records, want 0", len(again))
	}

	// After the lease expires the record is claimable again.
	clock = now.Add(2 * time.Minute)
	if again, _ := s.Fetch(context.Background(), 10, time.Minute); len(again) != 1 {
		t.Fatalf("Fetch after lease returned %d records, want 1", len(again))
	}
}

func TestMemoryStoreMarkDeliveredAndDead(t *testing.T) {
	s := NewMemoryStore()
	id, _ := s.Enqueue(context.Background(), Message{Topic: "t", Body: []byte("x")})

	if _, err := s.Fetch(context.Background(), 1, time.Minute); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := s.MarkDelivered(context.Background(), id); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	rec, _ := s.Get(id)
	if rec.Status != StatusDelivered || rec.DeliveredAt.IsZero() {
		t.Fatalf("record = %+v, want delivered", rec)
	}
	// Delivered records are not fetched.
	if got, _ := s.Fetch(context.Background(), 1, time.Minute); len(got) != 0 {
		t.Fatalf("Fetch delivered returned %d, want 0", len(got))
	}
}

func TestMemoryStoreRetryReschedules(t *testing.T) {
	now := time.Now()
	clock := now
	s := newTestMemoryStore(func() time.Time { return clock })
	id, _ := s.Enqueue(context.Background(), Message{Topic: "t", Body: []byte("x")})
	_, _ = s.Fetch(context.Background(), 1, time.Minute)

	future := now.Add(5 * time.Minute)
	if err := s.Retry(context.Background(), id, future, "boom"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	rec, _ := s.Get(id)
	if rec.LastError != "boom" || !rec.AvailableAt.Equal(future) {
		t.Fatalf("record = %+v, want rescheduled", rec)
	}
	// Not yet due.
	if got, _ := s.Fetch(context.Background(), 1, time.Minute); len(got) != 0 {
		t.Fatalf("Fetch before availableAt returned %d, want 0", len(got))
	}
	clock = future.Add(time.Second)
	if got, _ := s.Fetch(context.Background(), 1, time.Minute); len(got) != 1 {
		t.Fatalf("Fetch after availableAt returned %d, want 1", len(got))
	}
}

func TestMemoryStoreClosed(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Close()
	if _, err := s.Enqueue(context.Background(), Message{Topic: "t"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Enqueue after close err = %v, want ErrClosed", err)
	}
	if _, err := s.Fetch(context.Background(), 1, time.Minute); !errors.Is(err, ErrClosed) {
		t.Fatalf("Fetch after close err = %v, want ErrClosed", err)
	}
}

func TestMemoryStoreLenAndBoundaryUpdates(t *testing.T) {
	s := NewMemoryStore()
	if got := s.Len(); got != 0 {
		t.Fatalf("Len empty = %d, want 0", got)
	}
	id, err := s.Enqueue(context.Background(), Message{Topic: "orders", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := s.Len(); got != 1 {
		t.Fatalf("Len after enqueue = %d, want 1", got)
	}

	if err := s.MarkDelivered(context.Background(), "missing"); err != nil {
		t.Fatalf("MarkDelivered missing: %v", err)
	}
	if err := s.Retry(context.Background(), "missing", time.Now(), "missing"); err != nil {
		t.Fatalf("Retry missing: %v", err)
	}
	if err := s.MarkDead(context.Background(), id, "exhausted"); err != nil {
		t.Fatalf("MarkDead: %v", err)
	}
	rec, ok := s.Get(id)
	if !ok || rec.Status != StatusDead || rec.LastError != "exhausted" {
		t.Fatalf("Get after MarkDead = %+v, %v; want dead record", rec, ok)
	}
	if err := s.MarkDead(context.Background(), "missing", "ignored"); err != nil {
		t.Fatalf("MarkDead missing: %v", err)
	}
}

func TestMemoryStoreClosedUpdateOperations(t *testing.T) {
	s := NewMemoryStore()
	if _, err := s.Enqueue(context.Background(), Message{Topic: "orders", Body: []byte("x")}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.MarkDelivered(context.Background(), "id"); !errors.Is(err, ErrClosed) {
		t.Fatalf("MarkDelivered after close err = %v, want ErrClosed", err)
	}
	if err := s.Retry(context.Background(), "id", time.Now(), "boom"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Retry after close err = %v, want ErrClosed", err)
	}
	if err := s.MarkDead(context.Background(), "id", "boom"); !errors.Is(err, ErrClosed) {
		t.Fatalf("MarkDead after close err = %v, want ErrClosed", err)
	}
}

func TestRelayProcessBatchDelivers(t *testing.T) {
	s := NewMemoryStore()
	id, _ := s.Enqueue(context.Background(), Message{Topic: "orders", Body: []byte("payload")})

	var published []Message
	pub := PublisherFunc(func(_ context.Context, msg Message) error {
		published = append(published, msg)
		return nil
	})
	relay := NewRelay(s, pub, RelayConfig{BatchSize: 10})

	n, err := relay.ProcessBatch(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("ProcessBatch = %d, %v; want 1, nil", n, err)
	}
	if len(published) != 1 || published[0].Topic != "orders" {
		t.Fatalf("published = %+v", published)
	}
	rec, _ := s.Get(id)
	if rec.Status != StatusDelivered {
		t.Fatalf("record status = %s, want delivered", rec.Status)
	}
}

func TestRelayRetriesThenDeadLetters(t *testing.T) {
	now := time.Now()
	clock := now
	s := newTestMemoryStore(func() time.Time { return clock })
	id, _ := s.Enqueue(context.Background(), Message{Topic: "orders", Body: []byte("x")})

	pub := PublisherFunc(func(_ context.Context, _ Message) error {
		return errors.New("broker down")
	})
	relay := NewRelay(s, pub, RelayConfig{
		BatchSize:   10,
		MaxAttempts: 3,
		BaseBackoff: time.Second,
		MaxBackoff:  time.Minute,
		Visibility:  time.Minute,
	})
	relay.now = func() time.Time { return clock }

	// Attempt 1: failure -> retry scheduled.
	if _, err := relay.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch 1: %v", err)
	}
	rec, _ := s.Get(id)
	if rec.Status != StatusPending || rec.LastError != "broker down" {
		t.Fatalf("after attempt 1 record = %+v, want pending+retry", rec)
	}

	// Advance past backoff and reprocess until dead-lettered.
	for i := 0; i < 5 && rec.Status != StatusDead; i++ {
		clock = clock.Add(2 * time.Minute)
		if _, err := relay.ProcessBatch(context.Background()); err != nil {
			t.Fatalf("ProcessBatch loop: %v", err)
		}
		rec, _ = s.Get(id)
	}
	if rec.Status != StatusDead {
		t.Fatalf("record status = %s (attempts=%d), want dead", rec.Status, rec.Attempts)
	}
}

func TestRelayStartStop(t *testing.T) {
	s := NewMemoryStore()
	_, _ = s.Enqueue(context.Background(), Message{Topic: "orders", Body: []byte("x")})

	delivered := make(chan Message, 1)
	pub := PublisherFunc(func(_ context.Context, msg Message) error {
		select {
		case delivered <- msg:
		default:
		}
		return nil
	})
	relay := NewRelay(s, pub, RelayConfig{BatchSize: 10, PollInterval: 5 * time.Millisecond})

	relay.Start(context.Background())
	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not deliver within timeout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := relay.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Stopping again is a no-op.
	if err := relay.Stop(ctx); err != nil {
		t.Fatalf("Stop(2): %v", err)
	}
}

func TestRelayBackoff(t *testing.T) {
	relay := NewRelay(NewMemoryStore(), PublisherFunc(func(context.Context, Message) error { return nil }), RelayConfig{
		BaseBackoff: time.Second,
		MaxBackoff:  10 * time.Second,
	})
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 10 * time.Second}, // capped
		{10, 10 * time.Second},
	}
	for _, tc := range cases {
		if got := relay.backoff(tc.attempts); got != tc.want {
			t.Errorf("backoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

func TestMemoryStoreCopiesMessageOnEnqueueAndGet(t *testing.T) {
	s := NewMemoryStore()
	body := []byte("payload")
	headers := map[string]string{"trace": "original"}
	id, err := s.Enqueue(context.Background(), Message{Topic: "orders", Body: body, Headers: headers})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	body[0] = 'X'
	headers["trace"] = "mutated"
	rec, ok := s.Get(id)
	if !ok {
		t.Fatalf("Get(%q) returned ok=false", id)
	}
	if string(rec.Message.Body) != "payload" || rec.Message.Headers["trace"] != "original" {
		t.Fatalf("stored message was mutated by caller aliases: %+v", rec.Message)
	}

	rec.Message.Body[0] = 'Y'
	rec.Message.Headers["trace"] = "changed-through-copy"
	again, _ := s.Get(id)
	if string(again.Message.Body) != "payload" || again.Message.Headers["trace"] != "original" {
		t.Fatalf("Get returned mutable internal aliases: %+v", again.Message)
	}
}

func TestMemoryStoreFetchOldestAvailableFirst(t *testing.T) {
	base := time.Now()
	clock := base
	s := newTestMemoryStore(func() time.Time { return clock })

	second, _ := s.Enqueue(context.Background(), Message{Topic: "orders", Key: "second"})
	clock = base.Add(time.Second)
	first, _ := s.Enqueue(context.Background(), Message{Topic: "orders", Key: "first"})

	if err := s.Retry(context.Background(), second, base.Add(10*time.Second), "later"); err != nil {
		t.Fatalf("Retry second: %v", err)
	}
	if err := s.Retry(context.Background(), first, base.Add(5*time.Second), "earlier"); err != nil {
		t.Fatalf("Retry first: %v", err)
	}

	clock = base.Add(20 * time.Second)
	got, err := s.Fetch(context.Background(), 2, time.Minute)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 2 || got[0].ID != first || got[1].ID != second {
		t.Fatalf("Fetch order IDs = %+v, want first availableAt before second", got)
	}
}

func TestRelayProcessBatchCanceledContextDoesNotCountUndelivered(t *testing.T) {
	s := NewMemoryStore()
	_, _ = s.Enqueue(context.Background(), Message{Topic: "orders", Body: []byte("x")})

	var published int
	relay := NewRelay(s, PublisherFunc(func(context.Context, Message) error {
		published++
		return nil
	}), RelayConfig{BatchSize: 10})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	processed, err := relay.ProcessBatch(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessBatch err = %v, want context.Canceled", err)
	}
	if processed != 0 {
		t.Fatalf("ProcessBatch processed = %d, want 0 undelivered records counted", processed)
	}
	if published != 0 {
		t.Fatalf("publisher called %d times, want 0", published)
	}
}
