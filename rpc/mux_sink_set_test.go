package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRPCMuxDiagnosisSinkSetHotReloadDrainsPreviousGeneration(t *testing.T) {
	var firstClosed atomic.Int64
	firstRecords := make(chan string, 1)
	secondRecords := make(chan string, 1)
	cleanupFirst := RegisterRPCMuxOTelLogSinkProvider("reload-first", sinkSetTestProvider{
		newExporter: func(profile string) RPCMuxOTelLogExporter {
			return &sinkSetTestExporter{
				closed: &firstClosed,
				export: func(RPCMuxDiagnosisEventOTelLogRecord) {
					firstRecords <- profile
				},
			}
		},
	})
	defer cleanupFirst()
	cleanupSecond := RegisterRPCMuxOTelLogSinkProvider("reload-second", sinkSetTestProvider{
		newExporter: func(profile string) RPCMuxOTelLogExporter {
			return &sinkSetTestExporter{
				export: func(RPCMuxDiagnosisEventOTelLogRecord) {
					secondRecords <- profile
				},
			}
		},
	})
	defer cleanupSecond()

	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "v1",
		Sinks: []RPCMuxDiagnosisSinkConfig{{
			Name:    "reload-first",
			Profile: "profile-v1",
			Delivery: RPCMuxDiagnosisExporterDeliveryConfig{
				QueueSize: 1,
				Timeout:   time.Second,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	if got := receiveSinkSetValue(t, firstRecords); got != "profile-v1" {
		t.Fatalf("first profile = %q, want profile-v1", got)
	}
	if err := sinkSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{
		Version: "v2",
		Sinks: []RPCMuxDiagnosisSinkConfig{{
			Name:     "reload-second",
			Profile:  "profile-v2",
			Priority: 10,
		}},
	}); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if firstClosed.Load() != 1 {
		t.Fatalf("previous exporter close count = %d, want 1", firstClosed.Load())
	}

	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	if got := receiveSinkSetValue(t, secondRecords); got != "profile-v2" {
		t.Fatalf("second profile = %q, want profile-v2", got)
	}
	snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if snapshot.Version != "v2" || snapshot.Reloads != 2 || snapshot.Rollbacks != 0 ||
		snapshot.SinkCount != 1 || snapshot.Sinks[0].Name != "reload-second" ||
		snapshot.Sinks[0].Priority != 10 {
		t.Fatalf("hot reload snapshot = %+v", snapshot)
	}
}

func TestRPCMuxDiagnosisSinkSetReloadRollbackPreservesActiveGeneration(t *testing.T) {
	records := make(chan struct{}, 1)
	cleanup := RegisterRPCMuxOTelLogSinkProvider("rollback-good", sinkSetTestProvider{
		newExporter: func(string) RPCMuxOTelLogExporter {
			return &sinkSetTestExporter{export: func(RPCMuxDiagnosisEventOTelLogRecord) {
				records <- struct{}{}
			}}
		},
	})
	defer cleanup()

	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "stable-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "rollback-good"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	err = sinkSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{
		Version: "broken-v2",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "missing-sink", Profile: "secret-profile"}},
	})
	if err == nil {
		t.Fatal("Reload missing sink succeeded")
	}
	snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if snapshot.Version != "stable-v1" || snapshot.Reloads != 1 || snapshot.Rollbacks != 1 ||
		snapshot.LastReloadError != "sink is not registered" {
		t.Fatalf("rollback snapshot = %+v", snapshot)
	}
	if snapshot.LastReloadError == "secret-profile" {
		t.Fatalf("rollback snapshot leaked profile: %+v", snapshot)
	}
	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	receiveSinkSetValue(t, records)
}

