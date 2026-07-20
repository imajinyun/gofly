package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxRPCMuxDiagnosisSinkCount = 32

// RPCMuxDiagnosisSinkConfig configures one named registry sink in a fan-out set.
// Profile values are used only during construction and are never exposed by
// runtime snapshots.
type RPCMuxDiagnosisSinkConfig struct {
	Name     string                                `json:"name"`
	Profile  string                                `json:"profile,omitempty"`
	Priority int                                   `json:"priority,omitempty"`
	Delivery RPCMuxDiagnosisExporterDeliveryConfig `json:"delivery,omitempty"`
}

// RPCMuxDiagnosisSinkSetConfig describes one atomic sink-set generation.
type RPCMuxDiagnosisSinkSetConfig struct {
	Version string                      `json:"version"`
	Sinks   []RPCMuxDiagnosisSinkConfig `json:"sinks,omitempty"`
}

// RPCMuxDiagnosisSinkRuntimeSnapshot describes one active sink without profile
// values or provider instances.
type RPCMuxDiagnosisSinkRuntimeSnapshot struct {
	Name     string                                  `json:"name"`
	Priority int                                     `json:"priority"`
	Delivery RPCMuxDiagnosisExporterDeliverySnapshot `json:"delivery"`
}

// RPCMuxDiagnosisSinkSetSnapshot exposes hot-reload version, rollback, fan-out,
// and per-sink delivery health.
type RPCMuxDiagnosisSinkSetSnapshot struct {
	Version           string                               `json:"version,omitempty"`
	UpdatedAt         time.Time                            `json:"updatedAt,omitempty"`
	SinkCount         int                                  `json:"sinkCount"`
	Sinks             []RPCMuxDiagnosisSinkRuntimeSnapshot `json:"sinks,omitempty"`
	Reloads           int64                                `json:"reloads"`
	Rollbacks         int64                                `json:"rollbacks"`
	LastReloadAt      time.Time                            `json:"lastReloadAt,omitempty"`
	LastReloadErrorAt time.Time                            `json:"lastReloadErrorAt,omitempty"`
	LastReloadError   string                               `json:"lastReloadError,omitempty"`
	Closed            bool                                 `json:"closed"`
}

// RPCMuxDiagnosisSinkSetSnapshotter is implemented by versioned multi-sink
// exporters that expose hot-reload and per-sink SLO state.
type RPCMuxDiagnosisSinkSetSnapshotter interface {
	RPCMuxDiagnosisSinkSetSnapshot() RPCMuxDiagnosisSinkSetSnapshot
}

// RPCMuxDiagnosisSinkSet is a versioned, hot-reloadable, multi-sink exporter.
// Reload constructs and validates a complete candidate generation before the
// active pointer is swapped, so failures leave the current generation intact.
type RPCMuxDiagnosisSinkSet struct {
	reloadMu          sync.Mutex
	mu                sync.RWMutex
	active            *rpcMuxDiagnosisSinkGeneration
	reloads           int64
	rollbacks         int64
	lastReloadAt      time.Time
	lastReloadErrorAt time.Time
	lastReloadError   string
	closed            bool
}

type rpcMuxDiagnosisSinkGeneration struct {
	version   string
	updatedAt time.Time
	sinks     []rpcMuxDiagnosisSink
}

type rpcMuxDiagnosisSink struct {
	name     string
	priority int
	exporter RPCMuxDiagnosisEventExporter
	delivery RPCMuxDiagnosisExporterDeliverySnapshotter
}

// NewRPCMuxDiagnosisSinkSet builds the initial sink generation.
func NewRPCMuxDiagnosisSinkSet(config RPCMuxDiagnosisSinkSetConfig) (*RPCMuxDiagnosisSinkSet, error) {
	generation, err := buildRPCMuxDiagnosisSinkGeneration(config)
	if err != nil {
		return nil, err
	}
	return &RPCMuxDiagnosisSinkSet{
		active:       generation,
		reloads:      1,
		lastReloadAt: generation.updatedAt,
	}, nil
}

// ValidateRPCMuxDiagnosisSinkSetConfig validates one generation without
// constructing exporters or starting delivery workers.
func ValidateRPCMuxDiagnosisSinkSetConfig(config RPCMuxDiagnosisSinkSetConfig) error {
	_, err := validateRPCMuxDiagnosisSinkSetConfig(config)
	return err
}

