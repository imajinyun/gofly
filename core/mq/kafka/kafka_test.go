package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/gofly/gofly/core/mq"
	"github.com/gofly/gofly/core/observability/trace"
)

func TestNewValidationAndDefaults(t *testing.T) {
	if _, err := New(Options{}); err == nil || err.Error() != "kafka: at least one broker address is required" {
		t.Fatalf("New without brokers error = %v", err)
	}
	broker, err := New(Options{Brokers: []string{"127.0.0.1:9092"}})
	if err != nil {
		t.Fatalf("New valid error = %v", err)
	}
	if broker.opts.WriteTimeout != 10*time.Second || broker.opts.ReadTimeout != 10*time.Second || broker.opts.MinBytes != 1 || broker.opts.MaxBytes != 10<<20 {
		t.Fatalf("defaults = %#v", broker.opts)
	}
	if _, ok := broker.opts.Balancer.(*kafkago.Hash); !ok {
		t.Fatalf("balancer = %T, want *kafka.Hash", broker.opts.Balancer)
	}
}

func TestPublishSubscribeValidationAndClose(t *testing.T) {
	broker, err := New(Options{Brokers: []string{"127.0.0.1:9092"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(context.Background(), mq.Message{}); !errors.Is(err, mq.ErrInvalidTopic) {
		t.Fatalf("Publish empty topic error = %v, want ErrInvalidTopic", err)
	}
	if _, err := broker.Subscribe(context.Background(), "", "g", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrInvalidTopic) {
		t.Fatalf("Subscribe empty topic error = %v, want ErrInvalidTopic", err)
	}
	if _, err := broker.Subscribe(context.Background(), "topic", "", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrInvalidGroup) {
		t.Fatalf("Subscribe empty group error = %v, want ErrInvalidGroup", err)
	}
	if _, err := broker.Subscribe(context.Background(), "topic", "g", nil); err == nil || err.Error() != "kafka: handler is nil" {
		t.Fatalf("Subscribe nil handler error = %v, want handler error", err)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := broker.Publish(context.Background(), mq.Message{Topic: "topic"}); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Publish after close error = %v, want ErrClosed", err)
	}
}

func TestKafkaMessageRoundTrip(t *testing.T) {
	published := time.Unix(100, 200)
	msg := mq.Message{ID: "id-1", Key: "k", Body: []byte("body"), Attempts: 3, PublishedAt: published, Headers: map[string]string{"trace": "abc"}}
	km := toKafkaMessage(msg)
	got := fromKafkaMessage(km)
	if got.ID != msg.ID || got.Key != msg.Key || string(got.Body) != string(msg.Body) || got.Attempts != msg.Attempts || !got.PublishedAt.Equal(published) {
		t.Fatalf("fromKafkaMessage = %#v, want core fields from %#v", got, msg)
	}
	if got.Headers["trace"] != "abc" {
		t.Fatalf("headers = %#v, want trace header", got.Headers)
	}
}

func TestKafkaMessageEnvelopeHeaderBoundaries_BitsUT(t *testing.T) {
	published := time.Unix(123, 456)
	msg := mq.Message{ID: "id-1", Key: "key-1", Body: []byte("payload"), Attempts: 7, PublishedAt: published, Headers: map[string]string{"trace": "abc"}}
	km := toKafkaMessage(msg)

	headers := make(map[string]string, len(km.Headers))
	for _, header := range km.Headers {
		headers[header.Key] = string(header.Value)
	}
	if string(km.Key) != msg.Key || string(km.Value) != string(msg.Body) {
		t.Fatalf("kafka message key/value = %q/%q, want %q/%q", string(km.Key), string(km.Value), msg.Key, string(msg.Body))
	}
	if headers["mq-id"] != msg.ID || headers["mq-attempts"] != "7" || headers["mq-ts"] != published.Format(time.RFC3339Nano) || headers["h-trace"] != "abc" {
		t.Fatalf("headers = %#v, want envelope and user headers", headers)
	}

	withoutTimestamp := toKafkaMessage(mq.Message{ID: "id-2", Body: []byte("payload")})
	for _, header := range withoutTimestamp.Headers {
		if header.Key == "mq-ts" {
			t.Fatal("zero PublishedAt should not emit mq-ts header")
		}
	}
}

func TestKafkaMessageDecodeIgnoresMalformedEnvelopeHeaders_BitsUT(t *testing.T) {
	messageTime := time.Unix(10, 20)
	got := fromKafkaMessage(kafkago.Message{
		Key:   []byte("key-1"),
		Value: []byte("payload"),
		Time:  messageTime,
		Headers: []kafkago.Header{
			{Key: "mq-id", Value: []byte("id-1")},
			{Key: "mq-attempts", Value: []byte("not-an-int")},
			{Key: "mq-ts", Value: []byte("not-a-time")},
			{Key: "h-trace", Value: []byte("abc")},
			{Key: "h-", Value: []byte("empty-name")},
			{Key: "other", Value: []byte("ignored")},
		},
	})

	if got.ID != "id-1" || got.Key != "key-1" || string(got.Body) != "payload" {
		t.Fatalf("decoded message = %#v, want core fields", got)
	}
	if got.Attempts != 0 {
		t.Fatalf("malformed attempts decoded to %d, want 0", got.Attempts)
	}
	if !got.PublishedAt.Equal(messageTime) {
		t.Fatalf("malformed mq-ts PublishedAt = %s, want fallback %s", got.PublishedAt, messageTime)
	}
	if got.Headers["trace"] != "abc" {
		t.Fatalf("headers = %#v, want trace header", got.Headers)
	}
	if _, ok := got.Headers[""]; ok {
		t.Fatalf("headers = %#v, want malformed h- header ignored", got.Headers)
	}
}

func TestKafkaMessageTraceRoundTrip(t *testing.T) {
	sc := trace.SpanContext{TraceID: "abc12300000000000000000000000000", SpanID: "def4560000000000", Sampled: true}
	ctx := trace.NewContext(context.Background(), sc)
	msg := mq.Message{ID: "id-1", Key: "k", Body: []byte("body")}
	mq.InjectTrace(ctx, &msg)

	km := toKafkaMessage(msg)
	got := fromKafkaMessage(km)
	if got.Headers["traceparent"] != trace.TraceParent(sc) {
		t.Fatalf("trace header = %q, want %q", got.Headers["traceparent"], trace.TraceParent(sc))
	}

	extractedCtx := mq.ExtractTrace(context.Background(), got)
	extractedSC, ok := trace.FromContext(extractedCtx)
	if !ok {
		t.Fatal("expected trace context to be extracted")
	}
	if extractedSC.TraceID != sc.TraceID {
		t.Fatalf("extracted traceID = %q, want %q", extractedSC.TraceID, sc.TraceID)
	}
}

func TestWriterCreatesConfiguredWriterAndReusesIt(t *testing.T) {
	balancer := &kafkago.LeastBytes{}
	broker, err := New(Options{
		Brokers:      []string{"127.0.0.1:9092", "127.0.0.2:9092"},
		WriteTimeout: 2 * time.Second,
		ReadTimeout:  3 * time.Second,
		Balancer:     balancer,
	})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	w1, err := broker.writer("orders")
	if err != nil {
		t.Fatalf("writer error = %v", err)
	}
	w2, err := broker.writer("orders")
	if err != nil {
		t.Fatalf("writer reuse error = %v", err)
	}
	if w1 != w2 {
		t.Fatal("writer was not reused for same topic")
	}
	if w1.Topic != "orders" || w1.Balancer != balancer || w1.WriteTimeout != 2*time.Second || w1.ReadTimeout != 3*time.Second {
		t.Fatalf("writer config = %#v", w1)
	}
	if len(broker.writers) != 1 {
		t.Fatalf("writers len = %d, want 1", len(broker.writers))
	}
}

func TestCloseIsIdempotentAndRejectsWriterAfterClose(t *testing.T) {
	broker, err := New(Options{Brokers: []string{"127.0.0.1:9092"}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if _, err := broker.writer("orders"); err != nil {
		t.Fatalf("writer error = %v", err)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close twice error = %v", err)
	}
	if _, err := broker.writer("payments"); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("writer after close error = %v, want ErrClosed", err)
	}
	if len(broker.writers) != 0 {
		t.Fatalf("writers after close = %d, want 0", len(broker.writers))
	}
}

func TestSubscribeRejectsAfterClose(t *testing.T) {
	broker, err := New(Options{Brokers: []string{"127.0.0.1:9092"}})
	if err != nil {
		t.Fatalf("New error = %v", err)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	_, err = broker.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
	if !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Subscribe after close error = %v, want ErrClosed", err)
	}
}

func TestSleepContextBoundaries(t *testing.T) {
	if !sleepContext(context.Background(), 0) {
		t.Fatal("sleepContext with zero duration = false, want true")
	}
	if !sleepContext(context.Background(), -time.Second) {
		t.Fatal("sleepContext with negative duration = false, want true")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepContext(canceled, time.Hour) {
		t.Fatal("sleepContext with canceled context = true, want false")
	}

	if !sleepContext(context.Background(), time.Nanosecond) {
		t.Fatal("sleepContext with elapsed timer = false, want true")
	}
}

func TestSubscriptionProcessStopsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts []int
	sub := &subscription{
		topic: "orders",
		group: "workers",
		ctx:   ctx,
		cfg: mq.BuildSubscriptionConfig(
			"orders",
			mq.WithMaxAttempts(3),
			mq.WithRetryBackoff(0),
			mq.WithDeadLetterTopic(""),
		),
		handler: func(_ context.Context, msg mq.Message) error {
			attempts = append(attempts, msg.Attempts)
			if len(attempts) == 2 {
				cancel()
			}
			return errors.New("temporary failure")
		},
	}

	sub.process(kafkago.Message{Key: []byte("k"), Value: []byte("payload")})

	if len(attempts) != 2 {
		t.Fatalf("handler attempts = %v, want two attempts before cancellation", attempts)
	}
	if attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("handler attempts = %v, want [1 2]", attempts)
	}
}

func TestSubscriptionProcessHandlerSuccess(t *testing.T) {
	called := false
	sub := &subscription{
		topic: "orders",
		group: "workers",
		ctx:   context.Background(),
		cfg:   mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(1)),
		handler: func(_ context.Context, msg mq.Message) error {
			called = true
			return nil
		},
		reader: kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "orders"}),
	}

	sub.process(kafkago.Message{Key: []byte("k"), Value: []byte("payload")})
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestSubscriptionStopClosesReaderAndIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancelCalled := false
	sub := &subscription{
		ctx: ctx,
		cancel: func() {
			cancelCalled = true
			cancel()
		},
		reader: kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{"127.0.0.1:9092"}, Topic: "orders"}),
		done:   make(chan struct{}),
	}

	var stopCtx context.Context
	if err := sub.Stop(stopCtx); err != nil {
		t.Fatalf("Stop nil ctx error = %v", err)
	}
	if !cancelCalled {
		t.Fatal("Stop did not call subscription cancel")
	}
	if err := sub.Stop(context.Background()); err != nil {
		t.Fatalf("Stop twice error = %v", err)
	}
}

