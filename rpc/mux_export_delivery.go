package rpc

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imajinyun/gofly/core/observability/metrics"
)

const (
	defaultRPCMuxDiagnosisExporterQueueSize = 64
	defaultRPCMuxDiagnosisExporterTimeout   = time.Second
	defaultRPCMuxDiagnosisBreakerFailures   = 3
	defaultRPCMuxDiagnosisBreakerCooldown   = 30 * time.Second
	defaultRPCMuxDiagnosisMaxHungCalls      = 1
	defaultRPCMuxDiagnosisErrorBudgetMin    = 10
	defaultRPCMuxDiagnosisErrorBudgetPause  = 30 * time.Second

	RPCMuxDiagnosisSinkIsolationInProcess       = "in_process"
	RPCMuxDiagnosisSinkIsolationIsolatedProcess = "isolated_process"
	RPCMuxDiagnosisSinkIsolationWASM            = "wasm"
)

// RPCMuxDiagnosisSinkIsolationConfig describes the intended isolation profile
// for one sink generation. Non in-process modes are explicit operator
// contracts; applications must provide the actual isolated exporter.
type RPCMuxDiagnosisSinkIsolationConfig struct {
	Mode            string            `json:"mode,omitempty"`
	ShutdownTimeout time.Duration     `json:"shutdownTimeout,omitempty"`
	MaxMemoryBytes  int64             `json:"maxMemoryBytes,omitempty"`
	MaxCPUPercent   int               `json:"maxCpuPercent,omitempty"`
	AuditFields     map[string]string `json:"auditFields,omitempty"`
}

// RPCMuxDiagnosisExporterErrorBudgetConfig controls optional burn-rate based
// delivery automation for one diagnosis exporter sink.
type RPCMuxDiagnosisExporterErrorBudgetConfig struct {
	Enabled                   bool          `json:"enabled,omitempty"`
	MinSamples                int64         `json:"minSamples,omitempty"`
	BurnRateThreshold         float64       `json:"burnRateThreshold,omitempty"`
	RecoveryBurnRateThreshold float64       `json:"recoveryBurnRateThreshold,omitempty"`
	PauseDuration             time.Duration `json:"pauseDuration,omitempty"`
}

// RPCMuxDiagnosisExporterDeliveryConfig controls asynchronous bounded delivery
// to an application-owned diagnosis exporter.
type RPCMuxDiagnosisExporterDeliveryConfig struct {
	QueueSize               int                                      `json:"queueSize,omitempty"`
	Timeout                 time.Duration                            `json:"timeout,omitempty"`
	MaxHungCalls            int                                      `json:"maxHungCalls,omitempty"`
	BreakerFailureThreshold int                                      `json:"breakerFailureThreshold,omitempty"`
	BreakerCooldown         time.Duration                            `json:"breakerCooldown,omitempty"`
	ErrorBudget             RPCMuxDiagnosisExporterErrorBudgetConfig `json:"errorBudget,omitempty"`
	Isolation               RPCMuxDiagnosisSinkIsolationConfig       `json:"isolation,omitempty"`
}