func TestRPCMuxDiagnosisSinkSetSuccessfulReloadClearsRollbackState(t *testing.T) {
	cleanup := RegisterRPCMuxOTelLogSink("reload-state", func(string) RPCMuxOTelLogExporter {
		return RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {})
	})
	defer cleanup()
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "stable-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "reload-state"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	if err := sinkSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{
		Version: "broken-v2",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "missing-reload-state"}},
	}); err == nil {
		t.Fatal("broken reload succeeded")
	}
	failed := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if failed.LastReloadError == "" || failed.LastReloadErrorAt.IsZero() {
		t.Fatalf("failed reload snapshot = %+v, want error and timestamp", failed)
	}

	if err := sinkSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{
		Version: "stable-v3",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "reload-state"}},
	}); err != nil {
		t.Fatalf("successful reload: %v", err)
	}
	recovered := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if recovered.LastReloadError != "" || !recovered.LastReloadErrorAt.IsZero() {
		t.Fatalf("recovered reload snapshot = %+v, want cleared rollback state", recovered)
	}
}

func TestRPCMuxDiagnosisSinkSetReloadFailureUsesInjectedClock(t *testing.T) {
	cleanup := RegisterRPCMuxOTelLogSink("reload-clock", func(string) RPCMuxOTelLogExporter {
		return RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {})
	})
	defer cleanup()
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "stable-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "reload-clock"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	// A reload failure must stamp the injected clock rather than the wall clock,
	// so the snapshot timestamp is deterministic for observers and tests.
	stamped := time.Unix(1700000123, 0).UTC()
	sinkSet.nowFunc = func() time.Time { return stamped }
	if err := sinkSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{
		Version: "broken-v2",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "missing-reload-clock"}},
	}); err == nil {
		t.Fatal("broken reload succeeded")
	}
	if snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot(); !snapshot.LastReloadErrorAt.Equal(stamped) {
		t.Fatalf("reload failure timestamp = %v, want injected %v", snapshot.LastReloadErrorAt, stamped)
	}
}

func TestRPCMuxDiagnosisSinkSetSuccessfulReloadUsesInjectedClock(t *testing.T) {
	cleanup := RegisterRPCMuxOTelLogSink("reload-success-clock", func(string) RPCMuxOTelLogExporter {
		return RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {})
	})
	defer cleanup()
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "stable-v1",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "reload-success-clock"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	// A successful reload stamps lastReloadAt from the injected generation clock,
	// so the snapshot timestamp is deterministic.
	stamped := time.Unix(1700000456, 0).UTC()
	sinkSet.nowFunc = func() time.Time { return stamped }
	if err := sinkSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{
		Version: "stable-v2",
		Sinks:   []RPCMuxDiagnosisSinkConfig{{Name: "reload-success-clock"}},
	}); err != nil {
		t.Fatalf("successful reload: %v", err)
	}
	if snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot(); !snapshot.LastReloadAt.Equal(stamped) {
		t.Fatalf("reload success timestamp = %v, want injected %v", snapshot.LastReloadAt, stamped)
	}
	// The generation UpdatedAt that feeds the diff-plan context is stamped from
	// the same injected clock, and the diff plan reflects the version change.
	snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if !snapshot.UpdatedAt.Equal(stamped) {
		t.Fatalf("snapshot UpdatedAt = %v, want injected %v", snapshot.UpdatedAt, stamped)
	}
	if snapshot.LastDiffPlan.FromVersion != "stable-v1" || snapshot.LastDiffPlan.ToVersion != "stable-v2" {
		t.Fatalf("diff plan versions = %+v, want stable-v1 -> stable-v2", snapshot.LastDiffPlan)
	}
}

