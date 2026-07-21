package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/observability/metrics"
)

func TestGovernedRPCMuxDiagnosisEventExporterDelivery(t *testing.T) {
	oldMetrics := metrics.Default
	registry := metrics.NewRegistry()
	metrics.Default = registry
	registerRPCMuxDiagnosisExporterDeliveryMetrics(registry)
	t.Cleanup(func() {
		metrics.Default = oldMetrics
		registerRPCMuxDiagnosisExporterDeliveryMetrics(oldMetrics)
	})

	block := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	exporter := NewGovernedRPCMuxDiagnosisEventExporter(
		RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {
			startOnce.Do(func() { close(started) })
			<-block
		}),
		RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: 1, Timeout: 10 * time.Millisecond},
	)
	closer := exporter.(*governedRPCMuxDiagnosisExporter)

	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	<-started
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})

	waitForMuxExporterSnapshot(t, closer, func(snapshot RPCMuxDiagnosisExporterDeliverySnapshot) bool {
		return snapshot.TimedOut == 1 && snapshot.Dropped >= 1
	})
	close(block)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := closer.RPCMuxDiagnosisExporterDeliverySnapshot()
	if !snapshot.Closed || snapshot.Accepted != 2 || snapshot.TimedOut != 1 || snapshot.Dropped < 1 {
		t.Fatalf("delivery snapshot = %+v, want accepted, timeout, drop, and closed counters", snapshot)
	}
	customs := registry.Snapshot().Customs
	if customs["gofly_rpc_mux_diagnosis_exporter_delivery_total"].Type != metrics.MetricCounter {
		t.Fatalf("delivery metric = %+v, want counter", customs["gofly_rpc_mux_diagnosis_exporter_delivery_total"])
	}
}

func TestGovernedRPCMuxDiagnosisEventExporterIsolatesPanic(t *testing.T) {
	exporter := NewGovernedRPCMuxDiagnosisEventExporter(
		RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {
			panic("sink panic")
		}),
		RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: 1, Timeout: time.Second},
	)
	closer := exporter.(*governedRPCMuxDiagnosisExporter)
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	waitForMuxExporterSnapshot(t, closer, func(snapshot RPCMuxDiagnosisExporterDeliverySnapshot) bool {
		return snapshot.Panics == 1
	})
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := closer.RPCMuxDiagnosisExporterDeliverySnapshot()
	if snapshot.Panics != 1 || snapshot.Exported != 0 {
		t.Fatalf("panic snapshot = %+v, want one isolated panic and zero successful exports", snapshot)
	}
}

func TestGovernedRPCMuxDiagnosisEventExporterBreakerHalfOpenRecovery(t *testing.T) {
	blockFirst := make(chan struct{})
	var calls atomic.Int64
	exporter := newGovernedRPCMuxDiagnosisEventExporter(
		"half-open-recovery",
		RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {
			if calls.Add(1) == 1 {
				<-blockFirst
			}
		}),
		RPCMuxDiagnosisExporterDeliveryConfig{
			QueueSize:               2,
			Timeout:                 5 * time.Millisecond,
			MaxHungCalls:            2,
			BreakerFailureThreshold: 1,
			BreakerCooldown:         10 * time.Millisecond,
		},
	)
	governed := exporter.(*governedRPCMuxDiagnosisExporter)
	defer governed.Close()

	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	waitForMuxExporterSnapshot(t, governed, func(snapshot RPCMuxDiagnosisExporterDeliverySnapshot) bool {
		return snapshot.TimedOut == 1 && snapshot.BreakerState == "open"
	})
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	if snapshot := governed.RPCMuxDiagnosisExporterDeliverySnapshot(); snapshot.BreakerRejected != 1 {
		t.Fatalf("open breaker snapshot = %+v, want one rejection", snapshot)
	}

	close(blockFirst)
	time.Sleep(15 * time.Millisecond)
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	waitForMuxExporterSnapshot(t, governed, func(snapshot RPCMuxDiagnosisExporterDeliverySnapshot) bool {
		return snapshot.Exported == 1 && snapshot.BreakerState == "closed" &&
			snapshot.ConsecutiveFailures == 0
	})
	snapshot := governed.RPCMuxDiagnosisExporterDeliverySnapshot()
	if snapshot.Health != "healthy" || snapshot.LastSuccessAt.IsZero() ||
		snapshot.LastErrorAt.IsZero() || snapshot.LastLatencyNanos <= 0 ||
		snapshot.MaxLatencyNanos < snapshot.LastLatencyNanos ||
		snapshot.AverageLatencyNanos <= 0 {
		t.Fatalf("recovered delivery SLO snapshot = %+v", snapshot)
	}
}

