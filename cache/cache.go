// Package cache provides in-memory caching with TTL, single-flight loading, and
// Redis-backed model caching for gofly services.
package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	core "github.com/imajinyun/gofly/core"
	"github.com/imajinyun/gofly/core/syncx"
)

var (
	// ErrNotFound is returned when a cache entry does not exist.
	ErrNotFound = errors.New("cache entry not found")
	// ErrLoaderNil is returned when the cache loader is nil.
	ErrLoaderNil = errors.New("cache loader is nil")
)

const envCacheDisabled = "GOFLY_CACHE_DISABLED"

// Loader loads a value by key when it is not in the cache.
type Loader[T any] func(context.Context, string) (T, error)

// Option configures a Cache.
type Option[T any] func(*Cache[T])

// ModelLoader loads a model value by typed key when it is not in the cache.
type ModelLoader[T any, K comparable] func(context.Context, K) (T, error)

// ModelKeyFunc converts a typed key to a string cache key.
type ModelKeyFunc[K comparable] func(K) string

// ModelOption configures a ModelCache.
type ModelOption[T any, K comparable] func(*ModelCache[T, K])

// Stats holds cache statistics.
type Stats struct {
	Name       string `json:"name,omitempty"`
	Entries    int    `json:"entries"`
	MaxEntries int    `json:"maxEntries,omitempty"`
	Hits       int64  `json:"hits"`
	Misses     int64  `json:"misses"`
	StaleHits  int64  `json:"staleHits"`
	Loads      int64  `json:"loads"`
	LoadErrors int64  `json:"loadErrors"`
	Evictions  int64  `json:"evictions"`
	Deletes    int64  `json:"deletes"`
	Refreshes  int64  `json:"refreshes"`
	Disabled   bool   `json:"disabled,omitempty"`
	// Negatives counts negative (not-found) results stored to guard against
	// cache penetration.
	Negatives int64 `json:"negatives"`
	// BloomRejects counts lookups short-circuited by the bloom filter.
	BloomRejects int64 `json:"bloomRejects"`
}

type entry[T any] struct {
	value      T
	expiresAt  time.Time
	staleUntil time.Time
	accessedAt time.Time
	// negative marks a cached not-found result.
	negative bool
}

// BloomFilter is the subset of a bloom filter used by the cache to guard
// against penetration. The in-process *bloom.Filter satisfies it.
type BloomFilter interface {
	ContainsString(key string) bool
	AddString(key string)
}

type Cache[T any] struct {
	mu                   sync.RWMutex
	items                map[string]entry[T]
	loader               Loader[T]
	defaultTTL           time.Duration
	staleWhileRevalidate time.Duration
	refreshTimeout       time.Duration
	maxEntries           int
	name                 string
	stats                Stats
	group                syncx.Group[T]
	// protection knobs
	ttlJitter   float64
	negativeTTL time.Duration
	notFoundErr error
	bloom       BloomFilter
	disabled    bool
}

type ModelCache[T any, K comparable] struct {
	cache  *Cache[T]
	loader ModelLoader[T, K]
	key    ModelKeyFunc[K]
	prefix string
}

