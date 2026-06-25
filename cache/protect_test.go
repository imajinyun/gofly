package cache

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/bloom"
)

func TestNegativeCache(t *testing.T) {
	var calls int
	c := New[int](
		WithNegativeCache[int](time.Minute, nil),
	)
	loader := func(ctx context.Context, key string) (int, error) {
		calls++
		return 0, ErrNotFound
	}

	for i := 0; i < 3; i++ {
		_, err := c.GetOrLoad(context.Background(), "missing", loader)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1 (negative cached)", calls)
	}
	if got := c.Snapshot().Negatives; got != 1 {
		t.Fatalf("negatives = %d, want 1", got)
	}
}

func TestNegativeCacheCustomError(t *testing.T) {
	notFound := errors.New("no such user")
	var calls int
	c := New[int](WithNegativeCache[int](time.Minute, notFound))
	loader := func(ctx context.Context, key string) (int, error) {
		calls++
		return 0, notFound
	}
	for i := 0; i < 2; i++ {
		if _, err := c.GetOrLoad(context.Background(), "u", loader); !errors.Is(err, notFound) {
			t.Fatalf("err = %v, want notFound", err)
		}
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
}

func TestTTLJitter(t *testing.T) {
	c := New[int](WithDefaultTTL[int](time.Hour), WithTTLJitter[int](0.5))
	// jittered TTL must be within (0.5*ttl, ttl]
	for i := 0; i < 100; i++ {
		d := c.jitterTTL(time.Hour)
		if d > time.Hour || d <= 30*time.Minute {
			t.Fatalf("jitterTTL = %s, want (30m, 1h]", d)
		}
	}
	// zero jitter is a no-op
	plain := New[int](WithDefaultTTL[int](time.Hour))
	if got := plain.jitterTTL(time.Hour); got != time.Hour {
		t.Fatalf("no-jitter = %s, want 1h", got)
	}
}

func TestBloomFilterGuard(t *testing.T) {
	filter := bloom.New(1000, 0.01)
	filter.AddString("exists")

	var calls int
	c := New[string](WithBloomFilter[string](filter))
	loader := func(ctx context.Context, key string) (string, error) {
		calls++
		return "value:" + key, nil
	}

	// key not in bloom -> rejected without calling loader
	if _, err := c.GetOrLoad(context.Background(), "ghost", loader); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ghost err = %v, want ErrNotFound", err)
	}
	if calls != 0 {
		t.Fatalf("loader called %d times for bloom-rejected key", calls)
	}
	if got := c.Snapshot().BloomRejects; got != 1 {
		t.Fatalf("bloomRejects = %d, want 1", got)
	}

	// key in bloom -> loader runs and key is cached
	v, err := c.GetOrLoad(context.Background(), "exists", loader)
	if err != nil {
		t.Fatalf("exists err = %v", err)
	}
	if v != "value:exists" {
		t.Fatalf("value = %q", v)
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
}

func TestBloomFilterPopulatedOnLoad(t *testing.T) {
	filter := bloom.New(1000, 0.01)
	c := New[string](WithBloomFilter[string](filter))
	loader := func(ctx context.Context, key string) (string, error) { return "v", nil }

	// Pre-add so the first load is allowed.
	filter.AddString("k1")
	if _, err := c.GetOrLoad(context.Background(), "k1", loader); err != nil {
		t.Fatalf("load err = %v", err)
	}
	if !filter.ContainsString("k1") {
		t.Fatal("filter should contain loaded key")
	}

	var buf strings.Builder
	if err := c.WritePrometheus(&buf); err != nil {
		t.Fatalf("prometheus: %v", err)
	}
	if !strings.Contains(buf.String(), "bloom_rejects") {
		t.Fatalf("prometheus missing bloom_rejects:\n%s", buf.String())
	}
}
