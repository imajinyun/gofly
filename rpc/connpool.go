// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/gofly/gofly/core"
)

var (
	// ErrConnPoolClosed is returned when operating on a closed pool.
	ErrConnPoolClosed = errors.New("rpc connection pool is closed")
	// ErrConnPoolExhausted is returned when the pool has no available slots.
	ErrConnPoolExhausted = errors.New("rpc connection pool is exhausted")
)

// ConnPoolConfig controls connection pool sizing and timeouts.
type ConnPoolConfig struct {
	MaxIdle      int
	MaxActive    int
	IdleTimeout  time.Duration
	MaxLifetime  time.Duration
	DialTimeout  time.Duration
	WaitInterval time.Duration
}

// DefaultConnPoolConfig returns sensible defaults.
func DefaultConnPoolConfig() ConnPoolConfig {
	return ConnPoolConfig{MaxIdle: 16, MaxActive: 64, IdleTimeout: time.Minute, MaxLifetime: 30 * time.Minute, DialTimeout: 3 * time.Second, WaitInterval: 5 * time.Millisecond}
}

// ConnPoolStats reports the current pool state.
type ConnPoolStats struct {
	Idle    int   `json:"idle"`
	Active  int   `json:"active"`
	Created int64 `json:"created"`
	Reused  int64 `json:"reused"`
	Closed  int64 `json:"closed"`
	Waits   int64 `json:"waits"`
}

type ConnDialer func(context.Context) (net.Conn, error)

type ConnPool struct {
	mu     sync.Mutex
	dial   ConnDialer
	conf   ConnPoolConfig
	idle   []*PooledConn
	active int
	closed bool

	created atomic.Int64
	reused  atomic.Int64
	closedN atomic.Int64
	waits   atomic.Int64
}

type PooledConn struct {
	net.Conn
	pool     *ConnPool
	created  time.Time
	lastUsed time.Time
	released atomic.Bool
}

func NewConnPool(network, address string, conf ConnPoolConfig) *ConnPool {
	return NewConnPoolWithDialer(func(ctx context.Context) (net.Conn, error) {
		ctx = core.Context(ctx)
		dialer := net.Dialer{Timeout: conf.DialTimeout}
		return dialer.DialContext(ctx, network, address)
	}, conf)
}

func NewConnPoolWithDialer(dial ConnDialer, conf ConnPoolConfig) *ConnPool {
	defaults := DefaultConnPoolConfig()
	if conf.MaxIdle < 0 {
		conf.MaxIdle = 0
	}
	if conf.MaxActive < 0 {
		conf.MaxActive = 0
	}
	if conf.MaxIdle == 0 {
		conf.MaxIdle = defaults.MaxIdle
	}
	if conf.MaxActive == 0 {
		conf.MaxActive = defaults.MaxActive
	}
	if conf.IdleTimeout == 0 {
		conf.IdleTimeout = defaults.IdleTimeout
	}
	if conf.MaxLifetime == 0 {
		conf.MaxLifetime = defaults.MaxLifetime
	}
	if conf.DialTimeout == 0 {
		conf.DialTimeout = defaults.DialTimeout
	}
	if conf.WaitInterval <= 0 {
		conf.WaitInterval = defaults.WaitInterval
	}
	if dial == nil {
		dial = func(context.Context) (net.Conn, error) { return nil, errors.New("rpc connection pool dialer is nil") }
	}
	return &ConnPool{dial: dial, conf: conf}
}

func (p *ConnPool) Get(ctx context.Context) (*PooledConn, error) {
	if p == nil {
		return nil, ErrConnPoolClosed
	}
	ctx = core.Context(ctx)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, ok, err := p.tryGetIdleOrReserve()
		if err != nil {
			return nil, err
		}
		if conn != nil {
			return conn, nil
		}
		if ok {
			return p.dialReserved(ctx)
		}
		p.waits.Add(1)
		timer := time.NewTimer(p.conf.WaitInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *ConnPool) tryGetIdleOrReserve() (*PooledConn, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, false, ErrConnPoolClosed
	}
	now := time.Now()
	for len(p.idle) > 0 {
		last := len(p.idle) - 1
		conn := p.idle[last]
		p.idle[last] = nil
		p.idle = p.idle[:last]
		if p.expiredLocked(conn, now) {
			p.active--
			p.closedN.Add(1)
			_ = conn.Conn.Close() // best-effort cleanup of expired connection
			continue
		}
		conn.released.Store(false)
		p.reused.Add(1)
		return conn, false, nil
	}
	if p.conf.MaxActive > 0 && p.active >= p.conf.MaxActive {
		return nil, false, nil
	}
	p.active++
	return nil, true, nil
}

func (p *ConnPool) dialReserved(ctx context.Context) (*PooledConn, error) {
	conn, err := p.dial(ctx)
	if err != nil {
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
		return nil, fmt.Errorf("dial rpc connection: %w", err)
	}
	now := time.Now()
	pc := &PooledConn{Conn: conn, pool: p, created: now, lastUsed: now}
	p.created.Add(1)
	return pc, nil
}

func (p *ConnPool) Put(conn *PooledConn) error {
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (p *ConnPool) Discard(conn *PooledConn) error {
	if conn == nil || !conn.released.CompareAndSwap(false, true) {
		return nil
	}
	return p.release(conn, true)
}

func (p *ConnPool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.active -= len(idle)
	p.closedN.Add(int64(len(idle)))
	p.mu.Unlock()

	var err error
	for _, conn := range idle {
		if closeErr := conn.Conn.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}
	return err
}

func (p *ConnPool) Snapshot() ConnPoolStats {
	if p == nil {
		return ConnPoolStats{}
	}
	p.mu.Lock()
	idle := len(p.idle)
	active := p.active
	p.mu.Unlock()
	return ConnPoolStats{Idle: idle, Active: active, Created: p.created.Load(), Reused: p.reused.Load(), Closed: p.closedN.Load(), Waits: p.waits.Load()}
}

func (p *ConnPool) release(conn *PooledConn, discard bool) error {
	if p == nil {
		return conn.Conn.Close()
	}
	now := time.Now()
	conn.lastUsed = now
	p.mu.Lock()
	shouldClose := discard || p.closed || p.expiredLocked(conn, now) || len(p.idle) >= p.conf.MaxIdle
	if shouldClose {
		p.active--
		p.closedN.Add(1)
		p.mu.Unlock()
		return conn.Conn.Close()
	}
	p.idle = append(p.idle, conn)
	p.mu.Unlock()
	return nil
}

func (p *ConnPool) expiredLocked(conn *PooledConn, now time.Time) bool {
	if conn == nil {
		return true
	}
	if p.conf.IdleTimeout > 0 && now.Sub(conn.lastUsed) > p.conf.IdleTimeout {
		return true
	}
	return p.conf.MaxLifetime > 0 && now.Sub(conn.created) > p.conf.MaxLifetime
}

func (c *PooledConn) Close() error {
	if c == nil || !c.released.CompareAndSwap(false, true) {
		return nil
	}
	return c.pool.release(c, false)
}

func (c *PooledConn) Discard() error {
	if c == nil || !c.released.CompareAndSwap(false, true) {
		return nil
	}
	return c.pool.release(c, true)
}

func (c *PooledConn) Transport(opts ...FramedTransportOption) *FramedTransport {
	if c == nil {
		return nil
	}
	return NewFramedTransport(c, opts...)
}
