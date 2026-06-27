package llm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// cacheEntry holds one cached response with its expiration.
type cacheEntry struct {
	response Response
	expires  time.Time
	created  time.Time
}

// CachingProvider wraps a Provider with an in-memory response cache. Cache keys
// are derived from the request prompt, model and provider name so semantically
// identical requests reuse prior responses within the configured TTL.
//
// Only Complete responses are cached. Stream and Embed calls pass through
// to the inner provider without caching.
//
// Concurrent cache misses for the same key are coalesced: only the first
// caller invokes the inner provider while the rest wait for its result.
type CachingProvider struct {
	inner      Provider
	mu         sync.RWMutex
	entries    map[string]*cacheEntry
	maxEntries int
	ttl        time.Duration

	inflightMu sync.Mutex
	inflight   map[string]chan struct{}
}

// CacheSnapshot exposes cache state for observability and diagnostics.
type CacheSnapshot struct {
	Size      int    `json:"size"`
	MaxSize   int    `json:"maxSize"`
	TTL       string `json:"ttl"`
	HitRate   string `json:"hitRate,omitempty"`
	Hits      int    `json:"hits,omitempty"`
	Misses    int    `json:"misses,omitempty"`
	Evictions int    `json:"evictions,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
}

const (
	defaultCacheTTL       = 5 * time.Minute
	defaultCacheMaxSize   = 256
	evictionCheckInterval = 64
	envCacheDisabled      = "GOFLY_CACHE_DISABLED"
)

// NewCachingProvider wraps inner with an in-memory response cache. Nil inner
// is silently replaced with NoOpProvider.
func NewCachingProvider(inner Provider, opts ...CachingOption) *CachingProvider {
	if inner == nil {
		inner = NoOpProvider{}
	}
	p := &CachingProvider{
		inner:      inner,
		entries:    make(map[string]*cacheEntry),
		inflight:   make(map[string]chan struct{}),
		maxEntries: defaultCacheMaxSize,
		ttl:        defaultCacheTTL,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// CachingOption configures a CachingProvider.
type CachingOption func(*CachingProvider)

// WithCacheTTL sets the TTL for cached responses. Zero or negative values
// fall back to the default (5 minutes).
func WithCacheTTL(ttl time.Duration) CachingOption {
	return func(p *CachingProvider) {
		if ttl > 0 {
			p.ttl = ttl
		}
	}
}

// WithCacheMaxEntries sets the maximum number of cached entries. Values less
// than 1 fall back to the default (256).
func WithCacheMaxEntries(n int) CachingOption {
	return func(p *CachingProvider) {
		if n > 0 {
			p.maxEntries = n
		}
	}
}

// CacheSnapshot returns the current cache state for observability.
func (p *CachingProvider) CacheSnapshot() CacheSnapshot {
	if p == nil {
		return CacheSnapshot{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return CacheSnapshot{
		Size:     len(p.entries),
		MaxSize:  p.maxEntries,
		TTL:      p.ttl.String(),
		Disabled: llmCacheDisabledByEnv(),
	}
}

// Complete returns a cached response when available and valid, or delegates
// to the inner provider and caches the result. Concurrent cache misses for
// the same cache key are coalesced so only one inner provider call is made.
func (p *CachingProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if llmCacheDisabledByEnv() {
		return p.inner.Complete(ctx, req)
	}
	key := cacheKey(req)
	for {
		if resp, ok := p.get(key); ok {
			return resp, nil
		}
		if p.tryElect(key) {
			break // this goroutine is the elected caller
		}
		// Another goroutine is elected — wait for its result, then retry cache.
		if err := p.awaitResult(ctx, key); err != nil {
			return Response{}, err
		}
		// Loop back to check cache (handles stale inflight entries after expiry)
	}

	// We are the elected caller: fetch and cache.
	resp, err := p.inner.Complete(ctx, req)
	if err == nil {
		p.set(key, resp)
	}
	p.signalInflight(key)
	return resp, err
}

// Stream passes through to the inner provider without caching.
func (p *CachingProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	return p.inner.Stream(ctx, req)
}

// Embed passes through to the inner provider without caching.
func (p *CachingProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	return p.inner.Embed(ctx, req)
}

func (p *CachingProvider) get(key string) (Response, bool) {
	p.mu.RLock()
	entry, ok := p.entries[key]
	p.mu.RUnlock()
	if !ok {
		return Response{}, false
	}
	if time.Now().After(entry.expires) {
		p.mu.Lock()
		delete(p.entries, key)
		p.mu.Unlock()
		return Response{}, false
	}
	return entry.response, true
}

func (p *CachingProvider) set(key string, resp Response) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.entries) >= p.maxEntries {
		p.evictExpiredLocked()
	}
	if len(p.entries) >= p.maxEntries {
		var oldestKey string
		var oldestTime time.Time
		for k, entry := range p.entries {
			if oldestKey == "" || entry.created.Before(oldestTime) {
				oldestKey = k
				oldestTime = entry.created
			}
		}
		if oldestKey != "" {
			delete(p.entries, oldestKey)
		}
	}
	p.entries[key] = &cacheEntry{
		response: resp,
		expires:  time.Now().Add(p.ttl),
		created:  time.Now(),
	}
}

func (p *CachingProvider) evictExpiredLocked() {
	now := time.Now()
	for k, entry := range p.entries {
		if now.After(entry.expires) {
			delete(p.entries, k)
		}
	}
}

// tryElect attempts to become the elected caller for key. Returns true if
// this goroutine is elected (no existing inflight entry). If an existing
// entry has a closed channel (stale from a previous completed request), it
// replaces it with a new channel, effectively becoming the new elected caller.
func (p *CachingProvider) tryElect(key string) bool {
	p.inflightMu.Lock()
	defer p.inflightMu.Unlock()
	ch, exists := p.inflight[key]
	if !exists {
		p.inflight[key] = make(chan struct{})
		return true
	}
	// Check if the channel is stale (closed from a previous request).
	select {
	case <-ch:
		// Channel is closed — replace with a new one.
		p.inflight[key] = make(chan struct{})
		return true
	default:
		return false
	}
}

// awaitResult waits for the elected caller to complete. It returns nil when
// the result is available or ctx.Err() on cancellation.
func (p *CachingProvider) awaitResult(ctx context.Context, key string) error {
	p.inflightMu.Lock()
	ch, ok := p.inflight[key]
	p.inflightMu.Unlock()
	if !ok {
		return nil // no inflight entry (race with expiry), caller should retry
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// signalInflight closes the inflight channel for key, unblocking any waiters.
func (p *CachingProvider) signalInflight(key string) {
	p.inflightMu.Lock()
	ch, ok := p.inflight[key]
	p.inflightMu.Unlock()
	if ok {
		close(ch)
	}
}

// cacheKey derives a deterministic cache key from the request. It includes
// prompt, model and provider identifiers along with max output tokens so that
// different budget-constrained requests do not share the same cache entry.
func cacheKey(req Request) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d",
		req.Provider,
		req.Model,
		req.Prompt,
		req.MaxOutputTokens,
	)))
	return fmt.Sprintf("llm-cache-%x", h[:16])
}

func llmCacheDisabledByEnv() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(envCacheDisabled)))
	switch value {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

var _ Provider = (*CachingProvider)(nil)
