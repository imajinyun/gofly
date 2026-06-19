// Package trace provides W3C traceparent propagation and OpenTelemetry tracer
// lifecycle management for gofly services.
package trace

import oteltrace "go.opentelemetry.io/otel/trace"

// FromOTel converts an OpenTelemetry SpanContext to a gofly SpanContext.
func FromOTel(sc oteltrace.SpanContext) (SpanContext, bool) {
	if !sc.IsValid() {
		return SpanContext{}, false
	}
	return SpanContext{TraceID: sc.TraceID().String(), SpanID: sc.SpanID().String(), Sampled: sc.TraceFlags().IsSampled()}, true
}

// ToOTel converts a gofly SpanContext to an OpenTelemetry SpanContext.
func ToOTel(sc SpanContext) (oteltrace.SpanContext, bool) {
	traceID, err := oteltrace.TraceIDFromHex(sc.TraceID)
	if err != nil {
		return oteltrace.SpanContext{}, false
	}
	spanID, err := oteltrace.SpanIDFromHex(sc.SpanID)
	if err != nil {
		return oteltrace.SpanContext{}, false
	}
	flags := oteltrace.TraceFlags(0)
	if sc.Sampled {
		flags = oteltrace.FlagsSampled
	}
	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: flags}), true
}
