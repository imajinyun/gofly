// Package consul implements a discovery.Registry backed by the official
// HashiCorp Consul API. Registration uses Consul service entries with a TTL
// health check kept alive in the background; Watch uses blocking queries to
// stream live updates without polling overhead.
package consul

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	consulapi "github.com/hashicorp/consul/api"

	"github.com/gofly/gofly/core/discovery"
)

// Config configures the Consul registry.
type Config struct {
	// Address is the Consul agent address (host:port). Defaults to the
	// consulapi default when empty.
	Address string
	// Token is an optional ACL token.
	Token string
	// TTL is the health-check TTL applied to registrations.
	TTL time.Duration
	// DeregisterAfter removes the service this long after the check goes
	// critical.
	DeregisterAfter time.Duration
}

func (c Config) withDefaults() Config {
	if c.TTL <= 0 {
		c.TTL = 15 * time.Second
	}
	if c.DeregisterAfter <= 0 {
		c.DeregisterAfter = 1 * time.Minute
	}
	return c
}

// Registry is a discovery.Registry backed by Consul.
type Registry struct {
	client *consulapi.Client
	cfg    Config

	mu     sync.Mutex
	leases []*lease
	closed bool
}

var _ discovery.Registry = (*Registry)(nil)

// New creates a Consul-backed registry.
func New(cfg Config) (*Registry, error) {
	cfg = cfg.withDefaults()
	apiCfg := consulapi.DefaultConfig()
	if cfg.Address != "" {
		apiCfg.Address = cfg.Address
	}
	if cfg.Token != "" {
		apiCfg.Token = cfg.Token
	}
	client, err := consulapi.NewClient(apiCfg)
	if err != nil {
		return nil, fmt.Errorf("consul: new client: %w", err)
	}
	return &Registry{client: client, cfg: cfg}, nil
}

// NewWithClient wraps an existing Consul client.
func NewWithClient(client *consulapi.Client, cfg Config) (*Registry, error) {
	if client == nil {
		return nil, fmt.Errorf("consul: client is nil")
	}
	return &Registry{client: client, cfg: cfg.withDefaults()}, nil
}

// Register publishes the instance and starts a TTL check heartbeat.
func (r *Registry) Register(ctx context.Context, instance discovery.Instance, opts ...discovery.RegisterOption) (discovery.Lease, error) {
	_ = opts
	instance = normalize(instance)
	if instance.Service == "" || instance.Endpoint == "" {
		return nil, fmt.Errorf("consul: instance requires service and endpoint")
	}
	host, port, err := splitEndpoint(instance.Endpoint)
	if err != nil {
		return nil, err
	}
	checkID := "service:" + instance.ID
	reg := &consulapi.AgentServiceRegistration{
		ID:      instance.ID,
		Name:    instance.Service,
		Address: host,
		Port:    port,
		Tags:    tagsToSlice(instance),
		Meta:    instance.Metadata,
		Check: &consulapi.AgentServiceCheck{
			CheckID:                        checkID,
			TTL:                            (r.cfg.TTL * 2).String(),
			DeregisterCriticalServiceAfter: r.cfg.DeregisterAfter.String(),
		},
	}
	if err := r.client.Agent().ServiceRegister(reg); err != nil {
		return nil, fmt.Errorf("consul: register: %w", err)
	}
	// Pass the initial check immediately so the instance is healthy at once.
	_ = r.client.Agent().UpdateTTL(checkID, "registered", consulapi.HealthPassing)

	heartbeatCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	l := &lease{
		registry: r,
		instance: instance,
		checkID:  checkID,
		ttl:      r.cfg.TTL,
		cancel:   cancel,
		expires:  time.Now().Add(r.cfg.TTL),
	}
	go l.heartbeat(heartbeatCtx)
	r.mu.Lock()
	r.leases = append(r.leases, l)
	r.mu.Unlock()
	return l, nil
}

// Deregister removes the service instance from Consul.
func (r *Registry) Deregister(ctx context.Context, instance discovery.Instance) error {
	_ = ctx
	instance = normalize(instance)
	if instance.ID == "" {
		return nil
	}
	return r.client.Agent().ServiceDeregister(instance.ID)
}

