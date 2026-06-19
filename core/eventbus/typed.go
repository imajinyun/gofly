// Package eventbus provides a typed pub/sub event bus with synchronous delivery
// and panic recovery.
package eventbus

import (
	"context"
	"fmt"
)

// PanicError wraps a value recovered from a panicking handler.
type PanicError struct {
	Value any
}

// Error implements error.
func (e *PanicError) Error() string {
	return fmt.Sprintf("eventbus: handler panicked: %v", e.Value)
}

// TopicName derives a stable topic string for the event type T. It is used by
// the typed helpers so callers do not have to manage topic strings manually.
func TopicName[T any]() string {
	var zero T
	return fmt.Sprintf("%T", zero)
}

// Subscribe registers a type-safe handler on the bus. The topic is derived from
// the event type, and the handler receives the concrete type T. Events of a
// different concrete type published to the same topic are ignored.
func Subscribe[T any](bus *Bus, handler func(ctx context.Context, event T) error) (*Subscription, error) {
	return bus.Subscribe(TopicName[T](), func(ctx context.Context, event Event) error {
		typed, ok := event.(T)
		if !ok {
			return nil
		}
		return handler(ctx, typed)
	})
}

// Publish delivers a typed event synchronously, returning aggregated handler
// errors.
func Publish[T any](ctx context.Context, bus *Bus, event T) error {
	return bus.Publish(ctx, TopicName[T](), event)
}

// PublishAsync delivers a typed event asynchronously.
func PublishAsync[T any](ctx context.Context, bus *Bus, event T) error {
	return bus.PublishAsync(ctx, TopicName[T](), event)
}