// RPCMuxDiagnosisExporterDeliverySnapshot exposes low-cardinality delivery
// health without including event payloads or sink profile values.
type RPCMuxDiagnosisExporterDeliverySnapshot struct {
	Sink                string                             `json:"sink"`
	QueueSize           int                                `json:"queueSize"`
	QueueDepth          int                                `json:"queueDepth"`
	Timeout             int64                              `json:"timeoutNanos"`
	Accepted            int64                              `json:"accepted"`
	Exported            int64                              `json:"exported"`
	Dropped             int64                              `json:"dropped"`
	Backpressure        int64                              `json:"backpressure"`
	TimedOut            int64                              `json:"timedOut"`
	Panics              int64                              `json:"panics"`
	BreakerRejected     int64                              `json:"breakerRejected"`
	ConsecutiveFailures int64                              `json:"consecutiveFailures"`
	MaxHungCalls        int                                `json:"maxHungCalls"`
	ActiveCalls         int64                              `json:"activeCalls"`
	HungCalls           int64                              `json:"hungCalls"`
	BurnRate            float64                            `json:"burnRate"`
	ErrorBudgetPaused   bool                               `json:"errorBudgetPaused"`
	OperatorAction      string                             `json:"operatorAction,omitempty"`
	OperatorPaused      bool                               `json:"operatorPaused,omitempty"`
	OperatorPauseReason string                             `json:"operatorPauseReason,omitempty"`
	Isolation           RPCMuxDiagnosisSinkIsolationConfig `json:"isolation"`
	Health              string                             `json:"health"`
	BreakerState        string                             `json:"breakerState"`
	LastSuccessAt       time.Time                          `json:"lastSuccessAt,omitempty"`
	LastErrorAt         time.Time                          `json:"lastErrorAt,omitempty"`
	LastError           string                             `json:"lastError,omitempty"`
	LastLatencyNanos    int64                              `json:"lastLatencyNanos,omitempty"`
	MaxLatencyNanos     int64                              `json:"maxLatencyNanos,omitempty"`
	AverageLatencyNanos int64                              `json:"averageLatencyNanos,omitempty"`
	Closed              bool                               `json:"closed"`
}

func (e *governedRPCMuxDiagnosisExporter) forceProbe() {
	if e == nil {
		return
	}
	e.breakerOpenedAt.Store(0)
	e.errorBudgetPausedAt.Store(0)
	e.halfOpen.Store(false)
}

// RPCMuxDiagnosisExporterDeliverySnapshotter is implemented by governed
// exporters that expose bounded-delivery health.
type RPCMuxDiagnosisExporterDeliverySnapshotter interface {
	RPCMuxDiagnosisExporterDeliverySnapshot() RPCMuxDiagnosisExporterDeliverySnapshot
}

type governedRPCMuxDiagnosisExporter struct {
	exporter            RPCMuxDiagnosisEventExporter
	sink                string
	config              RPCMuxDiagnosisExporterDeliveryConfig
	queue               chan rpcMuxDiagnosisDelivery
	done                chan struct{}
	inFlight            chan struct{}
	closed              atomic.Bool
	accepted            atomic.Int64
	exported            atomic.Int64
	dropped             atomic.Int64
	backpressure        atomic.Int64
	timedOut            atomic.Int64
	panics              atomic.Int64
	breakerRejected     atomic.Int64
	activeCalls         atomic.Int64
	hungCalls           atomic.Int64
	consecutiveFailures atomic.Int64
	breakerOpenedAt     atomic.Int64
	errorBudgetPausedAt atomic.Int64
	halfOpen            atomic.Bool
	queued              atomic.Int64
	lastSuccessAt       atomic.Int64
	lastErrorAt         atomic.Int64
	lastError           atomic.Value
	lastLatency         atomic.Int64
	maxLatency          atomic.Int64
	totalLatency        atomic.Int64
	latencyCount        atomic.Int64
	lifecycle           sync.RWMutex
	close               sync.Once
	wait                sync.WaitGroup
}

type rpcMuxDiagnosisDelivery struct {
	ctx    context.Context
	record RPCMuxDiagnosisEventRecord
}

var (
	rpcMuxDiagnosisExporterDeliveryEvents      *metrics.Counter
	rpcMuxDiagnosisExporterQueueDepth          *metrics.Gauge
	rpcMuxDiagnosisExporterDeliveryLatency     *metrics.Histogram
	rpcMuxDiagnosisExporterConsecutiveFailures *metrics.Gauge
	rpcMuxDiagnosisExporterBreakerOpen         *metrics.Gauge
	rpcMuxDiagnosisExporterQueueDepthBySink    sync.Map
)

func init() {
	registerRPCMuxDiagnosisExporterDeliveryMetrics(metrics.Default)
}

