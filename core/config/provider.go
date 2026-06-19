// Package config provides layered configuration loading with file, environment
// and remote backends, plus validation hooks.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	core "github.com/gofly/gofly/core"
)

// Provider loads a typed configuration value.
type Provider[T any] interface {
	Load(context.Context) (T, error)
}

// WatchProvider is a Provider that can also watch for changes.
type WatchProvider[T any] interface {
	Provider[T]
	Watch(context.Context, func(T)) error
}

// IntervalWatchProvider watches for changes on a fixed polling interval.
type IntervalWatchProvider[T any] interface {
	Provider[T]
	WatchInterval(context.Context, time.Duration, func(T)) error
}

// StaticProvider is a Provider that returns a fixed value.
type StaticProvider[T any] struct {
	Value T
}

// ProviderFunc adapts a function to a Provider.
type ProviderFunc[T any] func(context.Context) (T, error)

// EnvProvider loads configuration from an environment variable.
type EnvProvider[T any] struct {
	Name      string
	Validator Validator[T]
}

// CompositeProvider tries each provider in order and returns the first success.
type CompositeProvider[T any] struct {
	Providers []Provider[T]
}

func (f ProviderFunc[T]) Load(ctx context.Context) (T, error) { return f(ctx) }

func NewEnvProvider[T any](name string, opts ...ManagerOption[T]) EnvProvider[T] {
	m := &Manager[T]{}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return EnvProvider[T]{Name: name, Validator: m.validator}
}

func NewCompositeProvider[T any](providers ...Provider[T]) CompositeProvider[T] {
	out := make([]Provider[T], 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			out = append(out, provider)
		}
	}
	return CompositeProvider[T]{Providers: out}
}

func (p StaticProvider[T]) Load(ctx context.Context) (T, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	return p.Value, nil
}

func (p EnvProvider[T]) Load(ctx context.Context) (T, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	data := os.Getenv(p.Name)
	if data == "" {
		var zero T
		return zero, fmt.Errorf("environment variable %q is empty", p.Name)
	}
	var value T
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return value, fmt.Errorf("decode environment variable %q: %w", p.Name, err)
	}
	if p.Validator != nil {
		if err := p.Validator(value); err != nil {
			return value, fmt.Errorf("validate config: %w", err)
		}
	}
	return value, nil
}

func (p CompositeProvider[T]) Load(ctx context.Context) (T, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	var errs []error
	for _, provider := range p.Providers {
		if provider == nil {
			continue
		}
		value, err := provider.Load(ctx)
		if err == nil {
			return value, nil
		}
		errs = append(errs, err)
	}
	var zero T
	if len(errs) == 0 {
		return zero, errors.New("no config providers configured")
	}
	return zero, errors.Join(errs...)
}

type FileProvider[T any] struct {
	Path      string
	Interval  time.Duration
	Validator Validator[T]
	Options   []LoadOption
}

func NewFileProvider[T any](path string, opts ...ManagerOption[T]) FileProvider[T] {
	m := &Manager[T]{}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return FileProvider[T]{Path: path, Validator: m.validator, Options: append([]LoadOption(nil), m.loadOptions...)}
}

func (p FileProvider[T]) Load(ctx context.Context) (T, error) {
	ctx = core.Context(ctx)
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	var value T
	if err := Load(p.Path, &value, p.Options...); err != nil {
		return value, err
	}
	if p.Validator != nil {
		if err := p.Validator(value); err != nil {
			return value, fmt.Errorf("validate config: %w", err)
		}
	}
	return value, nil
}

func (p FileProvider[T]) Watch(ctx context.Context, onChange func(T)) error {
	return p.WatchInterval(ctx, p.Interval, onChange)
}

func (p FileProvider[T]) WatchInterval(ctx context.Context, interval time.Duration, onChange func(T)) error {
	ctx = core.Context(ctx)
	if interval <= 0 {
		interval = time.Second
	}
	return Watch[T](ctx, p.Path, interval, func(next T) {
		if p.Validator != nil && p.Validator(next) != nil {
			return
		}
		if onChange != nil {
			onChange(next)
		}
	}, p.Options...)
}

func MustLoad[T any](provider Provider[T]) T {
	value, err := provider.Load(context.Background())
	if err != nil {
		panic(err)
	}
	return value
}
