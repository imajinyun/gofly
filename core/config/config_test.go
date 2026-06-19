package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofly/gofly/app"
)

type testConfig struct {
	Name string `json:"name" yaml:"name"`
}

func TestLoadSupportsYAMLAndEnvironmentExpansion(t *testing.T) {
	t.Setenv("GOFLY_CONFIG_NAME", "yaml-service")
	path := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(path, []byte("name: ${GOFLY_CONFIG_NAME}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var got testConfig
	if err := Load(path, &got, WithEnvExpansion()); err != nil {
		t.Fatalf("Load yaml with env expansion: %v", err)
	}
	if got.Name != "yaml-service" {
		t.Fatalf("Name = %q, want yaml-service", got.Name)
	}
}

func TestLoadSupportsTOMLServiceConf(t *testing.T) {
	t.Setenv("GOFLY_SERVICE_NAME", "toml-orders")
	path := filepath.Join(t.TempDir(), "app.toml")
	data := `
[service]
name = "${GOFLY_SERVICE_NAME}"
environment = "production"
startupTimeout = 5000000000

[service.log]
level = "info"
format = "json"
trace = true

[service.metrics]
enabled = true

[service.governance]
timeout = 3000000000
maxConcurrency = 64

[service.governance.rateLimit]
rate = 100
burst = 120
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Service app.ServiceConf `json:"service"`
	}
	if err := Load(path, &got, WithEnvExpansion(), WithStrictFields()); err != nil {
		t.Fatalf("Load toml service conf: %v", err)
	}
	service := got.Service.WithDefaults("")
	if service.Name != "toml-orders" || service.Environment != "production" || !service.Metrics.Enabled {
		t.Fatalf("service conf identity/metrics = %+v", service)
	}
	if service.Governance.Timeout != 3*time.Second || service.Governance.MaxConcurrency != 64 || service.Governance.RateLimit.Rate != 100 || service.Governance.RateLimit.Burst != 120 {
		t.Fatalf("service governance = %+v", service.Governance)
	}
}

func TestLoadReadsExplicitConfigPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "operator-config.json")
	if err := os.WriteFile(path, []byte(`{"name":"explicit"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var got testConfig
	if err := Load(path, &got); err != nil {
		t.Fatalf("Load explicit config path: %v", err)
	}
	if got.Name != "explicit" {
		t.Fatalf("Name = %q, want explicit", got.Name)
	}
}

func TestLoadStrictFieldsRejectsUnknownJSONField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"api","extra":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var got testConfig
	if err := Load(path, &got, WithStrictFields()); err == nil {
		t.Fatal("Load strict config succeeded, want unknown field error")
	}
}

func TestLoadValidatorRejectsDecodedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var got testConfig
	err := Load(path, &got, WithLoadValidator(func(cfg testConfig) error {
		if cfg.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	if err == nil || !strings.Contains(err.Error(), "validate config") || !strings.Contains(err.Error(), "name required") {
		t.Fatalf("Load with validator error = %v, want validation context", err)
	}
}

func TestLoadRejectsUnsupportedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.ini")
	if err := os.WriteFile(path, []byte(`name = api`), 0o644); err != nil {
		t.Fatal(err)
	}

	var got testConfig
	if err := Load(path, &got); err == nil {
		t.Fatal("Load unsupported config format succeeded, want error")
	}
}

func TestManagerReloadSubscribeAndSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager[testConfig](path, WithValidator(func(cfg testConfig) error {
		if cfg.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := m.Current().Name; got != "old" {
		t.Fatalf("Current().Name = %q, want old", got)
	}
	updates, unsubscribe := m.Subscribe(2)
	defer unsubscribe()
	if got := (<-updates).Name; got != "old" {
		t.Fatalf("initial subscription value = %q, want old", got)
	}
	if err := os.WriteFile(path, []byte(`{"name":"new"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	select {
	case got := <-updates:
		if got.Name != "new" {
			t.Fatalf("subscription update = %q, want new", got.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription update")
	}
	snapshot := m.Snapshot()
	if snapshot.Version != 2 || snapshot.Config.Name != "new" || snapshot.Subscribers != 1 || snapshot.LastError != "" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestManagerAppliesLoadOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"${APP_NAME}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_NAME", "orders")
	m, err := NewManager[testConfig](path, WithLoadOptions[testConfig](WithEnvExpansion()))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Current().Name; got != "orders" {
		t.Fatalf("current name = %q, want expanded env", got)
	}
}

func TestManagerWatchAppliesLoadOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"initial"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_NAME", "watched")
	m, err := NewManager[testConfig](path, WithLoadOptions[testConfig](WithEnvExpansion()))
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := m.Subscribe(4)
	defer unsubscribe()
	<-updates
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		err := m.Watch(ctx, time.Millisecond)
		if !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	time.Sleep(2 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`{"name":"${APP_NAME}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-updates:
		if got.Name != "watched" {
			t.Fatalf("watched name = %q, want expanded env", got.Name)
		}
	case err := <-errCh:
		t.Fatalf("Watch returned error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for expanded watch update")
	}
}

func TestManagerWatchProviderUsesCallerInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"initial"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManagerFromProvider[testConfig](NewFileProvider[testConfig](path))
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := m.Subscribe(4)
	defer unsubscribe()
	<-updates
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		err := m.Watch(ctx, time.Millisecond)
		if !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	time.Sleep(2 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`{"name":"provider-watch"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-updates:
		if got.Name != "provider-watch" {
			t.Fatalf("provider watched name = %q, want provider-watch", got.Name)
		}
	case err := <-errCh:
		t.Fatalf("Watch returned error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider watch update")
	}
}

func TestManagerWatchProviderRecordsApplyError(t *testing.T) {
	provider := &pushWatchProvider[testConfig]{value: testConfig{Name: "initial"}}
	m, err := NewManagerFromProvider[testConfig](provider, WithValidator(func(cfg testConfig) error {
		if cfg.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		err := m.Watch(ctx, time.Millisecond)
		if !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	provider.push(testConfig{})
	deadline := time.After(time.Second)
	for {
		if snapshot := m.Snapshot(); snapshot.LastError != "" && strings.Contains(snapshot.LastError, "name required") {
			return
		}
		select {
		case err := <-errCh:
			t.Fatalf("Watch returned error: %v", err)
		case <-deadline:
			t.Fatalf("timed out waiting for apply error, snapshot=%+v", m.Snapshot())
		case <-time.After(time.Millisecond):
		}
	}
}

func TestManagerValidationFailureKeepsPreviousConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"valid"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager[testConfig](path, WithValidator(func(cfg testConfig) error {
		if cfg.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"name":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(context.Background()); err == nil {
		t.Fatal("Reload invalid config succeeded, want error")
	}
	snapshot := m.Snapshot()
	if snapshot.Config.Name != "valid" || snapshot.Version != 1 || snapshot.LastError == "" {
		t.Fatalf("invalid reload should retain previous config: %+v", snapshot)
	}
}

func TestManagerWatchFanout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(path, []byte(`{"name":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager[testConfig](path)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	updates, unsubscribe := m.Subscribe(4)
	defer unsubscribe()
	<-updates
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		err := m.Watch(ctx, time.Millisecond)
		if !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	time.Sleep(2 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`{"name":"watched"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-updates:
		if got.Name != "watched" {
			t.Fatalf("watched update = %q, want watched", got.Name)
		}
	case err := <-errCh:
		t.Fatalf("watch returned error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watched update")
	}
}

func TestManagerFromProviderReloadsCompositeProvider(t *testing.T) {
	t.Setenv("GOFLY_PROVIDER_MANAGER", `{"name":"env-old"}`)
	provider := NewCompositeProvider[testConfig](
		ProviderFunc[testConfig](func(context.Context) (testConfig, error) {
			return testConfig{}, errors.New("primary unavailable")
		}),
		NewEnvProvider[testConfig]("GOFLY_PROVIDER_MANAGER"),
	)
	m, err := NewManagerFromProvider[testConfig](provider)
	if err != nil {
		t.Fatalf("NewManagerFromProvider: %v", err)
	}
	if got := m.Current().Name; got != "env-old" {
		t.Fatalf("Current().Name = %q, want env-old", got)
	}
	updates, unsubscribe := m.Subscribe(2)
	defer unsubscribe()
	<-updates

	t.Setenv("GOFLY_PROVIDER_MANAGER", `{"name":"env-new"}`)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("Reload provider: %v", err)
	}
	select {
	case got := <-updates:
		if got.Name != "env-new" {
			t.Fatalf("provider update = %q, want env-new", got.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider update")
	}
	if snapshot := m.Snapshot(); snapshot.Path != "provider" || snapshot.Version != 2 || snapshot.Config.Name != "env-new" {
		t.Fatalf("snapshot = %+v, want provider env-new v2", snapshot)
	}
}

func TestManagerWatchUsesNativeWatchProvider(t *testing.T) {
	source := NewMemorySource("app", []byte(`{"name":"old"}`))
	provider, err := NewRemoteProvider[testConfig](source)
	if err != nil {
		t.Fatalf("NewRemoteProvider: %v", err)
	}
	m, err := NewManagerFromProvider[testConfig](provider, WithValidator(func(cfg testConfig) error {
		if cfg.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewManagerFromProvider: %v", err)
	}
	updates, unsubscribe := m.Subscribe(4)
	defer unsubscribe()
	<-updates

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		err := m.Watch(ctx, time.Hour)
		if !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()
	for deadline := time.Now().Add(time.Second); source.WatcherCount() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if source.WatcherCount() == 0 {
		t.Fatal("native watch provider did not register watcher")
	}

	if err := source.Set(context.Background(), []byte(`{"name":"new"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-updates:
		if got.Name != "new" {
			t.Fatalf("native watch update = %q, want new", got.Name)
		}
	case err := <-errCh:
		t.Fatalf("watch returned error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for native watch update")
	}

	if err := source.Set(context.Background(), []byte(`{"name":""}`)); err != nil {
		t.Fatal(err)
	}
	var snapshot Snapshot[testConfig]
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		snapshot = m.Snapshot()
		if snapshot.LastError != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if snapshot.Config.Name != "new" || snapshot.Version != 2 || snapshot.LastError == "" {
		t.Fatalf("invalid native update snapshot = %+v, want previous config and last error", snapshot)
	}
}

func TestWatchReloadsChangedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	if err := writeConfigAtomically(path, []byte(`{"name":"old"}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := make(chan testConfig, 1)
	errCh := make(chan error, 1)
	go func() {
		err := Watch[testConfig](ctx, path, time.Millisecond, func(next testConfig) {
			changed <- next
		})
		if !errors.Is(err, context.Canceled) {
			errCh <- err
		}
	}()

	time.Sleep(2 * time.Millisecond)
	if err := writeConfigAtomically(path, []byte(`{"name":"new"}`)); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-changed:
		if got.Name != "new" {
			t.Fatalf("changed config name = %q, want new", got.Name)
		}
	case err := <-errCh:
		t.Fatalf("watch returned error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for config change")
	}
}

func TestParseTOMLValueBranches(t *testing.T) {
	cases := []struct {
		input string
		want  any
	}{
		{`"hello"`, "hello"},
		{"true", true},
		{"false", false},
		{"[1, 2, 3]", []any{int64(1), int64(2), int64(3)}},
		{"3.14", 3.14},
		{"42", int64(42)},
	}
	for _, c := range cases {
		got, err := parseTOMLValue(c.input)
		if err != nil {
			t.Fatalf("parseTOMLValue(%q) error: %v", c.input, err)
		}
		// Compare slices specially
		if wantSlice, ok := c.want.([]any); ok {
			gotSlice, ok2 := got.([]any)
			if !ok2 || len(gotSlice) != len(wantSlice) {
				t.Fatalf("parseTOMLValue(%q) = %v, want %v", c.input, got, c.want)
			}
			for i := range wantSlice {
				if gotSlice[i] != wantSlice[i] {
					t.Fatalf("parseTOMLValue(%q)[%d] = %v, want %v", c.input, i, gotSlice[i], wantSlice[i])
				}
			}
			continue
		}
		if got != c.want {
			t.Fatalf("parseTOMLValue(%q) = %v, want %v", c.input, got, c.want)
		}
	}

	// error paths
	if _, err := parseTOMLValue(""); err == nil {
		t.Fatal("expected error for empty value")
	}
	if _, err := parseTOMLValue("unsupported"); err == nil {
		t.Fatal("expected error for unsupported value")
	}
}

func TestParseTOMLArrayBranches(t *testing.T) {
	// empty array
	got, err := parseTOMLArray("")
	if err != nil {
		t.Fatalf("parseTOMLArray empty error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("parseTOMLArray empty = %v, want empty", got)
	}

	// nested array with strings and ints
	got, err = parseTOMLArray(`"a", "b", 1`)
	if err != nil {
		t.Fatalf("parseTOMLArray mixed error: %v", err)
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != int64(1) {
		t.Fatalf("parseTOMLArray mixed = %v, want [a b 1]", got)
	}

	// invalid element inside array
	if _, err := parseTOMLArray("bad, unsupported"); err == nil {
		t.Fatal("expected error for invalid array element")
	}
}

func writeConfigAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

type pushWatchProvider[T any] struct {
	value   T
	mu      sync.Mutex
	changes chan T
}

func (p *pushWatchProvider[T]) Load(context.Context) (T, error) {
	return p.value, nil
}

func (p *pushWatchProvider[T]) Watch(ctx context.Context, onChange func(T)) error {
	changes := p.ensureChanges()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case next := <-changes:
			if onChange != nil {
				onChange(next)
			}
		}
	}
}

func (p *pushWatchProvider[T]) push(next T) {
	p.ensureChanges() <- next
}

func (p *pushWatchProvider[T]) ensureChanges() chan T {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.changes == nil {
		p.changes = make(chan T, 4)
	}
	return p.changes
}
