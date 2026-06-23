// Package limit provides rate limiting, concurrency limiting and adaptive
// overload protection for gofly services.
package limit

import (
	"errors"
	"sync"
	"time"
)

// ErrLimited is returned when the adaptive limiter rejects a request.
var ErrLimited = errors.New("adaptive limiter rejected request")

// AdaptiveLimiter adjusts concurrency based on observed latency, error rate
// and CPU usage.
type AdaptiveLimiter struct {
	mu               sync.Mutex
	minLimit         int
	maxLimit         int
	limit            int
	inFlight         int
	cpuThreshold     int
	window           time.Duration
	targetLatency    time.Duration
	targetErrorRatio float64
	minSamples       int64
	windowStart      time.Time
	requests         int64
	success          int64
	passes           int64
	drops            int64
	totalLatency     time.Duration
	peakInFlight     int
	now              func() time.Time
	cpu              func() int
}

// AdaptiveLimiterOption customises AdaptiveLimiter.
type AdaptiveLimiterOption func(*AdaptiveLimiter)

// WithAdaptiveLimits sets the minimum and maximum concurrency limits.
func WithAdaptiveLimits(minLimit, maxLimit int) AdaptiveLimiterOption {
	return func(l *AdaptiveLimiter) {
		if minLimit > 0 {
			l.minLimit = minLimit
		}
		if maxLimit > 0 {
			l.maxLimit = maxLimit
		}
	}
}

func WithAdaptiveInitialLimit(limit int) AdaptiveLimiterOption {
	return func(l *AdaptiveLimiter) {
		if limit > 0 {
			l.limit = limit
		}
	}
}

func WithAdaptiveLimitWindow(window time.Duration) AdaptiveLimiterOption {
	return func(l *AdaptiveLimiter) {
		if window > 0 {
			l.window = window
		}
	}
}

func WithAdaptiveTargetLatency(latency time.Duration) AdaptiveLimiterOption {
	return func(l *AdaptiveLimiter) {
		if latency > 0 {
			l.targetLatency = latency
		}
	}
}

func WithAdaptiveTargetErrorRatio(ratio float64) AdaptiveLimiterOption {
	return func(l *AdaptiveLimiter) {
		if ratio >= 0 && ratio <= 1 {
			l.targetErrorRatio = ratio
		}
	}
}

func WithAdaptiveMinSamples(samples int64) AdaptiveLimiterOption {
	return func(l *AdaptiveLimiter) {
		if samples > 0 {
			l.minSamples = samples
		}
	}
}

// WithAdaptiveCPUThreshold enables CPU-aware shedding when paired with a CPU
// reader. The value uses millicpu notation, so 900 means 90%.
func WithAdaptiveCPUThreshold(threshold int) AdaptiveLimiterOption {
	return func(l *AdaptiveLimiter) {
		if threshold > 0 {
			l.cpuThreshold = threshold
		}
	}
}

// WithAdaptiveCPUReader injects the current CPU load in millicpu notation. The
// limiter keeps the reader injectable so tests and hosts without a runtime CPU
// sampler can opt in without global state.
func WithAdaptiveCPUReader(reader func() int) AdaptiveLimiterOption {
	return func(l *AdaptiveLimiter) {
		l.cpu = reader
	}
}

func NewAdaptiveLimiter(opts ...AdaptiveLimiterOption) *AdaptiveLimiter {
	l := &AdaptiveLimiter{
		minLimit:         1,
		maxLimit:         1000,
		limit:            10,
		window:           time.Second,
		targetLatency:    100 * time.Millisecond,
		targetErrorRatio: 0.2,
		minSamples:       10,
		now:              time.Now,
	}
	for _, opt := range opts {
		opt(l)
	}
	if l.minLimit <= 0 {
		l.minLimit = 1
	}
	if l.maxLimit < l.minLimit {
		l.maxLimit = l.minLimit
	}
	if l.limit < l.minLimit {
		l.limit = l.minLimit
	}
	if l.limit > l.maxLimit {
		l.limit = l.maxLimit
	}
	if l.window <= 0 {
		l.window = time.Second
	}
	if l.targetLatency <= 0 {
		l.targetLatency = 100 * time.Millisecond
	}
	if l.minSamples <= 0 {
		l.minSamples = 10
	}
	l.windowStart = l.now()
	return l
}

func (l *AdaptiveLimiter) Allow() (*AdaptiveToken, error) {
	if l == nil {
		return noopAdaptiveToken, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.refreshLocked(now)
	if l.shouldShedLocked() {
		l.drops++
		return nil, ErrLimited
	}
	if l.inFlight >= l.limit {
		l.drops++
		return nil, ErrLimited
	}
	l.inFlight++
	l.passes++
	if l.inFlight > l.peakInFlight {
		l.peakInFlight = l.inFlight
	}
	return &AdaptiveToken{limiter: l, start: now}, nil
}

func (l *AdaptiveLimiter) Limit() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked(l.now())
	return l.limit
}

