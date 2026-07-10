package rpc

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

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
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{
			FlowControl: RPCMuxFlowControlDiagnosis{
				CreditWaitTimeouts:        3,
				ConnectionWindowExhausted: 1,
			},
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled: true,
				Mode:    "experimental_mux_manager",
				FlowControl: RPCMuxFlowControlDiagnosis{
					CreditWaitTimeouts:        5,
					ConnectionWindowExhausted: 2,
				},
				Endpoints: []ExperimentalMuxEndpointSnapshot{{
					Endpoint:     "tcp://127.0.0.1:9000",
					ConnectionID: "muxconn-7",
					PoolSlot:     2,
				}},
			},
		}},
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
	if got := attrs["rpc.mux.manager.flow_control.credit_wait_timeout.count"].AsInt64(); got != 5 {
		t.Fatalf("manager credit timeout attr = %d, want 5", got)
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
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{
			FlowControl: RPCMuxFlowControlDiagnosis{WriteTimeouts: 1},
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled:     true,
				Mode:        "experimental_mux_manager",
				FlowControl: RPCMuxFlowControlDiagnosis{WriteTimeouts: 2},
			},
		}},
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
	if got := attrs["rpc.mux.manager.flow_control.write_timeout.count"].AsInt64(); got != 2 {
		t.Fatalf("manager write timeout attr = %d, want 2", got)
	}
}

