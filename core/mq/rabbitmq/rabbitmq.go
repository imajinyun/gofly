// Package rabbitmq provides a core/mq Broker backed by RabbitMQ using
// github.com/rabbitmq/amqp091-go. Topics map to durable direct exchanges and
// consumer groups map to durable queues bound to that exchange, giving each
// group an independent copy of the stream. Failed messages are retried in
// process and dead-lettered when configured.
package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/gofly/gofly/core/mq"
)

// Options configures the RabbitMQ broker.
type Options struct {
	// URL is the AMQP connection string (amqp://user:pass@host:port/vhost).
	URL string
	// ExchangePrefix is prepended to topic names when declaring exchanges.
	ExchangePrefix string
	// Prefetch bounds unacknowledged messages per consumer channel.
	Prefetch int
}

func (o Options) withDefaults() Options {
	if o.Prefetch <= 0 {
		o.Prefetch = 32
	}
	return o
}

func (o Options) exchange(topic string) string {
	if o.ExchangePrefix == "" {
		return topic
	}
	return o.ExchangePrefix + "." + topic
}

// amqpConnection abstracts *amqp.Connection for testability.
type amqpConnection interface {
	Channel() (amqpChannel, error)
	Close() error
}

// amqpChannel abstracts *amqp.Channel for testability.
type amqpChannel interface {
	Close() error
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error
	Qos(prefetchCount, prefetchSize int, global bool) error
	Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
	PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
}

// realAMQPConnection wraps *amqp.Connection to satisfy amqpConnection.
type realAMQPConnection struct{ *amqp.Connection }

func (c *realAMQPConnection) Channel() (amqpChannel, error) {
	ch, err := c.Connection.Channel()
	if err != nil {
		return nil, err
	}
	return ch, nil
}

// Broker is a core/mq.Broker backed by RabbitMQ.
// It manages a shared AMQP connection and lazily creates channels for
// publishing and consuming.
type Broker struct {
	opts Options

	mu       sync.Mutex
	conn     amqpConnection
	pubCh    amqpChannel
	subs     []*subscription
	declared map[string]bool
	closed   bool
}

var _ mq.Broker = (*Broker)(nil)

// New dials RabbitMQ and prepares a publishing channel.
func New(opts Options) (*Broker, error) {
	if opts.URL == "" {
		return nil, errors.New("rabbitmq: connection URL is required")
	}
	opts = opts.withDefaults()
	conn, err := amqp.Dial(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}
	return &Broker{opts: opts, conn: &realAMQPConnection{conn}, pubCh: ch, declared: make(map[string]bool)}, nil
}

func (b *Broker) ensureExchange(ch amqpChannel, topic string) error {
	b.mu.Lock()
	declared := b.declared[topic]
	b.mu.Unlock()
	if declared {
		return nil
	}
	if err := ch.ExchangeDeclare(b.opts.exchange(topic), amqp.ExchangeDirect, true, false, false, false, nil); err != nil {
		return err
	}
	b.mu.Lock()
	b.declared[topic] = true
	b.mu.Unlock()
	return nil
}

// Publish routes a message through the topic's direct exchange.
func (b *Broker) Publish(ctx context.Context, msg mq.Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if msg.Topic == "" {
		return mq.ErrInvalidTopic
	}
	mq.InjectTrace(ctx, &msg)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return mq.ErrClosed
	}
	ch := b.pubCh
	b.mu.Unlock()
	if err := b.ensureExchange(ch, msg.Topic); err != nil {
		return err
	}
	pub := amqp.Publishing{
		MessageId:    msg.ID,
		Body:         msg.Body,
		Headers:      toAMQPHeaders(msg),
		DeliveryMode: amqp.Persistent,
		Timestamp:    msg.PublishedAt,
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pubCh.PublishWithContext(ctx, b.opts.exchange(msg.Topic), msg.Topic, false, false, pub)
}

// Subscribe declares the group queue, binds it to the topic exchange and starts
// consuming.
func (b *Broker) Subscribe(ctx context.Context, topic, group string, handler mq.Handler, opts ...mq.SubscribeOption) (mq.Unsubscriber, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if topic == "" {
		return nil, mq.ErrInvalidTopic
	}
	if group == "" {
		return nil, mq.ErrInvalidGroup
	}
	if handler == nil {
		return nil, errors.New("rabbitmq: handler is nil")
	}
	b.mu.Lock()
	closed := b.closed
	conn := b.conn
	b.mu.Unlock()
	if closed {
		return nil, mq.ErrClosed
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: open consumer channel: %w", err)
	}
	if err := b.ensureExchange(ch, topic); err != nil {
		return nil, closeConsumerChannel(ch, err)
	}
	queue := topic + "." + group
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return nil, closeConsumerChannel(ch, fmt.Errorf("rabbitmq: declare queue: %w", err))
	}
	if err := ch.QueueBind(queue, topic, b.opts.exchange(topic), false, nil); err != nil {
		return nil, closeConsumerChannel(ch, fmt.Errorf("rabbitmq: bind queue: %w", err))
	}
	if err := ch.Qos(b.opts.Prefetch, 0, false); err != nil {
		return nil, closeConsumerChannel(ch, fmt.Errorf("rabbitmq: set qos: %w", err))
	}
	deliveries, err := ch.Consume(queue, group, false, false, false, false, nil)
	if err != nil {
		return nil, closeConsumerChannel(ch, fmt.Errorf("rabbitmq: consume: %w", err))
	}

	cfg := mq.BuildSubscriptionConfig(topic, opts...)
	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscription{
		broker:     b,
		topic:      topic,
		group:      group,
		channel:    ch,
		deliveries: deliveries,
		handler:    handler,
		cfg:        cfg,
		ctx:        subCtx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	workers := cfg.Concurrency()
	if workers < 1 {
		workers = 1
	}
	sub.sem = make(chan struct{}, workers)
	sub.wg.Add(1)
	go sub.run()
	return sub, nil
}