func (l *AdaptiveLimiter) InFlight() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inFlight
}

func (l *AdaptiveLimiter) Snapshot() AdaptiveSnapshot {
	if l == nil {
		return AdaptiveSnapshot{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.refreshLocked(now)
	return AdaptiveSnapshot{
		Limit:            l.limit,
		MinLimit:         l.minLimit,
		MaxLimit:         l.maxLimit,
		InFlight:         l.inFlight,
		CPUThreshold:     l.cpuThreshold,
		CPULoad:          l.cpuLoadLocked(),
		Overloaded:       l.shouldShedLocked(),
		Window:           l.window,
		TargetLatency:    l.targetLatency,
		TargetErrorRatio: l.targetErrorRatio,
		Requests:         l.requests,
		Success:          l.success,
		Failures:         l.requests - l.success,
		Passes:           l.passes,
		Drops:            l.drops,
		ErrorRatio:       l.errorRatioLocked(),
		AverageLatency:   l.averageLatencyLocked(),
		PeakInFlight:     l.peakInFlight,
	}
}

func (l *AdaptiveLimiter) shouldShedLocked() bool {
	if l.cpu == nil || l.cpuThreshold <= 0 {
		return false
	}
	return l.cpuLoadLocked() >= l.cpuThreshold && l.inFlight >= l.overloadFlightLocked()
}

func (l *AdaptiveLimiter) cpuLoadLocked() int {
	if l.cpu == nil {
		return 0
	}
	return l.cpu()
}

func (l *AdaptiveLimiter) overloadFlightLocked() int {
	threshold := l.limit / 2
	if threshold < 1 {
		return 1
	}
	return threshold
}

func (l *AdaptiveLimiter) refreshLocked(now time.Time) {
	if l.windowStart.IsZero() {
		l.windowStart = now
		return
	}
	if now.Sub(l.windowStart) < l.window {
		return
	}
	l.adjustLocked()
	l.windowStart = now
	l.requests = 0
	l.success = 0
	l.passes = 0
	l.drops = 0
	l.totalLatency = 0
	l.peakInFlight = l.inFlight
}

func (l *AdaptiveLimiter) adjustLocked() {
	if l.requests < l.minSamples {
		return
	}
	failures := l.requests - l.success
	errorRatio := float64(failures) / float64(l.requests)
	avgLatency := l.averageLatencyLocked()
	if errorRatio > l.targetErrorRatio || avgLatency > l.targetLatency {
		step := l.limit / 5
		if step < 1 {
			step = 1
		}
		l.limit -= step
		if l.limit < l.minLimit {
			l.limit = l.minLimit
		}
		return
	}
	if l.peakInFlight >= l.limit && l.limit < l.maxLimit {
		l.limit++
	}
}

func (l *AdaptiveLimiter) averageLatencyLocked() time.Duration {
	if l.requests == 0 {
		return 0
	}
	return time.Duration(int64(l.totalLatency) / l.requests)
}

func (l *AdaptiveLimiter) errorRatioLocked() float64 {
	if l.requests == 0 {
		return 0
	}
	return float64(l.requests-l.success) / float64(l.requests)
}

type AdaptiveToken struct {
	limiter *AdaptiveLimiter
	start   time.Time
	once    sync.Once
}

var noopAdaptiveToken = &AdaptiveToken{}

func (t *AdaptiveToken) Done(success bool) {
	if t == nil || t.limiter == nil {
		return
	}
	t.once.Do(func() {
		l := t.limiter
		l.mu.Lock()
		defer l.mu.Unlock()
		now := l.now()
		if l.inFlight > 0 {
			l.inFlight--
		}
		l.requests++
		if success {
			l.success++
		}
		l.totalLatency += now.Sub(t.start)
		l.refreshLocked(now)
	})
}

type AdaptiveSnapshot struct {
	Limit            int           `json:"limit"`
	MinLimit         int           `json:"minLimit"`
	MaxLimit         int           `json:"maxLimit"`
	InFlight         int           `json:"inFlight"`
	CPUThreshold     int           `json:"cpuThreshold,omitempty"`
	CPULoad          int           `json:"cpuLoad,omitempty"`
	Overloaded       bool          `json:"overloaded,omitempty"`
	Window           time.Duration `json:"window"`
	TargetLatency    time.Duration `json:"targetLatency"`
	TargetErrorRatio float64       `json:"targetErrorRatio"`
	Requests         int64         `json:"requests"`
	Success          int64         `json:"success"`
	Failures         int64         `json:"failures"`
	Passes           int64         `json:"passes"`
	Drops            int64         `json:"drops"`
	ErrorRatio       float64       `json:"errorRatio"`
	AverageLatency   time.Duration `json:"averageLatency"`
	PeakInFlight     int           `json:"peakInFlight"`
}
