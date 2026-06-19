// Package trace provides W3C trace context propagation for gofly services.
// It supports traceparent parsing, context injection/extraction, and pluggable
// sampling strategies.
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"hash/fnv"
	"strings"
)

// W3C trace context header and context key constants.
const (
	// TraceParentHeader is the HTTP header name for W3C traceparent.
	TraceParentHeader = "traceparent"
	// TraceIDKey is the structured-log key for the trace ID.
	TraceIDKey = "trace_id"
	// SpanIDKey is the structured-log key for the span ID.
	SpanIDKey = "span_id"
	// SampledKey is the structured-log key for the sampling decision.
	SampledKey = "trace_sampled"
)

type contextKey struct{}

// SpanContext carries trace and span identifiers for a single request.
type SpanContext struct {
	TraceID string
	SpanID  string
	Sampled bool
}

// Sampler decides whether a trace should be sampled.
type Sampler interface {
	Sample(traceID string, parentSampled bool, hasParent bool) bool
}

// SamplerFunc is an adapter to allow ordinary functions as Samplers.
type SamplerFunc func(traceID string, parentSampled bool, hasParent bool) bool

// Sample evaluates the sampling decision.
func (fn SamplerFunc) Sample(traceID string, parentSampled bool, hasParent bool) bool {
	return fn(traceID, parentSampled, hasParent)
}

// AlwaysSampler returns a sampler that always samples.
func AlwaysSampler() Sampler {
	return SamplerFunc(func(string, bool, bool) bool { return true })
}

// NeverSampler returns a sampler that never samples.
func NeverSampler() Sampler {
	return SamplerFunc(func(string, bool, bool) bool { return false })
}

// RatioSampler returns a sampler that samples probabilistically based on
// traceID. ratio must be in the range [0,1]; values outside the range are
// clamped to AlwaysSampler or NeverSampler.
func RatioSampler(ratio float64) Sampler {
	if ratio <= 0 {
		return NeverSampler()
	}
	if ratio >= 1 {
		return AlwaysSampler()
	}
	return SamplerFunc(func(traceID string, _ bool, _ bool) bool {
		return traceRatio(traceID) < ratio
	})
}

// ParentBasedSampler delegates to root sampler when there is no parent.
// When a parent exists, the parent's sampling decision is preserved.
func ParentBasedSampler(root Sampler) Sampler {
	if root == nil {
		root = AlwaysSampler()
	}
	return SamplerFunc(func(traceID string, parentSampled bool, hasParent bool) bool {
		if hasParent {
			return parentSampled
		}
		return root.Sample(traceID, false, false)
	})
}

// ShouldSample evaluates the sampler for the given trace ID.
// If sampler is nil, it defaults to true. If traceID is empty, a random
// trace ID is generated for the sampling decision.
func ShouldSample(sampler Sampler, traceID string) bool {
	if sampler == nil {
		return true
	}
	if traceID == "" {
		traceID = randomHex(16)
	}
	return sampler.Sample(traceID, false, false)
}

// NewContext returns a context carrying the given SpanContext.
func NewContext(ctx context.Context, sc SpanContext) context.Context {
	return context.WithValue(ctx, contextKey{}, sc)
}

// FromContext extracts the SpanContext from a context.
func FromContext(ctx context.Context) (SpanContext, bool) {
	sc, ok := ctx.Value(contextKey{}).(SpanContext)
	return sc, ok
}

// Start creates a new child span context from the parent traceparent string.
// If parent is invalid or empty, a new root trace is started.
func Start(ctx context.Context, parent string) (context.Context, SpanContext) {
	return StartWithSampler(ctx, parent, ParentBasedSampler(AlwaysSampler()))
}

// StartWithSampler creates a new child span context using the given sampler.
// If parent is invalid or empty, a new root trace is started.
func StartWithSampler(ctx context.Context, parent string, sampler Sampler) (context.Context, SpanContext) {
	sc, ok := ParseTraceParent(parent)
	if !ok {
		sc = SpanContext{TraceID: randomHex(16)}
	}
	if sampler == nil {
		sampler = ParentBasedSampler(AlwaysSampler())
	}
	sc.Sampled = sampler.Sample(sc.TraceID, sc.Sampled, ok)
	sc.SpanID = randomHex(8)
	ctx = NewContext(ctx, sc)
	return ctx, sc
}

// TraceParent formats a SpanContext as a W3C traceparent string.
func TraceParent(sc SpanContext) string {
	flags := "00"
	if sc.Sampled {
		flags = "01"
	}
	return "00-" + sc.TraceID + "-" + sc.SpanID + "-" + flags
}

// ParseTraceParent parses a W3C traceparent string into a SpanContext.
// It returns false if the value is malformed or contains all-zero IDs.
func ParseTraceParent(value string) (SpanContext, bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 || parts[0] != "00" || !validHex(parts[1], 32) || !validHex(parts[2], 16) || !validHex(parts[3], 2) {
		return SpanContext{}, false
	}
	if parts[1] == strings.Repeat("0", 32) || parts[2] == strings.Repeat("0", 16) {
		return SpanContext{}, false
	}
	return SpanContext{TraceID: parts[1], SpanID: parts[2], Sampled: parts[3] == "01"}, true
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "1" + strings.Repeat("0", n*2-1)
	}
	return hex.EncodeToString(b)
}

func validHex(s string, wantLen int) bool {
	if len(s) != wantLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func traceRatio(traceID string) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(traceID))
	return float64(h.Sum64()) / float64(^uint64(0))
}
