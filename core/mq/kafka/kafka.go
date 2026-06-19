// Package kafka provides a core/mq Broker backed by Apache Kafka using
// github.com/segmentio/kafka-go. Each consumer group maps to a Kafka consumer
// group; publishing uses a per-topic writer. Failed messages are retried in
// process and routed to a dead-letter topic when configured.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/gofly/gofly/core/mq"
)

// Options configures the Kafka broker.
// Zero values use sensible defaults suitable for most workloads.
type Options struct {
	// Brokers is the list of bootstrap broker addresses (host:port).
	Brokers []string
	// Dialer/Transport tuning is left to kafka-go defaults; WriteTimeout and
	// ReadTimeout bound individual operations.
	WriteTimeout time.Duration
	ReadTimeout  time.Duration
	// MinBytes/MaxBytes bound each fetch for consumers.
	MinBytes int
	MaxBytes int
	// Balancer selects the partition for produced messages; defaults to hash by
	// message key when nil.
	Balancer kafkago.Balancer
}

func (o Options) withDefaults() Options {
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 10 * time.Second
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = 10 * time.Second
	}
	if o.MinBytes <= 0 {
		o.MinBytes = 1
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 10 << 20 // 10MiB
	}
	if o.Balancer == nil {
		o.Balancer = &kafkago.Hash{}
	}
	return o
}

// Broker is a core/mq.Broker backed by Kafka.
// It lazily creates per-topic writers and manages consumer group readers.
type Broker struct {
	opts Options

	mu      sync.Mutex
	writers map[string]*kafkago.Writer
	subs    []*subscription
	closed  bool
}

var _ mq.Broker = (*Broker)(nil)

// New creates a Kafka broker. It does not connect eagerly.
func New(opts Options) (*Broker, error) {
	if len(opts.Brokers) == 0 {
		return nil, errors.New("kafka: at least one broker address is required")
	}
	return &Broker{opts: opts.withDefaults(), writers: make(map[string]*kafkago.Writer)}, nil
}

// Publish writes a message to the topic, creating the topic writer on demand.
func (b *Broker) Publish(ctx context.Context, msg mq.Message) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if msg.Topic == "" {
		return mq.ErrInvalidTopic
	}
	mq.InjectTrace(ctx, &msg)
	w, err := b.writer(msg.Topic)
	if err != nil {
		return err
	}
	return w.WriteMessages(ctx, toKafkaMessage(msg))
}

func (b *Broker) writer(topic string) (*kafkago.Writer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, mq.ErrClosed
	}
	if w, ok := b.writers[topic]; ok {
		return w, nil
	}
	w := &kafkago.Writer{
		Addr:         kafkago.TCP(b.opts.Brokers...),
		Topic:        topic,
		Balancer:     b.opts.Balancer,
		WriteTimeout: b.opts.WriteTimeout,
		ReadTimeout:  b.opts.ReadTimeout,
		RequiredAcks: kafkago.RequireOne,
	}
	b.writers[topic] = w
	return w, nil
}

// Subscribe starts a Kafka consumer group reader for the topic.
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
		return nil, errors.New("kafka: handler is nil")
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, mq.ErrClosed
	}
	b.mu.Unlock()

	cfg := mq.BuildSubscriptionConfig(topic, opts...)
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  b.opts.Brokers,
		Topic:    topic,
		GroupID:  group,
		MinBytes: b.opts.MinBytes,
		MaxBytes: b.opts.MaxBytes,
	})
	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscription{
		broker:  b,
		topic:   topic,
		group:   group,
		reader:  reader,
		handler: handler,
		cfg:     cfg,
		ctx:     subCtx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()

	workers := cfg.Concurrency()
	if workers < 1 {
		workers = 1
	}
	// kafka-go readers are not safe for concurrent FetchMessage from multiple
	// goroutines, so a single reader loop drives processing; concurrency is
	// applied to handler execution via a bounded worker pool.
	sub.sem = make(chan struct{}, workers)
	sub.wg.Add(1)
	go sub.run()
	return sub, nil
}

