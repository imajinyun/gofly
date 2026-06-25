// Package lock provides distributed and in-process locking with lease-based
// expiration and optional watchdog refresh.
package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	core "github.com/imajinyun/gofly/core"
	"github.com/imajinyun/gofly/core/kv"
)

var (
	// ErrLocked is returned when a lock is already held.
	ErrLocked = errors.New("lock already held")
	// ErrNotHeld is returned when unlocking a lock that is not held.
	ErrNotHeld = errors.New("lock not held")
	// ErrLeaseExpired is returned when a lease has expired.
	ErrLeaseExpired = errors.New("lock lease expired")
)

// Lease represents a held lock with an expiration time.
type Lease struct {
	Key       string    `json:"key"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Locker is the common interface for lock implementations.
type Locker interface {
	TryLock(ctx context.Context, key string, ttl time.Duration) (Lease, error)
	Lock(ctx context.Context, key string, ttl time.Duration) (Lease, error)
	Unlock(ctx context.Context, lease Lease) error
	Refresh(ctx context.Context, lease Lease, ttl time.Duration) (Lease, error)
}

// MemoryOption customises the in-memory locker.
type MemoryOption func(*MemoryLocker)

// KVOption customises the KV-backed locker.
type KVOption func(*KVLocker)

// MemoryStats reports the current state of a MemoryLocker.
type MemoryStats struct {
	Active    int   `json:"active"`
	Acquired  int64 `json:"acquired"`
	Contended int64 `json:"contended"`
	Released  int64 `json:"released"`
	Refreshed int64 `json:"refreshed"`
	Expired   int64 `json:"expired"`
	Failed    int64 `json:"failed"`
}

type lockState struct {
	token     string
	expiresAt time.Time
}

type MemoryLocker struct {
	mu            sync.Mutex
	locks         map[string]lockState
	defaultTTL    time.Duration
	retryInterval time.Duration
	newToken      func() (string, error)
	stats         MemoryStats
}

type KVLocker struct {
	store         kv.Store
	defaultTTL    time.Duration
	retryInterval time.Duration
	newToken      func() (string, error)
}

func NewMemoryLocker(opts ...MemoryOption) *MemoryLocker {
	l := &MemoryLocker{
		locks:         make(map[string]lockState),
		defaultTTL:    30 * time.Second,
		retryInterval: 25 * time.Millisecond,
		newToken:      randomToken,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(l)
		}
	}
	return l
}

func WithDefaultTTL(ttl time.Duration) MemoryOption {
	return func(l *MemoryLocker) {
		if ttl > 0 {
			l.defaultTTL = ttl
		}
	}
}

func WithRetryInterval(interval time.Duration) MemoryOption {
	return func(l *MemoryLocker) {
		if interval > 0 {
			l.retryInterval = interval
		}
	}
}

func WithTokenGenerator(fn func() (string, error)) MemoryOption {
	return func(l *MemoryLocker) {
		if fn != nil {
			l.newToken = fn
		}
	}
}

func NewKVLocker(store kv.Store, opts ...KVOption) *KVLocker {
	l := &KVLocker{
		store:         store,
		defaultTTL:    30 * time.Second,
		retryInterval: 25 * time.Millisecond,
		newToken:      randomToken,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(l)
		}
	}
	return l
}

func WithKVDefaultTTL(ttl time.Duration) KVOption {
	return func(l *KVLocker) {
		if ttl > 0 {
			l.defaultTTL = ttl
		}
	}
}

func WithKVRetryInterval(interval time.Duration) KVOption {
	return func(l *KVLocker) {
		if interval > 0 {
			l.retryInterval = interval
		}
	}
}

func WithKVTokenGenerator(fn func() (string, error)) KVOption {
	return func(l *KVLocker) {
		if fn != nil {
			l.newToken = fn
		}
	}
}

func (l *MemoryLocker) TryLock(ctx context.Context, key string, ttl time.Duration) (Lease, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	if key == "" {
		return Lease{}, ErrNotHeld
	}
	return l.tryLockAt(key, ttl, time.Now())
}

func (l *MemoryLocker) Lock(ctx context.Context, key string, ttl time.Duration) (Lease, error) {
	ctx = core.Context(ctx)
	lease, err := l.TryLock(ctx, key, ttl)
	if err == nil {
		return lease, nil
	}
	if !errors.Is(err, ErrLocked) {
		return Lease{}, err
	}
	ticker := time.NewTicker(l.retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Lease{}, ctx.Err()
		case <-ticker.C:
			lease, err := l.TryLock(ctx, key, ttl)
			if err == nil {
				return lease, nil
			}
			if !errors.Is(err, ErrLocked) {
				return Lease{}, err
			}
		}
	}
}

func (l *MemoryLocker) Unlock(ctx context.Context, lease Lease) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.cleanupExpiredLocked(now)
	state, ok := l.locks[lease.Key]
	if !ok {
		l.stats.Failed++
		return ErrNotHeld
	}
	if state.token != lease.Token {
		l.stats.Failed++
		return ErrNotHeld
	}
	if !now.Before(state.expiresAt) {
		delete(l.locks, lease.Key)
		l.stats.Expired++
		return ErrLeaseExpired
	}
	delete(l.locks, lease.Key)
	l.stats.Released++
	return nil
}

func (l *MemoryLocker) Refresh(ctx context.Context, lease Lease, ttl time.Duration) (Lease, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.cleanupExpiredLocked(now)
	state, ok := l.locks[lease.Key]
	if !ok {
		l.stats.Failed++
		return Lease{}, ErrNotHeld
	}
	if state.token != lease.Token {
		l.stats.Failed++
		return Lease{}, ErrNotHeld
	}
	if !now.Before(state.expiresAt) {
		delete(l.locks, lease.Key)
		l.stats.Expired++
		return Lease{}, ErrLeaseExpired
	}
	next := Lease{Key: lease.Key, Token: lease.Token, ExpiresAt: now.Add(l.ttl(ttl))}
	l.locks[lease.Key] = lockState{token: lease.Token, expiresAt: next.ExpiresAt}
	l.stats.Refreshed++
	return next, nil
}

func (l *MemoryLocker) Snapshot() MemoryStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupExpiredLocked(time.Now())
	snapshot := l.stats
	snapshot.Active = len(l.locks)
	return snapshot
}

func (l *KVLocker) TryLock(ctx context.Context, key string, ttl time.Duration) (Lease, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	if l == nil || l.store == nil || key == "" {
		return Lease{}, ErrNotHeld
	}
	token, err := l.newToken()
	if err != nil {
		return Lease{}, fmt.Errorf("generate lock token: %w", err)
	}
	ttl = l.ttl(ttl)
	ok, err := l.store.SetNX(ctx, key, []byte(token), ttl)
	if err != nil {
		return Lease{}, err
	}
	if !ok {
		return Lease{}, ErrLocked
	}
	return Lease{Key: key, Token: token, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (l *KVLocker) Lock(ctx context.Context, key string, ttl time.Duration) (Lease, error) {
	ctx = core.Context(ctx)
	lease, err := l.TryLock(ctx, key, ttl)
	if err == nil {
		return lease, nil
	}
	if !errors.Is(err, ErrLocked) {
		return Lease{}, err
	}
	interval := l.retryInterval
	if interval <= 0 {
		interval = 25 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Lease{}, ctx.Err()
		case <-ticker.C:
			lease, err := l.TryLock(ctx, key, ttl)
			if err == nil {
				return lease, nil
			}
			if !errors.Is(err, ErrLocked) {
				return Lease{}, err
			}
		}
	}
}

func (l *KVLocker) Unlock(ctx context.Context, lease Lease) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil || l.store == nil || lease.Key == "" || lease.Token == "" {
		return ErrNotHeld
	}
	value, err := l.store.Get(ctx, lease.Key)
	if errors.Is(err, kv.ErrNotFound) {
		return ErrNotHeld
	}
	if err != nil {
		return err
	}
	if string(value) != lease.Token {
		return ErrNotHeld
	}
	_, err = l.store.Delete(ctx, lease.Key)
	return err
}

func (l *KVLocker) Refresh(ctx context.Context, lease Lease, ttl time.Duration) (Lease, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	if l == nil || l.store == nil || lease.Key == "" || lease.Token == "" {
		return Lease{}, ErrNotHeld
	}
	value, err := l.store.Get(ctx, lease.Key)
	if errors.Is(err, kv.ErrNotFound) {
		return Lease{}, ErrLeaseExpired
	}
	if err != nil {
		return Lease{}, err
	}
	if string(value) != lease.Token {
		return Lease{}, ErrNotHeld
	}
	ttl = l.ttl(ttl)
	if err := l.store.Set(ctx, lease.Key, []byte(lease.Token), ttl); err != nil {
		return Lease{}, err
	}
	return Lease{Key: lease.Key, Token: lease.Token, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (l *MemoryLocker) tryLockAt(key string, ttl time.Duration, now time.Time) (Lease, error) {
	token, err := l.newToken()
	if err != nil {
		return Lease{}, fmt.Errorf("generate lock token: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupExpiredLocked(now)
	if state, ok := l.locks[key]; ok && now.Before(state.expiresAt) {
		l.stats.Contended++
		return Lease{}, ErrLocked
	}
	lease := Lease{Key: key, Token: token, ExpiresAt: now.Add(l.ttl(ttl))}
	l.locks[key] = lockState{token: token, expiresAt: lease.ExpiresAt}
	l.stats.Acquired++
	return lease, nil
}

func (l *MemoryLocker) cleanupExpiredLocked(now time.Time) {
	for key, state := range l.locks {
		if !now.Before(state.expiresAt) {
			delete(l.locks, key)
			l.stats.Expired++
		}
	}
}

func (l *MemoryLocker) ttl(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	return l.defaultTTL
}

func (l *KVLocker) ttl(ttl time.Duration) time.Duration {
	if ttl > 0 {
		return ttl
	}
	if l == nil || l.defaultTTL <= 0 {
		return 30 * time.Second
	}
	return l.defaultTTL
}

func randomToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
