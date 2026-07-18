// Command custom-mux-sink demonstrates registering a custom OTel-log-compatible
// mux diagnosis sink with the gofly RPC package and wiring it into a mux client.
//
// The demo shows the full application-side extension point:
//
//  1. Register a named sink factory with rpc.RegisterRPCMuxOTelLogSink
//  2. Verify discovery via rpc.RPCMuxOTelLogSinkRegistered
//  3. Build an exporter via rpc.NewRPCMuxOTelLogSinkExporter
//  4. Trigger diagnosis events through ObserveMuxDiagnosis and verify the
//     in-memory exporter receives them with the configured profile tag
//
// Run it:
//
//	go run ./examples/microservices/custom-mux-sink
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/imajinyun/gofly/rpc"
)

const (
	demoSinkName    = "otel-test"
	demoSinkProfile = "demo-custom-sink"
)

// inMemoryOTelLogSink is a thread-safe OTel-log-compatible exporter that
// buffers records in memory. Application-side sinks can forward to any log
// backend (OTel SDK, Loki, Elastic, etc.) without coupling gofly to it.
type inMemoryOTelLogSink struct {
	mu      sync.Mutex
	records []rpc.RPCMuxDiagnosisEventOTelLogRecord
	profile string
}

func (s *inMemoryOTelLogSink) ExportRPCMuxOTelLog(_ context.Context, record rpc.RPCMuxDiagnosisEventOTelLogRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
}

func (s *inMemoryOTelLogSink) snapshot() []rpc.RPCMuxDiagnosisEventOTelLogRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rpc.RPCMuxDiagnosisEventOTelLogRecord, len(s.records))
	copy(out, s.records)
	return out
}

