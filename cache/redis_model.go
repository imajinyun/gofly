// Package cache provides in-memory caching with TTL, single-flight loading,
// bloom-filter negative caching, and optional Redis-backed tiered caching.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	core "github.com/imajinyun/gofly/core"
)

// RedisModelClient is the minimal Redis command surface required by
// RedisModelCache. *core/kv/redis.Client satisfies this interface.
type RedisModelClient interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) (bool, error)
}

// RedisModelOption customizes a Redis-backed model cache.
type RedisModelOption[T any, K comparable] func(*RedisModelCache[T, K])

// RedisModelCache implements strict cache-aside semantics for generated model
// repositories: read Redis first, load from the backing repository on miss, and
// write-through/invalidate Redis after successful repository mutations.
type RedisModelCache[T any, K comparable] struct {
	client      RedisModelClient
	loader      ModelLoader[T, K]
	key         ModelKeyFunc[K]
	prefix      string
	ttl         time.Duration
	notFoundErr error
}

// NewRedisModel creates a Redis-backed model cache. Redis misses are detected
// with notFoundErr; generated repositories pass core/kv/redis.ErrNil.
func NewRedisModel[T any, K comparable](loader ModelLoader[T, K], client RedisModelClient, opts ...RedisModelOption[T, K]) *RedisModelCache[T, K] {
	m := &RedisModelCache[T, K]{
		client:      client,
		loader:      loader,
		key:         defaultModelKey[K],
		ttl:         time.Minute,
		notFoundErr: ErrNotFound,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if m.key == nil {
		m.key = defaultModelKey[K]
	}
	if m.ttl <= 0 {
		m.ttl = time.Minute
	}
	if m.notFoundErr == nil {
		m.notFoundErr = ErrNotFound
	}
	return m
}

func WithRedisModelKey[T any, K comparable](key ModelKeyFunc[K]) RedisModelOption[T, K] {
	return func(m *RedisModelCache[T, K]) {
		if key != nil {
			m.key = key
		}
	}
}

func WithRedisModelKeyPrefix[T any, K comparable](prefix string) RedisModelOption[T, K] {
	return func(m *RedisModelCache[T, K]) {
		m.prefix = strings.TrimSpace(prefix)
	}
}

func WithRedisModelTTL[T any, K comparable](ttl time.Duration) RedisModelOption[T, K] {
	return func(m *RedisModelCache[T, K]) {
		if ttl > 0 {
			m.ttl = ttl
		}
	}
}

func WithRedisModelNotFound[T any, K comparable](notFound error) RedisModelOption[T, K] {
	return func(m *RedisModelCache[T, K]) {
		if notFound != nil {
			m.notFoundErr = notFound
		}
	}
}

func (m *RedisModelCache[T, K]) Get(ctx context.Context, id K) (T, error) {
	ctx = core.Context(ctx)
	var zero T
	if m == nil || m.client == nil {
		return zero, errors.New("redis model cache client is nil")
	}
	if m.loader == nil {
		return zero, ErrLoaderNil
	}
	key := m.cacheKey(id)
	data, err := m.client.Get(ctx, key)
	if err == nil {
		var value T
		if err := json.Unmarshal(data, &value); err != nil {
			return zero, fmt.Errorf("decode redis model cache %q: %w", key, err)
		}
		return value, nil
	}
	if !errors.Is(err, m.notFoundErr) {
		return zero, fmt.Errorf("get redis model cache %q: %w", key, err)
	}
	value, err := m.loader(ctx, id)
	if err != nil {
		return zero, fmt.Errorf("load redis model cache %q: %w", key, err)
	}
	if err := m.Set(ctx, id, value); err != nil {
		return zero, err
	}
	return value, nil
}

func (m *RedisModelCache[T, K]) Set(ctx context.Context, id K, value T) error {
	ctx = core.Context(ctx)
	if m == nil || m.client == nil {
		return errors.New("redis model cache client is nil")
	}
	key := m.cacheKey(id)
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode redis model cache %q: %w", key, err)
	}
	if err := m.client.Set(ctx, key, data, m.ttl); err != nil {
		return fmt.Errorf("set redis model cache %q: %w", key, err)
	}
	return nil
}

func (m *RedisModelCache[T, K]) Invalidate(ctx context.Context, id K) error {
	ctx = core.Context(ctx)
	if m == nil || m.client == nil {
		return errors.New("redis model cache client is nil")
	}
	key := m.cacheKey(id)
	if _, err := m.client.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete redis model cache %q: %w", key, err)
	}
	return nil
}

func (m *RedisModelCache[T, K]) cacheKey(id K) string {
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
