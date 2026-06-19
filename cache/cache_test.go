package cache

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheSetGetTTLEviction(t *testing.T) {
	c := New[string](WithDefaultTTL[string](30*time.Millisecond), WithMaxEntries[string](2))
	c.Set("a", "one")
	c.Set("b", "two")
	if got, ok := c.Get("a"); !ok || got != "one" {
		t.Fatalf("Get(a) = %q, %v, want one, true", got, ok)
	}
	c.Set("c", "three")
	if _, ok := c.Get("b"); ok {
		t.Fatal("expected least recently used entry b to be evicted")
	}
	if got := c.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected expired entry to be unavailable")
	}
	snapshot := c.Snapshot()
	if snapshot.Hits == 0 || snapshot.Misses == 0 || snapshot.Evictions == 0 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestCacheGetOrLoadSingleflight(t *testing.T) {
	var loads atomic.Int64
	c := New[string](WithDefaultTTL[string](time.Minute))
	loader := func(ctx context.Context, key string) (string, error) {
		loads.Add(1)
		time.Sleep(10 * time.Millisecond)
		return "value:" + key, nil
	}
	const callers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := c.GetOrLoad(context.Background(), "same", loader)
			if err != nil {
				errs <- err
				return
			}
			if got != "value:same" {
				errs <- errors.New("unexpected loaded value")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("loader calls = %d, want 1", got)
	}
	if got, ok := c.Get("same"); !ok || got != "value:same" {
		t.Fatalf("cached value = %q, %v, want value:same, true", got, ok)
	}
}

func TestCacheDisabledByOptionBypassesStorage(t *testing.T) {
	c := New[string](WithDisabled[string](true))
	c.Set("k", "stored")
	if got, ok := c.Get("k"); ok || got != "" {
		t.Fatalf("disabled Get = %q, %v; want miss", got, ok)
	}

	calls := 0
	loader := func(_ context.Context, _ string) (string, error) {
		calls++
		return "loaded", nil
	}
	for i := 0; i < 2; i++ {
		got, err := c.GetOrLoad(context.Background(), "k", loader)
		if err != nil || got != "loaded" {
			t.Fatalf("GetOrLoad(%d) = %q, %v; want loaded, nil", i, got, err)
		}
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2 when cache disabled", calls)
	}
	if c.Len() != 0 {
		t.Fatalf("disabled cache Len = %d, want 0", c.Len())
	}
	if !c.Snapshot().Disabled {
		t.Fatalf("disabled cache Snapshot().Disabled = false, want true")
	}
}

func TestCacheDisabledByEnvBypassesStorage(t *testing.T) {
	t.Setenv(envCacheDisabled, "true")
	c := New[int]()
	if !c.Snapshot().Disabled {
		t.Fatalf("GOFLY_CACHE_DISABLED did not disable new cache")
	}
	c.Set("k", 1)
	if got, ok := c.Get("k"); ok || got != 0 {
		t.Fatalf("env-disabled Get = %d, %v; want miss", got, ok)
	}
}

