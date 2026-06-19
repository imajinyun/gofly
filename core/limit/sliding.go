// Package limit provides rate limiting, concurrency limiting and adaptive
// overload protection for gofly services.
package limit

import (
	"sync"
	"time"
)

// SlidingWindowLimiter enforces at most Quota events per Window using a
// rolling counter split into equal-sized buckets. Unlike a fixed-window
// counter it does not suffer from boundary bursts where up to 2*Quota events
// can pass around the window edge: expired buckets are continuously evicted as
// time advances, so the admitted rate stays close to Quota over any Window.
type SlidingWindowLimiter struct {
	mu         sync.Mutex
	quota      int64
	window     time.Duration
	bucketSize time.Duration
	buckets    []swBucket
	now        func() time.Time
}

type swBucket struct {
	start time.Time
	count int64
}

// NewSlidingWindow creates a sliding-window limiter allowing quota events per
// window, internally divided into buckets sub-windows for smoothing. More
// buckets yield a smoother approximation at the cost of more memory.
func NewSlidingWindow(quota int, window time.Duration, buckets int) *SlidingWindowLimiter {
	if quota <= 0 {
		quota = 1
	}
	if window <= 0 {
		window = time.Second
	}
	if buckets <= 0 {
		buckets = 10
	}
	bucketSize := window / time.Duration(buckets)
	if bucketSize <= 0 {
		bucketSize = time.Nanosecond
		buckets = int(window / bucketSize)
		if buckets <= 0 {
			buckets = 1
		}
	}
	return &SlidingWindowLimiter{
		quota:      int64(quota),
		window:     window,
		bucketSize: bucketSize,
		buckets:    make([]swBucket, buckets),
		now:        time.Now,
	}
}

// Allow reports whether a single event is permitted right now.
func (l *SlidingWindowLimiter) Allow() bool {
	return l.AllowN(1)
}

// AllowN reports whether n events are permitted within the current window.
// When admitting would exceed the quota the call returns false and consumes
// nothing.
func (l *SlidingWindowLimiter) AllowN(n int) bool {
	if l == nil {
		return true
	}
	if n <= 0 {
		n = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	current := l.bucketAt(now)
	if l.countLocked(now)+int64(n) > l.quota {
		return false
	}
	current.count += int64(n)
	return true
}

// Count returns the number of events counted within the current window.
func (l *SlidingWindowLimiter) Count() int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.countLocked(l.now())
}

func (l *SlidingWindowLimiter) countLocked(now time.Time) int64 {
	var total int64
	for i := range l.buckets {
		b := &l.buckets[i]
		if b.start.IsZero() || now.Sub(b.start) >= l.window {
			continue
		}
		total += b.count
	}
	return total
}

func (l *SlidingWindowLimiter) bucketAt(now time.Time) *swBucket {
	start := now.Truncate(l.bucketSize)
	idx := int(start.UnixNano()/int64(l.bucketSize)) % len(l.buckets)
	if idx < 0 {
		idx = -idx
	}
	b := &l.buckets[idx]
	if !b.start.Equal(start) {
		*b = swBucket{start: start}
	}
	return b
}
