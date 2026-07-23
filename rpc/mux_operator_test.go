package rpc

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
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
	if action := (*RPCMuxDiagnosisSinkSet)(nil).ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink: "missing", Action: RPCMuxDiagnosisOperatorPauseSink, Token: "approved",
	}); action.Action != "" {
		t.Fatalf("nil sink set apply action = %+v, want empty", action)
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
	if action := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(canceled, RPCMuxDiagnosisOperatorApproval{
		Sink: "missing", Action: RPCMuxDiagnosisOperatorPauseSink, Token: "approved",
	}); action.Action != "" {
		t.Fatalf("canceled apply action = %+v, want empty", action)
	}
	if action := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{}); action.Action != "" {
		t.Fatalf("empty apply action = %+v, want empty", action)
	}
	if action := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink: "missing", Action: RPCMuxDiagnosisOperatorPauseSink, Token: "approved",
	}); action.Action != "" {
		t.Fatalf("missing sink action = %+v, want empty", action)
	}
	if err := sinkSet.Close(); err != nil {
		t.Fatal(err)
	}
	if actions := sinkSet.RPCMuxDiagnosisOperatorActions(context.Background()); len(actions) != 0 {
		t.Fatalf("closed actions = %+v, want none", actions)
	}
	if action := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink: "missing", Action: RPCMuxDiagnosisOperatorPauseSink, Token: "approved",
	}); action.Action != "" {
		t.Fatalf("closed apply action = %+v, want empty", action)
	}
}

func TestRPCMuxDiagnosisSinkSetApplyOperatorAction(t *testing.T) {
	var exports atomic.Int64
	cleanup := RegisterRPCMuxOTelLogSink("operator-apply", func(string) RPCMuxOTelLogExporter {
		return RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {
			exports.Add(1)
		})
	})
	defer cleanup()
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "apply-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "operator-apply"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	dryRun := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink:   "operator-apply",
		Action: RPCMuxDiagnosisOperatorPauseSink,
	})
	if !dryRun.DryRun || dryRun.Approved || dryRun.Reason != "approval_required" {
		t.Fatalf("dry-run action = %+v", dryRun)
	}
	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	waitForOperatorExports(t, &exports, 1)
	if exports.Load() != 1 {
		t.Fatalf("exports after dry-run = %d, want 1", exports.Load())
	}

	approved := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink:   "operator-apply",
		Action: RPCMuxDiagnosisOperatorPauseSink,
		Token:  "approved",
	})
	if !approved.Approved || approved.DryRun || approved.Action != RPCMuxDiagnosisOperatorPauseSink {
		t.Fatalf("approved pause action = %+v", approved)
	}
	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	time.Sleep(10 * time.Millisecond)
	if exports.Load() != 1 {
		t.Fatalf("exports after pause = %d, want unchanged", exports.Load())
	}
	snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if !snapshot.Sinks[0].Delivery.OperatorPaused {
		t.Fatalf("snapshot after pause = %+v, want operator paused", snapshot)
	}

	resumed := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink:   "operator-apply",
		Action: RPCMuxDiagnosisOperatorResumeSink,
		Token:  "approved",
	})
	if !resumed.Approved || resumed.Action != RPCMuxDiagnosisOperatorResumeSink {
		t.Fatalf("resume action = %+v", resumed)
	}
	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	waitForOperatorExports(t, &exports, 2)
	if exports.Load() != 2 {
		t.Fatalf("exports after resume = %d, want 2", exports.Load())
	}

	governed := sinkSet.active.sinks[0].exporter.(*governedRPCMuxDiagnosisExporter)
	governed.breakerOpenedAt.Store(time.Now().UnixNano())
	probed := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink:   "operator-apply",
		Action: RPCMuxDiagnosisOperatorForceProbe,
		Token:  "approved",
	})
	if !probed.Approved || probed.Action != RPCMuxDiagnosisOperatorForceProbe || governed.breakerOpenedAt.Load() != 0 {
		t.Fatalf("force probe action = %+v breaker=%d", probed, governed.breakerOpenedAt.Load())
	}
	if invalid := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{Sink: "operator-apply", Action: "bad", Token: "approved"}); invalid.Action != "" {
		t.Fatalf("invalid action = %+v, want empty", invalid)
	}
	history := sinkSet.RPCMuxDiagnosisOperatorActionHistory(2)
	if len(history) != 2 || history[0].Action != RPCMuxDiagnosisOperatorResumeSink || history[1].Action != RPCMuxDiagnosisOperatorForceProbe {
		t.Fatalf("operator history = %+v, want latest resume and force-probe", history)
	}
	historySnapshot := sinkSet.RPCMuxDiagnosisOperatorHistorySnapshot(2)
	if len(historySnapshot.Actions) != 2 || historySnapshot.Checksum == "" {
		t.Fatalf("operator history snapshot = %+v", historySnapshot)
	}
	history[0].Details = map[string]string{"mutated": "true"}
	if got := sinkSet.RPCMuxDiagnosisOperatorActionHistory(1); len(got) != 1 || got[0].Details["mutated"] == "true" {
		t.Fatalf("operator history was not defensively copied: %+v", got)
	}
	sinkSet.recordOperatorActionLocked(RPCMuxDiagnosisOperatorAction{Action: RPCMuxDiagnosisOperatorPauseSink})
	for index := 0; index < defaultRPCMuxDiagnosisOperatorHistoryLimit+2; index++ {
		sinkSet.recordOperatorActionLocked(RPCMuxDiagnosisOperatorAction{
			Sink:     "operator-apply",
			Action:   RPCMuxDiagnosisOperatorPauseSink,
			Approved: true,
			Details:  map[string]string{"index": "x"},
		})
	}
	if got := sinkSet.RPCMuxDiagnosisOperatorActionHistory(0); len(got) != defaultRPCMuxDiagnosisOperatorHistoryLimit {
		t.Fatalf("operator history limit = %d, want %d", len(got), defaultRPCMuxDiagnosisOperatorHistoryLimit)
	}
}

