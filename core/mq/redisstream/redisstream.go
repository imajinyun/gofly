// Package redisstream provides a core/mq Broker backed by Redis Streams using
// the dependency-free core/redis client. It maps consumer groups to Redis
// stream consumer groups and preserves the retry/dead-letter semantics of the
// in-memory broker.
package redisstream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/kv/redis"
	"github.com/imajinyun/gofly/core/mq"
)

// Client is the subset of *redis.Client the broker depends on. It is satisfied
// by *redis.Client and can be faked in tests.
type Client interface {
	XAdd(ctx context.Context, stream string, maxLen int64, fields map[string]string) (string, error)
	XGroupCreate(ctx context.Context, stream, group, start string, mkStream bool) error
	XReadGroup(ctx context.Context, group, consumer, stream string, count int, block time.Duration) ([]redis.StreamEntry, error)
	XAck(ctx context.Context, stream, group string, ids ...string) (int64, error)
}

// Options tunes the Redis Stream broker.
// Zero values use sensible defaults suitable for most workloads.
type Options struct {
	// MaxLen approximately caps each stream length (0 = unbounded).
	MaxLen int64
	// Consumer is this process's consumer name within a group. When empty a
	// random name is generated per subscription.
	Consumer string
	// BlockInterval is the XREADGROUP blocking duration per poll.
	BlockInterval time.Duration
	// ReadCount is the max entries fetched per poll.
	ReadCount int
}

func (o Options) withDefaults() Options {
	if o.BlockInterval <= 0 {
		o.BlockInterval = 2 * time.Second
	}
	if o.ReadCount <= 0 {
		o.ReadCount = 16
	}
	return o
}

// Broker is a core/mq.Broker backed by Redis Streams.
type Broker struct {
	client Client
	opts   Options

	mu     sync.Mutex
	subs   []*subscription
	closed bool
}

var _ mq.Broker = (*Broker)(nil)

// New creates a Redis Stream broker over the given client.
func New(client Client, opts Options) (*Broker, error) {
	if client == nil {
		return nil, errors.New("redisstream: client is nil")
	}
	return &Broker{client: client, opts: opts.withDefaults()}, nil
}

// Publish appends a message to the topic's stream.
func (b *Broker) Publish(ctx context.Context, msg mq.Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if msg.Topic == "" {
		return mq.ErrInvalidTopic
	}
	mq.InjectTrace(ctx, &msg)
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return mq.ErrClosed
	}
	fields := encode(msg)
	_, err := b.client.XAdd(ctx, msg.Topic, b.opts.MaxLen, fields)
	return err
}

// Subscribe joins (or creates) the consumer group for topic and starts polling.
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
		return nil, errors.New("redisstream: handler is nil")
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, mq.ErrClosed
	}
	b.mu.Unlock()

	if err := b.client.XGroupCreate(ctx, topic, group, "$", true); err != nil {
		return nil, fmt.Errorf("redisstream: create group: %w", err)
	}

	cfg := mq.BuildSubscriptionConfig(topic, opts...)
	consumer := b.opts.Consumer
	if consumer == "" {
		consumer = fmt.Sprintf("%s-%d", group, time.Now().UnixNano())
	}
	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscription{
		broker:   b,
		topic:    topic,
		group:    group,
		consumer: consumer,
		handler:  handler,
		cfg:      cfg,
		ctx:      subCtx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	workers := cfg.Concurrency()
	if workers < 1 {
		workers = 1
	}
	sub.wg.Add(workers)
	for range workers {
		go sub.poll()
	}
	return sub, nil
}

// Close stops all subscriptions. It does not delete server-side groups.
func (b *Broker) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	subs := b.subs
	b.subs = nil
	b.mu.Unlock()
	for _, sub := range subs {
		if err := sub.Stop(ctx); err != nil {
			return err
		}
	}
	return nil
}

type subscription struct {
	broker   *Broker
	topic    string
	group    string
	consumer string
	handler  mq.Handler
	cfg      mq.SubscriptionConfig
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	once     sync.Once
	done     chan struct{}
}

func (s *subscription) poll() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		entries, err := s.broker.client.XReadGroup(s.ctx, s.group, s.consumer, s.topic, s.broker.opts.ReadCount, s.broker.opts.BlockInterval)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			// Back off briefly on transient read errors to avoid a hot loop.
			if !sleepContext(s.ctx, s.broker.opts.BlockInterval) {
				return
			}
			continue
		}
		for _, entry := range entries {
			s.process(entry)
		}
	}
}

func (s *subscription) process(entry redis.StreamEntry) {
	msg := decode(entry)
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
		if s.ctx.Err() != nil {
			return
		}
		if backoff := s.cfg.RetryBackoff(); backoff > 0 && attempt < attempts-1 {
			if !sleepContext(s.ctx, backoff) {
				return
			}
		}
	}
	if err != nil && s.cfg.DeadLetterTopic() != "" {
		dlqFields := encode(msg)
		if _, xerr := s.broker.client.XAdd(context.WithoutCancel(s.ctx), s.cfg.DeadLetterTopic(), s.broker.opts.MaxLen, dlqFields); xerr != nil {
			slog.ErrorContext(ctx, "redis stream dead-letter publish failed", "topic", s.topic, "group", s.group, "dead_letter_topic", s.cfg.DeadLetterTopic(), "entry_id", entry.ID, "error", xerr)
			return
		}
	}
	// Acknowledge regardless of outcome: failures are routed to the DLQ so the
	// entry must not block the consumer group's pending list indefinitely.
	if _, xerr := s.broker.client.XAck(context.WithoutCancel(s.ctx), s.topic, s.group, entry.ID); xerr != nil {
		slog.ErrorContext(ctx, "redis stream ack failed", "topic", s.topic, "group", s.group, "entry_id", entry.ID, "error", xerr)
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

// Stop cancels the subscription and waits for in-flight workers to drain.
func (s *subscription) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.once.Do(func() {
		s.cancel()
		go func() {
			s.wg.Wait()
			close(s.done)
		}()
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return nil
	}
}
