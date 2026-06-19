// Package eventbus provides a lightweight in-process publish/subscribe event bus
// for decoupling components within a single service. Handlers may be registered
// per topic and invoked synchronously (publisher waits, errors aggregated) or
// asynchronously (fire-and-forget via a worker goroutine).
//
// For cross-process delivery use core/mq; for transactional event emission use
// core/outbox. This bus is intentionally in-memory and dependency-free.
package eventbus

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned once the bus has been closed.
var ErrClosed = errors.New("eventbus: closed")

// Event is an arbitrary payload published to a topic.
type Event any

// Handler reacts to a published event. A non-nil error from a synchronous
// handler is aggregated and returned to the publisher.
type Handler func(ctx context.Context, event Event) error

// Subscription identifies a registered handler so it can be cancelled.
type Subscription struct {
	bus   *Bus
	topic string
	id    uint64
}

// Unsubscribe removes the handler. It is safe to call multiple times.
func (s *Subscription) Unsubscribe() {
	if s == nil || s.bus == nil {
		return
	}
	s.bus.unsubscribe(s.topic, s.id)
}

type subscriber struct {
	id      uint64
	handler Handler
}

// Bus is an in-process event bus. The zero value is not usable; call New.
type Bus struct {
	mu     sync.RWMutex
	nextID uint64
	topics map[string][]subscriber
	closed bool
	wg     sync.WaitGroup
}

// New returns an empty Bus.
func New() *Bus {
	return &Bus{topics: make(map[string][]subscriber)}
}

// Subscribe registers handler for topic and returns a Subscription used to
// cancel it.
func (b *Bus) Subscribe(topic string, handler Handler) (*Subscription, error) {
	if topic == "" {
		return nil, errors.New("eventbus: topic is empty")
	}
	if handler == nil {
		return nil, errors.New("eventbus: handler is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	b.nextID++
	id := b.nextID
	b.topics[topic] = append(b.topics[topic], subscriber{id: id, handler: handler})
	return &Subscription{bus: b, topic: topic, id: id}, nil
}

// Publish delivers event to every handler of topic synchronously, waiting for
// all to complete and returning the joined error of any that failed. Handlers
// run sequentially; a handler panic is recovered and reported as an error.
func (b *Bus) Publish(ctx context.Context, topic string, event Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	handlers, err := b.handlersFor(topic)
	if err != nil {
		return err
	}
	var errs error
	for _, sub := range handlers {
		if cerr := ctx.Err(); cerr != nil {
			return errors.Join(errs, cerr)
		}
		errs = errors.Join(errs, invoke(ctx, sub.handler, event))
	}
	return errs
}

// PublishAsync delivers event to every handler of topic on a background
// goroutine and returns immediately. Handler errors are discarded; use Publish
// when the caller needs delivery results. Pending async deliveries are awaited
// by Close.
func (b *Bus) PublishAsync(ctx context.Context, topic string, event Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	subs := b.topics[topic]
	handlers := make([]subscriber, len(subs))
	copy(handlers, subs)
	if len(handlers) == 0 {
		b.mu.Unlock()
		return nil
	}
	b.wg.Add(1)
	b.mu.Unlock()
	go func() {
		defer b.wg.Done()
		for _, sub := range handlers {
			if ctx.Err() != nil {
				return
			}
			_ = invoke(ctx, sub.handler, event)
		}
	}()
	return nil
}

// Close stops the bus, refusing new subscriptions/publishes and waiting for
// outstanding async deliveries to finish.
func (b *Bus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.topics = make(map[string][]subscriber)
	b.mu.Unlock()
	b.wg.Wait()
	return nil
}

func (b *Bus) handlersFor(topic string) ([]subscriber, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil, ErrClosed
	}
	subs := b.topics[topic]
	if len(subs) == 0 {
		return nil, nil
	}
	// Copy so delivery is unaffected by concurrent (un)subscribe.
	out := make([]subscriber, len(subs))
	copy(out, subs)
	return out, nil
}

func (b *Bus) unsubscribe(topic string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.topics[topic]
	for i, sub := range subs {
		if sub.id == id {
			b.topics[topic] = append(subs[:i], subs[i+1:]...)
			if len(b.topics[topic]) == 0 {
				delete(b.topics, topic)
			}
			return
		}
	}
}

func invoke(ctx context.Context, handler Handler, event Event) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Join(err, &PanicError{Value: r})
		}
	}()
	return handler(ctx, event)
}