func (s *inMemoryOTelLogSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// sinkDemoReport is the JSON output schema for the demo run.
type sinkDemoReport struct {
	Schema        string   `json:"schema"`
	SinkName      string   `json:"sinkName"`
	Profile       string   `json:"profile"`
	Registered    bool     `json:"registered"`
	EventCount    int      `json:"eventCount"`
	EventFamilies []string `json:"eventFamilies"`
	RecordNames   []string `json:"recordNames"`
	Gates         []string `json:"gates"`
}

func runDemo() sinkDemoReport {
	var sink *inMemoryOTelLogSink

	// Stage 1: register a custom sink factory. In a generated project this
	// would live in cmd/{service}/main.go before config validation so that
	// ValidateRPCMuxConfig can discover it by name.
	cleanup := rpc.RegisterRPCMuxOTelLogSink(demoSinkName, func(profile string) rpc.RPCMuxOTelLogExporter {
		sink = &inMemoryOTelLogSink{profile: profile}
		return sink
	})
	defer cleanup()

	report := sinkDemoReport{
		Schema:   "gofly.custom_mux_sink_demo.v1",
		SinkName: demoSinkName,
		Profile:  demoSinkProfile,
	}

	// Stage 2: verify the registry discovers the custom sink by name.
	report.Registered = rpc.RPCMuxOTelLogSinkRegistered(demoSinkName)
	if !report.Registered {
		report.Gates = append(report.Gates, "FAIL:sink-registry")
		return report
	}
	report.Gates = append(report.Gates, "PASS:sink-registry")

	// Verify case/whitespace-insensitive lookup works.
	if !rpc.RPCMuxOTelLogSinkRegistered("  OTEL-TEST  ") {
		report.Gates = append(report.Gates, "FAIL:sink-name-normalization")
		return report
	}
	report.Gates = append(report.Gates, "PASS:sink-name-normalization")

	// Stage 3: build an exporter for the custom sink via the standard
	// registry constructor. This is the same path generated config code
	// uses in RPCMuxConfig.ClientOptions().
	exporter := rpc.NewRPCMuxOTelLogSinkExporter(demoSinkName, demoSinkProfile)
	if exporter == nil {
		report.Gates = append(report.Gates, "FAIL:sink-exporter-creation")
		return report
	}
	if sink == nil {
		report.Gates = append(report.Gates, "FAIL:sink-factory-not-invoked")
		return report
	}
	if sink.profile != demoSinkProfile {
		report.Gates = append(report.Gates, "FAIL:profile-passthrough")
		return report
	}
	report.Gates = append(report.Gates, "PASS:profile-passthrough")
	report.Gates = append(report.Gates, "PASS:sink-exporter-creation")

	// Stage 4: wire the exporter into an RPC client via the public option,
	// then feed a diagnosis probe through ObserveMuxDiagnosis. In a real
	// application this would come from /rpc/diagnosis or from runtime
	// observation hooks on MuxStream calls.
	filter := rpc.RPCMuxDiagnosisFilter{
		EventFamily: "flow_control",
		Event:       "fragment_window_refill",
	}
	client, err := rpc.NewClient("http://demo-unused",
		rpc.WithMuxDiagnosisEventExporter(exporter, filter),
	)
	if err != nil {
		report.Gates = append(report.Gates, "FAIL:client-creation")
		return report
	}
	defer client.Close()
	report.Gates = append(report.Gates, "PASS:client-creation")

	probe := rpc.RPCDiagnosisProbe{
		Target:  "http://demo-unused",
		Method:  "demo/Ping",
		Matched: true,
		Diagnosis: rpc.RPCDiagnosisSnapshot{Mux: rpc.RPCMuxTransportDiagnosis{
			FlowControl: rpc.RPCMuxFlowControlDiagnosis{
				WriteTimeouts:            1,
				CreditWaitTimeouts:       2,
				FragmentWindowRefills:    3,
				FragmentWindowPolicyRisk: true,
			},
		}},
	}
	client.ObserveMuxDiagnosis(context.Background(), probe)

	// Stage 5: verify the custom sink received filtered events with correct
	// OTel-log shape, profile tag, and event attributes.
	report.EventCount = sink.count()
	if report.EventCount == 0 {
		report.Gates = append(report.Gates, "FAIL:sink-received-events")
		return report
	}
	report.Gates = append(report.Gates, "PASS:sink-received-events")

	// With filter Event=fragment_window_refill, only that event should pass.
	if report.EventCount != 1 {
		report.Gates = append(report.Gates, "FAIL:filter-too-many-events")
		return report
	}
	report.Gates = append(report.Gates, "PASS:event-filter-honored")

	records := sink.snapshot()
	record := records[0]
	if record.Event.Family != "flow_control" || record.Event.Event != "fragment_window_refill" {
		report.Gates = append(report.Gates, "FAIL:wrong-event")
		return report
	}
	if record.Event.Count != 3 {
		report.Gates = append(report.Gates, "FAIL:event-count")
		return report
	}
	report.Gates = append(report.Gates, "PASS:flow-control-event-received")
	report.Gates = append(report.Gates, "PASS:event-counts")

	if record.Name != "rpc.mux.diagnosis_event" {
		report.Gates = append(report.Gates, "FAIL:otel-record-name")
		return report
	}
	report.Gates = append(report.Gates, "PASS:otel-record-name")

	if record.Severity != "WARN" {
		report.Gates = append(report.Gates, "FAIL:otel-severity")
		return report
	}
	report.Gates = append(report.Gates, "PASS:otel-severity")

	hasEventNameAttr := false
	for _, attr := range record.Attributes {
		if attr.Key == "rpc.mux.event.name" && attr.Value == attribute.StringValue("fragment_window_refill") {
			hasEventNameAttr = true
		}
	}
	if !hasEventNameAttr {
		report.Gates = append(report.Gates, "FAIL:otel-attributes")
		return report
	}
	report.Gates = append(report.Gates, "PASS:otel-attributes")

	if record.Timestamp.IsZero() {
		report.Gates = append(report.Gates, "FAIL:otel-timestamp")
		return report
	}
	report.Gates = append(report.Gates, "PASS:otel-timestamp")

	families := make(map[string]bool)
	names := make(map[string]bool)
	for _, r := range records {
		families[r.Event.Family] = true
		names[r.Name] = true
	}
	for fam := range families {
		report.EventFamilies = append(report.EventFamilies, fam)
	}
	sort.Strings(report.EventFamilies)
	for n := range names {
		report.RecordNames = append(report.RecordNames, n)
	}
	sort.Strings(report.RecordNames)

	// Stage 6: verify cleanup restores registry state.
	cleanup()
	if rpc.RPCMuxOTelLogSinkRegistered(demoSinkName) {
		report.Gates = append(report.Gates, "FAIL:sink-cleanup")
		return report
	}
	report.Gates = append(report.Gates, "PASS:sink-cleanup")

	return report
}

func run(args []string, out io.Writer) int {
	flags := flag.NewFlagSet("custom-mux-sink", flag.ContinueOnError)
	flags.SetOutput(out)
	jsonOut := flags.Bool("json", false, "emit JSON report instead of human-readable output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	report := runDemo()

	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return 1
		}
		return 0
	}

	fmt.Fprintln(out, "gofly custom mux OTel log sink demo")
	fmt.Fprintln(out, strings.Repeat("=", 44))
	fmt.Fprintf(out, "  sink name:    %s\n", report.SinkName)
	fmt.Fprintf(out, "  profile:      %s\n", report.Profile)
	fmt.Fprintf(out, "  registered:   %t\n", report.Registered)
	fmt.Fprintf(out, "  event count:  %d\n", report.EventCount)
	if len(report.EventFamilies) > 0 {
		fmt.Fprintf(out, "  event fams:   %v\n", report.EventFamilies)
	}
	if len(report.RecordNames) > 0 {
		fmt.Fprintf(out, "  record names: %v\n", report.RecordNames)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "gates:")
	allPass := true
	for _, g := range report.Gates {
		fmt.Fprintf(out, "  %s\n", g)
		if strings.HasPrefix(g, "FAIL:") {
			allPass = false
		}
	}
	fmt.Fprintln(out)
	if allPass {
		fmt.Fprintln(out, "all gates passed — custom sink extension point works end-to-end.")
	} else {
		fmt.Fprintln(out, "some gates failed — see above.")
		return 1
	}

	_ = time.Second // referenced for import hygiene
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}