func TestRPCMuxDiagnosisSinkSetActivatesFromEmptyGenerationWithSecretProfile(t *testing.T) {
	records := make(chan string, 1)
	cleanup := RegisterRPCMuxOTelLogSinkProvider("dynamic-secret", sinkSetTestProvider{
		validate: func(profile string) error {
			if profile != "resolved-profile" {
				return errors.New("unexpected resolved profile")
			}
			return nil
		},
		newExporter: func(profile string) RPCMuxOTelLogExporter {
			return &sinkSetTestExporter{export: func(RPCMuxDiagnosisEventOTelLogRecord) {
				records <- profile
			}}
		},
	})
	defer cleanup()
	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version:       "disabled-v1",
		SchemaVersion: "mux-sinks/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	next := RPCMuxDiagnosisSinkSetConfig{
		Version:       "enabled-v2",
		SchemaVersion: "mux-sinks/v2",
		Secrets: func(_ context.Context, ref string) (string, error) {
			if ref != "secret://mux/dynamic-secret" {
				return "", errors.New("unknown secret ref")
			}
			return "resolved-profile", nil
		},
		Sinks: []RPCMuxDiagnosisSinkConfig{{
			Name:             "dynamic-secret",
			ProfileRef:       "secret://mux/dynamic-secret",
			ProfileSchema:    "dynamic-secret/v2",
			ProfileMigration: "v1-to-v2",
			Priority:         30,
		}},
	}
	plan, err := sinkSet.DiffRPCMuxDiagnosisSinkSetConfig(context.Background(), next)
	if err != nil {
		t.Fatalf("DiffRPCMuxDiagnosisSinkSetConfig: %v", err)
	}
	if !slicesEqual(plan.Activate, []string{"dynamic-secret"}) || !slicesEqual(plan.MigrateProfile, []string{"dynamic-secret"}) ||
		plan.FromSchemaVersion != "mux-sinks/v1" || plan.ToSchemaVersion != "mux-sinks/v2" {
		t.Fatalf("diff plan = %+v", plan)
	}
	if strings.Contains(fmt.Sprint(plan), "resolved-profile") || strings.Contains(fmt.Sprint(plan), "secret://") {
		t.Fatalf("diff plan leaked profile material: %+v", plan)
	}
	if err := sinkSet.Reload(context.Background(), next); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	if got := receiveSinkSetValue(t, records); got != "resolved-profile" {
		t.Fatalf("resolved profile = %q", got)
	}
	snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if snapshot.Version != "enabled-v2" || snapshot.SchemaVersion != "mux-sinks/v2" ||
		snapshot.SinkCount != 1 || snapshot.Sinks[0].ProfileSchema != "dynamic-secret/v2" ||
		snapshot.Sinks[0].ProfileMigration != "v1-to-v2" ||
		!slicesEqual(snapshot.LastDiffPlan.Activate, []string{"dynamic-secret"}) {
		t.Fatalf("dynamic activation snapshot = %+v", snapshot)
	}
	if strings.Contains(fmt.Sprint(snapshot), "resolved-profile") || strings.Contains(fmt.Sprint(snapshot), "secret://") {
		t.Fatalf("snapshot leaked profile material: %+v", snapshot)
	}
}

