package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingProvider tracks how many times Complete is called.
type countingProvider struct {
	NoOpProvider
	count atomic.Int32
	slow  time.Duration
}

func (p *countingProvider) Complete(_ context.Context, req Request) (Response, error) {
	p.count.Add(1)
	if p.slow > 0 {
		time.Sleep(p.slow)
	}
	return NoOpProvider{}.Complete(context.Background(), req)
}

func TestCachingProviderHitsAfterFirstComplete(t *testing.T) {
	inner := &countingProvider{}
	cache := NewCachingProvider(inner)

	req := Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64}
	resp1, err := cache.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if inner.count.Load() != 1 {
		t.Fatalf("inner called %d times, want 1", inner.count.Load())
	}

	resp2, err := cache.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("cached Complete: %v", err)
	}
	if inner.count.Load() != 1 {
		t.Fatalf("inner called %d times after cache hit, want 1", inner.count.Load())
	}
	if resp1.Text != resp2.Text || resp1.Usage.TotalTokens != resp2.Usage.TotalTokens {
		t.Fatalf("cached response differs from original: %+v vs %+v", resp1, resp2)
	}
}

func TestCachingProviderMissOnDifferentPrompt(t *testing.T) {
	inner := &countingProvider{}
	cache := NewCachingProvider(inner)

	_, _ = cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
	if inner.count.Load() != 1 {
		t.Fatalf("inner count = %d, want 1", inner.count.Load())
	}

	_, _ = cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "world", MaxOutputTokens: 64})
	if inner.count.Load() != 2 {
		t.Fatalf("inner count = %d, want 2 (different prompt)", inner.count.Load())
	}
}

func TestCachingProviderMissOnDifferentProvider(t *testing.T) {
	inner := &countingProvider{}
	cache := NewCachingProvider(inner)

	req := Request{Provider: "provider-a", Model: "noop", Prompt: "hello", MaxOutputTokens: 64}
	_, _ = cache.Complete(context.Background(), req)
	if inner.count.Load() != 1 {
		t.Fatalf("inner count = %d, want 1", inner.count.Load())
	}

	req.Provider = "provider-b"
	_, _ = cache.Complete(context.Background(), req)
	if inner.count.Load() != 2 {
		t.Fatalf("inner count = %d, want 2 (different provider)", inner.count.Load())
	}
}

func TestCachingProviderExpiresAfterTTL(t *testing.T) {
	inner := &countingProvider{}
	cache := NewCachingProvider(inner, WithCacheTTL(10*time.Millisecond))

	_, _ = cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
	if inner.count.Load() != 1 {
		t.Fatalf("inner count = %d, want 1", inner.count.Load())
	}

	time.Sleep(15 * time.Millisecond)

	_, _ = cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
	if inner.count.Load() != 2 {
		t.Fatalf("inner count = %d, want 2 (expired)", inner.count.Load())
	}
}

func TestCachingProviderMaxEntriesEviction(t *testing.T) {
	inner := &countingProvider{}
	cache := NewCachingProvider(inner, WithCacheMaxEntries(3))

	for i := range 3 {
		_, err := cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: strings.Repeat("x", i+1), MaxOutputTokens: 64})
		if err != nil {
			t.Fatalf("Complete %d: %v", i, err)
		}
	}
	if inner.count.Load() != 3 {
		t.Fatalf("inner count = %d, want 3 after initial fills", inner.count.Load())
	}

	// Fourth call should evict one entry
	_, _ = cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "unique", MaxOutputTokens: 64})
	if inner.count.Load() != 4 {
		t.Fatalf("inner count = %d, want 4 after eviction", inner.count.Load())
	}

	snap := cache.CacheSnapshot()
	if snap.Size < 1 || snap.Size > 3 {
		t.Fatalf("cache size = %d after eviction, want 1-3", snap.Size)
	}
}