func TestCacheStaleWhileRevalidate(t *testing.T) {
	var loads atomic.Int64
	c := New[string](
		WithDefaultTTL[string](20*time.Millisecond),
		WithStaleWhileRevalidate[string](time.Second),
		WithRefreshTimeout[string](time.Second),
	)
	c.Set("user:1", "old")
	time.Sleep(30 * time.Millisecond)
	got, err := c.GetOrLoad(context.Background(), "user:1", func(ctx context.Context, key string) (string, error) {
		loads.Add(1)
		return "new", nil
	})
	if err != nil {
		t.Fatalf("GetOrLoad stale: %v", err)
	}
	if got != "old" {
		t.Fatalf("GetOrLoad stale = %q, want old", got)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got, ok := c.Get("user:1"); ok && got == "new" {
			snapshot := c.Snapshot()
			if snapshot.StaleHits != 1 || snapshot.Refreshes != 1 || snapshot.LoadErrors != 0 {
				t.Fatalf("unexpected snapshot: %+v", snapshot)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("stale refresh did not update value; loads=%d snapshot=%+v", loads.Load(), c.Snapshot())
}

func TestCacheLoaderNilAndPrometheus(t *testing.T) {
	c := New[string](WithName[string](`api"cache`))
	if _, err := c.GetOrLoad(context.Background(), "missing"); !errors.Is(err, ErrLoaderNil) {
		t.Fatalf("GetOrLoad without loader = %v, want ErrLoaderNil", err)
	}
	c.Set("k", "v")
	var b strings.Builder
	if err := c.WritePrometheus(&b); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, `gofly_cache_entries{name="api\"cache"} 1`) {
		t.Fatalf("missing escaped entries metric: %s", out)
	}
	if !strings.Contains(out, `gofly_cache_events_total{name="api\"cache",event="misses"} 1`) {
		t.Fatalf("missing misses metric: %s", out)
	}
}

func TestModelCacheGetInvalidateAndSingleflight(t *testing.T) {
	type user struct {
		ID   int64
		Name string
	}
	var loads atomic.Int64
	m := NewModel(func(ctx context.Context, id int64) (*user, error) {
		loads.Add(1)
		time.Sleep(10 * time.Millisecond)
		return &user{ID: id, Name: "loaded"}, nil
	}, WithModelOptions[*user, int64](
		WithName[*user]("users"),
		WithDefaultTTL[*user](time.Minute),
	), WithModelKeyPrefix[*user, int64]("user"))

	const callers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := m.Get(context.Background(), 42)
			if err != nil {
				errs <- err
				return
			}
			if got.ID != 42 || got.Name != "loaded" {
				errs <- errors.New("unexpected model value")
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("model loads = %d, want 1", got)
	}
	if snapshot := m.Snapshot(); snapshot.Loads != 1 || snapshot.Misses != callers || snapshot.Name != "users" {
		t.Fatalf("snapshot = %+v, want one deduplicated load", snapshot)
	}
	m.Set(42, &user{ID: 42, Name: "manual"})
	got, err := m.Get(context.Background(), 42)
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if got.Name != "manual" {
		t.Fatalf("Get after Set name = %q, want manual", got.Name)
	}
	if !m.Invalidate(42) {
		t.Fatal("Invalidate returned false, want true")
	}
	got, err = m.Get(context.Background(), 42)
	if err != nil {
		t.Fatalf("Get after Invalidate: %v", err)
	}
	if got.Name != "loaded" || loads.Load() != 2 {
		t.Fatalf("Get after Invalidate = %+v loads=%d, want reloaded", got, loads.Load())
	}
}

func TestModelCacheLoaderErrors(t *testing.T) {
	m := NewModel[string, string](nil, WithModelOptions[string, string](WithName[string]("broken")))
	if _, err := m.Get(context.Background(), "x"); !errors.Is(err, ErrLoaderNil) {
		t.Fatalf("Get with nil loader = %v, want ErrLoaderNil", err)
	}
	if snapshot := m.Snapshot(); snapshot.Misses != 1 {
		t.Fatalf("snapshot misses = %d, want 1", snapshot.Misses)
	}
	var nilModel *ModelCache[string, string]
	if _, err := nilModel.Get(context.Background(), "x"); !errors.Is(err, ErrLoaderNil) {
		t.Fatalf("nil model Get = %v, want ErrLoaderNil", err)
	}
}

func TestCacheClear(t *testing.T) {
	c := New[string]()
	c.Set("a", "one")
	c.Set("b", "two")
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("Len after Clear = %d, want 0", c.Len())
	}
	var nilCache *Cache[string]
	nilCache.Clear()
}

func TestCacheOptionsNilGuardAndEdgeCases(t *testing.T) {
	// nil option should be ignored
	c := New[string](nil, WithName[string]("test"), nil)
	if c.Snapshot().Name != "test" {
		t.Fatalf("name = %q, want test", c.Snapshot().Name)
	}

	// negative TTL clamped to default
	c = New[string](WithDefaultTTL[string](-1 * time.Second))
	c.Set("k", "v")
	if _, ok := c.Get("k"); !ok {
		t.Fatal("negative TTL should clamp to default")
	}

	// staleWhileRevalidate negative clamped to 0
	c = New[string](WithStaleWhileRevalidate[string](-1 * time.Second))
	if c.staleWhileRevalidate != 0 {
		t.Fatalf("staleWhileRevalidate = %v, want 0", c.staleWhileRevalidate)
	}

	// refreshTimeout negative clamped to default
	c = New[string](WithRefreshTimeout[string](-1 * time.Second))
	if c.refreshTimeout != 5*time.Second {
		t.Fatalf("refreshTimeout = %v, want 5s", c.refreshTimeout)
	}

	// maxEntries negative ignored
	c = New[string](WithMaxEntries[string](-1))
	if c.maxEntries != 0 {
		t.Fatalf("maxEntries = %d, want 0", c.maxEntries)
	}

	// TTL jitter clamped
	c = New[string](WithTTLJitter[string](-0.5))
	if c.ttlJitter != 0 {
		t.Fatalf("ttlJitter = %v, want 0", c.ttlJitter)
	}
	c = New[string](WithTTLJitter[string](1.5))
	if c.ttlJitter != 0.99 {
		t.Fatalf("ttlJitter = %v, want 0.99", c.ttlJitter)
	}
}

func TestModelCacheNilGuardsAndOptions(t *testing.T) {
	var nilModel *ModelCache[string, int]
	nilModel.Set(1, "x")
	nilModel.Clear()
	if nilModel.Invalidate(1) {
		t.Fatal("nil Invalidate should return false")
	}
	if s := nilModel.Snapshot(); s.Entries != 0 {
		t.Fatalf("nil Snapshot = %+v", s)
	}
	if c := nilModel.Cache(); c != nil {
		t.Fatal("nil Cache should return nil")
	}

	// nil cache option ignored
	loader := func(_ context.Context, _ int) (string, error) { return "ok", nil }
	m := NewModel(loader, WithModelCache[string, int](nil))
	if m.cache == nil {
		t.Fatal("nil WithModelCache should not nil out cache")
	}

	// nil key option ignored
	m = NewModel(loader, WithModelKey[string, int](nil))
	if m.key == nil {
		t.Fatal("nil WithModelKey should not nil out key")
	}

	// custom key func
	m = NewModel(loader, WithModelKey[string, int](func(id int) string { return "custom:" + strconv.Itoa(id) }))
	if m.cacheKey(42) != "custom:42" {
		t.Fatalf("cacheKey = %q, want custom:42", m.cacheKey(42))
	}

	// prefix
	m = NewModel(loader, WithModelKeyPrefix[string, int]("pre"))
	if m.cacheKey(7) != "pre:7" {
		t.Fatalf("cacheKey = %q, want pre:7", m.cacheKey(7))
	}
}

func TestCacheNegativeCacheAndBloom(t *testing.T) {
	loads := 0
	loader := func(_ context.Context, _ string) (string, error) {
		loads++
		return "", ErrNotFound
	}
	c := New[string](
		WithLoader[string](loader),
		WithDefaultTTL[string](time.Minute),
		WithNegativeCache[string](time.Minute, nil),
	)
	for i := 0; i < 3; i++ {
		_, err := c.GetOrLoad(context.Background(), "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetOrLoad = %v, want ErrNotFound", err)
		}
	}
	if loads != 1 {
		t.Fatalf("loader calls = %d, want 1 (negative cache dedup)", loads)
	}
	if c.Snapshot().Negatives != 1 {
		t.Fatalf("negatives = %d, want 1", c.Snapshot().Negatives)
	}

	// bloom filter rejects unknown keys
	bloom := &fakeBloom{contains: false}
	c2 := New[string](
		WithLoader[string](loader),
		WithBloomFilter[string](bloom),
	)
	_, err := c2.GetOrLoad(context.Background(), "unknown")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("bloom reject = %v, want ErrNotFound", err)
	}
	if c2.Snapshot().BloomRejects != 1 {
		t.Fatalf("bloomRejects = %d, want 1", c2.Snapshot().BloomRejects)
	}
}

