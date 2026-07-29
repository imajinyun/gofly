package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/observability/metrics"
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
	if snapshot := store.RPCMuxDiagnosisOperatorStoreSnapshot(); !snapshot.HeaderPresent || snapshot.IntegrityStatus != "ok" {
		t.Fatalf("integrity envelope snapshot = %+v", snapshot)
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

func TestRPCMuxDiagnosisOperatorHistoryFileStoreIntegrityAndCompaction(t *testing.T) {
	store, err := NewRPCMuxDiagnosisOperatorHistoryFileStore(filepath.Join("history", "operator.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(t.TempDir(), store.path)
	store.maxActions = 2
	for index, actionName := range []string{RPCMuxDiagnosisOperatorPauseSink, RPCMuxDiagnosisOperatorResumeSink, RPCMuxDiagnosisOperatorForceProbe} {
		if err := store.AppendRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorAction{
			Sink:     "slog",
			Action:   actionName,
			Approved: true,
			Details:  map[string]string{"index": string(rune('0' + index))},
		}); err != nil {
			t.Fatalf("append action %d: %v", index, err)
		}
	}
	loaded, err := store.LoadRPCMuxDiagnosisOperatorActions(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0].Action != RPCMuxDiagnosisOperatorResumeSink || loaded[1].Action != RPCMuxDiagnosisOperatorForceProbe {
		t.Fatalf("compacted actions = %+v", loaded)
	}
	if _, err := os.Stat(store.path + ".bak"); err != nil {
		t.Fatalf("compaction backup stat: %v", err)
	}
	for _, actionName := range []string{RPCMuxDiagnosisOperatorPauseSink, RPCMuxDiagnosisOperatorResumeSink} {
		if err := store.AppendRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorAction{
			Sink:     "slog",
			Action:   actionName,
			Approved: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(store.path + ".bak.1"); err != nil {
		t.Fatalf("backup ring stat: %v", err)
	}
	ringReplay, err := store.ReplayRPCMuxDiagnosisOperatorHistory(context.Background(), "backup.1", 1)
	if err != nil || !ringReplay.Evidence.Exists || len(ringReplay.Actions) != 1 {
		t.Fatalf("backup ring replay = %+v err=%v", ringReplay, err)
	}
	snapshot := store.RPCMuxDiagnosisOperatorStoreSnapshot()
	if snapshot.Compactions == 0 || snapshot.Rotations == 0 || snapshot.IntegrityStatus != "ok" || snapshot.StoredActions != 2 {
		t.Fatalf("compaction snapshot = %+v", snapshot)
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), RPCMuxDiagnosisOperatorResumeSink, RPCMuxDiagnosisOperatorForceProbe, 1)
	if err := os.WriteFile(store.path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.LoadRPCMuxDiagnosisOperatorActions(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Action != RPCMuxDiagnosisOperatorPauseSink {
		t.Fatalf("loaded after tamper = %+v", loaded)
	}
	if snapshot := store.RPCMuxDiagnosisOperatorStoreSnapshot(); snapshot.TamperedLines != 1 || snapshot.IntegrityStatus != "tampered" {
		t.Fatalf("tampered snapshot = %+v", snapshot)
	}
}

func TestRPCMuxDiagnosisOperatorHistoryFileStoreReplayAndVerify(t *testing.T) {
	if replay, err := (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil).ReplayRPCMuxDiagnosisOperatorHistory(context.Background(), RPCMuxDiagnosisOperatorHistorySourcePrimary, 0); err != nil || replay.Source != "" {
		t.Fatalf("nil store replay = %+v err=%v", replay, err)
	}
	if verification, err := (*RPCMuxDiagnosisOperatorHistoryFileStore)(nil).VerifyRPCMuxDiagnosisOperatorHistory(context.Background()); err != nil ||
		verification.Primary.Source != "" {
		t.Fatalf("nil store verification = %+v err=%v", verification, err)
	}
	if integrity, err := (*RPCMuxDiagnosisSinkSet)(nil).RPCMuxDiagnosisOperatorHistoryIntegritySnapshot(context.Background()); err != nil ||
		integrity.IntegrityStatus != "" {
		t.Fatalf("nil sink set integrity = %+v err=%v", integrity, err)
	}
	store, err := NewRPCMuxDiagnosisOperatorHistoryFileStoreWithConfig(
		filepath.Join("history", "operator.jsonl"),
		RPCMuxDiagnosisOperatorHistoryFileStoreConfig{
			MaxSizeBytes: 32 << 10,
			MaxLineBytes: 8 << 10,
			MaxActions:   2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(t.TempDir(), store.path)
	for _, actionName := range []string{
		RPCMuxDiagnosisOperatorPauseSink,
		RPCMuxDiagnosisOperatorResumeSink,
		RPCMuxDiagnosisOperatorForceProbe,
	} {
		if err := store.AppendRPCMuxDiagnosisOperatorAction(context.Background(), RPCMuxDiagnosisOperatorAction{
			Sink:     "slog",
			Action:   actionName,
			Approved: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	primaryBefore, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	backupBefore, err := os.ReadFile(store.path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	primaryInfoBefore, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	backupInfoBefore, err := os.Stat(store.path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	snapshotBefore := store.RPCMuxDiagnosisOperatorStoreSnapshot()

	replay, err := store.ReplayRPCMuxDiagnosisOperatorHistory(context.Background(), RPCMuxDiagnosisOperatorHistorySourceBackup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Actions) != 1 || replay.Actions[0].Action != RPCMuxDiagnosisOperatorForceProbe ||
		!replay.Evidence.Exists || replay.Evidence.StoredActions != 3 || replay.Evidence.IntegrityStatus != "ok" {
		t.Fatalf("backup replay = %+v", replay)
	}
	verification, err := store.VerifyRPCMuxDiagnosisOperatorHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if verification.Primary.StoredActions != 2 || verification.Primary.IntegrityStatus != "ok" ||
		verification.Backup.StoredActions != 3 || verification.Backup.IntegrityStatus != "ok" {
		t.Fatalf("history verification = %+v", verification)
	}
	primaryReplay, err := store.ReplayRPCMuxDiagnosisOperatorHistory(context.Background(), RPCMuxDiagnosisOperatorHistorySourcePrimary, 0)
	if err != nil || len(primaryReplay.Actions) != 2 || primaryReplay.Evidence.IntegrityStatus != "ok" {
		t.Fatalf("primary replay = %+v err=%v", primaryReplay, err)
	}
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{Version: "integrity-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	sinkSet.WithOperatorHistoryStore(store)
	integrity, err := sinkSet.RPCMuxDiagnosisOperatorHistoryIntegritySnapshot(context.Background())
	if err != nil || integrity.IntegrityStatus != "ok" || len(integrity.Verification.Primary.Checksum) == 0 {
		t.Fatalf("sink set integrity = %+v err=%v", integrity, err)
	}
	if snapshotAfter := store.RPCMuxDiagnosisOperatorStoreSnapshot(); snapshotAfter.LastLoadAt != snapshotBefore.LastLoadAt {
		t.Fatalf("read-only verification updated snapshot: before=%+v after=%+v", snapshotBefore, snapshotAfter)
	}
	assertOperatorHistoryFileUnchanged(t, store.path, primaryBefore, primaryInfoBefore)
	assertOperatorHistoryFileUnchanged(t, store.path+".bak", backupBefore, backupInfoBefore)

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), RPCMuxDiagnosisOperatorForceProbe, RPCMuxDiagnosisOperatorPauseSink, 1)
	if err := os.WriteFile(store.path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	verification, err = store.VerifyRPCMuxDiagnosisOperatorHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if verification.Primary.IntegrityStatus != "tampered" || verification.Primary.TamperedLines != 1 ||
		verification.Backup.IntegrityStatus != "ok" {
		t.Fatalf("tampered verification = %+v", verification)
	}

	if _, err := store.ReplayRPCMuxDiagnosisOperatorHistory(context.Background(), "unknown", 0); err == nil {
		t.Fatal("unknown replay source succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.VerifyRPCMuxDiagnosisOperatorHistory(canceled); err == nil {
		t.Fatal("canceled history verification succeeded")
	}

	unreadable, err := NewRPCMuxDiagnosisOperatorHistoryFileStore("operator.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	unreadable.path = t.TempDir()
	replay, err = unreadable.ReplayRPCMuxDiagnosisOperatorHistory(context.Background(), RPCMuxDiagnosisOperatorHistorySourcePrimary, 0)
	if err == nil || replay.Evidence.IntegrityStatus != "unreadable" || replay.Evidence.LastError == "" {
		t.Fatalf("unreadable history replay = %+v err=%v", replay, err)
	}

	badHeader, err := NewRPCMuxDiagnosisOperatorHistoryFileStore("operator.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	badHeader.path = filepath.Join(t.TempDir(), "operator.jsonl")
	if err := os.WriteFile(badHeader.path, []byte(`{"type":"gofly.rpc_mux_operator_history.header","schemaVersion":"unsupported"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replay, err = badHeader.ReplayRPCMuxDiagnosisOperatorHistory(context.Background(), RPCMuxDiagnosisOperatorHistorySourcePrimary, 0)
	if err != nil || replay.Evidence.IntegrityStatus != "bad_lines" || !replay.Evidence.HeaderPresent {
		t.Fatalf("bad header replay = %+v err=%v", replay, err)
	}
}

func TestRPCMuxDiagnosisOperatorHistoryFileStoreConfig(t *testing.T) {
	defaultStore, err := NewRPCMuxDiagnosisOperatorHistoryFileStore("operator.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := defaultStore.RPCMuxDiagnosisOperatorStoreSnapshot(); snapshot.MaxActions != defaultRPCMuxDiagnosisOperatorStoreMaxActions ||
		snapshot.MaxSizeBytes != defaultRPCMuxDiagnosisOperatorStoreMaxSize ||
		snapshot.MaxLineBytes != defaultRPCMuxDiagnosisOperatorStoreMaxLine {
		t.Fatalf("default file store config = %+v", snapshot)
	}
	configured, err := NewRPCMuxDiagnosisOperatorHistoryFileStoreWithConfig("operator.jsonl", RPCMuxDiagnosisOperatorHistoryFileStoreConfig{
		MaxSizeBytes: 128 << 10,
		MaxLineBytes: 16 << 10,
		MaxActions:   7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := configured.RPCMuxDiagnosisOperatorStoreSnapshot(); snapshot.MaxActions != 7 ||
		snapshot.MaxSizeBytes != 128<<10 || snapshot.MaxLineBytes != 16<<10 {
		t.Fatalf("configured file store = %+v", snapshot)
	}
	for _, config := range []RPCMuxDiagnosisOperatorHistoryFileStoreConfig{
		{MaxSizeBytes: -1},
		{MaxLineBytes: -1},
		{MaxActions: -1},
		{MaxSizeBytes: 1024, MaxLineBytes: 2048},
		{MaxSizeBytes: maxRPCMuxDiagnosisOperatorStoreSize + 1},
		{MaxLineBytes: maxRPCMuxDiagnosisOperatorStoreLine + 1},
		{MaxActions: maxRPCMuxDiagnosisOperatorStoreActions + 1},
	} {
		if store, err := NewRPCMuxDiagnosisOperatorHistoryFileStoreWithConfig("operator.jsonl", config); err == nil || store != nil {
			t.Fatalf("invalid history config %+v returned store=%#v err=%v", config, store, err)
		}
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
	for _, test := range []struct {
		name        string
		diagnostics rpcMuxDiagnosisOperatorHistoryDiagnostics
		want        string
	}{
		{name: "truncated", diagnostics: rpcMuxDiagnosisOperatorHistoryDiagnostics{truncatedLines: 1}, want: "truncated"},
		{name: "mixed legacy", diagnostics: rpcMuxDiagnosisOperatorHistoryDiagnostics{legacyLines: 1, headerPresent: true}, want: "mixed_legacy"},
		{name: "legacy", diagnostics: rpcMuxDiagnosisOperatorHistoryDiagnostics{legacyLines: 1}, want: "legacy"},
		{name: "missing header", diagnostics: rpcMuxDiagnosisOperatorHistoryDiagnostics{}, want: "missing_header"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := integrityStatusForOperatorHistory(test.diagnostics); got != test.want {
				t.Fatalf("integrity status = %q, want %q", got, test.want)
			}
		})
	}
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{Version: "store-diagnostics-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	if snapshot := (*RPCMuxDiagnosisSinkSet)(nil).OperatorHistoryStoreSnapshot(); snapshot.Enabled {
		t.Fatalf("nil sink set store snapshot = %+v", snapshot)
	}
	if got := (*RPCMuxDiagnosisSinkSet)(nil).WithOperatorHistoryStore(fakeOperatorHistoryStore{}); got != nil {
		t.Fatalf("nil sink set with history store = %#v", got)
	}
	if snapshot := sinkSet.OperatorHistoryStoreSnapshot(); snapshot.Enabled {
		t.Fatalf("empty store snapshot = %+v", snapshot)
	}
	if integrity, err := sinkSet.RPCMuxDiagnosisOperatorHistoryIntegritySnapshot(context.Background()); err != nil ||
		integrity.IntegrityStatus != "disabled" {
		t.Fatalf("disabled store integrity = %+v err=%v", integrity, err)
	}
	customStore := fakeOperatorHistoryStore{}
	sinkSet.WithOperatorHistoryStore(customStore)
	if snapshot := sinkSet.OperatorHistoryStoreSnapshot(); !snapshot.Enabled || snapshot.Kind != "custom" {
		t.Fatalf("custom store snapshot = %+v", snapshot)
	}
	if integrity, err := sinkSet.RPCMuxDiagnosisOperatorHistoryIntegritySnapshot(context.Background()); err != nil ||
		integrity.IntegrityStatus != "unsupported" {
		t.Fatalf("custom store integrity = %+v err=%v", integrity, err)
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
	verification, err := store.VerifyRPCMuxDiagnosisOperatorHistory(context.Background())
	if err == nil || verification.Primary.IntegrityStatus != "size_limit_exceeded" ||
		verification.Backup.IntegrityStatus != "missing" {
		t.Fatalf("oversized verification = %+v err=%v", verification, err)
	}
	if replay, err := store.ReplayRPCMuxDiagnosisOperatorHistory(context.Background(), RPCMuxDiagnosisOperatorHistorySourceBackup, 0); err != nil ||
		replay.Evidence.Exists || replay.Evidence.IntegrityStatus != "missing" {
		t.Fatalf("missing backup replay = %+v err=%v", replay, err)
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

func TestRegisterRPCMuxDiagnosisOperatorHistoryMetricsNilRegistry(t *testing.T) {
	saved := rpcMuxDiagnosisOperatorHistoryIntegrityState
	t.Cleanup(func() { rpcMuxDiagnosisOperatorHistoryIntegrityState = saved })
	// A nil registry must fall back to the default registry rather than panic.
	registerRPCMuxDiagnosisOperatorHistoryMetrics(nil)
	if rpcMuxDiagnosisOperatorHistoryIntegrityState == nil {
		t.Fatal("nil registry did not fall back to default integrity gauge")
	}
}

func TestRPCMuxDiagnosisOperatorAuditSchemasCompatibility(t *testing.T) {
	schemas := RPCMuxDiagnosisOperatorAuditSchemas()
	debugReplay, ok := schemas["debugReplay"]
	if !ok {
		t.Fatalf("audit schemas = %+v, want debugReplay entry", schemas)
	}
	// Pin the schema version and field ordering so downstream control planes and
	// audit consumers do not silently break when field names drift.
	if debugReplay.Schema != "gofly.rpc_mux_operator_debug_replay_audit.v1" {
		t.Fatalf("debug replay schema version = %q, want v1", debugReplay.Schema)
	}
	wantFields := []string{"source", "limit", "token_result"}
	if len(debugReplay.Fields) != len(wantFields) {
		t.Fatalf("debug replay fields = %v, want %v", debugReplay.Fields, wantFields)
	}
	for i, field := range wantFields {
		if debugReplay.Fields[i] != field {
			t.Fatalf("debug replay field[%d] = %q, want %q", i, debugReplay.Fields[i], field)
		}
	}

	// pause_sink, resume_sink, and force_probe share one sink action schema so
	// their persisted details stay parseable against a single contract.
	wantSinkFields := []string{"operator_action", "breaker_state", "isolation_mode"}
	for _, action := range []string{
		RPCMuxDiagnosisOperatorPauseSink,
		RPCMuxDiagnosisOperatorResumeSink,
		RPCMuxDiagnosisOperatorForceProbe,
	} {
		schema, ok := schemas[action]
		if !ok {
			t.Fatalf("audit schemas = %+v, want %q entry", schemas, action)
		}
		if schema.Schema != "gofly.rpc_mux_operator_sink_action_audit.v1" {
			t.Fatalf("%s schema version = %q, want sink action v1", action, schema.Schema)
		}
		if len(schema.Fields) != len(wantSinkFields) {
			t.Fatalf("%s fields = %v, want %v", action, schema.Fields, wantSinkFields)
		}
		for i, field := range wantSinkFields {
			if schema.Fields[i] != field {
				t.Fatalf("%s field[%d] = %q, want %q", action, i, schema.Fields[i], field)
			}
		}
	}

	// The audit payload must carry the schema marker plus every declared field so
	// consumers can validate details against the published schema contract.
	details := RPCMuxDiagnosisOperatorDebugReplayAuditDetails{
		Source:      RPCMuxDiagnosisOperatorHistorySourcePrimary,
		Limit:       4,
		TokenResult: "approved",
	}.StringMap()
	if details["schema"] != debugReplay.Schema {
		t.Fatalf("details schema = %q, want %q", details["schema"], debugReplay.Schema)
	}
	for _, field := range debugReplay.Fields {
		if _, ok := details[field]; !ok {
			t.Fatalf("details %+v missing declared field %q", details, field)
		}
	}

	// Every schema entry must expose a machine-parseable JSON Schema whose typed
	// properties and required list stay in lockstep with the declared fields, so
	// external auditors can validate structurally rather than by field name only.
	for name, schema := range schemas {
		assertOperatorAuditJSONSchema(t, name, schema)
	}
}

func TestRPCMuxDiagnosisOperatorAuditRecordSchema(t *testing.T) {
	// A recorded debug replay resolves back to its published schema via the marker.
	debugRecord := RPCMuxDiagnosisOperatorAction{
		Action: RPCMuxDiagnosisOperatorDebugReplay,
		Details: RPCMuxDiagnosisOperatorDebugReplayAuditDetails{
			Source:      RPCMuxDiagnosisOperatorHistorySourcePrimary,
			Limit:       1,
			TokenResult: "approved",
		}.StringMap(),
	}
	schema, ok := RPCMuxDiagnosisOperatorAuditRecordSchema(debugRecord)
	if !ok || schema.Schema != "gofly.rpc_mux_operator_debug_replay_audit.v1" {
		t.Fatalf("debug replay record schema = (%+v, %v)", schema, ok)
	}

	// A recorded sink action resolves to the shared sink action schema.
	sinkRecord := rpcMuxDiagnosisOperatorActionForSink(RPCMuxDiagnosisSinkRuntimeSnapshot{
		Name: "sink-a",
		Delivery: RPCMuxDiagnosisExporterDeliverySnapshot{
			OperatorAction: "pause_sink_breaker",
			BreakerState:   "open",
			Isolation:      RPCMuxDiagnosisSinkIsolationConfig{Mode: RPCMuxDiagnosisSinkIsolationIsolatedProcess},
		},
	})
	schema, ok = RPCMuxDiagnosisOperatorAuditRecordSchema(sinkRecord)
	if !ok || schema.Schema != "gofly.rpc_mux_operator_sink_action_audit.v1" {
		t.Fatalf("sink action record schema = (%+v, %v)", schema, ok)
	}

	// A record without a schema marker, or with an unknown marker, is reported
	// as having no structured schema rather than guessing.
	if _, ok := RPCMuxDiagnosisOperatorAuditRecordSchema(RPCMuxDiagnosisOperatorAction{Action: RPCMuxDiagnosisOperatorPauseSink}); ok {
		t.Fatal("record without details resolved a schema")
	}
	if _, ok := RPCMuxDiagnosisOperatorAuditRecordSchema(RPCMuxDiagnosisOperatorAction{
		Details: map[string]string{"schema": "gofly.rpc_mux_operator_unknown.v9"},
	}); ok {
		t.Fatal("record with unknown marker resolved a schema")
	}
}

func TestRPCMuxDiagnosisOperatorAuditSchemaValidateDetails(t *testing.T) {
	schema := RPCMuxDiagnosisOperatorAuditSchemas()["debugReplay"]

	valid := RPCMuxDiagnosisOperatorDebugReplayAuditDetails{
		Source:      RPCMuxDiagnosisOperatorHistorySourcePrimary,
		Limit:       2,
		TokenResult: "approved",
	}.StringMap()
	if err := schema.ValidateDetails(valid); err != nil {
		t.Fatalf("valid details rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{"wrong marker", func(p map[string]string) { p["schema"] = "gofly.rpc_mux_operator_sink_action_audit.v1" }},
		{"missing field", func(p map[string]string) { delete(p, "token_result") }},
		{"undeclared field", func(p map[string]string) { p["injected"] = "x" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := RPCMuxDiagnosisOperatorDebugReplayAuditDetails{
				Source:      RPCMuxDiagnosisOperatorHistorySourcePrimary,
				Limit:       1,
				TokenResult: "approved",
			}.StringMap()
			test.mutate(payload)
			if err := schema.ValidateDetails(payload); err == nil {
				t.Fatalf("invalid details %v accepted", payload)
			}
		})
	}
}

func TestRPCMuxDiagnosisOperatorAuditRecordValid(t *testing.T) {
	// A real sink action record validates end to end via the marker lookup.
	sinkRecord := rpcMuxDiagnosisOperatorActionForSink(RPCMuxDiagnosisSinkRuntimeSnapshot{
		Name: "sink-a",
		Delivery: RPCMuxDiagnosisExporterDeliverySnapshot{
			OperatorAction: "pause_sink_breaker",
			BreakerState:   "open",
			Isolation:      RPCMuxDiagnosisSinkIsolationConfig{Mode: RPCMuxDiagnosisSinkIsolationIsolatedProcess},
		},
	})
	if err := RPCMuxDiagnosisOperatorAuditRecordValid(sinkRecord); err != nil {
		t.Fatalf("valid sink action record rejected: %v", err)
	}

	// A record without a recognized marker fails fast.
	if err := RPCMuxDiagnosisOperatorAuditRecordValid(RPCMuxDiagnosisOperatorAction{
		Action:  RPCMuxDiagnosisOperatorPauseSink,
		Details: map[string]string{"operator_action": "pause_sink_breaker"},
	}); err == nil {
		t.Fatal("record without schema marker validated")
	}
}

func TestVerifyOperatorAuditRecordSchema(t *testing.T) {
	var buf strings.Builder
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(saved) })

	// A record without a marker is skipped: no validation, no log.
	verifyOperatorAuditRecordSchema(RPCMuxDiagnosisOperatorAction{
		Sink:   "slog",
		Action: RPCMuxDiagnosisOperatorPauseSink,
	})
	// A well-formed marker-bearing record validates cleanly: still no log.
	verifyOperatorAuditRecordSchema(RPCMuxDiagnosisOperatorAction{
		Action: RPCMuxDiagnosisOperatorDebugReplay,
		Details: RPCMuxDiagnosisOperatorDebugReplayAuditDetails{
			Source:      RPCMuxDiagnosisOperatorHistorySourcePrimary,
			Limit:       1,
			TokenResult: "approved",
		}.StringMap(),
	})
	if buf.Len() != 0 {
		t.Fatalf("unexpected audit schema log for valid records: %s", buf.String())
	}

	// A marker-bearing record with a broken details map logs at debug level and
	// increments the low-cardinality violation metric, but the hook itself never
	// panics or rejects.
	before := operatorAuditSchemaViolationCount(t, RPCMuxDiagnosisOperatorDebugReplay)
	verifyOperatorAuditRecordSchema(RPCMuxDiagnosisOperatorAction{
		Sink:    "slog",
		Action:  RPCMuxDiagnosisOperatorDebugReplay,
		Details: map[string]string{"schema": "gofly.rpc_mux_operator_debug_replay_audit.v1"},
	})
	if !strings.Contains(buf.String(), "violates schema") {
		t.Fatalf("expected schema violation log, got: %s", buf.String())
	}
	if after := operatorAuditSchemaViolationCount(t, RPCMuxDiagnosisOperatorDebugReplay); after != before+1 {
		t.Fatalf("violation metric = %v, want %v", after, before+1)
	}

	// An unrecognized action folds into the low-cardinality "other" bucket.
	otherBefore := operatorAuditSchemaViolationCount(t, "other")
	verifyOperatorAuditRecordSchema(RPCMuxDiagnosisOperatorAction{
		Action:  "custom_action",
		Details: map[string]string{"schema": "gofly.rpc_mux_operator_debug_replay_audit.v1"},
	})
	if after := operatorAuditSchemaViolationCount(t, "other"); after != otherBefore+1 {
		t.Fatalf("other-bucket violation metric = %v, want %v", after, otherBefore+1)
	}
}

func operatorAuditSchemaViolationCount(t *testing.T, action string) float64 {
	t.Helper()
	snapshot := metrics.Default.Snapshot().Customs["gofly_rpc_mux_operator_audit_schema_violation_total"]
	for _, series := range snapshot.Series {
		if series.Labels["action"] == action {
			return series.Value
		}
	}
	return 0
}

func TestRPCMuxDiagnosisSinkSetRecordOperatorActionValidatesWithoutBlocking(t *testing.T) {
	var buf strings.Builder
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(saved) })

	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{Version: "audit-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	// A malformed but approved audit record is still recorded (write is never
	// blocked) while the schema violation is logged for observability.
	sinkSet.RecordRPCMuxDiagnosisOperatorAuditAction(RPCMuxDiagnosisOperatorAction{
		Action:   RPCMuxDiagnosisOperatorDebugReplay,
		Approved: true,
		Details:  map[string]string{"schema": "gofly.rpc_mux_operator_debug_replay_audit.v1"},
	})
	if history := sinkSet.RPCMuxDiagnosisOperatorActionHistory(1); len(history) != 1 {
		t.Fatalf("audit history = %+v, want the record retained", history)
	}
	if !strings.Contains(buf.String(), "violates schema") {
		t.Fatalf("expected schema violation log on write path, got: %s", buf.String())
	}
}

func assertOperatorAuditJSONSchema(t *testing.T, name string, schema RPCMuxDiagnosisOperatorAuditSchema) {
	t.Helper()
	if len(schema.JSONSchema) == 0 {
		t.Fatalf("%s schema missing json schema", name)
	}
	var document struct {
		Type                 string                    `json:"type"`
		AdditionalProperties bool                      `json:"additionalProperties"`
		Properties           map[string]map[string]any `json:"properties"`
		Required             []string                  `json:"required"`
	}
	if err := json.Unmarshal(schema.JSONSchema, &document); err != nil {
		t.Fatalf("%s json schema decode: %v", name, err)
	}
	if document.Type != "object" || document.AdditionalProperties {
		t.Fatalf("%s json schema = %s, want closed object", name, schema.JSONSchema)
	}
	// The schema marker property must be pinned to the schema version constant.
	schemaProp, ok := document.Properties["schema"]
	if !ok || schemaProp["type"] != "string" || schemaProp["const"] != schema.Schema {
		t.Fatalf("%s json schema marker = %#v, want const %q", name, schemaProp, schema.Schema)
	}
	wantRequired := append([]string{"schema"}, schema.Fields...)
	if len(document.Required) != len(wantRequired) {
		t.Fatalf("%s json schema required = %v, want %v", name, document.Required, wantRequired)
	}
	for i, field := range wantRequired {
		if document.Required[i] != field {
			t.Fatalf("%s json schema required[%d] = %q, want %q", name, i, document.Required[i], field)
		}
	}
	for _, field := range schema.Fields {
		prop, ok := document.Properties[field]
		if !ok || prop["type"] != "string" {
			t.Fatalf("%s json schema property %q = %#v, want string type", name, field, prop)
		}
	}
	if len(document.Properties) != len(schema.Fields)+1 {
		t.Fatalf("%s json schema properties = %v, want fields plus schema marker", name, document.Properties)
	}
}

// validateAgainstOperatorAuditJSONSchema is a minimal JSON Schema checker that
// enforces the subset the audit schemas rely on: type=object, a required list,
// additionalProperties=false, string-typed properties, and const markers. It
// returns an error describing the first violation, or nil when the payload is
// valid. It is intentionally test-only so production code carries no validator.
func validateAgainstOperatorAuditJSONSchema(raw json.RawMessage, payload map[string]string) error {
	var document struct {
		Type                 string                    `json:"type"`
		AdditionalProperties bool                      `json:"additionalProperties"`
		Properties           map[string]map[string]any `json:"properties"`
		Required             []string                  `json:"required"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	if document.Type != "object" {
		return fmt.Errorf("schema type %q is not object", document.Type)
	}
	for _, field := range document.Required {
		if _, ok := payload[field]; !ok {
			return fmt.Errorf("missing required field %q", field)
		}
	}
	for key, value := range payload {
		spec, ok := document.Properties[key]
		if !ok {
			if !document.AdditionalProperties {
				return fmt.Errorf("additional property %q not allowed", key)
			}
			continue
		}
		if typ, ok := spec["type"].(string); ok && typ != "string" {
			return fmt.Errorf("property %q must be %q", key, typ)
		}
		if want, ok := spec["const"].(string); ok && want != value {
			return fmt.Errorf("property %q must equal %q, got %q", key, want, value)
		}
	}
	return nil
}

func TestRPCMuxDiagnosisOperatorAuditJSONSchemaAcceptsAndRejects(t *testing.T) {
	schema := RPCMuxDiagnosisOperatorAuditSchemas()["debugReplay"]

	// A real, well-formed audit payload must validate against the published
	// JSON Schema, proving the schema is usable and not merely structural.
	valid := RPCMuxDiagnosisOperatorDebugReplayAuditDetails{
		Source:      RPCMuxDiagnosisOperatorHistorySourcePrimary,
		Limit:       2,
		TokenResult: "approved",
	}.StringMap()
	if err := validateAgainstOperatorAuditJSONSchema(schema.JSONSchema, valid); err != nil {
		t.Fatalf("valid debug replay payload rejected: %v", err)
	}

	// The sink action schema must accept its producer output too.
	sinkAction := rpcMuxDiagnosisOperatorActionForSink(RPCMuxDiagnosisSinkRuntimeSnapshot{
		Name: "sink-a",
		Delivery: RPCMuxDiagnosisExporterDeliverySnapshot{
			OperatorAction: "pause_sink_breaker",
			BreakerState:   "open",
			Isolation:      RPCMuxDiagnosisSinkIsolationConfig{Mode: RPCMuxDiagnosisSinkIsolationIsolatedProcess},
		},
	})
	sinkSchema := RPCMuxDiagnosisOperatorAuditSchemas()[sinkAction.Action]
	if err := validateAgainstOperatorAuditJSONSchema(sinkSchema.JSONSchema, sinkAction.Details); err != nil {
		t.Fatalf("valid sink action payload rejected: %v", err)
	}

	reject := func(name string, mutate func(map[string]string)) {
		t.Run(name, func(t *testing.T) {
			payload := RPCMuxDiagnosisOperatorDebugReplayAuditDetails{
				Source:      RPCMuxDiagnosisOperatorHistorySourcePrimary,
				Limit:       1,
				TokenResult: "approved",
			}.StringMap()
			mutate(payload)
			if err := validateAgainstOperatorAuditJSONSchema(schema.JSONSchema, payload); err == nil {
				t.Fatalf("invalid payload %v accepted", payload)
			}
		})
	}
	// required enforcement: a dropped declared field must fail.
	reject("missing required field", func(p map[string]string) { delete(p, "token_result") })
	// const marker enforcement: a wrong schema marker must fail.
	reject("wrong schema marker", func(p map[string]string) {
		p["schema"] = "gofly.rpc_mux_operator_debug_replay_audit.v2"
	})
	// additionalProperties:false enforcement: an undeclared key must fail.
	reject("unexpected extra field", func(p map[string]string) { p["injected"] = "true" })
}

func TestRPCMuxDiagnosisOperatorHistoryDegradedEvidence(t *testing.T) {
	tests := []struct {
		name         string
		verification RPCMuxDiagnosisOperatorHistoryVerification
		wantReason   string
		wantSource   string
	}{
		{
			name: "primary degraded",
			verification: RPCMuxDiagnosisOperatorHistoryVerification{
				Primary: RPCMuxDiagnosisOperatorHistoryFileEvidence{IntegrityStatus: "tampered", Source: "primary"},
			},
			wantReason: "tampered",
			wantSource: "primary",
		},
		{
			name: "backup degraded",
			verification: RPCMuxDiagnosisOperatorHistoryVerification{
				Primary: RPCMuxDiagnosisOperatorHistoryFileEvidence{IntegrityStatus: "ok", Source: "primary"},
				Backup:  RPCMuxDiagnosisOperatorHistoryFileEvidence{IntegrityStatus: "truncated", Source: "backup"},
			},
			wantReason: "truncated",
			wantSource: "backup",
		},
		{
			name: "ring backup degraded",
			verification: RPCMuxDiagnosisOperatorHistoryVerification{
				Primary: RPCMuxDiagnosisOperatorHistoryFileEvidence{IntegrityStatus: "ok", Source: "primary"},
				Backup:  RPCMuxDiagnosisOperatorHistoryFileEvidence{IntegrityStatus: "missing", Source: "backup"},
				Backups: []RPCMuxDiagnosisOperatorHistoryFileEvidence{
					{IntegrityStatus: "ok", Source: "backup.1"},
					{IntegrityStatus: "size_limit_exceeded", Source: "backup.2"},
				},
			},
			wantReason: "size_limit_exceeded",
			wantSource: "backup.2",
		},
		{
			name: "all healthy",
			verification: RPCMuxDiagnosisOperatorHistoryVerification{
				Primary: RPCMuxDiagnosisOperatorHistoryFileEvidence{IntegrityStatus: "ok", Source: "primary"},
				Backup:  RPCMuxDiagnosisOperatorHistoryFileEvidence{IntegrityStatus: "missing", Source: "backup"},
			},
			wantReason: "",
			wantSource: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason, source := rpcMuxDiagnosisOperatorHistoryDegradedEvidence(test.verification)
			if reason != test.wantReason || source != test.wantSource {
				t.Fatalf("degraded evidence = (%q, %q), want (%q, %q)", reason, source, test.wantReason, test.wantSource)
			}
		})
	}
}

func TestRPCMuxDiagnosisOperatorHistoryLimits(t *testing.T) {
	limits := RPCMuxDiagnosisOperatorHistoryLimits()
	if limits.MaxSizeBytes <= 0 || limits.MaxLineBytes <= 0 || limits.MaxActions <= 0 || limits.MaxBackups <= 0 {
		t.Fatalf("history limits = %+v, want positive hard bounds", limits)
	}
	if limits.MaxBackups != maxRPCMuxDiagnosisOperatorBackupFiles ||
		limits.MaxActions != maxRPCMuxDiagnosisOperatorStoreActions ||
		limits.MaxSizeBytes != maxRPCMuxDiagnosisOperatorStoreSize ||
		limits.MaxLineBytes != maxRPCMuxDiagnosisOperatorStoreLine {
		t.Fatalf("history limits = %+v, want core hard limit constants", limits)
	}
	if limits.DebugReplayCooldown != defaultRPCMuxDebugReplayInterval {
		t.Fatalf("history limits cooldown = %v, want default interval", limits.DebugReplayCooldown)
	}
}

func TestRPCMuxDiagnosisOperatorHistoryRecommendedLimits(t *testing.T) {
	recommended := RPCMuxDiagnosisOperatorHistoryRecommendedLimits()
	if recommended.MaxActions != recommendedRPCMuxDiagnosisOperatorStoreActions ||
		recommended.MaxBackups != recommendedRPCMuxDiagnosisOperatorBackupFiles ||
		recommended.MaxSizeBytes != recommendedRPCMuxDiagnosisOperatorStoreSize ||
		recommended.MaxLineBytes != recommendedRPCMuxDiagnosisOperatorStoreLine {
		t.Fatalf("recommended limits = %+v, want recommended constants", recommended)
	}
	// Recommended bounds must never exceed the core hard limits; otherwise a
	// config accepted by the production check could still be rejected at runtime.
	hard := RPCMuxDiagnosisOperatorHistoryLimits()
	if recommended.MaxActions > hard.MaxActions ||
		recommended.MaxBackups > hard.MaxBackups ||
		recommended.MaxSizeBytes > hard.MaxSizeBytes ||
		recommended.MaxLineBytes > hard.MaxLineBytes {
		t.Fatalf("recommended %+v exceeds hard limits %+v", recommended, hard)
	}
}

func TestRPCMuxDiagnosisOperatorSinkActionDetailsMatchSchema(t *testing.T) {
	// A real sink action must emit the schema marker plus exactly the detail keys
	// declared by the sink action audit schema, so adding a producer field without
	// updating the schema (or vice versa) fails fast rather than silently drifting.
	action := rpcMuxDiagnosisOperatorActionForSink(RPCMuxDiagnosisSinkRuntimeSnapshot{
		Name: "sink-a",
		Delivery: RPCMuxDiagnosisExporterDeliverySnapshot{
			OperatorAction: "pause_sink_breaker",
			BreakerState:   "open",
			Isolation: RPCMuxDiagnosisSinkIsolationConfig{
				Mode: RPCMuxDiagnosisSinkIsolationIsolatedProcess,
			},
		},
	})
	if action.Action != RPCMuxDiagnosisOperatorPauseSink {
		t.Fatalf("action = %+v, want pause_sink", action)
	}
	schema, ok := RPCMuxDiagnosisOperatorAuditSchemas()[action.Action]
	if !ok {
		t.Fatalf("no audit schema for action %q", action.Action)
	}
	// Persisted sink action details carry the schema marker like debug replay, so
	// both audit record types can be consumed the same way.
	if action.Details["schema"] != schema.Schema {
		t.Fatalf("action details schema = %q, want %q", action.Details["schema"], schema.Schema)
	}
	wantKeys := append([]string{"schema"}, schema.Fields...)
	if len(action.Details) != len(wantKeys) {
		t.Fatalf("action details = %v, want keys %v", action.Details, wantKeys)
	}
	for _, field := range schema.Fields {
		if _, ok := action.Details[field]; !ok {
			t.Fatalf("action details %v missing schema field %q", action.Details, field)
		}
	}
	for key := range action.Details {
		if key == "schema" {
			continue
		}
		found := false
		for _, field := range schema.Fields {
			if field == key {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("action detail key %q not declared in schema fields %v", key, schema.Fields)
		}
	}
}

func assertOperatorHistoryFileUnchanged(t *testing.T, path string, before []byte, infoBefore os.FileInfo) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || infoAfter.Size() != infoBefore.Size() || !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Fatalf("read-only replay modified %s", path)
	}
}
