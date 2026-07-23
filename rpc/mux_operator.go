package rpc

import (
	"context"
	"encoding/json"
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

	defaultRPCMuxDiagnosisOperatorHistoryLimit = 32
	defaultRPCMuxDiagnosisOperatorStoreMaxSize = 1 << 20
	defaultRPCMuxDiagnosisOperatorStoreMaxLine = 64 << 10
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

type RPCMuxDiagnosisOperatorStoreSnapshot struct {
	Enabled        bool      `json:"enabled"`
	Kind           string    `json:"kind,omitempty"`
	Path           string    `json:"path,omitempty"`
	MaxSizeBytes   int64     `json:"maxSizeBytes,omitempty"`
	MaxLineBytes   int64     `json:"maxLineBytes,omitempty"`
	StoredActions  int       `json:"storedActions,omitempty"`
	BadLines       int       `json:"badLines,omitempty"`
	Checksum       string    `json:"checksum,omitempty"`
	ChecksumStatus string    `json:"checksumStatus,omitempty"`
	LastError      string    `json:"lastError,omitempty"`
	LastWriteAt    time.Time `json:"lastWriteAt,omitempty"`
	LastLoadAt     time.Time `json:"lastLoadAt,omitempty"`
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
	mu           sync.Mutex
	snapshot     RPCMuxDiagnosisOperatorStoreSnapshot
}

func NewRPCMuxDiagnosisOperatorHistoryFileStore(path string) (*RPCMuxDiagnosisOperatorHistoryFileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsLocal(path) {
		return nil, fmt.Errorf("operator history file path must be local")
	}
	return &RPCMuxDiagnosisOperatorHistoryFileStore{
		path:         path,
		maxSizeBytes: defaultRPCMuxDiagnosisOperatorStoreMaxSize,
		maxLineBytes: defaultRPCMuxDiagnosisOperatorStoreMaxLine,
		snapshot: RPCMuxDiagnosisOperatorStoreSnapshot{
			Enabled:        true,
			Kind:           "file",
			Path:           path,
			MaxSizeBytes:   defaultRPCMuxDiagnosisOperatorStoreMaxSize,
			MaxLineBytes:   defaultRPCMuxDiagnosisOperatorStoreMaxLine,
			ChecksumStatus: "unchecked",
		},
	}, nil
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
	data, err := json.Marshal(cloneRPCMuxDiagnosisOperatorAction(action))
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		s.recordErrorLocked(err)
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		s.recordErrorLocked(err)
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		s.recordErrorLocked(err)
		return err
	}
	s.snapshot.LastError = ""
	s.snapshot.StoredActions++
	s.snapshot.ChecksumStatus = "unchecked"
	s.snapshot.LastWriteAt = time.Now().UTC()
	s.refreshFileSnapshotLocked(nil)
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
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	actions := make([]RPCMuxDiagnosisOperatorAction, 0, len(lines))
	badLines := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if int64(len(line)) > s.maxLineBytes {
			badLines++
			continue
		}
		var action RPCMuxDiagnosisOperatorAction
		if err := json.Unmarshal([]byte(line), &action); err != nil {
			badLines++
			continue
		}
		actions = append(actions, cloneRPCMuxDiagnosisOperatorAction(action))
	}
	checksum := checksumRPCMuxDiagnosisOperatorActions(actions)
	s.snapshot.StoredActions = len(actions)
	s.snapshot.BadLines = badLines
	s.snapshot.Checksum = checksum
	s.snapshot.ChecksumStatus = checksumStatusForOperatorHistory(len(actions), badLines)
	s.snapshot.LastError = ""
	s.snapshot.LastLoadAt = time.Now().UTC()
	if limit > 0 && limit < len(actions) {
		actions = actions[len(actions)-limit:]
	}
	return actions, nil
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
	s.snapshot.MaxSizeBytes = s.maxSizeBytes
	s.snapshot.MaxLineBytes = s.maxLineBytes
	if existingErr != nil {
		return
	}
	if info, err := os.Stat(s.path); err == nil && info.Size() > s.maxSizeBytes {
		s.snapshot.ChecksumStatus = "size_limit_exceeded"
	}
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

var _ RPCMuxDiagnosisOperatorActionSource = (*RPCMuxDiagnosisSinkSet)(nil)
var _ RPCMuxDiagnosisOperatorHistoryStore = (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil)
var _ RPCMuxDiagnosisOperatorHistoryStoreSnapshotter = (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil)
