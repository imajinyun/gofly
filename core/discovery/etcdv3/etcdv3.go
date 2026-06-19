// Package etcdv3 implements a production-grade discovery.Registry backed by the
// official etcd v3 client. Registration uses native etcd leases with automatic
// keep-alive renewal, and Watch streams live updates over a long-lived etcd
// watch channel instead of polling.
package etcdv3

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/gofly/gofly/core/discovery"
)

// Config configures the etcd v3 registry.
type Config struct {
	// Endpoints is the list of etcd endpoints (host:port).
	Endpoints []string
	// Prefix is the key namespace for service instances.
	Prefix string
	// DialTimeout bounds the initial connection.
	DialTimeout time.Duration
	// TTL is the default lease TTL applied to registrations.
	TTL time.Duration
	// Username/Password authenticate against etcd when set.
	Username string
	Password string
}

func (c Config) withDefaults() Config {
	if c.Prefix == "" {
		c.Prefix = "/gofly/services"
	}
	c.Prefix = strings.TrimRight(c.Prefix, "/")
	if c.DialTimeout <= 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.TTL <= 0 {
		c.TTL = 15 * time.Second
	}
	return c
}

// Registry is a discovery.Registry backed by etcd v3.
type Registry struct {
	client *clientv3.Client
	cfg    Config

	mu     sync.Mutex
	leases []*lease
	closed bool
}

var _ discovery.Registry = (*Registry)(nil)

// New connects to etcd and returns a Registry. The caller owns Close.
func New(cfg Config) (*Registry, error) {
	cfg = cfg.withDefaults()
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("etcdv3: at least one endpoint is required")
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("etcdv3: connect: %w", err)
	}
	return &Registry{client: client, cfg: cfg}, nil
}

// NewWithClient wraps an existing etcd client (useful for tests / shared pools).
func NewWithClient(client *clientv3.Client, cfg Config) (*Registry, error) {
	if client == nil {
		return nil, fmt.Errorf("etcdv3: client is nil")
	}
	return &Registry{client: client, cfg: cfg.withDefaults()}, nil
}

func (r *Registry) key(service, id string) string {
	return r.cfg.Prefix + "/" + service + "/" + id
}

func (r *Registry) servicePrefix(service string) string {
	return r.cfg.Prefix + "/" + service + "/"
}

// Register grants a lease, writes the instance under it and starts a keep-alive
// loop so the registration is renewed for as long as the lease lives.
func (r *Registry) Register(ctx context.Context, instance discovery.Instance, opts ...discovery.RegisterOption) (discovery.Lease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	instance = normalize(instance)
	if instance.Service == "" || instance.Endpoint == "" {
		return nil, fmt.Errorf("etcdv3: instance requires service and endpoint")
	}
	// discovery.RegisterOption mutates an unexported struct that cannot be read
	// from outside core/discovery, so the lease TTL comes from Config.TTL.
	_ = opts
	ttl := r.cfg.TTL

	grant, err := r.client.Grant(ctx, int64(ttl.Seconds())+1)
	if err != nil {
		return nil, fmt.Errorf("etcdv3: grant lease: %w", err)
	}
	data, err := json.Marshal(instance)
	if err != nil {
		return nil, fmt.Errorf("etcdv3: marshal instance: %w", err)
	}
	if _, err := r.client.Put(ctx, r.key(instance.Service, instance.ID), string(data), clientv3.WithLease(grant.ID)); err != nil {
		return nil, fmt.Errorf("etcdv3: put instance: %w", err)
	}

	keepCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	ch, err := r.client.KeepAlive(keepCtx, grant.ID)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("etcdv3: keepalive: %w", err)
	}
	l := &lease{
		registry: r,
		instance: instance,
		id:       grant.ID,
		ttl:      ttl,
		cancel:   cancel,
		expires:  time.Now().Add(ttl),
	}
	go l.consume(ch)
	r.mu.Lock()
	r.leases = append(r.leases, l)
	r.mu.Unlock()
	return l, nil
}

