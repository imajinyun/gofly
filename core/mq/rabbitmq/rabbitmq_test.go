package rabbitmq

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/imajinyun/gofly/core/mq"
)

func TestOptionsDefaultsAndExchange(t *testing.T) {
	opts := (Options{}).withDefaults()
	if opts.Prefetch != 32 {
		t.Fatalf("Prefetch = %d, want 32", opts.Prefetch)
	}
	if got := (Options{Prefetch: -1}).withDefaults(); got.Prefetch != 32 {
		t.Fatalf("Prefetch negative = %d, want 32", got.Prefetch)
	}
	if got := (Options{Prefetch: 64}).withDefaults(); got.Prefetch != 64 {
		t.Fatalf("Prefetch positive = %d, want 64", got.Prefetch)
	}
	if got := (Options{}).exchange("orders"); got != "orders" {
		t.Fatalf("exchange without prefix = %q, want orders", got)
	}
	if got := (Options{ExchangePrefix: "svc"}).exchange("orders"); got != "svc.orders" {
		t.Fatalf("exchange with prefix = %q, want svc.orders", got)
	}
}

func TestNewValidationAndBrokerValidation(t *testing.T) {
	if _, err := New(Options{}); err == nil || err.Error() != "rabbitmq: connection URL is required" {
		t.Fatalf("New without URL error = %v", err)
	}
	if _, err := New(Options{URL: "%zz"}); err == nil || !strings.Contains(err.Error(), "rabbitmq: dial:") {
		t.Fatalf("New invalid URL error = %v, want wrapped dial error", err)
	}
	broker := &Broker{}
	if err := broker.Publish(context.Background(), mq.Message{}); !errors.Is(err, mq.ErrInvalidTopic) {
		t.Fatalf("Publish empty topic error = %v, want ErrInvalidTopic", err)
	}
	if _, err := broker.Subscribe(context.Background(), "", "g", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrInvalidTopic) {
		t.Fatalf("Subscribe empty topic error = %v, want ErrInvalidTopic", err)
	}
	if _, err := broker.Subscribe(context.Background(), "topic", "", func(context.Context, mq.Message) error { return nil }); !errors.Is(err, mq.ErrInvalidGroup) {
		t.Fatalf("Subscribe empty group error = %v, want ErrInvalidGroup", err)
	}
	if _, err := broker.Subscribe(context.Background(), "topic", "g", nil); err == nil || err.Error() != "rabbitmq: handler is nil" {
		t.Fatalf("Subscribe nil handler error = %v, want handler error", err)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close zero broker error = %v", err)
	}
	if err := broker.Close(context.Background()); err != nil {
		t.Fatalf("Close zero broker twice error = %v", err)
	}
}

func TestAMQPHeadersAndDeliveryRoundTrip(t *testing.T) {
	published := time.Unix(111, 222)
	msg := mq.Message{ID: "id-1", Body: []byte("body"), Attempts: 4, PublishedAt: published, Headers: map[string]string{"trace": "abc"}}
	headers := toAMQPHeaders(msg)
	if headers["mq-attempts"] != int32(4) || headers["h-trace"] != "abc" {
		t.Fatalf("headers = %#v", headers)
	}
	delivery := amqp.Delivery{MessageId: msg.ID, Body: msg.Body, Timestamp: published, Headers: headers}
	delivery.Headers["h-ignore"] = 123
	got := fromAMQPDelivery(delivery)
	if got.ID != msg.ID || string(got.Body) != string(msg.Body) || !got.PublishedAt.Equal(published) {
		t.Fatalf("fromAMQPDelivery = %#v, want core fields from msg", got)
	}
	if got.Headers["trace"] != "abc" {
		t.Fatalf("headers = %#v, want trace header", got.Headers)
	}
	if _, ok := got.Headers["ignore"]; ok {
		t.Fatalf("non-string AMQP header should be ignored, got %#v", got.Headers)
	}
}

func TestAMQPDeliveryWithoutBusinessHeaders(t *testing.T) {
	published := time.Unix(222, 333)
	delivery := amqp.Delivery{
		MessageId: "id-without-headers",
		Body:      []byte("body"),
		Timestamp: published,
		Headers:   amqp.Table{"mq-attempts": int32(3), "x-extra": "ignored"},
	}

	got := fromAMQPDelivery(delivery)

	if got.ID != delivery.MessageId || string(got.Body) != string(delivery.Body) || !got.PublishedAt.Equal(published) {
		t.Fatalf("fromAMQPDelivery = %#v, want core delivery fields", got)
	}
	if got.Headers != nil {
		t.Fatalf("Headers = %#v, want nil when no h-* string headers exist", got.Headers)
	}
}