// Reload atomically replaces the active generation after full validation and
// construction. The previous generation is drained only after the swap.
func (s *RPCMuxDiagnosisSinkSet) Reload(ctx context.Context, config RPCMuxDiagnosisSinkSetConfig) error {
	if s == nil {
		return errors.New("rpc mux diagnosis sink set is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return errors.New("rpc mux diagnosis sink set is closed")
	}

	generation, err := buildRPCMuxDiagnosisSinkGeneration(config)
	if err != nil {
		reason := classifyRPCMuxDiagnosisSinkReloadError(err)
		s.recordReloadFailure(reason)
		return fmt.Errorf("rpc mux diagnosis sink set reload failed: %s", reason)
	}
	if err := ctx.Err(); err != nil {
		generation.close()
		s.recordReloadFailure("reload context canceled")
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		generation.close()
		return errors.New("rpc mux diagnosis sink set is closed")
	}
	previous := s.active
	s.active = generation
	s.reloads++
	s.lastReloadAt = generation.updatedAt
	s.lastReloadErrorAt = time.Time{}
	s.lastReloadError = ""
	s.mu.Unlock()

	previous.close()
	return nil
}

// ExportRPCMuxDiagnosisEvent fans out one record in descending priority order.
// Each sink has an independent bounded queue and breaker, so one slow sink
// cannot block or suppress another sink.
func (s *RPCMuxDiagnosisSinkSet) ExportRPCMuxDiagnosisEvent(ctx context.Context, record RPCMuxDiagnosisEventRecord) {
	if s == nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.active == nil {
		return
	}
	for _, sink := range s.active.sinks {
		sink.exporter.ExportRPCMuxDiagnosisEvent(ctx, record)
	}
}

// RPCMuxDiagnosisSinkSetSnapshot returns a deterministic runtime view.
func (s *RPCMuxDiagnosisSinkSet) RPCMuxDiagnosisSinkSetSnapshot() RPCMuxDiagnosisSinkSetSnapshot {
	if s == nil {
		return RPCMuxDiagnosisSinkSetSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := RPCMuxDiagnosisSinkSetSnapshot{
		Reloads:           s.reloads,
		Rollbacks:         s.rollbacks,
		LastReloadAt:      s.lastReloadAt,
		LastReloadErrorAt: s.lastReloadErrorAt,
		LastReloadError:   s.lastReloadError,
		Closed:            s.closed,
	}
	if s.active == nil {
		return snapshot
	}
	snapshot.Version = s.active.version
	snapshot.UpdatedAt = s.active.updatedAt
	snapshot.SinkCount = len(s.active.sinks)
	snapshot.Sinks = make([]RPCMuxDiagnosisSinkRuntimeSnapshot, 0, len(s.active.sinks))
	for _, sink := range s.active.sinks {
		snapshot.Sinks = append(snapshot.Sinks, RPCMuxDiagnosisSinkRuntimeSnapshot{
			Name:     sink.name,
			Priority: sink.priority,
			Delivery: sink.delivery.RPCMuxDiagnosisExporterDeliverySnapshot(),
		})
	}
	return snapshot
}

// Close stops fan-out and drains every active sink exactly once.
func (s *RPCMuxDiagnosisSinkSet) Close() error {
	if s == nil {
		return nil
	}
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	active := s.active
	s.active = nil
	s.mu.Unlock()
	active.close()
	return nil
}

func (s *RPCMuxDiagnosisSinkSet) recordReloadFailure(reason string) {
	s.mu.Lock()
	s.rollbacks++
	s.lastReloadErrorAt = time.Now().UTC()
	s.lastReloadError = reason
	s.mu.Unlock()
}

func buildRPCMuxDiagnosisSinkGeneration(config RPCMuxDiagnosisSinkSetConfig) (generation *rpcMuxDiagnosisSinkGeneration, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			generation.close()
			generation = nil
			err = fmt.Errorf("rpc mux diagnosis sink set construction panic")
		}
	}()
	validated, err := validateRPCMuxDiagnosisSinkSetConfig(config)
	if err != nil {
		return nil, err
	}
	generation = &rpcMuxDiagnosisSinkGeneration{
		version:   validated.Version,
		updatedAt: time.Now().UTC(),
		sinks:     make([]rpcMuxDiagnosisSink, 0, len(validated.Sinks)),
	}
	for _, sinkConfig := range validated.Sinks {
		exporter := NewRPCMuxOTelLogSinkExporter(sinkConfig.Name, sinkConfig.Profile)
		if exporter == nil {
			generation.close()
			return nil, fmt.Errorf("rpc mux diagnosis sink %q exporter construction failed", sinkConfig.Name)
		}
		governed, ok := newGovernedRPCMuxDiagnosisEventExporter(sinkConfig.Name, exporter, sinkConfig.Delivery).(*governedRPCMuxDiagnosisExporter)
		if !ok {
			generation.close()
			return nil, fmt.Errorf("rpc mux diagnosis sink %q delivery construction failed", sinkConfig.Name)
		}
		generation.sinks = append(generation.sinks, rpcMuxDiagnosisSink{
			name:     sinkConfig.Name,
			priority: sinkConfig.Priority,
			exporter: governed,
			delivery: governed,
		})
	}
	sort.SliceStable(generation.sinks, func(i, j int) bool {
		if generation.sinks[i].priority == generation.sinks[j].priority {
			return generation.sinks[i].name < generation.sinks[j].name
		}
		return generation.sinks[i].priority > generation.sinks[j].priority
	})
	return generation, nil
}

