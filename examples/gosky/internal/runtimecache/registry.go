// Package runtimecache is the named registry of process-local caches that
// the unmodified SmartAdmin cache console (support/cache) can inspect and
// clear. Every entry is code-owned; cache names never come from a policy row
// or browser payload.
package runtimecache

import (
	"fmt"
	"sort"
	"sync"
)

// Cache is one named process-local cache. Keys returns a bounded snapshot
// of current entry keys; Clear drops all entries.
type Cache interface {
	Keys() ([]string, error)
	Clear() error
}

// Registry maps code-owned names to cache handles.
type Registry struct {
	mu     sync.RWMutex
	caches map[string]Cache
}

// NewRegistry returns an empty cache registry.
func NewRegistry() *Registry {
	return &Registry{caches: make(map[string]Cache)}
}

// Register installs a cache under a name. Duplicate registration replaces
// the previous handle (package wiring happens once at startup).
func (r *Registry) Register(name string, cache Cache) error {
	if r == nil {
		return fmt.Errorf("cache registry is nil")
	}
	if name == "" {
		return fmt.Errorf("cache name is required")
	}
	if cache == nil {
		return fmt.Errorf("cache %q handle is nil", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caches[name] = cache
	return nil
}

// Names returns sorted registered cache names.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.caches))
	for name := range r.caches {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get resolves a registered cache.
func (r *Registry) Get(name string) (Cache, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cache, ok := r.caches[name]
	return cache, ok
}

// ClearAll clears every registered cache and returns the first error while
// continuing with the remaining caches.
func (r *Registry) ClearAll() error {
	if r == nil {
		return nil
	}
	var firstErr error
	for _, name := range r.Names() {
		cache, ok := r.Get(name)
		if !ok {
			continue
		}
		if err := cache.Clear(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Func adapts a pair of closures to the Cache interface.
type Func struct {
	KeysFn  func() ([]string, error)
	ClearFn func() error
}

// Keys calls the registered keys closure.
func (f Func) Keys() ([]string, error) {
	if f.KeysFn == nil {
		return []string{}, nil
	}
	return f.KeysFn()
}

// Clear calls the registered clear closure.
func (f Func) Clear() error {
	if f.ClearFn == nil {
		return nil
	}
	return f.ClearFn()
}