func TestAMQPHeadersClampAttempts(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		want     int32
	}{
		{name: "negative attempts", attempts: -1, want: 0},
		{name: "normal attempts", attempts: 7, want: 7},
		{name: "overflow attempts", attempts: math.MaxInt, want: math.MaxInt32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := mq.Message{Topic: "orders", Attempts: tt.attempts}
			headers := toAMQPHeaders(msg)
			if got := headers["mq-attempts"]; got != tt.want {
				t.Fatalf("mq-attempts = %#v, want %d", got, tt.want)
			}
		})
	}
}

func TestSubscriptionProcessDeadLetterFailureNacks(t *testing.T) {
	ack := &fakeAcknowledger{}
	broker := &Broker{closed: true}
	sub := &subscription{
		broker:  broker,
		topic:   "orders",
		group:   "workers",
		cfg:     mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(1), mq.WithDeadLetterTopic("orders.dlq")),
		ctx:     context.Background(),
		handler: func(context.Context, mq.Message) error { return errors.New("boom") },
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 7, MessageId: "msg-1", Body: []byte("payload")})

	if ack.ackTags != nil {
		t.Fatalf("Ack tags = %#v, want none when DLQ publish fails", ack.ackTags)
	}
	if !reflect.DeepEqual(ack.nackTags, []uint64{7}) || !reflect.DeepEqual(ack.nackRequeue, []bool{true}) {
		t.Fatalf("Nack tags/requeue = %#v/%#v, want [7]/[true]", ack.nackTags, ack.nackRequeue)
	}
}

func TestSubscriptionProcessSuccessAcks(t *testing.T) {
	ack := &fakeAcknowledger{}
	sub := &subscription{
		topic:   "orders",
		group:   "workers",
		cfg:     mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(1)),
		ctx:     context.Background(),
		handler: func(context.Context, mq.Message) error { return nil },
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 9, MessageId: "msg-2", Body: []byte("ok")})

	if !reflect.DeepEqual(ack.ackTags, []uint64{9}) {
		t.Fatalf("Ack tags = %#v, want [9]", ack.ackTags)
	}
	if ack.nackTags != nil || ack.rejectTags != nil {
		t.Fatalf("unexpected nack/reject tags = %#v/%#v", ack.nackTags, ack.rejectTags)
	}
}

func TestPublishAndSubscribeReturnClosed(t *testing.T) {
	broker := &Broker{closed: true}
	if err := broker.Publish(context.Background(), mq.Message{Topic: "orders"}); !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Publish closed error = %v, want ErrClosed", err)
	}
	_, err := broker.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
	if !errors.Is(err, mq.ErrClosed) {
		t.Fatalf("Subscribe closed error = %v, want ErrClosed", err)
	}
}

func TestSubscriptionProcessRetriesThenAcks(t *testing.T) {
	ack := &fakeAcknowledger{}
	attempts := make([]int, 0, 2)
	sub := &subscription{
		topic: "orders",
		group: "workers",
		cfg:   mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(2), mq.WithDeadLetterTopic("")),
		ctx:   context.Background(),
		handler: func(_ context.Context, msg mq.Message) error {
			attempts = append(attempts, msg.Attempts)
			if msg.Attempts == 1 {
				return errors.New("retry me")
			}
			return nil
		},
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 11, MessageId: "msg-retry", Body: []byte("payload")})

	if !reflect.DeepEqual(attempts, []int{1, 2}) {
		t.Fatalf("attempts = %#v, want [1 2]", attempts)
	}
	if !reflect.DeepEqual(ack.ackTags, []uint64{11}) || ack.nackTags != nil {
		t.Fatalf("ack/nack = %#v/%#v, want ack only", ack.ackTags, ack.nackTags)
	}
}

func TestSubscriptionProcessContextCanceledNacks(t *testing.T) {
	ack := &fakeAcknowledger{}
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
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 13, MessageId: "msg-cancel", Body: []byte("payload")})

	if ack.ackTags != nil {
		t.Fatalf("Ack tags = %#v, want none on canceled handler", ack.ackTags)
	}
	if !reflect.DeepEqual(ack.nackTags, []uint64{13}) || !reflect.DeepEqual(ack.nackRequeue, []bool{true}) {
		t.Fatalf("Nack tags/requeue = %#v/%#v, want [13]/[true]", ack.nackTags, ack.nackRequeue)
	}
}