// Close stops all subscriptions and closes all writers.
func (b *Broker) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	subs := b.subs
	writers := b.writers
	b.subs = nil
	b.writers = make(map[string]*kafkago.Writer)
	b.mu.Unlock()

	var firstErr error
	for _, sub := range subs {
		if err := sub.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, w := range writers {
		if err := w.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type subscription struct {
	broker  *Broker
	topic   string
	group   string
	reader  *kafkago.Reader
	handler mq.Handler
	cfg     mq.SubscriptionConfig
	ctx     context.Context
	cancel  context.CancelFunc
	sem     chan struct{}
	wg      sync.WaitGroup
	once    sync.Once
	done    chan struct{}
}

func (s *subscription) run() {
	defer s.wg.Done()
	for {
		m, err := s.reader.FetchMessage(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			if !sleepContext(s.ctx, 200*time.Millisecond) {
				return
			}
			continue
		}
		s.sem <- struct{}{}
		s.wg.Add(1)
		go func(km kafkago.Message) {
			defer s.wg.Done()
			defer func() { <-s.sem }()
			s.process(km)
		}(m)
	}
}

func (s *subscription) process(km kafkago.Message) {
	msg := fromKafkaMessage(km)
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
		dlq := msg
		dlq.Topic = s.cfg.DeadLetterTopic()
		w, werr := s.broker.writer(dlq.Topic)
		if werr != nil {
			slog.ErrorContext(ctx, "kafka dead-letter writer unavailable", "topic", s.topic, "dead_letter_topic", dlq.Topic, "error", werr)
			return
		}
		if werr = w.WriteMessages(context.WithoutCancel(s.ctx), toKafkaMessage(dlq)); werr != nil {
			slog.ErrorContext(ctx, "kafka dead-letter publish failed", "topic", s.topic, "dead_letter_topic", dlq.Topic, "error", werr)
			return
		}
	}
	// Commit regardless: failed messages are routed to the DLQ, so advancing
	// the offset prevents head-of-line blocking.
	if cerr := s.reader.CommitMessages(context.WithoutCancel(s.ctx), km); cerr != nil {
		slog.ErrorContext(ctx, "kafka commit failed", "topic", s.topic, "group", s.group, "error", cerr)
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

// Stop cancels the reader loop and waits for workers to drain.
func (s *subscription) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var closeErr error
	s.once.Do(func() {
		s.cancel()
		closeErr = s.reader.Close()
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

func toKafkaMessage(msg mq.Message) kafkago.Message {
	headers := make([]kafkago.Header, 0, len(msg.Headers)+4)
	headers = append(headers,
		kafkago.Header{Key: "mq-id", Value: []byte(msg.ID)},
		kafkago.Header{Key: "mq-attempts", Value: []byte(fmt.Sprintf("%d", msg.Attempts))},
	)
	if !msg.PublishedAt.IsZero() {
		headers = append(headers, kafkago.Header{Key: "mq-ts", Value: []byte(msg.PublishedAt.Format(time.RFC3339Nano))})
	}
	for k, v := range msg.Headers {
		headers = append(headers, kafkago.Header{Key: "h-" + k, Value: []byte(v)})
	}
	return kafkago.Message{Key: []byte(msg.Key), Value: msg.Body, Headers: headers}
}

func fromKafkaMessage(km kafkago.Message) mq.Message {
	msg := mq.Message{Key: string(km.Key), Body: km.Value, PublishedAt: km.Time}
	for _, h := range km.Headers {
		switch {
		case h.Key == "mq-id":
			msg.ID = string(h.Value)
		case len(h.Key) > 2 && h.Key[:2] == "h-":
			if msg.Headers == nil {
				msg.Headers = make(map[string]string)
			}
			msg.Headers[h.Key[2:]] = string(h.Value)
		}
	}
	return msg
}
