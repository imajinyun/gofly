package rpc

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
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
	Name             string                                `json:"name"`
	Profile          string                                `json:"profile,omitempty"`
	ProfileRef       string                                `json:"profileRef,omitempty"`
	ProfileSchema    string                                `json:"profileSchema,omitempty"`
	ProfileMigration string                                `json:"profileMigration,omitempty"`
	Priority         int                                   `json:"priority,omitempty"`
	Delivery         RPCMuxDiagnosisExporterDeliveryConfig `json:"delivery,omitempty"`
}

// RPCMuxDiagnosisSinkSetConfig describes one atomic sink-set generation.
type RPCMuxDiagnosisSinkSetConfig struct {
	Version       string                        `json:"version"`
	SchemaVersion string                        `json:"schemaVersion,omitempty"`
	Sinks         []RPCMuxDiagnosisSinkConfig   `json:"sinks,omitempty"`
	Secrets       RPCMuxDiagnosisSecretResolver `json:"-"`
}

// RPCMuxDiagnosisSecretResolver resolves profile references before validation.
// Resolved profile values are never exposed by snapshots or diff plans.
type RPCMuxDiagnosisSecretResolver func(context.Context, string) (string, error)

// NewRPCMuxDiagnosisEnvSecretResolver resolves profile references of the form
// env://NAME. It intentionally supports only environment variables so generated
// projects can avoid logging or storing raw profile JSON.
func NewRPCMuxDiagnosisEnvSecretResolver() RPCMuxDiagnosisSecretResolver {
	return func(ctx context.Context, ref string) (string, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		ref = strings.TrimSpace(ref)
		const prefix = "env://"
		if !strings.HasPrefix(ref, prefix) {
			return "", fmt.Errorf("unsupported secret reference scheme")
		}
		name := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
		if name == "" || strings.ContainsAny(name, "/\\\x00") {
			return "", fmt.Errorf("invalid environment secret reference")
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("environment secret reference is not set")
		}
		return value, nil
	}
}

// NewRPCMuxDiagnosisFileSecretResolver resolves profile references of the form
// file://relative/path under root. Absolute paths, path traversal and oversized
// files are rejected.
func NewRPCMuxDiagnosisFileSecretResolver(root string, maxBytes int64) RPCMuxDiagnosisSecretResolver {
	root = filepath.Clean(strings.TrimSpace(root))
	if maxBytes <= 0 {
		maxBytes = maxRPCMuxOTelLogProfileBytes
	}
	return func(ctx context.Context, ref string) (string, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if root == "" || root == "." {
			return "", fmt.Errorf("file secret resolver root is required")
		}
		ref = strings.TrimSpace(ref)
		const prefix = "file://"
		if !strings.HasPrefix(ref, prefix) {
			return "", fmt.Errorf("unsupported secret reference scheme")
		}
		rel := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
		if rel == "" || !filepath.IsLocal(rel) {
			return "", fmt.Errorf("invalid file secret reference")
		}
		path := filepath.Join(root, rel)
		resolvedRel, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(resolvedRel) {
			return "", fmt.Errorf("file secret reference escapes root")
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("file secret reference stat: %w", err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("file secret reference is a directory")
		}
		if info.Size() > maxBytes {
			return "", fmt.Errorf("file secret reference exceeds %d bytes", maxBytes)
		}
		// #nosec G304 -- ref is constrained by file://, filepath.IsLocal, filepath.Rel, and max-size stat under the configured resolver root.
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("file secret reference read: %w", err)
		}
		return string(data), nil
	}
}

