// Package limit provides rate limiting, concurrency limiting and adaptive
// overload protection for gofly services.
package limit

import "context"

// ConcurrencyLimiter bounds the number of concurrent in-flight operations.
type ConcurrencyLimiter struct {
	sem chan struct{}
}

// NewConcurrency creates a concurrency limiter allowing max simultaneous
// operations.
func NewConcurrency(max int) *ConcurrencyLimiter {
	if max <= 0 {
		max = 1
	}
	return &ConcurrencyLimiter{sem: make(chan struct{}, max)}
}

// TryAcquire attempts to acquire a slot without blocking.
func (l *ConcurrencyLimiter) TryAcquire() bool {
	if l == nil {
		return true
	}
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Acquire waits until a slot is available or ctx is cancelled.
func (l *ConcurrencyLimiter) Acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *ConcurrencyLimiter) Release() {
	if l == nil {
		return
	}
	select {
	case <-l.sem:
	default:
	}
}

func (l *ConcurrencyLimiter) InFlight() int {
	if l == nil {
		return 0
	}
	return len(l.sem)
}