// Deregister deletes the instance key. The associated lease keep-alive should
// be stopped via Lease.Close; this also removes the key explicitly.
func (r *Registry) Deregister(ctx context.Context, instance discovery.Instance) error {
	if ctx == nil {
		ctx = context.Background()
	}
	instance = normalize(instance)
	if instance.Service == "" || instance.ID == "" {
		return nil
	}
	_, err := r.client.Delete(ctx, r.key(instance.Service, instance.ID))
	return err
}

// Resolve returns the current healthy instances of a service.
func (r *Registry) Resolve(ctx context.Context, service string, opts ...discovery.ResolveOption) ([]discovery.Instance, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, fmt.Errorf("etcdv3: service name is required")
	}
	resp, err := r.client.Get(ctx, r.servicePrefix(service), clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("etcdv3: get instances: %w", err)
	}
	memory := discovery.NewMemoryRegistry()
	for _, kv := range resp.Kvs {
		instance, err := decode(service, kv.Value)
		if err != nil {
			continue
		}
		if instance.Endpoint != "" {
			if _, err := memory.Register(ctx, instance); err != nil {
				return nil, err
			}
		}
	}
	return memory.Resolve(ctx, service, opts...)
}

// Watch streams snapshot + incremental events over a long-lived etcd watch.
func (r *Registry) Watch(ctx context.Context, service string, opts ...discovery.ResolveOption) (<-chan discovery.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, fmt.Errorf("etcdv3: service name is required")
	}
	out := make(chan discovery.Event, 1)
	instances, err := r.Resolve(ctx, service, opts...)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	out <- discovery.Event{Type: discovery.EventSnapshot, Service: service, At: time.Now(), Instances: instances}

	watchCh := r.client.Watch(context.WithoutCancel(ctx), r.servicePrefix(service), clientv3.WithPrefix())
	go r.forward(ctx, service, opts, watchCh, out)
	return out, nil
}

func (r *Registry) forward(ctx context.Context, service string, opts []discovery.ResolveOption, watchCh clientv3.WatchChan, out chan<- discovery.Event) {
	defer close(out)
	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-watchCh:
			if !ok {
				return
			}
			if resp.Canceled {
				return
			}
			for _, ev := range resp.Events {
				event := r.toEvent(ctx, service, opts, ev)
				select {
				case out <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (r *Registry) toEvent(ctx context.Context, service string, opts []discovery.ResolveOption, ev *clientv3.Event) discovery.Event {
	instances, _ := r.Resolve(ctx, service, opts...)
	event := discovery.Event{Service: service, At: time.Now(), Instances: instances}
	switch ev.Type {
	case clientv3.EventTypeDelete:
		event.Type = discovery.EventDeregister
		if inst, err := decode(service, nil); err == nil {
			event.Instance = inst
		}
	default:
		event.Type = discovery.EventRegistered
		if inst, err := decode(service, ev.Kv.Value); err == nil {
			event.Instance = inst
		}
	}
	return event
}

// Close stops all keep-alive loops and closes the etcd client.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	leases := r.leases
	r.leases = nil
	r.mu.Unlock()
	var firstErr error
	for _, l := range leases {
		if err := l.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := r.client.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func normalize(instance discovery.Instance) discovery.Instance {
	instance.Service = strings.TrimSpace(instance.Service)
	instance.Endpoint = strings.TrimRight(strings.TrimSpace(instance.Endpoint), "/")
	instance.ID = strings.TrimSpace(instance.ID)
	if instance.ID == "" {
		instance.ID = instance.Endpoint
	}
	instance.Version = strings.TrimSpace(instance.Version)
	instance.Zone = strings.TrimSpace(instance.Zone)
	return instance
}

func decode(service string, value []byte) (discovery.Instance, error) {
	if len(value) == 0 {
		return discovery.Instance{Service: service}, nil
	}
	var instance discovery.Instance
	if err := json.Unmarshal(value, &instance); err != nil {
		return discovery.Instance{}, err
	}
	if instance.Service == "" {
		instance.Service = service
	}
	return normalize(instance), nil
}