// Resolve returns the healthy instances of a service.
func (r *Registry) Resolve(ctx context.Context, service string, opts ...discovery.ResolveOption) ([]discovery.Instance, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, fmt.Errorf("consul: service name is required")
	}
	entries, _, err := r.client.Health().Service(service, "", true, (&consulapi.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("consul: health service: %w", err)
	}
	return r.filter(ctx, service, entries, opts)
}

func (r *Registry) filter(ctx context.Context, service string, entries []*consulapi.ServiceEntry, opts []discovery.ResolveOption) ([]discovery.Instance, error) {
	memory := discovery.NewMemoryRegistry()
	for _, entry := range entries {
		instance := fromEntry(service, entry)
		if instance.Endpoint == "" {
			continue
		}
		if _, err := memory.Register(ctx, instance); err != nil {
			return nil, err
		}
	}
	return memory.Resolve(ctx, service, opts...)
}

// Watch streams snapshot + updates using Consul blocking queries.
func (r *Registry) Watch(ctx context.Context, service string, opts ...discovery.ResolveOption) (<-chan discovery.Event, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, fmt.Errorf("consul: service name is required")
	}
	out := make(chan discovery.Event, 1)
	instances, _ := r.Resolve(ctx, service, opts...)
	out <- discovery.Event{Type: discovery.EventSnapshot, Service: service, At: time.Now(), Instances: instances}
	go r.blockingWatch(ctx, service, opts, out)
	return out, nil
}

func (r *Registry) blockingWatch(ctx context.Context, service string, opts []discovery.ResolveOption, out chan<- discovery.Event) {
	defer close(out)
	var lastIndex uint64
	for {
		if ctx.Err() != nil {
			return
		}
		q := (&consulapi.QueryOptions{WaitIndex: lastIndex, WaitTime: 30 * time.Second}).WithContext(ctx)
		entries, meta, err := r.client.Health().Service(service, "", true, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !sleepContext(ctx, time.Second) {
				return
			}
			continue
		}
		if meta.LastIndex == lastIndex {
			continue // no change; blocking query timed out
		}
		lastIndex = meta.LastIndex
		instances, ferr := r.filter(ctx, service, entries, opts)
		if ferr != nil {
			continue
		}
		select {
		case out <- discovery.Event{Type: discovery.EventRegistered, Service: service, At: time.Now(), Instances: instances}:
		case <-ctx.Done():
			return
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Close stops heartbeats and deregisters known instances.
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
	return firstErr
}

func normalize(instance discovery.Instance) discovery.Instance {
	instance.Service = strings.TrimSpace(instance.Service)
	instance.Endpoint = strings.TrimRight(strings.TrimSpace(instance.Endpoint), "/")
	instance.ID = strings.TrimSpace(instance.ID)
	if instance.ID == "" {
		instance.ID = instance.Service + "-" + instance.Endpoint
	}
	instance.Version = strings.TrimSpace(instance.Version)
	instance.Zone = strings.TrimSpace(instance.Zone)
	return instance
}

func splitEndpoint(endpoint string) (string, int, error) {
	host := endpoint
	if i := strings.LastIndex(endpoint, "://"); i >= 0 {
		host = endpoint[i+3:]
	}
	h, p, found := strings.Cut(host, ":")
	if !found {
		return host, 0, nil
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("consul: invalid endpoint port %q: %w", endpoint, err)
	}
	return h, port, nil
}

func tagsToSlice(instance discovery.Instance) []string {
	tags := make([]string, 0, len(instance.Tags)+2)
	if instance.Version != "" {
		tags = append(tags, "version="+instance.Version)
	}
	if instance.Zone != "" {
		tags = append(tags, "zone="+instance.Zone)
	}
	for k, v := range instance.Tags {
		tags = append(tags, k+"="+v)
	}
	return tags
}

func fromEntry(service string, entry *consulapi.ServiceEntry) discovery.Instance {
	svc := entry.Service
	instance := discovery.Instance{
		ID:       svc.ID,
		Service:  service,
		Endpoint: fmt.Sprintf("%s:%d", svc.Address, svc.Port),
		Metadata: svc.Meta,
		Status:   discovery.StatusHealthy,
	}
	for _, tag := range svc.Tags {
		k, v, found := strings.Cut(tag, "=")
		if !found {
			continue
		}
		switch k {
		case "version":
			instance.Version = v
		case "zone":
			instance.Zone = v
		default:
			if instance.Tags == nil {
				instance.Tags = make(map[string]string)
			}
			instance.Tags[k] = v
		}
	}
	return instance
}
