// Package lock provides distributed and in-process locking with lease-based
// expiration and optional watchdog refresh.
package lock

import (
	"context"
	"sync"
	"time"
)

// Guard keeps a lease alive in the background by periodically calling
// Locker.Refresh until it is stopped or a refresh fails. It is the typical
// "watchdog" companion for long-running critical sections where the work may
// outlive the lease TTL.
type Guard struct {
	locker   Locker
	ttl      time.Duration
	interval time.Duration

	mu        sync.RWMutex
	lease     Lease
	stopped   bool
	stopCh    chan struct{}
	doneCh    chan struct{}
	errCh     chan error
	stopOnce  sync.Once
	refreshes int64
}

// GuardOption customises a Guard.
type GuardOption func(*Guard)

// WithGuardInterval overrides the refresh interval. By default the guard
// refreshes at one third of the lease TTL, which leaves room for retries before
// the lease would expire.
func WithGuardInterval(interval time.Duration) GuardOption {
	return func(g *Guard) {
		if interval > 0 {
			g.interval = interval
		}
	}
}

// Keepalive starts a background guard that refreshes lease using locker until the
// returned Guard is stopped, the context is cancelled, or a refresh fails. The
// caller must hold a valid lease already (e.g. from Locker.Lock). ttl is the TTL
// passed to each Refresh call; when ttl <= 0 the guard refreshes with the
// locker's default by passing 0 through.
func Keepalive(ctx context.Context, locker Locker, lease Lease, ttl time.Duration, opts ...GuardOption) *Guard {
	if ctx == nil {
		ctx = context.Background()
	}
	g := &Guard{
		locker: locker,
		ttl:    ttl,
		lease:  lease,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		errCh:  make(chan error, 1),
	}
	g.interval = guardInterval(ttl, lease)
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	if g.interval <= 0 {
		g.interval = 10 * time.Second
	}
	go g.run(ctx)
	return g
}

func guardInterval(ttl time.Duration, lease Lease) time.Duration {
	base := ttl
	if base <= 0 {
		base = time.Until(lease.ExpiresAt)
	}
	if base <= 0 {
		return 10 * time.Second
	}
	return base / 3
}

func (g *Guard) run(ctx context.Context) {
	defer close(g.doneCh)
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			g.fail(ctx.Err())
			return
		case <-g.stopCh:
			return
		case <-ticker.C:
			g.mu.RLock()
			current := g.lease
			g.mu.RUnlock()
			next, err := g.locker.Refresh(ctx, current, g.ttl)
			if err != nil {
				g.fail(err)
				return
			}
			g.mu.Lock()
			g.lease = next
			g.refreshes++
			g.mu.Unlock()
		}
	}
}

func (g *Guard) fail(err error) {
	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
	if err != nil {
		select {
		case g.errCh <- err:
		default:
		}
	}
}

// Stop terminates the guard and waits for the background goroutine to exit. It is
// safe to call multiple times.
func (g *Guard) Stop() {
	g.stopOnce.Do(func() {
		close(g.stopCh)
	})
	<-g.doneCh
	g.mu.Lock()
	g.stopped = true
	g.mu.Unlock()
}

// Lease returns the most recent lease the guard has observed (refreshed).
func (g *Guard) Lease() Lease {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.lease
}

// Refreshes returns how many successful refreshes the guard has performed.
func (g *Guard) Refreshes() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.refreshes
}

// Err returns a channel that receives the error that stopped the guard (refresh
// failure or context cancellation). It is buffered with capacity one; nothing is
// sent on a clean Stop.
func (g *Guard) Err() <-chan error { return g.errCh }

// Done returns a channel closed when the guard's background goroutine exits.
func (g *Guard) Done() <-chan struct{} { return g.doneCh }
