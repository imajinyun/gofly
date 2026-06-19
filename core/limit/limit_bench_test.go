package limit

import (
	"testing"
	"time"
)

// BenchmarkTokenLimiterAllow measures the token-bucket Allow hot path.
func BenchmarkTokenLimiterAllow(b *testing.B) {
	l := New(1_000_000, 1_000_000)
	b.ReportAllocs()
	for b.Loop() {
		l.Allow()
	}
}

// BenchmarkTokenLimiterAllowParallel measures Allow under contention, which is
// the realistic case for a shared limiter behind an HTTP server.
func BenchmarkTokenLimiterAllowParallel(b *testing.B) {
	l := New(1_000_000, 1_000_000)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Allow()
		}
	})
}

// BenchmarkSlidingWindowAllow measures the sliding-window limiter hot path.
func BenchmarkSlidingWindowAllow(b *testing.B) {
	l := NewSlidingWindow(1_000_000, time.Second, 10)
	b.ReportAllocs()
	for b.Loop() {
		l.Allow()
	}
}
