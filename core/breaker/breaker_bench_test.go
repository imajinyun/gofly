package breaker

import (
	"context"
	"errors"
	"testing"
)

// BenchmarkBreakerDoSuccess measures the closed-state happy path, which gates
// every protected call in production.
func BenchmarkBreakerDoSuccess(b *testing.B) {
	br := New()
	ctx := context.Background()
	ok := func() error { return nil }

	b.ReportAllocs()
	for b.Loop() {
		_ = br.Do(ctx, ok)
	}
}

// BenchmarkBreakerAllowParallel measures the Allow gate under contention.
func BenchmarkBreakerAllowParallel(b *testing.B) {
	br := New()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = br.Allow()
		}
	})
}

// BenchmarkAdaptiveBreakerDo measures the adaptive breaker's protected call
// path, including its rolling-window bookkeeping.
func BenchmarkAdaptiveBreakerDo(b *testing.B) {
	br := NewAdaptive()
	ctx := context.Background()
	errBoom := errors.New("boom")
	fn := func() error {
		if br.State() == Open {
			return nil
		}
		return errBoom
	}

	b.ReportAllocs()
	for b.Loop() {
		_ = br.Do(ctx, fn)
	}
}