type fakeBloom struct {
	contains bool
}

func (f *fakeBloom) ContainsString(string) bool { return f.contains }
func (f *fakeBloom) AddString(string)           {}

func TestCacheDeleteAndNilGuards(t *testing.T) {
	var nilCache *Cache[string]
	if nilCache.Delete("k") {
		t.Fatal("nil Delete should return false")
	}
	if nilCache.Len() != 0 {
		t.Fatal("nil Len should return 0")
	}
	if s := nilCache.Snapshot(); s.Entries != 0 {
		t.Fatalf("nil Snapshot = %+v", s)
	}

	c := New[string]()
	c.Set("k", "v")
	if !c.Delete("k") {
		t.Fatal("Delete existing should return true")
	}
	if c.Delete("k") {
		t.Fatal("Delete missing should return false")
	}
	if c.Delete("") {
		t.Fatal("Delete empty key should return false")
	}
}

func TestCacheGetOrLoadNilCacheAndEmptyKey(t *testing.T) {
	var nilCache *Cache[string]
	_, err := nilCache.GetOrLoad(context.Background(), "k")
	if !errors.Is(err, ErrLoaderNil) {
		t.Fatalf("nil GetOrLoad = %v, want ErrLoaderNil", err)
	}

	c := New[string]()
	_, err = c.GetOrLoad(context.Background(), "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty key GetOrLoad = %v, want ErrNotFound", err)
	}
}

