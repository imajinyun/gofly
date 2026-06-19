package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gofly/gofly/core/breaker"
)

// ErrCircuitOpen is returned when a provider circuit breaker is open and
// requests are being rejected without calling the upstream provider.
var ErrCircuitOpen = errors.New("llm circuit breaker is open")

// ErrNoProviderAvailable is returned when all providers in a failover chain
// have been exhausted without a successful response.
var ErrNoProviderAvailable = errors.New("llm no provider available after failover")

// CircuitBreakerProvider wraps a Provider with a circuit breaker. When the
// circuit is open, Complete/Stream/Embed return ErrCircuitOpen immediately
// without invoking the inner provider. After the open timeout elapses, a
// single probe (HalfOpen state) is allowed; success closes the circuit and
// failure reopens it.
type CircuitBreakerProvider struct {
	inner   Provider
	breaker *breaker.Breaker
}

// NewCircuitBreakerProvider wraps inner with a circuit breaker. Default
// thresholds: 5 consecutive failures to open, 5-second cooldown before
// half-open probe. Pass breaker options to override.
func NewCircuitBreakerProvider(inner Provider, opts ...breaker.Option) *CircuitBreakerProvider {
	if inner == nil {
		inner = NoOpProvider{}
	}
	breakerOpts := []breaker.Option{
		breaker.WithFailureThreshold(5),
		breaker.WithOpenTimeout(5 * time.Second),
	}
	breakerOpts = append(breakerOpts, opts...)
	return &CircuitBreakerProvider{
		inner:   inner,
		breaker: breaker.New(breakerOpts...),
	}
}

// CircuitBreakerSnapshot returns the current circuit breaker state for
// observability and manifest reporting.
func (p *CircuitBreakerProvider) CircuitBreakerSnapshot() breaker.BreakerSnapshot {
	if p == nil || p.breaker == nil {
		return breaker.BreakerSnapshot{}
	}
	return p.breaker.Snapshot()
}

// Complete applies circuit breaker before delegating.
func (p *CircuitBreakerProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if err := p.breaker.Allow(); err != nil {
		return Response{}, fmt.Errorf("%w: %s", ErrCircuitOpen, p.circuitLabel())
	}
	resp, err := p.inner.Complete(ctx, req)
	p.recordOutcome(err)
	return resp, err
}

// Stream checks circuit state before opening the stream. Errors from the
// returned stream channel are tracked for circuit state transitions.
func (p *CircuitBreakerProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if err := p.breaker.Allow(); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrCircuitOpen, p.circuitLabel())
	}
	stream, err := p.inner.Stream(ctx, req)
	if err != nil {
		p.recordOutcome(err)
		return nil, err
	}
	return p.governStreamCB(stream), nil
}

func (p *CircuitBreakerProvider) governStreamCB(stream <-chan StreamEvent) <-chan StreamEvent {
	out := make(chan StreamEvent, 1)
	go func() {
		defer close(out)
		var hadErr bool
		for event := range stream {
			if event.Err != nil {
				hadErr = true
				p.recordOutcome(event.Err)
			}
			out <- event
		}
		if !hadErr {
			p.recordOutcome(nil) // mark success
		}
	}()
	return out
}

// Embed applies circuit breaker before delegating.
func (p *CircuitBreakerProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if err := p.breaker.Allow(); err != nil {
		return EmbedResponse{}, fmt.Errorf("%w: %s", ErrCircuitOpen, p.circuitLabel())
	}
	resp, err := p.inner.Embed(ctx, req)
	p.recordOutcome(err)
	return resp, err
}

func (p *CircuitBreakerProvider) recordOutcome(err error) {
	if err == nil {
		_ = p.breaker.Do(context.Background(), func() error { return nil }) // side-effect: calls accept
	} else {
		_ = p.breaker.Do(context.Background(), func() error { return err }) // side-effect: calls reject
	}
}

func (p *CircuitBreakerProvider) circuitLabel() string {
	snapshot := p.breaker.Snapshot()
	return fmt.Sprintf("provider circuit breaker is %s (%d/%d failures)", snapshot.State, snapshot.Failures, snapshot.FailureThreshold)
}