func TestMuxDiagnosisLogAttrsIncludeTraceOnlyTroubleshootingFields(t *testing.T) {
	probe := RPCDiagnosisProbe{
		Target:      "http://unused",
		Method:      "orders/Watch",
		Endpoint:    "tcp://127.0.0.1:9002",
		FlowControl: "write_timeout",
		Matched:     true,
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled:         true,
				Mode:            "experimental_mux_manager",
				OpenRetries:     1,
				PoolExhaustions: 1,
				LastRetriedFrom: "tcp://127.0.0.1:9001",
				LastRetriedTo:   "tcp://127.0.0.1:9002",
				Endpoints: []ExperimentalMuxEndpointSnapshot{{
					Endpoint:     "tcp://127.0.0.1:9002",
					ConnectionID: "muxconn-3",
					PoolSlot:     2,
				}},
				Health: []ExperimentalMuxEndpointHealthSnapshot{{
					Endpoint: "tcp://127.0.0.1:9001",
					Reason:   "pool_exhausted",
					Ejected:  true,
					Cooldown: 50 * time.Millisecond,
				}},
			},
		}},
	}
	probe.Diagnosis.Mux.Events = RPCMuxDiagnosisEvents(probe.Diagnosis.Mux)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := &HTTPClient{opts: clientOptions{muxLog: true, muxLogger: logger}}

	client.logMuxStreamDiagnosis(context.Background(), probe, nil)

	line := buf.String()
	for _, want := range []string{
		`"msg":"rpc mux stream diagnosis"`,
		`"connection_id":"muxconn-3"`,
		`"pool_slot":2`,
		`"last_retried_from":"tcp://127.0.0.1:9001"`,
		`"last_retried_to":"tcp://127.0.0.1:9002"`,
		`"flow_control_event":"write_timeout"`,
		`"health_reason":"pool_exhausted"`,
		`"msg":"rpc mux runtime event"`,
		`"event_family":"retry"`,
		`"event":"open_before_retry"`,
		`"event_family":"health"`,
		`"event":"endpoint_cooldown"`,
		`"reason":"pool_exhausted"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("mux diagnosis log missing %s:\n%s", want, line)
		}
	}
}

func TestMuxDiagnosisEventExporterFiltersStructuredEvents(t *testing.T) {
	probe := RPCDiagnosisProbe{
		Target:   "http://unused",
		Method:   "orders/Watch",
		Endpoint: "tcp://127.0.0.1:9001",
		Matched:  true,
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled:         true,
				OpenRetries:     1,
				LastRetriedFrom: "tcp://127.0.0.1:9000",
				LastRetriedTo:   "tcp://127.0.0.1:9001",
				Health: []ExperimentalMuxEndpointHealthSnapshot{{
					Endpoint: "tcp://127.0.0.1:9001",
					Reason:   "dial_failure",
					Ejected:  true,
					Cooldown: 50 * time.Millisecond,
				}},
			},
		}},
	}
	probe.Diagnosis.Mux.Events = RPCMuxDiagnosisEvents(probe.Diagnosis.Mux)
	var records []RPCMuxDiagnosisEventRecord
	client := &HTTPClient{opts: clientOptions{
		muxEventFilter: RPCMuxDiagnosisFilter{EventFamily: "health"},
		muxEventExporter: RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record RPCMuxDiagnosisEventRecord) {
			records = append(records, record)
		}),
	}}

	client.exportMuxDiagnosisEvents(context.Background(), probe)

	if len(records) != 1 {
		t.Fatalf("exported mux records = %+v, want one health event", records)
	}
	record := records[0]
	if record.Target != "http://unused" ||
		record.Method != "orders/Watch" ||
		record.Endpoint != "tcp://127.0.0.1:9001" ||
		record.Event.Family != "health" ||
		record.Event.Event != "endpoint_cooldown" ||
		record.Event.Reason != "dial_failure" ||
		record.Event.Cooldown != 50*time.Millisecond ||
		record.ExportedAt.IsZero() {
		t.Fatalf("exported mux record = %+v, want structured health event", record)
	}
}

func TestSlogRPCMuxDiagnosisEventExporterEmitsStructuredEvent(t *testing.T) {
	var buf bytes.Buffer
	exporter := NewSlogRPCMuxDiagnosisEventExporter(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{
		Target:       "http://unused",
		Method:       "orders/Watch",
		Endpoint:     "tcp://127.0.0.1:9002",
		ConnectionID: "muxconn-11",
		PoolSlot:     2,
		ExportedAt:   time.Unix(1, 0),
		Event:        RPCMuxDiagnosisEvent{Family: "flow_control", Event: "write_timeout", Count: 1},
		Probe:        RPCDiagnosisProbe{Target: "http://unused", Method: "orders/Watch"},
	})

	line := buf.String()
	for _, want := range []string{
		`"msg":"rpc mux exported event"`,
		`"event_family":"flow_control"`,
		`"event":"write_timeout"`,
		`"endpoint":"tcp://127.0.0.1:9002"`,
		`"connection_id":"muxconn-11"`,
		`"pool_slot":2`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("exported mux event log missing %s:\n%s", want, line)
		}
	}
}

func TestRPCMuxDiagnosisEventsDeriveRetryHealthFlowControlAndDrain(t *testing.T) {
	diagnosis := RPCMuxTransportDiagnosis{
		FlowControl: RPCMuxFlowControlDiagnosis{WriteTimeouts: 2},
		Drain:       RPCMuxDrainDiagnosis{DrainReason: "rolling_restart", GoAwayFramesOut: 1},
		Manager: RPCMuxConnectionManagerDiagnosis{
			Enabled:         true,
			OpenRetries:     1,
			LastRetriedFrom: "tcp://127.0.0.1:9001",
			LastRetriedTo:   "tcp://127.0.0.1:9002",
			RetryReasons:    map[string]int64{"pool_exhausted": 1},
			FlowControl: RPCMuxFlowControlDiagnosis{
				CreditWaitTimeouts: 1,
			},
			Endpoints: []ExperimentalMuxEndpointSnapshot{{
				Endpoint:     "tcp://127.0.0.1:9002",
				ConnectionID: "muxconn-2",
				PoolSlot:     3,
			}},
			Health: []ExperimentalMuxEndpointHealthSnapshot{{
				Endpoint: "tcp://127.0.0.1:9001",
				Reason:   "pool_exhausted",
				Ejected:  true,
				Cooldown: 25 * time.Millisecond,
			}},
			CloseReasons: map[string]int64{"idle": 2},
			DrainReasons: map[string]int64{"resolver_update": 1},
		},
	}

	events := RPCMuxDiagnosisEvents(diagnosis)
	byKey := make(map[string]RPCMuxDiagnosisEvent, len(events))
	for _, event := range events {
		key := event.Family + "/" + event.Event + "/" + event.Reason
		byKey[key] = event
	}
	if event := byKey["flow_control/write_timeout/"]; event.Count != 2 {
		t.Fatalf("write timeout event = %+v, want count 2", event)
	}
	if event := byKey["flow_control/credit_wait_timeout/"]; event.Count != 1 || event.ConnectionID != "muxconn-2" || event.PoolSlot != 3 {
		t.Fatalf("manager flow-control event = %+v, want endpoint connection context", event)
	}
	if event := byKey["retry/open_before_retry/"]; event.Count != 1 || event.From != "tcp://127.0.0.1:9001" || event.To != "tcp://127.0.0.1:9002" {
		t.Fatalf("retry event = %+v, want retry source and target", event)
	}
	if event := byKey["retry/retry_reason/pool_exhausted"]; event.Count != 1 {
		t.Fatalf("retry reason event = %+v, want pool_exhausted count", event)
	}
	if event := byKey["health/endpoint_cooldown/pool_exhausted"]; event.Endpoint != "tcp://127.0.0.1:9001" || event.Cooldown != 25*time.Millisecond {
		t.Fatalf("health event = %+v, want cooldown endpoint", event)
	}
	if event := byKey["drain/goaway_out/rolling_restart"]; event.Count != 1 || event.Direction != "out" {
		t.Fatalf("drain goaway event = %+v, want out direction", event)
	}
	if event := byKey["lifecycle/close/idle"]; event.Count != 2 {
		t.Fatalf("close event = %+v, want idle count", event)
	}
	if event := byKey["drain/manager_drain/resolver_update"]; event.Count != 1 {
		t.Fatalf("manager drain event = %+v, want resolver update count", event)
	}
}

func TestMuxTraceAttributesIncludeManagerRetryAndCooldownDiagnosis(t *testing.T) {
	probe := RPCDiagnosisProbe{
		Endpoint: "tcp://127.0.0.1:9002",
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled:           true,
				Mode:              "experimental_mux_manager",
				PoolExhaustions:   1,
				DialFailures:      2,
				EndpointEjections: 3,
				OpenRetries:       4,
				LastRetriedFrom:   "tcp://127.0.0.1:9001",
				LastRetriedTo:     "tcp://127.0.0.1:9002",
				RetryReasons:      map[string]int64{"pool_exhausted": 1, "dial_failure": 2, "open_stream": 3},
				Health: []ExperimentalMuxEndpointHealthSnapshot{{
					Endpoint:      "tcp://127.0.0.1:9001",
					Ejected:       true,
					Reason:        "pool_exhausted",
					Cooldown:      25 * time.Millisecond,
					CooldownUntil: time.Unix(0, 123),
				}},
			},
		}},
	}

	attrs := muxAttributeMap(MuxTraceAttributes(probe))
	for key, want := range map[string]int64{
		"rpc.mux.manager.pool_exhaustions.count":            1,
		"rpc.mux.manager.dial_failures.count":               2,
		"rpc.mux.manager.endpoint_ejections.count":          3,
		"rpc.mux.manager.open_retries.count":                4,
		"rpc.mux.manager.retry_reason.pool_exhausted.count": 1,
		"rpc.mux.manager.retry_reason.dial_failure.count":   2,
		"rpc.mux.manager.retry_reason.open_stream.count":    3,
		"rpc.mux.manager.health.count":                      1,
		"rpc.mux.manager.health.cooldown_ms":                25,
	} {
		if got := attrs[key].AsInt64(); got != want {
			t.Fatalf("attr %s = %d, want %d (attrs=%v)", key, got, want, attrs)
		}
	}
	for key, want := range map[string]string{
		"rpc.mux.manager.last_retried_from":               "tcp://127.0.0.1:9001",
		"rpc.mux.manager.last_retried_to":                 "tcp://127.0.0.1:9002",
		"rpc.mux.manager.health.endpoint":                 "tcp://127.0.0.1:9001",
		"rpc.mux.manager.health.reason":                   "pool_exhausted",
		"rpc.mux.manager.health.cooldown":                 "25ms",
		"rpc.mux.manager.health.cooldown_until_unix_nano": "123",
	} {
		if got := attrs[key].AsString(); got != want {
			t.Fatalf("attr %s = %q, want %q (attrs=%v)", key, got, want, attrs)
		}
	}
	if got := attrs["rpc.mux.manager.health.ejected"].AsBool(); !got {
		t.Fatalf("health ejected attr = %v, want true", got)
	}
}

func muxAttributeMap(attrs []attribute.KeyValue) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		out[string(attr.Key)] = attr.Value
	}
	return out
}