func TestSubscriptionProcessMaxAttemptsZero(t *testing.T) {
	ack := &fakeAcknowledger{}
	sub := &subscription{
		topic:   "orders",
		group:   "workers",
		cfg:     mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(0)),
		ctx:     context.Background(),
		handler: func(context.Context, mq.Message) error { return nil },
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 21, MessageId: "msg-zero", Body: []byte("payload")})

	if !reflect.DeepEqual(ack.ackTags, []uint64{21}) {
		t.Fatalf("Ack tags = %#v, want [21]", ack.ackTags)
	}
}

func TestSubscriptionProcessBackoffThenSuccess(t *testing.T) {
	ack := &fakeAcknowledger{}
	attempts := make([]int, 0, 2)
	sub := &subscription{
		topic: "orders",
		group: "workers",
		cfg:   mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(2), mq.WithRetryBackoff(time.Millisecond)),
		ctx:   context.Background(),
		handler: func(_ context.Context, msg mq.Message) error {
			attempts = append(attempts, msg.Attempts)
			if msg.Attempts == 1 {
				return errors.New("retry me")
			}
			return nil
		},
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 23, MessageId: "msg-backoff-ok", Body: []byte("payload")})

	if !reflect.DeepEqual(attempts, []int{1, 2}) {
		t.Fatalf("attempts = %#v, want [1 2]", attempts)
	}
	if !reflect.DeepEqual(ack.ackTags, []uint64{23}) || ack.nackTags != nil {
		t.Fatalf("ack/nack = %#v/%#v, want ack only", ack.ackTags, ack.nackTags)
	}
}

func TestSubscriptionProcessAckError(t *testing.T) {
	ack := &fakeAcknowledger{ackErr: errors.New("ack failed")}
	sub := &subscription{
		topic:   "orders",
		group:   "workers",
		cfg:     mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(1)),
		ctx:     context.Background(),
		handler: func(context.Context, mq.Message) error { return nil },
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 25, MessageId: "msg-ack-err", Body: []byte("payload")})

	if !reflect.DeepEqual(ack.ackTags, []uint64{25}) {
		t.Fatalf("Ack tags = %#v, want [25]", ack.ackTags)
	}
}

func TestSubscriptionProcessContextCanceledNackFails(t *testing.T) {
	ack := &fakeAcknowledger{nackErr: errors.New("nack failed")}
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
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 27, MessageId: "msg-nack-fail", Body: []byte("payload")})

	if ack.ackTags != nil {
		t.Fatalf("Ack tags = %#v, want none", ack.ackTags)
	}
	if !reflect.DeepEqual(ack.nackTags, []uint64{27}) || !reflect.DeepEqual(ack.nackRequeue, []bool{true}) {
		t.Fatalf("Nack tags/requeue = %#v/%#v, want [27]/[true]", ack.nackTags, ack.nackRequeue)
	}
}

func TestSubscriptionProcessBackoffCancelNackFails(t *testing.T) {
	ack := &fakeAcknowledger{nackErr: errors.New("nack failed")}
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
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 29, MessageId: "msg-backoff-nack-fail", Body: []byte("payload")})

	if ack.ackTags != nil {
		t.Fatalf("Ack tags = %#v, want none", ack.ackTags)
	}
	if !reflect.DeepEqual(ack.nackTags, []uint64{29}) || !reflect.DeepEqual(ack.nackRequeue, []bool{true}) {
		t.Fatalf("Nack tags/requeue = %#v/%#v, want [29]/[true]", ack.nackTags, ack.nackRequeue)
	}
}

func TestSubscriptionProcessBackoffCancelNacks(t *testing.T) {
	ack := &fakeAcknowledger{}
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
	}

	sub.process(amqp.Delivery{Acknowledger: ack, DeliveryTag: 17, MessageId: "msg-backoff", Body: []byte("payload")})

	if ack.ackTags != nil {
		t.Fatalf("Ack tags = %#v, want none when backoff is canceled", ack.ackTags)
	}
	if !reflect.DeepEqual(ack.nackTags, []uint64{17}) || !reflect.DeepEqual(ack.nackRequeue, []bool{true}) {
		t.Fatalf("Nack tags/requeue = %#v/%#v, want [17]/[true]", ack.nackTags, ack.nackRequeue)
	}
}