func TestGovernedRPCMuxDiagnosisEventExporterCapsHungCalls(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	exporter := newGovernedRPCMuxDiagnosisEventExporter(
		"hung-limit",
		RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {
			startOnce.Do(func() { close(started) })
			<-block
		}),
		RPCMuxDiagnosisExporterDeliveryConfig{
			QueueSize:               2,
			Timeout:                 5 * time.Millisecond,
			MaxHungCalls:            1,
			BreakerFailureThreshold: 10,
		},
	)
	governed := exporter.(*governedRPCMuxDiagnosisExporter)
	defer func() {
		close(block)
		if err := governed.Close(); err != nil {
			t.Error(err)
		}
	}()

	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	<-started
	waitForMuxExporterSnapshot(t, governed, func(snapshot RPCMuxDiagnosisExporterDeliverySnapshot) bool {
		return snapshot.HungCalls == 1 && snapshot.OperatorAction == "pause_sink_hung_calls"
	})
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	snapshot := governed.RPCMuxDiagnosisExporterDeliverySnapshot()
	if snapshot.Health != "unhealthy" || snapshot.Backpressure == 0 || snapshot.MaxHungCalls != 1 {
		t.Fatalf("hung-call snapshot = %+v, want unhealthy cap and backpressure", snapshot)
	}
}

func TestGovernedRPCMuxDiagnosisEventExporterErrorBudgetAutomation(t *testing.T) {
	var calls atomic.Int64
	exporter := newGovernedRPCMuxDiagnosisEventExporter(
		"error-budget",
		RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {
			if calls.Add(1) <= 2 {
				panic("budget burn")
			}
		}),
		RPCMuxDiagnosisExporterDeliveryConfig{
			QueueSize:               4,
			Timeout:                 time.Second,
			BreakerFailureThreshold: 10,
			ErrorBudget: RPCMuxDiagnosisExporterErrorBudgetConfig{
				Enabled:           true,
				MinSamples:        2,
				BurnRateThreshold: 0.5,
				PauseDuration:     time.Hour,
			},
		},
	)
	governed := exporter.(*governedRPCMuxDiagnosisExporter)
	defer governed.Close()

	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	waitForMuxExporterSnapshot(t, governed, func(snapshot RPCMuxDiagnosisExporterDeliverySnapshot) bool {
		return snapshot.ErrorBudgetPaused && snapshot.OperatorAction == "pause_sink_error_budget"
	})
	before := governed.RPCMuxDiagnosisExporterDeliverySnapshot()
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	after := governed.RPCMuxDiagnosisExporterDeliverySnapshot()
	if !after.ErrorBudgetPaused || after.Dropped <= before.Dropped || after.BurnRate <= 0 {
		t.Fatalf("error-budget snapshot before=%+v after=%+v", before, after)
	}
}

