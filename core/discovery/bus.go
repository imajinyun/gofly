package discovery

import (
	"context"
	"sync"
)

// Bus publishes discovery events to service-scoped subscribers.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan Event]struct{}
	closed      bool
}

// NewBus creates an empty discovery event bus.
func NewBus() *Bus {
	return &Bus{subscribers: make(map[string]map[chan Event]struct{})}
}

// Subscribe subscribes to events for one service.
func (b *Bus) Subscribe(ctx context.Context, service string, buffer int) (<-chan Event, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	if buffer < 0 {
		buffer = 0
	}
	ch := make(chan Event, buffer)
	if b == nil {
		close(ch)
		return ch, func() {}
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	if b.subscribers == nil {
		b.subscribers = make(map[string]map[chan Event]struct{})
	}
	if b.subscribers[service] == nil {
		b.subscribers[service] = make(map[chan Event]struct{})
	}
	b.subscribers[service][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			if subs := b.subscribers[service]; subs != nil {
				delete(subs, ch)
				if len(subs) == 0 {
					delete(b.subscribers, service)
				}
			}
			b.mu.Unlock()
			close(ch)
		})
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ch, cancel
}

// Publish broadcasts an event to subscribers for event.Service.
func (b *Bus) Publish(event Event) {
	if b == nil {
		return
	}
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	subs := make([]chan Event, 0, len(b.subscribers[event.Service]))
	for ch := range b.subscribers[event.Service] {
		subs = append(subs, ch)
	}
	b.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- event:
			default:
			}
		}
	}
}

// Close closes all subscriptions.
func (b *Bus) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subscribers := b.subscribers
	b.subscribers = nil
	b.mu.Unlock()
	for _, subs := range subscribers {
		for ch := range subs {
			close(ch)
		}
	}
}
