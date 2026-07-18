package rpc

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const muxTraceConnectionIDKey = "rpc.mux.connection_id"

// RPCMuxDiagnosisEventRecord is the structured export envelope for one mux
// diagnosis event observed on a concrete runtime operation.
type RPCMuxDiagnosisEventRecord struct {
	Target       string               `json:"target,omitempty"`
	Method       string               `json:"method,omitempty"`
	Endpoint     string               `json:"endpoint,omitempty"`
	ConnectionID string               `json:"connectionId,omitempty"`
	PoolSlot     int                  `json:"poolSlot,omitempty"`
	Event        RPCMuxDiagnosisEvent `json:"event"`
	Probe        RPCDiagnosisProbe    `json:"probe,omitempty"`
	ExportedAt   time.Time            `json:"exportedAt"`
}

// RPCMuxDiagnosisEventExporter receives filtered mux diagnosis events. It is
// deliberately transport-neutral so callers can bridge to slog, OTel logs, or
// another structured sink without adding labels to metrics.
type RPCMuxDiagnosisEventExporter interface {
	ExportRPCMuxDiagnosisEvent(context.Context, RPCMuxDiagnosisEventRecord)
}

// RPCMuxDiagnosisEventExporterFunc adapts a function to RPCMuxDiagnosisEventExporter.
type RPCMuxDiagnosisEventExporterFunc func(context.Context, RPCMuxDiagnosisEventRecord)

func (f RPCMuxDiagnosisEventExporterFunc) ExportRPCMuxDiagnosisEvent(ctx context.Context, record RPCMuxDiagnosisEventRecord) {
	if f != nil {
		f(ctx, record)
	}
}

// RPCMuxDiagnosisEventOTelLogRecord is a small OTel-log-compatible envelope for
// mux diagnosis events. It uses OTel attributes without depending on the OTel
// logs SDK so applications can bridge it to their preferred log pipeline while
// keeping high-cardinality fields out of metrics.
type RPCMuxDiagnosisEventOTelLogRecord struct {
	Name       string               `json:"name"`
	Severity   string               `json:"severity"`
	Body       string               `json:"body"`
	Attributes []attribute.KeyValue `json:"attributes,omitempty"`
	Timestamp  time.Time            `json:"timestamp"`
	Event      RPCMuxDiagnosisEvent `json:"event"`
}

// RPCMuxOTelLogExporter receives OTel-log-compatible mux diagnosis records.
type RPCMuxOTelLogExporter interface {
	ExportRPCMuxOTelLog(context.Context, RPCMuxDiagnosisEventOTelLogRecord)
}

// RPCMuxOTelLogExporterFunc adapts a function to RPCMuxOTelLogExporter.
type RPCMuxOTelLogExporterFunc func(context.Context, RPCMuxDiagnosisEventOTelLogRecord)

func (f RPCMuxOTelLogExporterFunc) ExportRPCMuxOTelLog(ctx context.Context, record RPCMuxDiagnosisEventOTelLogRecord) {
	if f != nil {
		f(ctx, record)
	}
}

// RPCMuxOTelLogSinkFactory creates an OTel-log-compatible event sink for a
// configured profile. Applications can register custom sinks that forward to a
// concrete OTel log exporter without coupling gofly to a specific logs SDK.
type RPCMuxOTelLogSinkFactory func(profile string) RPCMuxOTelLogExporter

// RPCMuxOTelLogSinkProvider creates and validates one named OTel-compatible mux
// event sink. Use RegisterRPCMuxOTelLogSinkProvider when a sink owns
// profile-specific configuration such as endpoints or batch limits.
type RPCMuxOTelLogSinkProvider interface {
	NewRPCMuxOTelLogExporter(profile string) RPCMuxOTelLogExporter
	ValidateRPCMuxOTelLogProfile(profile string) error
}

type rpcMuxOTelLogSinkProvider struct {
	factory   RPCMuxOTelLogSinkFactory
	validator func(string) error
}

func (p rpcMuxOTelLogSinkProvider) NewRPCMuxOTelLogExporter(profile string) RPCMuxOTelLogExporter {
	if p.factory == nil {
		return nil
	}
	return p.factory(profile)
}

func (p rpcMuxOTelLogSinkProvider) ValidateRPCMuxOTelLogProfile(profile string) error {
	if p.validator == nil {
		return nil
	}
	return p.validator(profile)
}

var rpcMuxOTelLogSinks = struct {
	sync.RWMutex
	items map[string]RPCMuxOTelLogSinkProvider
}{
	items: map[string]RPCMuxOTelLogSinkProvider{
		"slog": rpcMuxOTelLogSinkProvider{
			factory: func(profile string) RPCMuxOTelLogExporter {
				return NewSlogRPCMuxOTelLogExporterWithProfile(nil, profile)
			},
		},
	},
}

// RegisterRPCMuxOTelLogSink registers or replaces an OTel-compatible mux event
// sink factory. The returned cleanup function restores the previous binding,
// which keeps tests and embedders isolated.
func RegisterRPCMuxOTelLogSink(name string, factory RPCMuxOTelLogSinkFactory) func() {
	if factory == nil {
		return func() {}
	}
	return RegisterRPCMuxOTelLogSinkProvider(name, rpcMuxOTelLogSinkProvider{factory: factory})
}