func registerRPCMuxDiagnosisExporterDeliveryMetrics(registry *metrics.Registry) {
	if registry == nil {
		registry = metrics.Default
	}
	rpcMuxDiagnosisExporterDeliveryEvents = registry.Counter(
		"gofly_rpc_mux_diagnosis_exporter_delivery_total",
		"Total mux diagnosis exporter delivery outcomes by sink.",
		"outcome",
		"sink",
	)
	rpcMuxDiagnosisExporterQueueDepth = registry.Gauge(
		"gofly_rpc_mux_diagnosis_exporter_queue_depth",
		"Current bounded mux diagnosis exporter queue depth by sink.",
		"sink",
	)
	rpcMuxDiagnosisExporterDeliveryLatency = registry.Histogram(
		"gofly_rpc_mux_diagnosis_exporter_delivery_duration_seconds",
		"Mux diagnosis exporter delivery latency by sink.",
		[]float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 5, 30},
		"sink",
	)
	rpcMuxDiagnosisExporterConsecutiveFailures = registry.Gauge(
		"gofly_rpc_mux_diagnosis_exporter_consecutive_failures",
		"Current consecutive mux diagnosis exporter failures by sink.",
		"sink",
	)
	rpcMuxDiagnosisExporterBreakerOpen = registry.Gauge(
		"gofly_rpc_mux_diagnosis_exporter_breaker_open",
		"Whether the mux diagnosis exporter circuit breaker is open by sink.",
		"sink",
	)
	rpcMuxDiagnosisExporterQueueDepthBySink.Range(func(key, value any) bool {
		sink, sinkOK := key.(string)
		depth, depthOK := value.(*atomic.Int64)
		if sinkOK && depthOK {
			rpcMuxDiagnosisExporterQueueDepth.Set(float64(depth.Load()), sink)
		}
		return true
	})
}

// NewGovernedRPCMuxDiagnosisEventExporter wraps an exporter with a bounded
// queue, per-export timeout, panic isolation, and delivery counters.
func NewGovernedRPCMuxDiagnosisEventExporter(exporter RPCMuxDiagnosisEventExporter, config RPCMuxDiagnosisExporterDeliveryConfig) RPCMuxDiagnosisEventExporter {
	return newGovernedRPCMuxDiagnosisEventExporter("default", exporter, config)
}

func newGovernedRPCMuxDiagnosisEventExporter(sink string, exporter RPCMuxDiagnosisEventExporter, config RPCMuxDiagnosisExporterDeliveryConfig) RPCMuxDiagnosisEventExporter {
	if exporter == nil {
		return nil
	}
	config = normalizeRPCMuxDiagnosisExporterDeliveryConfig(config)
	sink = normalizeRPCMuxOTelLogSinkName(sink)
	if sink == "" {
		sink = "default"
	}
	governed := &governedRPCMuxDiagnosisExporter{
		exporter: exporter,
		sink:     sink,
		config:   config,
		queue:    make(chan rpcMuxDiagnosisDelivery, config.QueueSize),
		done:     make(chan struct{}),
		inFlight: make(chan struct{}, 1),
	}
	governed.lastError.Store("")
	governed.wait.Go(governed.run)
	return governed
}

func normalizeRPCMuxDiagnosisExporterDeliveryConfig(config RPCMuxDiagnosisExporterDeliveryConfig) RPCMuxDiagnosisExporterDeliveryConfig {
	if config.QueueSize <= 0 {
		config.QueueSize = defaultRPCMuxDiagnosisExporterQueueSize
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultRPCMuxDiagnosisExporterTimeout
	}
	if config.BreakerFailureThreshold <= 0 {
		config.BreakerFailureThreshold = defaultRPCMuxDiagnosisBreakerFailures
	}
	if config.BreakerCooldown <= 0 {
		config.BreakerCooldown = defaultRPCMuxDiagnosisBreakerCooldown
	}
	if config.MaxHungCalls <= 0 {
		config.MaxHungCalls = defaultRPCMuxDiagnosisMaxHungCalls
	}
	if config.ErrorBudget.Enabled {
		if config.ErrorBudget.MinSamples <= 0 {
			config.ErrorBudget.MinSamples = defaultRPCMuxDiagnosisErrorBudgetMin
		}
		if config.ErrorBudget.BurnRateThreshold <= 0 {
			config.ErrorBudget.BurnRateThreshold = 1
		}
		if config.ErrorBudget.RecoveryBurnRateThreshold < 0 {
			config.ErrorBudget.RecoveryBurnRateThreshold = 0
		}
		if config.ErrorBudget.PauseDuration <= 0 {
			config.ErrorBudget.PauseDuration = defaultRPCMuxDiagnosisErrorBudgetPause
		}
	}
	config.Isolation = normalizeRPCMuxDiagnosisSinkIsolationConfig(config.Isolation)
	return config
}

