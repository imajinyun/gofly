// Package mq provides a transport-agnostic message broker abstraction with
// in-memory, Kafka, RabbitMQ, and Redis Stream implementations. It supports
// publish/subscribe, dead-letter queues, retry backoff, and W3C trace context
// propagation through message headers.
package mq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	core "github.com/gofly/gofly/core"
	"github.com/gofly/gofly/core/observability/trace"
)

// Broker-level errors returned by all implementations.
var (
	// ErrClosed is returned when an operation is performed on a closed broker.
	ErrClosed = errors.New("message broker closed")
	// ErrInvalidTopic is returned when a topic name is empty.
	ErrInvalidTopic = errors.New("message topic is empty")
	// ErrInvalidGroup is returned when a consumer group name is empty.
	ErrInvalidGroup = errors.New("message consumer group is empty")
	// ErrOverloaded is returned when the broker cannot accept more messages.
	ErrOverloaded = errors.New("message broker overloaded")
)

// Message is a single message envelope for publish/subscribe.
type Message struct {
	ID          string            `json:"id"`
	Topic       string            `json:"topic"`
	Key         string            `json:"key,omitempty"`
	Body        []byte            `json:"body,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Attempts    int               `json:"attempts"`
	PublishedAt time.Time         `json:"publishedAt"`
}

// Handler processes a consumed message. The context carries any trace
// context extracted from the message headers.
type Handler func(context.Context, Message) error

// Unsubscriber stops a previously started subscription. Driver-backed brokers
// (Kafka, RabbitMQ, Redis Stream) return an implementation that closes the
// underlying consumer; the in-memory *Subscription also satisfies it.
type Unsubscriber interface {
	Stop(context.Context) error
}

// Broker is the transport-agnostic message broker contract shared by the
// in-memory implementation and the external driver adapters. It lets callers
// depend on core/mq alone while swapping the concrete transport at wiring time.
type Broker interface {
	Publish(ctx context.Context, msg Message) error
	Subscribe(ctx context.Context, topic, group string, handler Handler, opts ...SubscribeOption) (Unsubscriber, error)
	Close(ctx context.Context) error
}

// Config carries the common subscription tuning that driver adapters reuse so
// retry/concurrency semantics stay consistent with the in-memory broker.
func newSubscriptionConfig(topic string, opts ...SubscribeOption) subscriptionConfig {
	cfg := subscriptionConfig{buffer: 64, concurrency: 1, maxAttempts: 3, retryBackoff: 50 * time.Millisecond, deadLetterTopic: topic + ".dlq"}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// Buffer exposes the configured channel buffer for driver adapters.
func (c subscriptionConfig) Buffer() int { return c.buffer }

// Concurrency exposes the configured worker count for driver adapters.
func (c subscriptionConfig) Concurrency() int { return c.concurrency }

// MaxAttempts exposes the configured max delivery attempts for driver adapters.
func (c subscriptionConfig) MaxAttempts() int { return c.maxAttempts }

// RetryBackoff exposes the configured retry backoff for driver adapters.
func (c subscriptionConfig) RetryBackoff() time.Duration { return c.retryBackoff }

// DeadLetterTopic exposes the configured dead-letter topic for driver adapters.
func (c subscriptionConfig) DeadLetterTopic() string { return c.deadLetterTopic }

// SubscriptionConfig is the exported view of subscription options for adapters
// that live in other packages.
type SubscriptionConfig = subscriptionConfig

// BuildSubscriptionConfig resolves SubscribeOption values into a concrete
// configuration for driver adapters defined outside this package.
func BuildSubscriptionConfig(topic string, opts ...SubscribeOption) SubscriptionConfig {
	return newSubscriptionConfig(topic, opts...)
}

// BrokerStats holds runtime statistics for a broker.
type BrokerStats struct {
	Published int64                    `json:"published"`
	Delivered int64                    `json:"delivered"`
	Acked     int64                    `json:"acked"`
	Nacked    int64                    `json:"nacked"`
	Retried   int64                    `json:"retried"`
	Dead      int64                    `json:"dead"`
	Dropped   int64                    `json:"dropped"`
	Topics    map[string]TopicStats    `json:"topics"`
	Groups    map[string]ConsumerStats `json:"groups"`
}

// TopicStats holds runtime statistics for a topic.
type TopicStats struct {
	Published int64 `json:"published"`
	Dead      int64 `json:"dead"`
}

// ConsumerStats holds runtime statistics for a consumer group.
type ConsumerStats struct {
	Delivered int64 `json:"delivered"`
	Acked     int64 `json:"acked"`
	Nacked    int64 `json:"nacked"`
	Retried   int64 `json:"retried"`
	Dead      int64 `json:"dead"`
	Dropped   int64 `json:"dropped"`
}

// SubscribeOption customizes subscription behaviour.
type SubscribeOption func(*subscriptionConfig)

type subscriptionConfig struct {
	buffer          int
	concurrency     int
	maxAttempts     int
	retryBackoff    time.Duration
	deadLetterTopic string
}

// MemoryBroker is an in-memory message broker for testing and single-process
// deployments.
type MemoryBroker struct {
	mu          sync.RWMutex
	closed      bool
	subscribers map[string]map[string]*Subscription
	stats       BrokerStats
	newID       func() (string, error)
}

// Subscription represents an active in-memory message subscription.
type Subscription struct {
	broker *MemoryBroker
	topic  string
	group  string
	cfg    subscriptionConfig
	ch     chan Message
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
	closed chan struct{}
}

// NewMemoryBroker creates a new in-memory broker.
func NewMemoryBroker() *MemoryBroker {
	return &MemoryBroker{
		subscribers: make(map[string]map[string]*Subscription),
		stats: BrokerStats{
			Topics: make(map[string]TopicStats),
			Groups: make(map[string]ConsumerStats),
		},
		newID: randomID,
	}
}

// WithBuffer sets the subscription channel buffer size.
func WithBuffer(size int) SubscribeOption {
	return func(cfg *subscriptionConfig) {
		if size > 0 {
			cfg.buffer = size
		}
	}
}

// WithConcurrency sets the number of concurrent message handlers.
func WithConcurrency(n int) SubscribeOption {
	return func(cfg *subscriptionConfig) {
		if n > 0 {
			cfg.concurrency = n
		}
	}
}

// WithMaxAttempts sets the maximum delivery attempts before dead-lettering.
func WithMaxAttempts(n int) SubscribeOption {
	return func(cfg *subscriptionConfig) {
		if n > 0 {
			cfg.maxAttempts = n
		}
	}
}

// WithRetryBackoff sets the delay between retry attempts.
func WithRetryBackoff(backoff time.Duration) SubscribeOption {
	return func(cfg *subscriptionConfig) {
		if backoff >= 0 {
			cfg.retryBackoff = backoff
		}
	}
}

// WithDeadLetterTopic sets the topic for failed messages.
func WithDeadLetterTopic(topic string) SubscribeOption {
	return func(cfg *subscriptionConfig) {
		cfg.deadLetterTopic = topic
	}
}

func (b *MemoryBroker) Publish(ctx context.Context, msg Message) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if msg.Topic == "" {
		return ErrInvalidTopic
	}
	prepared, err := b.prepare(msg)
	if err != nil {
		return err
	}
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return ErrClosed
	}
	subs := make([]*Subscription, 0, len(b.subscribers[prepared.Topic]))
	for _, sub := range b.subscribers[prepared.Topic] {
		subs = append(subs, sub)
	}
	b.mu.RUnlock()
	b.recordPublish(prepared.Topic)
	for _, sub := range subs {
		if err := sub.enqueue(ctx, prepared); err != nil {
			b.recordDrop(prepared.Topic, sub.group)
		}
	}
	return nil
}

func (b *MemoryBroker) Subscribe(ctx context.Context, topic, group string, handler Handler, opts ...SubscribeOption) (*Subscription, error) {
	ctx = core.Context(ctx)
	if topic == "" {
		return nil, ErrInvalidTopic
	}
	if group == "" {
		return nil, ErrInvalidGroup
	}
	if handler == nil {
		return nil, errors.New("message handler is nil")
	}
	cfg := newSubscriptionConfig(topic, opts...)
	subCtx, cancel := context.WithCancel(ctx)
	sub := &Subscription{broker: b, topic: topic, group: group, cfg: cfg, ch: make(chan Message, cfg.buffer), ctx: subCtx, cancel: cancel, closed: make(chan struct{})}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		cancel()
		return nil, ErrClosed
	}
	if b.subscribers[topic] == nil {
		b.subscribers[topic] = make(map[string]*Subscription)
	}
	old := b.subscribers[topic][group]
	b.subscribers[topic][group] = sub
	for range cfg.concurrency {
		sub.wg.Add(1)
		go sub.consume(handler)
	}
	b.mu.Unlock()
	if old != nil {
		if err := old.Stop(ctx); err != nil {
			_ = sub.Stop(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("stop previous subscription %s/%s: %w", topic, group, err)
		}
	}
	return sub, nil
}

// SubscribeBroker adapts Subscribe to the Broker interface by widening the
// concrete *Subscription return to the Unsubscriber contract.
func (b *MemoryBroker) SubscribeBroker(ctx context.Context, topic, group string, handler Handler, opts ...SubscribeOption) (Unsubscriber, error) {
	return b.Subscribe(ctx, topic, group, handler, opts...)
}

// memoryBrokerAdapter exposes *MemoryBroker through the Broker interface.
type memoryBrokerAdapter struct{ *MemoryBroker }

func (a memoryBrokerAdapter) Subscribe(ctx context.Context, topic, group string, handler Handler, opts ...SubscribeOption) (Unsubscriber, error) {
	return a.MemoryBroker.Subscribe(ctx, topic, group, handler, opts...)
}

// AsBroker wraps a *MemoryBroker so it satisfies the Broker interface.
func AsBroker(b *MemoryBroker) Broker { return memoryBrokerAdapter{b} }

func (b *MemoryBroker) Close(ctx context.Context) error {
	ctx = core.Context(ctx)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	subs := make([]*Subscription, 0)
	for _, groups := range b.subscribers {
		for _, sub := range groups {
			subs = append(subs, sub)
		}
	}
	b.subscribers = make(map[string]map[string]*Subscription)
	b.mu.Unlock()
	for _, sub := range subs {
		if err := sub.Stop(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (b *MemoryBroker) Snapshot() BrokerStats {
	b.mu.RLock()
	defer b.mu.RUnlock()
	snapshot := b.stats
	snapshot.Topics = make(map[string]TopicStats, len(b.stats.Topics))
	for key, value := range b.stats.Topics {
		snapshot.Topics[key] = value
	}
	snapshot.Groups = make(map[string]ConsumerStats, len(b.stats.Groups))
	for key, value := range b.stats.Groups {
		snapshot.Groups[key] = value
	}
	return snapshot
}

func (s *Subscription) Stop(ctx context.Context) error {
	ctx = core.Context(ctx)
	s.once.Do(func() {
		s.cancel()
		close(s.closed)
		s.broker.remove(s.topic, s.group, s)
	})
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (s *Subscription) enqueue(ctx context.Context, msg Message) error {
	select {
	case <-s.closed:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case s.ch <- cloneMessage(msg):
		return nil
	}
}

func (s *Subscription) consume(handler Handler) {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg := <-s.ch:
			s.handle(handler, msg)
		}
	}
}

func (s *Subscription) handle(handler Handler, msg Message) {
	s.broker.recordDelivery(msg.Topic, s.group)
	if err := handler(s.ctx, cloneMessage(msg)); err == nil {
		s.broker.recordAck(msg.Topic, s.group)
		return
	}
	s.broker.recordNack(msg.Topic, s.group)
	msg.Attempts++
	if msg.Attempts >= s.cfg.maxAttempts {
		s.broker.recordDead(msg.Topic, s.group)
		if s.cfg.deadLetterTopic != "" {
			originalTopic := msg.Topic
			msg.Topic = s.cfg.deadLetterTopic
			if err := s.broker.Publish(context.WithoutCancel(s.ctx), msg); err != nil {
				s.broker.recordDrop(originalTopic, s.group)
			}
		}
		return
	}
	s.broker.recordRetry(msg.Topic, s.group)
	timer := time.NewTimer(s.cfg.retryBackoff)
	select {
	case <-s.ctx.Done():
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	case <-timer.C:
		_ = s.enqueue(s.ctx, msg)
	}
}

func (b *MemoryBroker) prepare(msg Message) (Message, error) {
	if msg.ID == "" {
		id, err := b.newID()
		if err != nil {
			return Message{}, fmt.Errorf("generate message id: %w", err)
		}
		msg.ID = id
	}
	if msg.PublishedAt.IsZero() {
		msg.PublishedAt = time.Now()
	}
	return cloneMessage(msg), nil
}

func (b *MemoryBroker) remove(topic, group string, sub *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if groups := b.subscribers[topic]; groups != nil && groups[group] == sub {
		delete(groups, group)
		if len(groups) == 0 {
			delete(b.subscribers, topic)
		}
	}
}

func (b *MemoryBroker) recordPublish(topic string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stats.Published++
	stats := b.stats.Topics[topic]
	stats.Published++
	b.stats.Topics[topic] = stats
}

func (b *MemoryBroker) recordDelivery(topic, group string) {
	b.updateGroup(topic, group, func(s *ConsumerStats) { s.Delivered++; b.stats.Delivered++ })
}
func (b *MemoryBroker) recordAck(topic, group string) {
	b.updateGroup(topic, group, func(s *ConsumerStats) { s.Acked++; b.stats.Acked++ })
}
func (b *MemoryBroker) recordNack(topic, group string) {
	b.updateGroup(topic, group, func(s *ConsumerStats) { s.Nacked++; b.stats.Nacked++ })
}
func (b *MemoryBroker) recordRetry(topic, group string) {
	b.updateGroup(topic, group, func(s *ConsumerStats) { s.Retried++; b.stats.Retried++ })
}
func (b *MemoryBroker) recordDrop(topic, group string) {
	b.updateGroup(topic, group, func(s *ConsumerStats) { s.Dropped++; b.stats.Dropped++ })
}

func (b *MemoryBroker) recordDead(topic, group string) {
	b.updateGroup(topic, group, func(s *ConsumerStats) {
		s.Dead++
		b.stats.Dead++
		topicStats := b.stats.Topics[topic]
		topicStats.Dead++
		b.stats.Topics[topic] = topicStats
	})
}

func (b *MemoryBroker) updateGroup(topic, group string, fn func(*ConsumerStats)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := topic + ":" + group
	stats := b.stats.Groups[key]
	fn(&stats)
	b.stats.Groups[key] = stats
}

func cloneMessage(msg Message) Message {
	msg.Body = append([]byte(nil), msg.Body...)
	if len(msg.Headers) > 0 {
		headers := msg.Headers
		msg.Headers = make(map[string]string, len(msg.Headers))
		for key, value := range headers {
			msg.Headers[key] = value
		}
	}
	return msg
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// traceHeaderKey is the Message.Headers key used to propagate trace context.
const traceHeaderKey = "traceparent"

// InjectTrace extracts any trace context from ctx and injects it into msg.Headers
// so that downstream consumers can continue the trace.
func InjectTrace(ctx context.Context, msg *Message) {
	if sc, ok := trace.FromContext(ctx); ok {
		if msg.Headers == nil {
			msg.Headers = make(map[string]string)
		}
		msg.Headers[traceHeaderKey] = trace.TraceParent(sc)
	}
}

// ExtractTrace extracts a trace context from msg.Headers and returns a new
// context carrying that trace. If no trace header is present, ctx is returned
// unchanged.
func ExtractTrace(ctx context.Context, msg Message) context.Context {
	if parent, ok := msg.Headers[traceHeaderKey]; ok {
		ctx, _ = trace.Start(ctx, parent)
	}
	return ctx
}
