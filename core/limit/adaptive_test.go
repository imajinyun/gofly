package limit

import (
	"errors"
	"testing"
	"time"
)

func TestAdaptiveLimiterRejectsAtCurrentLimit(t *testing.T) {
	limiter := NewAdaptiveLimiter(WithAdaptiveLimits(1, 1), WithAdaptiveInitialLimit(1))
	token, err := limiter.Allow()
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if _, err := limiter.Allow(); !errors.Is(err, ErrLimited) {
		t.Fatalf("second allow = %v, want ErrLimited", err)
	}
	token.Done(true)
	if got := limiter.InFlight(); got != 0 {
		t.Fatalf("in flight after done = %d, want 0", got)
	}
	if token, err = limiter.Allow(); err != nil {
		t.Fatalf("allow after done: %v", err)
	}
	token.Done(true)
}

func TestAdaptiveLimiterAdjustsLimit(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := NewAdaptiveLimiter(
		WithAdaptiveLimits(1, 4),
		WithAdaptiveInitialLimit(2),
		WithAdaptiveLimitWindow(10*time.Millisecond),
		WithAdaptiveTargetLatency(time.Millisecond),
		WithAdaptiveMinSamples(1),
	)
	limiter.now = func() time.Time { return now }
	limiter.windowStart = now

	first, err := limiter.Allow()
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	now = now.Add(2 * time.Millisecond)
	first.Done(true)
	second, err := limiter.Allow()
	if err != nil {
		t.Fatalf("second allow: %v", err)
	}
	now = now.Add(9 * time.Millisecond)
	second.Done(true)
	if got := limiter.Limit(); got != 1 {
		t.Fatalf("limit after slow window = %d, want 1", got)
	}

	now = now.Add(time.Millisecond)
	token, err := limiter.Allow()
	if err != nil {
		t.Fatalf("allow in fast window: %v", err)
	}
	token.Done(true)
	now = now.Add(10 * time.Millisecond)
	if got := limiter.Limit(); got != 2 {
		t.Fatalf("limit after saturated fast window = %d, want 2", got)
	}
}

func TestAdaptiveLimiterSnapshotIncludesFailures(t *testing.T) {
	limiter := NewAdaptiveLimiter(WithAdaptiveLimits(1, 2), WithAdaptiveInitialLimit(1))
	token, err := limiter.Allow()
	if err != nil {
		t.Fatal(err)
	}
	token.Done(false)
	snapshot := limiter.Snapshot()
	if snapshot.Requests != 1 || snapshot.Failures != 1 || snapshot.ErrorRatio != 1 {
		t.Fatalf("snapshot = %#v, want failed request", snapshot)
	}
}

func TestAdaptiveLimiterSnapshotIncludesPassAndDropCounts(t *testing.T) {
	limiter := NewAdaptiveLimiter(WithAdaptiveLimits(1, 1), WithAdaptiveInitialLimit(1))
	token, err := limiter.Allow()
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if _, err := limiter.Allow(); !errors.Is(err, ErrLimited) {
		token.Done(true)
		t.Fatalf("second allow = %v, want ErrLimited", err)
	}
	snapshot := limiter.Snapshot()
	token.Done(true)
	if snapshot.Passes != 1 || snapshot.Drops != 1 {
		t.Fatalf("snapshot pass/drop = %d/%d, want 1/1", snapshot.Passes, snapshot.Drops)
	}
}

func TestAdaptiveLimiterPassDropCountsResetWithWindow(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := NewAdaptiveLimiter(
		WithAdaptiveLimits(1, 1),
		WithAdaptiveInitialLimit(1),
		WithAdaptiveLimitWindow(time.Millisecond),
		WithAdaptiveMinSamples(1),
	)
	limiter.now = func() time.Time { return now }
	limiter.windowStart = now

	token, err := limiter.Allow()
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if _, err := limiter.Allow(); !errors.Is(err, ErrLimited) {
		token.Done(true)
		t.Fatalf("second allow = %v, want ErrLimited", err)
	}
	token.Done(true)
	now = now.Add(2 * time.Millisecond)
	snapshot := limiter.Snapshot()
	if snapshot.Passes != 0 || snapshot.Drops != 0 || snapshot.Requests != 0 {
		t.Fatalf("snapshot after window reset = %#v, want reset pass/drop/request counters", snapshot)
	}
}

func TestAdaptiveLimiterRejectsWhenCPUSignalIsOverloaded(t *testing.T) {
	load := 950
	limiter := NewAdaptiveLimiter(
		WithAdaptiveLimits(1, 10),
		WithAdaptiveInitialLimit(4),
		WithAdaptiveCPUThreshold(900),
		WithAdaptiveCPUReader(func() int { return load }),
	)

	first, err := limiter.Allow()
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	second, err := limiter.Allow()
	if err != nil {
		first.Done(true)
		t.Fatalf("second allow: %v", err)
	}
	if _, err := limiter.Allow(); !errors.Is(err, ErrLimited) {
		first.Done(true)
		second.Done(true)
		t.Fatalf("third allow = %v, want ErrLimited under CPU overload", err)
	}
	snapshot := limiter.Snapshot()
	first.Done(true)
	second.Done(true)
	if !snapshot.Overloaded || snapshot.CPULoad != 950 || snapshot.CPUThreshold != 900 {
		t.Fatalf("snapshot = %#v, want overloaded CPU signal", snapshot)
	}
	if snapshot.Passes != 2 || snapshot.Drops != 1 {
		t.Fatalf("snapshot pass/drop = %d/%d, want 2/1", snapshot.Passes, snapshot.Drops)
	}

	load = 100
	if token, err := limiter.Allow(); err != nil {
		t.Fatalf("allow after CPU recovers: %v", err)
	} else {
		token.Done(true)
	}
}

