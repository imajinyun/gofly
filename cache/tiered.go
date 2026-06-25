// Package cache provides in-memory caching with TTL, single-flight loading,
// bloom-filter negative caching, and optional Redis-backed tiered caching.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/imajinyun/gofly/core"
	"github.com/imajinyun/gofly/core/kv"
)

// Codec serialises cache values for the shared (L2) backend.
type Codec[T any] interface {
	Marshal(T) ([]byte, error)
	Unmarshal([]byte) (T, error)
}

// JSONCodec is the default Codec; it (de)serialises values as JSON.
type JSONCodec[T any] struct{}

func (JSONCodec[T]) Marshal(value T) ([]byte, error) { return json.Marshal(value) }

func (JSONCodec[T]) Unmarshal(data []byte) (T, error) {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}
	return value, nil
}

// TieredCache combines a process-local L1 cache with a shared L2 backend
// (any kv.Store, e.g. Redis). Reads fall through L1 -> L2 -> loader and
// back-fill the faster layers; writes and deletes are applied to both layers.
type TieredCache[T any] struct {
	l1             *Cache[T]
	l2             kv.Store
	codec          Codec[T]
	loader         Loader[T]
	l2TTL          time.Duration
	namespace      string
	disabled       bool
	localDisabled  bool
	remoteDisabled bool
}

// TieredOption customises a TieredCache.
type TieredOption[T any] func(*TieredCache[T])

// WithL1 supplies a pre-configured L1 cache instead of the default.
func WithL1[T any](l1 *Cache[T]) TieredOption[T] {
	return func(t *TieredCache[T]) {
		if l1 != nil {
			t.l1 = l1
		}
	}
}

// WithCodec overrides the default JSON codec used for the L2 backend.
func WithCodec[T any](codec Codec[T]) TieredOption[T] {
	return func(t *TieredCache[T]) {
		if codec != nil {
			t.codec = codec
		}
	}
}

// WithLoader registers the source-of-truth loader used on a full miss.
func WithTieredLoader[T any](loader Loader[T]) TieredOption[T] {
	return func(t *TieredCache[T]) {
		t.loader = loader
	}
}

// WithL2TTL sets the TTL applied to entries written to the L2 backend.
func WithL2TTL[T any](ttl time.Duration) TieredOption[T] {
	return func(t *TieredCache[T]) {
		if ttl > 0 {
			t.l2TTL = ttl
		}
	}
}

// WithNamespace prefixes every L2 key, allowing multiple caches to share one
// backend without collisions.
func WithNamespace[T any](namespace string) TieredOption[T] {
	return func(t *TieredCache[T]) {
		t.namespace = namespace
	}
}

// WithTieredDisabled disables both local L1 and shared L2 cache access. The
// tiered cache becomes a pass-through loader: reads do not consult either
// cache, writes are ignored, and loaded values are not back-filled.
func WithTieredDisabled[T any](disabled bool) TieredOption[T] {
	return func(t *TieredCache[T]) {
		t.disabled = disabled
	}
}

// WithTieredLocalDisabled disables the in-process L1 cache while preserving L2
// behavior unless the whole tiered cache or remote layer is also disabled.
func WithTieredLocalDisabled[T any](disabled bool) TieredOption[T] {
	return func(t *TieredCache[T]) {
		t.localDisabled = disabled
	}
}

// WithTieredRemoteDisabled disables the shared L2 backend while preserving L1
// behavior unless the whole tiered cache or local layer is also disabled.
func WithTieredRemoteDisabled[T any](disabled bool) TieredOption[T] {
	return func(t *TieredCache[T]) {
		t.remoteDisabled = disabled
	}
}

