// Package di provides a lightweight dependency-injection container with lazy
// singletons, type-keyed resolution and ordered lifecycle cleanup.
//
// Providers are registered with Provide and resolved with Resolve. Each provider
// is invoked at most once; the produced value is cached and returned on
// subsequent resolutions. Values implementing io.Closer (or di.Closer) are
// closed in reverse instantiation order when Container.Close is called.
package di

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
)

var (
	// ErrNotRegistered is returned when resolving a type that has no provider.
	ErrNotRegistered = errors.New("di: type not registered")
	// ErrCyclic is returned when a provider depends on itself transitively.
	ErrCyclic = errors.New("di: cyclic dependency")
	// ErrClosed is returned when using a closed container.
	ErrClosed = errors.New("di: container is closed")
)

// Closer is an optional interface a resolved value may implement to participate
// in container shutdown. io.Closer is also honoured.
type Closer interface {
	Close() error
}

// Provider builds a value of a given type, possibly resolving dependencies from
// the same container.
type Provider[T any] func(*Container) (T, error)

// Container holds registered providers and cached singletons.
type Container struct {
	mu        sync.Mutex
	providers map[reflect.Type]any
	instances map[reflect.Type]any
	building  map[reflect.Type]bool
	closers   []func() error
	closed    bool
}

// New returns an empty Container.
func New() *Container {
	return &Container{
		providers: make(map[reflect.Type]any),
		instances: make(map[reflect.Type]any),
		building:  make(map[reflect.Type]bool),
	}
}

// Provide registers a lazy singleton provider for type T. A later Provide for
// the same type overrides the previous one (as long as it has not yet been
// instantiated).
func Provide[T any](c *Container, provider Provider[T]) error {
	if provider == nil {
		return errors.New("di: provider is nil")
	}
	key := typeOf[T]()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if _, ok := c.instances[key]; ok {
		return fmt.Errorf("di: %s already instantiated", key)
	}
	c.providers[key] = provider
	return nil
}

// Supply registers an already-constructed value as the singleton for type T.
func Supply[T any](c *Container, value T) error {
	return Provide(c, func(*Container) (T, error) { return value, nil })
}

// Resolve returns the singleton for type T, instantiating it on first use.
// Providers may resolve their own dependencies re-entrantly; the container lock
// is released while a provider runs so the dependency graph can be built.
func Resolve[T any](c *Container) (T, error) {
	var zero T
	key := typeOf[T]()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return zero, ErrClosed
	}
	if inst, ok := c.instances[key]; ok {
		c.mu.Unlock()
		return inst.(T), nil
	}
	raw, ok := c.providers[key]
	if !ok {
		c.mu.Unlock()
		return zero, fmt.Errorf("%w: %s", ErrNotRegistered, key)
	}
	if c.building[key] {
		c.mu.Unlock()
		return zero, fmt.Errorf("%w: %s", ErrCyclic, key)
	}
	provider := raw.(Provider[T])
	c.building[key] = true
	c.mu.Unlock()

	value, err := provider(c)

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.building, key)
	if err != nil {
		return zero, fmt.Errorf("di: build %s: %w", key, err)
	}
	if c.closed {
		// Container was closed while building; close the orphan if possible.
		if closer := closerFor(value); closer != nil {
			_ = closer()
		}
		return zero, ErrClosed
	}
	// Another goroutine may have produced the instance concurrently; reuse it.
	if inst, ok := c.instances[key]; ok {
		if closer := closerFor(value); closer != nil {
			_ = closer()
		}
		return inst.(T), nil
	}
	c.instances[key] = value
	if closer := closerFor(value); closer != nil {
		c.closers = append(c.closers, closer)
	}
	return value, nil
}

// MustResolve is like Resolve but panics on error.
func MustResolve[T any](c *Container) T {
	value, err := Resolve[T](c)
	if err != nil {
		panic(err)
	}
	return value
}

// Close shuts down the container, closing resolved values that implement Closer
// or io.Closer in reverse instantiation order. It is safe to call once; further
// use of the container returns ErrClosed.
func (c *Container) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	closers := c.closers
	c.closers = nil
	c.mu.Unlock()

	var err error
	for i := len(closers) - 1; i >= 0; i-- {
		if cerr := closers[i](); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}
	return err
}

func closerFor(value any) func() error {
	switch v := value.(type) {
	case Closer:
		return v.Close
	default:
		return nil
	}
}

func typeOf[T any]() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}