func TestAdaptiveLimiterNilGuard(t *testing.T) {
	var nilL *AdaptiveLimiter
	if token, err := nilL.Allow(); err != nil || token == nil {
		t.Fatalf("nil Allow = %v, %v, want noop token, nil", token, err)
	}
	if nilL.Limit() != 0 {
		t.Fatalf("nil Limit = %d, want 0", nilL.Limit())
	}
	if nilL.InFlight() != 0 {
		t.Fatalf("nil InFlight = %d, want 0", nilL.InFlight())
	}
	if s := nilL.Snapshot(); s != (AdaptiveSnapshot{}) {
		t.Fatalf("nil Snapshot = %+v", s)
	}
}

func TestAdaptiveLimiterOptionsClamping(t *testing.T) {
	// clamping: minLimit=0 clamped to 1; maxLimit=0 ignored (stays default 1000); limit clamped to maxLimit
	l := NewAdaptiveLimiter(WithAdaptiveLimits(0, 0), WithAdaptiveInitialLimit(0))
	if l.minLimit != 1 {
		t.Fatalf("minLimit = %d, want 1", l.minLimit)
	}
	// WithAdaptiveInitialLimit(0) is ignored, so limit stays at default 10
	if l.limit != 10 {
		t.Fatalf("limit = %d, want 10", l.limit)
	}

	// maxLimit < minLimit
	l = NewAdaptiveLimiter(WithAdaptiveLimits(10, 5))
	if l.maxLimit != 10 {
		t.Fatalf("maxLimit = %d, want 10", l.maxLimit)
	}

	// negative option values ignored
	l = NewAdaptiveLimiter(
		WithAdaptiveLimitWindow(-1),
		WithAdaptiveTargetLatency(-1),
		WithAdaptiveMinSamples(-1),
	)
	if l.window != time.Second || l.targetLatency != 100*time.Millisecond || l.minSamples != 10 {
		t.Fatalf("negative options not ignored")
	}

	// targetErrorRatio out of range ignored
	l = NewAdaptiveLimiter(WithAdaptiveTargetErrorRatio(-0.1), WithAdaptiveTargetErrorRatio(1.5))
	if l.targetErrorRatio != 0.2 {
		t.Fatalf("targetErrorRatio = %v, want 0.2", l.targetErrorRatio)
	}
}

func TestAdaptiveLimiterAdjustDownOnErrors(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewAdaptiveLimiter(
		WithAdaptiveLimits(10, 100),
		WithAdaptiveInitialLimit(50),
		WithAdaptiveLimitWindow(time.Hour),
		WithAdaptiveTargetLatency(time.Millisecond),
		WithAdaptiveTargetErrorRatio(0.01),
		WithAdaptiveMinSamples(1),
	)
	l.now = func() time.Time { return now }
	l.windowStart = now

	token, _ := l.Allow()
	token.Done(false)
	now = now.Add(2 * time.Hour)
	_ = l.Limit()
	if l.limit >= 50 {
		t.Fatalf("limit should decrease on error, got %d", l.limit)
	}
}

func TestAdaptiveLimiterAdjustUpOnHealthy(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewAdaptiveLimiter(
		WithAdaptiveLimits(1, 10),
		WithAdaptiveInitialLimit(5),
		WithAdaptiveLimitWindow(time.Hour),
		WithAdaptiveTargetLatency(time.Hour),
		WithAdaptiveTargetErrorRatio(1.0),
		WithAdaptiveMinSamples(1),
	)
	l.now = func() time.Time { return now }
	l.windowStart = now

	// window 1: acquire up to limit, immediately Done so latency stays low
	for i := 0; i < 5; i++ {
		token, _ := l.Allow()
		token.Done(true)
	}
	// cross to window 2: peakInFlight = inFlight = 0 (all Done), adjust does not increase
	now = now.Add(2 * time.Hour)
	_ = l.Limit()
	// window 2: acquire up to limit again, immediately Done one to meet minSamples;
	// keep rest in-flight so peakInFlight records inFlight at next window end
	tokens := make([]*AdaptiveToken, 0, 5)
	for i := 0; i < 5; i++ {
		token, _ := l.Allow()
		tokens = append(tokens, token)
	}
	tokens[0].Done(true)
	// cross to window 3: peakInFlight = inFlight = 4, adjust sees it and increases limit
	now = now.Add(2 * time.Hour)
	_ = l.Limit()
	for i := 1; i < len(tokens); i++ {
		tokens[i].Done(true)
	}
	if l.limit <= 5 {
		t.Fatalf("limit should increase when healthy and peakInFlight >= limit, got %d", l.limit)
	}
}

func TestAdaptiveLimiterRequestsBelowMinSamples(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewAdaptiveLimiter(
		WithAdaptiveLimits(10, 100),
		WithAdaptiveInitialLimit(50),
		WithAdaptiveLimitWindow(time.Hour),
		WithAdaptiveMinSamples(10),
	)
	l.now = func() time.Time { return now }
	l.windowStart = now

	// only 2 requests, below minSamples
	for i := 0; i < 2; i++ {
		token, _ := l.Allow()
		token.Done(true)
	}
	now = now.Add(2 * time.Hour)
	oldLimit := l.limit
	_ = l.Limit()
	if l.limit != oldLimit {
		t.Fatalf("limit should not adjust below minSamples, got %d, want %d", l.limit, oldLimit)
	}
}

func TestAdaptiveLimiterDoneNilGuard(t *testing.T) {
	var nilToken *AdaptiveToken
	nilToken.Done(true)
	noopAdaptiveToken.Done(true)
}
