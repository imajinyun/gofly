package rpc

import (
	"context"
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
)

// RPCMuxDiagnosisExporterDeliveryConfig controls asynchronous bounded delivery
// to an application-owned diagnosis exporter.
type RPCMuxDiagnosisExporterDeliveryConfig struct {
	QueueSize               int           `json:"queueSize,omitempty"`
	Timeout                 time.Duration `json:"timeout,omitempty"`
	BreakerFailureThreshold int           `json:"breakerFailureThreshold,omitempty"`
	BreakerCooldown         time.Duration `json:"breakerCooldown,omitempty"`
}

// RPCMuxDiagnosisExporterDeliverySnapshot exposes low-cardinality delivery
// health without including event payloads or sink profile values.
type RPCMuxDiagnosisExporterDeliverySnapshot struct {
	Sink                string    `json:"sink"`
	QueueSize           int       `json:"queueSize"`
	QueueDepth          int       `json:"queueDepth"`
	Timeout             int64     `json:"timeoutNanos"`
	Accepted            int64     `json:"accepted"`
	Exported            int64     `json:"exported"`
	Dropped             int64     `json:"dropped"`
	Backpressure        int64     `json:"backpressure"`
	TimedOut            int64     `json:"timedOut"`
	Panics              int64     `json:"panics"`
	BreakerRejected     int64     `json:"breakerRejected"`
	ConsecutiveFailures int64     `json:"consecutiveFailures"`
	Health              string    `json:"health"`
	BreakerState        string    `json:"breakerState"`
	LastSuccessAt       time.Time `json:"lastSuccessAt,omitempty"`
	LastErrorAt         time.Time `json:"lastErrorAt,omitempty"`
	LastError           string    `json:"lastError,omitempty"`
	LastLatencyNanos    int64     `json:"lastLatencyNanos,omitempty"`
	MaxLatencyNanos     int64     `json:"maxLatencyNanos,omitempty"`
	AverageLatencyNanos int64     `json:"averageLatencyNanos,omitempty"`
	Closed              bool      `json:"closed"`
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
	consecutiveFailures atomic.Int64
	breakerOpenedAt     atomic.Int64
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
	go func() {
		defer func() { <-e.inFlight }()
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

func (e *governedRPCMuxDiagnosisExporter) recordSuccess(latency time.Duration) {
	now := time.Now()
	e.lastSuccessAt.Store(now.UnixNano())
	e.consecutiveFailures.Store(0)
	e.breakerOpenedAt.Store(0)
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
	if e.breakerOpenedAt.Load() != 0 {
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