// FailoverProvider tries the primary provider first. If it returns a retryable
// error and fallback providers are registered, the next provider is tried.
// Non-retryable errors are returned immediately without attempting fallbacks.
type FailoverProvider struct {
	primary   Provider
	fallbacks []Provider
	specs     []ProviderSpec
	mu        sync.Mutex
	tryCount  int
}

// NewFailoverProvider creates a failover chain. The primary is always tried
// first. Fallbacks are tried in order when the primary (or previous fallback)
// returns a retryable error. Nil providers are silently replaced with NoOpProvider.
func NewFailoverProvider(primary Provider, fallbacks ...Provider) *FailoverProvider {
	if primary == nil {
		primary = NoOpProvider{}
	}
	cleaned := make([]Provider, 0, len(fallbacks))
	for _, fb := range fallbacks {
		if fb != nil {
			cleaned = append(cleaned, fb)
		}
	}
	return &FailoverProvider{
		primary:   primary,
		fallbacks: cleaned,
	}
}

// TryCount returns the number of provider attempts made by the last call.
func (p *FailoverProvider) TryCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tryCount
}

// Complete tries providers in order until one succeeds or all fail with
// non-retryable errors.
func (p *FailoverProvider) Complete(ctx context.Context, req Request) (Response, error) {
	p.mu.Lock()
	p.tryCount = 0
	p.mu.Unlock()

	resp, err := p.primary.Complete(ctx, req)
	p.mu.Lock()
	p.tryCount++
	p.mu.Unlock()
	if err == nil || !isRetryableProviderError(err) {
		return resp, err
	}
	lastErr := err
	for _, fb := range p.fallbacks {
		resp, err = fb.Complete(ctx, req)
		p.mu.Lock()
		p.tryCount++
		p.mu.Unlock()
		if err == nil || !isRetryableProviderError(err) {
			return resp, err
		}
		lastErr = err
	}
	return Response{}, fmt.Errorf("%w: %v", ErrNoProviderAvailable, lastErr)
}

// Stream tries providers in order until one successfully opens a stream.
func (p *FailoverProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	p.mu.Lock()
	p.tryCount = 0
	p.mu.Unlock()

	stream, err := p.primary.Stream(ctx, req)
	p.mu.Lock()
	p.tryCount++
	p.mu.Unlock()
	if err == nil || !isRetryableProviderError(err) {
		return stream, err
	}
	lastErr := err
	for _, fb := range p.fallbacks {
		stream, err = fb.Stream(ctx, req)
		p.mu.Lock()
		p.tryCount++
		p.mu.Unlock()
		if err == nil || !isRetryableProviderError(err) {
			return stream, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("%w: %v", ErrNoProviderAvailable, lastErr)
}

// Embed tries providers in order until one succeeds.
func (p *FailoverProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	p.mu.Lock()
	p.tryCount = 0
	p.mu.Unlock()

	resp, err := p.primary.Embed(ctx, req)
	p.mu.Lock()
	p.tryCount++
	p.mu.Unlock()
	if err == nil || !isRetryableProviderError(err) {
		return resp, err
	}
	lastErr := err
	for _, fb := range p.fallbacks {
		resp, err = fb.Embed(ctx, req)
		p.mu.Lock()
		p.tryCount++
		p.mu.Unlock()
		if err == nil || !isRetryableProviderError(err) {
			return resp, err
		}
		lastErr = err
	}
	return EmbedResponse{}, fmt.Errorf("%w: %v", ErrNoProviderAvailable, lastErr)
}

func isRetryableProviderError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCircuitOpen) {
		return true
	}
	var re retryableError
	if errors.As(err, &re) {
		return re.Retryable()
	}
	return errors.Is(err, ErrRateLimited) || errors.Is(err, context.DeadlineExceeded)
}

// WithCircuitBreaker returns an Option that wraps the underlying provider
// with a circuit breaker before governance controls are applied. This
// ensures the circuit breaker runs inside the governance boundary so that
// audit logs and metrics still capture rejected requests.
func WithCircuitBreaker(opts ...breaker.Option) Option {
	return func(p *GovernedProvider) {
		if p.provider != nil {
			p.provider = NewCircuitBreakerProvider(p.provider, opts...)
		}
	}
}

var _ Provider = (*CircuitBreakerProvider)(nil)
var _ Provider = (*FailoverProvider)(nil)
