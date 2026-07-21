package rpc

import (
	"context"
	"testing"
)

func TestRPCMuxDiagnosisOperatorActionForSink(t *testing.T) {
	tests := []struct {
		name           string
		operatorAction string
		closed         bool
		wantAction     string
		wantReason     string
	}{
		{name: "hung calls", operatorAction: "pause_sink_hung_calls", wantAction: RPCMuxDiagnosisOperatorPauseSink, wantReason: "hung_call_limit"},
		{name: "error budget", operatorAction: "pause_sink_error_budget", wantAction: RPCMuxDiagnosisOperatorPauseSink, wantReason: "error_budget_burn"},
		{name: "breaker open", operatorAction: "pause_sink_breaker", wantAction: RPCMuxDiagnosisOperatorPauseSink, wantReason: "breaker_open"},
		{name: "half open", operatorAction: "probe_sink_recovery", wantAction: RPCMuxDiagnosisOperatorForceProbe, wantReason: "breaker_half_open"},
		{name: "degraded", operatorAction: "degrade_sink", wantAction: RPCMuxDiagnosisOperatorForceProbe, wantReason: "consecutive_failures"},
		{name: "closed", closed: true, wantAction: RPCMuxDiagnosisOperatorResumeSink, wantReason: "closed"},
		{name: "none"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := rpcMuxDiagnosisOperatorActionForSink(RPCMuxDiagnosisSinkRuntimeSnapshot{
				Name: "sink-a",
				Delivery: RPCMuxDiagnosisExporterDeliverySnapshot{
					OperatorAction: test.operatorAction,
					BreakerState:   "open",
					Closed:         test.closed,
					Isolation: RPCMuxDiagnosisSinkIsolationConfig{
						Mode: RPCMuxDiagnosisSinkIsolationIsolatedProcess,
					},
				},
			})
			if action.Action != test.wantAction || action.Reason != test.wantReason {
				t.Fatalf("action = %+v, want action=%q reason=%q", action, test.wantAction, test.wantReason)
			}
			if test.wantAction != "" && action.Details["isolation_mode"] != RPCMuxDiagnosisSinkIsolationIsolatedProcess {
				t.Fatalf("action details = %+v, want isolation mode", action.Details)
			}
		})
	}
}

func TestRPCMuxDiagnosisSinkSetOperatorActionBoundaries(t *testing.T) {
	if actions := (*RPCMuxDiagnosisSinkSet)(nil).RPCMuxDiagnosisOperatorActions(context.Background()); len(actions) != 0 {
		t.Fatalf("nil sink set actions = %+v, want none", actions)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{Version: "actions-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	if actions := sinkSet.RPCMuxDiagnosisOperatorActions(canceled); len(actions) != 0 {
		t.Fatalf("canceled actions = %+v, want none", actions)
	}
	if err := sinkSet.Close(); err != nil {
		t.Fatal(err)
	}
	if actions := sinkSet.RPCMuxDiagnosisOperatorActions(context.Background()); len(actions) != 0 {
		t.Fatalf("closed actions = %+v, want none", actions)
	}
}