func TestRPCMuxDiagnosisSinkIsolationConfig(t *testing.T) {
	cfg := normalizeRPCMuxDiagnosisSinkIsolationConfig(RPCMuxDiagnosisSinkIsolationConfig{
		Mode:            RPCMuxDiagnosisSinkIsolationIsolatedProcess,
		ShutdownTimeout: 2 * time.Second,
		MaxMemoryBytes:  1024,
		MaxCPUPercent:   25,
		AuditFields:     map[string]string{"owner": "test"},
	})
	if cfg.Mode != RPCMuxDiagnosisSinkIsolationIsolatedProcess ||
		cfg.AuditFields["resource_boundary"] != "process" ||
		cfg.AuditFields["owner"] != "test" {
		t.Fatalf("isolated process config = %+v", cfg)
	}
	cfg.AuditFields["owner"] = "mutated"
	cloned := cloneRPCMuxDiagnosisSinkIsolationConfig(cfg)
	cfg.AuditFields["owner"] = "changed"
	if cloned.AuditFields["owner"] != "mutated" {
		t.Fatalf("cloned audit fields = %+v, want defensive copy", cloned.AuditFields)
	}

	wasm := normalizeRPCMuxDiagnosisSinkIsolationConfig(RPCMuxDiagnosisSinkIsolationConfig{Mode: RPCMuxDiagnosisSinkIsolationWASM})
	if wasm.AuditFields["resource_boundary"] != "wasm" {
		t.Fatalf("wasm config = %+v", wasm)
	}
	fallback := normalizeRPCMuxDiagnosisSinkIsolationConfig(RPCMuxDiagnosisSinkIsolationConfig{Mode: "unsupported"})
	if fallback.Mode != RPCMuxDiagnosisSinkIsolationInProcess || fallback.AuditFields["resource_boundary"] != "goroutine" {
		t.Fatalf("fallback config = %+v", fallback)
	}
	if err := validateRPCMuxDiagnosisSinkIsolationConfig(RPCMuxDiagnosisSinkIsolationConfig{Mode: "unsupported"}); err == nil {
		t.Fatal("unsupported isolation mode validated")
	}
	if err := validateRPCMuxDiagnosisSinkIsolationConfig(RPCMuxDiagnosisSinkIsolationConfig{ShutdownTimeout: -time.Second}); err == nil {
		t.Fatal("negative isolation timeout validated")
	}
}

func TestGovernedRPCMuxDiagnosisEventExporterHalfOpenAllowsSingleProbe(t *testing.T) {
	exporter := newGovernedRPCMuxDiagnosisEventExporter(
		"half-open-single-probe",
		RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {}),
		RPCMuxDiagnosisExporterDeliveryConfig{BreakerCooldown: time.Nanosecond},
	)
	governed := exporter.(*governedRPCMuxDiagnosisExporter)
	defer governed.Close()
	governed.breakerOpenedAt.Store(time.Now().Add(-time.Second).UnixNano())

	if !governed.allowDelivery() {
		t.Fatal("first half-open probe was rejected")
	}
	if governed.breakerState() != "half_open" {
		t.Fatalf("breaker state = %q, want half_open", governed.breakerState())
	}
	if governed.allowDelivery() {
		t.Fatal("second concurrent half-open probe was accepted")
	}
}

func TestGovernedRPCMuxDiagnosisEventExporterDrainsAcceptedEventsOnClose(t *testing.T) {
	records := make(chan RPCMuxDiagnosisEventRecord, 1)
	exporter := NewGovernedRPCMuxDiagnosisEventExporter(
		RPCMuxDiagnosisEventExporterFunc(func(_ context.Context, record RPCMuxDiagnosisEventRecord) {
			records <- record
		}),
		RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: 1, Timeout: time.Second},
	)
	closer := exporter.(*governedRPCMuxDiagnosisExporter)
	want := RPCMuxDiagnosisEventRecord{Target: "accepted-before-close"}
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), want)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-records:
		if got.Target != want.Target {
			t.Fatalf("drained record target = %q, want %q", got.Target, want.Target)
		}
	default:
		t.Fatal("accepted event was not drained on close")
	}
}