// NewTiered builds a TieredCache over the given shared backend.
func NewTiered[T any](l2 kv.Store, opts ...TieredOption[T]) (*TieredCache[T], error) {
	if l2 == nil {
		return nil, errors.New("cache: l2 backend is nil")
	}
	t := &TieredCache[T]{
		l1:       New[T](),
		l2:       l2,
		codec:    JSONCodec[T]{},
		l2TTL:    time.Minute,
		disabled: cacheDisabledByEnv(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	if t.l1 == nil {
		t.l1 = New[T]()
	}
	if t.codec == nil {
		t.codec = JSONCodec[T]{}
	}
	if t.disabled {
		t.localDisabled = true
		t.remoteDisabled = true
	}
	return t, nil
}

// L1 exposes the local cache for stats or direct access.
func (t *TieredCache[T]) L1() *Cache[T] { return t.l1 }

// Get returns a value from L1 or L2 without invoking the loader.
func (t *TieredCache[T]) Get(ctx context.Context, key string) (T, bool) {
	var zero T
	if key == "" {
		return zero, false
	}
	if t.localEnabled() {
		if value, ok := t.l1.Get(key); ok {
			return value, true
		}
	}
	if !t.remoteEnabled() {
		return zero, false
	}
	value, ok, err := t.getL2(ctx, key)
	if err != nil || !ok {
		return zero, false
	}
	if t.localEnabled() {
		t.l1.Set(key, value)
	}
	return value, true
}

// GetOrLoad resolves key through L1 -> L2 -> loader, back-filling faster layers.
func (t *TieredCache[T]) GetOrLoad(ctx context.Context, key string, loaders ...Loader[T]) (T, error) {
	ctx = core.Context(ctx)
	var zero T
	if key == "" {
		return zero, ErrNotFound
	}
	if t.localEnabled() {
		if value, ok := t.l1.Get(key); ok {
			return value, nil
		}
	}
	if t.remoteEnabled() {
		if value, ok, err := t.getL2(ctx, key); err == nil && ok {
			if t.localEnabled() {
				t.l1.Set(key, value)
			}
			return value, nil
		}
	}
	loader := firstLoader(t.loader, loaders...)
	if loader == nil {
		return zero, ErrLoaderNil
	}
	value, err := loader(ctx, key)
	if err != nil {
		return zero, fmt.Errorf("load cache entry: %w", err)
	}
	t.set(ctx, key, value)
	return value, nil
}

// Set writes value to enabled layers.
func (t *TieredCache[T]) Set(ctx context.Context, key string, value T) {
	if key == "" {
		return
	}
	t.set(ctx, key, value)
}

// Delete removes key from enabled layers.
func (t *TieredCache[T]) Delete(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	if t.localEnabled() {
		t.l1.Delete(key)
	}
	if !t.remoteEnabled() {
		return nil
	}
	_, err := t.l2.Delete(ctx, t.l2Key(key))
	if err != nil && !errors.Is(err, kv.ErrNotFound) {
		return err
	}
	return nil
}

func (t *TieredCache[T]) set(ctx context.Context, key string, value T) {
	if t.localEnabled() {
		t.l1.Set(key, value)
	}
	if !t.remoteEnabled() {
		return
	}
	data, err := t.codec.Marshal(value)
	if err != nil {
		return
	}
	_ = t.l2.Set(ctx, t.l2Key(key), data, t.l2TTL)
}

func (t *TieredCache[T]) getL2(ctx context.Context, key string) (T, bool, error) {
	var zero T
	if !t.remoteEnabled() {
		return zero, false, nil
	}
	data, err := t.l2.Get(ctx, t.l2Key(key))
	if err != nil {
		if errors.Is(err, kv.ErrNotFound) {
			return zero, false, nil
		}
		return zero, false, err
	}
	value, err := t.codec.Unmarshal(data)
	if err != nil {
		return zero, false, err
	}
	return value, true, nil
}

func (t *TieredCache[T]) localEnabled() bool {
	return t != nil && !t.disabled && !t.localDisabled && t.l1 != nil
}

func (t *TieredCache[T]) remoteEnabled() bool {
	return t != nil && !t.disabled && !t.remoteDisabled && t.l2 != nil
}

func (t *TieredCache[T]) l2Key(key string) string {
	if t.namespace == "" {
		return key
	}
	return t.namespace + ":" + key
}
