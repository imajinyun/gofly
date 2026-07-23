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
	Actions  []RPCMuxDiagnosisOperatorAction `json:"actions,omitempty"`
	Checksum string                          `json:"checksum,omitempty"`
}

type RPCMuxDiagnosisOperatorHistoryStore interface {
	AppendRPCMuxDiagnosisOperatorAction(context.Context, RPCMuxDiagnosisOperatorAction) error
	LoadRPCMuxDiagnosisOperatorActions(context.Context, int) ([]RPCMuxDiagnosisOperatorAction, error)
}

type RPCMuxDiagnosisOperatorHistoryFileStore struct {
	path string
	mu   sync.Mutex
}

func NewRPCMuxDiagnosisOperatorHistoryFileStore(path string) (*RPCMuxDiagnosisOperatorHistoryFileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsLocal(path) {
		return nil, fmt.Errorf("operator history file path must be local")
	}
	return &RPCMuxDiagnosisOperatorHistoryFileStore{path: path}, nil
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
	return RPCMuxDiagnosisOperatorHistorySnapshot{
		Actions:  actions,
		Checksum: checksumRPCMuxDiagnosisOperatorActions(actions),
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
	return s.operatorStore.AppendRPCMuxDiagnosisOperatorAction(ctx, cloneRPCMuxDiagnosisOperatorAction(action))
}

func (s *RPCMuxDiagnosisSinkSet) StoredRPCMuxDiagnosisOperatorActionHistory(ctx context.Context, limit int) []RPCMuxDiagnosisOperatorAction {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	store := s.operatorStore
	s.mu.RUnlock()
	if store == nil {
		return nil
	}
	actions, err := store.LoadRPCMuxDiagnosisOperatorActions(ctx, limit)
	if err != nil {
		return nil
	}
	return actions
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
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
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
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	actions := make([]RPCMuxDiagnosisOperatorAction, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var action RPCMuxDiagnosisOperatorAction
		if err := json.Unmarshal([]byte(line), &action); err != nil {
			return nil, err
		}
		actions = append(actions, cloneRPCMuxDiagnosisOperatorAction(action))
	}
	if limit > 0 && limit < len(actions) {
		actions = actions[len(actions)-limit:]
	}
	return actions, nil
}

var _ RPCMuxDiagnosisOperatorActionSource = (*RPCMuxDiagnosisSinkSet)(nil)
var _ RPCMuxDiagnosisOperatorHistoryStore = (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil)
