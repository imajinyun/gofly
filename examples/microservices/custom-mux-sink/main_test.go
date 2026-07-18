package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunDemoGates(t *testing.T) {
	report := runDemo()

	if report.Schema != "gofly.custom_mux_sink_demo.v1" {
		t.Fatalf("schema = %q, want gofly.custom_mux_sink_demo.v1", report.Schema)
	}
	if report.SinkName != "otel-test" {
		t.Fatalf("sink name = %q, want otel-test", report.SinkName)
	}
	if !report.Registered {
		t.Fatal("custom otel-test sink should be registered")
	}
	if report.EventCount != 1 {
		t.Fatalf("event count = %d, want 1 (filtered to fragment_window_refill)", report.EventCount)
	}

	foundFlowControl := false
	for _, fam := range report.EventFamilies {
		if fam == "flow_control" {
			foundFlowControl = true
		}
	}
	if !foundFlowControl {
		t.Fatalf("event families = %v, want flow_control", report.EventFamilies)
	}

	for _, g := range report.Gates {
		if strings.HasPrefix(g, "FAIL:") {
			t.Fatalf("gate failed: %s\nfull report: %+v", g, report)
		}
	}
}

func TestRunDemoJSONContract(t *testing.T) {
	report := runDemo()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var decoded sinkDemoReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if decoded.Schema != report.Schema ||
		decoded.SinkName != report.SinkName ||
		decoded.Profile != report.Profile ||
		decoded.EventCount != report.EventCount {
		t.Fatalf("json round-trip mismatch: got %+v, want %+v", decoded, report)
	}
}

func TestRunCLIContracts(t *testing.T) {
	var text bytes.Buffer
	if code := run(nil, &text); code != 0 {
		t.Fatalf("text run exit code = %d\n%s", code, text.String())
	}
	for _, want := range []string{
		"gofly custom mux OTel log sink demo",
		"sink name:    otel-test",
		"profile:      demo-custom-sink",
		"event fams:   [flow_control]",
		"record names: [rpc.mux.diagnosis_event]",
		"PASS:sink-registry",
		"all gates passed",
	} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text output missing %q:\n%s", want, text.String())
		}
	}

	var jsonOut bytes.Buffer
	if code := run([]string{"--json"}, &jsonOut); code != 0 {
		t.Fatalf("json run exit code = %d\n%s", code, jsonOut.String())
	}
	var report sinkDemoReport
	if err := json.Unmarshal(jsonOut.Bytes(), &report); err != nil {
		t.Fatalf("decode CLI JSON: %v\n%s", err, jsonOut.String())
	}
	if report.Schema != "gofly.custom_mux_sink_demo.v1" ||
		report.SinkName != demoSinkName ||
		report.Profile != demoSinkProfile ||
		report.EventCount != 1 {
		t.Fatalf("CLI JSON report = %+v", report)
	}

	var usage bytes.Buffer
	if code := run([]string{"--unknown"}, &usage); code != 2 {
		t.Fatalf("invalid flag exit code = %d, want 2", code)
	}
	if !strings.Contains(usage.String(), "flag provided but not defined") {
		t.Fatalf("invalid flag output = %q", usage.String())
	}
}

func TestRunCLIWriterFailure(t *testing.T) {
	if code := run([]string{"--json"}, failingWriter{}); code != 1 {
		t.Fatalf("writer failure exit code = %d, want 1", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, bytes.ErrTooLarge
}