func TestPublishNilContext(t *testing.T) {
	b := &Broker{closed: true}
	var nilCtx context.Context
	if err := b.Publish(nilCtx, mq.Message{Topic: "orders"}); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Publish nil ctx error = %v, want ErrClosed", err)
	}
}

func TestSubscribeNilContext(t *testing.T) {
	b := &Broker{closed: true}
	var nilCtx context.Context
	_, err := b.Subscribe(nilCtx, "orders", "workers", func(context.Context, mq.Message) error { return nil })
	if !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Subscribe nil ctx error = %v, want ErrClosed", err)
	}
}

func TestCloseStopsSubscriptionsAndClosesWriters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sub1 := &subscription{done: make(chan struct{})}
	sub1.once.Do(func() {})

	sub2 := &subscription{done: make(chan struct{})}
	sub2.once.Do(func() {})
	close(sub2.done)

	b := &Broker{subs: []*subscription{sub1, sub2}, writers: map[string]*kafkago.Writer{"orders": {}}}
	if err := b.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close error = %v, want context.Canceled", err)
	}
}

func TestSubscriptionProcessContextCanceledReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sub := &subscription{
		topic: "orders",
		group: "workers",
		cfg:   mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(3)),
		ctx:   ctx,
		handler: func(context.Context, mq.Message) error {
			cancel()
			return errors.New("stopping")
		},
		reader: kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "orders"}),
	}

	sub.process(kafkago.Message{Key: []byte("k"), Value: []byte("payload")})
}

func TestSubscriptionProcessBackoffCanceledReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(time.Millisecond, cancel)
	defer timer.Stop()
	sub := &subscription{
		topic: "orders",
		group: "workers",
		cfg:   mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(2), mq.WithRetryBackoff(time.Hour)),
		ctx:   ctx,
		handler: func(context.Context, mq.Message) error {
			return errors.New("retry later")
		},
		reader: kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "orders"}),
	}

	sub.process(kafkago.Message{Key: []byte("k"), Value: []byte("payload")})
}

func TestSubscriptionProcessDeadLetterWriterUnavailable(t *testing.T) {
	b := &Broker{closed: true}
	sub := &subscription{
		broker:  b,
		topic:   "orders",
		group:   "workers",
		cfg:     mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(1), mq.WithDeadLetterTopic("orders.dlq")),
		ctx:     context.Background(),
		handler: func(context.Context, mq.Message) error { return errors.New("boom") },
		reader:  kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "orders"}),
	}

	sub.process(kafkago.Message{Key: []byte("k"), Value: []byte("payload")})
}

func TestSubscriptionProcessDeadLetterWriteFails(t *testing.T) {
	b, _ := New(Options{Brokers: []string{"127.0.0.1:1"}})
	sub := &subscription{
		broker:  b,
		topic:   "orders",
		group:   "workers",
		cfg:     mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(1), mq.WithDeadLetterTopic("orders.dlq")),
		ctx:     context.Background(),
		handler: func(context.Context, mq.Message) error { return errors.New("boom") },
		reader:  kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "orders"}),
	}

	sub.process(kafkago.Message{Key: []byte("k"), Value: []byte("payload")})
}

func TestSubscriptionRunStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sub := &subscription{
		ctx:    ctx,
		reader: kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{"127.0.0.1:1"}, Topic: "orders"}),
		sem:    make(chan struct{}, 1),
	}
	sub.wg.Add(1)
	cancel()
	sub.run()
}

func TestSubscriptionStopHonorsCallerCancellation(t *testing.T) {
	subCtx, cancelSub := context.WithCancel(context.Background())
	sub := &subscription{
		ctx:    subCtx,
		cancel: cancelSub,
		reader: kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{"127.0.0.1:9092"}, Topic: "orders"}),
		done:   make(chan struct{}),
	}
	sub.wg.Add(1)

	stopCtx, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	if err := sub.Stop(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop canceled context error = %v, want context.Canceled", err)
	}

	sub.wg.Done()
	if err := sub.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after worker drain error = %v", err)
	}
}

func BenchmarkToKafkaMessage(b *testing.B) {
	msg := mq.Message{ID: "id-1", Key: "k", Body: []byte("body"), Attempts: 3, Headers: map[string]string{"trace": "abc"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = toKafkaMessage(msg)
	}
}

func BenchmarkFromKafkaMessage(b *testing.B) {
	km := kafkago.Message{Key: []byte("k"), Value: []byte("body"), Headers: []kafkago.Header{{Key: "mq-id", Value: []byte("id-1")}, {Key: "h-trace", Value: []byte("abc")}}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fromKafkaMessage(km)
	}
}
