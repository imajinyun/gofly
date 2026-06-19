package limit

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scriptBackend is a tiny fake Backend that interprets the two known scripts
// well enough to exercise the limiter logic without a real Redis server.
type scriptBackend struct {
	counters map[string]int64
	tokens   map[string]float64
	ts       map[string]int64
}

func newScriptBackend() *scriptBackend {
	return &scriptBackend{
		counters: make(map[string]int64),
		tokens:   make(map[string]float64),
		ts:       make(map[string]int64),
	}
}

func (b *scriptBackend) Eval(_ context.Context, script string, keys []string, args ...string) (int64, error) {
	switch {
	case strings.Contains(script, "INCR"):
		b.counters[keys[0]]++
		return b.counters[keys[0]], nil
	case strings.Contains(script, "math.min(burst"):
		rate, _ := strconv.ParseFloat(args[0], 64)
		burst, _ := strconv.ParseFloat(args[1], 64)
		now, _ := strconv.ParseInt(args[2], 10, 64)
		requested, _ := strconv.ParseFloat(args[3], 64)
		tokKey, tsKey := keys[0], keys[1]
		tokens, ok := b.tokens[tokKey]
		if !ok {
			tokens = burst
		}
		last, ok := b.ts[tsKey]
		if !ok {
			last = now
		}
		elapsed := float64(now-last) / 1000.0
		if elapsed < 0 {
			elapsed = 0
		}
		tokens += elapsed * rate
		if tokens > burst {
			tokens = burst
		}
		allowed := int64(0)
		if tokens >= requested {
			tokens -= requested
			allowed = 1
		}
		b.tokens[tokKey] = tokens
		b.ts[tsKey] = now
		return allowed, nil
	default:
		return 0, nil
	}
}

func TestDistributedPeriodLimiterEnforcesQuota(t *testing.T) {
	backend := newScriptBackend()
	limiter := NewDistributedPeriod(backend, 3, time.Second)
	ctx := context.Background()
	allowed := 0
	for i := 0; i < 5; i++ {
		ok, err := limiter.Allow(ctx, "user")
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if ok {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("allowed = %d, want 3", allowed)
	}
}

func TestDistributedPeriodLimiterNilBackend(t *testing.T) {
	var limiter *DistributedPeriodLimiter
	if _, err := limiter.Allow(context.Background(), "k"); err != ErrBackendNil {
		t.Fatalf("err = %v, want ErrBackendNil", err)
	}
}

func TestDistributedTokenLimiterBurstThenRefill(t *testing.T) {
	backend := newScriptBackend()
	limiter := NewDistributedToken(backend, 10, 2)
	current := time.Unix(1000, 0)
	limiter.now = func() time.Time { return current }
	ctx := context.Background()

	// Burst of 2 should be allowed immediately.
	for i := 0; i < 2; i++ {
		ok, err := limiter.Allow(ctx, "key")
		if err != nil {
			t.Fatalf("Allow: %v", err)
		}
		if !ok {
			t.Fatalf("burst token %d denied", i)
		}
	}
	// Third request within the same instant is denied.
	if ok, _ := limiter.Allow(ctx, "key"); ok {
		t.Fatal("third token allowed, want denied")
	}
	// After 200ms at 10 tokens/s, ~2 tokens refill; one should be allowed.
	current = current.Add(200 * time.Millisecond)
	if ok, err := limiter.Allow(ctx, "key"); err != nil || !ok {
		t.Fatalf("after refill allow = %v, err = %v; want true", ok, err)
	}
}

func TestDistributedLimiterNilAndZeroGuard(t *testing.T) {
	// nil limiters
	var nilPeriod *DistributedPeriodLimiter
	if _, err := nilPeriod.Allow(context.Background(), "k"); !errors.Is(err, ErrBackendNil) {
		t.Fatalf("nil period Allow err = %v, want ErrBackendNil", err)
	}

	var nilToken *DistributedTokenLimiter
	if _, err := nilToken.Allow(context.Background(), "k"); !errors.Is(err, ErrBackendNil) {
		t.Fatalf("nil token Allow err = %v, want ErrBackendNil", err)
	}
	if _, err := nilToken.AllowN(context.Background(), "k", 5); !errors.Is(err, ErrBackendNil) {
		t.Fatalf("nil token AllowN err = %v, want ErrBackendNil", err)
	}

	// zero quota/rate/burst clamped
	backend := newScriptBackend()
	p := NewDistributedPeriod(backend, 0, 0)
	if p.quota != 1 || p.window != time.Second {
		t.Fatalf("period zero values not clamped: quota=%d window=%v", p.quota, p.window)
	}
	tok := NewDistributedToken(backend, 0, 0)
	if tok.rate != 1 || tok.burst != 1 {
		t.Fatalf("token zero values not clamped: rate=%d burst=%d", tok.rate, tok.burst)
	}

	// AllowN with n <= 0 treated as 1
	ok, err := NewDistributedToken(backend, 10, 10).AllowN(context.Background(), "k2", 0)
	if err != nil {
		t.Fatalf("AllowN(0): %v", err)
	}
	if !ok {
		t.Fatal("AllowN(0) should be treated as 1")
	}

	// ctx nil defaults to Background
	var nilCtx context.Context
	ok, err = NewDistributedPeriod(backend, 1, time.Second).Allow(nilCtx, "k3")
	if err != nil {
		t.Fatalf("Allow(nil ctx): %v", err)
	}
	if !ok {
		t.Fatal("Allow(nil ctx) should work")
	}
}
