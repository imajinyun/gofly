package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	RPCMuxDiagnosisOperatorPauseSink  = "pause_sink"
	RPCMuxDiagnosisOperatorResumeSink = "resume_sink"
	RPCMuxDiagnosisOperatorForceProbe = "force_probe"

	// RPCMuxDiagnosisOperatorHistorySourcePrimary selects the active JSONL file.
	RPCMuxDiagnosisOperatorHistorySourcePrimary = "primary"
	// RPCMuxDiagnosisOperatorHistorySourceBackup selects the rotated .bak file.
	RPCMuxDiagnosisOperatorHistorySourceBackup = "backup"

	defaultRPCMuxDiagnosisOperatorHistoryLimit    = 32
	defaultRPCMuxDiagnosisOperatorStoreMaxSize    = 1 << 20
	defaultRPCMuxDiagnosisOperatorStoreMaxLine    = 64 << 10
	defaultRPCMuxDiagnosisOperatorStoreMaxActions = 128

	maxRPCMuxDiagnosisOperatorStoreSize    = 64 << 20
	maxRPCMuxDiagnosisOperatorStoreLine    = 1 << 20
	maxRPCMuxDiagnosisOperatorStoreActions = 64 << 10
	maxRPCMuxDiagnosisOperatorBackupFiles  = 3

	rpcMuxDiagnosisOperatorHistoryFileHeaderType    = "gofly.rpc_mux_operator_history.header"
	rpcMuxDiagnosisOperatorHistoryFileSchemaVersion = "gofly.rpc_mux_operator_history.v1"
)