func normalizeRPCMuxDiagnosisSinkIsolationConfig(config RPCMuxDiagnosisSinkIsolationConfig) RPCMuxDiagnosisSinkIsolationConfig {
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	switch config.Mode {
	case "", RPCMuxDiagnosisSinkIsolationInProcess:
		config.Mode = RPCMuxDiagnosisSinkIsolationInProcess
	case RPCMuxDiagnosisSinkIsolationIsolatedProcess, RPCMuxDiagnosisSinkIsolationWASM:
	default:
		config.Mode = RPCMuxDiagnosisSinkIsolationInProcess
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = defaultRPCMuxDiagnosisExporterTimeout
	}
	if config.MaxMemoryBytes < 0 {
		config.MaxMemoryBytes = 0
	}
	if config.MaxCPUPercent < 0 {
		config.MaxCPUPercent = 0
	}
	config.AuditFields = cloneStringMap(config.AuditFields)
	if config.AuditFields == nil {
		config.AuditFields = make(map[string]string, 2)
	}
	config.AuditFields["isolation_mode"] = config.Mode
	switch config.Mode {
	case RPCMuxDiagnosisSinkIsolationIsolatedProcess:
		config.AuditFields["resource_boundary"] = "process"
	case RPCMuxDiagnosisSinkIsolationWASM:
		config.AuditFields["resource_boundary"] = "wasm"
	default:
		config.AuditFields["resource_boundary"] = "goroutine"
	}
	return config
}

func validateRPCMuxDiagnosisSinkIsolationConfig(config RPCMuxDiagnosisSinkIsolationConfig) error {
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	switch mode {
	case "", RPCMuxDiagnosisSinkIsolationInProcess, RPCMuxDiagnosisSinkIsolationIsolatedProcess, RPCMuxDiagnosisSinkIsolationWASM:
	default:
		return fmt.Errorf("unsupported sink isolation mode %q", strings.TrimSpace(config.Mode))
	}
	if config.ShutdownTimeout < 0 {
		return fmt.Errorf("sink isolation shutdown timeout must not be negative")
	}
	if config.MaxMemoryBytes < 0 {
		return fmt.Errorf("sink isolation max memory bytes must not be negative")
	}
	if config.MaxCPUPercent < 0 {
		return fmt.Errorf("sink isolation max cpu percent must not be negative")
	}
	return nil
}

func cloneRPCMuxDiagnosisSinkIsolationConfig(config RPCMuxDiagnosisSinkIsolationConfig) RPCMuxDiagnosisSinkIsolationConfig {
	config.AuditFields = cloneStringMap(config.AuditFields)
	return config
}

