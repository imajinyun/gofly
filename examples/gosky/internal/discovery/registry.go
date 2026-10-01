package discovery

import (
	"context"
	"fmt"
	"os"
	"strings"

	corediscovery "github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/discovery/consul"
	"github.com/imajinyun/gofly/core/discovery/etcdv3"

	appconfig "github.com/imajinyun/gofly/examples/gosky/internal/config"
)

type closeFunc func(context.Context) error

func NewRegistry(ctx context.Context, cfg appconfig.DiscoveryConfig) (corediscovery.Registry, closeFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	switch cfg.ProviderName() {
	case "memory":
		return corediscovery.NewMemoryRegistry(), noopClose, nil
	case "consul":
		registry, err := consul.New(consul.Config{Address: cfg.Address, Token: envValue(cfg.TokenEnv), TTL: cfg.RegistryTTL()})
		if err != nil {
			return nil, nil, fmt.Errorf("create consul discovery registry: %w", err)
		}
		return registry, registry.Close, nil
	case "etcdv3":
		registry, err := etcdv3.New(etcdv3.Config{
			Endpoints:   cfg.ResolvedEndpoints(),
			Prefix:      cfg.Prefix,
			DialTimeout: cfg.DialTimeoutDuration(),
			TTL:         cfg.RegistryTTL(),
			Username:    envValue(cfg.UsernameEnv),
			Password:    envValue(cfg.PasswordEnv),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("create etcdv3 discovery registry: %w", err)
		}
		return registry, registry.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported discovery provider %q", cfg.Provider)
	}
}

func noopClose(context.Context) error { return nil }

func envValue(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return os.Getenv(name)
}