// RegisterRPCMuxOTelLogSinkProvider registers or replaces a provider that owns
// both exporter construction and profile validation. The returned cleanup
// function restores the previous binding.
func RegisterRPCMuxOTelLogSinkProvider(name string, provider RPCMuxOTelLogSinkProvider) func() {
	name = normalizeRPCMuxOTelLogSinkName(name)
	if name == "" || isNilRPCMuxOTelLogSinkProvider(provider) {
		return func() {}
	}
	rpcMuxOTelLogSinks.Lock()
	previous, hadPrevious := rpcMuxOTelLogSinks.items[name]
	rpcMuxOTelLogSinks.items[name] = provider
	rpcMuxOTelLogSinks.Unlock()
	return func() {
		rpcMuxOTelLogSinks.Lock()
		defer rpcMuxOTelLogSinks.Unlock()
		if hadPrevious {
			rpcMuxOTelLogSinks.items[name] = previous
			return
		}
		delete(rpcMuxOTelLogSinks.items, name)
	}
}

func isNilRPCMuxOTelLogSinkProvider(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// RPCMuxOTelLogSinkRegistered reports whether a configured sink is available.
// Empty names resolve to the built-in slog sink.
func RPCMuxOTelLogSinkRegistered(name string) bool {
	_, ok := lookupRPCMuxOTelLogSink(name)
	return ok
}

// ValidateRPCMuxOTelLogSinkProfile validates a configured sink and its
// sink-specific profile before runtime construction.
func ValidateRPCMuxOTelLogSinkProfile(name string, profile string) error {
	provider, ok := lookupRPCMuxOTelLogSink(name)
	if !ok {
		return fmt.Errorf("rpc mux otel log sink %q is not registered", strings.TrimSpace(name))
	}
	if err := provider.ValidateRPCMuxOTelLogProfile(strings.TrimSpace(profile)); err != nil {
		return fmt.Errorf("rpc mux otel log sink %q profile: %w", normalizeRPCMuxOTelLogSinkName(name), err)
	}
	return nil
}

// NewRPCMuxOTelLogSinkExporter returns an OTel-compatible event exporter for a
// registered sink. Unknown sinks return nil so callers can fail fast during
// configuration validation.
func NewRPCMuxOTelLogSinkExporter(name string, profile string) RPCMuxDiagnosisEventExporter {
	provider, ok := lookupRPCMuxOTelLogSink(name)
	if !ok {
		return nil
	}
	profile = strings.TrimSpace(profile)
	if err := provider.ValidateRPCMuxOTelLogProfile(profile); err != nil {
		return nil
	}
	exporter := provider.NewRPCMuxOTelLogExporter(profile)
	if exporter == nil {
		return nil
	}
	return NewRPCMuxOTelLogDiagnosisEventExporter(exporter)
}

func lookupRPCMuxOTelLogSink(name string) (RPCMuxOTelLogSinkProvider, bool) {
	name = normalizeRPCMuxOTelLogSinkName(name)
	if name == "" {
		name = "slog"
	}
	rpcMuxOTelLogSinks.RLock()
	factory, ok := rpcMuxOTelLogSinks.items[name]
	rpcMuxOTelLogSinks.RUnlock()
	return factory, ok
}

func normalizeRPCMuxOTelLogSinkName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

type rpcMuxOTelLogEventExporter struct {
	exporter RPCMuxOTelLogExporter
}

// NewRPCMuxOTelLogDiagnosisEventExporter bridges mux diagnosis events into an
// OTel-log-compatible record contract. connection_id and pool_slot are kept as
// log attributes only; they must not be mirrored into metric labels.
func NewRPCMuxOTelLogDiagnosisEventExporter(exporter RPCMuxOTelLogExporter) RPCMuxDiagnosisEventExporter {
	return rpcMuxOTelLogEventExporter{exporter: exporter}
}

func (e rpcMuxOTelLogEventExporter) ExportRPCMuxDiagnosisEvent(ctx context.Context, record RPCMuxDiagnosisEventRecord) {
	if e.exporter == nil {
		return
	}
	e.exporter.ExportRPCMuxOTelLog(ctx, MuxDiagnosisEventOTelLogRecord(record))
}

type slogRPCMuxOTelLogExporter struct {
	logger  *slog.Logger
	profile string
}

// NewSlogRPCMuxOTelLogExporter emits OTel-log-compatible mux diagnosis records
// through slog. Use it as a dependency-free adapter when an application routes
// slog to an OTel log backend.
func NewSlogRPCMuxOTelLogExporter(logger *slog.Logger) RPCMuxOTelLogExporter {
	return slogRPCMuxOTelLogExporter{logger: logger}
}

// NewSlogRPCMuxOTelLogExporterWithProfile emits OTel-log-compatible mux
// diagnosis records through slog and tags each record with a configured sink
// profile name.
func NewSlogRPCMuxOTelLogExporterWithProfile(logger *slog.Logger, profile string) RPCMuxOTelLogExporter {
	return slogRPCMuxOTelLogExporter{logger: logger, profile: strings.TrimSpace(profile)}
}

func (e slogRPCMuxOTelLogExporter) ExportRPCMuxOTelLog(ctx context.Context, record RPCMuxDiagnosisEventOTelLogRecord) {
	logger := e.logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := muxOTelLogSlogAttrs(record)
	if e.profile != "" {
		attrs = append(attrs, slog.String("otel_log_profile", e.profile))
	}
	switch record.Severity {
	case "WARN":
		logger.WarnContext(ctx, "rpc mux otel log event", attrs...)
	default:
		logger.InfoContext(ctx, "rpc mux otel log event", attrs...)
	}
}

type slogRPCMuxDiagnosisEventExporter struct {
	logger *slog.Logger
}

// NewSlogRPCMuxDiagnosisEventExporter exports mux runtime events as structured
// slog records. Use it with an OTel slog handler to forward the same contract to
// an OTel log exporter.
func NewSlogRPCMuxDiagnosisEventExporter(logger *slog.Logger) RPCMuxDiagnosisEventExporter {
	return slogRPCMuxDiagnosisEventExporter{logger: logger}
}

func (e slogRPCMuxDiagnosisEventExporter) ExportRPCMuxDiagnosisEvent(ctx context.Context, record RPCMuxDiagnosisEventRecord) {
	logger := e.logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := muxDiagnosisEventLogAttrs(record.Probe, record.Event)
	if record.Endpoint != "" && !slogAttrsContain(attrs, "endpoint") {
		attrs = append(attrs, slog.String("endpoint", record.Endpoint))
	}
	if record.ConnectionID != "" && !slogAttrsContain(attrs, "connection_id") {
		attrs = append(attrs, slog.String("connection_id", record.ConnectionID))
	}
	if record.PoolSlot > 0 && !slogAttrsContain(attrs, "pool_slot") {
		attrs = append(attrs, slog.Int("pool_slot", record.PoolSlot))
	}
	attrs = append(attrs, slog.Time("exported_at", record.ExportedAt))
	switch record.Event.Family {
	case "flow_control", "health", "drain", "lifecycle", "negotiation":
		logger.WarnContext(ctx, "rpc mux exported event", attrs...)
	default:
		logger.InfoContext(ctx, "rpc mux exported event", attrs...)
	}
}

// MuxDiagnosisEventOTelLogRecord converts a mux diagnosis event into a stable
// OTel-log-compatible record. The attribute keys intentionally use the rpc.mux
// namespace shared by trace attributes while preserving high-cardinality values
// only in log records.
func MuxDiagnosisEventOTelLogRecord(record RPCMuxDiagnosisEventRecord) RPCMuxDiagnosisEventOTelLogRecord {
	return RPCMuxDiagnosisEventOTelLogRecord{
		Name:       "rpc.mux.diagnosis_event",
		Severity:   muxDiagnosisEventOTelSeverity(record.Event),
		Body:       "rpc mux diagnosis event",
		Attributes: muxDiagnosisEventOTelAttributes(record),
		Timestamp:  record.ExportedAt,
		Event:      record.Event,
	}
}

func muxDiagnosisEventOTelSeverity(event RPCMuxDiagnosisEvent) string {
	switch event.Family {
	case "flow_control", "health", "drain", "lifecycle", "negotiation":
		return "WARN"
	default:
		return "INFO"
	}
}

func muxDiagnosisEventOTelAttributes(record RPCMuxDiagnosisEventRecord) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("rpc.mux.event.family", record.Event.Family),
		attribute.String("rpc.mux.event.name", record.Event.Event),
		attribute.Int64("rpc.mux.event.count", record.Event.Count),
	}
	if record.Target != "" {
		attrs = append(attrs, attribute.String("rpc.mux.target", record.Target))
	}
	if record.Method != "" {
		attrs = append(attrs, attribute.String("rpc.mux.method", record.Method))
	}
	if endpoint := muxDiagnosisRecordEndpoint(record.Probe, record.Event); endpoint != "" {
		attrs = append(attrs, attribute.String("rpc.mux.endpoint", endpoint))
	} else if record.Endpoint != "" {
		attrs = append(attrs, attribute.String("rpc.mux.endpoint", record.Endpoint))
	}
	if connectionID := muxDiagnosisRecordConnectionID(record.Probe, record.Event); connectionID != "" {
		attrs = append(attrs, attribute.String(muxTraceConnectionIDKey, connectionID))
	} else if record.ConnectionID != "" {
		attrs = append(attrs, attribute.String(muxTraceConnectionIDKey, record.ConnectionID))
	}
	if poolSlot := muxDiagnosisRecordPoolSlot(record.Probe, record.Event); poolSlot > 0 {
		attrs = append(attrs, attribute.Int("rpc.mux.pool_slot", poolSlot))
	} else if record.PoolSlot > 0 {
		attrs = append(attrs, attribute.Int("rpc.mux.pool_slot", record.PoolSlot))
	}
	if record.Event.Reason != "" {
		attrs = append(attrs, attribute.String("rpc.mux.event.reason", record.Event.Reason))
	}
	if record.Event.From != "" {
		attrs = append(attrs, attribute.String("rpc.mux.event.from", record.Event.From))
	}
	if record.Event.To != "" {
		attrs = append(attrs, attribute.String("rpc.mux.event.to", record.Event.To))
	}
	if record.Event.PeerProtocol != "" {
		attrs = append(attrs, attribute.String("rpc.mux.event.peer_protocol", record.Event.PeerProtocol))
	}
	if record.Event.Direction != "" {
		attrs = append(attrs, attribute.String("rpc.mux.event.direction", record.Event.Direction))
	}
	if record.Event.Cooldown > 0 {
		attrs = append(attrs, attribute.Int64("rpc.mux.event.cooldown_ms", record.Event.Cooldown.Milliseconds()))
	}
	return attrs
}

