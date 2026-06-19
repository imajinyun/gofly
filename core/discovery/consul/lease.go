// Package consul implements a discovery.Registry backed by the official
// HashiCorp Consul client.
package consul

import (
	"context"
	"sync"
	"time"

	consulapi "github.com/hashicorp/consul/api"

	"github.com/gofly/gofly/core/discovery"
)

// lease keeps a Consul TTL health check alive and satisfies discovery.Lease.
type lease struct {
	registry *Registry
	instance discovery.Instance
	checkID  string
	ttl      time.Duration
	cancel   context.CancelFunc

	mu      sync.Mutex
	expires time.Time
	closed  bool
}

var _ discovery.Lease = (*lease)(nil)

// heartbeat refreshes the TTL check at half the TTL interval until cancelled.
func (l *lease) heartbeat(ctx context.Context) {
	interval := l.ttl / 2
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.registry.client.Agent().UpdateTTL(l.checkID, "alive", consulapi.HealthPassing); err == nil {
				l.mu.Lock()
				l.expires = time.Now().Add(l.ttl)
				l.mu.Unlock()
			}
		}
	}
}

// KeepAlive issues a single explicit TTL refresh.
func (l *lease) KeepAlive(ctx context.Context) error {
	if l == nil || l.registry == nil {
		return nil
	}
	err := l.registry.client.Agent().UpdateTTL(l.checkID, "alive", consulapi.HealthPassing)
	if err == nil {
		l.mu.Lock()
		l.expires = time.Now().Add(l.ttl)
		l.mu.Unlock()
	}
	return err
}

// Close stops the heartbeat and deregisters the instance.
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
	return l.registry.Deregister(ctx, l.instance)
}

// Instance returns the registered instance.
func (l *lease) Instance() discovery.Instance {
	if l == nil {
		return discovery.Instance{}
	}
	return l.instance
}

// ExpiresAt returns the current TTL expiry estimate.
func (l *lease) ExpiresAt() time.Time {
	if l == nil {
		return time.Time{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expires
}
