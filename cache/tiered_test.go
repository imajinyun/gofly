package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofly/gofly/core/kv"
)

func TestTieredCacheL1Hit(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2)
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	c.Set(ctx, "k", 42)

	got, ok := c.Get(ctx, "k")
	if !ok || got != 42 {
		t.Fatalf("Get = %d, %v; want 42, true", got, ok)
	}
}

func TestTieredCacheBackfillFromL2(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2, WithNamespace[int]("ns"))
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()

	// Write directly to L2 (with namespaced key), bypassing L1.
	data, _ := (JSONCodec[int]{}).Marshal(7)
	if err := l2.Set(ctx, "ns:k", data, time.Minute); err != nil {
		t.Fatalf("l2.Set: %v", err)
	}

	got, ok := c.Get(ctx, "k")
	if !ok || got != 7 {
		t.Fatalf("Get = %d, %v; want 7, true", got, ok)
	}
	// L1 should now be back-filled.
	if v, ok := c.L1().Get("k"); !ok || v != 7 {
		t.Fatalf("L1 back-fill = %d, %v; want 7, true", v, ok)
	}
}

func TestTieredCacheGetOrLoad(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2)
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()

	calls := 0
	loader := func(_ context.Context, _ string) (int, error) {
		calls++
		return 99, nil
	}

	got, err := c.GetOrLoad(ctx, "k", loader)
	if err != nil || got != 99 {
		t.Fatalf("GetOrLoad = %d, %v; want 99, nil", got, err)
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d; want 1", calls)
	}

	// Second call served from L1, loader not invoked again.
	got, err = c.GetOrLoad(ctx, "k", loader)
	if err != nil || got != 99 {
		t.Fatalf("GetOrLoad(2) = %d, %v; want 99, nil", got, err)
	}
	if calls != 1 {
		t.Fatalf("loader calls after L1 hit = %d; want 1", calls)
	}

	// Drop L1 only; value should still come from L2 without loader.
	c.L1().Delete("k")
	got, err = c.GetOrLoad(ctx, "k", loader)
	if err != nil || got != 99 {
		t.Fatalf("GetOrLoad(3) = %d, %v; want 99, nil", got, err)
	}
	if calls != 1 {
		t.Fatalf("loader calls after L2 hit = %d; want 1", calls)
	}
}

func TestTieredCacheDisabledByOptionBypassesLocalAndRemote(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2, WithTieredDisabled[int](true))
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	c.Set(ctx, "k", 5)
	if got, ok := c.Get(ctx, "k"); ok || got != 0 {
		t.Fatalf("disabled Get after Set = %d, %v; want miss", got, ok)
	}
	if _, err := l2.Get(ctx, "k"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("disabled Set wrote L2 err = %v, want ErrNotFound", err)
	}

	calls := 0
	loader := func(_ context.Context, _ string) (int, error) {
		calls++
		return 9, nil
	}
	for i := 0; i < 2; i++ {
		got, err := c.GetOrLoad(ctx, "k", loader)
		if err != nil || got != 9 {
			t.Fatalf("GetOrLoad(%d) = %d, %v; want 9, nil", i, got, err)
		}
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2 with both cache tiers disabled", calls)
	}
	if c.L1().Len() != 0 {
		t.Fatalf("disabled L1 Len = %d, want 0", c.L1().Len())
	}
	if _, err := l2.Get(ctx, "k"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("disabled GetOrLoad wrote L2 err = %v, want ErrNotFound", err)
	}
}

func TestTieredCacheDisabledByEnvBypassesLocalAndRemote(t *testing.T) {
	t.Setenv(envCacheDisabled, "yes")
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2)
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	c.Set(ctx, "k", 5)
	if _, ok := c.Get(ctx, "k"); ok {
		t.Fatalf("env-disabled Get after Set returned value")
	}
	if _, err := l2.Get(ctx, "k"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("env-disabled Set wrote L2 err = %v, want ErrNotFound", err)
	}
}

func TestTieredCacheCanDisableRemoteOnly(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2, WithTieredRemoteDisabled[int](true))
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	c.Set(ctx, "k", 5)
	if got, ok := c.Get(ctx, "k"); !ok || got != 5 {
		t.Fatalf("remote-disabled local Get = %d, %v; want 5, true", got, ok)
	}
	if _, err := l2.Get(ctx, "k"); !errors.Is(err, kv.ErrNotFound) {
		t.Fatalf("remote-disabled Set wrote L2 err = %v, want ErrNotFound", err)
	}
}

func TestTieredCacheGetOrLoadNoLoader(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2)
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	if _, err := c.GetOrLoad(context.Background(), "k"); !errors.Is(err, ErrLoaderNil) {
		t.Fatalf("GetOrLoad err = %v; want ErrLoaderNil", err)
	}
}

