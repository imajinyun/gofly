package config

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeSource is an in-memory RemoteSource used to exercise RemoteProvider
// without any external dependency.
type fakeSource struct {
	data    []byte
	getErr  error
	updates chan RemoteValue
}

func newFakeSource(data string) *fakeSource {
	return &fakeSource{data: []byte(data), updates: make(chan RemoteValue, 4)}
}

func (s *fakeSource) Get(ctx context.Context) (RemoteValue, error) {
	if s.getErr != nil {
		return RemoteValue{}, s.getErr
	}
	return RemoteValue{Key: "k", Data: s.data}, nil
}

func (s *fakeSource) Watch(ctx context.Context, onChange func(RemoteValue)) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case v := <-s.updates:
			onChange(v)
		}
	}
}

func (s *fakeSource) Close() error { return nil }

func TestRemoteProviderLoadAndValidate(t *testing.T) {
	if _, err := NewRemoteProvider[testConfig](nil); err == nil {
		t.Fatal("NewRemoteProvider(nil) should error")
	}

	src := newFakeSource(`{"name":"remote"}`)
	p, err := NewRemoteProvider[testConfig](src, WithRemoteValidator[testConfig](func(c testConfig) error {
		if c.Name == "" {
			return errors.New("name required")
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewRemoteProvider: %v", err)
	}

	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "remote" {
		t.Fatalf("Load name = %q, want remote", got.Name)
	}

	// Validator rejects empty payloads.
	src.data = []byte(`{"name":""}`)
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("Load should fail validation for empty name")
	}

	// Get errors propagate.
	src.getErr = errors.New("boom")
	if _, err := p.Load(context.Background()); err == nil {
		t.Fatal("Load should propagate source error")
	}
}

func TestRemoteProviderWatch(t *testing.T) {
	src := newFakeSource(`{"name":"v1"}`)
	p, err := NewRemoteProvider[testConfig](src)
	if err != nil {
		t.Fatalf("NewRemoteProvider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changes := make(chan testConfig, 2)
	go func() { _ = p.Watch(ctx, func(c testConfig) { changes <- c }) }()

	src.updates <- RemoteValue{Key: "k", Data: []byte(`{"name":"v2"}`)}
	select {
	case c := <-changes:
		if c.Name != "v2" {
			t.Fatalf("watch name = %q, want v2", c.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watch update")
	}

	// Malformed payload must be dropped, not forwarded.
	src.updates <- RemoteValue{Key: "k", Data: []byte(`not-json`)}
	src.updates <- RemoteValue{Key: "k", Data: []byte(`{"name":"v3"}`)}
	select {
	case c := <-changes:
		if c.Name != "v3" {
			t.Fatalf("watch name = %q, want v3 (malformed skipped)", c.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for v3 update")
	}
}

func TestRemoteProviderAsWatchProvider(t *testing.T) {
	src := newFakeSource(`{"name":"x"}`)
	p, err := NewRemoteProvider[testConfig](src)
	if err != nil {
		t.Fatalf("NewRemoteProvider: %v", err)
	}
	var _ Provider[testConfig] = p
	var _ WatchProvider[testConfig] = p
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRemoteProviderWithDecoder(t *testing.T) {
	customDecoder := func(data []byte) (testConfig, error) {
		return testConfig{Name: "custom"}, nil
	}
	src := newFakeSource(`{"name":"ignored"}`)
	p, err := NewRemoteProvider[testConfig](src, WithDecoder(customDecoder))
	if err != nil {
		t.Fatalf("NewRemoteProvider: %v", err)
	}
	got, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != "custom" {
		t.Fatalf("name = %q, want custom", got.Name)
	}

	// nil decoder should be ignored (default JSON used)
	src2 := newFakeSource(`{"name":"default"}`)
	p2, err := NewRemoteProvider[testConfig](src2, WithDecoder[testConfig](nil))
	if err != nil {
		t.Fatalf("NewRemoteProvider: %v", err)
	}
	got2, err := p2.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got2.Name != "default" {
		t.Fatalf("name = %q, want default", got2.Name)
	}
}

func TestMemorySourceGetWatchSetAndClose(t *testing.T) {
	source := NewMemorySource("app", []byte(`{"name":"v1"}`))
	got, err := source.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Key != "app" || string(got.Data) != `{"name":"v1"}` || got.Version != 1 {
		t.Fatalf("initial remote value = %+v", got)
	}
	got.Data[0] = 'x'
	stable, err := source.Get(context.Background())
	if err != nil {
		t.Fatalf("Get stable: %v", err)
	}
	if string(stable.Data) != `{"name":"v1"}` {
		t.Fatalf("Get exposed internal data = %q", stable.Data)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan RemoteValue, 1)
	errCh := make(chan error, 1)
	go func() {
		err := source.Watch(ctx, func(value RemoteValue) { updates <- value })
		if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrRemoteSourceClosed) {
			errCh <- err
		}
	}()
	for deadline := time.Now().Add(time.Second); source.WatcherCount() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if source.WatcherCount() == 0 {
		t.Fatal("watcher was not registered")
	}
	if err := source.Set(context.Background(), []byte(`{"name":"v2"}`)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	select {
	case value := <-updates:
		if string(value.Data) != `{"name":"v2"}` || value.Version != 2 {
			t.Fatalf("watch value = %+v", value)
		}
	case err := <-errCh:
		t.Fatalf("Watch returned error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for memory source update")
	}
	if err := source.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := source.Get(context.Background()); !errors.Is(err, ErrRemoteSourceClosed) {
		t.Fatalf("closed Get error = %v, want ErrRemoteSourceClosed", err)
	}
}
