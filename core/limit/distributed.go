// Package limit provides rate limiting, concurrency limiting and adaptive
// overload protection for gofly services.
package limit

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Backend is the minimal contract a distributed limiter needs: atomic Lua
// evaluation returning an integer reply. It is satisfied by *redis.Client.
type Backend interface {
	Eval(ctx context.Context, script string, keys []string, args ...string) (int64, error)
}

// ErrBackendNil is returned when a distributed limiter is built without a backend.
var ErrBackendNil = errors.New("distributed limiter: backend is nil")

// periodScript performs a fixed-window period limit atomically: it increments
// the counter and sets the window TTL on first use, returning the new count.
const periodScript = `local c = redis.call('INCR', KEYS[1])
if c == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return c`

// DistributedPeriodLimiter enforces at most Quota events per Window across all
// processes sharing the same Backend and key.
type DistributedPeriodLimiter struct {
	backend Backend
	quota   int64
	window  time.Duration
}

// NewDistributedPeriod creates a fixed-window distributed limiter.
func NewDistributedPeriod(backend Backend, quota int, window time.Duration) *DistributedPeriodLimiter {
	if quota <= 0 {
		quota = 1
	}
	if window <= 0 {
		window = time.Second
	}
	return &DistributedPeriodLimiter{backend: backend, quota: int64(quota), window: window}
}

// Allow reports whether an event identified by key is permitted in the current
// window. The first return reports admission; the second carries backend errors.
func (l *DistributedPeriodLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if l == nil || l.backend == nil {
		return false, ErrBackendNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ms := l.window.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	count, err := l.backend.Eval(ctx, periodScript, []string{key}, strconv.FormatInt(ms, 10))
	if err != nil {
		return false, err
	}
	return count <= l.quota, nil
}

// tokenScript implements a token-bucket admission check. KEYS[1] holds the
// stored token count and KEYS[2] the last-refill timestamp (ms).
// ARGV: rate(per sec), burst, now(ms), requested.
// #nosec G101 -- this is a Redis token-bucket Lua script, not a hardcoded credential.
const tokenScript = `local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local requested = tonumber(ARGV[4])
local tokens = tonumber(redis.call('GET', KEYS[1]))
local last = tonumber(redis.call('GET', KEYS[2]))
if tokens == nil then tokens = burst end
if last == nil then last = now end
local elapsed = math.max(0, now - last) / 1000.0
tokens = math.min(burst, tokens + elapsed * rate)
local allowed = 0
if tokens >= requested then
  tokens = tokens - requested
  allowed = 1
end
local ttl = math.ceil(burst / rate) * 2000
redis.call('SET', KEYS[1], tokens, 'PX', ttl)
redis.call('SET', KEYS[2], now, 'PX', ttl)
return allowed`

// DistributedTokenLimiter is a distributed token-bucket limiter shared across
// processes via a Backend.
type DistributedTokenLimiter struct {
	backend Backend
	rate    int
	burst   int
	now     func() time.Time
}

// NewDistributedToken creates a distributed token-bucket limiter allowing rate
// tokens per second with the given burst capacity.
func NewDistributedToken(backend Backend, rate, burst int) *DistributedTokenLimiter {
	if rate <= 0 {
		rate = 1
	}
	if burst <= 0 {
		burst = rate
	}
	return &DistributedTokenLimiter{backend: backend, rate: rate, burst: burst, now: time.Now}
}

// Allow reports whether a single token is available for key.
func (l *DistributedTokenLimiter) Allow(ctx context.Context, key string) (bool, error) {
	return l.AllowN(ctx, key, 1)
}

// AllowN reports whether n tokens are available for key.
func (l *DistributedTokenLimiter) AllowN(ctx context.Context, key string, n int) (bool, error) {
	if l == nil || l.backend == nil {
		return false, ErrBackendNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if n <= 0 {
		n = 1
	}
	now := l.now().UnixMilli()
	allowed, err := l.backend.Eval(ctx, tokenScript,
		[]string{key + ":tokens", key + ":ts"},
		strconv.Itoa(l.rate),
		strconv.Itoa(l.burst),
		strconv.FormatInt(now, 10),
		strconv.Itoa(n),
	)
	if err != nil {
		return false, err
	}
	return allowed == 1, nil
}
