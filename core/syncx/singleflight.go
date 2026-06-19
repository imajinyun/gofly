// Package syncx provides concurrency helpers: single-flight groups and
// cooperative cancellation utilities.
package syncx

import (
	"context"
	"sync"
)

// Group is a generic single-flight group that suppresses duplicate in-flight
// calls for the same key.
type Group[T any] struct {
	mu    sync.Mutex
	calls map[string]*call[T]
}

type call[T any] struct {
	done   chan struct{}
	value  T
	err    error
	panic  any
	shared bool
}

// Do executes fn for key, sharing the result with concurrent callers for the
// same key.
func (g *Group[T]) Do(ctx context.Context, key string, fn func(context.Context) (T, error)) (value T, shared bool, err error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, false, err
	}
	if key == "" {
		value, err := fn(ctx)
		return value, false, err
	}

	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*call[T])
	}
	if c := g.calls[key]; c != nil {
		c.shared = true
		done := c.done
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return zero, true, ctx.Err()
		case <-done:
			if c.panic != nil {
				panic(c.panic)
			}
			return c.value, true, c.err
		}
	}
	c := &call[T]{done: make(chan struct{})}
	g.calls[key] = c
	g.mu.Unlock()

	defer func() {
		if v := recover(); v != nil {
			c.panic = v
		}
		g.mu.Lock()
		delete(g.calls, key)
		// Read shared under the lock; waiters set it concurrently.
		shared = c.shared
		g.mu.Unlock()
		close(c.done)
		if c.panic != nil {
			panic(c.panic)
		}
	}()
	c.value, c.err = fn(ctx)
	// shared is finalised by the deferred closure under the lock.
	return c.value, false, c.err
}

func (g *Group[T]) Forget(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.calls, key)
}