// RPCMuxDiagnosisOperatorAction describes a dry-run operator action for one
// sink. It is intentionally declarative; callers must apply any real action in
// their own control plane after audit and approval.
type RPCMuxDiagnosisOperatorAction struct {
	Sink        string            `json:"sink"`
	Action      string            `json:"action"`
	Reason      string            `json:"reason"`
	DryRun      bool              `json:"dryRun"`
	Approved    bool              `json:"approved,omitempty"`
	Health      string            `json:"health,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
	GeneratedAt time.Time         `json:"generatedAt"`
}

// RPCMuxDiagnosisOperatorApproval confirms an operator action. Empty tokens
// keep the action in dry-run mode.
type RPCMuxDiagnosisOperatorApproval struct {
	Sink   string `json:"sink"`
	Action string `json:"action"`
	Token  string `json:"token,omitempty"`
}

type RPCMuxDiagnosisOperatorHistorySnapshot struct {
	Actions       []RPCMuxDiagnosisOperatorAction         `json:"actions,omitempty"`
	Checksum      string                                  `json:"checksum,omitempty"`
	StoreSnapshot RPCMuxDiagnosisOperatorStoreSnapshot    `json:"store,omitempty"`
	Diagnostics   RPCMuxDiagnosisOperatorHistoryDiagState `json:"diagnostics,omitempty"`
}

type RPCMuxDiagnosisOperatorHistoryStore interface {
	AppendRPCMuxDiagnosisOperatorAction(context.Context, RPCMuxDiagnosisOperatorAction) error
	LoadRPCMuxDiagnosisOperatorActions(context.Context, int) ([]RPCMuxDiagnosisOperatorAction, error)
}

type RPCMuxDiagnosisOperatorHistoryStoreSnapshotter interface {
	RPCMuxDiagnosisOperatorStoreSnapshot() RPCMuxDiagnosisOperatorStoreSnapshot
}

// RPCMuxDiagnosisOperatorHistoryStoreVerifier exposes redacted, read-only
// integrity evidence for a history store.
type RPCMuxDiagnosisOperatorHistoryStoreVerifier interface {
	VerifyRPCMuxDiagnosisOperatorHistory(context.Context) (RPCMuxDiagnosisOperatorHistoryVerification, error)
}

// RPCMuxDiagnosisOperatorHistoryFileStoreConfig bounds file reads and
// compaction. Zero values select the conservative defaults.
type RPCMuxDiagnosisOperatorHistoryFileStoreConfig struct {
	MaxSizeBytes int64 `json:"maxSizeBytes,omitempty"`
	MaxLineBytes int64 `json:"maxLineBytes,omitempty"`
	MaxActions   int   `json:"maxActions,omitempty"`
	MaxBackups   int   `json:"maxBackups,omitempty"`
}

type RPCMuxDiagnosisOperatorStoreSnapshot struct {
	Enabled         bool      `json:"enabled"`
	Kind            string    `json:"kind,omitempty"`
	Path            string    `json:"path,omitempty"`
	SchemaVersion   string    `json:"schemaVersion,omitempty"`
	MaxSizeBytes    int64     `json:"maxSizeBytes,omitempty"`
	MaxLineBytes    int64     `json:"maxLineBytes,omitempty"`
	MaxActions      int       `json:"maxActions,omitempty"`
	StoredActions   int       `json:"storedActions,omitempty"`
	BadLines        int       `json:"badLines,omitempty"`
	TamperedLines   int       `json:"tamperedLines,omitempty"`
	TruncatedLines  int       `json:"truncatedLines,omitempty"`
	LegacyLines     int       `json:"legacyLines,omitempty"`
	HeaderPresent   bool      `json:"headerPresent,omitempty"`
	Checksum        string    `json:"checksum,omitempty"`
	ChecksumStatus  string    `json:"checksumStatus,omitempty"`
	IntegrityStatus string    `json:"integrityStatus,omitempty"`
	Compactions     int       `json:"compactions,omitempty"`
	Rotations       int       `json:"rotations,omitempty"`
	BackupRetention int       `json:"backupRetention,omitempty"`
	LastError       string    `json:"lastError,omitempty"`
	LastWriteAt     time.Time `json:"lastWriteAt,omitempty"`
	LastLoadAt      time.Time `json:"lastLoadAt,omitempty"`
	LastCompactedAt time.Time `json:"lastCompactedAt,omitempty"`
}

// RPCMuxDiagnosisOperatorHistoryFileEvidence summarizes one history file
// without exposing its operator actions or raw contents.
type RPCMuxDiagnosisOperatorHistoryFileEvidence struct {
	Source          string `json:"source"`
	Path            string `json:"path,omitempty"`
	Exists          bool   `json:"exists"`
	SizeBytes       int64  `json:"sizeBytes,omitempty"`
	StoredActions   int    `json:"storedActions,omitempty"`
	BadLines        int    `json:"badLines,omitempty"`
	TamperedLines   int    `json:"tamperedLines,omitempty"`
	TruncatedLines  int    `json:"truncatedLines,omitempty"`
	LegacyLines     int    `json:"legacyLines,omitempty"`
	HeaderPresent   bool   `json:"headerPresent,omitempty"`
	Checksum        string `json:"checksum,omitempty"`
	ChecksumStatus  string `json:"checksumStatus,omitempty"`
	IntegrityStatus string `json:"integrityStatus"`
	LastError       string `json:"lastError,omitempty"`
}

// RPCMuxDiagnosisOperatorHistoryReplay contains validated actions from one
// explicitly selected history file and its integrity evidence.
type RPCMuxDiagnosisOperatorHistoryReplay struct {
	Source   string                                     `json:"source"`
	Actions  []RPCMuxDiagnosisOperatorAction            `json:"actions,omitempty"`
	Evidence RPCMuxDiagnosisOperatorHistoryFileEvidence `json:"evidence"`
}

// RPCMuxDiagnosisOperatorHistoryVerification contains redacted evidence for
// the primary and backup files.
type RPCMuxDiagnosisOperatorHistoryVerification struct {
	Primary RPCMuxDiagnosisOperatorHistoryFileEvidence   `json:"primary"`
	Backup  RPCMuxDiagnosisOperatorHistoryFileEvidence   `json:"backup"`
	Backups []RPCMuxDiagnosisOperatorHistoryFileEvidence `json:"backups,omitempty"`
}

// RPCMuxDiagnosisOperatorHistoryIntegritySnapshot is safe for control-plane
// export because it contains store and integrity summaries, never actions.
type RPCMuxDiagnosisOperatorHistoryIntegritySnapshot struct {
	Store           RPCMuxDiagnosisOperatorStoreSnapshot       `json:"store"`
	IntegrityStatus string                                     `json:"integrityStatus"`
	DegradedReason  string                                     `json:"degradedReason,omitempty"`
	DegradedSource  string                                     `json:"degradedSource,omitempty"`
	Verification    RPCMuxDiagnosisOperatorHistoryVerification `json:"verification,omitempty"`
}

type RPCMuxDiagnosisOperatorHistoryDiagState struct {
	StoreEnabled        bool   `json:"storeEnabled,omitempty"`
	StoreKind           string `json:"storeKind,omitempty"`
	StoreChecksumStatus string `json:"storeChecksumStatus,omitempty"`
	ChecksumMismatch    bool   `json:"checksumMismatch,omitempty"`
	LastError           string `json:"lastError,omitempty"`
}

type RPCMuxDiagnosisOperatorHistoryFileStore struct {
	path         string
	maxSizeBytes int64
	maxLineBytes int64
	maxActions   int
	maxBackups   int
	mu           sync.Mutex
	snapshot     RPCMuxDiagnosisOperatorStoreSnapshot
}

type rpcMuxDiagnosisOperatorHistoryFileHeader struct {
	Type          string    `json:"type"`
	SchemaVersion string    `json:"schemaVersion"`
	CreatedAt     time.Time `json:"createdAt"`
}

type rpcMuxDiagnosisOperatorHistoryFileEntry struct {
	SchemaVersion string                        `json:"schemaVersion"`
	Checksum      string                        `json:"checksum"`
	Action        RPCMuxDiagnosisOperatorAction `json:"action"`
}

func NewRPCMuxDiagnosisOperatorHistoryFileStore(path string) (*RPCMuxDiagnosisOperatorHistoryFileStore, error) {
	return NewRPCMuxDiagnosisOperatorHistoryFileStoreWithConfig(path, RPCMuxDiagnosisOperatorHistoryFileStoreConfig{})
}

// NewRPCMuxDiagnosisOperatorHistoryFileStoreWithConfig constructs a bounded
// local file store. Zero limits retain the conservative defaults.
func NewRPCMuxDiagnosisOperatorHistoryFileStoreWithConfig(path string, config RPCMuxDiagnosisOperatorHistoryFileStoreConfig) (*RPCMuxDiagnosisOperatorHistoryFileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsLocal(path) {
		return nil, fmt.Errorf("operator history file path must be local")
	}
	config, err := normalizeRPCMuxDiagnosisOperatorHistoryFileStoreConfig(config)
	if err != nil {
		return nil, err
	}
	return &RPCMuxDiagnosisOperatorHistoryFileStore{
		path:         path,
		maxSizeBytes: config.MaxSizeBytes,
		maxLineBytes: config.MaxLineBytes,
		maxActions:   config.MaxActions,
		maxBackups:   config.MaxBackups,
		snapshot: RPCMuxDiagnosisOperatorStoreSnapshot{
			Enabled:         true,
			Kind:            "file",
			Path:            path,
			SchemaVersion:   rpcMuxDiagnosisOperatorHistoryFileSchemaVersion,
			MaxSizeBytes:    config.MaxSizeBytes,
			MaxLineBytes:    config.MaxLineBytes,
			MaxActions:      config.MaxActions,
			BackupRetention: config.MaxBackups,
			ChecksumStatus:  "unchecked",
			IntegrityStatus: "unchecked",
		},
	}, nil
}

func normalizeRPCMuxDiagnosisOperatorHistoryFileStoreConfig(config RPCMuxDiagnosisOperatorHistoryFileStoreConfig) (RPCMuxDiagnosisOperatorHistoryFileStoreConfig, error) {
	if config.MaxSizeBytes < 0 || config.MaxSizeBytes > maxRPCMuxDiagnosisOperatorStoreSize {
		return RPCMuxDiagnosisOperatorHistoryFileStoreConfig{}, fmt.Errorf("operator history maxSizeBytes must be between 0 and %d", maxRPCMuxDiagnosisOperatorStoreSize)
	}
	if config.MaxLineBytes < 0 || config.MaxLineBytes > maxRPCMuxDiagnosisOperatorStoreLine {
		return RPCMuxDiagnosisOperatorHistoryFileStoreConfig{}, fmt.Errorf("operator history maxLineBytes must be between 0 and %d", maxRPCMuxDiagnosisOperatorStoreLine)
	}
	if config.MaxActions < 0 || config.MaxActions > maxRPCMuxDiagnosisOperatorStoreActions {
		return RPCMuxDiagnosisOperatorHistoryFileStoreConfig{}, fmt.Errorf("operator history maxActions must be between 0 and %d", maxRPCMuxDiagnosisOperatorStoreActions)
	}
	if config.MaxBackups < 0 || config.MaxBackups > maxRPCMuxDiagnosisOperatorBackupFiles {
		return RPCMuxDiagnosisOperatorHistoryFileStoreConfig{}, fmt.Errorf("operator history maxBackups must be between 0 and %d", maxRPCMuxDiagnosisOperatorBackupFiles)
	}
	if config.MaxSizeBytes == 0 {
		config.MaxSizeBytes = defaultRPCMuxDiagnosisOperatorStoreMaxSize
	}
	if config.MaxLineBytes == 0 {
		config.MaxLineBytes = defaultRPCMuxDiagnosisOperatorStoreMaxLine
	}
	if config.MaxActions == 0 {
		config.MaxActions = defaultRPCMuxDiagnosisOperatorStoreMaxActions
	}
	if config.MaxBackups == 0 {
		config.MaxBackups = maxRPCMuxDiagnosisOperatorBackupFiles
	}
	if config.MaxLineBytes > config.MaxSizeBytes {
		return RPCMuxDiagnosisOperatorHistoryFileStoreConfig{}, fmt.Errorf("operator history maxLineBytes must not exceed maxSizeBytes")
	}
	return config, nil
}

// RPCMuxDiagnosisOperatorActionSource exposes dry-run operator actions.
type RPCMuxDiagnosisOperatorActionSource interface {
	RPCMuxDiagnosisOperatorActions(context.Context) []RPCMuxDiagnosisOperatorAction
}

func (s *RPCMuxDiagnosisSinkSet) RPCMuxDiagnosisOperatorActions(ctx context.Context) []RPCMuxDiagnosisOperatorAction {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || ctx.Err() != nil {
		return nil
	}
	snapshot := s.RPCMuxDiagnosisSinkSetSnapshot()
	if snapshot.Closed {
		return nil
	}
	actions := make([]RPCMuxDiagnosisOperatorAction, 0, len(snapshot.Sinks))
	for _, sink := range snapshot.Sinks {
		if action := rpcMuxDiagnosisOperatorActionForSink(sink); action.Action != "" {
			action.GeneratedAt = time.Now().UTC()
			action.DryRun = true
			actions = append(actions, action)
		}
	}
	return actions
}

func rpcMuxDiagnosisOperatorActionForSink(sink RPCMuxDiagnosisSinkRuntimeSnapshot) RPCMuxDiagnosisOperatorAction {
	delivery := sink.Delivery
	action := RPCMuxDiagnosisOperatorAction{
		Sink:   sink.Name,
		Health: delivery.Health,
		Details: map[string]string{
			"operator_action": delivery.OperatorAction,
			"breaker_state":   delivery.BreakerState,
			"isolation_mode":  delivery.Isolation.Mode,
		},
	}
	switch strings.TrimSpace(delivery.OperatorAction) {
	case "pause_sink_hung_calls":
		action.Action = RPCMuxDiagnosisOperatorPauseSink
		action.Reason = "hung_call_limit"
	case "pause_sink_error_budget":
		action.Action = RPCMuxDiagnosisOperatorPauseSink
		action.Reason = "error_budget_burn"
	case "pause_sink_breaker":
		action.Action = RPCMuxDiagnosisOperatorPauseSink
		action.Reason = "breaker_open"
	case "probe_sink_recovery":
		action.Action = RPCMuxDiagnosisOperatorForceProbe
		action.Reason = "breaker_half_open"
	case "degrade_sink":
		action.Action = RPCMuxDiagnosisOperatorForceProbe
		action.Reason = "consecutive_failures"
	default:
		if delivery.Closed {
			action.Action = RPCMuxDiagnosisOperatorResumeSink
			action.Reason = "closed"
		}
	}
	if action.Action == "" {
		return RPCMuxDiagnosisOperatorAction{}
	}
	return action
}

// ApplyRPCMuxDiagnosisOperatorAction applies an explicitly approved sink action.
// Without a token it returns the same action as a dry-run plan.
func (s *RPCMuxDiagnosisSinkSet) ApplyRPCMuxDiagnosisOperatorAction(ctx context.Context, approval RPCMuxDiagnosisOperatorApproval) RPCMuxDiagnosisOperatorAction {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || ctx.Err() != nil {
		return RPCMuxDiagnosisOperatorAction{}
	}
	approval.Sink = strings.TrimSpace(approval.Sink)
	approval.Action = strings.TrimSpace(approval.Action)
	if approval.Sink == "" || approval.Action == "" {
		return RPCMuxDiagnosisOperatorAction{}
	}
	if strings.TrimSpace(approval.Token) == "" {
		return RPCMuxDiagnosisOperatorAction{
			Sink:        approval.Sink,
			Action:      approval.Action,
			Reason:      "approval_required",
			DryRun:      true,
			GeneratedAt: time.Now().UTC(),
		}
	}
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.active == nil {
		return RPCMuxDiagnosisOperatorAction{}
	}
	for index := range s.active.sinks {
		if s.active.sinks[index].name != approval.Sink {
			continue
		}
		switch approval.Action {
		case RPCMuxDiagnosisOperatorPauseSink:
			s.active.sinks[index].paused = true
			s.active.sinks[index].pauseReason = "operator_approved"
		case RPCMuxDiagnosisOperatorResumeSink:
			s.active.sinks[index].paused = false
			s.active.sinks[index].pauseReason = ""
		case RPCMuxDiagnosisOperatorForceProbe:
			s.active.sinks[index].paused = false
			s.active.sinks[index].pauseReason = ""
			if governed, ok := s.active.sinks[index].exporter.(*governedRPCMuxDiagnosisExporter); ok {
				governed.forceProbe()
			}
		default:
			return RPCMuxDiagnosisOperatorAction{}
		}
		action := RPCMuxDiagnosisOperatorAction{
			Sink:        approval.Sink,
			Action:      approval.Action,
			Reason:      "operator_approved",
			DryRun:      false,
			Approved:    true,
			GeneratedAt: time.Now().UTC(),
		}
		s.recordOperatorActionLocked(action)
		_ = s.appendOperatorHistory(ctx, action)
		return action
	}
	return RPCMuxDiagnosisOperatorAction{}
}

func (s *RPCMuxDiagnosisSinkSet) RPCMuxDiagnosisOperatorActionHistory(limit int) []RPCMuxDiagnosisOperatorAction {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.operatorHistory) {
		limit = len(s.operatorHistory)
	}
	start := len(s.operatorHistory) - limit
	out := make([]RPCMuxDiagnosisOperatorAction, 0, limit)
	for _, action := range s.operatorHistory[start:] {
		out = append(out, cloneRPCMuxDiagnosisOperatorAction(action))
	}
	return out
}

func (s *RPCMuxDiagnosisSinkSet) RPCMuxDiagnosisOperatorHistorySnapshot(limit int) RPCMuxDiagnosisOperatorHistorySnapshot {
	actions := s.RPCMuxDiagnosisOperatorActionHistory(limit)
	checksum := checksumRPCMuxDiagnosisOperatorActions(actions)
	storeActions, storeLoadErr := s.storedRPCMuxDiagnosisOperatorActionHistoryWithError(context.Background(), limit)
	storeSnapshot := s.operatorHistoryStoreSnapshot()
	if storeLoadErr != nil {
		storeSnapshot.LastError = storeLoadErr.Error()
	}
	storeChecksum := checksumRPCMuxDiagnosisOperatorActions(storeActions)
	checksumMismatch := storeSnapshot.Enabled && checksum != "" && storeChecksum != "" && checksum != storeChecksum
	return RPCMuxDiagnosisOperatorHistorySnapshot{
		Actions:       actions,
		Checksum:      checksum,
		StoreSnapshot: storeSnapshot,
		Diagnostics: RPCMuxDiagnosisOperatorHistoryDiagState{
			StoreEnabled:        storeSnapshot.Enabled,
			StoreKind:           storeSnapshot.Kind,
			StoreChecksumStatus: storeSnapshot.ChecksumStatus,
			ChecksumMismatch:    checksumMismatch,
			LastError:           storeSnapshot.LastError,
		},
	}
}

func (s *RPCMuxDiagnosisSinkSet) recordOperatorActionLocked(action RPCMuxDiagnosisOperatorAction) {
	if s == nil || !action.Approved {
		return
	}
	s.operatorHistory = append(s.operatorHistory, cloneRPCMuxDiagnosisOperatorAction(action))
	if len(s.operatorHistory) > defaultRPCMuxDiagnosisOperatorHistoryLimit {
		copy(s.operatorHistory, s.operatorHistory[len(s.operatorHistory)-defaultRPCMuxDiagnosisOperatorHistoryLimit:])
		s.operatorHistory = s.operatorHistory[:defaultRPCMuxDiagnosisOperatorHistoryLimit]
	}
}

func (s *RPCMuxDiagnosisSinkSet) WithOperatorHistoryStore(store RPCMuxDiagnosisOperatorHistoryStore) *RPCMuxDiagnosisSinkSet {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operatorStore = store
	return s
}

func (s *RPCMuxDiagnosisSinkSet) appendOperatorHistory(ctx context.Context, action RPCMuxDiagnosisOperatorAction) error {
	if s == nil || s.operatorStore == nil || !action.Approved {
		return nil
	}
	err := s.operatorStore.AppendRPCMuxDiagnosisOperatorAction(ctx, cloneRPCMuxDiagnosisOperatorAction(action))
	return err
}

func (s *RPCMuxDiagnosisSinkSet) StoredRPCMuxDiagnosisOperatorActionHistory(ctx context.Context, limit int) []RPCMuxDiagnosisOperatorAction {
	actions, _ := s.storedRPCMuxDiagnosisOperatorActionHistoryWithError(ctx, limit)
	return actions
}

func (s *RPCMuxDiagnosisSinkSet) storedRPCMuxDiagnosisOperatorActionHistoryWithError(ctx context.Context, limit int) ([]RPCMuxDiagnosisOperatorAction, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	store := s.operatorStore
	s.mu.RUnlock()
	if store == nil {
		return nil, nil
	}
	actions, err := store.LoadRPCMuxDiagnosisOperatorActions(ctx, limit)
	if err != nil {
		return nil, err
	}
	return actions, nil
}

func (s *RPCMuxDiagnosisSinkSet) OperatorHistoryStoreSnapshot() RPCMuxDiagnosisOperatorStoreSnapshot {
	if s == nil {
		return RPCMuxDiagnosisOperatorStoreSnapshot{}
	}
	return s.operatorHistoryStoreSnapshot()
}

// RPCMuxDiagnosisOperatorHistoryIntegritySnapshot returns redacted, read-only
// integrity evidence for the configured store.
func (s *RPCMuxDiagnosisSinkSet) RPCMuxDiagnosisOperatorHistoryIntegritySnapshot(ctx context.Context) (RPCMuxDiagnosisOperatorHistoryIntegritySnapshot, error) {
	if s == nil {
		return RPCMuxDiagnosisOperatorHistoryIntegritySnapshot{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.RLock()
	store := s.operatorStore
	s.mu.RUnlock()
	snapshot := RPCMuxDiagnosisOperatorHistoryIntegritySnapshot{
		Store:           s.operatorHistoryStoreSnapshot(),
		IntegrityStatus: "disabled",
	}
	if store == nil {
		return snapshot, nil
	}
	verifier, ok := store.(RPCMuxDiagnosisOperatorHistoryStoreVerifier)
	if !ok {
		snapshot.IntegrityStatus = "unsupported"
		return snapshot, nil
	}
	verification, err := verifier.VerifyRPCMuxDiagnosisOperatorHistory(ctx)
	snapshot.Verification = verification
	snapshot.IntegrityStatus = verification.Primary.IntegrityStatus
	snapshot.DegradedReason, snapshot.DegradedSource = rpcMuxDiagnosisOperatorHistoryDegradedEvidence(verification)
	return snapshot, err
}

func rpcMuxDiagnosisOperatorHistoryDegradedEvidence(verification RPCMuxDiagnosisOperatorHistoryVerification) (string, string) {
	if rpcMuxDiagnosisOperatorHistoryIntegrityDegraded(verification.Primary.IntegrityStatus) {
		return verification.Primary.IntegrityStatus, verification.Primary.Source
	}
	if rpcMuxDiagnosisOperatorHistoryIntegrityDegraded(verification.Backup.IntegrityStatus) {
		return verification.Backup.IntegrityStatus, verification.Backup.Source
	}
	for _, evidence := range verification.Backups {
		if rpcMuxDiagnosisOperatorHistoryIntegrityDegraded(evidence.IntegrityStatus) {
			return evidence.IntegrityStatus, evidence.Source
		}
	}
	return "", ""
}

func (s *RPCMuxDiagnosisSinkSet) ReplayRPCMuxDiagnosisOperatorHistory(ctx context.Context, source string, limit int) (RPCMuxDiagnosisOperatorHistoryReplay, error) {
	if s == nil {
		return RPCMuxDiagnosisOperatorHistoryReplay{}, nil
	}
	s.mu.RLock()
	store := s.operatorStore
	s.mu.RUnlock()
	replayer, ok := store.(interface {
		ReplayRPCMuxDiagnosisOperatorHistory(context.Context, string, int) (RPCMuxDiagnosisOperatorHistoryReplay, error)
	})
	if !ok {
		return RPCMuxDiagnosisOperatorHistoryReplay{}, nil
	}
	return replayer.ReplayRPCMuxDiagnosisOperatorHistory(ctx, source, limit)
}

func (s *RPCMuxDiagnosisSinkSet) operatorHistoryStoreSnapshot() RPCMuxDiagnosisOperatorStoreSnapshot {
	if s == nil {
		return RPCMuxDiagnosisOperatorStoreSnapshot{}
	}
	s.mu.RLock()
	store := s.operatorStore
	s.mu.RUnlock()
	if store == nil {
		return RPCMuxDiagnosisOperatorStoreSnapshot{}
	}
	if snapshotter, ok := store.(RPCMuxDiagnosisOperatorHistoryStoreSnapshotter); ok {
		return snapshotter.RPCMuxDiagnosisOperatorStoreSnapshot()
	}
	return RPCMuxDiagnosisOperatorStoreSnapshot{Enabled: true, Kind: "custom"}
}

func cloneRPCMuxDiagnosisOperatorAction(action RPCMuxDiagnosisOperatorAction) RPCMuxDiagnosisOperatorAction {
	if action.Details != nil {
		action.Details = cloneStringMap(action.Details)
	}
	return action
}

func checksumRPCMuxDiagnosisOperatorActions(actions []RPCMuxDiagnosisOperatorAction) string {
	if len(actions) == 0 {
		return ""
	}
	data, err := json.Marshal(actions)
	if err != nil {
		return ""
	}
	hash := fnv.New64a()
	_, _ = hash.Write(data)
	return fmt.Sprintf("%016x", hash.Sum64())
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) AppendRPCMuxDiagnosisOperatorAction(ctx context.Context, action RPCMuxDiagnosisOperatorAction) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, err := marshalRPCMuxDiagnosisOperatorHistoryEntry(action)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		s.recordErrorLocked(err)
		return err
	}
	if err := s.ensureHistoryHeaderLocked(); err != nil {
		s.recordErrorLocked(err)
		return err
	}
	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		s.recordErrorLocked(err)
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(entry, '\n')); err != nil {
		s.recordErrorLocked(err)
		return err
	}
	s.snapshot.LastError = ""
	s.snapshot.StoredActions++
	s.snapshot.ChecksumStatus = "unchecked"
	s.snapshot.IntegrityStatus = "unchecked"
	s.snapshot.LastWriteAt = time.Now().UTC()
	s.refreshFileSnapshotLocked(nil)
	if err := s.compactHistoryLocked(false); err != nil {
		s.recordErrorLocked(err)
		return err
	}
	return nil
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) LoadRPCMuxDiagnosisOperatorActions(ctx context.Context, limit int) ([]RPCMuxDiagnosisOperatorAction, error) {
	if s == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Stat(s.path)
	if os.IsNotExist(err) {
		s.snapshot.LastError = ""
		s.snapshot.StoredActions = 0
		s.snapshot.BadLines = 0
		s.snapshot.Checksum = ""
		s.snapshot.ChecksumStatus = "missing"
		s.snapshot.LastLoadAt = time.Now().UTC()
		return nil, nil
	}
	if err != nil {
		s.recordErrorLocked(err)
		return nil, err
	}
	if info.Size() > s.maxSizeBytes {
		err := fmt.Errorf("operator history file exceeds max size")
		s.recordErrorLocked(err)
		return nil, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		s.recordErrorLocked(err)
		return nil, err
	}
	actions, diagnostics := s.parseHistoryLinesLocked(data)
	checksum := checksumRPCMuxDiagnosisOperatorActions(actions)
	s.snapshot.StoredActions = len(actions)
	s.snapshot.BadLines = diagnostics.badLines
	s.snapshot.TamperedLines = diagnostics.tamperedLines
	s.snapshot.TruncatedLines = diagnostics.truncatedLines
	s.snapshot.LegacyLines = diagnostics.legacyLines
	s.snapshot.HeaderPresent = diagnostics.headerPresent
	s.snapshot.Checksum = checksum
	s.snapshot.ChecksumStatus = checksumStatusForOperatorHistory(len(actions), diagnostics.badLines)
	s.snapshot.IntegrityStatus = integrityStatusForOperatorHistory(diagnostics)
	s.snapshot.LastError = ""
	s.snapshot.LastLoadAt = time.Now().UTC()
	if limit > 0 && limit < len(actions) {
		actions = actions[len(actions)-limit:]
	}
	return actions, nil
}

// ReplayRPCMuxDiagnosisOperatorHistory reads and validates one history file
// without updating the store snapshot or changing either history file.
func (s *RPCMuxDiagnosisOperatorHistoryFileStore) ReplayRPCMuxDiagnosisOperatorHistory(ctx context.Context, source string, limit int) (RPCMuxDiagnosisOperatorHistoryReplay, error) {
	if s == nil {
		return RPCMuxDiagnosisOperatorHistoryReplay{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RPCMuxDiagnosisOperatorHistoryReplay{}, err
	}
	source = strings.ToLower(strings.TrimSpace(source))
	path, err := s.historySourcePath(source)
	if err != nil {
		return RPCMuxDiagnosisOperatorHistoryReplay{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	actions, evidence, err := s.replayHistoryPathLocked(path, source)
	if limit > 0 && limit < len(actions) {
		actions = actions[len(actions)-limit:]
	}
	return RPCMuxDiagnosisOperatorHistoryReplay{
		Source:   source,
		Actions:  actions,
		Evidence: evidence,
	}, err
}

// VerifyRPCMuxDiagnosisOperatorHistory validates the primary and backup files
// without returning actions.
func (s *RPCMuxDiagnosisOperatorHistoryFileStore) VerifyRPCMuxDiagnosisOperatorHistory(ctx context.Context) (RPCMuxDiagnosisOperatorHistoryVerification, error) {
	if s == nil {
		return RPCMuxDiagnosisOperatorHistoryVerification{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RPCMuxDiagnosisOperatorHistoryVerification{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, primary, primaryErr := s.replayHistoryPathLocked(s.path, RPCMuxDiagnosisOperatorHistorySourcePrimary)
	_, backup, backupErr := s.replayHistoryPathLocked(s.path+".bak", RPCMuxDiagnosisOperatorHistorySourceBackup)
	backups := make([]RPCMuxDiagnosisOperatorHistoryFileEvidence, 0, maxRPCMuxDiagnosisOperatorBackupFiles)
	for index := 1; index <= s.maxBackups; index++ {
		source := fmt.Sprintf("%s.%d", RPCMuxDiagnosisOperatorHistorySourceBackup, index)
		_, evidence, err := s.replayHistoryPathLocked(fmt.Sprintf("%s.bak.%d", s.path, index), source)
		backups = append(backups, evidence)
		backupErr = errors.Join(backupErr, err)
	}
	return RPCMuxDiagnosisOperatorHistoryVerification{
		Primary: primary,
		Backup:  backup,
		Backups: backups,
	}, errors.Join(primaryErr, backupErr)
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) historySourcePath(source string) (string, error) {
	switch source {
	case RPCMuxDiagnosisOperatorHistorySourcePrimary:
		return s.path, nil
	case RPCMuxDiagnosisOperatorHistorySourceBackup:
		return s.path + ".bak", nil
	default:
		if strings.HasPrefix(source, RPCMuxDiagnosisOperatorHistorySourceBackup+".") {
			suffix := strings.TrimPrefix(source, RPCMuxDiagnosisOperatorHistorySourceBackup+".")
			switch suffix {
			case "1", "2", "3":
				return s.path + ".bak." + suffix, nil
			}
		}
		return "", fmt.Errorf("operator history source must be primary or backup")
	}
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) replayHistoryPathLocked(path string, source string) ([]RPCMuxDiagnosisOperatorAction, RPCMuxDiagnosisOperatorHistoryFileEvidence, error) {
	evidence := RPCMuxDiagnosisOperatorHistoryFileEvidence{
		Source:          source,
		Path:            path,
		ChecksumStatus:  "missing",
		IntegrityStatus: "missing",
	}
	// #nosec G304 G703 -- path is the constructor-validated local store path or a bounded backup sibling.
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, evidence, nil
	}
	if err != nil {
		evidence.LastError = err.Error()
		evidence.IntegrityStatus = "unreadable"
		return nil, evidence, err
	}
	evidence.Exists = true
	evidence.SizeBytes = info.Size()
	if info.Size() > s.maxSizeBytes {
		err := fmt.Errorf("operator history file exceeds max size")
		evidence.ChecksumStatus = "size_limit_exceeded"
		evidence.IntegrityStatus = "size_limit_exceeded"
		evidence.LastError = err.Error()
		return nil, evidence, err
	}
	// #nosec G304 G703 -- path is the constructor-validated local store path or a bounded backup sibling.
	data, err := os.ReadFile(path)
	if err != nil {
		evidence.LastError = err.Error()
		evidence.IntegrityStatus = "unreadable"
		return nil, evidence, err
	}
	actions, diagnostics := s.parseHistoryLinesLocked(data)
	evidence.StoredActions = len(actions)
	evidence.BadLines = diagnostics.badLines
	evidence.TamperedLines = diagnostics.tamperedLines
	evidence.TruncatedLines = diagnostics.truncatedLines
	evidence.LegacyLines = diagnostics.legacyLines
	evidence.HeaderPresent = diagnostics.headerPresent
	evidence.Checksum = checksumRPCMuxDiagnosisOperatorActions(actions)
	evidence.ChecksumStatus = checksumStatusForOperatorHistory(len(actions), diagnostics.badLines)
	evidence.IntegrityStatus = integrityStatusForOperatorHistory(diagnostics)
	return actions, evidence, nil
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) RPCMuxDiagnosisOperatorStoreSnapshot() RPCMuxDiagnosisOperatorStoreSnapshot {
	if s == nil {
		return RPCMuxDiagnosisOperatorStoreSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshFileSnapshotLocked(nil)
	return s.snapshot
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) recordErrorLocked(err error) {
	if err == nil {
		return
	}
	s.snapshot.LastError = err.Error()
	s.refreshFileSnapshotLocked(err)
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) refreshFileSnapshotLocked(existingErr error) {
	s.snapshot.Enabled = true
	s.snapshot.Kind = "file"
	s.snapshot.Path = s.path
	s.snapshot.SchemaVersion = rpcMuxDiagnosisOperatorHistoryFileSchemaVersion
	s.snapshot.MaxSizeBytes = s.maxSizeBytes
	s.snapshot.MaxLineBytes = s.maxLineBytes
	s.snapshot.MaxActions = s.maxActions
	s.snapshot.BackupRetention = s.maxBackups
	if existingErr != nil {
		return
	}
	if info, err := os.Stat(s.path); err == nil && info.Size() > s.maxSizeBytes {
		s.snapshot.ChecksumStatus = "size_limit_exceeded"
	}
}

type rpcMuxDiagnosisOperatorHistoryDiagnostics struct {
	badLines       int
	tamperedLines  int
	truncatedLines int
	legacyLines    int
	headerPresent  bool
}

func marshalRPCMuxDiagnosisOperatorHistoryEntry(action RPCMuxDiagnosisOperatorAction) ([]byte, error) {
	action = cloneRPCMuxDiagnosisOperatorAction(action)
	checksum, err := checksumRPCMuxDiagnosisOperatorAction(action)
	if err != nil {
		return nil, err
	}
	return json.Marshal(rpcMuxDiagnosisOperatorHistoryFileEntry{
		SchemaVersion: rpcMuxDiagnosisOperatorHistoryFileSchemaVersion,
		Checksum:      checksum,
		Action:        action,
	})
}

func checksumRPCMuxDiagnosisOperatorAction(action RPCMuxDiagnosisOperatorAction) (string, error) {
	data, err := json.Marshal(cloneRPCMuxDiagnosisOperatorAction(action))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) ensureHistoryHeaderLocked() error {
	if _, err := os.Stat(s.path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	header, err := json.Marshal(rpcMuxDiagnosisOperatorHistoryFileHeader{
		Type:          rpcMuxDiagnosisOperatorHistoryFileHeaderType,
		SchemaVersion: rpcMuxDiagnosisOperatorHistoryFileSchemaVersion,
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(header, '\n'), 0o600)
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) parseHistoryLinesLocked(data []byte) ([]RPCMuxDiagnosisOperatorAction, rpcMuxDiagnosisOperatorHistoryDiagnostics) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	actions := make([]RPCMuxDiagnosisOperatorAction, 0, len(lines))
	var diagnostics rpcMuxDiagnosisOperatorHistoryDiagnostics
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if int64(len(line)) > s.maxLineBytes {
			diagnostics.badLines++
			diagnostics.truncatedLines++
			continue
		}
		action, lineDiagnostics, ok := parseRPCMuxDiagnosisOperatorHistoryLine(line)
		diagnostics.badLines += lineDiagnostics.badLines
		diagnostics.tamperedLines += lineDiagnostics.tamperedLines
		diagnostics.legacyLines += lineDiagnostics.legacyLines
		if lineDiagnostics.headerPresent {
			diagnostics.headerPresent = true
		}
		if ok {
			actions = append(actions, action)
		}
	}
	return actions, diagnostics
}

func parseRPCMuxDiagnosisOperatorHistoryLine(line string) (RPCMuxDiagnosisOperatorAction, rpcMuxDiagnosisOperatorHistoryDiagnostics, bool) {
	var header rpcMuxDiagnosisOperatorHistoryFileHeader
	if err := json.Unmarshal([]byte(line), &header); err == nil && header.Type == rpcMuxDiagnosisOperatorHistoryFileHeaderType {
		if header.SchemaVersion != rpcMuxDiagnosisOperatorHistoryFileSchemaVersion {
			return RPCMuxDiagnosisOperatorAction{}, rpcMuxDiagnosisOperatorHistoryDiagnostics{badLines: 1, headerPresent: true}, false
		}
		return RPCMuxDiagnosisOperatorAction{}, rpcMuxDiagnosisOperatorHistoryDiagnostics{headerPresent: true}, false
	}
	var entry rpcMuxDiagnosisOperatorHistoryFileEntry
	if err := json.Unmarshal([]byte(line), &entry); err == nil && entry.SchemaVersion != "" {
		if entry.SchemaVersion != rpcMuxDiagnosisOperatorHistoryFileSchemaVersion || entry.Checksum == "" {
			return RPCMuxDiagnosisOperatorAction{}, rpcMuxDiagnosisOperatorHistoryDiagnostics{badLines: 1}, false
		}
		checksum, err := checksumRPCMuxDiagnosisOperatorAction(entry.Action)
		if err != nil || checksum != entry.Checksum {
			return RPCMuxDiagnosisOperatorAction{}, rpcMuxDiagnosisOperatorHistoryDiagnostics{badLines: 1, tamperedLines: 1}, false
		}
		return cloneRPCMuxDiagnosisOperatorAction(entry.Action), rpcMuxDiagnosisOperatorHistoryDiagnostics{}, true
	}
	var action RPCMuxDiagnosisOperatorAction
	if err := json.Unmarshal([]byte(line), &action); err != nil {
		return RPCMuxDiagnosisOperatorAction{}, rpcMuxDiagnosisOperatorHistoryDiagnostics{badLines: 1}, false
	}
	return cloneRPCMuxDiagnosisOperatorAction(action), rpcMuxDiagnosisOperatorHistoryDiagnostics{legacyLines: 1}, true
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) compactHistoryLocked(force bool) error {
	info, err := os.Stat(s.path)
	if err != nil {
		return err
	}
	shouldCompact := force || (s.maxActions > 0 && s.snapshot.StoredActions > s.maxActions)
	if !shouldCompact && s.maxSizeBytes > 0 && info.Size() > s.maxSizeBytes/2 {
		shouldCompact = true
	}
	if !shouldCompact {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	actions, _ := s.parseHistoryLinesLocked(data)
	if s.maxActions > 0 && len(actions) > s.maxActions {
		actions = actions[len(actions)-s.maxActions:]
	}
	compact, err := marshalRPCMuxDiagnosisOperatorHistoryFile(actions)
	if err != nil {
		return err
	}
	if err := s.rotateBackupsLocked(); err != nil {
		return err
	}
	// #nosec G306 G703 -- s.path is constrained to filepath.IsLocal by NewRPCMuxDiagnosisOperatorHistoryFileStore.
	if err := os.WriteFile(s.path, compact, 0o600); err != nil {
		_ = os.Rename(s.path+".bak", s.path)
		return err
	}
	s.snapshot.Compactions++
	s.snapshot.Rotations++
	s.snapshot.StoredActions = len(actions)
	s.snapshot.BadLines = 0
	s.snapshot.TamperedLines = 0
	s.snapshot.TruncatedLines = 0
	s.snapshot.LegacyLines = 0
	s.snapshot.HeaderPresent = true
	s.snapshot.Checksum = checksumRPCMuxDiagnosisOperatorActions(actions)
	s.snapshot.ChecksumStatus = checksumStatusForOperatorHistory(len(actions), 0)
	s.snapshot.IntegrityStatus = integrityStatusForOperatorHistory(rpcMuxDiagnosisOperatorHistoryDiagnostics{headerPresent: true})
	s.snapshot.LastCompactedAt = time.Now().UTC()
	return nil
}

func (s *RPCMuxDiagnosisOperatorHistoryFileStore) rotateBackupsLocked() error {
	for index := s.maxBackups; index >= 1; index-- {
		current := s.path + ".bak"
		if index > 1 {
			current = fmt.Sprintf("%s.bak.%d", s.path, index-1)
		}
		next := fmt.Sprintf("%s.bak.%d", s.path, index)
		if index == s.maxBackups {
			_ = os.Remove(next)
		}
		if err := os.Rename(current, next); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(s.path, s.path+".bak"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func marshalRPCMuxDiagnosisOperatorHistoryFile(actions []RPCMuxDiagnosisOperatorAction) ([]byte, error) {
	header, err := json.Marshal(rpcMuxDiagnosisOperatorHistoryFileHeader{
		Type:          rpcMuxDiagnosisOperatorHistoryFileHeaderType,
		SchemaVersion: rpcMuxDiagnosisOperatorHistoryFileSchemaVersion,
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	lines := [][]byte{header}
	for _, action := range actions {
		entry, err := marshalRPCMuxDiagnosisOperatorHistoryEntry(action)
		if err != nil {
			return nil, err
		}
		lines = append(lines, entry)
	}
	return append([]byte(strings.Join(bytesLinesToStrings(lines), "\n")), '\n'), nil
}

func bytesLinesToStrings(lines [][]byte) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, string(line))
	}
	return out
}

func checksumStatusForOperatorHistory(actions int, badLines int) string {
	switch {
	case badLines > 0 && actions > 0:
		return "partial_bad_lines"
	case badLines > 0:
		return "bad_lines"
	case actions > 0:
		return "ok"
	default:
		return "empty"
	}
}

func integrityStatusForOperatorHistory(diagnostics rpcMuxDiagnosisOperatorHistoryDiagnostics) string {
	switch {
	case diagnostics.tamperedLines > 0:
		return "tampered"
	case diagnostics.truncatedLines > 0:
		return "truncated"
	case diagnostics.badLines > 0:
		return "bad_lines"
	case diagnostics.legacyLines > 0 && diagnostics.headerPresent:
		return "mixed_legacy"
	case diagnostics.legacyLines > 0:
		return "legacy"
	case diagnostics.headerPresent:
		return "ok"
	default:
		return "missing_header"
	}
}

var _ RPCMuxDiagnosisOperatorActionSource = (*RPCMuxDiagnosisSinkSet)(nil)
var _ RPCMuxDiagnosisOperatorHistoryStore = (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil)
var _ RPCMuxDiagnosisOperatorHistoryStoreSnapshotter = (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil)
var _ RPCMuxDiagnosisOperatorHistoryStoreVerifier = (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil)