func closeConsumerChannel(ch amqpChannel, err error) error {
	if closeErr := ch.Close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("rabbitmq: close consumer channel: %w", closeErr))
	}
	return err
}

// Close stops subscriptions and tears down the connection.
func (b *Broker) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	subs := b.subs
	conn := b.conn
	pubCh := b.pubCh
	b.subs = nil
	b.mu.Unlock()

	var firstErr error
	for _, sub := range subs {
		if err := sub.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if pubCh != nil {
		if err := pubCh.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if conn != nil {
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type subscription struct {
	broker     *Broker
	topic      string
	group      string
	channel    amqpChannel
	deliveries <-chan amqp.Delivery
	handler    mq.Handler
	cfg        mq.SubscriptionConfig
	ctx        context.Context
	cancel     context.CancelFunc
	sem        chan struct{}
	wg         sync.WaitGroup
	once       sync.Once
	done       chan struct{}
}

func (s *subscription) run() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case d, ok := <-s.deliveries:
			if !ok {
				return
			}
			s.sem <- struct{}{}
			s.wg.Add(1)
			go func(delivery amqp.Delivery) {
				defer s.wg.Done()
				defer func() { <-s.sem }()
				s.process(delivery)
			}(d)
		}
	}
}

func (s *subscription) process(d amqp.Delivery) {
	msg := fromAMQPDelivery(d)
	msg.Topic = s.topic
	ctx := mq.ExtractTrace(s.ctx, msg)
	attempts := s.cfg.MaxAttempts()
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		msg.Attempts = attempt + 1
		if err = s.handler(ctx, msg); err == nil {
			break
		}
		if ctx.Err() != nil {
			if nerr := d.Nack(false, true); nerr != nil {
				slog.ErrorContext(ctx, "rabbitmq nack failed", "topic", s.topic, "group", s.group, "error", nerr)
			}
			return
		}
		if backoff := s.cfg.RetryBackoff(); backoff > 0 && attempt < attempts-1 {
			if !sleepContext(ctx, backoff) {
				if nerr := d.Nack(false, true); nerr != nil {
					slog.ErrorContext(ctx, "rabbitmq nack failed", "topic", s.topic, "group", s.group, "error", nerr)
				}
				return
			}
		}
	}
	if err != nil && s.cfg.DeadLetterTopic() != "" {
		dlq := msg
		dlq.Topic = s.cfg.DeadLetterTopic()
		if perr := s.broker.Publish(context.WithoutCancel(ctx), dlq); perr != nil {
			slog.ErrorContext(ctx, "rabbitmq dead-letter publish failed", "topic", s.topic, "group", s.group, "dead_letter_topic", dlq.Topic, "error", perr)
			if nerr := d.Nack(false, true); nerr != nil {
				slog.ErrorContext(ctx, "rabbitmq nack after dead-letter failure failed", "topic", s.topic, "group", s.group, "error", nerr)
			}
			return
		}
	}
	// Ack regardless: failures are dead-lettered, so the message must leave the
	// queue to avoid blocking the consumer.
	if aerr := d.Ack(false); aerr != nil {
		slog.ErrorContext(ctx, "rabbitmq ack failed", "topic", s.topic, "group", s.group, "error", aerr)
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Stop cancels consumption and waits for in-flight handlers.
func (s *subscription) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var closeErr error
	s.once.Do(func() {
		s.cancel()
		closeErr = s.channel.Close()
		go func() {
			s.wg.Wait()
			close(s.done)
		}()
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return closeErr
	}
}

func toAMQPHeaders(msg mq.Message) amqp.Table {
	table := amqp.Table{"mq-attempts": amqpAttempts(msg.Attempts)}
	for k, v := range msg.Headers {
		table["h-"+k] = v
	}
	return table
}

func amqpAttempts(attempts int) int32 {
	if attempts < 0 {
		return 0
	}
	if attempts > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(attempts)
}

func fromAMQPDelivery(d amqp.Delivery) mq.Message {
	msg := mq.Message{ID: d.MessageId, Body: d.Body, PublishedAt: d.Timestamp}
	for k, v := range d.Headers {
		if len(k) > 2 && k[:2] == "h-" {
			if msg.Headers == nil {
				msg.Headers = make(map[string]string)
			}
			if s, ok := v.(string); ok {
				msg.Headers[k[2:]] = s
			}
		}
	}
	return msg
}
