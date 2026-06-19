// Package etcdv3 implements a production-grade discovery.Registry backed by the
// official etcd v3 client.
package etcdv3

import (
	"context"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/gofly/gofly/core/discovery"
)

// lease wraps a native etcd lease and its keep-alive loop, satisfying
// discovery.Lease.
type lease struct {
	registry *Registry
	instance discovery.Instance
	id       clientv3.LeaseID
	ttl      time.Duration
	cancel   context.CancelFunc

	mu      sync.Mutex
	expires time.Time
	closed  bool
}

var _ discovery.Lease = (*lease)(nil)

// consume drains the keep-alive channel, refreshing the expiry on each renewal.
// When the channel closes, the lease is considered expired.
func (l *lease) consume(ch <-chan *clientv3.LeaseKeepAliveResponse) {
	for resp := range ch {
		if resp == nil {
			continue
		}
		l.mu.Lock()
		l.expires = time.Now().Add(time.Duration(resp.TTL) * time.Second)
		l.mu.Unlock()
	}
}

// KeepAlive issues a single synchronous renewal. The background loop started at
// registration normally handles renewals; this provides an explicit refresh for
// callers that manage their own cadence.
func (l *lease) KeepAlive(ctx context.Context) error {
	if l == nil || l.registry == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := l.registry.client.KeepAliveOnce(ctx, l.id)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.expires = time.Now().Add(time.Duration(resp.TTL) * time.Second)
	l.mu.Unlock()
	return nil
}

// Close stops the keep-alive loop and revokes the lease, removing the instance.
func (l *lease) Close(ctx context.Context) error {
	if l == nil || l.registry == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := l.registry.client.Revoke(ctx, l.id)
	return err
}

// Instance returns the registered instance.
func (l *lease) Instance() discovery.Instance {
	if l == nil {
		return discovery.Instance{}
	}
	return l.instance
}

// ExpiresAt returns the current lease expiry estimate.
func (l *lease) ExpiresAt() time.Time {
	if l == nil {
		return time.Time{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expires
}
