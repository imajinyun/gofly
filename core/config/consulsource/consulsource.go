// Package consulsource implements a config.RemoteSource backed by the Consul
// KV store. It reads a single key and streams live updates using Consul
// blocking queries (event-driven, no busy polling).
package consulsource

import (
	"context"
	"fmt"
	"math"
	"time"

	consulapi "github.com/hashicorp/consul/api"

	"github.com/imajinyun/gofly/core/config"
)

// Config configures the Consul KV config source.
type Config struct {
	// Address is the Consul agent address (host:port). Defaults to the
	// consulapi default when empty.
	Address string
	// Token is an optional ACL token.
	Token string
	// Key is the Consul KV key that stores the configuration payload.
	Key string
	// WaitTime bounds each blocking query. Defaults to 30s.
	WaitTime time.Duration
}

// Source is a config.RemoteSource backed by the Consul KV store.
type Source struct {
	client   *consulapi.Client
	key      string
	waitTime time.Duration
}

var _ config.RemoteSource = (*Source)(nil)

// New creates a Consul KV config source.
func New(cfg Config) (*Source, error) {
	if cfg.Key == "" {
		return nil, fmt.Errorf("consulsource: key is required")
	}
	apiCfg := consulapi.DefaultConfig()
	if cfg.Address != "" {
		apiCfg.Address = cfg.Address
	}
	if cfg.Token != "" {
		apiCfg.Token = cfg.Token
	}
	client, err := consulapi.NewClient(apiCfg)
	if err != nil {
		return nil, fmt.Errorf("consulsource: new client: %w", err)
	}
	return newWithClient(client, cfg.Key, cfg.WaitTime), nil
}

// NewWithClient wraps an existing Consul client.
func NewWithClient(client *consulapi.Client, key string) (*Source, error) {
	if client == nil {
		return nil, fmt.Errorf("consulsource: client is nil")
	}
	if key == "" {
		return nil, fmt.Errorf("consulsource: key is required")
	}
	return newWithClient(client, key, 0), nil
}

func newWithClient(client *consulapi.Client, key string, waitTime time.Duration) *Source {
	if waitTime <= 0 {
		waitTime = 30 * time.Second
	}
	return &Source{client: client, key: key, waitTime: waitTime}
}

// Get fetches the current configuration payload.
func (s *Source) Get(ctx context.Context) (config.RemoteValue, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	q := (&consulapi.QueryOptions{}).WithContext(ctx)
	pair, _, err := s.client.KV().Get(s.key, q)
	if err != nil {
		return config.RemoteValue{}, fmt.Errorf("consulsource: get %q: %w", s.key, err)
	}
	if pair == nil {
		return config.RemoteValue{}, fmt.Errorf("consulsource: key %q not found", s.key)
	}
	version, err := consulModifyIndexVersion(pair.ModifyIndex)
	if err != nil {
		return config.RemoteValue{}, err
	}
	return config.RemoteValue{Key: s.key, Data: pair.Value, Version: version}, nil
}

// Watch streams updates using Consul blocking queries until ctx ends.
func (s *Source) Watch(ctx context.Context, onChange func(config.RemoteValue)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastIndex uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		q := (&consulapi.QueryOptions{WaitIndex: lastIndex, WaitTime: s.waitTime}).WithContext(ctx)
		pair, meta, err := s.client.KV().Get(s.key, q)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("consulsource: watch %q: %w", s.key, err)
		}
		if meta == nil || meta.LastIndex == lastIndex {
			continue // no change; blocking query timed out
		}
		lastIndex = meta.LastIndex
		if pair == nil {
			continue // key deleted; nothing to publish
		}
		if onChange != nil {
			version, err := consulModifyIndexVersion(pair.ModifyIndex)
			if err != nil {
				return err
			}
			onChange(config.RemoteValue{
				Key:     s.key,
				Data:    pair.Value,
				Version: version,
			})
		}
	}
}

// Close is a no-op; Consul clients hold no long-lived connection to release.
func (s *Source) Close() error { return nil }

func consulModifyIndexVersion(index uint64) (int64, error) {
	if index > uint64(math.MaxInt64) {
		return 0, fmt.Errorf("consulsource: modify index %d overflows int64 version", index)
	}
	return int64(index), nil
}
