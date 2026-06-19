// Package config provides layered configuration loading with file, environment
// and remote backends, plus validation hooks.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	core "github.com/gofly/gofly/core"
)

// ErrRemoteSourceClosed is returned when a remote source has been closed.
var ErrRemoteSourceClosed = errors.New("config remote source is closed")

// RemoteValue is a single configuration payload fetched from a remote source.
type RemoteValue struct {
	// Key identifies the configuration entry (e.g. etcd key, nacos dataId).
	Key string
	// Data is the raw configuration bytes.
	Data []byte
	// Version is an opaque revision indicator when the backend provides one.
	Version int64
}

// RemoteSource abstracts a remote configuration backend (etcd, consul, nacos…).
//
// Implementations live in dedicated sub-packages so that core/config stays free
// of heavy third-party SDK dependencies. A source only deals with raw bytes; the
// generic RemoteProvider is responsible for decoding into a typed value.
type RemoteSource interface {
	// Get fetches the current configuration payload.
	Get(context.Context) (RemoteValue, error)
	// Watch blocks until ctx is cancelled, invoking onChange for every update.
	// Implementations should be event-driven (long-lived watch / blocking query)
	// rather than polling whenever the backend supports it.
	Watch(ctx context.Context, onChange func(RemoteValue)) error
	// Close releases the underlying connection.
	Close() error
}

// MemorySource is an in-memory RemoteSource useful for tests, local
// development, and embedding a lightweight dynamic config center in examples.
type MemorySource struct {
	mu       sync.RWMutex
	key      string
	value    RemoteValue
	watchers map[chan RemoteValue]struct{}
	closed   bool
}

// NewMemorySource creates an in-memory remote source with an initial payload.
func NewMemorySource(key string, data []byte) *MemorySource {
	value := RemoteValue{Key: key, Data: cloneBytes(data), Version: 1}
	return &MemorySource{key: key, value: value, watchers: make(map[chan RemoteValue]struct{})}
}

// Get fetches the current in-memory configuration value.
func (s *MemorySource) Get(ctx context.Context) (RemoteValue, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return RemoteValue{}, err
	}
	if s == nil {
		return RemoteValue{}, ErrRemoteSourceClosed
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return RemoteValue{}, ErrRemoteSourceClosed
	}
	return cloneRemoteValue(s.value), nil
}

// Set updates the payload, increments its version, and fan-outs the update to
// active watchers without blocking slow consumers.
func (s *MemorySource) Set(ctx context.Context, data []byte) error {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return ErrRemoteSourceClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrRemoteSourceClosed
	}
	s.value = RemoteValue{Key: s.key, Data: cloneBytes(data), Version: s.value.Version + 1}
	for ch := range s.watchers {
		latest := cloneRemoteValue(s.value)
		select {
		case ch <- latest:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- latest:
			default:
			}
		}
	}
	return nil
}

// WatcherCount returns the number of active watchers.
func (s *MemorySource) WatcherCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.watchers)
}

// Watch blocks until ctx is cancelled or the source is closed.
func (s *MemorySource) Watch(ctx context.Context, onChange func(RemoteValue)) error {
	ctx = core.Context(ctx)
	if s == nil {
		return ErrRemoteSourceClosed
	}
	ch := make(chan RemoteValue, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrRemoteSourceClosed
	}
	s.watchers[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if _, ok := s.watchers[ch]; ok {
			delete(s.watchers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case value, ok := <-ch:
			if !ok {
				return ErrRemoteSourceClosed
			}
			if onChange != nil {
				onChange(cloneRemoteValue(value))
			}
		}
	}
}

// Close releases all watchers.
func (s *MemorySource) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for ch := range s.watchers {
		delete(s.watchers, ch)
		close(ch)
	}
	return nil
}

// Decoder turns raw remote bytes into a typed configuration value.
type Decoder[T any] func([]byte) (T, error)

// JSONDecoder decodes the payload as JSON. It is the default decoder.
func JSONDecoder[T any]() Decoder[T] {
	return func(data []byte) (T, error) {
		var value T
		if err := json.Unmarshal(data, &value); err != nil {
			return value, fmt.Errorf("decode config: %w", err)
		}
		return value, nil
	}
}

// RemoteProvider adapts a RemoteSource into a typed Provider/WatchProvider.
type RemoteProvider[T any] struct {
	source    RemoteSource
	decoder   Decoder[T]
	validator Validator[T]
}

// RemoteProviderOption customises a RemoteProvider.
type RemoteProviderOption[T any] func(*RemoteProvider[T])

// WithDecoder overrides the default JSON decoder.
func WithDecoder[T any](decoder Decoder[T]) RemoteProviderOption[T] {
	return func(p *RemoteProvider[T]) {
		if decoder != nil {
			p.decoder = decoder
		}
	}
}

// WithRemoteValidator validates every decoded value before it is published.
func WithRemoteValidator[T any](validator Validator[T]) RemoteProviderOption[T] {
	return func(p *RemoteProvider[T]) {
		p.validator = validator
	}
}

// NewRemoteProvider builds a RemoteProvider over the given source.
func NewRemoteProvider[T any](source RemoteSource, opts ...RemoteProviderOption[T]) (*RemoteProvider[T], error) {
	if source == nil {
		return nil, fmt.Errorf("config remote source is nil")
	}
	p := &RemoteProvider[T]{source: source, decoder: JSONDecoder[T]()}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p, nil
}

// Load implements Provider.
func (p *RemoteProvider[T]) Load(ctx context.Context) (T, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	value, err := p.source.Get(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	return p.decode(value.Data)
}

// Watch implements WatchProvider. It blocks until ctx is cancelled, decoding and
// validating every update before invoking onChange.
func (p *RemoteProvider[T]) Watch(ctx context.Context, onChange func(T)) error {
	ctx = core.Context(ctx)
	return p.source.Watch(ctx, func(raw RemoteValue) {
		value, err := p.decode(raw.Data)
		if err != nil {
			return
		}
		if onChange != nil {
			onChange(value)
		}
	})
}

// Close releases the underlying source.
func (p *RemoteProvider[T]) Close() error {
	return p.source.Close()
}

func (p *RemoteProvider[T]) decode(data []byte) (T, error) {
	value, err := p.decoder(data)
	if err != nil {
		return value, err
	}
	if p.validator != nil {
		if err := p.validator(value); err != nil {
			return value, fmt.Errorf("validate config: %w", err)
		}
	}
	return value, nil
}

func cloneRemoteValue(value RemoteValue) RemoteValue {
	value.Data = cloneBytes(value.Data)
	return value
}

func cloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return append([]byte(nil), data...)
}

var (
	_ Provider[struct{}]      = (*RemoteProvider[struct{}])(nil)
	_ WatchProvider[struct{}] = (*RemoteProvider[struct{}])(nil)
)
