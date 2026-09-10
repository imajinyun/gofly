package grpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/syncx"
)

const (
	defaultAppTokenProxyMaxEntries = 128
	defaultAppTokenProxyTTL        = 10 * time.Minute
)

type appTokenProxyEntry struct {
	conn     *ClientConn
	lastUsed time.Time
	expires  time.Time
}

type AppTokenProxyOption func(*AppTokenProxy)

// AppTokenProxySnapshot is a secret-free view of the connection cache.
type AppTokenProxySnapshot struct {
	Entries    int
	MaxEntries int
	TTL        time.Duration
	Closed     bool
}

// AppTokenProxy caches a bounded number of gRPC connections by app/token
// identity. Cache keys contain only one-way credential digests.
type AppTokenProxy struct {
	target     string
	options    []ClientOption
	maxEntries int
	ttl        time.Duration
	now        func() time.Time

	mu      sync.Mutex
	entries map[string]appTokenProxyEntry
	closed  bool
	group   syncx.Group[*ClientConn]
}

func NewAppTokenProxy(target string, opts ...AppTokenProxyOption) (*AppTokenProxy, error) {
	if target == "" {
		return nil, errors.New("grpc proxy target is required")
	}
	proxy := &AppTokenProxy{
		target:     target,
		maxEntries: defaultAppTokenProxyMaxEntries,
		ttl:        defaultAppTokenProxyTTL,
		now:        time.Now,
		entries:    make(map[string]appTokenProxyEntry),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(proxy)
		}
	}
	return proxy, nil
}

func WithAppTokenProxyMaxEntries(maxEntries int) AppTokenProxyOption {
	return func(proxy *AppTokenProxy) {
		if maxEntries > 0 {
			proxy.maxEntries = maxEntries
		}
	}
}

func WithAppTokenProxyTTL(ttl time.Duration) AppTokenProxyOption {
	return func(proxy *AppTokenProxy) {
		if ttl > 0 {
			proxy.ttl = ttl
		}
	}
}

func WithAppTokenProxyClientOptions(opts ...ClientOption) AppTokenProxyOption {
	return func(proxy *AppTokenProxy) {
		proxy.options = append(proxy.options, opts...)
	}
}

func (p *AppTokenProxy) TakeConn(ctx context.Context, credential AppTokenCredentials) (*ClientConn, error) {
	if p == nil {
		return nil, errors.New("grpc app token proxy is nil")
	}
	if credential.App == "" || credential.Token == "" {
		return nil, auth.ErrMissingCredentials
	}
	key := appTokenProxyKey(credential.App, credential.Token)
	if conn, ok := p.cached(key); ok {
		return conn, nil
	}
	conn, _, err := p.group.Do(ctx, key, func(ctx context.Context) (*ClientConn, error) {
		if conn, ok := p.cached(key); ok {
			return conn, nil
		}
		if p.isClosed() {
			return nil, errors.New("grpc app token proxy is closed")
		}
		opts := append([]ClientOption(nil), p.options...)
		opts = append(opts, WithAppTokenCredentials(credential))
		conn, err := Dial(ctx, p.target, opts...)
		if err != nil {
			return nil, err
		}
		if err := p.store(key, conn); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return conn, nil
	})
	return conn, err
}

func (p *AppTokenProxy) cached(key string) (*ClientConn, bool) {
	now := p.now()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, false
	}
	entry, ok := p.entries[key]
	if !ok {
		p.mu.Unlock()
		return nil, false
	}
	if !now.Before(entry.expires) {
		delete(p.entries, key)
		p.mu.Unlock()
		_ = entry.conn.Close()
		return nil, false
	}
	entry.lastUsed = now
	p.entries[key] = entry
	p.mu.Unlock()
	return entry.conn, true
}

func (p *AppTokenProxy) store(key string, conn *ClientConn) error {
	now := p.now()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("grpc app token proxy is closed")
	}
	var evicted *ClientConn
	if len(p.entries) >= p.maxEntries {
		oldestKey := ""
		var oldest time.Time
		for candidate, entry := range p.entries {
			if oldestKey == "" || entry.lastUsed.Before(oldest) {
				oldestKey, oldest = candidate, entry.lastUsed
			}
		}
		evicted = p.entries[oldestKey].conn
		delete(p.entries, oldestKey)
	}
	p.entries[key] = appTokenProxyEntry{conn: conn, lastUsed: now, expires: now.Add(p.ttl)}
	p.mu.Unlock()
	if evicted != nil {
		if err := evicted.Close(); err != nil {
			p.mu.Lock()
			if stored, ok := p.entries[key]; ok && stored.conn == conn {
				delete(p.entries, key)
			}
			p.mu.Unlock()
			return fmt.Errorf("close evicted grpc proxy connection: %w", err)
		}
	}
	return nil
}

func (p *AppTokenProxy) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *AppTokenProxy) Snapshot() AppTokenProxySnapshot {
	if p == nil {
		return AppTokenProxySnapshot{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return AppTokenProxySnapshot{Entries: len(p.entries), MaxEntries: p.maxEntries, TTL: p.ttl, Closed: p.closed}
}

func (p *AppTokenProxy) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	entries := p.entries
	p.entries = make(map[string]appTokenProxyEntry)
	p.mu.Unlock()
	var closeErr error
	for _, entry := range entries {
		if err := entry.conn.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func appTokenProxyKey(app, token string) string {
	digest := sha256.Sum256([]byte(app + "\x00" + token))
	return hex.EncodeToString(digest[:])
}