func TestGovernedRPCMuxDiagnosisEventExporterBoundaries(t *testing.T) {
	if exporter := NewGovernedRPCMuxDiagnosisEventExporter(nil, RPCMuxDiagnosisExporterDeliveryConfig{}); exporter != nil {
		t.Fatalf("nil exporter wrapper = %#v, want nil", exporter)
	}
	if snapshot := (*governedRPCMuxDiagnosisExporter)(nil).RPCMuxDiagnosisExporterDeliverySnapshot(); snapshot.Sink != "" ||
		snapshot.QueueSize != 0 || snapshot.Accepted != 0 || snapshot.Isolation.Mode != "" {
		t.Fatalf("nil exporter snapshot = %+v, want zero value", snapshot)
	}
	if err := (*governedRPCMuxDiagnosisExporter)(nil).Close(); err != nil {
		t.Fatalf("nil exporter close: %v", err)
	}
	//nolint:staticcheck // verifies the exported delivery wrapper preserves nil-context compatibility.
	(*governedRPCMuxDiagnosisExporter)(nil).ExportRPCMuxDiagnosisEvent(nil, RPCMuxDiagnosisEventRecord{})

	records := make(chan RPCMuxDiagnosisEventRecord, 1)
	exporter := NewGovernedRPCMuxDiagnosisEventExporter(
		RPCMuxDiagnosisEventExporterFunc(func(ctx context.Context, record RPCMuxDiagnosisEventRecord) {
			if ctx == nil {
				t.Error("governed exporter received nil context")
			}
			records <- record
		}),
		RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: -1, Timeout: -time.Second},
	)
	governed := exporter.(*governedRPCMuxDiagnosisExporter)
	snapshot := governed.RPCMuxDiagnosisExporterDeliverySnapshot()
	if snapshot.QueueSize != defaultRPCMuxDiagnosisExporterQueueSize || snapshot.Timeout != int64(defaultRPCMuxDiagnosisExporterTimeout) {
		t.Fatalf("default delivery snapshot = %+v", snapshot)
	}

	//nolint:staticcheck // verifies nil contexts are normalized before asynchronous delivery.
	exporter.ExportRPCMuxDiagnosisEvent(nil, RPCMuxDiagnosisEventRecord{Target: "nil-context"})
	select {
	case record := <-records:
		if record.Target != "nil-context" {
			t.Fatalf("record target = %q", record.Target)
		}
	case <-time.After(time.Second):
		t.Fatal("nil-context record was not exported")
	}
	if err := governed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := governed.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	before := governed.RPCMuxDiagnosisExporterDeliverySnapshot()
	exporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{Target: "after-close"})
	after := governed.RPCMuxDiagnosisExporterDeliverySnapshot()
	if !after.Closed || after.Accepted != before.Accepted {
		t.Fatalf("after-close snapshot = %+v, before = %+v", after, before)
	}
}

func TestCloseRPCMuxDiagnosisExporterContracts(t *testing.T) {
	var closed atomic.Int64
	closeRPCMuxDiagnosisExporter(RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {}))
	closeRPCMuxDiagnosisExporter(&closableMuxDiagnosisExporter{closed: &closed})
	if closed.Load() != 1 {
		t.Fatalf("close count = %d, want 1", closed.Load())
	}
}

func TestMuxDiagnosisExporterDeliveryOptionWiring(t *testing.T) {
	filter := RPCMuxDiagnosisFilter{ConnectionID: "conn-1"}
	config := RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: 3, Timeout: 25 * time.Millisecond}
	sink := RPCMuxDiagnosisEventExporterFunc(func(context.Context, RPCMuxDiagnosisEventRecord) {})

	clientOptions := clientOptions{}
	WithMuxDiagnosisEventExporterDelivery(sink, filter, config)(&clientOptions)
	clientDelivery, ok := clientOptions.muxEventExporter.(*governedRPCMuxDiagnosisExporter)
	if !ok || clientOptions.muxEventFilter.ConnectionID != filter.ConnectionID {
		t.Fatalf("client exporter=%T filter=%+v", clientOptions.muxEventExporter, clientOptions.muxEventFilter)
	}
	clientSnapshot := clientDelivery.RPCMuxDiagnosisExporterDeliverySnapshot()
	if clientSnapshot.QueueSize != config.QueueSize || clientSnapshot.Timeout != int64(config.Timeout) {
		t.Fatalf("client delivery snapshot = %+v", clientSnapshot)
	}
	t.Cleanup(func() {
		if err := clientDelivery.Close(); err != nil {
			t.Errorf("close client delivery: %v", err)
		}
	})

	serverOptions := serverOptions{}
	WithServerMuxDiagnosisEventExporterDelivery(sink, filter, config)(&serverOptions)
	serverDelivery, ok := serverOptions.muxEventExporter.(*governedRPCMuxDiagnosisExporter)
	if !ok || serverOptions.muxEventFilter.ConnectionID != filter.ConnectionID {
		t.Fatalf("server exporter=%T filter=%+v", serverOptions.muxEventExporter, serverOptions.muxEventFilter)
	}
	serverSnapshot := serverDelivery.RPCMuxDiagnosisExporterDeliverySnapshot()
	if serverSnapshot.QueueSize != config.QueueSize || serverSnapshot.Timeout != int64(config.Timeout) {
		t.Fatalf("server delivery snapshot = %+v", serverSnapshot)
	}
	t.Cleanup(func() {
		if err := serverDelivery.Close(); err != nil {
			t.Errorf("close server delivery: %v", err)
		}
	})
}