func muxOTelLogSlogAttrs(record RPCMuxDiagnosisEventOTelLogRecord) []any {
	attrs := []any{
		slog.String("otel_log_name", record.Name),
		slog.String("otel_log_severity", record.Severity),
		slog.String("otel_log_body", record.Body),
		slog.Time("timestamp", record.Timestamp),
	}
	for _, attr := range record.Attributes {
		key := strings.ReplaceAll(string(attr.Key), ".", "_")
		attrs = append(attrs, slog.Any(key, attr.Value.AsInterface()))
	}
	return attrs
}

func slogAttrsContain(attrs []any, key string) bool {
	for i := 0; i < len(attrs); i++ {
		attr := attrs[i]
		if existing, ok := attr.(slog.Attr); ok && existing.Key == key {
			return true
		}
		if existing, ok := attr.(string); ok && existing == key {
			return true
		}
		if _, ok := attr.(string); ok {
			i++
		}
	}
	return false
}

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
	appendMuxTraceFlowControlAttributes(&attrs, probe.Diagnosis.Mux.FlowControl)
	candidate, negotiated := muxEffectiveCandidateSnapshot(probe.Diagnosis.Mux)
	appendMuxTraceCandidateAttributes(&attrs, candidate, negotiated)
	appendMuxTraceManagerAttributes(&attrs, probe.ConnectionID, probe.Diagnosis.Mux.Manager)
	appendMuxTraceEventAttributes(&attrs, probe.Diagnosis.Mux.Events)
	return attrs
}

