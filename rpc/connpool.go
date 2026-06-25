// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/imajinyun/gofly/core"
)

const (
	ConnPoolModePool  = "pool"
	ConnPoolModeLong  = "long"
	ConnPoolModeShort = "short"
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
	Mode         string
	OnClose      func(endpoint string, reason string, stats ConnPoolStats)
}

// DefaultConnPoolConfig returns sensible defaults.
func DefaultConnPoolConfig() ConnPoolConfig {
	return ConnPoolConfig{MaxIdle: 16, MaxActive: 64, IdleTimeout: time.Minute, MaxLifetime: 30 * time.Minute, DialTimeout: 3 * time.Second, WaitInterval: 5 * time.Millisecond, Mode: ConnPoolModePool}
}

// ConnPoolStats reports the current pool state.
type ConnPoolStats struct {
	Endpoint         string        `json:"endpoint,omitempty"`
	Mode             string        `json:"mode,omitempty"`
	Idle             int           `json:"idle"`
	Active           int           `json:"active"`
	Created          int64         `json:"created"`
	Reused           int64         `json:"reused"`
	Closed           int64         `json:"closed"`
	Waits            int64         `json:"waits"`
	IdleTimeout      time.Duration `json:"idleTimeout,omitempty"`
	MaxLifetime      time.Duration `json:"maxLifetime,omitempty"`
	LastClosedReason string        `json:"lastClosedReason,omitempty"`
}

type ConnDialer func(context.Context) (net.Conn, error)
type EndpointConnDialer func(context.Context, string) (net.Conn, error)

type ConnPool struct {
	mu               sync.Mutex
	dial             ConnDialer
	conf             ConnPoolConfig
	endpoint         string
	lastClosedReason string
	idle             []*PooledConn
	active           int
	closed           bool

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
	conf.Mode = normalizeConnPoolMode(conf.Mode)
	if dial == nil {
		dial = func(context.Context) (net.Conn, error) { return nil, errors.New("rpc connection pool dialer is nil") }
	}
	return &ConnPool{dial: dial, conf: conf}
}

func normalizeConnPoolMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ConnPoolModeShort:
		return ConnPoolModeShort
	case ConnPoolModeLong:
		return ConnPoolModeLong
	default:
		return ConnPoolModePool
	}
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
	return p.closeWithReason("closed")
}

func (p *ConnPool) closeWithReason(reason string) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.lastClosedReason = reason
	idle := p.idle
	p.idle = nil
	p.active -= len(idle)
	p.closedN.Add(int64(len(idle)))
	stats := p.snapshotLocked()
	p.mu.Unlock()

	var err error
	for _, conn := range idle {
		if closeErr := conn.Conn.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}
	if p.conf.OnClose != nil {
		p.conf.OnClose(p.endpoint, reason, stats)
	}
	return err
}

func (p *ConnPool) Snapshot() ConnPoolStats {
	if p == nil {
		return ConnPoolStats{}
	}
	p.mu.Lock()
	stats := p.snapshotLocked()
	p.mu.Unlock()
	return stats
}

func (p *ConnPool) snapshotLocked() ConnPoolStats {
	return ConnPoolStats{
		Endpoint:         p.endpoint,
		Mode:             p.conf.Mode,
		Idle:             len(p.idle),
		Active:           p.active,
		Created:          p.created.Load(),
		Reused:           p.reused.Load(),
		Closed:           p.closedN.Load(),
		Waits:            p.waits.Load(),
		IdleTimeout:      p.conf.IdleTimeout,
		MaxLifetime:      p.conf.MaxLifetime,
		LastClosedReason: p.lastClosedReason,
	}
}

func (p *ConnPool) release(conn *PooledConn, discard bool) error {
	if p == nil {
		return conn.Conn.Close()
	}
	now := time.Now()
	conn.lastUsed = now
	p.mu.Lock()
	shouldClose := discard || p.closed || p.conf.Mode == ConnPoolModeShort || p.expiredLocked(conn, now) || len(p.idle) >= p.conf.MaxIdle
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

type ConnPoolManager struct {
	mu     sync.Mutex
	dial   EndpointConnDialer
	conf   ConnPoolConfig
	pools  map[string]*ConnPool
	closed bool
}

type ConnPoolManagerSnapshot struct {
	Mode      string          `json:"mode,omitempty"`
	Endpoints []ConnPoolStats `json:"endpoints,omitempty"`
	Closed    bool            `json:"closed"`
}

func NewConnPoolManager(dial EndpointConnDialer, conf ConnPoolConfig) *ConnPoolManager {
	if dial == nil {
		dial = func(context.Context, string) (net.Conn, error) {
			return nil, errors.New("rpc connection pool manager dialer is nil")
		}
	}
	conf.Mode = normalizeConnPoolMode(conf.Mode)
	if conf.Mode == ConnPoolModeLong {
		if conf.MaxIdle == 0 {
			conf.MaxIdle = 1
		}
		if conf.MaxActive == 0 {
			conf.MaxActive = 1
		}
	}
	if conf.Mode == ConnPoolModeShort {
		conf.MaxIdle = 0
	}
	return &ConnPoolManager{dial: dial, conf: conf, pools: make(map[string]*ConnPool)}
}

func (m *ConnPoolManager) Get(ctx context.Context, endpoint string) (*PooledConn, error) {
	if m == nil {
		return nil, ErrConnPoolClosed
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return nil, errors.New("rpc connection pool endpoint is required")
	}
	pool, err := m.pool(endpoint)
	if err != nil {
		return nil, err
	}
	return pool.Get(ctx)
}

func (m *ConnPoolManager) RemoveEndpoint(endpoint string) error {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if m == nil || endpoint == "" {
		return nil
	}
	m.mu.Lock()
	pool := m.pools[endpoint]
	delete(m.pools, endpoint)
	m.mu.Unlock()
	if pool == nil {
		return nil
	}
	return pool.closeWithReason("endpoint_removed")
}

func (m *ConnPoolManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	pools := m.pools
	m.pools = nil
	m.mu.Unlock()
	var err error
	for _, pool := range pools {
		err = errors.Join(err, pool.closeWithReason("manager_closed"))
	}
	return err
}

func (m *ConnPoolManager) Snapshot() ConnPoolManagerSnapshot {
	if m == nil {
		return ConnPoolManagerSnapshot{}
	}
	m.mu.Lock()
	pools := make([]*ConnPool, 0, len(m.pools))
	for _, pool := range m.pools {
		pools = append(pools, pool)
	}
	closed := m.closed
	mode := normalizeConnPoolMode(m.conf.Mode)
	m.mu.Unlock()
	endpoints := make([]ConnPoolStats, 0, len(pools))
	for _, pool := range pools {
		endpoints = append(endpoints, pool.Snapshot())
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].Endpoint < endpoints[j].Endpoint })
	return ConnPoolManagerSnapshot{Mode: mode, Endpoints: endpoints, Closed: closed}
}

func (m *ConnPoolManager) pool(endpoint string) (*ConnPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrConnPoolClosed
	}
	if pool := m.pools[endpoint]; pool != nil {
		return pool, nil
	}
	conf := m.conf
	pool := NewConnPoolWithDialer(func(ctx context.Context) (net.Conn, error) {
		return m.dial(ctx, endpoint)
	}, conf)
	pool.endpoint = endpoint
	m.pools[endpoint] = pool
	return pool, nil
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