func validateRPCMuxDiagnosisSinkSetConfig(config RPCMuxDiagnosisSinkSetConfig) (RPCMuxDiagnosisSinkSetConfig, error) {
	config.Version = strings.TrimSpace(config.Version)
	if config.Version == "" {
		return RPCMuxDiagnosisSinkSetConfig{}, errors.New("rpc mux diagnosis sink set version is required")
	}
	if len(config.Sinks) > maxRPCMuxDiagnosisSinkCount {
		return RPCMuxDiagnosisSinkSetConfig{}, fmt.Errorf("rpc mux diagnosis sink set has %d sinks; maximum is %d", len(config.Sinks), maxRPCMuxDiagnosisSinkCount)
	}
	validated := RPCMuxDiagnosisSinkSetConfig{
		Version: config.Version,
		Sinks:   make([]RPCMuxDiagnosisSinkConfig, 0, len(config.Sinks)),
	}
	seen := make(map[string]struct{}, len(config.Sinks))
	for _, sinkConfig := range config.Sinks {
		sinkConfig.Name = normalizeRPCMuxOTelLogSinkName(sinkConfig.Name)
		if sinkConfig.Name == "" {
			return RPCMuxDiagnosisSinkSetConfig{}, errors.New("rpc mux diagnosis sink name is required")
		}
		if _, ok := seen[sinkConfig.Name]; ok {
			return RPCMuxDiagnosisSinkSetConfig{}, fmt.Errorf("rpc mux diagnosis sink %q is duplicated", sinkConfig.Name)
		}
		seen[sinkConfig.Name] = struct{}{}
		if err := ValidateRPCMuxOTelLogSinkProfile(sinkConfig.Name, sinkConfig.Profile); err != nil {
			return RPCMuxDiagnosisSinkSetConfig{}, err
		}
		sinkConfig.Profile = strings.TrimSpace(sinkConfig.Profile)
		validated.Sinks = append(validated.Sinks, sinkConfig)
	}
	return validated, nil
}

func (g *rpcMuxDiagnosisSinkGeneration) close() {
	if g == nil {
		return
	}
	for _, sink := range g.sinks {
		closeRPCMuxDiagnosisExporter(sink.exporter)
	}
	g.sinks = nil
}

func classifyRPCMuxDiagnosisSinkReloadError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "not registered"):
		return "sink is not registered"
	case strings.Contains(message, "profile"):
		return "sink profile validation failed"
	case strings.Contains(message, "duplicated"):
		return "duplicate sink configuration"
	case strings.Contains(message, "maximum"):
		return "sink count limit exceeded"
	case strings.Contains(message, "panic"):
		return "sink construction panic"
	default:
		return "sink generation construction failed"
	}
}

var (
	_ RPCMuxDiagnosisEventExporter      = (*RPCMuxDiagnosisSinkSet)(nil)
	_ RPCMuxDiagnosisSinkSetSnapshotter = (*RPCMuxDiagnosisSinkSet)(nil)
	_ io.Closer                         = (*RPCMuxDiagnosisSinkSet)(nil)
)
