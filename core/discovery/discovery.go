// Package discovery provides service registration and discovery abstractions
// for gofly services, including instance metadata, leases, and resolver interfaces.
package discovery

import (
	"context"
	"strings"
	"time"
)

const (
	StatusUnknown   = ""
	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
)

// Instance represents a registered service instance.
type Instance struct {
	ID       string            `json:"id,omitempty"`
	Service  string            `json:"service,omitempty"`
	Endpoint string            `json:"endpoint"`
	Weight   int               `json:"weight,omitempty"`
	Version  string            `json:"version,omitempty"`
	Zone     string            `json:"zone,omitempty"`
	Status   string            `json:"status,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Lease represents a service registration lease with keepalive support.
type Lease interface {
	KeepAlive(context.Context) error
	Close(context.Context) error
	Instance() Instance
	ExpiresAt() time.Time
}

// Registrar registers and deregisters service instances.
type Registrar interface {
	Register(context.Context, Instance, ...RegisterOption) (Lease, error)
	Deregister(context.Context, Instance) error
}

// Resolver resolves service names to a list of instances.
type Resolver interface {
	Resolve(context.Context, string, ...ResolveOption) ([]Instance, error)
	Watch(context.Context, string, ...ResolveOption) (<-chan Event, error)
}

type Registry interface {
	Registrar
	Resolver
}

type EventType string

const (
	EventSnapshot   EventType = "snapshot"
	EventRegistered EventType = "registered"
	EventDeregister EventType = "deregistered"
	EventExpired    EventType = "expired"
)

type Event struct {
	Type      EventType  `json:"type"`
	Service   string     `json:"service,omitempty"`
	At        time.Time  `json:"at"`
	Instance  Instance   `json:"instance,omitempty"`
	Instances []Instance `json:"instances,omitempty"`
}

type RegisterOption func(*registerOptions)

type registerOptions struct {
	ttl time.Duration
}

func WithTTL(ttl time.Duration) RegisterOption {
	return func(o *registerOptions) {
		if ttl > 0 {
			o.ttl = ttl
		}
	}
}

type ResolveOption func(*resolveOptions)

type resolveOptions struct {
	tags             map[string]string
	version          string
	zone             string
	includeUnhealthy bool
}

func WithTag(key, value string) ResolveOption {
	return func(o *resolveOptions) {
		key = strings.TrimSpace(key)
		if key == "" {
			return
		}
		if o.tags == nil {
			o.tags = make(map[string]string)
		}
		o.tags[key] = value
	}
}

func WithTags(tags map[string]string) ResolveOption {
	return func(o *resolveOptions) {
		for key, value := range tags {
			WithTag(key, value)(o)
		}
	}
}

func WithVersion(version string) ResolveOption {
	return func(o *resolveOptions) {
		o.version = strings.TrimSpace(version)
	}
}

func WithZone(zone string) ResolveOption {
	return func(o *resolveOptions) {
		o.zone = strings.TrimSpace(zone)
	}
}

func IncludeUnhealthy() ResolveOption {
	return func(o *resolveOptions) {
		o.includeUnhealthy = true
	}
}

func normalizeInstance(instance Instance) Instance {
	instance.Service = strings.TrimSpace(instance.Service)
	instance.Endpoint = strings.TrimRight(strings.TrimSpace(instance.Endpoint), "/")
	instance.ID = strings.TrimSpace(instance.ID)
	if instance.ID == "" {
		instance.ID = instance.Endpoint
	}
	if instance.Weight < 0 {
		instance.Weight = 0
	}
	instance.Version = strings.TrimSpace(instance.Version)
	instance.Zone = strings.TrimSpace(instance.Zone)
	instance.Status = strings.TrimSpace(instance.Status)
	instance.Tags = cloneStringMap(instance.Tags)
	instance.Metadata = cloneStringMap(instance.Metadata)
	return instance
}

func cloneInstances(instances []Instance) []Instance {
	if len(instances) == 0 {
		return nil
	}
	out := make([]Instance, len(instances))
	for i, instance := range instances {
		out[i] = normalizeInstance(instance)
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func applyResolveOptions(opts []ResolveOption) resolveOptions {
	var options resolveOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	return options
}

func instanceMatches(instance Instance, options resolveOptions) bool {
	if !options.includeUnhealthy && instance.Status == StatusUnhealthy {
		return false
	}
	if options.version != "" && instance.Version != options.version {
		return false
	}
	if options.zone != "" && instance.Zone != options.zone {
		return false
	}
	for key, value := range options.tags {
		if instance.Tags[key] != value {
			return false
		}
	}
	return true
}
