// Package breaker provides circuit breaker implementations for gofly services,
// including simple threshold-based and adaptive breakers.
package breaker

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrOpen is returned when the circuit breaker is open.
var ErrOpen = errors.New("breaker is open")

// State represents the circuit breaker state.
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Breaker is a simple threshold-based circuit breaker.
type Breaker struct {
	mu               sync.Mutex
	state            State
	failures         int
	failureThreshold int
	openTimeout      time.Duration
	openedAt         time.Time
}

// Option configures a Breaker.
type Option func(*Breaker)

// WithFailureThreshold sets the number of consecutive failures before opening.
func WithFailureThreshold(n int) Option {
	return func(b *Breaker) {
		if n > 0 {
			b.failureThreshold = n
		}
	}
}

// WithOpenTimeout sets the duration before attempting to half-open.
func WithOpenTimeout(d time.Duration) Option {
	return func(b *Breaker) {
		if d > 0 {
			b.openTimeout = d
		}
	}
}

// New creates a Breaker with the given options.
func New(opts ...Option) *Breaker {
	b := &Breaker{failureThreshold: 3, openTimeout: time.Second}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked(time.Now())
	return b.state
}

func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked(time.Now())
	if b.state == Open {
		return ErrOpen
	}
	return nil
}

func (b *Breaker) Do(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.Allow(); err != nil {
		return err
	}
	err := fn()
	if err != nil {
		b.reject()
		return err
	}
	b.accept()
	return nil
}

func (b *Breaker) accept() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.state = Closed
}

func (b *Breaker) reject() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == HalfOpen {
		b.openLocked(time.Now())
		return
	}
	b.failures++
	if b.failures >= b.failureThreshold {
		b.openLocked(time.Now())
	}
}

func (b *Breaker) refreshLocked(now time.Time) {
	if b.state == Open && now.Sub(b.openedAt) >= b.openTimeout {
		b.state = HalfOpen
	}
}

func (b *Breaker) openLocked(now time.Time) {
	b.state = Open
	b.openedAt = now
}

type bucket struct {
	start    time.Time
	requests int64
	success  int64
}

type AdaptiveBreaker struct {
	mu           sync.Mutex
	state        State
	openedAt     time.Time
	openTimeout  time.Duration
	window       time.Duration
	bucketSize   time.Duration
	buckets      []bucket
	minRequests  int64
	failureRatio float64
	k            float64
}

type BreakerSnapshot struct {
	State            string        `json:"state"`
	Failures         int           `json:"failures"`
	FailureThreshold int           `json:"failureThreshold"`
	OpenTimeout      time.Duration `json:"openTimeout"`
	OpenedAt         time.Time     `json:"openedAt,omitempty"`
}