func New[T any](opts ...Option[T]) *Cache[T] {
	c := &Cache[T]{
		items:                make(map[string]entry[T]),
		defaultTTL:           time.Minute,
		staleWhileRevalidate: 0,
		refreshTimeout:       5 * time.Second,
		notFoundErr:          ErrNotFound,
		disabled:             cacheDisabledByEnv(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	c.stats.Name = c.name
	c.stats.MaxEntries = c.maxEntries
	c.stats.Disabled = c.disabled
	if c.disabled {
		slog.Debug("cache created in disabled mode", "name", c.name)
	}
	return c
}

func WithName[T any](name string) Option[T] {
	return func(c *Cache[T]) {
		c.name = strings.TrimSpace(name)
	}
}

func WithDefaultTTL[T any](ttl time.Duration) Option[T] {
	return func(c *Cache[T]) {
		if ttl > 0 {
			c.defaultTTL = ttl
		}
	}
}

func WithStaleWhileRevalidate[T any](ttl time.Duration) Option[T] {
	return func(c *Cache[T]) {
		if ttl >= 0 {
			c.staleWhileRevalidate = ttl
		}
	}
}

func WithRefreshTimeout[T any](timeout time.Duration) Option[T] {
	return func(c *Cache[T]) {
		if timeout > 0 {
			c.refreshTimeout = timeout
		}
	}
}

func WithMaxEntries[T any](max int) Option[T] {
	return func(c *Cache[T]) {
		if max > 0 {
			c.maxEntries = max
		}
	}
}

func WithLoader[T any](loader Loader[T]) Option[T] {
	return func(c *Cache[T]) {
		c.loader = loader
	}
}

// WithDisabled turns the cache into a pass-through loader. When disabled, Set
// is a no-op, Get always misses, and GetOrLoad invokes the loader without
// storing local, stale, negative, or bloom-filter cache state.
func WithDisabled[T any](disabled bool) Option[T] {
	return func(c *Cache[T]) {
		c.disabled = disabled
		c.stats.Disabled = disabled
		if disabled {
			c.items = make(map[string]entry[T])
		}
	}
}

// WithTTLJitter spreads entry expiry by up to fraction of the TTL to avoid mass
// expiration (cache avalanche). fraction is clamped to [0, 1); 0.1 means each
// TTL is randomly shortened by 0–10%.
func WithTTLJitter[T any](fraction float64) Option[T] {
	return func(c *Cache[T]) {
		if fraction < 0 {
			fraction = 0
		}
		if fraction >= 1 {
			fraction = 0.99
		}
		c.ttlJitter = fraction
	}
}

// WithNegativeCache enables caching of not-found results returned by the loader
// for ttl, guarding against cache penetration. A loader signals "not found" by
// returning notFound (matched with errors.Is). When notFound is nil the
// package ErrNotFound sentinel is used.
func WithNegativeCache[T any](ttl time.Duration, notFound error) Option[T] {
	return func(c *Cache[T]) {
		if ttl > 0 {
			c.negativeTTL = ttl
		}
		if notFound != nil {
			c.notFoundErr = notFound
		}
	}
}

// WithBloomFilter attaches a bloom filter consulted before invoking the loader.
// Keys absent from the filter are rejected immediately, preventing penetration
// by keys that are known never to exist. Successful loads add the key to the
// filter.
func WithBloomFilter[T any](filter BloomFilter) Option[T] {
	return func(c *Cache[T]) {
		c.bloom = filter
	}
}

func NewModel[T any, K comparable](loader ModelLoader[T, K], opts ...ModelOption[T, K]) *ModelCache[T, K] {
	m := &ModelCache[T, K]{
		cache:  New[T](),
		loader: loader,
		key:    defaultModelKey[K],
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if m.cache == nil {
		m.cache = New[T]()
	}
	if m.key == nil {
		m.key = defaultModelKey[K]
	}
	return m
}

func WithModelCache[T any, K comparable](c *Cache[T]) ModelOption[T, K] {
	return func(m *ModelCache[T, K]) {
		if c != nil {
			m.cache = c
		}
	}
}

func WithModelOptions[T any, K comparable](opts ...Option[T]) ModelOption[T, K] {
	return func(m *ModelCache[T, K]) {
		m.cache = New[T](opts...)
	}
}

func WithModelKey[T any, K comparable](key ModelKeyFunc[K]) ModelOption[T, K] {
	return func(m *ModelCache[T, K]) {
		if key != nil {
			m.key = key
		}
	}
}

func WithModelKeyPrefix[T any, K comparable](prefix string) ModelOption[T, K] {
	return func(m *ModelCache[T, K]) {
		prefix = strings.TrimSpace(prefix)
		m.prefix = prefix
	}
}

func (m *ModelCache[T, K]) Get(ctx context.Context, id K) (T, error) {
	var zero T
	if m == nil {
		return zero, ErrLoaderNil
	}
	key := m.cacheKey(id)
	if m.loader == nil {
		if m.cache != nil {
			m.cache.recordMiss()
		}
		return zero, ErrLoaderNil
	}
	return m.cache.GetOrLoad(ctx, key, func(ctx context.Context, _ string) (T, error) {
		return m.loader(ctx, id)
	})
}

func (m *ModelCache[T, K]) Set(id K, value T, ttl ...time.Duration) {
	if m == nil || m.cache == nil {
		return
	}
	m.cache.Set(m.cacheKey(id), value, ttl...)
}

func (m *ModelCache[T, K]) Invalidate(id K) bool {
	if m == nil || m.cache == nil {
		return false
	}
	return m.cache.Delete(m.cacheKey(id))
}

func (m *ModelCache[T, K]) Clear() {
	if m == nil || m.cache == nil {
		return
	}
	m.cache.Clear()
}

func (m *ModelCache[T, K]) Snapshot() Stats {
	if m == nil || m.cache == nil {
		return Stats{}
	}
	return m.cache.Snapshot()
}

func (m *ModelCache[T, K]) Cache() *Cache[T] {
	if m == nil {
		return nil
	}
	return m.cache
}

func (m *ModelCache[T, K]) cacheKey(id K) string {
	var key string
	if m.key == nil {
		key = defaultModelKey(id)
	} else {
		key = m.key(id)
	}
	if m.prefix == "" {
		return key
	}
	return m.prefix + ":" + key
}

func (c *Cache[T]) Set(key string, value T, ttl ...time.Duration) {
	if c == nil || c.disabled || key == "" {
		return
	}
	expiresAt, staleUntil := c.deadlines(ttl...)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = entry[T]{value: value, expiresAt: expiresAt, staleUntil: staleUntil, accessedAt: now}
	c.evictLocked(now)
}

func (c *Cache[T]) Get(key string) (T, bool) {
	var zero T
	if c == nil || key == "" {
		return zero, false
	}
	if c.disabled {
		c.recordMiss()
		return zero, false
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	ent, ok := c.items[key]
	if !ok || ent.expired(now) {
		if ok {
			delete(c.items, key)
			c.stats.Evictions++
		}
		c.stats.Misses++
		return zero, false
	}
	ent.accessedAt = now
	c.items[key] = ent
	c.stats.Hits++
	return ent.value, true
}

func (c *Cache[T]) GetOrLoad(ctx context.Context, key string, loaders ...Loader[T]) (T, error) {
	ctx = core.Context(ctx)
	var zero T
	if c == nil {
		return zero, ErrLoaderNil
	}
	if key == "" {
		c.recordMiss()
		return zero, ErrNotFound
	}
	if c.disabled {
		c.recordMiss()
		loader := firstLoader(c.loader, loaders...)
		if loader == nil {
			return zero, ErrLoaderNil
		}
		value, err := loader(ctx, key)
		if err != nil {
			c.recordLoadError()
			return zero, fmt.Errorf("load cache entry: %w", err)
		}
		c.recordLoad()
		return value, nil
	}
	now := time.Now()
	if value, state, ok := c.lookup(key, now); ok {
		switch state {
		case "negative":
			return zero, c.notFoundError()
		case "stale":
			c.refresh(context.WithoutCancel(ctx), key, firstLoader(c.loader, loaders...), true)
		}
		return value, nil
	}
	if c.bloom != nil && !c.bloom.ContainsString(key) {
		c.recordBloomReject()
		slog.Debug("cache bloom filter rejected key", "name", c.name, "key", key)
		return zero, c.notFoundError()
	}
	loader := firstLoader(c.loader, loaders...)
	if loader == nil {
		return zero, ErrLoaderNil
	}
	return c.load(ctx, key, loader)
}

func (c *Cache[T]) Delete(key string) bool {
	if c == nil || key == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok {
		return false
	}
	delete(c.items, key)
	c.stats.Deletes++
	return true
}

func (c *Cache[T]) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.items {
		delete(c.items, key)
	}
}

func (c *Cache[T]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

func (c *Cache[T]) Snapshot() Stats {
	if c == nil {
		return Stats{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshot := c.stats
	snapshot.Name = c.name
	snapshot.MaxEntries = c.maxEntries
	snapshot.Disabled = c.disabled
	snapshot.Entries = len(c.items)
	return snapshot
}

func (c *Cache[T]) WritePrometheus(w io.Writer) error {
	snapshot := c.Snapshot()
	label := prometheusLabel(snapshot.Name)
	if _, err := fmt.Fprintln(w, "# HELP gofly_cache_entries Current number of cache entries."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_cache_entries gauge"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "gofly_cache_entries{name=\"%s\"} %d\n", label, snapshot.Entries); err != nil {
		return err
	}
	counters := map[string]int64{
		"bloom_rejects": snapshot.BloomRejects,
		"deletes":       snapshot.Deletes,
		"evictions":     snapshot.Evictions,
		"hits":          snapshot.Hits,
		"load_errors":   snapshot.LoadErrors,
		"loads":         snapshot.Loads,
		"misses":        snapshot.Misses,
		"negatives":     snapshot.Negatives,
		"refreshes":     snapshot.Refreshes,
		"stale_hits":    snapshot.StaleHits,
	}
	keys := make([]string, 0, len(counters))
	for key := range counters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if _, err := fmt.Fprintln(w, "# HELP gofly_cache_events_total Total cache events by type."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_cache_events_total counter"); err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := fmt.Fprintf(w, "gofly_cache_events_total{name=\"%s\",event=\"%s\"} %d\n", label, key, counters[key]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Cache[T]) lookup(key string, now time.Time) (T, string, bool) {
	var zero T
	c.mu.Lock()
	defer c.mu.Unlock()
	ent, ok := c.items[key]
	if !ok {
		c.stats.Misses++
		return zero, "", false
	}
	if ent.fresh(now) {
		ent.accessedAt = now
		c.items[key] = ent
		if ent.negative {
			c.stats.Hits++
			return zero, "negative", true
		}
		c.stats.Hits++
		return ent.value, "fresh", true
	}
	if ent.stale(now) {
		ent.accessedAt = now
		c.items[key] = ent
		if ent.negative {
			c.stats.Hits++
			return zero, "negative", true
		}
		c.stats.StaleHits++
		return ent.value, "stale", true
	}
	delete(c.items, key)
	c.stats.Misses++
	c.stats.Evictions++
	return zero, "", false
}

func (c *Cache[T]) load(ctx context.Context, key string, loader Loader[T]) (T, error) {
	value, _, err := c.group.Do(ctx, key, func(ctx context.Context) (T, error) {
		value, err := loader(ctx, key)
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			c.stats.LoadErrors++
			if c.negativeTTL > 0 && errors.Is(err, c.notFoundError()) {
				now := time.Now()
				expiresAt := now.Add(c.jitterTTL(c.negativeTTL))
				c.items[key] = entry[T]{expiresAt: expiresAt, staleUntil: expiresAt, accessedAt: now, negative: true}
				c.stats.Negatives++
				c.evictLocked(now)
			}
			slog.Warn("cache load error", "name", c.name, "key", key, "error", err)
			return value, err
		}
		c.stats.Loads++
		now := time.Now()
		expiresAt := now.Add(c.jitterTTL(c.defaultTTL))
		c.items[key] = entry[T]{value: value, expiresAt: expiresAt, staleUntil: expiresAt.Add(c.staleWhileRevalidate), accessedAt: now}
		if c.bloom != nil {
			c.bloom.AddString(key)
		}
		c.evictLocked(now)
		return value, nil
	})
	if err != nil {
		var zero T
		return zero, fmt.Errorf("load cache entry: %w", err)
	}
	return value, nil
}

func (c *Cache[T]) refresh(ctx context.Context, key string, loader Loader[T], async bool) {
	if loader == nil {
		return
	}
	run := func() {
		refreshCtx := ctx
		var cancel context.CancelFunc
		if c.refreshTimeout > 0 {
			refreshCtx, cancel = context.WithTimeout(ctx, c.refreshTimeout)
			defer cancel()
		}
		if _, _, err := c.group.Do(refreshCtx, key, func(ctx context.Context) (T, error) {
			value, err := loader(ctx, key)
			c.mu.Lock()
			defer c.mu.Unlock()
			if err != nil {
				c.stats.LoadErrors++
				slog.Warn("cache background refresh failed", "name", c.name, "key", key, "error", err)
				return value, err
			}
			c.stats.Loads++
			c.stats.Refreshes++
			now := time.Now()
			expiresAt := now.Add(c.jitterTTL(c.defaultTTL))
			c.items[key] = entry[T]{value: value, expiresAt: expiresAt, staleUntil: expiresAt.Add(c.staleWhileRevalidate), accessedAt: now}
			c.evictLocked(now)
			return value, nil
		}); err != nil {
			return
		}
	}
	if async {
		go run()
		return
	}
	run()
}

func (c *Cache[T]) deadlines(ttl ...time.Duration) (time.Time, time.Time) {
	expireAfter := c.defaultTTL
	if len(ttl) > 0 && ttl[0] > 0 {
		expireAfter = ttl[0]
	}
	expireAfter = c.jitterTTL(expireAfter)
	now := time.Now()
	expiresAt := now.Add(expireAfter)
	return expiresAt, expiresAt.Add(c.staleWhileRevalidate)
}

func (c *Cache[T]) jitterTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 || c.ttlJitter <= 0 {
		return ttl
	}
	maxJitter := int64(float64(ttl) * c.ttlJitter)
	if maxJitter <= 0 {
		return ttl
	}
	// #nosec G404 -- TTL jitter only spreads cache expirations; it is not used for secrets or security decisions.
	return ttl - time.Duration(rand.Int63n(maxJitter+1))
}

func (c *Cache[T]) evictLocked(now time.Time) {
	for key, ent := range c.items {
		if ent.expired(now) {
			delete(c.items, key)
			c.stats.Evictions++
		}
	}
	if c.maxEntries <= 0 {
		return
	}
	for len(c.items) > c.maxEntries {
		var oldestKey string
		var oldest time.Time
		for key, ent := range c.items {
			if oldestKey == "" || ent.accessedAt.Before(oldest) {
				oldestKey = key
				oldest = ent.accessedAt
			}
		}
		if oldestKey == "" {
			return
		}
		delete(c.items, oldestKey)
		c.stats.Evictions++
	}
}

func (c *Cache[T]) recordMiss() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Misses++
}

func (c *Cache[T]) recordLoad() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Loads++
}

func (c *Cache[T]) recordLoadError() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.LoadErrors++
}

func (c *Cache[T]) recordBloomReject() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Misses++
	c.stats.BloomRejects++
}

func (c *Cache[T]) notFoundError() error {
	if c.notFoundErr != nil {
		return c.notFoundErr
	}
	return ErrNotFound
}

func (e entry[T]) fresh(now time.Time) bool {
	return e.expiresAt.IsZero() || now.Before(e.expiresAt)
}

func (e entry[T]) stale(now time.Time) bool {
	return !e.staleUntil.IsZero() && now.Before(e.staleUntil)
}

func (e entry[T]) expired(now time.Time) bool {
	return !e.fresh(now) && !e.stale(now)
}

func firstLoader[T any](base Loader[T], loaders ...Loader[T]) Loader[T] {
	for _, loader := range loaders {
		if loader != nil {
			return loader
		}
	}
	return base
}

func defaultModelKey[K comparable](id K) string {
	return fmt.Sprint(id)
}

func prometheusLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return strings.ReplaceAll(s, "\"", "\\\"")
}

func cacheDisabledByEnv() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(envCacheDisabled)))
	switch value {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}