func TestRabbitMQSleepContextBoundaries(t *testing.T) {
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

func TestSubscriptionRunStopsOnContextAndDeliveryClose(t *testing.T) {
	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		deliveries := make(chan amqp.Delivery)
		sub := &subscription{ctx: ctx, deliveries: deliveries, sem: make(chan struct{}, 1)}
		cancel()
		sub.wg.Add(1)

		sub.run()
	})

	t.Run("deliveries closed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		deliveries := make(chan amqp.Delivery)
		close(deliveries)
		sub := &subscription{ctx: ctx, deliveries: deliveries, sem: make(chan struct{}, 1)}
		sub.wg.Add(1)

		sub.run()
	})
}

func TestSubscriptionRunDispatchesDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deliveries := make(chan amqp.Delivery)
	ack := &fakeAcknowledger{}
	handled := make(chan mq.Message, 1)
	sub := &subscription{
		topic:      "orders",
		group:      "workers",
		cfg:        mq.BuildSubscriptionConfig("orders", mq.WithMaxAttempts(1)),
		ctx:        ctx,
		deliveries: deliveries,
		sem:        make(chan struct{}, 1),
		handler: func(_ context.Context, msg mq.Message) error {
			handled <- msg
			return nil
		},
	}
	sub.wg.Add(1)
	go sub.run()

	deliveries <- amqp.Delivery{Acknowledger: ack, DeliveryTag: 19, MessageId: "msg-run", Body: []byte("payload")}

	select {
	case got := <-handled:
		if got.ID != "msg-run" || got.Topic != "orders" || got.Attempts != 1 {
			t.Fatalf("handled message = %#v, want dispatched topic/id/attempt", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dispatched delivery")
	}

	close(deliveries)
	done := make(chan struct{})
	go func() {
		sub.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription run to stop")
	}
	if !reflect.DeepEqual(ack.ackTags, []uint64{19}) {
		t.Fatalf("Ack tags = %#v, want [19]", ack.ackTags)
	}
}

func TestSubscriptionStopBoundaries(t *testing.T) {
	t.Run("already stopped returns when done is closed", func(t *testing.T) {
		sub := &subscription{done: make(chan struct{})}
		sub.once.Do(func() {})
		close(sub.done)

		var nilCtx context.Context
		if err := sub.Stop(nilCtx); err != nil {
			t.Fatalf("Stop already done error = %v, want nil", err)
		}
	})

	t.Run("caller context canceled while waiting", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sub := &subscription{done: make(chan struct{})}
		sub.once.Do(func() {})

		if err := sub.Stop(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Stop canceled error = %v, want context.Canceled", err)
		}
	})
}

func TestEnsureExchangeAlreadyDeclared(t *testing.T) {
	b := &Broker{declared: map[string]bool{"orders": true}}
	if err := b.ensureExchange(nil, "orders"); err != nil {
		t.Fatalf("ensureExchange already declared error = %v, want nil", err)
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

func TestCloseStopsSubscriptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sub1 := &subscription{done: make(chan struct{})}
	sub1.once.Do(func() {})

	sub2 := &subscription{done: make(chan struct{})}
	sub2.once.Do(func() {})
	close(sub2.done)

	b := &Broker{subs: []*subscription{sub1, sub2}}
	if err := b.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close error = %v, want context.Canceled", err)
	}
}

type fakeAcknowledger struct {
	ackTags     []uint64
	nackTags    []uint64
	nackRequeue []bool
	rejectTags  []uint64
	ackErr      error
	nackErr     error
	rejectErr   error
}

func (f *fakeAcknowledger) Ack(tag uint64, multiple bool) error {
	f.ackTags = append(f.ackTags, tag)
	return f.ackErr
}

func (f *fakeAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	f.nackTags = append(f.nackTags, tag)
	f.nackRequeue = append(f.nackRequeue, requeue)
	return f.nackErr
}

func (f *fakeAcknowledger) Reject(tag uint64, requeue bool) error {
	f.rejectTags = append(f.rejectTags, tag)
	return f.rejectErr
}

type fakeAMQPConnection struct {
	channel  amqpChannel
	chErr    error
	closeErr error
}

func (f *fakeAMQPConnection) Channel() (amqpChannel, error) {
	if f.chErr != nil {
		return nil, f.chErr
	}
	return f.channel, nil
}

func (f *fakeAMQPConnection) Close() error {
	return f.closeErr
}

type fakeAMQPChannel struct {
	closeErr       error
	exchangeErr    error
	queueDeclErr   error
	queueBindErr   error
	qosErr         error
	consumeErr     error
	publishErr     error
	queue          amqp.Queue
	deliveries     <-chan amqp.Delivery
	exchanges      []string
	queuesDeclared []string
	queuesBound    []string
	published      []amqp.Publishing
}