func appendMuxTraceFlowControlAttributes(attrs *[]attribute.KeyValue, diagnosis RPCMuxFlowControlDiagnosis) {
	*attrs = append(*attrs,
		attribute.Int64("rpc.mux.flow_control.write_timeout.count", diagnosis.WriteTimeouts),
		attribute.Int64("rpc.mux.flow_control.credit_wait_timeout.count", diagnosis.CreditWaitTimeouts),
		attribute.Int64("rpc.mux.flow_control.connection_window_exhausted.count", diagnosis.ConnectionWindowExhausted),
		attribute.Int64("rpc.mux.flow_control.fragment_backpressure.count", diagnosis.FragmentBackpressure),
		attribute.Int64("rpc.mux.flow_control.fragment_frames_in.count", diagnosis.FragmentFramesIn),
		attribute.Int64("rpc.mux.flow_control.fragment_frames_out.count", diagnosis.FragmentFramesOut),
		attribute.String("rpc.mux.flow_control.fragment_stream_window_update_policy", diagnosis.FragmentStreamWindowUpdatePolicy),
		attribute.String("rpc.mux.flow_control.fragment_connection_window_update_policy", diagnosis.FragmentConnectionWindowUpdatePolicy),
		attribute.Float64("rpc.mux.flow_control.fragment_stream_window_refill_ratio", diagnosis.FragmentStreamWindowRefillRatio),
		attribute.Float64("rpc.mux.flow_control.fragment_connection_window_refill_ratio", diagnosis.FragmentConnectionWindowRefillRatio),
		attribute.Int("rpc.mux.flow_control.fragment_max_deferred_fragments", diagnosis.FragmentMaxDeferredFragments),
		attribute.Int64("rpc.mux.flow_control.fragment_window_refill.count", diagnosis.FragmentWindowRefills),
		attribute.Int64("rpc.mux.flow_control.fragment_window_refill_latency_total.ns", diagnosis.FragmentWindowRefillLatencyTotal.Nanoseconds()),
		attribute.Int64("rpc.mux.flow_control.fragment_window_refill_latency_max.ns", diagnosis.FragmentWindowRefillLatencyMax.Nanoseconds()),
		attribute.Int64("rpc.mux.flow_control.fragment_window_refill_latency_avg.ns", diagnosis.FragmentWindowRefillLatencyAvg.Nanoseconds()),
		attribute.Int64("rpc.mux.flow_control.fragment_deferred_stream_window_updates.count", diagnosis.FragmentDeferredStreamWindowUpdates),
		attribute.Int64("rpc.mux.flow_control.fragment_deferred_connection_window_updates.count", diagnosis.FragmentDeferredConnectionWindowUpdates),
		attribute.Bool("rpc.mux.flow_control.fragment_window_policy_risk", diagnosis.FragmentWindowPolicyRisk),
		attribute.String("rpc.mux.flow_control.fragment_window_policy_risk_reason", diagnosis.FragmentWindowPolicyRiskReason),
		attribute.String("rpc.mux.flow_control.fragment_window_policy_risk_mode", diagnosis.FragmentWindowPolicyRiskMode),
		attribute.Int("rpc.mux.flow_control.fragment_estimated_max_fragments", diagnosis.FragmentEstimatedMaxFragments),
	)
}

func appendMuxTraceCandidateAttributes(attrs *[]attribute.KeyValue, candidate ExperimentalMuxCandidateSnapshot, negotiated bool) {
	if !candidate.Enabled {
		return
	}
	*attrs = append(*attrs,
		attribute.Bool("rpc.mux.candidate.tls", candidate.TLS),
		attribute.Bool("rpc.mux.candidate.mutual_tls", candidate.MutualTLS),
	)
	if negotiated && candidate.NegotiatedProtocol != "" {
		*attrs = append(*attrs, attribute.String("rpc.mux.candidate.negotiated_protocol", candidate.NegotiatedProtocol))
	}
	if candidate.Protocol != "" {
		*attrs = append(*attrs, attribute.String("rpc.mux.candidate.protocol", candidate.Protocol))
	}
}

