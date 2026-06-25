// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	core "github.com/imajinyun/gofly/core"
	"github.com/imajinyun/gofly/core/discovery"
)

type EtcdRegistry struct {
	client        *http.Client
	baseURL       string
	prefix        string
	watchInterval time.Duration
}

var _ discovery.Registrar = (*EtcdRegistry)(nil)
var _ discovery.Resolver = (*EtcdRegistry)(nil)

const defaultEtcdWatchInterval = 100 * time.Millisecond

func NewEtcdRegistry(baseURL string, prefix string, client *http.Client) (*EtcdRegistry, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("etcd base url is required")
	}
	if prefix == "" {
		prefix = "/gofly/services"
	}
	if client == nil {
		client = core.DefaultHTTPClient()
	}
	return &EtcdRegistry{client: client, baseURL: strings.TrimRight(baseURL, "/"), prefix: strings.TrimRight(prefix, "/"), watchInterval: defaultEtcdWatchInterval}, nil
}

func (r *EtcdRegistry) RegisterService(ctx context.Context, service string, endpoint string) error {
	return r.RegisterInstance(ctx, service, ServiceInstance{Endpoint: endpoint})
}

func (r *EtcdRegistry) RegisterInstance(ctx context.Context, service string, instance ServiceInstance) error {
	_, err := r.Register(ctx, discoveryInstance(service, instance, instance.Endpoint))
	return err
}

func (r *EtcdRegistry) Register(ctx context.Context, instance discovery.Instance, opts ...discovery.RegisterOption) (discovery.Lease, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instance = normalizeEtcdDiscoveryInstance(instance)
	if instance.Service == "" || instance.Endpoint == "" {
		return noopEtcdLease{instance: instance}, nil
	}
	// The lightweight HTTP v3 adapter does not allocate native etcd leases. Keep
	// accepting discovery.RegisterOption for API compatibility; callers can refresh
	// the key through the returned Lease.KeepAlive when they need lease-like behavior.
	_ = opts
	data, err := json.Marshal(instance)
	if err != nil {
		return nil, fmt.Errorf("marshal etcd service instance: %w", err)
	}
	if err := r.do(ctx, "/v3/kv/put", map[string]string{
		"key":   b64(r.key(instance.Service, instance.Endpoint)),
		"value": b64(string(data)),
	}, nil); err != nil {
		return nil, err
	}
	return &etcdLease{registry: r, instance: instance}, nil
}

func (r *EtcdRegistry) DeregisterService(ctx context.Context, service string, endpoint string) error {
	return r.Deregister(ctx, discovery.Instance{Service: service, Endpoint: endpoint})
}

func (r *EtcdRegistry) Deregister(ctx context.Context, instance discovery.Instance) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	instance = normalizeEtcdDiscoveryInstance(instance)
	if instance.Service == "" || instance.Endpoint == "" {
		return nil
	}
	return r.do(ctx, "/v3/kv/deleterange", map[string]string{"key": b64(r.key(instance.Service, instance.Endpoint))}, nil)
}

func (r *EtcdRegistry) Resolver(service string) Resolver {
	return NewDiscoveryResolver(r, service)
}

func (r *EtcdRegistry) ResolveInstances(ctx context.Context, service string) ([]ServiceInstance, error) {
	instances, err := r.Resolve(ctx, service)
	if err != nil {
		return nil, err
	}
	return serviceInstancesFromDiscovery(instances), nil
}

func (r *EtcdRegistry) Resolve(ctx context.Context, service string, opts ...discovery.ResolveOption) ([]discovery.Instance, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, fmt.Errorf("service name is required")
	}
	var resp etcdRangeResponse
	if err := r.do(ctx, "/v3/kv/range", map[string]string{"key": b64(r.servicePrefix(service)), "range_end": b64(prefixEnd(r.servicePrefix(service)))}, &resp); err != nil {
		return nil, err
	}
	memory := discovery.NewMemoryRegistry()
	for _, kv := range resp.KVs {
		instance, err := decodeEtcdInstance(service, kv.Value)
		if err != nil {
			return nil, err
		}
		if instance.Endpoint != "" {
			if _, err := memory.Register(ctx, instance); err != nil {
				return nil, err
			}
		}
	}
	instances, err := memory.Resolve(ctx, service, opts...)
	if err != nil {
		return nil, err
	}
	return instances, nil
}

func (r *EtcdRegistry) Watch(ctx context.Context, service string, opts ...discovery.ResolveOption) (<-chan discovery.Event, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service = strings.TrimSpace(service)
	if service == "" {
		return nil, fmt.Errorf("service name is required")
	}
	out := make(chan discovery.Event, 1)
	instances, err := r.Resolve(ctx, service, opts...)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		instances = nil
	}
	out <- discovery.Event{Type: discovery.EventSnapshot, Service: service, At: time.Now(), Instances: instances}
	go r.watch(ctx, service, opts, out, instances)
	return out, nil
}

