package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
)

// discardLogs replaces the global slog logger with a no-op handler and
// returns a restore function. Callers must defer restore().
func discardLogs() func() {
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return func() { slog.SetDefault(orig) }
}

func BenchmarkCacheSet(b *testing.B) {
	defer discardLogs()()
	c := New[string](WithName[string]("bench"))
	ctx := context.Background()
	for b.Loop() {
		for i := range b.N {
			c.Set(fmt.Sprintf("key-%d", i), "value")
		}
	}
	_ = ctx
}

func BenchmarkCacheGetHit(b *testing.B) {
	defer discardLogs()()
	c := New[string](WithName[string]("bench"))
	c.Set("key", "value")
	for b.Loop() {
		v, ok := c.Get("key")
		if !ok || v != "value" {
			b.Fatal("unexpected miss or wrong value")
		}
	}
}

func BenchmarkCacheGetMiss(b *testing.B) {
	defer discardLogs()()
	c := New[string](WithName[string]("bench"))
	for b.Loop() {
		_, ok := c.Get("missing-key")
		if ok {
			b.Fatal("expected miss")
		}
	}
}

func BenchmarkCacheGetOrLoadWithLoader(b *testing.B) {
	defer discardLogs()()
	c := New[string](WithName[string]("bench"), WithLoader(func(_ context.Context, key string) (string, error) {
		return "loaded-" + key, nil
	}))
	ctx := context.Background()
	for b.Loop() {
		v, err := c.GetOrLoad(ctx, "key")
		if err != nil || v != "loaded-key" {
			b.Fatalf("unexpected error=%v value=%s", err, v)
		}
	}
}

func BenchmarkCacheGetOrLoadLoaderError(b *testing.B) {
	defer discardLogs()()
	errSentinel := errors.New("loader error")
	c := New[string](WithName[string]("bench"), WithLoader(func(_ context.Context, key string) (string, error) {
		return "", errSentinel
	}))
	ctx := context.Background()
	for b.Loop() {
		_, err := c.GetOrLoad(ctx, "key")
		if !errors.Is(err, errSentinel) {
			b.Fatalf("expected sentinel error, got %v", err)
		}
	}
}

func BenchmarkCacheGetOrLoadBloomReject(b *testing.B) {
	defer discardLogs()()
	bf := newBenchBloom()
	c := New[string](WithName[string]("bench"), WithLoader(func(_ context.Context, key string) (string, error) {
		return "loaded", nil
	}), WithBloomFilter[string](bf))
	ctx := context.Background()
	for b.Loop() {
		_, err := c.GetOrLoad(ctx, "unknown-key")
		if !errors.Is(err, ErrNotFound) {
			b.Fatalf("expected not-found for bloom reject, got %v", err)
		}
	}
}

func BenchmarkCacheParallelSetGet(b *testing.B) {
	defer discardLogs()()
	c := New[string](WithName[string]("bench"))
	ctx := context.Background()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i)
			c.Set(key, "value")
			v, ok := c.Get(key)
			if !ok || v != "value" {
				b.Fatal("unexpected miss or wrong value")
			}
			i++
		}
	})
	_ = ctx
}

// benchBloom is a minimal bloom filter that rejects all keys not added.
type benchBloom struct {
	items map[string]struct{}
}

func newBenchBloom() *benchBloom {
	return &benchBloom{items: make(map[string]struct{})}
}

func (b *benchBloom) ContainsString(key string) bool {
	_, ok := b.items[key]
	return ok
}

func (b *benchBloom) AddString(key string) {
	b.items[key] = struct{}{}
}
