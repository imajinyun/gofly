// Package discovery provides service registration and discovery abstractions
// with implementations for consul, etcd, kubernetes and in-memory testing.
package discovery

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	core "github.com/gofly/gofly/core"
)

// ErrNoInstances is returned when a resolver finds no healthy instances.
var ErrNoInstances = errors.New("no service instances resolved")

// MemoryRegistry is an in-memory discovery backend useful for tests and
// single-process deployments.
type MemoryRegistry struct {
	mu       sync.RWMutex
	services map[string]map[string]memoryEntry
	watchers map[string]map[chan Event]watchState
}

type memoryEntry struct {
	instance  Instance
	expiresAt time.Time
	ttl       time.Duration
}

type watchState struct {
	options resolveOptions
}

// memoryLease implements Lease for MemoryRegistry.
type memoryLease struct {
	registry *MemoryRegistry
	instance Instance
	ttl      time.Duration
}

// NewMemoryRegistry creates an empty in-memory registry.
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		services: make(map[string]map[string]memoryEntry),
		watchers: make(map[string]map[chan Event]watchState),
	}
}

func (r *MemoryRegistry) Register(ctx context.Context, instance Instance, opts ...RegisterOption) (Lease, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instance = normalizeInstance(instance)
	if instance.Service == "" || instance.Endpoint == "" {
		return nil, errors.New("service and endpoint are required")
	}
	var options registerOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	entry := memoryEntry{instance: instance, ttl: options.ttl}
	if options.ttl > 0 {
		entry.expiresAt = time.Now().Add(options.ttl)
	}
	r.mu.Lock()
	if r.services == nil {
		r.services = make(map[string]map[string]memoryEntry)
	}
	if r.services[instance.Service] == nil {
		r.services[instance.Service] = make(map[string]memoryEntry)
	}
	r.services[instance.Service][instance.ID] = entry
	r.mu.Unlock()
	r.notify(instance.Service, EventRegistered, instance)
	return &memoryLease{registry: r, instance: instance, ttl: options.ttl}, nil
}

func (r *MemoryRegistry) Deregister(ctx context.Context, instance Instance) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	instance = normalizeInstance(instance)
	if instance.Service == "" || instance.ID == "" {
		return nil
	}
	r.mu.Lock()
	if service := r.services[instance.Service]; service != nil {
		delete(service, instance.ID)
		if len(service) == 0 {
			delete(r.services, instance.Service)
		}
	}
	r.mu.Unlock()
	r.notify(instance.Service, EventDeregister, instance)
	return nil
}

func (r *MemoryRegistry) Resolve(ctx context.Context, service string, opts ...ResolveOption) ([]Instance, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := applyResolveOptions(opts)
	instances, expired := r.resolve(service, options, time.Now())
	for _, instance := range expired {
		r.notify(instance.Service, EventExpired, instance)
	}
	if len(instances) == 0 {
		return nil, ErrNoInstances
	}
	return instances, nil
}

func (r *MemoryRegistry) Watch(ctx context.Context, service string, opts ...ResolveOption) (<-chan Event, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := applyResolveOptions(opts)
	ch := make(chan Event, 1)
	instances, expired := r.resolve(service, options, time.Now())
	r.mu.Lock()
	if r.watchers == nil {
		r.watchers = make(map[string]map[chan Event]watchState)
	}
	if r.watchers[service] == nil {
		r.watchers[service] = make(map[chan Event]watchState)
	}
	r.watchers[service][ch] = watchState{options: options}
	r.mu.Unlock()
	for _, instance := range expired {
		r.notify(instance.Service, EventExpired, instance)
	}
	ch <- Event{Type: EventSnapshot, Service: service, At: time.Now(), Instances: instances}
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		delete(r.watchers[service], ch)
		if len(r.watchers[service]) == 0 {
			delete(r.watchers, service)
		}
		r.mu.Unlock()
		close(ch)
	}()
	return ch, nil
}

func (r *MemoryRegistry) Snapshot() map[string][]Instance {
	if r == nil {
		return nil
	}
	out := make(map[string][]Instance)
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for service := range r.services {
		instances := r.resolveLocked(service, resolveOptions{includeUnhealthy: true}, now, nil)
		if len(instances) > 0 {
			out[service] = instances
		}
	}
	return out
}

func (r *MemoryRegistry) Watchers(service string) int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.watchers[service])
}

func (r *MemoryRegistry) resolve(service string, options resolveOptions, now time.Time) ([]Instance, []Instance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	expired := make([]Instance, 0)
	instances := r.resolveLocked(service, options, now, &expired)
	return instances, expired
}

func (r *MemoryRegistry) resolveLocked(service string, options resolveOptions, now time.Time, expired *[]Instance) []Instance {
	entries := r.services[service]
	if len(entries) == 0 {
		return nil
	}
	out := make([]Instance, 0, len(entries))
	for id, entry := range entries {
		if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
			delete(entries, id)
			if expired != nil {
				*expired = append(*expired, entry.instance)
			}
			continue
		}
		instance := normalizeInstance(entry.instance)
		if instanceMatches(instance, options) {
			out = append(out, instance)
		}
	}
	if len(entries) == 0 {
		delete(r.services, service)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Endpoint < out[j].Endpoint })
	return out
}

func (r *MemoryRegistry) notify(service string, eventType EventType, instance Instance) {
	if r == nil {
		return
	}
	r.mu.RLock()
	watchers := make(map[chan Event]watchState, len(r.watchers[service]))
	for ch, state := range r.watchers[service] {
		watchers[ch] = state
	}
	r.mu.RUnlock()
	if len(watchers) == 0 {
		return
	}
	now := time.Now()
	for ch, state := range watchers {
		instances, _ := r.resolve(service, state.options, now)
		event := Event{Type: eventType, Service: service, At: now, Instance: normalizeInstance(instance), Instances: instances}
		select {
		case ch <- event:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- event:
			default:
			}
		}
	}
}

func (l *memoryLease) KeepAlive(ctx context.Context) error {
	if l == nil || l.registry == nil {
		return nil
	}
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.ttl <= 0 {
		return nil
	}
	l.registry.mu.Lock()
	if service := l.registry.services[l.instance.Service]; service != nil {
		entry, ok := service[l.instance.ID]
		if ok {
			entry.expiresAt = time.Now().Add(l.ttl)
			service[l.instance.ID] = entry
		}
	}
	l.registry.mu.Unlock()
	l.registry.notify(l.instance.Service, EventRegistered, l.instance)
	return nil
}

func (l *memoryLease) Close(ctx context.Context) error {
	if l == nil || l.registry == nil {
		return nil
	}
	return l.registry.Deregister(ctx, l.instance)
}

func (l *memoryLease) Instance() Instance {
	if l == nil {
		return Instance{}
	}
	return normalizeInstance(l.instance)
}

func (l *memoryLease) ExpiresAt() time.Time {
	if l == nil || l.registry == nil {
		return time.Time{}
	}
	l.registry.mu.RLock()
	defer l.registry.mu.RUnlock()
	if service := l.registry.services[l.instance.Service]; service != nil {
		return service[l.instance.ID].expiresAt
	}
	return time.Time{}
}
