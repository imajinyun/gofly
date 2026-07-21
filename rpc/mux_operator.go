package rpc

import (
	"context"
	"strings"
	"time"
)

const (
	RPCMuxDiagnosisOperatorPauseSink  = "pause_sink"
	RPCMuxDiagnosisOperatorResumeSink = "resume_sink"
	RPCMuxDiagnosisOperatorForceProbe = "force_probe"
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
		return RPCMuxDiagnosisOperatorAction{
			Sink:        approval.Sink,
			Action:      approval.Action,
			Reason:      "operator_approved",
			DryRun:      false,
			Approved:    true,
			GeneratedAt: time.Now().UTC(),
		}
	}
	return RPCMuxDiagnosisOperatorAction{}
}

var _ RPCMuxDiagnosisOperatorActionSource = (*RPCMuxDiagnosisSinkSet)(nil)
