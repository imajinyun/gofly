// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/imajinyun/gofly/rpc"
)

type gatewayFailoverResolver struct {
	source rpc.Resolver

	mu          sync.RWMutex
	endpoints   []string
	instances   []rpc.ServiceInstance
	removed     []string
	err         error
	stale       bool
	updates     int64
	fallbacks   int64
	lastUpdated time.Time
}

func newGatewayFailoverResolver(source rpc.Resolver) *gatewayFailoverResolver {
	return &gatewayFailoverResolver{source: source}
}

func (r *gatewayFailoverResolver) Resolve(ctx context.Context) ([]string, error) {
	if r == nil || r.source == nil {
		return nil, errors.New("resolver is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoints, err := r.source.Resolve(ctx)
	endpoints = normalizeTargets(endpoints)
	if err == nil && len(endpoints) > 0 {
		r.set(endpoints, nil, nil)
		return append([]string(nil), endpoints...), nil
	}
	if err == nil {
		err = errors.New("no gateway discovery endpoints resolved")
	}
	if cached := r.fallback(err); len(cached) > 0 {
		return cached, nil
	}
	return nil, err
}

func (r *gatewayFailoverResolver) ResolveInstances(ctx context.Context) ([]rpc.ServiceInstance, error) {
	if r == nil || r.source == nil {
		return nil, errors.New("resolver is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instanceResolver, ok := r.source.(rpc.InstanceResolver)
	if !ok {
		endpoints, err := r.Resolve(ctx)
		if err != nil {
			return nil, err
		}
		return serviceInstancesFromEndpoints(endpoints), nil
	}
	instances, err := instanceResolver.ResolveInstances(ctx)
	instances = normalizeGatewayFailoverInstances(instances)
	if err == nil && len(instances) > 0 {
		endpoints := endpointsFromServiceInstances(instances)
		r.set(endpoints, instances, nil)
		return cloneServiceInstances(instances), nil
	}
	if err == nil {
		err = errors.New("no gateway discovery instances resolved")
	}
	if _, cachedInstances := r.fallbackState(err); len(cachedInstances) > 0 {
		return cachedInstances, nil
	}
	return nil, err
}

func (r *gatewayFailoverResolver) Snapshot() rpc.ResolverSnapshot {
	if r == nil {
		return rpc.ResolverSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := rpc.ResolverSnapshot{
		Endpoints:   append([]string(nil), r.endpoints...),
		Removed:     append([]string(nil), r.removed...),
		Updates:     r.updates,
		Fallbacks:   r.fallbacks,
		Stale:       r.stale,
		LastUpdated: r.lastUpdated,
	}
	if r.err != nil {
		snapshot.Error = r.err.Error()
	}
	return snapshot
}

func (r *gatewayFailoverResolver) set(endpoints []string, instances []rpc.ServiceInstance, err error) {
	endpoints = normalizeTargets(endpoints)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = removedGatewayFailoverEndpoints(r.endpoints, endpoints)
	r.endpoints = append([]string(nil), endpoints...)
	r.instances = cloneServiceInstances(instances)
	r.err = err
	r.stale = false
	r.updates++
	r.lastUpdated = time.Now()
}

func (r *gatewayFailoverResolver) fallback(err error) []string {
	endpoints, _ := r.fallbackState(err)
	return endpoints
}

func (r *gatewayFailoverResolver) fallbackState(err error) ([]string, []rpc.ServiceInstance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
	if len(r.endpoints) == 0 && len(r.instances) > 0 {
		r.endpoints = endpointsFromServiceInstances(r.instances)
	}
	if len(r.endpoints) == 0 {
		return nil, nil
	}
	r.stale = true
	r.fallbacks++
	return append([]string(nil), r.endpoints...), cloneServiceInstances(r.instances)
}

func serviceInstancesFromEndpoints(endpoints []string) []rpc.ServiceInstance {
	endpoints = normalizeTargets(endpoints)
	if len(endpoints) == 0 {
		return nil
	}
	instances := make([]rpc.ServiceInstance, 0, len(endpoints))
	for _, endpoint := range endpoints {
		instances = append(instances, rpc.ServiceInstance{Endpoint: endpoint})
	}
	return instances
}

func endpointsFromServiceInstances(instances []rpc.ServiceInstance) []string {
	if len(instances) == 0 {
		return nil
	}
	endpoints := make([]string, 0, len(instances))
	for _, instance := range instances {
		endpoints = append(endpoints, instance.Endpoint)
	}
	return normalizeTargets(endpoints)
}

func normalizeGatewayFailoverInstances(instances []rpc.ServiceInstance) []rpc.ServiceInstance {
	if len(instances) == 0 {
		return nil
	}
	out := make([]rpc.ServiceInstance, 0, len(instances))
	for _, instance := range instances {
		endpoint := normalizeTargets([]string{instance.Endpoint})
		if len(endpoint) == 0 {
			continue
		}
		instance.Endpoint = endpoint[0]
		instance.Tags = cloneMap(instance.Tags)
		instance.Metadata = cloneMap(instance.Metadata)
		out = append(out, instance)
	}
	return out
}

func removedGatewayFailoverEndpoints(previous []string, current []string) []string {
	if len(previous) == 0 {
		return nil
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, endpoint := range current {
		currentSet[endpoint] = struct{}{}
	}
	removed := make([]string, 0)
	for _, endpoint := range previous {
		if _, ok := currentSet[endpoint]; !ok {
			removed = append(removed, endpoint)
		}
	}
	return removed
}