func (f *fakeAMQPChannel) Close() error {
	return f.closeErr
}

func (f *fakeAMQPChannel) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error {
	if f.exchangeErr != nil {
		return f.exchangeErr
	}
	f.exchanges = append(f.exchanges, name)
	return nil
}

func (f *fakeAMQPChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error) {
	if f.queueDeclErr != nil {
		return amqp.Queue{}, f.queueDeclErr
	}
	f.queuesDeclared = append(f.queuesDeclared, name)
	return f.queue, nil
}

func (f *fakeAMQPChannel) QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error {
	if f.queueBindErr != nil {
		return f.queueBindErr
	}
	f.queuesBound = append(f.queuesBound, name)
	return nil
}

func (f *fakeAMQPChannel) Qos(prefetchCount, prefetchSize int, global bool) error {
	return f.qosErr
}

func (f *fakeAMQPChannel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error) {
	if f.consumeErr != nil {
		return nil, f.consumeErr
	}
	return f.deliveries, nil
}

func (f *fakeAMQPChannel) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error {
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, msg)
	return nil
}

func TestCloseConsumerChannel(t *testing.T) {
	t.Run("close succeeds", func(t *testing.T) {
		ch := &fakeAMQPChannel{}
		origErr := errors.New("original error")
		if err := closeConsumerChannel(ch, origErr); err != origErr {
			t.Fatalf("closeConsumerChannel error = %v, want original error", err)
		}
	})

	t.Run("close fails joins errors", func(t *testing.T) {
		ch := &fakeAMQPChannel{closeErr: errors.New("close failed")}
		origErr := errors.New("original error")
		err := closeConsumerChannel(ch, origErr)
		if err == nil {
			t.Fatal("expected joined error, got nil")
		}
		if !errors.Is(err, origErr) {
			t.Fatalf("error does not contain original: %v", err)
		}
		if !strings.Contains(err.Error(), "close consumer channel") {
			t.Fatalf("error missing close context: %v", err)
		}
	})
}

