// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"strings"

	"github.com/gofly/gofly/core/discovery"
)

// DiscoveryResolver resolves service endpoints via the discovery package.
type DiscoveryResolver struct {
	resolver discovery.Resolver
	service  string
	options  []discovery.ResolveOption
}

// DiscoveryRegistrar registers service endpoints via the discovery package.
type DiscoveryRegistrar struct {
	registrar discovery.Registrar
	options   []discovery.RegisterOption
}

// NewDiscoveryResolver creates a resolver for the given service.
func NewDiscoveryResolver(resolver discovery.Resolver, service string, opts ...discovery.ResolveOption) *DiscoveryResolver {
	return &DiscoveryResolver{resolver: resolver, service: strings.TrimSpace(service), options: opts}
}

func (r *DiscoveryResolver) Resolve(ctx context.Context) ([]string, error) {
	instances, err := r.ResolveInstances(ctx)
	if err != nil {
		return nil, err
	}
	endpoints := make([]string, 0, len(instances))
	for _, instance := range instances {
		endpoints = append(endpoints, instance.Endpoint)
	}
	return endpoints, nil
}

func (r *DiscoveryResolver) ResolveInstances(ctx context.Context) ([]ServiceInstance, error) {
	if r == nil || r.resolver == nil {
		return nil, discovery.ErrNoInstances
	}
	instances, err := r.resolver.Resolve(ctx, r.service, r.options...)
	if err != nil {
		return nil, err
	}
	return serviceInstancesFromDiscovery(instances), nil
}

func (r *DiscoveryResolver) Watch(ctx context.Context) (<-chan []string, error) {
	if r == nil || r.resolver == nil {
		return nil, discovery.ErrNoInstances
	}
	events, err := r.resolver.Watch(ctx, r.service, r.options...)
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

func NewDiscoveryRegistrar(registrar discovery.Registrar, opts ...discovery.RegisterOption) *DiscoveryRegistrar {
	return &DiscoveryRegistrar{registrar: registrar, options: opts}
}

func (r *DiscoveryRegistrar) RegisterService(ctx context.Context, service string, endpoint string) error {
	if r == nil || r.registrar == nil {
		return nil
	}
	_, err := r.registrar.Register(ctx, discovery.Instance{Service: service, Endpoint: endpoint}, r.options...)
	return err
}

func (r *DiscoveryRegistrar) DeregisterService(ctx context.Context, service string, endpoint string) error {
	if r == nil || r.registrar == nil {
		return nil
	}
	return r.registrar.Deregister(ctx, discovery.Instance{Service: service, Endpoint: endpoint})
}

func serviceInstancesFromDiscovery(instances []discovery.Instance) []ServiceInstance {
	if len(instances) == 0 {
		return nil
	}
	out := make([]ServiceInstance, 0, len(instances))
	for _, instance := range instances {
		if instance.Endpoint == "" {
			continue
		}
		out = append(out, ServiceInstance{
			Endpoint: instance.Endpoint,
			Weight:   instance.Weight,
			Version:  instance.Version,
			Zone:     instance.Zone,
			Status:   instance.Status,
			Tags:     discoveryTags(instance),
			Metadata: cloneTags(instance.Metadata),
		})
	}
	return out
}

func endpointsFromDiscovery(instances []discovery.Instance) []string {
	if len(instances) == 0 {
		return nil
	}
	out := make([]string, 0, len(instances))
	for _, instance := range instances {
		if instance.Endpoint != "" {
			out = append(out, instance.Endpoint)
		}
	}
	return out
}

func discoveryTags(instance discovery.Instance) map[string]string {
	tags := make(map[string]string, len(instance.Tags)+3)
	for key, value := range instance.Tags {
		tags[key] = value
	}
	if instance.Version != "" {
		tags["version"] = instance.Version
	}
	if instance.Zone != "" {
		tags["zone"] = instance.Zone
	}
	if instance.Status != "" {
		tags["status"] = instance.Status
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

func discoveryInstance(service string, instance ServiceInstance, endpoint string) discovery.Instance {
	return discovery.Instance{
		Service:  service,
		Endpoint: endpoint,
		Weight:   instance.Weight,
		Version:  instance.Version,
		Zone:     instance.Zone,
		Status:   instance.Status,
		Tags:     cloneTags(instance.Tags),
		Metadata: cloneTags(instance.Metadata),
	}
}
