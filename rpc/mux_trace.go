package rpc

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
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
	case "flow_control", "health", "drain", "lifecycle":
		logger.WarnContext(ctx, "rpc mux exported event", attrs...)
	default:
		logger.InfoContext(ctx, "rpc mux exported event", attrs...)
	}
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
	appendMuxTraceManagerAttributes(&attrs, probe.ConnectionID, probe.Diagnosis.Mux.Manager)
	appendMuxTraceEventAttributes(&attrs, probe.Diagnosis.Mux.Events)
	return attrs
}

func appendMuxTraceFlowControlAttributes(attrs *[]attribute.KeyValue, diagnosis RPCMuxFlowControlDiagnosis) {
	*attrs = append(*attrs,
		attribute.Int64("rpc.mux.flow_control.write_timeout.count", diagnosis.WriteTimeouts),
		attribute.Int64("rpc.mux.flow_control.credit_wait_timeout.count", diagnosis.CreditWaitTimeouts),
		attribute.Int64("rpc.mux.flow_control.connection_window_exhausted.count", diagnosis.ConnectionWindowExhausted),
	)
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
	for _, family := range []string{"retry", "health", "flow_control", "drain", "lifecycle"} {
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
	exporter := c.opts.muxEventExporter
	if exporter == nil {
		return
	}
	filter := c.opts.muxEventFilter
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
		case "flow_control", "health", "drain", "lifecycle":
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