func TestRPCConnectionModeOptionContracts(t *testing.T) {
	dial := EndpointConnDialer(func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("dial should not run while configuring options")
	})
	tests := []struct {
		name string
		opt  ClientOption
		mode string
	}{
		{name: "pool", opt: WithConnPool(dial, ConnPoolConfig{Mode: ConnPoolModePool}), mode: ConnPoolModePool},
		{name: "short", opt: WithShortConnection(dial), mode: ConnPoolModeShort},
		{name: "long", opt: WithLongConnection(dial), mode: ConnPoolModeLong},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := clientOptions{}
			test.opt(&options)
			if options.connPool == nil || options.connPool.conf.Mode != test.mode {
				t.Fatalf("connection manager = %+v, want mode %q", options.connPool, test.mode)
			}
		})
	}
}

func TestMuxDiagnosisLoggingOptionWiring(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	filter := RPCMuxDiagnosisFilter{Endpoint: "http://127.0.0.1:9000"}

	clientOptions := clientOptions{}
	WithMuxDiagnosisEventLogging(logger, filter)(&clientOptions)
	if clientOptions.muxEventExporter == nil || clientOptions.muxEventFilter.Endpoint != filter.Endpoint {
		t.Fatalf("client logging exporter=%T filter=%+v", clientOptions.muxEventExporter, clientOptions.muxEventFilter)
	}
	clientOptions.muxEventExporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{Endpoint: "client"})

	serverOptions := serverOptions{}
	WithServerMuxDiagnosisEventLogging(logger, filter)(&serverOptions)
	if serverOptions.muxEventExporter == nil || serverOptions.muxEventFilter.Endpoint != filter.Endpoint {
		t.Fatalf("server logging exporter=%T filter=%+v", serverOptions.muxEventExporter, serverOptions.muxEventFilter)
	}
	serverOptions.muxEventExporter.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{Endpoint: "server"})

	if !stringsContainAll(output.String(), "client", "server") {
		t.Fatalf("slog output = %q, want client and server records", output.String())
	}
}

type closableMuxDiagnosisExporter struct {
	closed *atomic.Int64
}

func (*closableMuxDiagnosisExporter) ExportRPCMuxDiagnosisEvent(context.Context, RPCMuxDiagnosisEventRecord) {
}

func (e *closableMuxDiagnosisExporter) Close() error {
	e.closed.Add(1)
	return nil
}

var _ io.Closer = (*closableMuxDiagnosisExporter)(nil)

func stringsContainAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !bytes.Contains([]byte(value), []byte(part)) {
			return false
		}
	}
	return true
}

func waitForMuxExporterSnapshot(t *testing.T, exporter RPCMuxDiagnosisExporterDeliverySnapshotter, condition func(RPCMuxDiagnosisExporterDeliverySnapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := exporter.RPCMuxDiagnosisExporterDeliverySnapshot()
		if condition(snapshot) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("delivery condition not met: %+v", exporter.RPCMuxDiagnosisExporterDeliverySnapshot())
}