func TestCacheRecordLoadErrorNilGuard(t *testing.T) {
	var nilCache *Cache[string]
	nilCache.recordLoadError()
	nilCache.recordLoad()
	nilCache.recordMiss()
	nilCache.recordBloomReject()
}

func TestCachePrometheusNilCache(t *testing.T) {
	var nilCache *Cache[string]
	if err := nilCache.WritePrometheus(io.Discard); err != nil {
		t.Fatalf("nil WritePrometheus error = %v", err)
	}
}

func TestCacheJitterTTL(t *testing.T) {
	c := New[string](WithTTLJitter[string](0.5), WithDefaultTTL[string](time.Second))
	jittered := c.jitterTTL(time.Second)
	if jittered <= 0 || jittered > time.Second {
		t.Fatalf("jittered = %v, want in (0, 1s]", jittered)
	}
	if c.jitterTTL(0) != 0 {
		t.Fatal("jitterTTL(0) should be 0")
	}
	if c.jitterTTL(-1) != -1 {
		t.Fatal("jitterTTL(-1) should be -1")
	}
}

func TestCacheEvictLockedMaxEntries(t *testing.T) {
	c := New[string](WithMaxEntries[string](2), WithDefaultTTL[string](time.Hour))
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("c", "3")
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2 after eviction", c.Len())
	}
}

func TestCacheGetEmptyKeyAndDisabled(t *testing.T) {
	c := New[string]()
	if _, ok := c.Get(""); ok {
		t.Fatal("Get empty key should miss")
	}
	c = New[string](WithDisabled[string](true))
	c.Set("k", "v")
	if _, ok := c.Get("k"); ok {
		t.Fatal("disabled Get should miss")
	}
}

func TestCacheSetNilAndEmptyKey(t *testing.T) {
	var nilCache *Cache[string]
	nilCache.Set("k", "v")

	c := New[string]()
	c.Set("", "v")
	if c.Len() != 0 {
		t.Fatal("Set empty key should be no-op")
	}
}

func TestCacheNotFoundError(t *testing.T) {
	c := New[string]()
	if !errors.Is(c.notFoundError(), ErrNotFound) {
		t.Fatal("default notFoundError should be ErrNotFound")
	}
	customErr := errors.New("custom not found")
	c2 := New[string](WithNegativeCache[string](time.Minute, customErr))
	if !errors.Is(c2.notFoundError(), customErr) {
		t.Fatal("custom notFoundError should match")
	}
}

func TestCacheLookupStaleNegativeAndExpired(t *testing.T) {
	c := New[string](WithDefaultTTL[string](10*time.Millisecond), WithStaleWhileRevalidate[string](20*time.Millisecond))
	c.Set("fresh", "v")
	time.Sleep(5 * time.Millisecond)
	if _, state, ok := c.lookup("fresh", time.Now()); !ok || state != "fresh" {
		t.Fatalf("fresh lookup = %v, %v, want true, fresh", state, ok)
	}

	c.Set("stale", "v")
	time.Sleep(15 * time.Millisecond)
	if _, state, ok := c.lookup("stale", time.Now()); !ok || state != "stale" {
		t.Fatalf("stale lookup = %v, %v, want true, stale", state, ok)
	}

	c.Set("expired", "v")
	time.Sleep(35 * time.Millisecond)
	if _, state, ok := c.lookup("expired", time.Now()); ok || state != "" {
		t.Fatalf("expired lookup = %v, %v, want false, empty", state, ok)
	}
}

func TestCacheFirstLoader(t *testing.T) {
	base := func(_ context.Context, _ string) (string, error) { return "base", nil }
	override := func(_ context.Context, _ string) (string, error) { return "override", nil }
	if l := firstLoader(base, nil, override); l == nil {
		t.Fatal("firstLoader should return override")
	}
	if l := firstLoader(base, nil, nil); l == nil {
		t.Fatal("firstLoader should fall back to base")
	}
	var nilLoader Loader[string]
	if l := firstLoader(nilLoader, nil); l != nil {
		t.Fatal("firstLoader with all nil should return nil")
	}
}
