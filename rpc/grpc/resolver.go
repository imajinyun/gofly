// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/gofly/gofly/rpc"

	stdgrpc "google.golang.org/grpc"
	_ "google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/resolver"
)

const ResolverScheme = "gofly"

const roundRobinServiceConfig = `{"loadBalancingConfig":[{"round_robin":{}}]}`

type ResolverOption func(*ResolverBuilder)

type ResolverBuilder struct {
	scheme        string
	registry      *rpc.Registry
	resolvers     map[string]rpc.WatchResolver
	serviceConfig string
}

type watchResolver struct {
	ctx    context.Context
	cancel context.CancelFunc
	cc     resolver.ClientConn
	source rpc.WatchResolver
	config string
	mu     sync.Mutex
	closed bool
}

func Target(service string) string {
	return ResolverScheme + ":///" + strings.Trim(strings.TrimSpace(service), "/")
}

func NewResolverBuilder(resolvers map[string]rpc.WatchResolver, opts ...ResolverOption) *ResolverBuilder {
	b := &ResolverBuilder{scheme: ResolverScheme, serviceConfig: roundRobinServiceConfig}
	if len(resolvers) > 0 {
		b.resolvers = make(map[string]rpc.WatchResolver, len(resolvers))
		for service, source := range resolvers {
			service = strings.Trim(strings.TrimSpace(service), "/")
			if service != "" && source != nil {
				b.resolvers[service] = source
			}
		}
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	if b.scheme == "" {
		b.scheme = ResolverScheme
	}
	return b
}

func NewRegistryResolverBuilder(registry *rpc.Registry, opts ...ResolverOption) *ResolverBuilder {
	b := NewResolverBuilder(nil, opts...)
	b.registry = registry
	return b
}

func WithResolverScheme(scheme string) ResolverOption {
	return func(b *ResolverBuilder) {
		if scheme = strings.TrimSpace(scheme); scheme != "" {
			b.scheme = strings.ToLower(scheme)
		}
	}
}

func WithResolverServiceConfig(config string) ResolverOption {
	return func(b *ResolverBuilder) {
		b.serviceConfig = strings.TrimSpace(config)
	}
}

func WithRoundRobinResolver() ResolverOption {
	return WithResolverServiceConfig(roundRobinServiceConfig)
}

func WithServiceResolver(service string, source rpc.WatchResolver, opts ...ResolverOption) ClientOption {
	return WithDialOptions(stdgrpc.WithResolvers(NewResolverBuilder(map[string]rpc.WatchResolver{service: source}, opts...)))
}

func WithRegistryResolver(registry *rpc.Registry, opts ...ResolverOption) ClientOption {
	return WithDialOptions(stdgrpc.WithResolvers(NewRegistryResolverBuilder(registry, opts...)))
}

func (b *ResolverBuilder) Scheme() string {
	if b == nil || b.scheme == "" {
		return ResolverScheme
	}
	return b.scheme
}

func (b *ResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	if b == nil {
		return nil, errors.New("grpc resolver builder is nil")
	}
	service := serviceFromTarget(target)
	if service == "" {
		return nil, errors.New("grpc resolver service is required")
	}
	source := b.source(service)
	if source == nil {
		return nil, fmt.Errorf("grpc resolver for service %q is not registered", service)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &watchResolver{ctx: ctx, cancel: cancel, cc: cc, source: source, config: b.serviceConfig}
	r.ResolveNow(resolver.ResolveNowOptions{})
	updates, err := source.Watch(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("watch grpc resolver service %q: %w", service, err)
	}
	go r.watch(updates)
	return r, nil
}

func (b *ResolverBuilder) source(service string) rpc.WatchResolver {
	if b == nil {
		return nil
	}
	if source := b.resolvers[service]; source != nil {
		return source
	}
	if b.registry == nil {
		return nil
	}
	source, _ := b.registry.Resolver(service).(rpc.WatchResolver)
	return source
}

func (r *watchResolver) ResolveNow(resolver.ResolveNowOptions) {
	if r == nil || r.isClosed() {
		return
	}
	endpoints, err := r.source.Resolve(r.ctx)
	if err != nil {
		r.cc.ReportError(fmt.Errorf("resolve grpc endpoints: %w", err))
		return
	}
	r.update(endpoints)
}

func (r *watchResolver) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.cancel()
	}
	r.mu.Unlock()
}

func (r *watchResolver) watch(updates <-chan []string) {
	for {
		select {
		case <-r.ctx.Done():
			return
		case endpoints, ok := <-updates:
			if !ok {
				return
			}
			r.update(endpoints)
		}
	}
}

func (r *watchResolver) update(endpoints []string) {
	if r == nil || r.isClosed() {
		return
	}
	addresses := make([]resolver.Address, 0, len(endpoints))
	seen := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
		if endpoint == "" {
			continue
		}
		if _, ok := seen[endpoint]; ok {
			continue
		}
		seen[endpoint] = struct{}{}
		addresses = append(addresses, resolver.Address{Addr: endpoint})
	}
	if len(addresses) == 0 {
		r.cc.ReportError(errors.New("no grpc endpoints resolved"))
		return
	}
	state := resolver.State{Addresses: addresses}
	if r.config != "" {
		state.ServiceConfig = r.cc.ParseServiceConfig(r.config)
	}
	if err := r.cc.UpdateState(state); err != nil {
		r.cc.ReportError(fmt.Errorf("update grpc resolver state: %w", err))
	}
}

func (r *watchResolver) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func serviceFromTarget(target resolver.Target) string {
	service := strings.Trim(strings.TrimSpace(target.Endpoint()), "/")
	if service != "" {
		return service
	}
	service = strings.Trim(strings.TrimSpace(target.URL.Path), "/")
	if service != "" {
		return service
	}
	return strings.Trim(strings.TrimSpace(target.URL.Host), "/")
}
