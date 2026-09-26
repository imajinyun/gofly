package cache

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestCacheAccessOrder(t *testing.T) {
	for _, clock := range []struct {
		name string
		wrap bool
	}{
		{name: "equal timestamps"},
		{name: "sequence rollover", wrap: true},
	} {
		t.Run(clock.name, func(t *testing.T) {
			for _, tc := range []struct{ name string }{
				{name: "get"},
				{name: "replace"},
				{name: "fresh lookup"},
				{name: "load"},
				{name: "negative lookup"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						c := New[string](WithMaxEntries[string](2), WithNegativeCache[string](time.Minute, ErrNotFound))
						c.Set("a", "one")
						c.Set("b", "two")
						if clock.wrap {
							// Reaching this boundary through public calls would require 2^64 accesses.
							c.mu.Lock()
							c.nextAccessOrder = ^uint64(0)
							c.mu.Unlock()
						}
						startedAt := time.Now()
						loads := 0
						loader := func(context.Context, string) (string, error) {
							loads++
							if tc.name == "negative lookup" {
								return "", ErrNotFound
							}
							return "one", nil
						}
						switch tc.name {
						case "get":
							if got, ok := c.Get("a"); !ok || got != "one" {
								t.Fatalf("Get(a) = %q, %v; want one, true", got, ok)
							}
						case "replace":
							c.Set("a", "one")
						case "fresh lookup", "load", "negative lookup":
							if tc.name != "fresh lookup" {
								c.Delete("a")
							}
							got, err := c.GetOrLoad(t.Context(), "a", loader)
							if tc.name == "negative lookup" {
								if !errors.Is(err, ErrNotFound) {
									t.Fatalf("GetOrLoad(a) = %q, %v; want ErrNotFound", got, err)
								}
								// Make b most recent, then prove a negative hit also updates recency.
								c.Get("b")
								if _, err := c.GetOrLoad(t.Context(), "a", loader); !errors.Is(err, ErrNotFound) {
									t.Fatalf("negative hit = %v, want ErrNotFound", err)
								}
							} else if err != nil || got != "one" {
								t.Fatalf("GetOrLoad(a) = %q, %v; want one, nil", got, err)
							}
						}
						c.Set("c", "three")
						if _, ok := c.Get("b"); ok {
							t.Fatal("Get(b) hit after accessing a and inserting c; want LRU eviction")
						}
						if tc.name == "negative lookup" {
							if _, err := c.GetOrLoad(t.Context(), "a", loader); !errors.Is(err, ErrNotFound) {
								t.Fatalf("retained negative entry = %v, want ErrNotFound", err)
							}
						} else if got, ok := c.Get("a"); !ok || got != "one" {
							t.Fatalf("Get(a) after eviction = %q, %v; want one, true", got, ok)
						}
						wantLoads := 0
						if tc.name == "load" || tc.name == "negative lookup" {
							wantLoads = 1
						}
						if loads != wantLoads {
							t.Fatalf("loader calls = %d, want %d", loads, wantLoads)
						}
						if got := c.Snapshot(); got.Entries != 2 || got.Evictions != 1 {
							t.Fatalf("snapshot = %+v, want 2 entries and 1 eviction", got)
						}
						if elapsed := time.Since(startedAt); elapsed != 0 {
							t.Fatalf("accesses advanced clock by %s, want equal timestamps", elapsed)
						}
					})
				})
			}
		})
	}
	t.Run("empty keys", func(t *testing.T) {
		c := New[string](WithMaxEntries[string](1))
		c.Set("", "ignored")
		if _, ok := c.Get(""); ok {
			t.Fatal("Get(empty) hit, want miss")
		}
		if _, err := c.GetOrLoad(t.Context(), "", func(context.Context, string) (string, error) {
			t.Fatal("empty key reached loader")
			return "", nil
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetOrLoad(empty) = %v, want ErrNotFound", err)
		}
		c.Set("a", "one")
		c.Set("b", "two")
		if got := c.Snapshot(); got.Entries != 1 || got.Evictions != 1 {
			t.Fatalf("snapshot = %+v, want 1 entry and 1 eviction", got)
		}
	})
	t.Run("stale lookup and refresh", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			c := New[string](WithDefaultTTL[string](time.Second), WithStaleWhileRevalidate[string](time.Minute), WithMaxEntries[string](2))
			c.Set("a", "old")
			c.Set("b", "two")
			time.Sleep(time.Second)
			release := make(chan struct{})
			defer close(release)
			got, err := c.GetOrLoad(t.Context(), "a", func(context.Context, string) (string, error) {
				<-release
				return "new", nil
			})
			if err != nil || got != "old" {
				t.Fatalf("stale lookup = %q, %v; want old, nil", got, err)
			}
			synctest.Wait()
			c.Set("c", "three")
			if _, ok := c.Get("b"); ok {
				t.Fatal("stale hit did not protect a over b")
			}
			release <- struct{}{}
			synctest.Wait()
			// The completed refresh must now protect a over the more recently inserted c.
			c.Set("d", "four")
			if _, ok := c.Get("c"); ok {
				t.Fatal("refresh did not update recency before inserting d")
			}
			if got, ok := c.Get("a"); !ok || got != "new" {
				t.Fatalf("refreshed Get(a) = %q, %v; want new, true", got, ok)
			}
		})
	})
}
