// Package app provides the gofly application runtime lifecycle management.
// It coordinates server startup, graceful shutdown, hooks, and production
// configuration defaults.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ServiceConfig holds the basic service identity and lifecycle timeouts.
type ServiceConfig struct {
	Name            string        `json:"name"`
	Mode            string        `json:"mode,omitempty"`
	StartupTimeout  time.Duration `json:"startupTimeout,omitempty"`
	ShutdownTimeout time.Duration `json:"shutdownTimeout,omitempty"`
}

// ConfigProvider loads a configuration value from an external source.
type ConfigProvider[T any] interface {
	Load(context.Context) (T, error)
}

// ServerBuilder constructs servers and runtime options from a loaded config.
type ServerBuilder[T any] func(context.Context, T) ([]Server, []Option, error)

// SetUp converts ServiceConfig into app Options.
func SetUp(conf ServiceConfig) []Option {
	options := make([]Option, 0, 2)
	if conf.StartupTimeout > 0 {
		options = append(options, WithStartupTimeout(conf.StartupTimeout))
	}
	if conf.ShutdownTimeout > 0 {
		options = append(options, WithShutdownTimeout(conf.ShutdownTimeout))
	}
	return options
}

// SetUpService is a convenience wrapper around SetUp using ServiceConf.
func SetUpService(conf ServiceConf) []Option {
	return SetUp(conf.ServiceConfig())
}

// RunWithProvider loads configuration from provider, builds servers using
// builder, and runs them with the supplied options.
func RunWithProvider[T any](ctx context.Context, provider ConfigProvider[T], builder ServerBuilder[T], opts ...Option) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if provider == nil {
		return errors.New("config provider is nil")
	}
	if builder == nil {
		return errors.New("server builder is nil")
	}
	conf, err := provider.Load(ctx)
	if err != nil {
		return fmt.Errorf("load service config: %w", err)
	}
	servers, builtOpts, err := builder(ctx, conf)
	if err != nil {
		return fmt.Errorf("build service: %w", err)
	}
	allOpts := make([]Option, 0, len(builtOpts)+len(opts))
	allOpts = append(allOpts, builtOpts...)
	allOpts = append(allOpts, opts...)
	return Run(ctx, servers, allOpts...)
}
