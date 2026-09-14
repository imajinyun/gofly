package limit

import (
	"math"
	"runtime/metrics"
	"sync"
)

const (
	runtimeCPUTotalMetric = "/cpu/classes/total:cpu-seconds"
	runtimeCPUIdleMetric  = "/cpu/classes/idle:cpu-seconds"
)

type runtimeCPUSampleReader func() (total float64, idle float64, ok bool)

// RuntimeCPUReader reports process-runtime CPU saturation in per-mille.
// The first sample establishes a baseline and reports zero so callers fail open.
type RuntimeCPUReader struct {
	mu          sync.Mutex
	read        runtimeCPUSampleReader
	initialized bool
	total       float64
	idle        float64
}

func NewRuntimeCPUReader() *RuntimeCPUReader {
	return newRuntimeCPUReader(readRuntimeCPUSample)
}

func newRuntimeCPUReader(read runtimeCPUSampleReader) *RuntimeCPUReader {
	return &RuntimeCPUReader{read: read}
}

func (r *RuntimeCPUReader) Permille() int {
	if r == nil || r.read == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	total, idle, ok := r.read()
	if !ok || math.IsNaN(total) || math.IsNaN(idle) || math.IsInf(total, 0) || math.IsInf(idle, 0) {
		return 0
	}
	if !r.initialized {
		r.initialized, r.total, r.idle = true, total, idle
		return 0
	}
	totalDelta, idleDelta := total-r.total, idle-r.idle
	if totalDelta <= 0 || idleDelta < 0 {
		r.total, r.idle = total, idle
		return 0
	}
	r.total, r.idle = total, idle
	busy := totalDelta - idleDelta
	if busy <= 0 {
		return 0
	}
	if busy >= totalDelta {
		return 1000
	}
	return int(math.Round(busy / totalDelta * 1000))
}

func readRuntimeCPUSample() (float64, float64, bool) {
	samples := []metrics.Sample{{Name: runtimeCPUTotalMetric}, {Name: runtimeCPUIdleMetric}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindFloat64 || samples[1].Value.Kind() != metrics.KindFloat64 {
		return 0, 0, false
	}
	return samples[0].Value.Float64(), samples[1].Value.Float64(), true
}
