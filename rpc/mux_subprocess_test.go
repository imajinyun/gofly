package rpc

import (
	"context"
	"encoding/json"
	"os"
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
	profile := `{"command":"` + os.Args[0] + `","args":["-test.run=TestRPCMuxSubprocessHelperProcess","--"],"timeout":1000000000,"maxOutputBytes":1024}`
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
	if err := provider.ValidateRPCMuxOTelLogProfile(`{"command":""}`); err == nil {
		t.Fatal("empty subprocess command validated")
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
	if len(os.Args) < 3 || os.Args[1] != "-test.run=TestRPCMuxSubprocessHelperProcess" {
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
		Command: os.Args[0],
		Args:    []string{"-test.run=TestRPCMuxSubprocessHelperProcess", "--"},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	exporter.ExportRPCMuxDiagnosisEvent(canceled, RPCMuxDiagnosisEventRecord{})
}
