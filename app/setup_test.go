package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type setupConfig struct {
	ServiceConfig
	Message string
}

type failingProvider struct{}

func (failingProvider) Load(context.Context) (setupConfig, error) {
	return setupConfig{}, errors.New("load failed")
}

type staticSetupProvider struct {
	value setupConfig
}

func (p staticSetupProvider) Load(ctx context.Context) (setupConfig, error) {
	if err := ctx.Err(); err != nil {
		return setupConfig{}, err
	}
	return p.value, nil
}

func TestSetUpMapsServiceTimeouts(t *testing.T) {
	var opts Options
	for _, opt := range SetUp(ServiceConfig{StartupTimeout: time.Second, ShutdownTimeout: 2 * time.Second}) {
		opt(&opts)
	}
	if opts.StartupTimeout != time.Second || opts.ShutdownTimeout != 2*time.Second {
		t.Fatalf("options = %+v, want configured startup and shutdown timeouts", opts)
	}
}

func TestSetUpServiceMapsUnifiedServiceTimeouts(t *testing.T) {
	var opts Options
	for _, opt := range SetUpService(ServiceConf{Shutdown: ShutdownConfig{StartupTimeout: 3 * time.Second, Timeout: 4 * time.Second}}) {
		opt(&opts)
	}
	if opts.StartupTimeout != 3*time.Second || opts.ShutdownTimeout != 4*time.Second {
		t.Fatalf("options = %+v, want unified startup and shutdown timeouts", opts)
	}
}

func TestRunWithProviderLoadsBuildsAndRuns(t *testing.T) {
	server := &fakeServer{stop: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := staticSetupProvider{value: setupConfig{ServiceConfig: ServiceConfig{StartupTimeout: time.Second, ShutdownTimeout: time.Second}, Message: "boot"}}

	err := RunWithProvider(ctx, provider, func(ctx context.Context, cfg setupConfig) ([]Server, []Option, error) {
		if cfg.Message != "boot" {
			return nil, nil, errors.New("unexpected config")
		}
		return []Server{server}, append(SetUp(cfg.ServiceConfig), AfterStart(func(context.Context) error {
			cancel()
			return nil
		})), nil
	})
	if err != nil {
		t.Fatalf("RunWithProvider: %v", err)
	}
	if !server.started.Load() || !server.shutdown.Load() {
		t.Fatalf("started/shutdown = %v/%v, want true/true", server.started.Load(), server.shutdown.Load())
	}
}

func TestRunWithProviderValidatesInputs(t *testing.T) {
	if err := RunWithProvider[setupConfig](context.Background(), nil, nil); err == nil {
		t.Fatal("RunWithProvider nil provider succeeded, want error")
	}
	if err := RunWithProvider(context.Background(), staticSetupProvider{}, nil); err == nil {
		t.Fatal("RunWithProvider nil builder succeeded, want error")
	}
	if err := RunWithProvider(context.Background(), failingProvider{}, func(context.Context, setupConfig) ([]Server, []Option, error) {
		return nil, nil, nil
	}); err == nil {
		t.Fatal("RunWithProvider load failure succeeded, want error")
	}
}
