package etcdv3

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/imajinyun/gofly/core/discovery"
)

// ZRPCResolver reads the etcd layout used by go-zero zRPC, where each child
// key under the configured service key stores one raw gRPC endpoint. It is
// intentionally read-only so using it cannot rewrite an existing zRPC
// registry into gofly's native JSON instance format.
type ZRPCResolver struct {
	client *clientv3.Client
	once   sync.Once
	err    error
}

var _ discovery.Resolver = (*ZRPCResolver)(nil)

// NewZRPCResolver creates a resolver for an existing zRPC etcd registry. The
// service argument passed to Resolve or Watch is interpreted as Etcd.Key.
func NewZRPCResolver(cfg Config) (*ZRPCResolver, error) {
	cfg = cfg.withDefaults()
	if len(cfg.Endpoints) == 0 {
		return nil, errors.New("zrpc etcd resolver: at least one endpoint is required")
	}
	clientConfig := clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
	}
	if cfg.TLS != nil {
		clientConfig.TLS = cfg.TLS.Clone()
	}
	client, err := clientv3.New(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("zrpc etcd resolver: connect: %w", err)
	}
	return &ZRPCResolver{client: client}, nil
}

// NewZRPCResolverWithClient wraps an etcd client. The caller transfers client
// ownership to the resolver and must close the resolver when it is no longer
// used.
func NewZRPCResolverWithClient(client *clientv3.Client) (*ZRPCResolver, error) {
	if client == nil {
		return nil, errors.New("zrpc etcd resolver: client is nil")
	}
	return &ZRPCResolver{client: client}, nil
}

func (r *ZRPCResolver) Resolve(ctx context.Context, service string, opts ...discovery.ResolveOption) ([]discovery.Instance, error) {
	ctx = nonNilContext(ctx)
	instances, _, _, err := r.snapshot(ctx, service, opts...)
	return instances, err
}

func (r *ZRPCResolver) Watch(ctx context.Context, service string, opts ...discovery.ResolveOption) (<-chan discovery.Event, error) {
	ctx = nonNilContext(ctx)
	if r == nil || r.client == nil {
		return nil, errors.New("zrpc etcd resolver is not initialized")
	}
	service, prefix, err := zrpcServicePrefix(service)
	if err != nil {
		return nil, err
	}
	instances, values, revision, err := r.snapshot(ctx, service, opts...)
	if err != nil && !errors.Is(err, discovery.ErrNoInstances) {
		return nil, err
	}
	out := make(chan discovery.Event, 1)
	out <- discovery.Event{Type: discovery.EventSnapshot, Service: service, At: time.Now(), Instances: instances}
	watchOptions := []clientv3.OpOption{clientv3.WithPrefix(), clientv3.WithPrevKV()}
	if revision > 0 {
		watchOptions = append(watchOptions, clientv3.WithRev(revision+1))
	}
	watch := r.client.Watch(ctx, prefix, watchOptions...)
	go r.forwardZRPC(ctx, service, values, opts, watch, out)
	return out, nil
}

func (r *ZRPCResolver) snapshot(ctx context.Context, service string, opts ...discovery.ResolveOption) ([]discovery.Instance, map[string]string, int64, error) {
	if r == nil || r.client == nil {
		return nil, nil, 0, errors.New("zrpc etcd resolver is not initialized")
	}
	service, prefix, err := zrpcServicePrefix(service)
	if err != nil {
		return nil, nil, 0, err
	}
	response, err := r.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, nil, 0, fmt.Errorf("zrpc etcd resolver: get endpoints: %w", err)
	}
	values := make(map[string]string, len(response.Kvs))
	for _, item := range response.Kvs {
		if endpoint := strings.TrimSpace(string(item.Value)); endpoint != "" {
			values[string(item.Key)] = endpoint
		}
	}
	instances, err := filterZRPCInstances(ctx, service, values, opts...)
	revision := int64(0)
	if response.Header != nil {
		revision = response.Header.Revision
	}
	return instances, values, revision, err
}

func (r *ZRPCResolver) forwardZRPC(ctx context.Context, service string, values map[string]string, opts []discovery.ResolveOption, watch clientv3.WatchChan, out chan<- discovery.Event) {
	defer close(out)
	previous, _ := filterZRPCInstances(ctx, service, values, opts...)
	for {
		select {
		case <-ctx.Done():
			return
		case response, ok := <-watch:
			if !ok || response.Canceled || response.Err() != nil {
				return
			}
			for _, event := range response.Events {
				key := string(event.Kv.Key)
				if event.Type == clientv3.EventTypeDelete {
					delete(values, key)
					continue
				}
				if endpoint := strings.TrimSpace(string(event.Kv.Value)); endpoint != "" {
					values[key] = endpoint
				} else {
					delete(values, key)
				}
			}
			current, err := filterZRPCInstances(ctx, service, values, opts...)
			if err != nil && !errors.Is(err, discovery.ErrNoInstances) {
				return
			}
			event := discovery.Event{
				Type: discovery.EventSnapshot, Service: service, At: time.Now(),
				Instances: current, Changes: discovery.DiffInstances(previous, current),
			}
			previous = current
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}

func filterZRPCInstances(ctx context.Context, service string, values map[string]string, opts ...discovery.ResolveOption) ([]discovery.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	memory := discovery.NewMemoryRegistry()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		endpoint := strings.TrimSpace(values[key])
		if endpoint == "" {
			continue
		}
		id := strings.TrimPrefix(key, strings.TrimRight(service, "/")+"/")
		if id == "" {
			id = endpoint
		}
		if _, err := memory.Register(ctx, discovery.Instance{ID: id, Service: service, Endpoint: endpoint, Status: discovery.StatusHealthy}); err != nil {
			return nil, err
		}
	}
	return memory.Resolve(ctx, service, opts...)
}

func zrpcServicePrefix(service string) (string, string, error) {
	service = strings.TrimRight(strings.TrimSpace(service), "/")
	if service == "" {
		return "", "", errors.New("zrpc etcd resolver: service key is required")
	}
	return service, service + "/", nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// Close releases the owned etcd client. It is safe to call repeatedly.
func (r *ZRPCResolver) Close(context.Context) error {
	if r == nil || r.client == nil {
		return nil
	}
	r.once.Do(func() { r.err = r.client.Close() })
	return r.err
}