func TestRPCMuxDiagnosisOperatorHistoryFileStore(t *testing.T) {
	store, err := NewRPCMuxDiagnosisOperatorHistoryFileStore(filepath.Join("history", "operator.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	absStore, err := NewRPCMuxDiagnosisOperatorHistoryFileStore("/tmp/operator.jsonl")
	if err == nil || absStore != nil {
		t.Fatalf("absolute operator history file store = %#v err=%v, want error", absStore, err)
	}
	dir := t.TempDir()
	store.path = filepath.Join(dir, store.path)
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "stored-history-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "slog"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	sinkSet.WithOperatorHistoryStore(store)
	approved := sinkSet.ApplyRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorApproval{
		Sink:   "slog",
		Action: RPCMuxDiagnosisOperatorPauseSink,
		Token:  "approved",
	})
	if !approved.Approved {
		t.Fatalf("approved action = %+v", approved)
	}
	actions := sinkSet.StoredRPCMuxDiagnosisOperatorActionHistory(context.Background(), 1)
	if len(actions) != 1 || actions[0].Action != RPCMuxDiagnosisOperatorPauseSink {
		t.Fatalf("stored actions = %+v", actions)
	}
	storeSnapshot := sinkSet.OperatorHistoryStoreSnapshot()
	if !storeSnapshot.Enabled || storeSnapshot.Kind != "file" || storeSnapshot.StoredActions != 1 {
		t.Fatalf("store snapshot = %+v", storeSnapshot)
	}
	loaded, err := store.LoadRPCMuxDiagnosisOperatorActions(context.Background(), 1)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("loaded actions = %+v err=%v", loaded, err)
	}
	if err := os.WriteFile(store.path, []byte("{bad\n"+`{"sink":"slog","action":"resume_sink","approved":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.LoadRPCMuxDiagnosisOperatorActions(context.Background(), 5)
	if err != nil || len(loaded) != 1 || loaded[0].Action != RPCMuxDiagnosisOperatorResumeSink {
		t.Fatalf("loaded actions with bad lines = %+v err=%v", loaded, err)
	}
	if snapshot := store.RPCMuxDiagnosisOperatorStoreSnapshot(); snapshot.BadLines != 1 || snapshot.ChecksumStatus != "partial_bad_lines" {
		t.Fatalf("bad line store snapshot = %+v", snapshot)
	}
	historySnapshot := sinkSet.RPCMuxDiagnosisOperatorHistorySnapshot(5)
	if !historySnapshot.Diagnostics.ChecksumMismatch || historySnapshot.Diagnostics.StoreChecksumStatus != "partial_bad_lines" {
		t.Fatalf("history diagnostics = %+v", historySnapshot.Diagnostics)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.AppendRPCMuxDiagnosisOperatorAction(canceled, approved); err == nil {
		t.Fatal("canceled append succeeded")
	}
	if _, err := store.LoadRPCMuxDiagnosisOperatorActions(canceled, 1); err == nil {
		t.Fatal("canceled load succeeded")
	}
}

func TestRPCMuxDiagnosisOperatorHistoryStoreDiagnostics(t *testing.T) {
	if snapshot := (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil).RPCMuxDiagnosisOperatorStoreSnapshot(); snapshot.Enabled {
		t.Fatalf("nil file store snapshot = %+v", snapshot)
	}
	if status := checksumStatusForOperatorHistory(0, 1); status != "bad_lines" {
		t.Fatalf("bad-only checksum status = %q", status)
	}
	if status := checksumStatusForOperatorHistory(0, 0); status != "empty" {
		t.Fatalf("empty checksum status = %q", status)
	}
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{Version: "store-diagnostics-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	if snapshot := sinkSet.OperatorHistoryStoreSnapshot(); snapshot.Enabled {
		t.Fatalf("empty store snapshot = %+v", snapshot)
	}
	customStore := fakeOperatorHistoryStore{}
	sinkSet.WithOperatorHistoryStore(customStore)
	if snapshot := sinkSet.OperatorHistoryStoreSnapshot(); !snapshot.Enabled || snapshot.Kind != "custom" {
		t.Fatalf("custom store snapshot = %+v", snapshot)
	}

	store, err := NewRPCMuxDiagnosisOperatorHistoryFileStore(filepath.Join("history", "operator.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(t.TempDir(), store.path)
	if loaded, err := store.LoadRPCMuxDiagnosisOperatorActions(context.Background(), 5); err != nil || len(loaded) != 0 {
		t.Fatalf("missing file load = %+v err=%v", loaded, err)
	}
	if snapshot := store.RPCMuxDiagnosisOperatorStoreSnapshot(); snapshot.ChecksumStatus != "missing" {
		t.Fatalf("missing file snapshot = %+v", snapshot)
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, []byte(`{"sink":"slog","action":"pause_sink","approved":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.maxSizeBytes = 1
	if _, err := store.LoadRPCMuxDiagnosisOperatorActions(context.Background(), 5); err == nil {
		t.Fatal("oversized operator history loaded")
	}
	if snapshot := store.RPCMuxDiagnosisOperatorStoreSnapshot(); snapshot.ChecksumStatus != "size_limit_exceeded" || snapshot.LastError == "" {
		t.Fatalf("oversized file snapshot = %+v", snapshot)
	}
}

type fakeOperatorHistoryStore struct{}

func (fakeOperatorHistoryStore) AppendRPCMuxDiagnosisOperatorAction(context.Context, RPCMuxDiagnosisOperatorAction) error {
	return nil
}

func (fakeOperatorHistoryStore) LoadRPCMuxDiagnosisOperatorActions(context.Context, int) ([]RPCMuxDiagnosisOperatorAction, error) {
	return nil, nil
}

func waitForOperatorExports(t *testing.T, exports *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if exports.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("exports = %d, want at least %d", exports.Load(), want)
}
