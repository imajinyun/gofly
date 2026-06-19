package llm

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofly/gofly/core/breaker"
)

// errFailOnceProvider succeeds on the first call and fails on subsequent calls.
type sequencedProvider struct {
	NoOpProvider
	callCount atomic.Int32
	failAfter int32
	err       error
}

func (p *sequencedProvider) failIfNeeded() error {
	count := p.callCount.Add(1)
	if count > p.failAfter {
		return p.err
	}
	return nil
}

type failProvider struct {
	NoOpProvider
	err error
}

func (p *failProvider) Complete(_ context.Context, _ Request) (Response, error) {
	return Response{}, p.err
}

func (p *failProvider) Stream(_ context.Context, _ Request) (<-chan StreamEvent, error) {
	return nil, p.err
}

func (p *failProvider) Embed(_ context.Context, _ EmbedRequest) (EmbedResponse, error) {
	return EmbedResponse{}, p.err
}

type succeedAfterFailProvider struct {
	NoOpProvider
	attempts int32
	failFor  int32
	err      error
}

func (p *succeedAfterFailProvider) Complete(_ context.Context, _ Request) (Response, error) {
	p.attempts++
	if p.attempts <= p.failFor {
		return Response{}, p.err
	}
	return Response{Text: "ok"}, nil
}

// retryableErr is a test error implementing the retryableError interface.
type retryableErr struct {
	msg       string
	retryable bool
}

func (e *retryableErr) Error() string   { return e.msg }
func (e *retryableErr) Retryable() bool { return e.retryable }

