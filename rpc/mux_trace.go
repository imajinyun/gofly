package rpc

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const muxTraceConnectionIDKey = "rpc.mux.connection_id"

// MuxTraceAttributes returns OTel span attributes for a filtered mux diagnosis.
// connection_id is intentionally trace-only and must not be used as a metric
// label because it is high-cardinality.
func MuxTraceAttributes(probe RPCDiagnosisProbe) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 8)
	if probe.Endpoint != "" {
		attrs = append(attrs, attribute.String("rpc.mux.endpoint", probe.Endpoint))
	}
	if probe.ConnectionID != "" {
		attrs = append(attrs, attribute.String(muxTraceConnectionIDKey, probe.ConnectionID))
	}
	if probe.PoolSlot > 0 {
		attrs = append(attrs, attribute.Int("rpc.mux.pool_slot", probe.PoolSlot))
	}
	if probe.FlowControl != "" {
		attrs = append(attrs, attribute.String("rpc.mux.flow_control.event", probe.FlowControl))
	}
	appendMuxTraceManagerAttributes(&attrs, probe.ConnectionID, probe.Diagnosis.Mux.Manager)
	return attrs
}

func appendMuxTraceManagerAttributes(attrs *[]attribute.KeyValue, probeConnectionID string, diagnosis RPCMuxConnectionManagerDiagnosis) {
	if !diagnosis.Enabled {
		return
	}
	*attrs = append(*attrs,
		attribute.String("rpc.mux.mode", diagnosis.Mode),
		attribute.Int("rpc.mux.endpoint.count", len(diagnosis.Endpoints)),
		attribute.Int64("rpc.mux.flow_control.write_timeout.count", diagnosis.FlowControl.WriteTimeouts),
		attribute.Int64("rpc.mux.flow_control.credit_wait_timeout.count", diagnosis.FlowControl.CreditWaitTimeouts),
		attribute.Int64("rpc.mux.flow_control.connection_window_exhausted.count", diagnosis.FlowControl.ConnectionWindowExhausted),
	)
	if len(diagnosis.Endpoints) == 1 {
		endpoint := diagnosis.Endpoints[0]
		if endpoint.Endpoint != "" {
			*attrs = append(*attrs, attribute.String("rpc.mux.filtered_endpoint", endpoint.Endpoint))
		}
		if probeConnectionID == "" && endpoint.ConnectionID != "" {
			*attrs = append(*attrs, attribute.String(muxTraceConnectionIDKey, endpoint.ConnectionID))
		}
		if endpoint.PoolSlot > 0 {
			*attrs = append(*attrs, attribute.Int("rpc.mux.filtered_pool_slot", endpoint.PoolSlot))
		}
	}
}

// AnnotateMuxDiagnosisSpan attaches mux diagnosis attributes to the current OTel span.
func AnnotateMuxDiagnosisSpan(ctx context.Context, probe RPCDiagnosisProbe) {
	if ctx == nil {
		return
	}
	span := oteltrace.SpanFromContext(ctx)
	if span == nil || !span.SpanContext().IsValid() {
		return
	}
	attrs := MuxTraceAttributes(probe)
	if len(attrs) == 0 {
		return
	}
	span.SetAttributes(attrs...)
}
