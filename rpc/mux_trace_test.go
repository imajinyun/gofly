package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
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
				CreditWaitTimeouts:                      3,
				ConnectionWindowExhausted:               1,
				FragmentBackpressure:                    4,
				FragmentFramesIn:                        2,
				FragmentFramesOut:                       3,
				FragmentStreamWindowUpdatePolicy:        experimentalMuxWindowUpdateOnReceive,
				FragmentConnectionWindowUpdatePolicy:    experimentalMuxWindowUpdateOnReceive,
				FragmentStreamWindowRefillRatio:         0.5,
				FragmentConnectionWindowRefillRatio:     0.25,
				FragmentMaxDeferredFragments:            4,
				FragmentWindowRefills:                   2,
				FragmentWindowRefillLatencyTotal:        12 * time.Millisecond,
				FragmentWindowRefillLatencyMax:          8 * time.Millisecond,
				FragmentWindowRefillLatencyAvg:          6 * time.Millisecond,
				FragmentDeferredStreamWindowUpdates:     2,
				FragmentDeferredConnectionWindowUpdates: 3,
				FragmentWindowPolicyRisk:                true,
				FragmentWindowPolicyRiskReason:          experimentalMuxFragmentPolicyRiskMaxDeferred,
				FragmentEstimatedMaxFragments:           6,
			},
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled: true,
				Mode:    "experimental_mux_manager",
				FlowControl: RPCMuxFlowControlDiagnosis{
					CreditWaitTimeouts:                      5,
					ConnectionWindowExhausted:               2,
					FragmentBackpressure:                    6,
					FragmentFramesIn:                        4,
					FragmentFramesOut:                       5,
					FragmentStreamWindowUpdatePolicy:        experimentalMuxWindowUpdateOnReceive,
					FragmentConnectionWindowUpdatePolicy:    experimentalMuxWindowUpdatePerFragment,
					FragmentStreamWindowRefillRatio:         0.75,
					FragmentConnectionWindowRefillRatio:     1,
					FragmentMaxDeferredFragments:            3,
					FragmentWindowRefills:                   3,
					FragmentWindowRefillLatencyTotal:        30 * time.Millisecond,
					FragmentWindowRefillLatencyMax:          15 * time.Millisecond,
					FragmentWindowRefillLatencyAvg:          10 * time.Millisecond,
					FragmentDeferredStreamWindowUpdates:     4,
					FragmentDeferredConnectionWindowUpdates: 0,
					FragmentWindowPolicyRisk:                true,
					FragmentWindowPolicyRiskReason:          experimentalMuxFragmentPolicyRiskStream,
					FragmentEstimatedMaxFragments:           5,
				},
				RefillProfile: RPCMuxRefillProfile{
					StreamWindowUpdatePolicy:     experimentalMuxWindowUpdateOnReceive,
					ConnectionWindowUpdatePolicy: experimentalMuxWindowUpdatePerFragment,
					StreamWindowRefillRatio:      0.75,
					ConnectionWindowRefillRatio:  1,
					MaxDeferredFragments:         3,
					Refills:                      3,
					RefillLatencyMax:             15 * time.Millisecond,
					RefillLatencyAvg:             10 * time.Millisecond,
					LastFlowControlEvent:         "fragment_window_refill",
					LastBackpressureEvent:        "credit_wait_timeout",
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
	if got := attrs["rpc.mux.flow_control.fragment_stream_window_refill_ratio"].AsFloat64(); got != 0.5 {
		t.Fatalf("stream refill ratio attr = %v, want 0.5", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_connection_window_refill_ratio"].AsFloat64(); got != 0.25 {
		t.Fatalf("connection refill ratio attr = %v, want 0.25", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_max_deferred_fragments"].AsInt64(); got != 4 {
		t.Fatalf("max deferred fragments attr = %d, want 4", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_window_refill.count"].AsInt64(); got != 2 {
		t.Fatalf("fragment window refill attr = %d, want 2", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_window_refill_latency_total.ns"].AsInt64(); got != int64(12*time.Millisecond) {
		t.Fatalf("fragment refill latency total attr = %d, want 12ms", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_window_refill_latency_max.ns"].AsInt64(); got != int64(8*time.Millisecond) {
		t.Fatalf("fragment refill latency max attr = %d, want 8ms", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_window_refill_latency_avg.ns"].AsInt64(); got != int64(6*time.Millisecond) {
		t.Fatalf("fragment refill latency avg attr = %d, want 6ms", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_stream_window_refill_ratio"].AsFloat64(); got != 0.75 {
		t.Fatalf("manager stream refill ratio attr = %v, want 0.75", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_connection_window_refill_ratio"].AsFloat64(); got != 1 {
		t.Fatalf("manager connection refill ratio attr = %v, want 1", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_max_deferred_fragments"].AsInt64(); got != 3 {
		t.Fatalf("manager max deferred fragments attr = %d, want 3", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_window_refill.count"].AsInt64(); got != 3 {
		t.Fatalf("manager fragment window refill attr = %d, want 3", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_window_refill_latency_total.ns"].AsInt64(); got != int64(30*time.Millisecond) {
		t.Fatalf("manager fragment refill latency total attr = %d, want 30ms", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_window_refill_latency_max.ns"].AsInt64(); got != int64(15*time.Millisecond) {
		t.Fatalf("manager fragment refill latency max attr = %d, want 15ms", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_window_refill_latency_avg.ns"].AsInt64(); got != int64(10*time.Millisecond) {
		t.Fatalf("manager fragment refill latency avg attr = %d, want 10ms", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.stream_window_update_policy"].AsString(); got != experimentalMuxWindowUpdateOnReceive {
		t.Fatalf("manager refill profile stream policy attr = %q, want on_receive", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.connection_window_update_policy"].AsString(); got != experimentalMuxWindowUpdatePerFragment {
		t.Fatalf("manager refill profile connection policy attr = %q, want per_fragment", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.stream_window_refill_ratio"].AsFloat64(); got != 0.75 {
		t.Fatalf("manager refill profile stream ratio attr = %v, want 0.75", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.connection_window_refill_ratio"].AsFloat64(); got != 1 {
		t.Fatalf("manager refill profile connection ratio attr = %v, want 1", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.max_deferred_fragments"].AsInt64(); got != 3 {
		t.Fatalf("manager refill profile max deferred attr = %d, want 3", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.refills.count"].AsInt64(); got != 3 {
		t.Fatalf("manager refill profile refills attr = %d, want 3", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.refill_latency_max.ns"].AsInt64(); got != int64(15*time.Millisecond) {
		t.Fatalf("manager refill profile max latency attr = %d, want 15ms", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.refill_latency_avg.ns"].AsInt64(); got != int64(10*time.Millisecond) {
		t.Fatalf("manager refill profile avg latency attr = %d, want 10ms", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.last_flow_control_event"].AsString(); got != "fragment_window_refill" {
		t.Fatalf("manager refill profile last flow-control attr = %q, want fragment_window_refill", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.last_backpressure_event"].AsString(); got != "credit_wait_timeout" {
		t.Fatalf("manager refill profile last backpressure attr = %q, want credit_wait_timeout", got)
	}
	if got := attrs["rpc.mux.flow_control.connection_window_exhausted.count"].AsInt64(); got != 1 {
		t.Fatalf("window exhausted attr = %d, want 1", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.credit_wait_timeout.count"].AsInt64(); got != 5 {
		t.Fatalf("manager credit timeout attr = %d, want 5", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_backpressure.count"].AsInt64(); got != 4 {
		t.Fatalf("fragment backpressure attr = %d, want 4", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_frames_in.count"].AsInt64(); got != 2 {
		t.Fatalf("fragment frames in attr = %d, want 2", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_frames_out.count"].AsInt64(); got != 3 {
		t.Fatalf("fragment frames out attr = %d, want 3", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_backpressure.count"].AsInt64(); got != 6 {
		t.Fatalf("manager fragment backpressure attr = %d, want 6", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_stream_window_update_policy"].AsString(); got != experimentalMuxWindowUpdateOnReceive {
		t.Fatalf("fragment stream window policy attr = %q, want on_receive", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_deferred_connection_window_updates.count"].AsInt64(); got != 3 {
		t.Fatalf("fragment deferred connection updates attr = %d, want 3", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_connection_window_update_policy"].AsString(); got != experimentalMuxWindowUpdatePerFragment {
		t.Fatalf("manager fragment connection window policy attr = %q, want per_fragment", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_deferred_stream_window_updates.count"].AsInt64(); got != 4 {
		t.Fatalf("manager fragment deferred stream updates attr = %d, want 4", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_window_policy_risk"].AsBool(); !got {
		t.Fatalf("fragment policy risk attr = %v, want true", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_window_policy_risk_reason"].AsString(); got != experimentalMuxFragmentPolicyRiskMaxDeferred {
		t.Fatalf("fragment policy risk reason attr = %q, want max deferred risk", got)
	}
	if got := attrs["rpc.mux.flow_control.fragment_estimated_max_fragments"].AsInt64(); got != 6 {
		t.Fatalf("fragment estimated max fragments attr = %d, want 6", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_window_policy_risk"].AsBool(); !got {
		t.Fatalf("manager fragment policy risk attr = %v, want true", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_window_policy_risk_reason"].AsString(); got != experimentalMuxFragmentPolicyRiskStream {
		t.Fatalf("manager fragment policy risk reason attr = %q, want stream risk", got)
	}
	if got := attrs["rpc.mux.manager.flow_control.fragment_estimated_max_fragments"].AsInt64(); got != 5 {
		t.Fatalf("manager fragment estimated max fragments attr = %d, want 5", got)
	}
}

func TestMuxTraceAndLogAttributesIncludeNegotiatedMTLSSuccess(t *testing.T) {
	probe := RPCDiagnosisProbe{
		Target:   "http://unused",
		Method:   "orders/Watch",
		Endpoint: "tcp://127.0.0.1:9003",
		Matched:  true,
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled: true,
				Mode:    "experimental_mux_manager",
				Candidate: ExperimentalMuxCandidateSnapshot{
					Enabled:            true,
					Protocol:           "gofly-mux/mtls-test",
					NegotiatedProtocol: "gofly-mux/mtls-test",
					TLS:                true,
					MutualTLS:          true,
				},
				Endpoints: []ExperimentalMuxEndpointSnapshot{{
					Endpoint:     "tcp://127.0.0.1:9003",
					ConnectionID: "muxconn-12",
					PoolSlot:     1,
					Adapter: ExperimentalMuxAdapterSnapshot{Candidate: ExperimentalMuxCandidateSnapshot{
						Enabled:            true,
						Protocol:           "gofly-mux/mtls-test",
						NegotiatedProtocol: "gofly-mux/mtls-test",
						TLS:                true,
						MutualTLS:          true,
					}},
				}},
			},
		}},
	}

	attrs := muxAttributeMap(MuxTraceAttributes(probe))
	if got := attrs["rpc.mux.candidate.tls"].AsBool(); !got {
		t.Fatalf("trace tls attr = %v, want true (attrs=%v)", got, attrs)
	}
	if got := attrs["rpc.mux.candidate.mutual_tls"].AsBool(); !got {
		t.Fatalf("trace mutual tls attr = %v, want true", got)
	}
	if got := attrs["rpc.mux.candidate.negotiated_protocol"].AsString(); got != "gofly-mux/mtls-test" {
		t.Fatalf("trace negotiated protocol attr = %q, want gofly-mux/mtls-test (attrs=%v)", got, attrs)
	}
	if got := attrs["rpc.mux.candidate.protocol"].AsString(); got != "gofly-mux/mtls-test" {
		t.Fatalf("trace candidate protocol attr = %q, want gofly-mux/mtls-test", got)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := &HTTPClient{opts: clientOptions{muxLog: true, muxLogger: logger}}
	client.logMuxStreamDiagnosis(context.Background(), probe, nil)
	line := buf.String()
	for _, want := range []string{
		`"tls":true`,
		`"mutual_tls":true`,
		`"negotiated_protocol":"gofly-mux/mtls-test"`,
		`"candidate_protocol":"gofly-mux/mtls-test"`,
		`"connection_id":"muxconn-12"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("mux success log missing %s:\n%s", want, line)
		}
	}
}

func TestMuxTraceAttributesDoNotTreatCandidateConfigAsNegotiatedSuccess(t *testing.T) {
	probe := RPCDiagnosisProbe{
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled: true,
				Candidate: ExperimentalMuxCandidateSnapshot{
					Enabled:            true,
					Protocol:           "gofly-mux/config-only",
					NegotiatedProtocol: "gofly-mux/config-only",
					TLS:                true,
					MutualTLS:          true,
				},
			},
		}},
	}

	attrs := muxAttributeMap(MuxTraceAttributes(probe))
	if got := attrs["rpc.mux.candidate.tls"].AsBool(); !got {
		t.Fatalf("trace tls attr = %v, want true", got)
	}
	if got := attrs["rpc.mux.candidate.mutual_tls"].AsBool(); !got {
		t.Fatalf("trace mutual tls attr = %v, want true", got)
	}
	if _, ok := attrs["rpc.mux.candidate.negotiated_protocol"]; ok {
		t.Fatalf("trace attrs = %v, config-only candidate must not report negotiated protocol", attrs)
	}
	if got := attrs["rpc.mux.candidate.protocol"].AsString(); got != "gofly-mux/config-only" {
		t.Fatalf("trace candidate protocol attr = %q, want config-only protocol", got)
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

func TestObserveMuxDiagnosisEmitsConfiguredRefillProfileTraceLogAndEvents(t *testing.T) {
	probe := RPCDiagnosisProbe{
		Target:      "http://unused",
		Method:      "orders/Watch",
		Endpoint:    "tcp://127.0.0.1:9002",
		FlowControl: "fragment_window_refill",
		Matched:     true,
		Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{
			Manager: RPCMuxConnectionManagerDiagnosis{
				Enabled: true,
				Mode:    "experimental_mux_manager",
				FlowControl: RPCMuxFlowControlDiagnosis{
					FragmentWindowRefills:               2,
					FragmentStreamWindowRefillRatio:     0.5,
					FragmentConnectionWindowRefillRatio: 0.25,
					FragmentMaxDeferredFragments:        2,
				},
				RefillProfile: RPCMuxRefillProfile{
					StreamWindowUpdatePolicy:     experimentalMuxWindowUpdateOnReceive,
					ConnectionWindowUpdatePolicy: experimentalMuxWindowUpdateOnReceive,
					StreamWindowRefillRatio:      0.5,
					ConnectionWindowRefillRatio:  0.25,
					MaxDeferredFragments:         2,
					Refills:                      2,
					RefillLatencyMax:             4 * time.Millisecond,
					RefillLatencyAvg:             2 * time.Millisecond,
					LastFlowControlEvent:         "fragment_window_refill",
					LastBackpressureEvent:        "fragment_backpressure",
				},
				Endpoints: []ExperimentalMuxEndpointSnapshot{{
					Endpoint:     "tcp://127.0.0.1:9002",
					ConnectionID: "muxconn-4",
					PoolSlot:     1,
				}},
			},
		}},
	}
	probe.Diagnosis.Mux.Events = RPCMuxDiagnosisEvents(probe.Diagnosis.Mux)
	var buf bytes.Buffer
	var exported []RPCMuxDiagnosisEventRecord
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client, err := NewClient("http://unused",
		WithMuxTraceAnnotation(),
		WithMuxDiagnosisLogging(logger),
		WithMuxDiagnosisEventExporter(RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record RPCMuxDiagnosisEventRecord) {
			exported = append(exported, record)
		}), RPCMuxDiagnosisFilter{EventFamily: "flow_control", Event: "fragment_window_refill"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	ctx, span := provider.Tracer("rpc-mux-test").Start(context.Background(), "observe-refill-profile", oteltrace.WithSpanKind(oteltrace.SpanKindInternal))

	client.ObserveMuxDiagnosis(ctx, probe)
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("trace spans = %d, want one refillProfile diagnosis span", len(spans))
	}
	attrs := muxAttributeMap(spans[0].Attributes())
	if got := attrs["rpc.mux.manager.refill_profile.refills.count"].AsInt64(); got != 2 {
		t.Fatalf("refill profile refills attr = %d, want 2", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.stream_window_refill_ratio"].AsFloat64(); got != 0.5 {
		t.Fatalf("refill profile stream ratio attr = %v, want 0.5", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.connection_window_refill_ratio"].AsFloat64(); got != 0.25 {
		t.Fatalf("refill profile connection ratio attr = %v, want 0.25", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.max_deferred_fragments"].AsInt64(); got != 2 {
		t.Fatalf("refill profile max deferred attr = %d, want 2", got)
	}
	if got := attrs["rpc.mux.manager.refill_profile.last_flow_control_event"].AsString(); got != "fragment_window_refill" {
		t.Fatalf("refill profile last flow-control attr = %q, want fragment_window_refill", got)
	}
	line := buf.String()
	for _, want := range []string{
		`"msg":"rpc mux stream diagnosis"`,
		`"refill_profile_stream_window_refill_ratio":0.5`,
		`"refill_profile_connection_window_refill_ratio":0.25`,
		`"refill_profile_max_deferred_fragments":2`,
		`"refill_profile_refills":2`,
		`"refill_profile_last_flow_control_event":"fragment_window_refill"`,
		`"msg":"rpc mux runtime event"`,
		`"event_family":"flow_control"`,
		`"event":"fragment_window_refill"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("mux refillProfile diagnosis log missing %s:\n%s", want, line)
		}
	}
	if len(exported) != 1 {
		t.Fatalf("exported refillProfile events = %+v, want one fragment_window_refill record", exported)
	}
	record := exported[0]
	if record.Target != "http://unused" ||
		record.Method != "orders/Watch" ||
		record.Endpoint != "tcp://127.0.0.1:9002" ||
		record.ConnectionID != "muxconn-4" ||
		record.PoolSlot != 1 ||
		record.Event.Family != "flow_control" ||
		record.Event.Event != "fragment_window_refill" ||
		record.Event.Count != 2 ||
		record.ExportedAt.IsZero() {
		t.Fatalf("exported refillProfile record = %+v, want structured fragment_window_refill event", record)
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

func TestMuxDiagnosisEventOTelLogRecordMapsStableAttributes(t *testing.T) {
	record := RPCMuxDiagnosisEventRecord{
		Target:       "http://unused",
		Method:       "orders/Watch",
		Endpoint:     "tcp://127.0.0.1:9002",
		ConnectionID: "muxconn-12",
		PoolSlot:     3,
		ExportedAt:   time.Unix(2, 0),
		Event: RPCMuxDiagnosisEvent{
			Family:       "flow_control",
			Event:        "fragment_window_refill",
			Count:        2,
			Endpoint:     "tcp://127.0.0.1:9003",
			ConnectionID: "muxconn-13",
			PoolSlot:     4,
			Reason:       "window_refill",
		},
		Probe: RPCDiagnosisProbe{Target: "http://unused", Method: "orders/Watch"},
	}

	logRecord := MuxDiagnosisEventOTelLogRecord(record)

	if logRecord.Name != "rpc.mux.diagnosis_event" ||
		logRecord.Severity != "WARN" ||
		logRecord.Body != "rpc mux diagnosis event" ||
		!logRecord.Timestamp.Equal(time.Unix(2, 0)) ||
		logRecord.Event.Event != "fragment_window_refill" {
		t.Fatalf("otel log record = %+v, want stable mux diagnosis envelope", logRecord)
	}
	attrs := muxAttributeMap(logRecord.Attributes)
	for key, want := range map[string]string{
		"rpc.mux.event.family":  "flow_control",
		"rpc.mux.event.name":    "fragment_window_refill",
		"rpc.mux.target":        "http://unused",
		"rpc.mux.method":        "orders/Watch",
		"rpc.mux.endpoint":      "tcp://127.0.0.1:9003",
		"rpc.mux.connection_id": "muxconn-13",
		"rpc.mux.event.reason":  "window_refill",
	} {
		if got := attrs[key].AsString(); got != want {
			t.Fatalf("otel log attr %s = %q, want %q (attrs=%v)", key, got, want, attrs)
		}
	}
	if got := attrs["rpc.mux.pool_slot"].AsInt64(); got != 4 {
		t.Fatalf("otel log pool slot attr = %d, want 4", got)
	}
	if got := attrs["rpc.mux.event.count"].AsInt64(); got != 2 {
		t.Fatalf("otel log event count attr = %d, want 2", got)
	}
}

func TestRPCMuxOTelLogDiagnosisEventExporterForwardsRecord(t *testing.T) {
	var records []RPCMuxDiagnosisEventOTelLogRecord
	exporter := NewRPCMuxOTelLogDiagnosisEventExporter(RPCMuxOTelLogExporterFunc(func(_ context.Context, record RPCMuxDiagnosisEventOTelLogRecord) {
		records = append(records, record)
	}))

	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{
		Target:     "http://unused",
		Method:     "orders/Watch",
		Endpoint:   "tcp://127.0.0.1:9002",
		ExportedAt: time.Unix(3, 0),
		Event:      RPCMuxDiagnosisEvent{Family: "retry", Event: "open_before_retry", Count: 1, From: "tcp://bad", To: "tcp://ok"},
		Probe:      RPCDiagnosisProbe{Target: "http://unused", Method: "orders/Watch"},
	})

	if len(records) != 1 {
		t.Fatalf("otel log exported records = %+v, want one record", records)
	}
	record := records[0]
	if record.Severity != "INFO" || record.Event.Event != "open_before_retry" {
		t.Fatalf("otel log exported record = %+v, want retry info record", record)
	}
	attrs := muxAttributeMap(record.Attributes)
	if attrs["rpc.mux.event.from"].AsString() != "tcp://bad" ||
		attrs["rpc.mux.event.to"].AsString() != "tcp://ok" ||
		attrs["rpc.mux.endpoint"].AsString() != "tcp://ok" {
		t.Fatalf("otel log exported attrs = %+v, want retry routing fields", attrs)
	}
}

func TestSlogRPCMuxOTelLogExporterEmitsCompatibleRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	exporter := NewSlogRPCMuxOTelLogExporterWithProfile(logger, "generated-mtls-refill")

	exporter.ExportRPCMuxOTelLog(context.Background(), RPCMuxDiagnosisEventOTelLogRecord{
		Name:      "rpc.mux.diagnosis_event",
		Severity:  "WARN",
		Body:      "rpc mux diagnosis event",
		Timestamp: time.Unix(4, 0),
		Attributes: []attribute.KeyValue{
			attribute.String("rpc.mux.event.family", "flow_control"),
			attribute.String("rpc.mux.event.name", "fragment_window_refill"),
			attribute.String("rpc.mux.connection_id", "muxconn-22"),
			attribute.Int("rpc.mux.pool_slot", 2),
		},
		Event: RPCMuxDiagnosisEvent{Family: "flow_control", Event: "fragment_window_refill", Count: 1},
	})

	line := buf.String()
	for _, want := range []string{
		`"level":"WARN"`,
		`"msg":"rpc mux otel log event"`,
		`"otel_log_name":"rpc.mux.diagnosis_event"`,
		`"otel_log_severity":"WARN"`,
		`"otel_log_body":"rpc mux diagnosis event"`,
		`"otel_log_profile":"generated-mtls-refill"`,
		`"rpc_mux_event_family":"flow_control"`,
		`"rpc_mux_event_name":"fragment_window_refill"`,
		`"rpc_mux_connection_id":"muxconn-22"`,
		`"rpc_mux_pool_slot":2`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("otel-compatible mux event log missing %s:\n%s", want, line)
		}
	}
}

func TestRPCMuxOTelLogExporterBoundaryContracts(t *testing.T) {
	t.Run("nil bridge exporter is a no-op", func(t *testing.T) {
		exporter := NewRPCMuxOTelLogDiagnosisEventExporter(nil)
		exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{
			Event: RPCMuxDiagnosisEvent{Family: "retry", Event: "open_before_retry"},
		})
	})

	t.Run("default slog exporter emits info without profile", func(t *testing.T) {
		var buf bytes.Buffer
		exporter := NewSlogRPCMuxOTelLogExporter(
			slog.New(slog.NewJSONHandler(&buf, nil)),
		)
		exporter.ExportRPCMuxOTelLog(context.Background(), RPCMuxDiagnosisEventOTelLogRecord{
			Name:     "rpc.mux.diagnosis_event",
			Severity: "INFO",
			Body:     "rpc mux diagnosis event",
			Event: RPCMuxDiagnosisEvent{
				Family: "retry",
				Event:  "open_before_retry",
				Count:  1,
			},
		})
		line := buf.String()
		if !strings.Contains(line, `"level":"INFO"`) ||
			!strings.Contains(line, `"otel_log_name":"rpc.mux.diagnosis_event"`) ||
			!strings.Contains(line, `"otel_log_body":"rpc mux diagnosis event"`) {
			t.Fatalf("default slog exporter output = %s", line)
		}
		if strings.Contains(line, `"otel_log_profile"`) {
			t.Fatalf("default slog exporter unexpectedly emitted profile: %s", line)
		}
	})

	t.Run("nil logger falls back to slog default", func(t *testing.T) {
		exporter := NewSlogRPCMuxOTelLogExporter(nil)
		exporter.ExportRPCMuxOTelLog(context.Background(), RPCMuxDiagnosisEventOTelLogRecord{
			Name:     "rpc.mux.diagnosis_event",
			Severity: "INFO",
			Body:     "default logger boundary",
		})
	})
}

func TestMuxTraceRetryReasonContracts(t *testing.T) {
	reasons := map[string]int64{"dial_failure": 2}
	if got := withMuxRetryReason(reasons, ""); !reflect.DeepEqual(got, reasons) {
		t.Fatalf("empty retry reason changed map: got=%v want=%v", got, reasons)
	}
	reasons = withMuxRetryReason(nil, "pool_exhausted")
	if reasons["pool_exhausted"] != 1 {
		t.Fatalf("new retry reason map = %v, want pool_exhausted=1", reasons)
	}
	reasons["pool_exhausted"] = 3
	if got := withMuxRetryReason(reasons, "pool_exhausted"); got["pool_exhausted"] != 3 {
		t.Fatalf("existing retry reason count = %d, want 3", got["pool_exhausted"])
	}

	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "success", err: nil, want: ""},
		{name: "pool exhausted", err: NewError(CodeResourceExhausted, "pool"), want: "pool_exhausted"},
		{name: "dial failure", err: NewError(CodeUnavailable, "dial"), want: "dial_failure"},
		{name: "stream failure", err: NewError(CodeInvalidArgument, "stream"), want: "open_stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := muxTraceErrorReason(test.err); got != test.want {
				t.Fatalf("muxTraceErrorReason = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMuxEffectiveCandidateSnapshotPrecedence(t *testing.T) {
	candidate := func(protocol string) ExperimentalMuxCandidateSnapshot {
		return ExperimentalMuxCandidateSnapshot{Enabled: true, Protocol: protocol}
	}
	for _, test := range []struct {
		name      string
		diagnosis RPCMuxTransportDiagnosis
		want      string
		exact     bool
	}{
		{
			name: "single manager endpoint",
			diagnosis: RPCMuxTransportDiagnosis{
				Manager: RPCMuxConnectionManagerDiagnosis{
					Endpoints: []ExperimentalMuxEndpointSnapshot{{
						Adapter: ExperimentalMuxAdapterSnapshot{Candidate: candidate("endpoint")},
					}},
				},
				Adapter: ExperimentalMuxAdapterSnapshot{Candidate: candidate("adapter")},
			},
			want:  "endpoint",
			exact: true,
		},
		{
			name: "direct adapter",
			diagnosis: RPCMuxTransportDiagnosis{
				Manager: RPCMuxConnectionManagerDiagnosis{
					Endpoints: []ExperimentalMuxEndpointSnapshot{{}, {}},
				},
				Adapter: ExperimentalMuxAdapterSnapshot{Candidate: candidate("adapter")},
			},
			want:  "adapter",
			exact: true,
		},
		{
			name: "standalone candidate",
			diagnosis: RPCMuxTransportDiagnosis{
				Candidate: candidate("standalone"),
			},
			want:  "standalone",
			exact: true,
		},
		{
			name: "manager aggregate",
			diagnosis: RPCMuxTransportDiagnosis{
				Manager: RPCMuxConnectionManagerDiagnosis{
					Enabled:   true,
					Candidate: candidate("manager"),
				},
			},
			want:  "manager",
			exact: false,
		},
		{
			name:      "absent",
			diagnosis: RPCMuxTransportDiagnosis{},
			want:      "",
			exact:     false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, exact := muxEffectiveCandidateSnapshot(test.diagnosis)
			if got.Protocol != test.want || exact != test.exact {
				t.Fatalf("effective candidate = (%+v, %v), want protocol %q exact %v", got, exact, test.want, test.exact)
			}
		})
	}
}

func TestRPCMuxOTelLogSinkRegistryCreatesCustomExporter(t *testing.T) {
	const sinkName = "custom-otel"
	var records []RPCMuxDiagnosisEventOTelLogRecord
	cleanup := RegisterRPCMuxOTelLogSink("  "+sinkName+"  ", func(profile string) RPCMuxOTelLogExporter {
		if profile != "custom-profile" {
			t.Fatalf("custom mux otel sink profile = %q, want custom-profile", profile)
		}
		return RPCMuxOTelLogExporterFunc(func(_ context.Context, record RPCMuxDiagnosisEventOTelLogRecord) {
			records = append(records, record)
		})
	})
	defer cleanup()

	if !RPCMuxOTelLogSinkRegistered(sinkName) {
		t.Fatalf("registered mux otel sink %q not found", sinkName)
	}
	exporter := NewRPCMuxOTelLogSinkExporter(sinkName, "custom-profile")
	if exporter == nil {
		t.Fatal("custom mux otel sink exporter is nil")
	}
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{
		Target:     "http://unused",
		Method:     "orders/Watch",
		ExportedAt: time.Unix(5, 0),
		Event:      RPCMuxDiagnosisEvent{Family: "flow_control", Event: "write_timeout", Count: 1},
		Probe:      RPCDiagnosisProbe{Target: "http://unused", Method: "orders/Watch"},
	})

	if len(records) != 1 || records[0].Event.Event != "write_timeout" || records[0].Severity != "WARN" {
		t.Fatalf("custom mux otel sink records = %+v, want one write_timeout warning", records)
	}
	cleanup()
	if RPCMuxOTelLogSinkRegistered(sinkName) {
		t.Fatalf("custom mux otel sink %q still registered after cleanup", sinkName)
	}
	if exporter := NewRPCMuxOTelLogSinkExporter(sinkName, "custom-profile"); exporter != nil {
		t.Fatalf("custom mux otel sink exporter after cleanup = %#v, want nil", exporter)
	}
}

func TestRPCMuxOTelLogDiagnosisEventExporterCloseContracts(t *testing.T) {
	var closed atomic.Int64
	closable := NewRPCMuxOTelLogDiagnosisEventExporter(&sinkSetTestExporter{closed: &closed})
	closer, ok := closable.(io.Closer)
	if !ok {
		t.Fatalf("closable exporter type = %T, want io.Closer", closable)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if closed.Load() != 1 {
		t.Fatalf("close count = %d, want 1", closed.Load())
	}

	plain := NewRPCMuxOTelLogDiagnosisEventExporter(
		RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {}),
	)
	if err := plain.(io.Closer).Close(); err != nil {
		t.Fatalf("close non-closable exporter: %v", err)
	}
}

func TestRPCMuxOTelLogSinkProviderValidatesProfile(t *testing.T) {
	const sinkName = "validated-otel"
	var receivedProfile string
	cleanup := RegisterRPCMuxOTelLogSinkProvider(sinkName, testRPCMuxOTelLogSinkProvider{
		validate: func(profile string) error {
			if profile != "production" {
				return errors.New("profile must be production")
			}
			return nil
		},
		newExporter: func(profile string) RPCMuxOTelLogExporter {
			receivedProfile = profile
			return RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {})
		},
	})
	defer cleanup()

	if err := ValidateRPCMuxOTelLogSinkProfile(sinkName, "invalid"); err == nil || !strings.Contains(err.Error(), "profile must be production") {
		t.Fatalf("ValidateRPCMuxOTelLogSinkProfile invalid profile = %v, want provider error", err)
	}
	if exporter := NewRPCMuxOTelLogSinkExporter(sinkName, "invalid"); exporter != nil {
		t.Fatalf("invalid profile exporter = %#v, want nil", exporter)
	}
	if err := ValidateRPCMuxOTelLogSinkProfile(sinkName, " production "); err != nil {
		t.Fatalf("ValidateRPCMuxOTelLogSinkProfile valid profile: %v", err)
	}
	if exporter := NewRPCMuxOTelLogSinkExporter(sinkName, " production "); exporter == nil {
		t.Fatal("valid profile exporter is nil")
	}
	if receivedProfile != "production" {
		t.Fatalf("provider received profile = %q, want production", receivedProfile)
	}
	if err := ValidateRPCMuxOTelLogSinkProfile("missing", "production"); err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("ValidateRPCMuxOTelLogSinkProfile missing sink = %v, want registration error", err)
	}

	cleanupNil := RegisterRPCMuxOTelLogSinkProvider("nil-exporter", testRPCMuxOTelLogSinkProvider{})
	defer cleanupNil()
	if exporter := NewRPCMuxOTelLogSinkExporter("nil-exporter", "production"); exporter != nil {
		t.Fatalf("nil provider exporter = %#v, want nil", exporter)
	}
}

type testRPCMuxOTelLogSinkProvider struct {
	validate    func(string) error
	newExporter func(string) RPCMuxOTelLogExporter
	schema      json.RawMessage
	schemaPanic bool
}

func (p testRPCMuxOTelLogSinkProvider) ValidateRPCMuxOTelLogProfile(profile string) error {
	if p.validate == nil {
		return nil
	}
	return p.validate(profile)
}

func (p testRPCMuxOTelLogSinkProvider) NewRPCMuxOTelLogExporter(profile string) RPCMuxOTelLogExporter {
	if p.newExporter == nil {
		return nil
	}
	return p.newExporter(profile)
}

func (p testRPCMuxOTelLogSinkProvider) RPCMuxOTelLogProfileSchema() json.RawMessage {
	if p.schemaPanic {
		panic("schema provider panic")
	}
	return p.schema
}

func TestRPCMuxOTelLogSinkRegistrySnapshotAndTypedProfile(t *testing.T) {
	const sinkName = "typed-profile"
	cleanup := RegisterRPCMuxOTelLogSinkProvider(sinkName, testRPCMuxOTelLogSinkProvider{
		schema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"batchSize":{"type":"integer"}}}`),
	})
	defer cleanup()

	type profile struct {
		BatchSize int `json:"batchSize"`
	}
	var decoded profile
	if err := DecodeRPCMuxOTelLogProfile(`{"batchSize":32}`, &decoded); err != nil {
		t.Fatalf("DecodeRPCMuxOTelLogProfile valid profile: %v", err)
	}
	if decoded.BatchSize != 32 {
		t.Fatalf("decoded batch size = %d, want 32", decoded.BatchSize)
	}
	for _, raw := range []string{
		`{"batchSize":32,"unknown":true}`,
		`{"batchSize":32} {"batchSize":64}`,
	} {
		if err := DecodeRPCMuxOTelLogProfile(raw, &decoded); err == nil {
			t.Fatalf("DecodeRPCMuxOTelLogProfile(%q) succeeded, want strict error", raw)
		}
	}

	snapshot := RPCMuxOTelLogSinkRegistry()
	if len(snapshot.Capabilities) == 0 {
		t.Fatal("registry capabilities are empty")
	}
	for _, want := range []string{
		"subprocess_isolation_runner",
		"subprocess_delivery_audit",
		"subprocess_policy_error_categories",
		"operator_action_approval",
		"operator_action_history",
		"operator_history_integrity_envelope",
		"operator_history_compaction",
		"file_secret_resolver",
		"layered_secret_resolver",
	} {
		if !slices.Contains(snapshot.Capabilities, want) {
			t.Fatalf("registry capabilities = %+v, missing %q", snapshot.Capabilities, want)
		}
	}
	var found *RPCMuxOTelLogSinkSnapshot
	var subprocessFound *RPCMuxOTelLogSinkSnapshot
	for i := range snapshot.Sinks {
		if snapshot.Sinks[i].Name == sinkName {
			found = &snapshot.Sinks[i]
		}
		if snapshot.Sinks[i].Name == "subprocess" {
			subprocessFound = &snapshot.Sinks[i]
		}
	}
	if found == nil || !found.ClientExport || !found.ServerExport || !found.DeliveryGovernance || !json.Valid(found.ProfileSchema) {
		t.Fatalf("typed sink snapshot = %+v, want schema and symmetric governed export capabilities", found)
	}
	if subprocessFound == nil {
		t.Fatalf("registry sinks = %+v, want built-in subprocess sink", snapshot.Sinks)
	}
	for _, want := range []string{
		RPCMuxSubprocessPolicyErrorCommandDenied,
		RPCMuxSubprocessPolicyErrorWorkDirEscaped,
		RPCMuxSubprocessPolicyErrorEnvNotWhitelisted,
	} {
		if !slices.Contains(subprocessFound.ProfileValidationCategories, want) {
			t.Fatalf("subprocess validation categories = %+v, missing %q", subprocessFound.ProfileValidationCategories, want)
		}
	}
}

func TestRPCMuxOTelLogSinkRegistryRejectsEmptyOrNilFactory(t *testing.T) {
	cleanup := RegisterRPCMuxOTelLogSink(" ", func(string) RPCMuxOTelLogExporter {
		t.Fatal("empty sink factory should not be called")
		return nil
	})
	cleanup()
	cleanup = RegisterRPCMuxOTelLogSink("nil-factory", nil)
	cleanup()

	if RPCMuxOTelLogSinkRegistered("nil-factory") {
		t.Fatal("nil factory sink should not be registered")
	}
	if exporter := NewRPCMuxOTelLogSinkExporter("nil-factory", "profile"); exporter != nil {
		t.Fatalf("nil factory sink exporter = %#v, want nil", exporter)
	}
	if exporter := NewRPCMuxOTelLogSinkExporter("", "default-profile"); exporter == nil {
		t.Fatal("empty mux otel sink should resolve to built-in slog exporter")
	}

	var nilProvider *testRPCMuxOTelLogSinkProvider
	cleanup = RegisterRPCMuxOTelLogSinkProvider("nil-provider", nilProvider)
	cleanup()
	if RPCMuxOTelLogSinkRegistered("nil-provider") {
		t.Fatal("typed nil provider sink should not be registered")
	}
}

func TestRPCMuxOTelLogSinkProfileAndSchemaFailureContracts(t *testing.T) {
	type profile struct {
		BatchSize int `json:"batchSize"`
	}
	var decoded profile
	for _, test := range []struct {
		name    string
		profile string
		target  any
		want    string
	}{
		{name: "empty profile", profile: " ", target: &decoded, want: "profile is required"},
		{name: "oversized profile", profile: strings.Repeat("x", maxRPCMuxOTelLogProfileBytes+1), target: &decoded, want: "profile exceeds"},
		{name: "nil target", profile: `{}`, target: nil, want: "profile target is required"},
		{name: "malformed trailing value", profile: `{"batchSize":1} {`, target: &decoded, want: "decode profile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := DecodeRPCMuxOTelLogProfile(test.profile, test.target); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeRPCMuxOTelLogProfile error = %v, want containing %q", err, test.want)
			}
		})
	}

	for _, test := range []struct {
		name     string
		provider testRPCMuxOTelLogSinkProvider
	}{
		{name: "invalid schema", provider: testRPCMuxOTelLogSinkProvider{schema: json.RawMessage(`{`)}},
		{name: "schema panic", provider: testRPCMuxOTelLogSinkProvider{schemaPanic: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			sinkName := "schema-" + strings.ReplaceAll(test.name, " ", "-")
			cleanup := RegisterRPCMuxOTelLogSinkProvider(sinkName, test.provider)
			defer cleanup()
			snapshot := RPCMuxOTelLogSinkRegistry()
			for _, sink := range snapshot.Sinks {
				if sink.Name == sinkName {
					if sink.ProfileSchema != nil {
						t.Fatalf("sink schema = %s, want omitted invalid schema", sink.ProfileSchema)
					}
					return
				}
			}
			t.Fatalf("sink %q missing from registry snapshot", sinkName)
		})
	}
}

func TestRPCMuxDiagnosisEventsDeriveNegotiationFailures(t *testing.T) {
	tests := []struct {
		name  string
		phase string
		want  string
	}{
		{name: "tls", phase: experimentalMuxCandidateFailureTLS, want: "tls_failure"},
		{name: "alpn", phase: experimentalMuxCandidateFailureALPN, want: "alpn_mismatch"},
		{name: "preface", phase: experimentalMuxCandidateFailurePreface, want: "preface_mismatch"},
		{name: "protocol", phase: experimentalMuxCandidateFailureProtocol, want: "protocol_mismatch"},
		{name: "frame policy", phase: experimentalMuxCandidateFailureFramePolicy, want: "frame_policy_mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diagnosis := RPCMuxTransportDiagnosis{Candidate: ExperimentalMuxCandidateSnapshot{
				Enabled:              true,
				NegotiationFailures:  2,
				LastNegotiationPhase: tt.phase,
				LastNegotiationError: "negotiation failed",
				PeerProtocol:         "gofly-mux/peer",
			}}
			diagnosis = withRPCMuxNegotiationDiagnosis(diagnosis)

			events := RPCMuxDiagnosisEvents(diagnosis)
			if len(events) != 1 {
				t.Fatalf("events = %+v, want one negotiation event", events)
			}
			event := events[0]
			if event.Family != "negotiation" ||
				event.Event != tt.want ||
				event.Count != 2 ||
				event.PeerProtocol != "gofly-mux/peer" ||
				event.Reason != "negotiation failed" {
				t.Fatalf("negotiation event = %+v, want %s taxonomy with peer protocol and reason", event, tt.want)
			}
			if diagnosis.Negotiation.Failures != 2 ||
				diagnosis.Negotiation.LastEvent != tt.want ||
				diagnosis.Negotiation.LastError != "negotiation failed" ||
				diagnosis.Negotiation.PeerProtocol != "gofly-mux/peer" {
				t.Fatalf("negotiation summary = %+v, want %s count and last failure details", diagnosis.Negotiation, tt.want)
			}

			attrs := muxAttributeMap(MuxTraceAttributes(RPCDiagnosisProbe{
				Diagnosis: RPCDiagnosisSnapshot{Mux: RPCMuxTransportDiagnosis{Events: events}},
			}))
			if got := attrs["rpc.mux.event.negotiation.count"].AsInt64(); got != 2 {
				t.Fatalf("negotiation trace event count = %d, want 2 (attrs=%v)", got, attrs)
			}
		})
	}
}

func TestRPCMuxDiagnosisEventsDeriveAccumulatedNegotiationFailures(t *testing.T) {
	diagnosis := RPCMuxTransportDiagnosis{Candidate: ExperimentalMuxCandidateSnapshot{
		Enabled:                  true,
		NegotiationFailures:      3,
		NegotiationFailureEvents: map[string]int64{"preface_mismatch": 1, "frame_policy_mismatch": 2},
		LastNegotiationPhase:     experimentalMuxCandidateFailureFramePolicy,
		LastNegotiationError:     "peer frame codec mismatch",
		PeerProtocol:             "gofly-mux/peer",
	}}
	diagnosis = withRPCMuxNegotiationDiagnosis(diagnosis)
	events := RPCMuxDiagnosisEvents(diagnosis)

	if len(events) != 2 {
		t.Fatalf("events = %+v, want two accumulated negotiation events", events)
	}
	if diagnosis.Negotiation.Failures != 3 ||
		diagnosis.Negotiation.PrefaceMismatch != 1 ||
		diagnosis.Negotiation.FramePolicyMismatch != 2 ||
		diagnosis.Negotiation.LastEvent != "frame_policy_mismatch" ||
		diagnosis.Negotiation.LastError != "peer frame codec mismatch" ||
		diagnosis.Negotiation.PeerProtocol != "gofly-mux/peer" {
		t.Fatalf("negotiation summary = %+v, want accumulated phase counters", diagnosis.Negotiation)
	}
	byEvent := make(map[string]RPCMuxDiagnosisEvent, len(events))
	for _, event := range events {
		byEvent[event.Event] = event
	}
	if event := byEvent["preface_mismatch"]; event.Family != "negotiation" || event.Count != 1 || event.Reason != "" {
		t.Fatalf("preface event = %+v, want historical count without last reason", event)
	}
	if event := byEvent["frame_policy_mismatch"]; event.Count != 2 ||
		event.PeerProtocol != "gofly-mux/peer" ||
		event.Reason != "peer frame codec mismatch" {
		t.Fatalf("frame policy event = %+v, want last failure details", event)
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
				CreditWaitTimeouts:   1,
				FragmentBackpressure: 2,
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
	if event := byKey["flow_control/fragment_backpressure/"]; event.Count != 2 || event.ConnectionID != "muxconn-2" || event.PoolSlot != 3 {
		t.Fatalf("manager fragment flow-control event = %+v, want endpoint connection context", event)
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

func TestRPCMuxFlowControlFilterWrappers(t *testing.T) {
	diagnosis := RPCMuxTransportDiagnosis{
		FlowControl: RPCMuxFlowControlDiagnosis{
			WriteTimeouts:             2,
			CreditWaitTimeouts:        3,
			ConnectionWindowExhausted: 4,
		},
		Manager: RPCMuxConnectionManagerDiagnosis{
			FlowControl: RPCMuxFlowControlDiagnosis{
				WriteTimeouts:      5,
				CreditWaitTimeouts: 6,
			},
		},
	}
	filtered := FilterRPCMuxDiagnosisByFlowControlEvent(diagnosis, "write-timeout")
	if filtered.FlowControl.WriteTimeouts != 2 || filtered.FlowControl.CreditWaitTimeouts != 0 ||
		len(filtered.FlowControl.Events) != 1 || filtered.FlowControl.Events[0].Event != "write_timeout" {
		t.Fatalf("filtered diagnosis = %+v", filtered.FlowControl)
	}
	manager := withRPCMuxManagerFlowControlEvents(diagnosis.Manager, "credit_wait_timeout")
	if len(manager.FlowControl.Events) != 1 || manager.FlowControl.Events[0].Event != "credit_wait_timeout" ||
		manager.FlowControl.Events[0].Count != 6 {
		t.Fatalf("manager flow-control events = %+v", manager.FlowControl.Events)
	}
}

func TestRPCMuxDiagnosisEventsExposeFlowControlByPoolSlot(t *testing.T) {
	endpoint := "tcp://127.0.0.1:9002"
	diagnosis := RPCMuxTransportDiagnosis{
		Manager: RPCMuxConnectionManagerDiagnosis{
			Enabled: true,
			FlowControl: RPCMuxFlowControlDiagnosis{
				FragmentWindowRefills: 5,
			},
			Endpoints: []ExperimentalMuxEndpointSnapshot{
				{
					Endpoint:     endpoint,
					ConnectionID: "muxconn-1",
					PoolSlot:     1,
					Adapter: ExperimentalMuxAdapterSnapshot{Transport: ExperimentalMuxTransportSnapshot{
						FragmentWindowRefills: 2,
					}},
				},
				{
					Endpoint:     endpoint,
					ConnectionID: "muxconn-2",
					PoolSlot:     2,
					Adapter: ExperimentalMuxAdapterSnapshot{Transport: ExperimentalMuxTransportSnapshot{
						FragmentWindowRefills: 3,
					}},
				},
			},
		},
	}

	events := rpcMuxDiagnosisEventView(diagnosis, RPCMuxDiagnosisFilter{
		FlowControlEvent: "fragment-window-refill",
		EventFamily:      "flow-control",
		Event:            "fragment-window-refill",
	})
	byConnection := make(map[string]RPCMuxDiagnosisEvent, len(events))
	for _, event := range events {
		if event.ConnectionID != "" {
			byConnection[event.ConnectionID] = event
		}
	}
	if len(byConnection) != 2 ||
		byConnection["muxconn-1"].Endpoint != endpoint ||
		byConnection["muxconn-1"].PoolSlot != 1 ||
		byConnection["muxconn-1"].Count != 2 ||
		byConnection["muxconn-2"].Endpoint != endpoint ||
		byConnection["muxconn-2"].PoolSlot != 2 ||
		byConnection["muxconn-2"].Count != 3 {
		t.Fatalf("flow-control events = %+v, want per-pool-slot refill events", events)
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