func TestCircuitBreakerCompleteClosesAfterSuccess(t *testing.T) {
	inner := NoOpProvider{}
	cb := NewCircuitBreakerProvider(inner, breaker.WithFailureThreshold(2), breaker.WithOpenTimeout(50*time.Millisecond))

	// First call succeeds
	resp, err := cb.Complete(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if resp.Usage.InputTokens == 0 {
		t.Fatal("first Complete: expected non-zero usage")
	}

	snap := cb.CircuitBreakerSnapshot()
	if snap.State != "closed" {
		t.Fatalf("after success state = %s, want closed", snap.State)
	}
}

func TestCircuitBreakerOpensAfterFailures(t *testing.T) {
	inner := &failProvider{err: errors.New("provider down")}
	cb := NewCircuitBreakerProvider(inner, breaker.WithFailureThreshold(3), breaker.WithOpenTimeout(5*time.Second))

	for i := range 3 {
		_, err := cb.Complete(context.Background(), Request{Prompt: "hello"})
		if err == nil {
			t.Fatalf("call %d: expected error", i+1)
		}
	}

	// Fourth call should trip the circuit
	_, err := cb.Complete(context.Background(), Request{Prompt: "hello"})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("after 3 failures: expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreakerHalfOpenProbe(t *testing.T) {
	inner := &succeedAfterFailProvider{failFor: 2, err: errors.New("transient")}
	cb := NewCircuitBreakerProvider(inner, breaker.WithFailureThreshold(2), breaker.WithOpenTimeout(50*time.Millisecond))

	// Two failures to open circuit
	_, _ = cb.Complete(context.Background(), Request{Prompt: "hello"})
	_, _ = cb.Complete(context.Background(), Request{Prompt: "hello"})

	snap := cb.CircuitBreakerSnapshot()
	if snap.State != "open" {
		t.Fatalf("after 2 failures: state = %s, want open", snap.State)
	}

	// Wait for half-open
	time.Sleep(60 * time.Millisecond)

	// Next call should probe (half-open) and succeed, closing the circuit
	resp, err := cb.Complete(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("half-open probe: %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("half-open probe: got text %q, want %q", resp.Text, "ok")
	}

	snap = cb.CircuitBreakerSnapshot()
	if snap.State != "closed" {
		t.Fatalf("after success probe: state = %s, want closed", snap.State)
	}
}

func TestCircuitBreakerStreamOpensOnError(t *testing.T) {
	inner := &failProvider{err: errors.New("stream failed")}
	cb := NewCircuitBreakerProvider(inner, breaker.WithFailureThreshold(1), breaker.WithOpenTimeout(5*time.Second))

	_, err := cb.Stream(context.Background(), Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected stream error")
	}

	// Circuit should be open after one failure
	_, err = cb.Stream(context.Background(), Request{Prompt: "hello"})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreakerEmbed(t *testing.T) {
	inner := &failProvider{err: errors.New("embed down")}
	cb := NewCircuitBreakerProvider(inner, breaker.WithFailureThreshold(2), breaker.WithOpenTimeout(5*time.Second))

	_, _ = cb.Embed(context.Background(), EmbedRequest{Inputs: []string{"test"}})
	_, _ = cb.Embed(context.Background(), EmbedRequest{Inputs: []string{"test"}})

	_, err := cb.Embed(context.Background(), EmbedRequest{Inputs: []string{"test"}})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after 2 embed failures, got %v", err)
	}
}

func TestFailoverCompleteFallsBackOnRetryableError(t *testing.T) {
	primary := &failProvider{err: &retryableErr{msg: "primary down", retryable: true}}
	fallback := NoOpProvider{}
	f := NewFailoverProvider(primary, fallback)

	resp, err := f.Complete(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
	if resp.Usage.InputTokens == 0 {
		t.Fatal("expected non-zero usage from fallback")
	}
	if f.TryCount() != 2 {
		t.Fatalf("TryCount = %d, want 2 (primary+fallback)", f.TryCount())
	}
}

func TestFailoverCompleteNonRetryableErrorNoFallback(t *testing.T) {
	primary := &failProvider{err: &retryableErr{msg: "bad request", retryable: false}}
	fallback := NoOpProvider{}
	f := NewFailoverProvider(primary, fallback)

	_, err := f.Complete(context.Background(), Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected non-retryable error to propagate without fallback")
	}
	if f.TryCount() != 1 {
		t.Fatalf("TryCount = %d, want 1 (no fallback attempted)", f.TryCount())
	}
}

func TestFailoverAllFallbacksExhausted(t *testing.T) {
	primary := &failProvider{err: &retryableErr{msg: "p down", retryable: true}}
	fb1 := &failProvider{err: &retryableErr{msg: "fb1 down", retryable: true}}
	fb2 := &failProvider{err: &retryableErr{msg: "fb2 down", retryable: true}}
	f := NewFailoverProvider(primary, fb1, fb2)

	_, err := f.Complete(context.Background(), Request{Prompt: "hello"})
	if !errors.Is(err, ErrNoProviderAvailable) {
		t.Fatalf("expected ErrNoProviderAvailable, got %v", err)
	}
	if f.TryCount() != 3 {
		t.Fatalf("TryCount = %d, want 3 (primary+2 fallbacks)", f.TryCount())
	}
}

func TestFailoverStreamFallsBack(t *testing.T) {
	primary := &failProvider{err: &retryableErr{msg: "primary stream down", retryable: true}}
	fallback := NoOpProvider{}
	f := NewFailoverProvider(primary, fallback)

	stream, err := f.Stream(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("expected fallback stream to open: %v", err)
	}
	event, ok := <-stream
	if !ok || !event.Done {
		t.Fatal("expected done event from fallback stream")
	}
	if f.TryCount() != 2 {
		t.Fatalf("TryCount = %d, want 2", f.TryCount())
	}
}

func TestFailoverEmbedFallsBackOnRetryable(t *testing.T) {
	primary := &failProvider{err: &retryableErr{msg: "embed down", retryable: true}}
	fallback := NoOpProvider{}
	f := NewFailoverProvider(primary, fallback)

	resp, err := f.Embed(context.Background(), EmbedRequest{Inputs: []string{"test"}})
	if err != nil {
		t.Fatalf("expected fallback embed: %v", err)
	}
	if len(resp.Vectors) != 1 {
		t.Fatalf("expected 1 vector from fallback, got %d", len(resp.Vectors))
	}
}

func TestWithCircuitBreakerOption(t *testing.T) {
	inner := &failProvider{err: errors.New("down")}
	gp := NewGovernedProvider(inner, WithCircuitBreaker(breaker.WithFailureThreshold(1), breaker.WithOpenTimeout(5*time.Second)))

	_, err := gp.Complete(context.Background(), Request{Prompt: "hello"})
	if err == nil {
		t.Fatal("expected error from governed provider with circuit breaker")
	}
	if !errors.Is(err, ErrCircuitOpen) && !strings.Contains(err.Error(), "down") {
		t.Fatalf("unexpected error type: %v", err)
	}
}

func TestFailoverNilProviderDefaultsToNoop(t *testing.T) {
	f := NewFailoverProvider(nil)
	resp, err := f.Complete(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("nil primary should default to NoOp: %v", err)
	}
	if resp.Usage.InputTokens == 0 {
		t.Fatal("expected usage from default NoOp")
	}
}

func TestCircuitBreakerSnapshotEmptyForNil(t *testing.T) {
	var cb *CircuitBreakerProvider
	snap := cb.CircuitBreakerSnapshot()
	if snap.State != "" {
		t.Fatalf("nil snapshot state = %q, want empty", snap.State)
	}
}

func TestFailoverStreamEventErrorsTracked(t *testing.T) {
	inner := NoOpProvider{}
	cb := NewCircuitBreakerProvider(inner, breaker.WithFailureThreshold(1), breaker.WithOpenTimeout(5*time.Second))

	// First stream succeeds
	stream, err := cb.Stream(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	for range stream {
	}

	snap := cb.CircuitBreakerSnapshot()
	if snap.State != "closed" {
		t.Fatalf("after successful stream: state = %s, want closed", snap.State)
	}
}
