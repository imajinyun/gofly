package etcdv3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/imajinyun/gofly/core/discovery"
)

// ZRPCRegistrar publishes the etcd layout consumed by go-zero zRPC, where
// each child key under the service key stores one raw gRPC endpoint. It is
// intentionally registration-only so it cannot be confused with gofly's
// structured JSON registry.
type ZRPCRegistrar struct {
	client *clientv3.Client
	cfg    Config

	mu     sync.Mutex
	leases []*zrpcLease
	closed bool
}

var _ discovery.Registrar = (*ZRPCRegistrar)(nil)

// NewZRPCRegistrar creates a registrar for go-zero zRPC-compatible etcd keys.
// The caller owns Close.
func NewZRPCRegistrar(cfg Config) (*ZRPCRegistrar, error) {
	cfg = cfg.withDefaults()
	if len(cfg.Endpoints) == 0 {
		return nil, errors.New("zrpc etcd registrar: at least one endpoint is required")
	}
	clientConfig := clientv3.Config{
		Endpoints: cfg.Endpoints, DialTimeout: cfg.DialTimeout,
		Username: cfg.Username, Password: cfg.Password,
	}
	if cfg.TLS != nil {
		clientConfig.TLS = cfg.TLS.Clone()
	}
	client, err := clientv3.New(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("zrpc etcd registrar: connect: %w", err)
	}
	return &ZRPCRegistrar{client: client, cfg: cfg}, nil
}

// NewZRPCRegistrarWithClient wraps an etcd client. The caller transfers client
// ownership to the registrar and must close the registrar when it is no longer
// used.
func NewZRPCRegistrarWithClient(client *clientv3.Client, cfg Config) (*ZRPCRegistrar, error) {
	if client == nil {
		return nil, errors.New("zrpc etcd registrar: client is nil")
	}
	return &ZRPCRegistrar{client: client, cfg: cfg.withDefaults()}, nil
}

func (r *ZRPCRegistrar) Register(ctx context.Context, instance discovery.Instance, opts ...discovery.RegisterOption) (discovery.Lease, error) {
	ctx = nonNilContext(ctx)
	if r == nil || r.client == nil {
		return nil, errors.New("zrpc etcd registrar is not initialized")
	}
	instance = normalize(instance)
	if instance.Service == "" || instance.Endpoint == "" {
		return nil, errors.New("zrpc etcd registrar: instance requires service and endpoint")
	}
	// RegisterOption's state is intentionally private to the discovery package;
	// the compatibility registrar therefore uses its explicit Config.TTL.
	_ = opts
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, errors.New("zrpc etcd registrar is closed")
	}
	grant, err := r.client.Grant(ctx, int64(r.cfg.TTL.Seconds())+1)
	if err != nil {
		return nil, fmt.Errorf("zrpc etcd registrar: grant lease: %w", err)
	}
	id := fmt.Sprint(grant.ID)
	if r.cfg.RegistrationID > 0 {
		id = fmt.Sprint(r.cfg.RegistrationID)
	}
	instance.ID = id
	key := zrpcRegistrationKey(instance.Service, id)
	if _, err := r.client.Put(ctx, key, instance.Endpoint, clientv3.WithLease(grant.ID)); err != nil {
		_, revokeErr := r.client.Revoke(context.WithoutCancel(ctx), grant.ID)
		return nil, errors.Join(fmt.Errorf("zrpc etcd registrar: put endpoint: %w", err), revokeErr)
	}
	keepCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	keepAlive, err := r.client.KeepAlive(keepCtx, grant.ID)
	if err != nil {
		cancel()
		_, revokeErr := r.client.Revoke(context.WithoutCancel(ctx), grant.ID)
		return nil, errors.Join(fmt.Errorf("zrpc etcd registrar: keepalive: %w", err), revokeErr)
	}
	lease := &zrpcLease{
		registrar: r, instance: instance, id: grant.ID, cancel: cancel,
		expires: time.Now().Add(r.cfg.TTL),
	}
	go lease.consume(keepAlive)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = lease.Close(context.WithoutCancel(ctx))
		return nil, errors.New("zrpc etcd registrar is closed")
	}
	r.leases = append(r.leases, lease)
	r.mu.Unlock()
	return lease, nil
}

func (r *ZRPCRegistrar) Deregister(ctx context.Context, instance discovery.Instance) error {
	ctx = nonNilContext(ctx)
	if r == nil || r.client == nil {
		return errors.New("zrpc etcd registrar is not initialized")
	}
	instance = normalize(instance)
	if instance.Service == "" || instance.ID == "" {
		return nil
	}
	if _, err := r.client.Delete(ctx, zrpcRegistrationKey(instance.Service, instance.ID)); err != nil {
		return fmt.Errorf("zrpc etcd registrar: delete endpoint: %w", err)
	}
	return nil
}

// Close revokes all registrations and closes the owned etcd client.
func (r *ZRPCRegistrar) Close(ctx context.Context) error {
	if r == nil || r.client == nil {
		return nil
	}
	ctx = nonNilContext(ctx)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	leases := r.leases
	r.leases = nil
	r.mu.Unlock()
	var closeErr error
	for index := len(leases) - 1; index >= 0; index-- {
		closeErr = errors.Join(closeErr, leases[index].Close(ctx))
	}
	closeErr = errors.Join(closeErr, r.client.Close())
	return closeErr
}

func zrpcRegistrationKey(service, id string) string {
	return strings.TrimRight(strings.TrimSpace(service), "/") + "/" + strings.TrimLeft(strings.TrimSpace(id), "/")
}

type zrpcLease struct {
	registrar *ZRPCRegistrar
	instance  discovery.Instance
	id        clientv3.LeaseID
	cancel    context.CancelFunc

	mu      sync.Mutex
	expires time.Time
	closed  bool
}

var _ discovery.Lease = (*zrpcLease)(nil)

func (l *zrpcLease) consume(ch <-chan *clientv3.LeaseKeepAliveResponse) {
	for response := range ch {
		if response == nil {
			continue
		}
		l.mu.Lock()
		l.expires = time.Now().Add(time.Duration(response.TTL) * time.Second)
		l.mu.Unlock()
	}
}

func (l *zrpcLease) KeepAlive(ctx context.Context) error {
	if l == nil || l.registrar == nil {
		return nil
	}
	response, err := l.registrar.client.KeepAliveOnce(nonNilContext(ctx), l.id)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.expires = time.Now().Add(time.Duration(response.TTL) * time.Second)
	l.mu.Unlock()
	return nil
}

func (l *zrpcLease) Close(ctx context.Context) error {
	if l == nil || l.registrar == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
	if _, err := l.registrar.client.Revoke(nonNilContext(ctx), l.id); err != nil {
		return fmt.Errorf("zrpc etcd registrar: revoke lease: %w", err)
	}
	return nil
}

func (l *zrpcLease) Instance() discovery.Instance {
	if l == nil {
		return discovery.Instance{}
	}
	return l.instance
}

func (l *zrpcLease) ExpiresAt() time.Time {
	if l == nil {
		return time.Time{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expires
}