func TestBrokerWithFakeAMQP(t *testing.T) {
	t.Run("Subscribe channel open error", func(t *testing.T) {
		conn := &fakeAMQPConnection{chErr: errors.New("channel failed")}
		b := &Broker{opts: Options{Prefetch: 32}, conn: conn, declared: make(map[string]bool)}
		_, err := b.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "open consumer channel") {
			t.Fatalf("Subscribe error = %v, want channel open error", err)
		}
	})

	t.Run("Subscribe exchange declare error", func(t *testing.T) {
		ch := &fakeAMQPChannel{exchangeErr: errors.New("exchange failed")}
		conn := &fakeAMQPConnection{channel: ch}
		b := &Broker{opts: Options{Prefetch: 32}, conn: conn, declared: make(map[string]bool)}
		_, err := b.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "exchange") {
			t.Fatalf("Subscribe error = %v, want exchange error", err)
		}
	})

	t.Run("Subscribe queue declare error", func(t *testing.T) {
		ch := &fakeAMQPChannel{queueDeclErr: errors.New("queue failed")}
		conn := &fakeAMQPConnection{channel: ch}
		b := &Broker{opts: Options{Prefetch: 32}, conn: conn, declared: make(map[string]bool)}
		_, err := b.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "declare queue") {
			t.Fatalf("Subscribe error = %v, want queue declare error", err)
		}
	})

	t.Run("Subscribe queue bind error", func(t *testing.T) {
		ch := &fakeAMQPChannel{queueBindErr: errors.New("bind failed")}
		conn := &fakeAMQPConnection{channel: ch}
		b := &Broker{opts: Options{Prefetch: 32}, conn: conn, declared: make(map[string]bool)}
		_, err := b.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "bind queue") {
			t.Fatalf("Subscribe error = %v, want queue bind error", err)
		}
	})

	t.Run("Subscribe qos error", func(t *testing.T) {
		ch := &fakeAMQPChannel{qosErr: errors.New("qos failed")}
		conn := &fakeAMQPConnection{channel: ch}
		b := &Broker{opts: Options{Prefetch: 32}, conn: conn, declared: make(map[string]bool)}
		_, err := b.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "set qos") {
			t.Fatalf("Subscribe error = %v, want qos error", err)
		}
	})

	t.Run("Subscribe consume error", func(t *testing.T) {
		ch := &fakeAMQPChannel{consumeErr: errors.New("consume failed")}
		conn := &fakeAMQPConnection{channel: ch}
		b := &Broker{opts: Options{Prefetch: 32}, conn: conn, declared: make(map[string]bool)}
		_, err := b.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "consume") {
			t.Fatalf("Subscribe error = %v, want consume error", err)
		}
	})

	t.Run("Subscribe success", func(t *testing.T) {
		deliveries := make(chan amqp.Delivery)
		ch := &fakeAMQPChannel{deliveries: deliveries}
		conn := &fakeAMQPConnection{channel: ch}
		b := &Broker{opts: Options{Prefetch: 32}, conn: conn, declared: make(map[string]bool)}
		sub, err := b.Subscribe(context.Background(), "orders", "workers", func(context.Context, mq.Message) error { return nil })
		if err != nil {
			t.Fatalf("Subscribe error = %v", err)
		}
		if sub == nil {
			t.Fatal("expected non-nil subscription")
		}
		if len(ch.exchanges) != 1 || ch.exchanges[0] != "orders" {
			t.Fatalf("exchanges = %v, want [orders]", ch.exchanges)
		}
		if len(ch.queuesDeclared) != 1 || ch.queuesDeclared[0] != "orders.workers" {
			t.Fatalf("queues = %v, want [orders.workers]", ch.queuesDeclared)
		}
	})

	t.Run("Publish success", func(t *testing.T) {
		ch := &fakeAMQPChannel{}
		b := &Broker{opts: Options{Prefetch: 32}, pubCh: ch, declared: make(map[string]bool)}
		err := b.Publish(context.Background(), mq.Message{Topic: "orders", ID: "msg-1", Body: []byte("hello")})
		if err != nil {
			t.Fatalf("Publish error = %v", err)
		}
		if len(ch.published) != 1 {
			t.Fatalf("published count = %d, want 1", len(ch.published))
		}
	})

	t.Run("Publish exchange error", func(t *testing.T) {
		ch := &fakeAMQPChannel{exchangeErr: errors.New("exchange failed")}
		b := &Broker{opts: Options{Prefetch: 32}, pubCh: ch, declared: make(map[string]bool)}
		err := b.Publish(context.Background(), mq.Message{Topic: "orders", ID: "msg-1", Body: []byte("hello")})
		if err == nil || err.Error() != "exchange failed" {
			t.Fatalf("Publish error = %v, want exchange error", err)
		}
	})

	t.Run("Publish publish error", func(t *testing.T) {
		ch := &fakeAMQPChannel{publishErr: errors.New("publish failed")}
		b := &Broker{opts: Options{Prefetch: 32}, pubCh: ch, declared: make(map[string]bool)}
		err := b.Publish(context.Background(), mq.Message{Topic: "orders", ID: "msg-1", Body: []byte("hello")})
		if err == nil || err.Error() != "publish failed" {
			t.Fatalf("Publish error = %v, want publish error", err)
		}
	})

	t.Run("Close returns connection close error", func(t *testing.T) {
		conn := &fakeAMQPConnection{closeErr: errors.New("conn close failed")}
		ch := &fakeAMQPChannel{closeErr: errors.New("ch close failed")}
		b := &Broker{opts: Options{Prefetch: 32}, conn: conn, pubCh: ch, declared: make(map[string]bool)}
		err := b.Close(context.Background())
		if err == nil {
			t.Fatal("expected close error, got nil")
		}
	})

	t.Run("ensureExchange declares new exchange", func(t *testing.T) {
		ch := &fakeAMQPChannel{}
		b := &Broker{opts: Options{ExchangePrefix: "svc"}, declared: make(map[string]bool)}
		if err := b.ensureExchange(ch, "orders"); err != nil {
			t.Fatalf("ensureExchange error = %v", err)
		}
		if len(ch.exchanges) != 1 || ch.exchanges[0] != "svc.orders" {
			t.Fatalf("exchanges = %v, want [svc.orders]", ch.exchanges)
		}
		if !b.declared["orders"] {
			t.Fatal("expected orders to be marked declared")
		}
	})
}

func BenchmarkToAMQPHeaders(b *testing.B) {
	msg := mq.Message{ID: "id-1", Headers: map[string]string{"trace": "abc"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = toAMQPHeaders(msg)
	}
}

func BenchmarkFromAMQPDelivery(b *testing.B) {
	d := amqp.Delivery{MessageId: "id-1", Body: []byte("body"), Headers: amqp.Table{"h-trace": "abc"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = fromAMQPDelivery(d)
	}
}