func (b *Breaker) Snapshot() BreakerSnapshot {
	if b == nil {
		return BreakerSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked(time.Now())
	return BreakerSnapshot{
		State:            b.state.String(),
		Failures:         b.failures,
		FailureThreshold: b.failureThreshold,
		OpenTimeout:      b.openTimeout,
		OpenedAt:         b.openedAt,
	}
}

type AdaptiveSnapshot struct {
	State        string        `json:"state"`
	OpenTimeout  time.Duration `json:"openTimeout"`
	OpenedAt     time.Time     `json:"openedAt,omitempty"`
	Window       time.Duration `json:"window"`
	BucketSize   time.Duration `json:"bucketSize"`
	Buckets      int           `json:"buckets"`
	MinRequests  int64         `json:"minRequests"`
	FailureRatio float64       `json:"failureRatio"`
	K            float64       `json:"k"`
	Requests     int64         `json:"requests"`
	Success      int64         `json:"success"`
	Failures     int64         `json:"failures"`
	ErrorRatio   float64       `json:"errorRatio"`
}

type AdaptiveOption func(*AdaptiveBreaker)

func WithAdaptiveOpenTimeout(d time.Duration) AdaptiveOption {
	return func(b *AdaptiveBreaker) {
		if d > 0 {
			b.openTimeout = d
		}
	}
}

func WithAdaptiveWindow(d time.Duration) AdaptiveOption {
	return func(b *AdaptiveBreaker) {
		if d > 0 {
			b.window = d
		}
	}
}

func WithAdaptiveBuckets(n int) AdaptiveOption {
	return func(b *AdaptiveBreaker) {
		if n > 0 {
			b.buckets = make([]bucket, n)
		}
	}
}

func WithAdaptiveMinRequests(n int64) AdaptiveOption {
	return func(b *AdaptiveBreaker) {
		if n > 0 {
			b.minRequests = n
		}
	}
}

func WithAdaptiveFailureRatio(ratio float64) AdaptiveOption {
	return func(b *AdaptiveBreaker) {
		if ratio > 0 && ratio <= 1 {
			b.failureRatio = ratio
		}
	}
}

func WithAdaptiveK(k float64) AdaptiveOption {
	return func(b *AdaptiveBreaker) {
		if k > 0 {
			b.k = k
		}
	}
}

func NewAdaptive(opts ...AdaptiveOption) *AdaptiveBreaker {
	b := &AdaptiveBreaker{
		openTimeout:  5 * time.Second,
		window:       10 * time.Second,
		buckets:      make([]bucket, 10),
		minRequests:  20,
		failureRatio: 0.5,
		k:            1.5,
	}
	for _, opt := range opts {
		opt(b)
	}
	if len(b.buckets) == 0 {
		b.buckets = make([]bucket, 10)
	}
	if b.window <= 0 {
		b.window = 10 * time.Second
	}
	b.bucketSize = b.window / time.Duration(len(b.buckets))
	if b.bucketSize <= 0 {
		b.bucketSize = time.Second
	}
	return b
}

func (b *AdaptiveBreaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshLocked(time.Now())
	return b.state
}

func (b *AdaptiveBreaker) Snapshot() AdaptiveSnapshot {
	if b == nil {
		return AdaptiveSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.refreshLocked(now)
	requests, success := b.snapshotLocked(now)
	failures := requests - success
	errorRatio := 0.0
	if requests > 0 {
		errorRatio = float64(failures) / float64(requests)
	}
	return AdaptiveSnapshot{
		State:        b.state.String(),
		OpenTimeout:  b.openTimeout,
		OpenedAt:     b.openedAt,
		Window:       b.window,
		BucketSize:   b.bucketSize,
		Buckets:      len(b.buckets),
		MinRequests:  b.minRequests,
		FailureRatio: b.failureRatio,
		K:            b.k,
		Requests:     requests,
		Success:      success,
		Failures:     failures,
		ErrorRatio:   errorRatio,
	}
}

func (b *AdaptiveBreaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.refreshLocked(now)
	if b.state == Open {
		return ErrOpen
	}
	if b.state == Closed && b.shouldOpenLocked(now) {
		b.openLocked(now)
		return ErrOpen
	}
	return nil
}

func (b *AdaptiveBreaker) Do(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.Allow(); err != nil {
		return err
	}
	err := fn()
	if err != nil {
		b.MarkFailure()
		return err
	}
	b.MarkSuccess()
	return nil
}

func (b *AdaptiveBreaker) MarkSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.refreshLocked(now)
	current := b.currentBucketLocked(now)
	current.requests++
	current.success++
	if b.state == HalfOpen {
		b.state = Closed
		b.resetLocked()
	}
}

func (b *AdaptiveBreaker) MarkFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.refreshLocked(now)
	b.currentBucketLocked(now).requests++
	if b.state == HalfOpen || b.shouldOpenLocked(now) {
		b.openLocked(now)
	}
}

func (b *AdaptiveBreaker) refreshLocked(now time.Time) {
	if b.state == Open && now.Sub(b.openedAt) >= b.openTimeout {
		b.state = HalfOpen
	}
}

func (b *AdaptiveBreaker) shouldOpenLocked(now time.Time) bool {
	requests, success := b.snapshotLocked(now)
	if requests < b.minRequests {
		return false
	}
	failures := requests - success
	if float64(failures)/float64(requests) < b.failureRatio {
		return false
	}
	return float64(requests) >= b.k*float64(success+1)
}

func (b *AdaptiveBreaker) snapshotLocked(now time.Time) (requests int64, success int64) {
	for i := range b.buckets {
		bucket := &b.buckets[i]
		if bucket.start.IsZero() || now.Sub(bucket.start) >= b.window {
			continue
		}
		requests += bucket.requests
		success += bucket.success
	}
	return requests, success
}

func (b *AdaptiveBreaker) currentBucketLocked(now time.Time) *bucket {
	start := now.Truncate(b.bucketSize)
	idx := int(start.UnixNano()/int64(b.bucketSize)) % len(b.buckets)
	if idx < 0 {
		idx = -idx
	}
	current := &b.buckets[idx]
	if !current.start.Equal(start) {
		*current = bucket{start: start}
	}
	return current
}

func (b *AdaptiveBreaker) openLocked(now time.Time) {
	b.state = Open
	b.openedAt = now
}

func (b *AdaptiveBreaker) resetLocked() {
	for i := range b.buckets {
		b.buckets[i] = bucket{}
	}
}