func (e *governedRPCMuxDiagnosisExporter) ExportRPCMuxDiagnosisEvent(ctx context.Context, record RPCMuxDiagnosisEventRecord) {
	if e == nil {
		return
	}
	e.lifecycle.RLock()
	defer e.lifecycle.RUnlock()
	if e.closed.Load() {
		return
	}
	if e.errorBudgetPaused() {
		e.rejectErrorBudget()
		return
	}
	if e.hungCalls.Load() >= int64(e.config.MaxHungCalls) {
		e.rejectHungLimit()
		return
	}
	if !e.allowEnqueue() {
		e.rejectBreaker()
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	delivery := rpcMuxDiagnosisDelivery{ctx: context.WithoutCancel(ctx), record: record}
	e.updateQueueDepth(1)
	select {
	case e.queue <- delivery:
		e.accepted.Add(1)
		rpcMuxDiagnosisExporterDeliveryEvents.Inc("accepted", e.sink)
	default:
		e.updateQueueDepth(-1)
		e.dropped.Add(1)
		rpcMuxDiagnosisExporterDeliveryEvents.Inc("dropped", e.sink)
	}
}

func (e *governedRPCMuxDiagnosisExporter) run() {
	for {
		select {
		case delivery := <-e.queue:
			e.updateQueueDepth(-1)
			e.export(delivery)
		case <-e.done:
			e.drain()
			return
		}
	}
}

func (e *governedRPCMuxDiagnosisExporter) drain() {
	for {
		select {
		case delivery := <-e.queue:
			e.updateQueueDepth(-1)
			e.export(delivery)
		default:
			return
		}
	}
}

func (e *governedRPCMuxDiagnosisExporter) updateQueueDepth(delta int64) {
	e.queued.Add(delta)
	depth := rpcMuxDiagnosisExporterQueueDepthCounter(e.sink).Add(delta)
	rpcMuxDiagnosisExporterQueueDepth.Set(float64(depth), e.sink)
}

func (e *governedRPCMuxDiagnosisExporter) export(delivery rpcMuxDiagnosisDelivery) {
	if !e.allowDelivery() {
		e.rejectBreaker()
		return
	}
	if e.hungCalls.Load() >= int64(e.config.MaxHungCalls) {
		e.rejectHungLimit()
		return
	}
	select {
	case e.inFlight <- struct{}{}:
	default:
		e.halfOpen.Store(false)
		e.dropped.Add(1)
		e.backpressure.Add(1)
		rpcMuxDiagnosisExporterDeliveryEvents.Inc("backpressure", e.sink)
		return
	}
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(delivery.ctx, e.config.Timeout)
	defer cancel()
	completed := make(chan bool, 1)
	var timedOut atomic.Bool
	e.activeCalls.Add(1)
	go func() {
		defer func() {
			if timedOut.Load() {
				e.hungCalls.Add(-1)
			}
			e.activeCalls.Add(-1)
			<-e.inFlight
		}()
		panicked := false
		defer func() {
			if recover() != nil {
				panicked = true
				e.panics.Add(1)
				rpcMuxDiagnosisExporterDeliveryEvents.Inc("panic", e.sink)
			}
			completed <- panicked
		}()
		e.exporter.ExportRPCMuxDiagnosisEvent(ctx, delivery.record)
	}()
	select {
	case panicked := <-completed:
		if !panicked {
			e.exported.Add(1)
			rpcMuxDiagnosisExporterDeliveryEvents.Inc("exported", e.sink)
			e.recordSuccess(time.Since(startedAt))
		} else {
			e.recordFailure("panic", time.Since(startedAt))
		}
	case <-ctx.Done():
		timedOut.Store(true)
		e.hungCalls.Add(1)
		e.timedOut.Add(1)
		rpcMuxDiagnosisExporterDeliveryEvents.Inc("timeout", e.sink)
		e.recordFailure("timeout", time.Since(startedAt))
	}
}

func (e *governedRPCMuxDiagnosisExporter) RPCMuxDiagnosisExporterDeliverySnapshot() RPCMuxDiagnosisExporterDeliverySnapshot {
	if e == nil {
		return RPCMuxDiagnosisExporterDeliverySnapshot{}
	}
	return RPCMuxDiagnosisExporterDeliverySnapshot{
		Sink:                e.sink,
		QueueSize:           e.config.QueueSize,
		QueueDepth:          len(e.queue),
		Timeout:             int64(e.config.Timeout),
		Accepted:            e.accepted.Load(),
		Exported:            e.exported.Load(),
		Dropped:             e.dropped.Load(),
		Backpressure:        e.backpressure.Load(),
		TimedOut:            e.timedOut.Load(),
		Panics:              e.panics.Load(),
		BreakerRejected:     e.breakerRejected.Load(),
		ConsecutiveFailures: e.consecutiveFailures.Load(),
		MaxHungCalls:        e.config.MaxHungCalls,
		ActiveCalls:         e.activeCalls.Load(),
		HungCalls:           e.hungCalls.Load(),
		BurnRate:            e.burnRate(),
		ErrorBudgetPaused:   e.errorBudgetPaused(),
		OperatorAction:      e.operatorAction(),
		Isolation:           cloneRPCMuxDiagnosisSinkIsolationConfig(e.config.Isolation),
		Health:              e.health(),
		BreakerState:        e.breakerState(),
		LastSuccessAt:       unixNanoToTime(e.lastSuccessAt.Load()),
		LastErrorAt:         unixNanoToTime(e.lastErrorAt.Load()),
		LastError:           loadAtomicString(&e.lastError),
		LastLatencyNanos:    e.lastLatency.Load(),
		MaxLatencyNanos:     e.maxLatency.Load(),
		AverageLatencyNanos: averageInt64(e.totalLatency.Load(), e.latencyCount.Load()),
		Closed:              e.closed.Load(),
	}
}

func (e *governedRPCMuxDiagnosisExporter) Close() error {
	if e == nil {
		return nil
	}
	e.close.Do(func() {
		e.lifecycle.Lock()
		e.closed.Store(true)
		close(e.done)
		e.lifecycle.Unlock()
		e.wait.Wait()
		closeRPCMuxDiagnosisExporter(e.exporter)
	})
	return nil
}

func (e *governedRPCMuxDiagnosisExporter) allowDelivery() bool {
	openedAt := e.breakerOpenedAt.Load()
	if openedAt == 0 {
		return true
	}
	if time.Since(time.Unix(0, openedAt)) < e.config.BreakerCooldown {
		return false
	}
	return e.halfOpen.CompareAndSwap(false, true)
}

func (e *governedRPCMuxDiagnosisExporter) allowEnqueue() bool {
	openedAt := e.breakerOpenedAt.Load()
	return openedAt == 0 || time.Since(time.Unix(0, openedAt)) >= e.config.BreakerCooldown
}

func (e *governedRPCMuxDiagnosisExporter) rejectBreaker() {
	e.dropped.Add(1)
	e.breakerRejected.Add(1)
	rpcMuxDiagnosisExporterDeliveryEvents.Inc("breaker_open", e.sink)
}

func (e *governedRPCMuxDiagnosisExporter) rejectHungLimit() {
	e.dropped.Add(1)
	e.backpressure.Add(1)
	rpcMuxDiagnosisExporterDeliveryEvents.Inc("hung_limit", e.sink)
}

func (e *governedRPCMuxDiagnosisExporter) rejectErrorBudget() {
	e.dropped.Add(1)
	rpcMuxDiagnosisExporterDeliveryEvents.Inc("error_budget_paused", e.sink)
}

func (e *governedRPCMuxDiagnosisExporter) recordSuccess(latency time.Duration) {
	now := time.Now()
	e.lastSuccessAt.Store(now.UnixNano())
	e.consecutiveFailures.Store(0)
	e.breakerOpenedAt.Store(0)
	if !e.config.ErrorBudget.Enabled || e.burnRate() <= e.config.ErrorBudget.RecoveryBurnRateThreshold {
		e.errorBudgetPausedAt.Store(0)
	}
	e.halfOpen.Store(false)
	e.recordLatency(latency)
	rpcMuxDiagnosisExporterConsecutiveFailures.Set(0, e.sink)
	rpcMuxDiagnosisExporterBreakerOpen.Set(0, e.sink)
}

func (e *governedRPCMuxDiagnosisExporter) recordFailure(reason string, latency time.Duration) {
	now := time.Now()
	e.lastErrorAt.Store(now.UnixNano())
	e.lastError.Store(strings.TrimSpace(reason))
	failures := e.consecutiveFailures.Add(1)
	e.halfOpen.Store(false)
	e.recordLatency(latency)
	rpcMuxDiagnosisExporterConsecutiveFailures.Set(float64(failures), e.sink)
	if failures >= int64(e.config.BreakerFailureThreshold) {
		e.breakerOpenedAt.Store(now.UnixNano())
		rpcMuxDiagnosisExporterBreakerOpen.Set(1, e.sink)
	}
	e.evaluateErrorBudget(now)
}

func (e *governedRPCMuxDiagnosisExporter) recordLatency(latency time.Duration) {
	nanos := int64(latency)
	e.lastLatency.Store(nanos)
	e.totalLatency.Add(nanos)
	e.latencyCount.Add(1)
	for {
		current := e.maxLatency.Load()
		if nanos <= current || e.maxLatency.CompareAndSwap(current, nanos) {
			break
		}
	}
	rpcMuxDiagnosisExporterDeliveryLatency.Observe(latency.Seconds(), e.sink)
}

func (e *governedRPCMuxDiagnosisExporter) health() string {
	if e.closed.Load() {
		return "closed"
	}
	if e.errorBudgetPaused() || e.breakerOpenedAt.Load() != 0 || e.hungCalls.Load() >= int64(e.config.MaxHungCalls) {
		return "unhealthy"
	}
	if e.consecutiveFailures.Load() > 0 {
		return "degraded"
	}
	return "healthy"
}

func (e *governedRPCMuxDiagnosisExporter) breakerState() string {
	if e.breakerOpenedAt.Load() == 0 {
		return "closed"
	}
	if e.halfOpen.Load() {
		return "half_open"
	}
	return "open"
}

func (e *governedRPCMuxDiagnosisExporter) evaluateErrorBudget(now time.Time) {
	if !e.config.ErrorBudget.Enabled {
		return
	}
	total := e.accepted.Load() + e.dropped.Load()
	if total < e.config.ErrorBudget.MinSamples {
		return
	}
	if e.burnRate() >= e.config.ErrorBudget.BurnRateThreshold {
		e.errorBudgetPausedAt.CompareAndSwap(0, now.UnixNano())
	}
}

func (e *governedRPCMuxDiagnosisExporter) errorBudgetPaused() bool {
	if !e.config.ErrorBudget.Enabled {
		return false
	}
	pausedAt := e.errorBudgetPausedAt.Load()
	if pausedAt == 0 {
		return false
	}
	if time.Since(time.Unix(0, pausedAt)) < e.config.ErrorBudget.PauseDuration {
		return true
	}
	return false
}

func (e *governedRPCMuxDiagnosisExporter) burnRate() float64 {
	total := e.accepted.Load() + e.dropped.Load()
	if total <= 0 {
		return 0
	}
	failed := e.dropped.Load() + e.timedOut.Load() + e.panics.Load()
	return float64(failed) / float64(total)
}

func (e *governedRPCMuxDiagnosisExporter) operatorAction() string {
	switch {
	case e.closed.Load():
		return "none"
	case e.hungCalls.Load() >= int64(e.config.MaxHungCalls):
		return "pause_sink_hung_calls"
	case e.errorBudgetPaused():
		return "pause_sink_error_budget"
	case e.breakerState() == "half_open":
		return "probe_sink_recovery"
	case e.breakerOpenedAt.Load() != 0:
		return "pause_sink_breaker"
	case e.consecutiveFailures.Load() > 0:
		return "degrade_sink"
	default:
		return "none"
	}
}

func averageInt64(total int64, count int64) int64 {
	if count <= 0 {
		return 0
	}
	return total / count
}

func loadAtomicString(value *atomic.Value) string {
	loaded := value.Load()
	text, _ := loaded.(string)
	return text
}

func rpcMuxDiagnosisExporterQueueDepthCounter(sink string) *atomic.Int64 {
	counter, _ := rpcMuxDiagnosisExporterQueueDepthBySink.LoadOrStore(sink, new(atomic.Int64))
	return counter.(*atomic.Int64)
}

func closeRPCMuxDiagnosisExporter(exporter RPCMuxDiagnosisEventExporter) {
	closer, ok := exporter.(io.Closer)
	if ok {
		_ = closer.Close()
	}
}
