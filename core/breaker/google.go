// Package breaker provides circuit breaker implementations for gofly services,
// including a standard state-machine breaker and a Google SRE adaptive throttle.
package breaker

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

// GoogleBreaker implements the client-side adaptive throttling algorithm from
// the Google SRE book ("Handling Overload"). Instead of a hard open/closed
// switch it probabilistically rejects requests based on the ratio of recent
// requests to accepted responses:
//
//	rejectProbability = max(0, (requests - K*accepts) / (requests + 1))
//
// As the backend recovers and accepts climb, the probability decays smoothly
// back to zero, avoiding the thundering-herd retry storms a binary breaker can
// cause when it flips back to closed.
type GoogleBreaker struct {
	mu         sync.Mutex
	k          float64
	window     time.Duration
	bucketSize time.Duration
	buckets    []googleBucket
	rand       *rand.Rand
	now        func() time.Time
}

type googleBucket struct {
	start    time.Time
	requests int64
	accepts  int64
}

// GoogleOption configures a GoogleBreaker.
type GoogleOption func(*GoogleBreaker)

// WithGoogleK sets the aggressiveness multiplier K. Larger K accepts more
// requests (less aggressive throttling); the SRE book default is 2.0.
func WithGoogleK(k float64) GoogleOption {
	return func(b *GoogleBreaker) {
		if k > 0 {
			b.k = k
		}
	}
}

// WithGoogleWindow sets the rolling statistics window.
func WithGoogleWindow(d time.Duration) GoogleOption {
	return func(b *GoogleBreaker) {
		if d > 0 {
			b.window = d
		}
	}
}

// WithGoogleBuckets sets the number of sub-buckets the window is divided into.
func WithGoogleBuckets(n int) GoogleOption {
	return func(b *GoogleBreaker) {
		if n > 0 {
			b.buckets = make([]googleBucket, n)
		}
	}
}

// NewGoogle creates a Google SRE adaptive breaker.
func NewGoogle(opts ...GoogleOption) *GoogleBreaker {
	b := &GoogleBreaker{
		k:       2.0,
		window:  10 * time.Second,
		buckets: make([]googleBucket, 10),
		// #nosec G404 -- breaker sampling is probabilistic load shedding, not cryptographic randomness.
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
		now:  time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	if len(b.buckets) == 0 {
		b.buckets = make([]googleBucket, 10)
	}
	if b.window <= 0 {
		b.window = 10 * time.Second
	}
	if b.k <= 0 {
		b.k = 2.0
	}
	b.bucketSize = b.window / time.Duration(len(b.buckets))
	if b.bucketSize <= 0 {
		b.bucketSize = time.Second
	}
	return b
}

// Allow reports whether a request should be admitted. It returns ErrOpen when
// the request is probabilistically rejected. A rejected request is still
// counted so that sustained overload keeps the rejection probability high.
func (b *GoogleBreaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	requests, accepts := b.snapshotLocked(now)
	if b.acceptedLocked(requests, accepts) {
		b.currentBucketLocked(now).requests++
		return nil
	}
	b.currentBucketLocked(now).requests++
	return ErrOpen
}

// Do admits the call through Allow, executes fn, and records the outcome.
func (b *GoogleBreaker) Do(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.Allow(); err != nil {
		return err
	}
	err := fn()
	if err != nil {
		b.MarkFailure()
		return err
	}
	b.MarkSuccess()
	return nil
}

// MarkSuccess records an accepted response.
func (b *GoogleBreaker) MarkSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentBucketLocked(b.now()).accepts++
}

// MarkFailure records a rejected response (no accept increment).
func (b *GoogleBreaker) MarkFailure() {}

// RejectProbability returns the current drop probability in [0,1).
func (b *GoogleBreaker) RejectProbability() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	requests, accepts := b.snapshotLocked(b.now())
	return b.rejectProbabilityLocked(requests, accepts)
}

func (b *GoogleBreaker) acceptedLocked(requests, accepts int64) bool {
	prob := b.rejectProbabilityLocked(requests, accepts)
	if prob <= 0 {
		return true
	}
	return b.rand.Float64() >= prob
}

func (b *GoogleBreaker) rejectProbabilityLocked(requests, accepts int64) float64 {
	weighted := b.k * float64(accepts)
	prob := (float64(requests) - weighted) / float64(requests+1)
	if prob < 0 {
		return 0
	}
	return prob
}

func (b *GoogleBreaker) snapshotLocked(now time.Time) (requests int64, accepts int64) {
	for i := range b.buckets {
		bucket := &b.buckets[i]
		if bucket.start.IsZero() || now.Sub(bucket.start) >= b.window {
			continue
		}
		requests += bucket.requests
		accepts += bucket.accepts
	}
	return requests, accepts
}

func (b *GoogleBreaker) currentBucketLocked(now time.Time) *googleBucket {
	start := now.Truncate(b.bucketSize)
	idx := int(start.UnixNano()/int64(b.bucketSize)) % len(b.buckets)
	if idx < 0 {
		idx = -idx
	}
	current := &b.buckets[idx]
	if !current.start.Equal(start) {
		*current = googleBucket{start: start}
	}
	return current
}