func TestCachingProviderNilInnerDefaultsToNoop(t *testing.T) {
	cache := NewCachingProvider(nil)
	resp, err := cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello"})
	if err != nil {
		t.Fatalf("nil inner Complete: %v", err)
	}
	if resp.Usage.InputTokens == 0 {
		t.Fatal("nil inner should default to NoOp with usage")
	}
}

func TestCachingProviderStreamPassThrough(t *testing.T) {
	inner := &countingProvider{}
	cache := NewCachingProvider(inner)

	stream, err := cache.Stream(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	event, ok := <-stream
	if !ok || !event.Done {
		t.Fatal("expected done event from stream pass-through")
	}
}

func TestCachingProviderEmbedPassThrough(t *testing.T) {
	inner := &countingProvider{}
	cache := NewCachingProvider(inner)

	resp, err := cache.Embed(context.Background(), EmbedRequest{Inputs: []string{"test"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Vectors) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(resp.Vectors))
	}
}

func TestCachingProviderConcurrency(t *testing.T) {
	inner := &countingProvider{slow: 2 * time.Millisecond}
	cache := NewCachingProvider(inner)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // barrier: all goroutines block here
			_, err := cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
			errs <- err
		}()
	}
	close(start) // release all goroutines simultaneously
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Complete: %v", err)
		}
	}

	// Only one inner call should happen; all others are cache hits
	if inner.count.Load() != 1 {
		t.Fatalf("inner called %d times concurrently, want 1", inner.count.Load())
	}
}

func TestCachingProviderErrorNotCached(t *testing.T) {
	errProvider := &failProvider{err: context.DeadlineExceeded}
	cache := NewCachingProvider(errProvider)

	_, err := cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
	if err == nil {
		t.Fatal("expected error from fail provider")
	}

	// Second call should also go to inner (error not cached)
	_, err = cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
	if err == nil {
		t.Fatal("expected error from fail provider on second call (no error caching)")
	}
}

func TestCachingProviderConcurrentErrorCoalescing(t *testing.T) {
	errProvider := &failProvider{err: context.DeadlineExceeded}
	cache := NewCachingProvider(errProvider)

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cache.Complete(context.Background(), Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
			if err == nil {
				errs <- fmt.Errorf("expected error from coalesced call")
			} else {
				errs <- nil
			}
		}()
	}
	wg.Wait()
	close(errs)

	var failCount int
	for err := range errs {
		if err != nil {
			failCount++
		}
	}
	if failCount > 0 {
		t.Fatalf("%d concurrent callers did not receive the error", failCount)
	}
}

func TestCachingProviderCacheSnapshotEmptyForNil(t *testing.T) {
	var cache *CachingProvider
	snap := cache.CacheSnapshot()
	if snap.Size != 0 || snap.MaxSize != 0 {
		t.Fatalf("nil snapshot = %+v, want zeros", snap)
	}
}

func TestCacheKeyDeterministic(t *testing.T) {
	k1 := cacheKey(Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
	k2 := cacheKey(Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
	if k1 != k2 {
		t.Fatalf("cache keys differ for identical requests: %s vs %s", k1, k2)
	}
}

func TestCacheKeyDifferentForDifferentMaxOutputTokens(t *testing.T) {
	k1 := cacheKey(Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 32})
	k2 := cacheKey(Request{Provider: "noop", Model: "noop", Prompt: "hello", MaxOutputTokens: 64})
	if k1 == k2 {
		t.Fatalf("cache keys should differ for different MaxOutputTokens")
	}
}

func TestWithCacheTTLZeroUsesDefault(t *testing.T) {
	p := NewCachingProvider(NoOpProvider{}, WithCacheTTL(0), WithCacheMaxEntries(0))
	if p.ttl != defaultCacheTTL {
		t.Fatalf("TTL = %v, want default %v", p.ttl, defaultCacheTTL)
	}
	if p.maxEntries != defaultCacheMaxSize {
		t.Fatalf("maxEntries = %d, want default %d", p.maxEntries, defaultCacheMaxSize)
	}
}
