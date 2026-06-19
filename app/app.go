// Package app provides the gofly application runtime lifecycle management.
// It coordinates server startup, graceful shutdown, hooks, and production
// configuration defaults.
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Server is the interface implemented by HTTP, gRPC, and other servers.
type Server interface {
	Start() error
	Shutdown(context.Context) error
}

// Option customizes the behaviour of Run.
type Option func(*Options)

// Hook is a lifecycle callback executed during startup or shutdown.
type Hook func(context.Context) error

// Options collects all runtime configuration for Run.
type Options struct {
	ShutdownTimeout time.Duration
	StartupTimeout  time.Duration
	BeforeStart     []Hook
	AfterStart      []Hook
	BeforeShutdown  []Hook
	AfterShutdown   []Hook
}

// WithShutdownTimeout sets the maximum duration to wait for graceful shutdown.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.ShutdownTimeout = timeout
		}
	}
}

// WithStartupTimeout sets the maximum duration to wait for startup.
func WithStartupTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.StartupTimeout = timeout
		}
	}
}

// BeforeStart registers hooks to run before servers start.
func BeforeStart(hooks ...Hook) Option {
	return func(o *Options) {
		o.BeforeStart = append(o.BeforeStart, hooks...)
	}
}

// AfterStart registers hooks to run after servers have started.
func AfterStart(hooks ...Hook) Option {
	return func(o *Options) {
		o.AfterStart = append(o.AfterStart, hooks...)
	}
}

// BeforeShutdown registers hooks to run before shutdown begins.
func BeforeShutdown(hooks ...Hook) Option {
	return func(o *Options) {
		o.BeforeShutdown = append(o.BeforeShutdown, hooks...)
	}
}

// AfterShutdown registers hooks to run after shutdown completes.
func AfterShutdown(hooks ...Hook) Option {
	return func(o *Options) {
		o.AfterShutdown = append(o.AfterShutdown, hooks...)
	}
}

// Run starts all servers and blocks until they stop or the context is cancelled.
func Run(ctx context.Context, servers []Server, opts ...Option) error {
	if ctx == nil {
		ctx = context.Background()
	}
	options := Options{StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second}
	for _, opt := range opts {
		opt(&options)
	}
	if len(servers) == 0 {
		return errors.New("server list is empty")
	}
	startupCtx, startupCancel := context.WithTimeout(ctx, options.StartupTimeout)
	defer startupCancel()
	if err := runHooks(startupCtx, options.BeforeStart); err != nil {
		return fmt.Errorf("before start: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, len(servers))
	var wg sync.WaitGroup
	for _, srv := range servers {
		srv := srv
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Start(); err != nil {
				errCh <- err
				cancel()
			}
		}()
	}
	if err := runHooks(startupCtx, options.AfterStart); err != nil {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(shutdownContext(ctx), options.ShutdownTimeout)
		defer shutdownCancel()
		_ = shutdownServers(shutdownCtx, servers)
		wg.Wait()
		return fmt.Errorf("after start: %w", err)
	}
	var runErr error
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
	case err := <-errCh:
		runErr = err
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(shutdownContext(ctx), options.ShutdownTimeout)
	defer shutdownCancel()
	var shutdownErr error
	if err := runHooks(shutdownCtx, options.BeforeShutdown); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("before shutdown: %w", err))
	}
	shutdownErr = errors.Join(shutdownErr, shutdownServers(shutdownCtx, servers))
	if err := runHooks(shutdownCtx, options.AfterShutdown); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("after shutdown: %w", err))
	}
	wg.Wait()
	if shutdownErr != nil {
		return fmt.Errorf("shutdown servers: %w", shutdownErr)
	}
	if errors.Is(runErr, context.Canceled) {
		return nil
	}
	return runErr
}

func runHooks(ctx context.Context, hooks []Hook) error {
	var hookErr error
	for _, hook := range hooks {
		if hook == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(hookErr, err)
		}
		if err := hook(ctx); err != nil {
			hookErr = errors.Join(hookErr, err)
		}
	}
	return hookErr
}

func shutdownServers(ctx context.Context, servers []Server) error {
	var shutdownErr error
	for _, srv := range servers {
		if err := srv.Shutdown(ctx); err != nil {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}
	return shutdownErr
}
