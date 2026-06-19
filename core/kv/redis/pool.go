// Package redis provides a minimal RESP2 Redis client with connection pooling.
package redis

import (
	"bufio"
	"context"
	"net"
	"sync"
	"time"
)

// conn is a single pooled connection to a Redis server.
type conn struct {
	netConn   net.Conn
	reader    *bufio.Reader
	writer    *bufio.Writer
	createdAt time.Time
	usedAt    time.Time
}

func (c *conn) close() error {
	if c == nil || c.netConn == nil {
		return nil
	}
	return c.netConn.Close()
}

// do writes a command and reads a single reply, applying read/write deadlines.
func (c *conn) do(timeout time.Duration, args ...string) (reply, error) {
	if timeout > 0 {
		_ = c.netConn.SetDeadline(time.Now().Add(timeout))
	} else {
		_ = c.netConn.SetDeadline(time.Time{})
	}
	if err := writeCommand(c.writer, args...); err != nil {
		return reply{}, err
	}
	return readReply(c.reader)
}

// pool is a simple bounded connection pool with idle reuse.
type pool struct {
	mu       sync.Mutex
	idle     []*conn
	active   int
	closed   bool
	cond     *sync.Cond
	dial     func() (*conn, error)
	maxConns int
	maxIdle  int
	idleTTL  time.Duration
	maxLife  time.Duration
}

func newPool(dial func() (*conn, error), maxConns, maxIdle int, idleTTL, maxLife time.Duration) *pool {
	p := &pool{
		dial:     dial,
		maxConns: maxConns,
		maxIdle:  maxIdle,
		idleTTL:  idleTTL,
		maxLife:  maxLife,
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *pool) get(ctx context.Context) (*conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	for {
		if p.closed {
			p.mu.Unlock()
			return nil, ErrClosed
		}
		now := time.Now()
		for len(p.idle) > 0 {
			c := p.idle[len(p.idle)-1]
			p.idle = p.idle[:len(p.idle)-1]
			if p.stale(c, now) {
				p.active--
				_ = c.close()
				continue
			}
			p.active++ // borrowed conns are counted as active while in use
			p.mu.Unlock()
			return c, nil
		}
		if p.maxConns <= 0 || p.active < p.maxConns {
			p.active++
			p.mu.Unlock()
			c, err := p.dial()
			if err != nil {
				p.mu.Lock()
				p.active--
				p.cond.Signal()
				p.mu.Unlock()
				return nil, err
			}
			return c, nil
		}
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return nil, err
		}
		stopWake := context.AfterFunc(ctx, func() {
			p.mu.Lock()
			p.cond.Broadcast()
			p.mu.Unlock()
		})
		p.cond.Wait()
		stopWake()
	}
}

// stale reports whether an idle connection should be discarded.
func (p *pool) stale(c *conn, now time.Time) bool {
	if p.maxLife > 0 && now.Sub(c.createdAt) >= p.maxLife {
		return true
	}
	if p.idleTTL > 0 && now.Sub(c.usedAt) >= p.idleTTL {
		return true
	}
	return false
}

func (p *pool) put(c *conn, broken bool) {
	if c == nil {
		return
	}
	p.mu.Lock()
	p.active--
	if broken || p.closed {
		p.mu.Unlock()
		_ = c.close()
		p.signal()
		return
	}
	c.usedAt = time.Now()
	if p.maxIdle > 0 && len(p.idle) >= p.maxIdle {
		p.mu.Unlock()
		_ = c.close()
		p.signal()
		return
	}
	p.idle = append(p.idle, c)
	p.mu.Unlock()
	p.signal()
}

func (p *pool) signal() {
	p.mu.Lock()
	p.cond.Signal()
	p.mu.Unlock()
}

func (p *pool) close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.cond.Broadcast()
	p.mu.Unlock()
	var err error
	for _, c := range idle {
		if cerr := c.close(); cerr != nil {
			err = cerr
		}
	}
	return err
}

func (p *pool) snapshot() (active, idle int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active, len(p.idle)
}
