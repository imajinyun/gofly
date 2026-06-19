// Package etcdsource implements a config.RemoteSource backed by the official
// etcd v3 client. It fetches a single key and streams live updates over a
// long-lived etcd watch channel (event-driven, no polling).
package etcdsource

import (
	"context"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/gofly/gofly/core/config"
)

// Config configures the etcd config source.
type Config struct {
	// Endpoints is the list of etcd endpoints (host:port).
	Endpoints []string
	// Key is the etcd key that stores the configuration payload.
	Key string
	// DialTimeout bounds the initial connection.
	DialTimeout time.Duration
	// Username/Password authenticate against etcd when set.
	Username string
	Password string
}

// Source is a config.RemoteSource backed by etcd v3.
type Source struct {
	client   *clientv3.Client
	key      string
	ownsConn bool
}

var _ config.RemoteSource = (*Source)(nil)

// New connects to etcd and returns a Source. The caller owns Close.
func New(cfg Config) (*Source, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("etcdsource: at least one endpoint is required")
	}
	if cfg.Key == "" {
		return nil, fmt.Errorf("etcdsource: key is required")
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("etcdsource: connect: %w", err)
	}
	return &Source{client: client, key: cfg.Key, ownsConn: true}, nil
}

// NewWithClient wraps an existing etcd client. Close will not close it.
func NewWithClient(client *clientv3.Client, key string) (*Source, error) {
	if client == nil {
		return nil, fmt.Errorf("etcdsource: client is nil")
	}
	if key == "" {
		return nil, fmt.Errorf("etcdsource: key is required")
	}
	return &Source{client: client, key: key}, nil
}

// Get fetches the current configuration payload.
func (s *Source) Get(ctx context.Context) (config.RemoteValue, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := s.client.Get(ctx, s.key)
	if err != nil {
		return config.RemoteValue{}, fmt.Errorf("etcdsource: get %q: %w", s.key, err)
	}
	if len(resp.Kvs) == 0 {
		return config.RemoteValue{}, fmt.Errorf("etcdsource: key %q not found", s.key)
	}
	kv := resp.Kvs[0]
	return config.RemoteValue{Key: s.key, Data: kv.Value, Version: kv.ModRevision}, nil
}

// Watch streams updates over a long-lived etcd watch channel until ctx ends.
func (s *Source) Watch(ctx context.Context, onChange func(config.RemoteValue)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	wch := s.client.Watch(ctx, s.key)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case resp, ok := <-wch:
			if !ok {
				return nil
			}
			if err := resp.Err(); err != nil {
				return fmt.Errorf("etcdsource: watch %q: %w", s.key, err)
			}
			for _, ev := range resp.Events {
				if ev.Kv == nil || ev.Type != clientv3.EventTypePut {
					continue
				}
				if onChange != nil {
					onChange(config.RemoteValue{
						Key:     s.key,
						Data:    ev.Kv.Value,
						Version: ev.Kv.ModRevision,
					})
				}
			}
		}
	}
}

// Close releases the etcd client when this Source owns it.
func (s *Source) Close() error {
	if s.ownsConn && s.client != nil {
		return s.client.Close()
	}
	return nil
}