func appendMuxTraceManagerAttributes(attrs *[]attribute.KeyValue, probeConnectionID string, diagnosis RPCMuxConnectionManagerDiagnosis) {
	if !diagnosis.Enabled {
		return
	}
	*attrs = append(*attrs,
		attribute.String("rpc.mux.mode", diagnosis.Mode),
		attribute.Int("rpc.mux.endpoint.count", len(diagnosis.Endpoints)),
		attribute.Int64("rpc.mux.manager.open_retries.count", diagnosis.OpenRetries),
		attribute.Int64("rpc.mux.manager.pool_exhaustions.count", diagnosis.PoolExhaustions),
		attribute.Int64("rpc.mux.manager.dial_failures.count", diagnosis.DialFailures),
		attribute.Int64("rpc.mux.manager.endpoint_ejections.count", diagnosis.EndpointEjections),
		attribute.Int64("rpc.mux.manager.endpoint_recoveries.count", diagnosis.EndpointRecoveries),
		attribute.Int64("rpc.mux.manager.flow_control.write_timeout.count", diagnosis.FlowControl.WriteTimeouts),
		attribute.Int64("rpc.mux.manager.flow_control.credit_wait_timeout.count", diagnosis.FlowControl.CreditWaitTimeouts),
		attribute.Int64("rpc.mux.manager.flow_control.connection_window_exhausted.count", diagnosis.FlowControl.ConnectionWindowExhausted),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_backpressure.count", diagnosis.FlowControl.FragmentBackpressure),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_frames_in.count", diagnosis.FlowControl.FragmentFramesIn),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_frames_out.count", diagnosis.FlowControl.FragmentFramesOut),
		attribute.String("rpc.mux.manager.flow_control.fragment_stream_window_update_policy", diagnosis.FlowControl.FragmentStreamWindowUpdatePolicy),
		attribute.String("rpc.mux.manager.flow_control.fragment_connection_window_update_policy", diagnosis.FlowControl.FragmentConnectionWindowUpdatePolicy),
		attribute.Float64("rpc.mux.manager.flow_control.fragment_stream_window_refill_ratio", diagnosis.FlowControl.FragmentStreamWindowRefillRatio),
		attribute.Float64("rpc.mux.manager.flow_control.fragment_connection_window_refill_ratio", diagnosis.FlowControl.FragmentConnectionWindowRefillRatio),
		attribute.Int("rpc.mux.manager.flow_control.fragment_max_deferred_fragments", diagnosis.FlowControl.FragmentMaxDeferredFragments),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_window_refill.count", diagnosis.FlowControl.FragmentWindowRefills),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_window_refill_latency_total.ns", diagnosis.FlowControl.FragmentWindowRefillLatencyTotal.Nanoseconds()),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_window_refill_latency_max.ns", diagnosis.FlowControl.FragmentWindowRefillLatencyMax.Nanoseconds()),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_window_refill_latency_avg.ns", diagnosis.FlowControl.FragmentWindowRefillLatencyAvg.Nanoseconds()),
		attribute.String("rpc.mux.manager.refill_profile.stream_window_update_policy", diagnosis.RefillProfile.StreamWindowUpdatePolicy),
		attribute.String("rpc.mux.manager.refill_profile.connection_window_update_policy", diagnosis.RefillProfile.ConnectionWindowUpdatePolicy),
		attribute.Float64("rpc.mux.manager.refill_profile.stream_window_refill_ratio", diagnosis.RefillProfile.StreamWindowRefillRatio),
		attribute.Float64("rpc.mux.manager.refill_profile.connection_window_refill_ratio", diagnosis.RefillProfile.ConnectionWindowRefillRatio),
		attribute.Int("rpc.mux.manager.refill_profile.max_deferred_fragments", diagnosis.RefillProfile.MaxDeferredFragments),
		attribute.Int64("rpc.mux.manager.refill_profile.refills.count", diagnosis.RefillProfile.Refills),
		attribute.Int64("rpc.mux.manager.refill_profile.refill_latency_max.ns", diagnosis.RefillProfile.RefillLatencyMax.Nanoseconds()),
		attribute.Int64("rpc.mux.manager.refill_profile.refill_latency_avg.ns", diagnosis.RefillProfile.RefillLatencyAvg.Nanoseconds()),
		attribute.String("rpc.mux.manager.refill_profile.last_flow_control_event", diagnosis.RefillProfile.LastFlowControlEvent),
		attribute.String("rpc.mux.manager.refill_profile.last_backpressure_event", diagnosis.RefillProfile.LastBackpressureEvent),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_deferred_stream_window_updates.count", diagnosis.FlowControl.FragmentDeferredStreamWindowUpdates),
		attribute.Int64("rpc.mux.manager.flow_control.fragment_deferred_connection_window_updates.count", diagnosis.FlowControl.FragmentDeferredConnectionWindowUpdates),
		attribute.Bool("rpc.mux.manager.flow_control.fragment_window_policy_risk", diagnosis.FlowControl.FragmentWindowPolicyRisk),
		attribute.String("rpc.mux.manager.flow_control.fragment_window_policy_risk_reason", diagnosis.FlowControl.FragmentWindowPolicyRiskReason),
		attribute.String("rpc.mux.manager.flow_control.fragment_window_policy_risk_mode", diagnosis.FlowControl.FragmentWindowPolicyRiskMode),
		attribute.Int("rpc.mux.manager.flow_control.fragment_estimated_max_fragments", diagnosis.FlowControl.FragmentEstimatedMaxFragments),
	)
	if diagnosis.LastRetriedFrom != "" {
		*attrs = append(*attrs, attribute.String("rpc.mux.manager.last_retried_from", diagnosis.LastRetriedFrom))
	}
	if diagnosis.LastRetriedTo != "" {
		*attrs = append(*attrs, attribute.String("rpc.mux.manager.last_retried_to", diagnosis.LastRetriedTo))
	}
	appendMuxTraceRetryReasonAttributes(attrs, diagnosis.RetryReasons)
	appendMuxTraceHealthAttributes(attrs, diagnosis.Health)
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

