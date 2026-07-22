package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRPCMuxSubprocessExporterProvider(t *testing.T) {
	provider := rpcMuxSubprocessOTelLogSinkProvider{}
	schema := provider.RPCMuxOTelLogProfileSchema()
	if !json.Valid(schema) {
		t.Fatalf("subprocess profile schema is invalid: %s", schema)
	}
	workRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(workRoot, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	profile := `{"command":"` + os.Args[0] + `","args":["-test.run=TestRPCMuxSubprocessHelperProcess","--"],"timeout":5000000000,"maxOutputBytes":1024,"workDir":"work","workDirRoot":"` + workRoot + `","allowCommands":["` + os.Args[0] + `"],"env":{"GOFLY_MUX_TEST":"1","GOFLY_MUX_HELPER":"1"},"envWhitelist":["GOFLY_MUX_TEST","GOFLY_MUX_HELPER"]}`
	if err := provider.ValidateRPCMuxOTelLogProfile(profile); err != nil {
		t.Fatalf("ValidateRPCMuxOTelLogProfile: %v", err)
	}
	exporter := provider.NewRPCMuxOTelLogExporter(profile)
	if exporter == nil {
		t.Fatal("subprocess provider returned nil exporter")
	}
	exporter.ExportRPCMuxOTelLog(context.Background(), RPCMuxDiagnosisEventOTelLogRecord{
		Timestamp: time.Now(),
		Event:     RPCMuxDiagnosisEvent{Family: "flow_control", Event: "write_timeout"},
	})
	subprocessSnapshot := exporter.(RPCMuxSubprocessExporterSnapshotter).RPCMuxSubprocessExporterSnapshot()
	if subprocessSnapshot.Runs != 1 || subprocessSnapshot.Command != os.Args[0] ||
		subprocessSnapshot.LastExitCode != 0 || subprocessSnapshot.LastDuration <= 0 ||
		subprocessSnapshot.LastRunAt.IsZero() || subprocessSnapshot.LastOutputTruncated {
		t.Fatalf("subprocess snapshot = %+v", subprocessSnapshot)
	}
	delivery := newGovernedRPCMuxDiagnosisEventExporter("subprocess-snapshot", NewRPCMuxOTelLogDiagnosisEventExporter(exporter), RPCMuxDiagnosisExporterDeliveryConfig{
		QueueSize: 1,
		Timeout:   5 * time.Second,
	}).(*governedRPCMuxDiagnosisExporter)
	defer delivery.Close()
	if got := delivery.RPCMuxDiagnosisExporterDeliverySnapshot(); got.Subprocess == nil || got.Subprocess.Command != os.Args[0] {
		t.Fatalf("delivery subprocess snapshot = %+v", got.Subprocess)
	}
	if err := provider.ValidateRPCMuxOTelLogProfile(`{"command":""}`); err == nil {
		t.Fatal("empty subprocess command validated")
	}
	if err := provider.ValidateRPCMuxOTelLogProfile(`{"command":"` + os.Args[0] + `","allowCommands":["/bin/other"]}`); err == nil {
		t.Fatal("non-allowlisted subprocess command validated")
	}
	if err := provider.ValidateRPCMuxOTelLogProfile(`{"command":"` + os.Args[0] + `","denyCommands":["` + os.Args[0] + `"]}`); err == nil {
		t.Fatal("denylisted subprocess command validated")
	}
	if err := provider.ValidateRPCMuxOTelLogProfile(`{"command":"` + os.Args[0] + `","workDir":"../escape","workDirRoot":"` + workRoot + `"}`); err == nil {
		t.Fatal("escaping subprocess workDir validated")
	}
	if err := provider.ValidateRPCMuxOTelLogProfile(`{"command":"` + os.Args[0] + `","env":{"SECRET":"x"}}`); err == nil {
		t.Fatal("env without whitelist validated")
	}
	if err := provider.ValidateRPCMuxOTelLogProfile(`{"command":"` + os.Args[0] + `","env":{"SECRET":"x"},"envWhitelist":["OTHER"]}`); err == nil {
		t.Fatal("non-whitelisted env validated")
	}
	if err := provider.ValidateRPCMuxOTelLogProfile(`{`); err == nil {
		t.Fatal("invalid subprocess profile JSON validated")
	}
	if exporter := provider.NewRPCMuxOTelLogExporter(`{"command":""}`); exporter != nil {
		t.Fatalf("bad subprocess profile exporter = %#v, want nil", exporter)
	}
	if exporter := provider.NewRPCMuxOTelLogExporter(`{`); exporter != nil {
		t.Fatalf("invalid JSON subprocess exporter = %#v, want nil", exporter)
	}
	(rpcMuxSubprocessOTelLogExporter{}).ExportRPCMuxOTelLog(context.Background(), RPCMuxDiagnosisEventOTelLogRecord{})
}

func TestRPCMuxSubprocessHelperProcess(t *testing.T) {
	if os.Getenv("GOFLY_MUX_HELPER") != "1" {
		return
	}
	var record RPCMuxDiagnosisEventRecord
	if err := json.NewDecoder(os.Stdin).Decode(&record); err != nil {
		os.Exit(2)
	}
	if record.Event.Event != "write_timeout" {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestNormalizeRPCMuxSubprocessExporterConfig(t *testing.T) {
	if _, err := NewRPCMuxSubprocessDiagnosisEventExporter(RPCMuxSubprocessExporterConfig{}); err == nil {
		t.Fatal("empty subprocess config validated")
	}
	if _, err := NewRPCMuxSubprocessDiagnosisEventExporter(RPCMuxSubprocessExporterConfig{Command: "bad\ncmd"}); err == nil {
		t.Fatal("newline command validated")
	}
	args := make([]string, defaultRPCMuxSubprocessMaxArgs+1)
	if _, err := NewRPCMuxSubprocessDiagnosisEventExporter(RPCMuxSubprocessExporterConfig{Command: os.Args[0], Args: args}); err == nil {
		t.Fatal("too many subprocess args validated")
	}
	if _, err := NewRPCMuxSubprocessDiagnosisEventExporter(RPCMuxSubprocessExporterConfig{Command: os.Args[0], Args: []string{"bad\narg"}}); err == nil {
		t.Fatal("newline arg validated")
	}
	if _, err := NewRPCMuxSubprocessDiagnosisEventExporter(RPCMuxSubprocessExporterConfig{
		Command:     os.Args[0],
		WorkDir:     "work",
		WorkDirRoot: "relative",
	}); err == nil {
		t.Fatal("relative workDirRoot validated")
	}
	if _, err := NewRPCMuxSubprocessDiagnosisEventExporter(RPCMuxSubprocessExporterConfig{
		Command:      os.Args[0],
		Env:          map[string]string{"BAD=KEY": "x"},
		EnvWhitelist: []string{"BAD=KEY"},
	}); err == nil {
		t.Fatal("bad env key validated")
	}
	if _, err := NewRPCMuxSubprocessDiagnosisEventExporter(RPCMuxSubprocessExporterConfig{
		Command:      os.Args[0],
		Env:          map[string]string{"BAD": "x\x00"},
		EnvWhitelist: []string{"BAD"},
	}); err == nil {
		t.Fatal("bad env value validated")
	}
	if got := subprocessEnvList(map[string]string{"A": "1"}); len(got) != 1 || got[0] != "A=1" {
		t.Fatalf("subprocess env list = %+v", got)
	}
}

func TestLimitWriter(t *testing.T) {
	var b strings.Builder
	writer := &limitWriter{w: &b, max: 3}
	if n, err := writer.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("first write n=%d err=%v", n, err)
	}
	if n, err := writer.Write([]byte("zzz")); err != nil || n != 3 {
		t.Fatalf("second write n=%d err=%v", n, err)
	}
	if got := b.String(); got != "abc" {
		t.Fatalf("limited output = %q, want abc", got)
	}
	var unlimited strings.Builder
	if n, err := (&limitWriter{w: &unlimited}).Write([]byte("all")); err != nil || n != 3 || unlimited.String() != "" {
		t.Fatalf("unlimited disabled writer n=%d err=%v out=%q", n, err, unlimited.String())
	}
	if n, err := (&limitWriter{w: &unlimited, max: 1, n: 1}).Write([]byte("x")); err != nil || n != 1 {
		t.Fatalf("full writer n=%d err=%v", n, err)
	}
	if n, err := (*limitWriter)(nil).Write([]byte("x")); err != nil || n != 1 {
		t.Fatalf("nil writer n=%d err=%v", n, err)
	}
}

func TestRPCMuxSubprocessExporterHonorsContext(t *testing.T) {
	exporter, err := NewRPCMuxSubprocessDiagnosisEventExporter(RPCMuxSubprocessExporterConfig{
		Command:      os.Args[0],
		Args:         []string{"-test.run=TestRPCMuxSubprocessHelperProcess", "--"},
		Timeout:      time.Second,
		Env:          map[string]string{"GOFLY_MUX_HELPER": "1"},
		EnvWhitelist: []string{"GOFLY_MUX_HELPER"},
	})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	exporter.ExportRPCMuxDiagnosisEvent(canceled, RPCMuxDiagnosisEventRecord{})
	snapshot := exporter.(*rpcMuxSubprocessExporter).RPCMuxSubprocessExporterSnapshot()
	if snapshot.Runs != 1 || snapshot.LastExitCode != -1 || snapshot.LastDuration <= 0 || snapshot.Command != os.Args[0] {
		t.Fatalf("subprocess snapshot = %+v", snapshot)
	}
	(*rpcMuxSubprocessExporter)(nil).recordRun(0, 0, false, false, nil)
	if got := (*rpcMuxSubprocessExporter)(nil).RPCMuxSubprocessExporterSnapshot(); got.Command != "" {
		t.Fatalf("nil subprocess snapshot = %+v", got)
	}
}
