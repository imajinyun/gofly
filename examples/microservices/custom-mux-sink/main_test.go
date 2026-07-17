package main

import (
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
