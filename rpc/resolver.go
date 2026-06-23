// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gofly/gofly/core/discovery"
)

type Resolver interface {
	Resolve(ctx context.Context) ([]string, error)
}

type ServiceInstance struct {
	Endpoint string            `json:"endpoint"`
	Weight   int               `json:"weight,omitempty"`
	Version  string            `json:"version,omitempty"`
	Zone     string            `json:"zone,omitempty"`
	Status   string            `json:"status,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type InstanceResolver interface {
	ResolveInstances(ctx context.Context) ([]ServiceInstance, error)
}

type Registrar interface {
	RegisterService(ctx context.Context, service string, endpoint string) error
	DeregisterService(ctx context.Context, service string, endpoint string) error
}

type ResolverFunc func(context.Context) ([]string, error)

func (f ResolverFunc) Resolve(ctx context.Context) ([]string, error) { return f(ctx) }

type WatchResolver interface {
	Resolver
	Watch(ctx context.Context) (<-chan []string, error)
}

type CachedResolver struct {
	source      WatchResolver
	mu          sync.RWMutex
	endpoints   []string
	removed     []string
	err         error
	watchers    map[chan []string]struct{}
	updates     int64
	lastUpdated time.Time
}

type ResolverSnapshot struct {
	Endpoints   []string  `json:"endpoints"`
	Removed     []string  `json:"removed,omitempty"`
	Error       string    `json:"error,omitempty"`
	Watchers    int       `json:"watchers"`
	Updates     int64     `json:"updates"`
	LastUpdated time.Time `json:"lastUpdated,omitempty"`
}

func NewCachedResolver(ctx context.Context, source WatchResolver) (*CachedResolver, error) {
	if source == nil {
		return nil, errors.New("watch resolver is required")
	}
	r := &CachedResolver{source: source, err: errors.New("no rpc endpoints resolved"), watchers: make(map[chan []string]struct{})}
	if endpoints, err := source.Resolve(ctx); err == nil {
		r.set(endpoints)
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	} else {
		r.err = err
	}
	updates, err := source.Watch(ctx)
	if err != nil {
		return nil, err
	}
	go r.watch(ctx, updates)
	return r, nil
}

func (r *CachedResolver) Resolve(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.endpoints) == 0 {
		return nil, r.err
	}
	return append([]string(nil), r.endpoints...), nil
}

func (r *CachedResolver) Snapshot() ResolverSnapshot {
	if r == nil {
		return ResolverSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := ResolverSnapshot{
		Endpoints:   append([]string(nil), r.endpoints...),
		Removed:     append([]string(nil), r.removed...),
		Watchers:    len(r.watchers),
		Updates:     r.updates,
		LastUpdated: r.lastUpdated,
	}
	if r.err != nil {
		snapshot.Error = r.err.Error()
	}
	return snapshot
}

func (r *CachedResolver) Watch(ctx context.Context) (<-chan []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan []string, 1)
	r.mu.Lock()
	if r.watchers == nil {
		r.watchers = make(map[chan []string]struct{})
	}
	r.watchers[ch] = struct{}{}
	initial := append([]string(nil), r.endpoints...)
	r.mu.Unlock()
	ch <- initial
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		delete(r.watchers, ch)
		close(ch)
		r.mu.Unlock()
	}()
	return ch, nil
}

func (r *CachedResolver) watch(ctx context.Context, updates <-chan []string) {
	for {
		select {
		case <-ctx.Done():
			return
		case endpoints, ok := <-updates:
			if !ok {
				return
			}
			r.set(endpoints)
		}
	}
}

func (r *CachedResolver) set(endpoints []string) {
	endpoints = normalizeEndpoints(endpoints)
	r.mu.Lock()
	r.removed = removedEndpoints(r.endpoints, endpoints)
	r.endpoints = endpoints
	r.updates++
	r.lastUpdated = time.Now()
	if len(endpoints) == 0 {
		r.err = errors.New("no rpc endpoints resolved")
	} else {
		r.err = nil
	}
	for watcher := range r.watchers {
		select {
		case watcher <- append([]string(nil), endpoints...):
		default:
			select {
			case <-watcher:
			default:
			}
			select {
			case watcher <- append([]string(nil), endpoints...):
			default:
			}
		}
	}
	r.mu.Unlock()
}

type StaticResolver struct {
	Endpoints []string
}

func NewStaticResolver(endpoints ...string) StaticResolver {
	return StaticResolver{Endpoints: endpoints}
}

func (r StaticResolver) Resolve(ctx context.Context) ([]string, error) {
	endpoints := normalizeEndpoints(r.Endpoints)
	if len(endpoints) == 0 {
		return nil, errors.New("no rpc endpoints resolved")
	}
	return endpoints, nil
}

type Registry struct {
	mu        sync.RWMutex
	discovery *discovery.MemoryRegistry
}

type RegisterOption func(*registerOptions)

type registerOptions struct {
	ttl time.Duration
}

func NewRegistry() *Registry {
	return &Registry{
		discovery: discovery.NewMemoryRegistry(),
	}
}

func WithRegisterTTL(ttl time.Duration) RegisterOption {
	return func(o *registerOptions) {
		if ttl > 0 {
			o.ttl = ttl
		}
	}
}

func (r *Registry) Register(service string, endpoint string) {
	_ = r.RegisterService(context.Background(), service, endpoint)
}

func (r *Registry) RegisterService(ctx context.Context, service string, endpoint string) error {
	return r.RegisterServiceWithOptions(ctx, service, endpoint)
}

func (r *Registry) RegisterServiceWithOptions(ctx context.Context, service string, endpoint string, opts ...RegisterOption) error {
	return r.RegisterInstance(ctx, service, ServiceInstance{Endpoint: endpoint}, opts...)
}

func (r *Registry) RegisterInstance(ctx context.Context, service string, instance ServiceInstance, opts ...RegisterOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var options registerOptions
	for _, opt := range opts {
		opt(&options)
	}
	service = strings.TrimSpace(service)
	endpoint := strings.TrimRight(strings.TrimSpace(instance.Endpoint), "/")
	if service == "" || endpoint == "" {
		return nil
	}
	discoveryOptions := make([]discovery.RegisterOption, 0, 1)
	if options.ttl > 0 {
		discoveryOptions = append(discoveryOptions, discovery.WithTTL(options.ttl))
	}
	_, err := r.discoveryRegistry().Register(ctx, discoveryInstance(service, instance, endpoint), discoveryOptions...)
	if err != nil {
		return err
	}
	return nil
}

func (r *Registry) Deregister(service string, endpoint string) {
	_ = r.DeregisterService(context.Background(), service, endpoint)
}

func (r *Registry) DeregisterService(ctx context.Context, service string, endpoint string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service = strings.TrimSpace(service)
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if service == "" || endpoint == "" {
		return nil
	}
	if err := r.discoveryRegistry().Deregister(ctx, discovery.Instance{Service: service, Endpoint: endpoint}); err != nil {
		return err
	}
	return nil
}

func (r *Registry) ResolveService(ctx context.Context, service string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instances, err := r.discoveryRegistry().Resolve(ctx, strings.TrimSpace(service))
	if err != nil {
		return nil, err
	}
	return endpointsFromDiscovery(instances), nil
}

func (r *Registry) ResolveInstances(ctx context.Context, service string) ([]ServiceInstance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instances, err := r.discoveryRegistry().Resolve(ctx, strings.TrimSpace(service))
	if err != nil {
		return nil, err
	}
	return serviceInstancesFromDiscovery(instances), nil
}

func (r *Registry) WatchService(ctx context.Context, service string) (<-chan []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service = strings.TrimSpace(service)
	events, err := r.discoveryRegistry().Watch(ctx, service)
	if err != nil {
		return nil, err
	}
	out := make(chan []string, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				endpoints := endpointsFromDiscovery(event.Instances)
				select {
				case out <- endpoints:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (r *Registry) WatchEvents(ctx context.Context, service string) (<-chan discovery.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.discoveryRegistry().Watch(ctx, strings.TrimSpace(service))
}

func (r *Registry) Discovery() *discovery.MemoryRegistry {
	return r.discoveryRegistry()
}

func (r *Registry) discoveryRegistry() *discovery.MemoryRegistry {
	if r == nil {
		return discovery.NewMemoryRegistry()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.discovery == nil {
		r.discovery = discovery.NewMemoryRegistry()
	}
	return r.discovery
}

func (r *Registry) Resolver(service string) Resolver {
	return registryResolver{registry: r, service: service}
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	return out
}

type registryResolver struct {
	registry *Registry
	service  string
}

func (r registryResolver) Resolve(ctx context.Context) ([]string, error) {
	return r.registry.ResolveService(ctx, r.service)
}

func (r registryResolver) ResolveInstances(ctx context.Context) ([]ServiceInstance, error) {
	return r.registry.ResolveInstances(ctx, r.service)
}

func (r registryResolver) Watch(ctx context.Context) (<-chan []string, error) {
	return r.registry.WatchService(ctx, r.service)
}

func (r registryResolver) WatchEvents(ctx context.Context) (<-chan discovery.Event, error) {
	return r.registry.WatchEvents(ctx, r.service)
}

type Balancer interface {
	Pick(ctx context.Context, endpoints []string) (string, error)
}