func appendMuxTraceRetryReasonAttributes(attrs *[]attribute.KeyValue, reasons map[string]int64) {
	for _, reason := range []string{"dial_failure", "pool_exhausted", "open_stream"} {
		if count := reasons[reason]; count > 0 {
			*attrs = append(*attrs, attribute.Int64("rpc.mux.manager.retry_reason."+reason+".count", count))
		}
	}
}

func appendMuxTraceHealthAttributes(attrs *[]attribute.KeyValue, health []ExperimentalMuxEndpointHealthSnapshot) {
	if len(health) == 0 {
		return
	}
	*attrs = append(*attrs, attribute.Int("rpc.mux.manager.health.count", len(health)))
	item := health[0]
	if item.Endpoint != "" {
		*attrs = append(*attrs, attribute.String("rpc.mux.manager.health.endpoint", item.Endpoint))
	}
	if item.Reason != "" {
		*attrs = append(*attrs, attribute.String("rpc.mux.manager.health.reason", item.Reason))
	}
	if item.Ejected {
		*attrs = append(*attrs, attribute.Bool("rpc.mux.manager.health.ejected", true))
	}
	if item.Cooldown > 0 {
		*attrs = append(*attrs, attribute.String("rpc.mux.manager.health.cooldown", item.Cooldown.String()))
		*attrs = append(*attrs, attribute.Int64("rpc.mux.manager.health.cooldown_ms", item.Cooldown.Milliseconds()))
	}
	if !item.CooldownUntil.IsZero() {
		*attrs = append(*attrs, attribute.String("rpc.mux.manager.health.cooldown_until_unix_nano", strconv.FormatInt(item.CooldownUntil.UnixNano(), 10)))
	}
}