// NewRPCMuxDiagnosisLayeredSecretResolver tries resolvers in order until one
// resolves the reference. It preserves the last error for operator diagnosis.
func NewRPCMuxDiagnosisLayeredSecretResolver(resolvers ...RPCMuxDiagnosisSecretResolver) RPCMuxDiagnosisSecretResolver {
	return func(ctx context.Context, ref string) (string, error) {
		var lastErr error
		for _, resolver := range resolvers {
			if resolver == nil {
				continue
			}
			value, err := resolver(ctx, ref)
			if err == nil {
				return value, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return "", lastErr
		}
		return "", fmt.Errorf("secret reference resolver is not configured")
	}
}

// RPCMuxDiagnosisSinkRuntimeSnapshot describes one active sink without profile
// values or provider instances.
type RPCMuxDiagnosisSinkRuntimeSnapshot struct {
	Name             string                                  `json:"name"`
	ProfileSchema    string                                  `json:"profileSchema,omitempty"`
	ProfileMigration string                                  `json:"profileMigration,omitempty"`
	Priority         int                                     `json:"priority"`
	Delivery         RPCMuxDiagnosisExporterDeliverySnapshot `json:"delivery"`
}

// RPCMuxDiagnosisSinkSetDiffPlan describes a generation change without profile
// values. Operators can inspect it before applying a hot reload.
type RPCMuxDiagnosisSinkSetDiffPlan struct {
	FromVersion       string   `json:"fromVersion,omitempty"`
	ToVersion         string   `json:"toVersion,omitempty"`
	FromSchemaVersion string   `json:"fromSchemaVersion,omitempty"`
	ToSchemaVersion   string   `json:"toSchemaVersion,omitempty"`
	Activate          []string `json:"activate,omitempty"`
	Deactivate        []string `json:"deactivate,omitempty"`
	Retain            []string `json:"retain,omitempty"`
	ChangePriority    []string `json:"changePriority,omitempty"`
	ChangeProfile     []string `json:"changeProfile,omitempty"`
	ChangeDelivery    []string `json:"changeDelivery,omitempty"`
	MigrateProfile    []string `json:"migrateProfile,omitempty"`
}

// RPCMuxDiagnosisSinkSetSnapshot exposes hot-reload version, rollback, fan-out,
// and per-sink delivery health.
type RPCMuxDiagnosisSinkSetSnapshot struct {
	Version           string                               `json:"version,omitempty"`
	SchemaVersion     string                               `json:"schemaVersion,omitempty"`
	UpdatedAt         time.Time                            `json:"updatedAt,omitempty"`
	SinkCount         int                                  `json:"sinkCount"`
	Sinks             []RPCMuxDiagnosisSinkRuntimeSnapshot `json:"sinks,omitempty"`
	LastDiffPlan      RPCMuxDiagnosisSinkSetDiffPlan       `json:"lastDiffPlan,omitempty"`
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
	lastDiffPlan      RPCMuxDiagnosisSinkSetDiffPlan
	operatorHistory   []RPCMuxDiagnosisOperatorAction
	operatorStore     RPCMuxDiagnosisOperatorHistoryStore
	closed            bool
}

type rpcMuxDiagnosisSinkGeneration struct {
	version       string
	schemaVersion string
	updatedAt     time.Time
	sinks         []rpcMuxDiagnosisSink
	fingerprints  map[string]rpcMuxDiagnosisSinkFingerprint
}

type rpcMuxDiagnosisSink struct {
	name             string
	profileSchema    string
	profileMigration string
	priority         int
	exporter         RPCMuxDiagnosisEventExporter
	delivery         RPCMuxDiagnosisExporterDeliverySnapshotter
	paused           bool
	pauseReason      string
}

type rpcMuxDiagnosisSinkFingerprint struct {
	priority         int
	profileHash      string
	profileRef       string
	profileSchema    string
	profileMigration string
	delivery         RPCMuxDiagnosisExporterDeliveryConfig
}

// NewRPCMuxDiagnosisSinkSet builds the initial sink generation.
func NewRPCMuxDiagnosisSinkSet(config RPCMuxDiagnosisSinkSetConfig) (*RPCMuxDiagnosisSinkSet, error) {
	generation, err := buildRPCMuxDiagnosisSinkGeneration(context.Background(), config)
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
	_, err := validateRPCMuxDiagnosisSinkSetConfig(context.Background(), config)
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

	generation, err := buildRPCMuxDiagnosisSinkGeneration(ctx, config)
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
	s.lastDiffPlan = diffRPCMuxDiagnosisSinkGenerations(previous, generation)
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
		if sink.paused {
			continue
		}
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
	snapshot.SchemaVersion = s.active.schemaVersion
	snapshot.UpdatedAt = s.active.updatedAt
	snapshot.SinkCount = len(s.active.sinks)
	snapshot.LastDiffPlan = cloneRPCMuxDiagnosisSinkSetDiffPlan(s.lastDiffPlan)
	snapshot.Sinks = make([]RPCMuxDiagnosisSinkRuntimeSnapshot, 0, len(s.active.sinks))
	for _, sink := range s.active.sinks {
		delivery := sink.delivery.RPCMuxDiagnosisExporterDeliverySnapshot()
		delivery.OperatorPaused = sink.paused
		delivery.OperatorPauseReason = sink.pauseReason
		snapshot.Sinks = append(snapshot.Sinks, RPCMuxDiagnosisSinkRuntimeSnapshot{
			Name:             sink.name,
			ProfileSchema:    sink.profileSchema,
			ProfileMigration: sink.profileMigration,
			Priority:         sink.priority,
			Delivery:         delivery,
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

func buildRPCMuxDiagnosisSinkGeneration(ctx context.Context, config RPCMuxDiagnosisSinkSetConfig) (generation *rpcMuxDiagnosisSinkGeneration, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			generation.close()
			generation = nil
			err = fmt.Errorf("rpc mux diagnosis sink set construction panic")
		}
	}()
	validated, err := validateRPCMuxDiagnosisSinkSetConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	generation = &rpcMuxDiagnosisSinkGeneration{
		version:       validated.Version,
		schemaVersion: validated.SchemaVersion,
		updatedAt:     time.Now().UTC(),
		sinks:         make([]rpcMuxDiagnosisSink, 0, len(validated.Sinks)),
		fingerprints:  make(map[string]rpcMuxDiagnosisSinkFingerprint, len(validated.Sinks)),
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
			name:             sinkConfig.Name,
			profileSchema:    sinkConfig.ProfileSchema,
			profileMigration: sinkConfig.ProfileMigration,
			priority:         sinkConfig.Priority,
			exporter:         governed,
			delivery:         governed,
		})
		generation.fingerprints[sinkConfig.Name] = rpcMuxDiagnosisSinkFingerprint{
			priority:         sinkConfig.Priority,
			profileHash:      hashRPCMuxDiagnosisProfile(sinkConfig.Profile),
			profileRef:       sinkConfig.ProfileRef,
			profileSchema:    sinkConfig.ProfileSchema,
			profileMigration: sinkConfig.ProfileMigration,
			delivery:         sinkConfig.Delivery,
		}
	}
	sort.SliceStable(generation.sinks, func(i, j int) bool {
		if generation.sinks[i].priority == generation.sinks[j].priority {
			return generation.sinks[i].name < generation.sinks[j].name
		}
		return generation.sinks[i].priority > generation.sinks[j].priority
	})
	return generation, nil
}

func validateRPCMuxDiagnosisSinkSetConfig(ctx context.Context, config RPCMuxDiagnosisSinkSetConfig) (RPCMuxDiagnosisSinkSetConfig, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config.Version = strings.TrimSpace(config.Version)
	if config.Version == "" {
		return RPCMuxDiagnosisSinkSetConfig{}, errors.New("rpc mux diagnosis sink set version is required")
	}
	config.SchemaVersion = strings.TrimSpace(config.SchemaVersion)
	if len(config.Sinks) > maxRPCMuxDiagnosisSinkCount {
		return RPCMuxDiagnosisSinkSetConfig{}, fmt.Errorf("rpc mux diagnosis sink set has %d sinks; maximum is %d", len(config.Sinks), maxRPCMuxDiagnosisSinkCount)
	}
	validated := RPCMuxDiagnosisSinkSetConfig{
		Version:       config.Version,
		SchemaVersion: config.SchemaVersion,
		Sinks:         make([]RPCMuxDiagnosisSinkConfig, 0, len(config.Sinks)),
	}
	seen := make(map[string]struct{}, len(config.Sinks))
	for _, sinkConfig := range config.Sinks {
		sinkConfig.Name = normalizeRPCMuxOTelLogSinkName(sinkConfig.Name)
		sinkConfig.Profile = strings.TrimSpace(sinkConfig.Profile)
		sinkConfig.ProfileRef = strings.TrimSpace(sinkConfig.ProfileRef)
		sinkConfig.ProfileSchema = strings.TrimSpace(sinkConfig.ProfileSchema)
		sinkConfig.ProfileMigration = strings.TrimSpace(sinkConfig.ProfileMigration)
		if sinkConfig.Name == "" {
			return RPCMuxDiagnosisSinkSetConfig{}, errors.New("rpc mux diagnosis sink name is required")
		}
		if _, ok := seen[sinkConfig.Name]; ok {
			return RPCMuxDiagnosisSinkSetConfig{}, fmt.Errorf("rpc mux diagnosis sink %q is duplicated", sinkConfig.Name)
		}
		seen[sinkConfig.Name] = struct{}{}
		if sinkConfig.Profile != "" && sinkConfig.ProfileRef != "" {
			return RPCMuxDiagnosisSinkSetConfig{}, fmt.Errorf("rpc mux diagnosis sink %q profile and profileRef are mutually exclusive", sinkConfig.Name)
		}
		if err := validateRPCMuxDiagnosisSinkIsolationConfig(sinkConfig.Delivery.Isolation); err != nil {
			return RPCMuxDiagnosisSinkSetConfig{}, fmt.Errorf("rpc mux diagnosis sink %q delivery isolation: %w", sinkConfig.Name, err)
		}
		if sinkConfig.ProfileRef != "" {
			if config.Secrets == nil {
				return RPCMuxDiagnosisSinkSetConfig{}, fmt.Errorf("rpc mux diagnosis sink %q profileRef requires a secret resolver", sinkConfig.Name)
			}
			profile, err := config.Secrets(ctx, sinkConfig.ProfileRef)
			if err != nil {
				return RPCMuxDiagnosisSinkSetConfig{}, fmt.Errorf("rpc mux diagnosis sink %q profileRef: %w", sinkConfig.Name, err)
			}
			sinkConfig.Profile = strings.TrimSpace(profile)
		}
		if err := ValidateRPCMuxOTelLogSinkProfile(sinkConfig.Name, sinkConfig.Profile); err != nil {
			return RPCMuxDiagnosisSinkSetConfig{}, err
		}
		validated.Sinks = append(validated.Sinks, sinkConfig)
	}
	return validated, nil
}

// DiffRPCMuxDiagnosisSinkSetConfig validates a candidate generation and returns
// a redacted operator diff plan against the current active generation.
func (s *RPCMuxDiagnosisSinkSet) DiffRPCMuxDiagnosisSinkSetConfig(ctx context.Context, config RPCMuxDiagnosisSinkSetConfig) (RPCMuxDiagnosisSinkSetDiffPlan, error) {
	if s == nil {
		return RPCMuxDiagnosisSinkSetDiffPlan{}, errors.New("rpc mux diagnosis sink set is nil")
	}
	candidate, err := buildRPCMuxDiagnosisSinkGeneration(ctx, config)
	if err != nil {
		return RPCMuxDiagnosisSinkSetDiffPlan{}, err
	}
	defer candidate.close()
	s.mu.RLock()
	active := cloneRPCMuxDiagnosisSinkGenerationForDiff(s.active)
	s.mu.RUnlock()
	return diffRPCMuxDiagnosisSinkGenerations(active, candidate), nil
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
	case strings.Contains(message, "secret resolver"):
		return "sink profile secret resolver unavailable"
	case strings.Contains(message, "profileRef"):
		return "sink profile reference resolution failed"
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

func diffRPCMuxDiagnosisSinkGenerations(current, candidate *rpcMuxDiagnosisSinkGeneration) RPCMuxDiagnosisSinkSetDiffPlan {
	plan := RPCMuxDiagnosisSinkSetDiffPlan{}
	if current != nil {
		plan.FromVersion = current.version
		plan.FromSchemaVersion = current.schemaVersion
	}
	if candidate != nil {
		plan.ToVersion = candidate.version
		plan.ToSchemaVersion = candidate.schemaVersion
	}
	currentItems := map[string]rpcMuxDiagnosisSinkFingerprint{}
	if current != nil {
		currentItems = current.fingerprints
	}
	candidateItems := map[string]rpcMuxDiagnosisSinkFingerprint{}
	if candidate != nil {
		candidateItems = candidate.fingerprints
	}
	for name, next := range candidateItems {
		prev, ok := currentItems[name]
		if !ok {
			plan.Activate = append(plan.Activate, name)
			if next.profileMigration != "" {
				plan.MigrateProfile = append(plan.MigrateProfile, name)
			}
			continue
		}
		plan.Retain = append(plan.Retain, name)
		if prev.priority != next.priority {
			plan.ChangePriority = append(plan.ChangePriority, name)
		}
		if prev.profileHash != next.profileHash || prev.profileRef != next.profileRef ||
			prev.profileSchema != next.profileSchema || prev.profileMigration != next.profileMigration {
			plan.ChangeProfile = append(plan.ChangeProfile, name)
		}
		if !equalRPCMuxDiagnosisExporterDeliveryConfig(prev.delivery, next.delivery) {
			plan.ChangeDelivery = append(plan.ChangeDelivery, name)
		}
		if prev.profileMigration != next.profileMigration && next.profileMigration != "" {
			plan.MigrateProfile = append(plan.MigrateProfile, name)
		}
	}
	for name := range currentItems {
		if _, ok := candidateItems[name]; !ok {
			plan.Deactivate = append(plan.Deactivate, name)
		}
	}
	sort.Strings(plan.Activate)
	sort.Strings(plan.Deactivate)
	sort.Strings(plan.Retain)
	sort.Strings(plan.ChangePriority)
	sort.Strings(plan.ChangeProfile)
	sort.Strings(plan.ChangeDelivery)
	sort.Strings(plan.MigrateProfile)
	return plan
}

func cloneRPCMuxDiagnosisSinkGenerationForDiff(in *rpcMuxDiagnosisSinkGeneration) *rpcMuxDiagnosisSinkGeneration {
	if in == nil {
		return nil
	}
	out := &rpcMuxDiagnosisSinkGeneration{
		version:       in.version,
		schemaVersion: in.schemaVersion,
		fingerprints:  make(map[string]rpcMuxDiagnosisSinkFingerprint, len(in.fingerprints)),
	}
	for name, fingerprint := range in.fingerprints {
		out.fingerprints[name] = fingerprint
	}
	return out
}

func cloneRPCMuxDiagnosisSinkSetDiffPlan(in RPCMuxDiagnosisSinkSetDiffPlan) RPCMuxDiagnosisSinkSetDiffPlan {
	in.Activate = append([]string(nil), in.Activate...)
	in.Deactivate = append([]string(nil), in.Deactivate...)
	in.Retain = append([]string(nil), in.Retain...)
	in.ChangePriority = append([]string(nil), in.ChangePriority...)
	in.ChangeProfile = append([]string(nil), in.ChangeProfile...)
	in.ChangeDelivery = append([]string(nil), in.ChangeDelivery...)
	in.MigrateProfile = append([]string(nil), in.MigrateProfile...)
	return in
}

func hashRPCMuxDiagnosisProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return ""
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(profile))
	return fmt.Sprintf("%016x", hash.Sum64())
}