func TestRPCMuxDiagnosisEnvSecretResolver(t *testing.T) {
	t.Setenv("GOFLY_MUX_PROFILE", "env-profile")
	resolver := NewRPCMuxDiagnosisEnvSecretResolver()
	got, err := resolver(context.Background(), "env://GOFLY_MUX_PROFILE")
	if err != nil || got != "env-profile" {
		t.Fatalf("env resolver = %q err=%v, want env-profile nil", got, err)
	}
	tests := []struct {
		name string
		ref  string
	}{
		{name: "unsupported scheme", ref: "secret://profile"},
		{name: "empty env", ref: "env://"},
		{name: "path env", ref: "env://BAD/NAME"},
		{name: "missing env", ref: "env://GOFLY_MUX_PROFILE_MISSING"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolver(context.Background(), test.ref); err == nil {
				t.Fatalf("resolver(%q) succeeded, want error", test.ref)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver(canceled, "env://GOFLY_MUX_PROFILE"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolver err = %v, want context canceled", err)
	}
}

func TestRPCMuxDiagnosisFileAndLayeredSecretResolver(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte("file-profile"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolver := NewRPCMuxDiagnosisFileSecretResolver(dir, 64)
	got, err := resolver(context.Background(), "file://profile.json")
	if err != nil || got != "file-profile" {
		t.Fatalf("file resolver = %q err=%v, want file-profile nil", got, err)
	}
	tests := []struct {
		name string
		ref  string
	}{
		{name: "unsupported", ref: "env://PROFILE"},
		{name: "empty", ref: "file://"},
		{name: "escape", ref: "file://../profile.json"},
		{name: "missing", ref: "file://missing.json"},
		{name: "directory", ref: "file://profiles"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolver(context.Background(), test.ref); err == nil {
				t.Fatalf("file resolver(%q) succeeded, want error", test.ref)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "large.json"), []byte(strings.Repeat("x", 8)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRPCMuxDiagnosisFileSecretResolver(dir, 4)(context.Background(), "file://large.json"); err == nil {
		t.Fatal("oversized file secret resolved")
	}
	if _, err := NewRPCMuxDiagnosisFileSecretResolver("", 64)(context.Background(), "file://profile.json"); err == nil {
		t.Fatal("file resolver without root succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver(canceled, "file://profile.json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled file resolver err = %v, want context canceled", err)
	}
	layered := NewRPCMuxDiagnosisLayeredSecretResolver(nil, NewRPCMuxDiagnosisEnvSecretResolver(), resolver)
	if got, err := layered(context.Background(), "file://profile.json"); err != nil || got != "file-profile" {
		t.Fatalf("layered resolver = %q err=%v", got, err)
	}
	if _, err := NewRPCMuxDiagnosisLayeredSecretResolver()(context.Background(), "file://profile.json"); err == nil {
		t.Fatal("empty layered resolver succeeded")
	}
}

func TestRPCMuxDiagnosisSinkSetDiffPlanClassifiesChanges(t *testing.T) {
	cleanup := RegisterRPCMuxOTelLogSink("diff-plan", func(string) RPCMuxOTelLogExporter {
		return RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {})
	})
	defer cleanup()
	cleanupRemoved := RegisterRPCMuxOTelLogSink("diff-removed", func(string) RPCMuxOTelLogExporter {
		return RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {})
	})
	defer cleanupRemoved()

	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version:       "diff-v1",
		SchemaVersion: "mux-sinks/v1",
		Sinks: []RPCMuxDiagnosisSinkConfig{
			{Name: "diff-plan", Profile: "p1", ProfileSchema: "schema-v1", Priority: 1, Delivery: RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: 1}},
			{Name: "diff-removed", Profile: "p1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()

	plan, err := sinkSet.DiffRPCMuxDiagnosisSinkSetConfig(context.Background(), RPCMuxDiagnosisSinkSetConfig{
		Version:       "diff-v2",
		SchemaVersion: "mux-sinks/v2",
		Sinks: []RPCMuxDiagnosisSinkConfig{{
			Name:             "diff-plan",
			Profile:          "p2",
			ProfileSchema:    "schema-v2",
			ProfileMigration: "v1-to-v2",
			Priority:         2,
			Delivery:         RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: 2},
		}},
	})
	if err != nil {
		t.Fatalf("DiffRPCMuxDiagnosisSinkSetConfig: %v", err)
	}
	if !slicesEqual(plan.Retain, []string{"diff-plan"}) ||
		!slicesEqual(plan.Deactivate, []string{"diff-removed"}) ||
		!slicesEqual(plan.ChangePriority, []string{"diff-plan"}) ||
		!slicesEqual(plan.ChangeProfile, []string{"diff-plan"}) ||
		!slicesEqual(plan.ChangeDelivery, []string{"diff-plan"}) ||
		!slicesEqual(plan.MigrateProfile, []string{"diff-plan"}) {
		t.Fatalf("diff plan = %+v", plan)
	}
}

func TestRPCMuxDiagnosisDeliveryConfigEquality(t *testing.T) {
	base := RPCMuxDiagnosisExporterDeliveryConfig{
		QueueSize: 1,
		Isolation: RPCMuxDiagnosisSinkIsolationConfig{
			Mode:        RPCMuxDiagnosisSinkIsolationInProcess,
			AuditFields: map[string]string{"owner": "a"},
		},
	}
	same := base
	same.Isolation.AuditFields = map[string]string{"owner": "a"}
	if !equalRPCMuxDiagnosisExporterDeliveryConfig(base, same) {
		t.Fatal("equal delivery config was reported different")
	}
	differentMode := same
	differentMode.Isolation.Mode = RPCMuxDiagnosisSinkIsolationWASM
	if equalRPCMuxDiagnosisExporterDeliveryConfig(base, differentMode) {
		t.Fatal("different isolation mode was reported equal")
	}
	differentAuditLen := same
	differentAuditLen.Isolation.AuditFields = map[string]string{"owner": "a", "team": "runtime"}
	if equalRPCMuxDiagnosisExporterDeliveryConfig(base, differentAuditLen) {
		t.Fatal("different audit field length was reported equal")
	}
	differentAuditValue := same
	differentAuditValue.Isolation.AuditFields = map[string]string{"owner": "b"}
	if equalRPCMuxDiagnosisExporterDeliveryConfig(base, differentAuditValue) {
		t.Fatal("different audit field value was reported equal")
	}
}

func TestClassifyRPCMuxDiagnosisSinkReloadError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", want: ""},
		{name: "not registered", err: errors.New("sink is not registered"), want: "sink is not registered"},
		{name: "secret resolver", err: errors.New("profileRef requires a secret resolver"), want: "sink profile secret resolver unavailable"},
		{name: "profile ref", err: errors.New("profileRef: missing secret"), want: "sink profile reference resolution failed"},
		{name: "profile", err: errors.New("profile rejected"), want: "sink profile validation failed"},
		{name: "duplicate", err: errors.New("sink is duplicated"), want: "duplicate sink configuration"},
		{name: "maximum", err: errors.New("maximum is 32"), want: "sink count limit exceeded"},
		{name: "panic", err: errors.New("construction panic"), want: "sink construction panic"},
		{name: "fallback", err: errors.New("unknown"), want: "sink generation construction failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyRPCMuxDiagnosisSinkReloadError(test.err); got != test.want {
				t.Fatalf("classification = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRPCMuxDiagnosisSinkSetPriorityAndFailureIsolation(t *testing.T) {
	var orderMu sync.Mutex
	var order []string
	block := make(chan struct{})
	cleanupSlow := RegisterRPCMuxOTelLogSinkProvider("fanout-slow", sinkSetTestProvider{
		newExporter: func(string) RPCMuxOTelLogExporter {
			return &sinkSetTestExporter{export: func(RPCMuxDiagnosisEventOTelLogRecord) {
				orderMu.Lock()
				order = append(order, "slow")
				orderMu.Unlock()
				<-block
			}}
		},
	})
	defer cleanupSlow()
	healthy := make(chan struct{}, 2)
	cleanupHealthy := RegisterRPCMuxOTelLogSinkProvider("fanout-healthy", sinkSetTestProvider{
		newExporter: func(string) RPCMuxOTelLogExporter {
			return &sinkSetTestExporter{export: func(RPCMuxDiagnosisEventOTelLogRecord) {
				orderMu.Lock()
				order = append(order, "healthy")
				orderMu.Unlock()
				healthy <- struct{}{}
			}}
		},
	})
	defer cleanupHealthy()

	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{
		Version: "fanout-v1",
		Sinks: []RPCMuxDiagnosisSinkConfig{
			{
				Name:     "fanout-healthy",
				Priority: 10,
				Delivery: RPCMuxDiagnosisExporterDeliveryConfig{QueueSize: 2, Timeout: time.Second},
			},
			{
				Name:     "fanout-slow",
				Priority: 20,
				Delivery: RPCMuxDiagnosisExporterDeliveryConfig{
					QueueSize:               1,
					Timeout:                 10 * time.Millisecond,
					MaxHungCalls:            2,
					BreakerFailureThreshold: 1,
					BreakerCooldown:         time.Hour,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(block)
		if err := sinkSet.Close(); err != nil {
			t.Error(err)
		}
	}()

	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	receiveSinkSetValue(t, healthy)
	waitForMuxSinkSetSnapshot(t, sinkSet, func(snapshot RPCMuxDiagnosisSinkSetSnapshot) bool {
		return snapshot.Sinks[0].Delivery.TimedOut == 1
	})
	sinkSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	receiveSinkSetValue(t, healthy)
	waitForMuxSinkSetSnapshot(t, sinkSet, func(snapshot RPCMuxDiagnosisSinkSetSnapshot) bool {
		return snapshot.Sinks[1].Delivery.Exported == 2
	})

	snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if snapshot.Sinks[0].Name != "fanout-slow" || snapshot.Sinks[1].Name != "fanout-healthy" {
		t.Fatalf("sink priority order = %+v", snapshot.Sinks)
	}
	if snapshot.Sinks[0].Delivery.Health != "unhealthy" ||
		snapshot.Sinks[0].Delivery.BreakerState != "open" ||
		snapshot.Sinks[0].Delivery.BreakerRejected != 1 {
		t.Fatalf("slow sink delivery = %+v", snapshot.Sinks[0].Delivery)
	}
	if snapshot.Sinks[1].Delivery.Exported != 2 || snapshot.Sinks[1].Delivery.Health != "healthy" {
		t.Fatalf("healthy sink delivery = %+v", snapshot.Sinks[1].Delivery)
	}
}

func TestRPCMuxDiagnosisSinkSetConstructionPanicClosesCandidateAndRollsBack(t *testing.T) {
	var firstClosed atomic.Int64
	cleanupFirst := RegisterRPCMuxOTelLogSinkProvider("panic-first", sinkSetTestProvider{
		newExporter: func(string) RPCMuxOTelLogExporter {
			return &sinkSetTestExporter{closed: &firstClosed}
		},
	})
	defer cleanupFirst()
	cleanupPanic := RegisterRPCMuxOTelLogSinkProvider("panic-second", sinkSetTestProvider{
		newExporter: func(string) RPCMuxOTelLogExporter {
			panic("construction failed")
		},
	})
	defer cleanupPanic()

	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{Version: "stable-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sinkSet.Close()
	err = sinkSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{
		Version: "panic-v2",
		Sinks: []RPCMuxDiagnosisSinkConfig{
			{Name: "panic-first"},
			{Name: "panic-second"},
		},
	})
	if err == nil || err.Error() != "rpc mux diagnosis sink set reload failed: sink construction panic" {
		t.Fatalf("panic reload error = %v", err)
	}
	if firstClosed.Load() != 1 {
		t.Fatalf("partially constructed exporter close count = %d, want 1", firstClosed.Load())
	}
	snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
	if snapshot.Version != "stable-v1" || snapshot.Rollbacks != 1 ||
		snapshot.LastReloadError != "sink construction panic" {
		t.Fatalf("panic rollback snapshot = %+v", snapshot)
	}
}

func TestRPCMuxDiagnosisSinkSetValidationAndLifecycleBoundaries(t *testing.T) {
	if _, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{}); err == nil {
		t.Fatal("empty version succeeded")
	}
	var nilSet *RPCMuxDiagnosisSinkSet
	nilSet.ExportRPCMuxDiagnosisEvent(context.Background(), RPCMuxDiagnosisEventRecord{})
	if snapshot := nilSet.RPCMuxDiagnosisSinkSetSnapshot(); snapshot.Version != "" ||
		snapshot.SinkCount != 0 || len(snapshot.Sinks) != 0 || snapshot.Closed {
		t.Fatalf("nil snapshot = %+v", snapshot)
	}
	if err := nilSet.Close(); err != nil {
		t.Fatal(err)
	}
	if err := nilSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{Version: "v1"}); err == nil {
		t.Fatal("nil sink set reload succeeded")
	}

	sinkSet, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{Version: "empty-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sinkSet.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sinkSet.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sinkSet.Reload(context.Background(), RPCMuxDiagnosisSinkSetConfig{Version: "v2"}); err == nil {
		t.Fatal("reload after close succeeded")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	active, err := NewRPCMuxDiagnosisSinkSet(RPCMuxDiagnosisSinkSetConfig{Version: "active-v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if err := active.Reload(canceled, RPCMuxDiagnosisSinkSetConfig{Version: "v2"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reload = %v, want context canceled", err)
	}
}

func TestValidateRPCMuxDiagnosisSinkSetConfigBoundaries(t *testing.T) {
	cleanup := RegisterRPCMuxOTelLogSinkProvider("validated-set", sinkSetTestProvider{
		validate: func(profile string) error {
			if profile == "invalid" {
				return errors.New("profile rejected")
			}
			return nil
		},
		newExporter: func(string) RPCMuxOTelLogExporter {
			return RPCMuxOTelLogExporterFunc(func(context.Context, RPCMuxDiagnosisEventOTelLogRecord) {})
		},
	})
	defer cleanup()

	tooMany := make([]RPCMuxDiagnosisSinkConfig, maxRPCMuxDiagnosisSinkCount+1)
	for index := range tooMany {
		tooMany[index] = RPCMuxDiagnosisSinkConfig{Name: "validated-set"}
	}
	tests := []struct {
		name   string
		config RPCMuxDiagnosisSinkSetConfig
		want   string
	}{
		{name: "missing version", config: RPCMuxDiagnosisSinkSetConfig{}, want: "version is required"},
		{name: "too many", config: RPCMuxDiagnosisSinkSetConfig{Version: "v1", Sinks: tooMany}, want: "maximum is 32"},
		{name: "empty name", config: RPCMuxDiagnosisSinkSetConfig{Version: "v1", Sinks: []RPCMuxDiagnosisSinkConfig{{}}}, want: "sink name is required"},
		{name: "normalized duplicate", config: RPCMuxDiagnosisSinkSetConfig{Version: "v1", Sinks: []RPCMuxDiagnosisSinkConfig{{Name: "VALIDATED-SET"}, {Name: " validated-set "}}}, want: "is duplicated"},
		{name: "invalid profile", config: RPCMuxDiagnosisSinkSetConfig{Version: "v1", Sinks: []RPCMuxDiagnosisSinkConfig{{Name: "validated-set", Profile: "invalid"}}}, want: "profile rejected"},
		{name: "missing provider", config: RPCMuxDiagnosisSinkSetConfig{Version: "v1", Sinks: []RPCMuxDiagnosisSinkConfig{{Name: "missing-provider"}}}, want: "is not registered"},
		{name: "valid", config: RPCMuxDiagnosisSinkSetConfig{Version: " v1 ", Sinks: []RPCMuxDiagnosisSinkConfig{{Name: " VALIDATED-SET ", Profile: " valid "}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRPCMuxDiagnosisSinkSetConfig(test.config)
			if test.want == "" {
				if err != nil {
					t.Fatalf("ValidateRPCMuxDiagnosisSinkSetConfig: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

type sinkSetTestProvider struct {
	validate    func(string) error
	newExporter func(string) RPCMuxOTelLogExporter
}

func (p sinkSetTestProvider) ValidateRPCMuxOTelLogProfile(profile string) error {
	if p.validate == nil {
		return nil
	}
	return p.validate(profile)
}

func (p sinkSetTestProvider) NewRPCMuxOTelLogExporter(profile string) RPCMuxOTelLogExporter {
	if p.newExporter == nil {
		return nil
	}
	return p.newExporter(profile)
}

type sinkSetTestExporter struct {
	closed *atomic.Int64
	export func(RPCMuxDiagnosisEventOTelLogRecord)
}

func (e *sinkSetTestExporter) ExportRPCMuxOTelLog(_ context.Context, record RPCMuxDiagnosisEventOTelLogRecord) {
	if e.export != nil {
		e.export(record)
	}
}

func (e *sinkSetTestExporter) Close() error {
	if e.closed != nil {
		e.closed.Add(1)
	}
	return nil
}

func receiveSinkSetValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sink value")
		var zero T
		return zero
	}
}

func waitForMuxSinkSetSnapshot(t *testing.T, sinkSet *RPCMuxDiagnosisSinkSet, condition func(RPCMuxDiagnosisSinkSetSnapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := sinkSet.RPCMuxDiagnosisSinkSetSnapshot()
		if condition(snapshot) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("sink set condition not met: %+v", sinkSet.RPCMuxDiagnosisSinkSetSnapshot())
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var _ io.Closer = (*sinkSetTestExporter)(nil)
