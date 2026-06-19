// Package limit provides rate limiting, concurrency limiting, and adaptive
// limiting for gofly services.
package limit

import (
	"sync"
	"time"
)

// Limiter is a token bucket rate limiter.
type Limiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

// New creates a token bucket Limiter with the given rate and burst.
func New(rate, burst int) *Limiter {
	if rate <= 0 {
		rate = 1
	}
	if burst <= 0 {
		burst = rate
	}
	return &Limiter{rate: float64(rate), burst: float64(burst), tokens: float64(burst), last: time.Now()}
}

// Allow returns true if a token is available.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