func TestTieredCacheDelete(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2)
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	c.Set(ctx, "k", 5)

	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := c.Get(ctx, "k"); ok {
		t.Fatalf("Get after Delete returned a value")
	}
	// Deleting a missing key is not an error.
	if err := c.Delete(ctx, "missing"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestNewTieredNilBackend(t *testing.T) {
	if _, err := NewTiered[int](nil); err == nil {
		t.Fatal("NewTiered(nil) = nil error; want error")
	}
}

func TestTieredCacheOptionsNilGuard(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2, nil, WithL1[int](nil), WithCodec[int](nil), WithTieredLoader[int](nil), WithL2TTL[int](-1), WithNamespace[int](""))
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	if c.l1 == nil {
		t.Fatal("l1 should not be nil after nil option guard")
	}
	if c.codec == nil {
		t.Fatal("codec should not be nil after nil option guard")
	}
	if c.l2TTL != time.Minute {
		t.Fatalf("l2TTL = %v, want 1m (negative ignored)", c.l2TTL)
	}
	if c.namespace != "" {
		t.Fatalf("namespace = %q, want empty", c.namespace)
	}

	// custom L1 and codec
	customL1 := New[int](WithName[int]("custom"))
	customCodec := JSONCodec[int]{}
	c2, err := NewTiered[int](l2, WithL1[int](customL1), WithCodec[int](customCodec), WithL2TTL[int](5*time.Second), WithNamespace[int]("ns"))
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	if c2.l1 != customL1 {
		t.Fatal("custom L1 not applied")
	}
	if c2.l2TTL != 5*time.Second {
		t.Fatalf("l2TTL = %v, want 5s", c2.l2TTL)
	}
	if c2.namespace != "ns" {
		t.Fatalf("namespace = %q, want ns", c2.namespace)
	}
}

func TestTieredCacheSetEmptyKeyAndDisabledRemote(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2, WithTieredRemoteDisabled[int](true))
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	c.Set(ctx, "", 5)
	if c.L1().Len() != 0 {
		t.Fatal("Set empty key should be no-op")
	}
	c.Set(ctx, "k", 5)
	if c.L1().Len() != 1 {
		t.Fatal("Set with remote disabled should still write L1")
	}
}

func TestTieredCacheGetEmptyKeyAndLocalDisabled(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2, WithTieredLocalDisabled[int](true))
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	if _, ok := c.Get(ctx, ""); ok {
		t.Fatal("Get empty key should miss")
	}
	c.Set(ctx, "k", 5)
	if _, ok := c.Get(ctx, "k"); !ok {
		t.Fatal("Get with local disabled should read from L2")
	}
}

func TestTieredCacheDeleteEmptyKeyAndRemoteDisabled(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2, WithTieredRemoteDisabled[int](true))
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	if err := c.Delete(ctx, ""); err != nil {
		t.Fatalf("Delete empty key error = %v", err)
	}
	c.Set(ctx, "k", 5)
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := c.Get(ctx, "k"); ok {
		t.Fatal("Get after Delete should miss")
	}
}

func TestTieredCacheGetOrLoadEmptyKeyAndNoLoader(t *testing.T) {
	l2 := kv.NewMemoryStore()
	c, err := NewTiered[int](l2)
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	_, err = c.GetOrLoad(context.Background(), "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetOrLoad empty key = %v, want ErrNotFound", err)
	}
}

func TestTieredCacheGetL2Error(t *testing.T) {
	l2 := &failingStore{err: errors.New("boom")}
	c, err := NewTiered[int](l2)
	if err != nil {
		t.Fatalf("NewTiered: %v", err)
	}
	ctx := context.Background()
	if _, ok := c.Get(ctx, "k"); ok {
		t.Fatal("Get with failing L2 should miss")
	}
	_, err = c.GetOrLoad(ctx, "k", func(_ context.Context, _ string) (int, error) { return 7, nil })
	if err != nil {
		t.Fatalf("GetOrLoad loader fallback: %v", err)
	}
}

type failingStore struct {
	err error
}

func (f *failingStore) Get(context.Context, string) ([]byte, error)              { return nil, f.err }
func (f *failingStore) Set(context.Context, string, []byte, time.Duration) error { return f.err }
func (f *failingStore) SetNX(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, f.err
}
func (f *failingStore) Delete(context.Context, string) (bool, error)       { return false, f.err }
func (f *failingStore) Exists(context.Context, string) (bool, error)       { return false, f.err }
func (f *failingStore) TTL(context.Context, string) (time.Duration, error) { return 0, f.err }
func (f *failingStore) Close() error                                       { return nil }
