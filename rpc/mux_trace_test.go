package rpc

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestMuxTraceAttributesIncludeConnectionDiagnosis(t *testing.T) {
	probe := RPCDiagnosisProbe{
		Endpoint:     "tcp://127.0.0.1:9000",
		ConnectionID: "muxconn-7",
		PoolSlot:     2,
		FlowControl:  "credit_wait_timeout",
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{Manager: RPCMuxConnectionManagerDiagnosis{
			Enabled: true,
			Mode:    "experimental_mux_manager",
			FlowControl: RPCMuxFlowControlDiagnosis{
				CreditWaitTimeouts:        3,
				ConnectionWindowExhausted: 1,
			},
			Endpoints: []ExperimentalMuxEndpointSnapshot{{
				Endpoint:     "tcp://127.0.0.1:9000",
				ConnectionID: "muxconn-7",
				PoolSlot:     2,
			}},
		}}},
	}

	attrs := muxAttributeMap(MuxTraceAttributes(probe))
	for key, want := range map[string]string{
		"rpc.mux.endpoint":           "tcp://127.0.0.1:9000",
		"rpc.mux.connection_id":      "muxconn-7",
		"rpc.mux.flow_control.event": "credit_wait_timeout",
		"rpc.mux.mode":               "experimental_mux_manager",
		"rpc.mux.filtered_endpoint":  "tcp://127.0.0.1:9000",
	} {
		if got := attrs[key].AsString(); got != want {
			t.Fatalf("attr %s = %q, want %q (attrs=%v)", key, got, want, attrs)
		}
	}
	if got := attrs["rpc.mux.pool_slot"].AsInt64(); got != 2 {
		t.Fatalf("pool slot attr = %d, want 2", got)
	}
	if got := attrs["rpc.mux.flow_control.credit_wait_timeout.count"].AsInt64(); got != 3 {
		t.Fatalf("credit timeout attr = %d, want 3", got)
	}
	if got := attrs["rpc.mux.flow_control.connection_window_exhausted.count"].AsInt64(); got != 1 {
		t.Fatalf("window exhausted attr = %d, want 1", got)
	}
}

func TestAnnotateMuxDiagnosisSpanSetsTraceOnlyConnectionAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	ctx, span := provider.Tracer("rpc-mux-test").Start(context.Background(), "mux-diagnosis", oteltrace.WithSpanKind(oteltrace.SpanKindInternal))

	AnnotateMuxDiagnosisSpan(ctx, RPCDiagnosisProbe{
		Endpoint:     "tcp://127.0.0.1:9001",
		ConnectionID: "muxconn-9",
		PoolSlot:     1,
		FlowControl:  "write_timeout",
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{Manager: RPCMuxConnectionManagerDiagnosis{
			Enabled:     true,
			Mode:        "experimental_mux_manager",
			FlowControl: RPCMuxFlowControlDiagnosis{WriteTimeouts: 1},
		}}},
	})
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	attrs := muxAttributeMap(spans[0].Attributes())
	if got := attrs["rpc.mux.connection_id"].AsString(); got != "muxconn-9" {
		t.Fatalf("trace connection id attr = %q, want muxconn-9 (attrs=%v)", got, attrs)
	}
	if got := attrs["rpc.mux.flow_control.event"].AsString(); got != "write_timeout" {
		t.Fatalf("trace flow event attr = %q, want write_timeout", got)
	}
	if got := attrs["rpc.mux.flow_control.write_timeout.count"].AsInt64(); got != 1 {
		t.Fatalf("write timeout attr = %d, want 1", got)
	}
}

func muxAttributeMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}
