// Package callstats aggregates low-cardinality call phase diagnostics.
package callstats

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Stable phase names shared by REST, RPC, gRPC and future transports.
const (
	PhaseResolve    = "resolve"
	PhaseLoadBal    = "lb"
	PhaseConnect    = "connect"
	PhaseSend       = "send"
	PhaseRecv       = "recv"
	PhaseHandler    = "handler"
	PhaseRetry      = "retry"
	PhaseFallback   = "fallback"
	PhaseBreaker    = "breaker"
	PhaseGovernance = "governance"
)

// Registry collects aggregate phase metrics without high-cardinality labels.
type Registry struct {
	mu     sync.RWMutex
	phases map[string]*phaseStats
}

type phaseStats struct {
	calls         int64
	errors        int64
	totalDuration time.Duration
	maxDuration   time.Duration
}

// PhaseSnapshot captures aggregate diagnostics for one call phase.
type PhaseSnapshot struct {
	Phase         string        `json:"phase"`
	Calls         int64         `json:"calls"`
	Errors        int64         `json:"errors,omitempty"`
	TotalDuration time.Duration `json:"totalDuration,omitempty"`
	MaxDuration   time.Duration `json:"maxDuration,omitempty"`
	AvgDuration   time.Duration `json:"avgDuration,omitempty"`
}

// Snapshot is a deterministic point-in-time copy of phase diagnostics.
type Snapshot struct {
	Phases []PhaseSnapshot `json:"phases,omitempty"`
}

// NewRegistry creates an empty call stats registry.
func NewRegistry() *Registry {
	return &Registry{phases: make(map[string]*phaseStats)}
}

// Observe records one completed phase.
func (r *Registry) Observe(phase string, duration time.Duration, failed bool) {
	phase = strings.TrimSpace(phase)
	if r == nil || phase == "" {
		return
	}
	if duration < 0 {
		duration = 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.phases == nil {
		r.phases = make(map[string]*phaseStats)
	}
	stats := r.phases[phase]
	if stats == nil {
		stats = &phaseStats{}
		r.phases[phase] = stats
	}
	stats.calls++
	if failed {
		stats.errors++
	}
	stats.totalDuration += duration
	if duration > stats.maxDuration {
		stats.maxDuration = duration
	}
}

// ObserveSince records one phase using the elapsed time since start.
func (r *Registry) ObserveSince(phase string, start time.Time, failed bool) {
	if start.IsZero() {
		r.Observe(phase, 0, failed)
		return
	}
	r.Observe(phase, time.Since(start), failed)
}

// Snapshot returns deterministic aggregate phase diagnostics.
func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.phases))
	for key := range r.phases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := Snapshot{Phases: make([]PhaseSnapshot, 0, len(keys))}
	for _, key := range keys {
		stats := r.phases[key]
		if stats == nil {
			continue
		}
		var avg time.Duration
		if stats.calls > 0 {
			avg = stats.totalDuration / time.Duration(stats.calls)
		}
		out.Phases = append(out.Phases, PhaseSnapshot{
			Phase:         key,
			Calls:         stats.calls,
			Errors:        stats.errors,
			TotalDuration: stats.totalDuration,
			MaxDuration:   stats.maxDuration,
			AvgDuration:   avg,
		})
	}
	return out
}