func equalRPCMuxDiagnosisExporterDeliveryConfig(left, right RPCMuxDiagnosisExporterDeliveryConfig) bool {
	return left.QueueSize == right.QueueSize &&
		left.Timeout == right.Timeout &&
		left.MaxHungCalls == right.MaxHungCalls &&
		left.BreakerFailureThreshold == right.BreakerFailureThreshold &&
		left.BreakerCooldown == right.BreakerCooldown &&
		left.ErrorBudget == right.ErrorBudget &&
		equalRPCMuxDiagnosisSinkIsolationConfig(left.Isolation, right.Isolation)
}

func equalRPCMuxDiagnosisSinkIsolationConfig(left, right RPCMuxDiagnosisSinkIsolationConfig) bool {
	if left.Mode != right.Mode ||
		left.ShutdownTimeout != right.ShutdownTimeout ||
		left.MaxMemoryBytes != right.MaxMemoryBytes ||
		left.MaxCPUPercent != right.MaxCPUPercent ||
		len(left.AuditFields) != len(right.AuditFields) {
		return false
	}
	for key, leftValue := range left.AuditFields {
		if right.AuditFields[key] != leftValue {
			return false
		}
	}
	return true
}

var (
	_ RPCMuxDiagnosisEventExporter      = (*RPCMuxDiagnosisSinkSet)(nil)
	_ RPCMuxDiagnosisSinkSetSnapshotter = (*RPCMuxDiagnosisSinkSet)(nil)
	_ io.Closer                         = (*RPCMuxDiagnosisSinkSet)(nil)
)
