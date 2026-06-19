// Package outbox implements the transactional outbox pattern with pluggable
// stores (memory, SQL) and a relay that forwards messages to a Publisher.
package outbox

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Publisher forwards an outbox message to a transport. It is intentionally
// narrow so the relay does not depend on a concrete broker; see BrokerPublisher
// for a core/mq adapter.
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}

// PublisherFunc adapts a function to the Publisher interface.
type PublisherFunc func(ctx context.Context, msg Message) error

// Publish implements Publisher.
func (f PublisherFunc) Publish(ctx context.Context, msg Message) error { return f(ctx, msg) }

// RelayConfig tunes the polling relay.
type RelayConfig struct {
	// BatchSize is the maximum number of records claimed per poll.
	BatchSize int
	// PollInterval is the idle wait between polls when no work is found.
	PollInterval time.Duration
	// Visibility is the lease duration for claimed records; it should exceed the
	// expected publish latency so records are not re-claimed while in flight.
	Visibility time.Duration
	// MaxAttempts is the delivery attempt budget before dead-lettering.
	MaxAttempts int
	// BaseBackoff is the first retry delay; it doubles each attempt up to
	// MaxBackoff.
	BaseBackoff time.Duration
	// MaxBackoff caps the exponential retry delay.
	MaxBackoff time.Duration
	// Logger receives delivery failures; defaults to slog.Default().
	Logger *slog.Logger
}

func (c RelayConfig) withDefaults() RelayConfig {
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.Visibility <= 0 {
		c.Visibility = 30 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = time.Minute
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Relay polls a Store for pending records and publishes them, applying retries
// with exponential backoff and dead-lettering on exhaustion.
type Relay struct {
	store     Store
	publisher Publisher
	cfg       RelayConfig

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
	now     func() time.Time
}

// NewRelay constructs a Relay over the given store and publisher.
func NewRelay(store Store, publisher Publisher, cfg RelayConfig) *Relay {
	return &Relay{
		store:     store,
		publisher: publisher,
		cfg:       cfg.withDefaults(),
		now:       time.Now,
	}
}

// Start launches the polling loop in a background goroutine. It returns
// immediately; call Stop to shut the loop down.
func (r *Relay) Start(ctx context.Context) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.stop = make(chan struct{})
	r.done = make(chan struct{})
	r.mu.Unlock()

	go r.loop(ctx)
}

// Stop signals the polling loop to exit and waits for it to finish or the
// context to expire.
func (r *Relay) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return nil
	}
	r.running = false
	stop, done := r.stop, r.done
	r.mu.Unlock()

	close(stop)
	if ctx == nil {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Relay) loop(ctx context.Context) {
	defer close(r.done)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-timer.C:
		}
		processed, err := r.ProcessBatch(ctx)
		if err != nil && ctx.Err() == nil {
			r.cfg.Logger.WarnContext(ctx, "outbox relay batch failed", "error", err)
		}
		// When the batch was full there may be more work; poll again promptly.
		wait := r.cfg.PollInterval
		if processed >= r.cfg.BatchSize {
			wait = 0
		}
		timer.Reset(wait)
	}
}

// ProcessBatch claims and publishes one batch of due records, returning the
// number of records processed. It is exported so callers can drive the relay
// manually (e.g. in tests or a custom scheduler).
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	records, err := r.store.Fetch(ctx, r.cfg.BatchSize, r.cfg.Visibility)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		r.deliver(ctx, rec)
		processed++
	}
	return processed, nil
}

func (r *Relay) deliver(ctx context.Context, rec Record) {
	err := r.publisher.Publish(ctx, rec.Message)
	if err == nil {
		if mErr := r.store.MarkDelivered(ctx, rec.ID); mErr != nil {
			r.cfg.Logger.WarnContext(ctx, "outbox mark delivered failed", "id", rec.ID, "error", mErr)
		}
		return
	}
	if rec.Attempts >= r.cfg.MaxAttempts {
		if mErr := r.store.MarkDead(ctx, rec.ID, err.Error()); mErr != nil {
			r.cfg.Logger.WarnContext(ctx, "outbox mark dead failed", "id", rec.ID, "error", mErr)
		}
		r.cfg.Logger.WarnContext(ctx, "outbox message dead-lettered", "id", rec.ID, "topic", rec.Message.Topic, "attempts", rec.Attempts, "error", err)
		return
	}
	availableAt := r.now().Add(r.backoff(rec.Attempts))
	if rErr := r.store.Retry(ctx, rec.ID, availableAt, err.Error()); rErr != nil {
		r.cfg.Logger.WarnContext(ctx, "outbox retry schedule failed", "id", rec.ID, "error", rErr)
	}
}

// backoff returns the delay before the next attempt given the attempt count
// already consumed (1 == first attempt just failed).
func (r *Relay) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := r.cfg.BaseBackoff
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= r.cfg.MaxBackoff {
			return r.cfg.MaxBackoff
		}
	}
	if delay > r.cfg.MaxBackoff {
		return r.cfg.MaxBackoff
	}
	return delay
}