func (r *EtcdRegistry) watch(ctx context.Context, service string, opts []discovery.ResolveOption, out chan<- discovery.Event, previous []discovery.Instance) {
	defer close(out)
	interval := r.watchInterval
	if interval <= 0 {
		interval = defaultEtcdWatchInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	previousByID := etcdInstancesByID(previous)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			instances, err := r.Resolve(ctx, service, opts...)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				instances = nil
			}
			nextByID := etcdInstancesByID(instances)
			eventType, instance, changed := diffEtcdInstances(previousByID, nextByID)
			if !changed {
				continue
			}
			previousByID = nextByID
			select {
			case out <- discovery.Event{Type: eventType, Service: service, At: time.Now(), Instance: instance, Instances: instances}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (r *EtcdRegistry) do(ctx context.Context, path string, body any, out any) error {
	ctx = core.Context(ctx)
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal etcd request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create etcd request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("call etcd: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("call etcd: status %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode etcd response: %w", err)
	}
	return nil
}

func (r *EtcdRegistry) servicePrefix(service string) string { return r.prefix + "/" + service + "/" }
func (r *EtcdRegistry) key(service string, endpoint string) string {
	return r.servicePrefix(service) + endpoint
}
func b64(v string) string { return base64.StdEncoding.EncodeToString([]byte(v)) }

func prefixEnd(prefix string) string {
	if prefix == "" {
		return "\x00"
	}
	b := []byte(prefix)
	b[len(b)-1]++
	return string(b)
}

type etcdRangeResponse struct {
	KVs []struct {
		Value string `json:"value"`
	} `json:"kvs"`
}

type etcdLease struct {
	registry  *EtcdRegistry
	instance  discovery.Instance
	expiresAt time.Time
}

type noopEtcdLease struct {
	instance discovery.Instance
}

func (l *etcdLease) KeepAlive(ctx context.Context) error {
	if l == nil || l.registry == nil {
		return nil
	}
	_, err := l.registry.Register(ctx, l.instance)
	return err
}

func (l *etcdLease) Close(ctx context.Context) error {
	if l == nil || l.registry == nil {
		return nil
	}
	return l.registry.Deregister(ctx, l.instance)
}

func (l *etcdLease) Instance() discovery.Instance {
	if l == nil {
		return discovery.Instance{}
	}
	return normalizeEtcdDiscoveryInstance(l.instance)
}

func (l *etcdLease) ExpiresAt() time.Time {
	if l == nil {
		return time.Time{}
	}
	return l.expiresAt
}

func (l noopEtcdLease) KeepAlive(context.Context) error { return nil }
func (l noopEtcdLease) Close(context.Context) error     { return nil }
func (l noopEtcdLease) Instance() discovery.Instance {
	return normalizeEtcdDiscoveryInstance(l.instance)
}
func (l noopEtcdLease) ExpiresAt() time.Time { return time.Time{} }

func decodeEtcdInstance(service string, encodedValue string) (discovery.Instance, error) {
	value, err := base64.StdEncoding.DecodeString(encodedValue)
	if err != nil {
		return discovery.Instance{}, fmt.Errorf("decode etcd value: %w", err)
	}
	var instance discovery.Instance
	if err := json.Unmarshal(value, &instance); err == nil && instance.Endpoint != "" {
		if instance.Service == "" {
			instance.Service = service
		}
		return normalizeEtcdDiscoveryInstance(instance), nil
	}
	var rpcInstance ServiceInstance
	if err := json.Unmarshal(value, &rpcInstance); err != nil {
		return discovery.Instance{}, fmt.Errorf("unmarshal etcd service instance: %w", err)
	}
	return normalizeEtcdDiscoveryInstance(discoveryInstance(service, rpcInstance, rpcInstance.Endpoint)), nil
}

func normalizeEtcdDiscoveryInstance(instance discovery.Instance) discovery.Instance {
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
	instance.Tags = cloneTags(instance.Tags)
	instance.Metadata = cloneTags(instance.Metadata)
	return instance
}

func etcdInstancesByID(instances []discovery.Instance) map[string]discovery.Instance {
	out := make(map[string]discovery.Instance, len(instances))
	for _, instance := range instances {
		instance = normalizeEtcdDiscoveryInstance(instance)
		if instance.ID != "" {
			out[instance.ID] = instance
		}
	}
	return out
}

func diffEtcdInstances(previous, next map[string]discovery.Instance) (discovery.EventType, discovery.Instance, bool) {
	for id, instance := range next {
		old, ok := previous[id]
		if !ok || !sameEtcdInstance(old, instance) {
			return discovery.EventRegistered, instance, true
		}
	}
	for id, instance := range previous {
		if _, ok := next[id]; !ok {
			return discovery.EventDeregister, instance, true
		}
	}
	return discovery.EventSnapshot, discovery.Instance{}, false
}

func sameEtcdInstance(a, b discovery.Instance) bool {
	a = normalizeEtcdDiscoveryInstance(a)
	b = normalizeEtcdDiscoveryInstance(b)
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
}
