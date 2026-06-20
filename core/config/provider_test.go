package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStaticProviderLoad(t *testing.T) {
	provider := StaticProvider[testConfig]{Value: testConfig{Name: "static"}}
	got, err := provider.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "static" {
		t.Fatalf("config name = %q, want static", got.Name)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load canceled error = %v, want context.Canceled", err)
	}
}

func TestFileProviderLoadValidateAndMustLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"file"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := NewFileProvider[testConfig](path, WithValidator(func(cfg testConfig) error {
		if cfg.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	got, err := provider.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "file" {
		t.Fatalf("config name = %q, want file", got.Name)
	}
	if got := MustLoad[testConfig](provider); got.Name != "file" {
		t.Fatalf("MustLoad name = %q, want file", got.Name)
	}

	if err := os.WriteFile(path, []byte(`{"name":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Load(context.Background()); err == nil {
		t.Fatal("Load invalid config succeeded, want validation error")
	}
}

func TestFileProviderLoadOptionsApplyToLoadAndWatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"${APP_NAME}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_NAME", "orders")
	provider := FileProvider[testConfig]{Path: path, Interval: time.Millisecond, Options: []LoadOption{WithEnvExpansion()}}
	loaded, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "orders" {
		t.Fatalf("loaded name = %q, want expanded env", loaded.Name)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan testConfig, 1)
	done := make(chan error, 1)
	go func() {
		err := provider.Watch(ctx, func(next testConfig) { updates <- next })
		if !errors.Is(err, context.Canceled) {
			done <- err
			return
		}
		done <- nil
	}()
	time.Sleep(2 * time.Millisecond)
	t.Setenv("APP_NAME", "billing")
	if err := os.WriteFile(path, []byte(`{"name":"${APP_NAME}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-updates:
		if got.Name != "billing" {
			t.Fatalf("watch update name = %q, want expanded env", got.Name)
		}
	case err := <-done:
		t.Fatalf("Watch returned early: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch update")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNewFileProviderAppliesLoadOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"${APP_NAME}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_NAME", "orders")
	provider := NewFileProvider[testConfig](path, WithLoadOptions[testConfig](WithEnvExpansion()))
	loaded, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "orders" {
		t.Fatalf("loaded name = %q, want expanded env", loaded.Name)
	}
}

func TestEnvProviderAndCompositeProvider(t *testing.T) {
	t.Setenv("GOFLY_TEST_CONFIG", `{"name":"env"}`)
	env := NewEnvProvider[testConfig]("GOFLY_TEST_CONFIG", WithValidator(func(cfg testConfig) error {
		if cfg.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	got, err := env.Load(context.Background())
	if err != nil {
		t.Fatalf("env Load: %v", err)
	}
	if got.Name != "env" {
		t.Fatalf("env name = %q, want env", got.Name)
	}

	composite := NewCompositeProvider[testConfig](
		ProviderFunc[testConfig](func(context.Context) (testConfig, error) {
			return testConfig{}, errors.New("first unavailable")
		}),
		env,
	)
	got, err = composite.Load(context.Background())
	if err != nil {
		t.Fatalf("composite Load: %v", err)
	}
	if got.Name != "env" {
		t.Fatalf("composite name = %q, want env fallback", got.Name)
	}
}

func TestEnvProviderLoadBranches(t *testing.T) {
	// empty env
	provider := NewEnvProvider[testConfig]("GOFLY_MISSING_ENV_VAR_999")
	if _, err := provider.Load(context.Background()); err == nil {
		t.Fatal("expected error for empty env")
	}

	// invalid JSON
	t.Setenv("GOFLY_BAD_JSON", `not-json`)
	provider2 := NewEnvProvider[testConfig]("GOFLY_BAD_JSON")
	if _, err := provider2.Load(context.Background()); err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	// no validator path
	t.Setenv("GOFLY_NO_VALIDATOR", `{"name":"ok"}`)
	provider3 := NewEnvProvider[testConfig]("GOFLY_NO_VALIDATOR")
	got, err := provider3.Load(context.Background())
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if got.Name != "ok" {
		t.Fatalf("name = %q, want ok", got.Name)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider3.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load canceled error = %v, want context.Canceled", err)
	}
}

func TestCompositeProviderAllFailAndNoProviders(t *testing.T) {
	// all providers fail
	composite := NewCompositeProvider[testConfig](
		ProviderFunc[testConfig](func(context.Context) (testConfig, error) {
			return testConfig{}, errors.New("first fail")
		}),
		ProviderFunc[testConfig](func(context.Context) (testConfig, error) {
			return testConfig{}, errors.New("second fail")
		}),
	)
	if _, err := composite.Load(context.Background()); err == nil {
		t.Fatal("expected error when all providers fail")
	}

	// no providers
	empty := NewCompositeProvider[testConfig]()
	if _, err := empty.Load(context.Background()); err == nil {
		t.Fatal("expected error for empty composite")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := composite.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load canceled error = %v, want context.Canceled", err)
	}

	filtered := NewCompositeProvider[testConfig](nil, StaticProvider[testConfig]{Value: testConfig{Name: "static"}}, nil)
	if len(filtered.Providers) != 1 {
		t.Fatalf("filtered providers = %d, want 1", len(filtered.Providers))
	}
}

func TestFileProviderAndMustLoadErrorBoundaries_BitsUT(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := NewFileProvider[testConfig](filepath.Join(t.TempDir(), "missing.json"))
	if _, err := provider.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("FileProvider.Load canceled error = %v, want context.Canceled", err)
	}
	if _, err := provider.Load(context.Background()); err == nil {
		t.Fatal("FileProvider.Load missing file succeeded, want error")
	}

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("MustLoad missing provider did not panic")
		}
	}()
	_ = MustLoad[testConfig](provider)
}

func TestFileProviderWatchSkipsInvalidUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := writeConfigAtomically(path, []byte(`{"name":"old"}`)); err != nil {
		t.Fatal(err)
	}
	provider := NewFileProvider[testConfig](path, WithValidator(func(cfg testConfig) error {
		if cfg.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	provider.Interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan testConfig, 2)
	errCh := make(chan error, 1)
	go func() {
		err := provider.Watch(ctx, func(next testConfig) { updates <- next })
		if !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()

	time.Sleep(2 * time.Millisecond)
	if err := writeConfigAtomically(path, []byte(`{"name":""}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Millisecond)
	select {
	case got := <-updates:
		t.Fatalf("invalid update should be skipped, got %+v", got)
	default:
	}
	if err := writeConfigAtomically(path, []byte(`{"name":"new"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-updates:
		if got.Name != "new" {
			t.Fatalf("watched name = %q, want new", got.Name)
		}
	case err := <-errCh:
		t.Fatalf("Watch returned error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for valid watched update")
	}
}