func appendMuxTraceEventAttributes(attrs *[]attribute.KeyValue, events []RPCMuxDiagnosisEvent) {
	if len(events) == 0 {
		return
	}
	*attrs = append(*attrs, attribute.Int("rpc.mux.event.count", len(events)))
	counts := make(map[string]int64, 4)
	for _, event := range events {
		if event.Family == "" {
			continue
		}
		count := event.Count
		if count <= 0 {
			count = 1
		}
		counts[event.Family] += count
	}
	for _, family := range []string{"retry", "health", "flow_control", "drain", "lifecycle", "negotiation"} {
		if count := counts[family]; count > 0 {
			*attrs = append(*attrs, attribute.Int64("rpc.mux.event."+family+".count", count))
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

func (c *HTTPClient) annotateMuxStreamSpan(ctx context.Context, method string, endpoint string, flowControlEvent string, err error) {
	if c == nil || (!c.opts.muxTrace && !c.opts.muxLog && c.opts.muxEventExporter == nil) {
		return
	}
	probe := c.muxStreamDiagnosisProbe(method, endpoint, flowControlEvent, err)
	c.observeMuxDiagnosis(ctx, probe, err)
}

// ObserveMuxDiagnosis applies the client's configured mux trace, log, and
// event-export hooks to an already captured diagnosis probe. It is useful for
// admin or generated-project smoke paths that query /rpc/diagnosis after a
// stream operation and want the same opt-in observability contract as MuxStream.
func (c *HTTPClient) ObserveMuxDiagnosis(ctx context.Context, probe RPCDiagnosisProbe) {
	if c == nil || (!c.opts.muxTrace && !c.opts.muxLog && c.opts.muxEventExporter == nil) {
		return
	}
	c.observeMuxDiagnosis(ctx, probe, nil)
}

func (c *HTTPClient) observeMuxDiagnosis(ctx context.Context, probe RPCDiagnosisProbe, err error) {
	if c.opts.muxTrace {
		AnnotateMuxDiagnosisSpan(ctx, probe)
	}
	if c.opts.muxLog {
		c.logMuxStreamDiagnosis(ctx, probe, err)
	}
	if c.opts.muxEventExporter != nil {
		c.exportMuxDiagnosisEvents(ctx, probe)
	}
}

func (c *HTTPClient) muxStreamDiagnosisProbe(method string, endpoint string, flowControlEvent string, err error) RPCDiagnosisProbe {
	snapshot := c.RuntimeSnapshot()
	probe := RPCDiagnosisProbe{
		Target:      snapshot.Target,
		Method:      strings.Trim(strings.TrimSpace(method), "/"),
		Endpoint:    normalizeMuxDiagnosisEndpoint(endpoint),
		FlowControl: NormalizeRPCMuxFlowControlEvent(flowControlEvent),
		Diagnosis:   snapshot.Diagnosis,
		Discovery:   snapshot.Discovery,
		Matched:     true,
		GeneratedAt: time.Now(),
	}
	if probe.FlowControl != "" {
		probe.Diagnosis.Mux.FlowControl = filterRPCMuxFlowControlDiagnosis(probe.Diagnosis.Mux.FlowControl, probe.FlowControl)
		probe.Diagnosis.Mux.Manager = withRPCMuxManagerFlowControlEvents(probe.Diagnosis.Mux.Manager, probe.FlowControl)
	}
	if err != nil {
		probe.Diagnosis.Mux.Manager.RetryReasons = withMuxRetryReason(probe.Diagnosis.Mux.Manager.RetryReasons, muxTraceErrorReason(err))
	}
	probe.Diagnosis.Mux.Events = RPCMuxDiagnosisEvents(probe.Diagnosis.Mux)
	return probe
}

func (c *HTTPClient) exportMuxDiagnosisEvents(ctx context.Context, probe RPCDiagnosisProbe) {
	exportRPCMuxDiagnosisEvents(ctx, c.opts.muxEventExporter, c.opts.muxEventFilter, probe)
}

func exportRPCMuxDiagnosisEvents(ctx context.Context, exporter RPCMuxDiagnosisEventExporter, filter RPCMuxDiagnosisFilter, probe RPCDiagnosisProbe) {
	if exporter == nil {
		return
	}
	if filter.Endpoint == "" {
		filter.Endpoint = probe.Endpoint
	}
	if filter.ConnectionID == "" {
		filter.ConnectionID = probe.ConnectionID
	}
	if filter.PoolSlot <= 0 {
		filter.PoolSlot = probe.PoolSlot
	}
	events := rpcMuxDiagnosisEventView(probe.Diagnosis.Mux, filter)
	for _, event := range events {
		exporter.ExportRPCMuxDiagnosisEvent(ctx, RPCMuxDiagnosisEventRecord{
			Target:       probe.Target,
			Method:       probe.Method,
			Endpoint:     muxDiagnosisRecordEndpoint(probe, event),
			ConnectionID: muxDiagnosisRecordConnectionID(probe, event),
			PoolSlot:     muxDiagnosisRecordPoolSlot(probe, event),
			Event:        event,
			Probe:        probe,
			ExportedAt:   time.Now(),
		})
	}
}

func muxDiagnosisRecordEndpoint(probe RPCDiagnosisProbe, event RPCMuxDiagnosisEvent) string {
	if event.Endpoint != "" {
		return event.Endpoint
	}
	if probe.Endpoint != "" {
		return probe.Endpoint
	}
	return event.To
}

func muxDiagnosisRecordConnectionID(probe RPCDiagnosisProbe, event RPCMuxDiagnosisEvent) string {
	if event.ConnectionID != "" {
		return event.ConnectionID
	}
	return probe.ConnectionID
}

func muxDiagnosisRecordPoolSlot(probe RPCDiagnosisProbe, event RPCMuxDiagnosisEvent) int {
	if event.PoolSlot > 0 {
		return event.PoolSlot
	}
	return probe.PoolSlot
}

func (c *HTTPClient) logMuxStreamDiagnosis(ctx context.Context, probe RPCDiagnosisProbe, err error) {
	logger := c.opts.muxLogger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := muxDiagnosisLogAttrs(probe)
	if err != nil {
		attrs = append(attrs, slog.Any("error", err), slog.String("rpc_error_code", string(CodeOf(err))))
		logger.WarnContext(ctx, "rpc mux stream diagnosis", attrs...)
		logMuxDiagnosisEvents(ctx, logger, probe)
		return
	}
	logger.InfoContext(ctx, "rpc mux stream diagnosis", attrs...)
	logMuxDiagnosisEvents(ctx, logger, probe)
}

func logMuxDiagnosisEvents(ctx context.Context, logger *slog.Logger, probe RPCDiagnosisProbe) {
	for _, event := range probe.Diagnosis.Mux.Events {
		attrs := muxDiagnosisEventLogAttrs(probe, event)
		switch event.Family {
		case "flow_control", "health", "drain", "lifecycle", "negotiation":
			logger.WarnContext(ctx, "rpc mux runtime event", attrs...)
		default:
			logger.InfoContext(ctx, "rpc mux runtime event", attrs...)
		}
	}
}

func muxDiagnosisEventLogAttrs(probe RPCDiagnosisProbe, event RPCMuxDiagnosisEvent) []any {
	attrs := []any{
		slog.String("target", probe.Target),
		slog.String("method", probe.Method),
		slog.String("event_family", event.Family),
		slog.String("event", event.Event),
		slog.Int64("count", event.Count),
	}
	if event.Endpoint != "" {
		attrs = append(attrs, slog.String("endpoint", event.Endpoint))
	} else if probe.Endpoint != "" {
		attrs = append(attrs, slog.String("endpoint", probe.Endpoint))
	}
	if event.ConnectionID != "" {
		attrs = append(attrs, slog.String("connection_id", event.ConnectionID))
	} else if probe.ConnectionID != "" {
		attrs = append(attrs, slog.String("connection_id", probe.ConnectionID))
	}
	if event.PoolSlot > 0 {
		attrs = append(attrs, slog.Int("pool_slot", event.PoolSlot))
	} else if probe.PoolSlot > 0 {
		attrs = append(attrs, slog.Int("pool_slot", probe.PoolSlot))
	}
	if event.PeerProtocol != "" {
		attrs = append(attrs, slog.String("peer_protocol", event.PeerProtocol))
	}
	if event.Reason != "" {
		attrs = append(attrs, slog.String("reason", event.Reason))
	}
	if event.From != "" {
		attrs = append(attrs, slog.String("last_retried_from", event.From))
	}
	if event.To != "" {
		attrs = append(attrs, slog.String("last_retried_to", event.To))
	}
	if event.Cooldown > 0 {
		attrs = append(attrs, slog.Duration("cooldown", event.Cooldown))
	}
	if event.Direction != "" {
		attrs = append(attrs, slog.String("direction", event.Direction))
	}
	return attrs
}

func muxDiagnosisLogAttrs(probe RPCDiagnosisProbe) []any {
	attrs := []any{
		slog.String("target", probe.Target),
		slog.String("method", probe.Method),
		slog.String("endpoint", probe.Endpoint),
		slog.Bool("matched", probe.Matched),
	}
	if probe.ConnectionID != "" {
		attrs = append(attrs, slog.String("connection_id", probe.ConnectionID))
	}
	if probe.PoolSlot > 0 {
		attrs = append(attrs, slog.Int("pool_slot", probe.PoolSlot))
	}
	if probe.FlowControl != "" {
		attrs = append(attrs, slog.String("flow_control_event", probe.FlowControl))
	}
	if candidate, negotiated := muxEffectiveCandidateSnapshot(probe.Diagnosis.Mux); candidate.Enabled {
		attrs = append(attrs,
			slog.Bool("tls", candidate.TLS),
			slog.Bool("mutual_tls", candidate.MutualTLS),
		)
		if negotiated && candidate.NegotiatedProtocol != "" {
			attrs = append(attrs, slog.String("negotiated_protocol", candidate.NegotiatedProtocol))
		}
		if candidate.Protocol != "" {
			attrs = append(attrs, slog.String("candidate_protocol", candidate.Protocol))
		}
	}
	manager := probe.Diagnosis.Mux.Manager
	if manager.Enabled {
		attrs = append(attrs,
			slog.String("mux_mode", manager.Mode),
			slog.Int64("open_retries", manager.OpenRetries),
			slog.Int64("pool_exhaustions", manager.PoolExhaustions),
			slog.Int64("dial_failures", manager.DialFailures),
			slog.String("last_retried_from", manager.LastRetriedFrom),
			slog.String("last_retried_to", manager.LastRetriedTo),
		)
		if profile := manager.RefillProfile; !muxRefillProfileLogEmpty(profile) {
			attrs = append(attrs,
				slog.String("refill_profile_stream_window_update_policy", profile.StreamWindowUpdatePolicy),
				slog.String("refill_profile_connection_window_update_policy", profile.ConnectionWindowUpdatePolicy),
				slog.Float64("refill_profile_stream_window_refill_ratio", profile.StreamWindowRefillRatio),
				slog.Float64("refill_profile_connection_window_refill_ratio", profile.ConnectionWindowRefillRatio),
				slog.Int("refill_profile_max_deferred_fragments", profile.MaxDeferredFragments),
				slog.Int64("refill_profile_refills", profile.Refills),
				slog.Duration("refill_profile_refill_latency_max", profile.RefillLatencyMax),
				slog.Duration("refill_profile_refill_latency_avg", profile.RefillLatencyAvg),
				slog.String("refill_profile_last_flow_control_event", profile.LastFlowControlEvent),
				slog.String("refill_profile_last_backpressure_event", profile.LastBackpressureEvent),
			)
		}
		if len(manager.Endpoints) == 1 {
			endpoint := manager.Endpoints[0]
			attrs = append(attrs, slog.String("filtered_endpoint", endpoint.Endpoint))
			if probe.ConnectionID == "" && endpoint.ConnectionID != "" {
				attrs = append(attrs, slog.String("connection_id", endpoint.ConnectionID))
			}
			if probe.PoolSlot == 0 && endpoint.PoolSlot > 0 {
				attrs = append(attrs, slog.Int("pool_slot", endpoint.PoolSlot))
			}
		}
		if len(manager.Health) > 0 {
			health := manager.Health[0]
			attrs = append(attrs,
				slog.String("health_endpoint", health.Endpoint),
				slog.String("health_reason", health.Reason),
				slog.Bool("health_ejected", health.Ejected),
				slog.Duration("health_cooldown", health.Cooldown),
			)
		}
	}
	return attrs
}

func muxRefillProfileLogEmpty(profile RPCMuxRefillProfile) bool {
	return profile.StreamWindowUpdatePolicy == "" &&
		profile.ConnectionWindowUpdatePolicy == "" &&
		profile.StreamWindowRefillRatio == 0 &&
		profile.ConnectionWindowRefillRatio == 0 &&
		profile.MaxDeferredFragments == 0 &&
		profile.Refills == 0 &&
		profile.RefillLatencyMax == 0 &&
		profile.RefillLatencyAvg == 0 &&
		profile.LastFlowControlEvent == "" &&
		profile.LastBackpressureEvent == ""
}

func muxEffectiveCandidateSnapshot(diagnosis RPCMuxTransportDiagnosis) (ExperimentalMuxCandidateSnapshot, bool) {
	if len(diagnosis.Manager.Endpoints) == 1 && diagnosis.Manager.Endpoints[0].Adapter.Candidate.Enabled {
		return diagnosis.Manager.Endpoints[0].Adapter.Candidate, true
	}
	if diagnosis.Adapter.Candidate.Enabled {
		return diagnosis.Adapter.Candidate, true
	}
	if !diagnosis.Manager.Enabled && diagnosis.Candidate.Enabled {
		return diagnosis.Candidate, true
	}
	if diagnosis.Manager.Candidate.Enabled {
		return diagnosis.Manager.Candidate, false
	}
	return ExperimentalMuxCandidateSnapshot{}, false
}

func withMuxRetryReason(reasons map[string]int64, reason string) map[string]int64 {
	if reason == "" {
		return reasons
	}
	if reasons == nil {
		reasons = make(map[string]int64, 1)
	}
	if reasons[reason] == 0 {
		reasons[reason] = 1
	}
	return reasons
}

func muxTraceErrorReason(err error) string {
	switch CodeOf(err) {
	case CodeOK:
		return ""
	case CodeResourceExhausted:
		return "pool_exhausted"
	case CodeUnavailable:
		return "dial_failure"
	default:
		return "open_stream"
	}
}
