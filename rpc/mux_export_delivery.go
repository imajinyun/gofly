package rpc

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/imajinyun/gofly/core/observability/metrics"
)

const (
	defaultRPCMuxDiagnosisExporterQueueSize = 64
	defaultRPCMuxDiagnosisExporterTimeout   = time.Second
)

// RPCMuxDiagnosisExporterDeliveryConfig controls asynchronous bounded delivery
// to an application-owned diagnosis exporter.
type RPCMuxDiagnosisExporterDeliveryConfig struct {
	QueueSize int           `json:"queueSize,omitempty"`
	Timeout   time.Duration `json:"timeout,omitempty"`
}

// RPCMuxDiagnosisExporterDeliverySnapshot exposes low-cardinality delivery
// health without including event payloads or sink profile values.
type RPCMuxDiagnosisExporterDeliverySnapshot struct {
	QueueSize    int   `json:"queueSize"`
	QueueDepth   int   `json:"queueDepth"`
	Timeout      int64 `json:"timeoutNanos"`
	Accepted     int64 `json:"accepted"`
	Exported     int64 `json:"exported"`
	Dropped      int64 `json:"dropped"`
	Backpressure int64 `json:"backpressure"`
	TimedOut     int64 `json:"timedOut"`
	Panics       int64 `json:"panics"`
	Closed       bool  `json:"closed"`
}

// RPCMuxDiagnosisExporterDeliverySnapshotter is implemented by governed
// exporters that expose bounded-delivery health.
type RPCMuxDiagnosisExporterDeliverySnapshotter interface {
	RPCMuxDiagnosisExporterDeliverySnapshot() RPCMuxDiagnosisExporterDeliverySnapshot
}

type governedRPCMuxDiagnosisExporter struct {
	exporter     RPCMuxDiagnosisEventExporter
	config       RPCMuxDiagnosisExporterDeliveryConfig
	queue        chan rpcMuxDiagnosisDelivery
	done         chan struct{}
	inFlight     chan struct{}
	closed       atomic.Bool
	accepted     atomic.Int64
	exported     atomic.Int64
	dropped      atomic.Int64
	backpressure atomic.Int64
	timedOut     atomic.Int64
	panics       atomic.Int64
	lifecycle    sync.RWMutex
	close        sync.Once
	wait         sync.WaitGroup
}

type rpcMuxDiagnosisDelivery struct {
	ctx    context.Context
	record RPCMuxDiagnosisEventRecord
}

var (
	rpcMuxDiagnosisExporterDeliveryEvents *metrics.Counter
	rpcMuxDiagnosisExporterQueueDepth     *metrics.Gauge
	rpcMuxDiagnosisExporterQueued         atomic.Int64
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
		"Total mux diagnosis exporter delivery outcomes.",
		"outcome",
	)
	rpcMuxDiagnosisExporterQueueDepth = registry.Gauge(
		"gofly_rpc_mux_diagnosis_exporter_queue_depth",
		"Current bounded mux diagnosis exporter queue depth.",
	)
	rpcMuxDiagnosisExporterQueueDepth.Set(float64(rpcMuxDiagnosisExporterQueued.Load()))
}

// NewGovernedRPCMuxDiagnosisEventExporter wraps an exporter with a bounded
// queue, per-export timeout, panic isolation, and delivery counters.
func NewGovernedRPCMuxDiagnosisEventExporter(exporter RPCMuxDiagnosisEventExporter, config RPCMuxDiagnosisExporterDeliveryConfig) RPCMuxDiagnosisEventExporter {
	if exporter == nil {
		return nil
	}
	config = normalizeRPCMuxDiagnosisExporterDeliveryConfig(config)
	governed := &governedRPCMuxDiagnosisExporter{
		exporter: exporter,
		config:   config,
		queue:    make(chan rpcMuxDiagnosisDelivery, config.QueueSize),
		done:     make(chan struct{}),
		inFlight: make(chan struct{}, 1),
	}
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
	if ctx == nil {
		ctx = context.Background()
	}
	delivery := rpcMuxDiagnosisDelivery{ctx: context.WithoutCancel(ctx), record: record}
	updateRPCMuxDiagnosisExporterQueueDepth(1)
	select {
	case e.queue <- delivery:
		e.accepted.Add(1)
		rpcMuxDiagnosisExporterDeliveryEvents.Inc("accepted")
	default:
		updateRPCMuxDiagnosisExporterQueueDepth(-1)
		e.dropped.Add(1)
		rpcMuxDiagnosisExporterDeliveryEvents.Inc("dropped")
	}
}

func (e *governedRPCMuxDiagnosisExporter) run() {
	for {
		select {
		case delivery := <-e.queue:
			updateRPCMuxDiagnosisExporterQueueDepth(-1)
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
			updateRPCMuxDiagnosisExporterQueueDepth(-1)
			e.export(delivery)
		default:
			return
		}
	}
}

func updateRPCMuxDiagnosisExporterQueueDepth(delta int64) {
	depth := rpcMuxDiagnosisExporterQueued.Add(delta)
	rpcMuxDiagnosisExporterQueueDepth.Set(float64(depth))
}

func (e *governedRPCMuxDiagnosisExporter) export(delivery rpcMuxDiagnosisDelivery) {
	select {
	case e.inFlight <- struct{}{}:
	default:
		e.dropped.Add(1)
		e.backpressure.Add(1)
		rpcMuxDiagnosisExporterDeliveryEvents.Inc("backpressure")
		return
	}
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
				rpcMuxDiagnosisExporterDeliveryEvents.Inc("panic")
			}
			completed <- panicked
		}()
		e.exporter.ExportRPCMuxDiagnosisEvent(ctx, delivery.record)
	}()
	select {
	case panicked := <-completed:
		if !panicked {
			e.exported.Add(1)
			rpcMuxDiagnosisExporterDeliveryEvents.Inc("exported")
		}
	case <-ctx.Done():
		e.timedOut.Add(1)
		rpcMuxDiagnosisExporterDeliveryEvents.Inc("timeout")
	}
}

func (e *governedRPCMuxDiagnosisExporter) RPCMuxDiagnosisExporterDeliverySnapshot() RPCMuxDiagnosisExporterDeliverySnapshot {
	if e == nil {
		return RPCMuxDiagnosisExporterDeliverySnapshot{}
	}
	return RPCMuxDiagnosisExporterDeliverySnapshot{
		QueueSize:    e.config.QueueSize,
		QueueDepth:   len(e.queue),
		Timeout:      int64(e.config.Timeout),
		Accepted:     e.accepted.Load(),
		Exported:     e.exported.Load(),
		Dropped:      e.dropped.Load(),
		Backpressure: e.backpressure.Load(),
		TimedOut:     e.timedOut.Load(),
		Panics:       e.panics.Load(),
		Closed:       e.closed.Load(),
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
	})
	return nil
}

func closeRPCMuxDiagnosisExporter(exporter RPCMuxDiagnosisEventExporter) {
	closer, ok := exporter.(io.Closer)
	if ok {
		_ = closer.Close()
	}
}
