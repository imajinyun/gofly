// Package retry provides configurable retry policies with backoff strategies
// for gofly service calls.
package retry

import (
	"context"
	"math"
	rand "math/rand/v2"
	"time"
)

// BackoffFunc computes the delay before a given retry attempt.
type BackoffFunc func(attempt int) time.Duration

// Budget decides whether a retry is permitted.
type Budget interface {
	AllowRetry(retry int, err error) bool
}

// Policy defines retry behaviour: number of attempts, backoff, budget and
// per-error retry eligibility.
type Policy struct {
	Attempts    int
	Backoff     time.Duration
	BackoffFunc BackoffFunc
	Budget      Budget
	ShouldRetry func(error) bool
}

// MaxRetriesBudget allows retries up to a fixed maximum.
type MaxRetriesBudget struct {
	MaxRetries int
}

// AllowRetry reports whether retry is within the configured maximum.
func (b MaxRetriesBudget) AllowRetry(retry int, err error) bool {
	if b.MaxRetries < 0 {
		return false
	}
	return retry <= b.MaxRetries
}

// FixedBackoff returns a BackoffFunc that always returns d.
func FixedBackoff(d time.Duration) BackoffFunc {
	return func(attempt int) time.Duration { return d }
}

// ExponentialBackoff returns a BackoffFunc that doubles the delay on each
// attempt, capped at max.
func ExponentialBackoff(base, max time.Duration) BackoffFunc {
	return func(attempt int) time.Duration {
		if base <= 0 {
			return 0
		}
		if attempt <= 0 {
			attempt = 1
		}
		if attempt > 62 {
			attempt = 62
		}
		d := time.Duration(int64(base) * (1 << (attempt - 1)))
		if d < 0 || (max > 0 && d > max) {
			return max
		}
		return d
	}
}

// JitterBackoff wraps a BackoffFunc and adds random jitter within ±ratio of
// the computed delay, smoothing thundering-herd retries.
func JitterBackoff(next BackoffFunc, ratio float64) BackoffFunc {
	return func(attempt int) time.Duration {
		d := nextDelay(next, 0, attempt)
		if d <= 0 || ratio <= 0 {
			return d
		}
		if ratio > 1 {
			ratio = 1
		}
		span := int64(math.Round(float64(d) * ratio))
		if span <= 0 {
			return d
		}
		// #nosec G404 -- retry jitter only spreads load; it is not used for secrets or security decisions.
		offset := rand.Int64N(span*2+1) - span
		jittered := int64(d) + offset
		if jittered < 0 {
			return 0
		}
		return time.Duration(jittered)
	}
}

// Do executes fn and retries according to p until success, context
// cancellation, or the policy budget is exhausted.
func Do(ctx context.Context, p Policy, fn func() error) error {
	if p.Attempts <= 0 {
		p.Attempts = 1
	}
	var last error
	for i := 0; i < p.Attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = fn()
		if last == nil {
			return nil
		}
		if p.ShouldRetry != nil && !p.ShouldRetry(last) {
			return last
		}
		retry := i + 1
		if p.Budget != nil && !p.Budget.AllowRetry(retry, last) {
			return last
		}
		delay := nextDelay(p.BackoffFunc, p.Backoff, retry)
		if i == p.Attempts-1 || delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return last
}

func nextDelay(fn BackoffFunc, fallback time.Duration, attempt int) time.Duration {
	if fn != nil {
		return fn(attempt)
	}
	return fallback
}
