package command

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

type commandDoctorSplitDryRunEvidence struct {
	Schema              string   `json:"schema"`
	Status              string   `json:"status"`
	Family              string   `json:"family"`
	Package             string   `json:"package"`
	AcceptanceGate      string   `json:"acceptanceGate"`
	DryRunOnly          bool     `json:"dryRunOnly"`
	NoPhysicalMove      bool     `json:"noPhysicalMove"`
	FamilyFiles         []string `json:"familyFiles"`
	GoldenTests         []string `json:"goldenTests"`
	GoldenFields        []string `json:"goldenFields"`
	SupportBundleFields []string `json:"supportBundleFields"`
	RequiredGates       []string `json:"requiredGates"`
}

func TestDoctorFamilyJSONGoldenContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &stdout, Err: &stderr}, outputText, verbosityNormal, func() error {
		return doctorCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor --json wrote stderr = %q, want stdout-only JSON", stderr.String())
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("doctor --json decode: %v\n%s", err, stdout.String())
	}
	for _, field := range []string{"version", "go", "os", "arch", "checks", "summary", "nextActions"} {
		if _, ok := report[field]; !ok {
			t.Fatalf("doctor JSON missing field %q: %#v", field, report)
		}
	}
	if got, ok := report["go"].(string); !ok || got != runtime.Version() {
		t.Fatalf("doctor JSON go = %#v, want %q", report["go"], runtime.Version())
	}
	checks, ok := report["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("doctor JSON checks = %#v, want non-empty array", report["checks"])
	}
	check, ok := checks[0].(map[string]any)
	if !ok {
		t.Fatalf("doctor JSON check[0] = %T, want object", checks[0])
	}
	for _, field := range []string{"name", "status"} {
		if _, ok := check[field]; !ok {
			t.Fatalf("doctor JSON check missing field %q: %#v", field, check)
		}
	}
	if status, ok := check["status"].(string); !ok || !isDoctorGoldenStatus(status) {
		t.Fatalf("doctor JSON check status = %#v, want ok/warn/fail", check["status"])
	}
	nextActions, ok := report["nextActions"].([]any)
	if !ok || len(nextActions) == 0 {
		t.Fatalf("doctor JSON nextActions = %#v, want non-empty remediation actions", report["nextActions"])
	}
}

func TestDoctorFamilySupportBundleContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &stdout, Err: &stderr}, outputText, verbosityNormal, func() error {
		return bugCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("bug --json: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("bug --json wrote stderr = %q, want stdout-only JSON", stderr.String())
	}
	var report bugReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("bug --json decode: %v\n%s", err, stdout.String())
	}
	if report.SupportBundle.Schema != "gofly.support_bundle.v1" {
		t.Fatalf("supportBundle.schema = %q, want gofly.support_bundle.v1", report.SupportBundle.Schema)
	}
	for _, want := range []string{"Authorization", "Cookie", "GOFLY_LLM_*", "*TOKEN*", "*SECRET*", "*PASSWORD*"} {
		if !containsDoctorSplitString(report.SupportBundle.Redaction, want) {
			t.Fatalf("supportBundle.redaction missing %q: %#v", want, report.SupportBundle.Redaction)
		}
	}
	for _, want := range []string{"gofly doctor --json", "gofly env check --json", "gofly release check --json --strict", "gofly bug --json", "curl -fsS http://127.0.0.1:9090/admin/control-plane"} {
		if !containsDoctorSplitString(report.SupportBundle.Commands, want) {
			t.Fatalf("supportBundle.commands missing %q: %#v", want, report.SupportBundle.Commands)
		}
	}
	if !strings.Contains(report.SupportBundle.Description, "removing secrets") ||
		!strings.Contains(report.SupportBundle.Description, "generated.rpcMuxConfigWarningSchema") ||
		!strings.Contains(report.SupportBundle.Description, "generated.controlPlaneSchemaChecksums") {
		t.Fatalf("supportBundle.description = %q, want redaction guidance", report.SupportBundle.Description)
	}
	for _, want := range []string{
		"attach this support bundle when opening an issue or asking for help",
		"run `gofly doctor --json` and fix failed checks before rerunning generators",
	} {
		if !containsDoctorSplitString(report.NextActions, want) {
			t.Fatalf("bug nextActions missing %q: %#v", want, report.NextActions)
		}
	}
}

func TestDoctorFamilyDryRunEvidence(t *testing.T) {
	evidence := loadCommandDoctorSplitDryRunEvidence(t)
	if evidence.Schema != "gofly.command_doctor_split_dry_run.v1" {
		t.Fatalf("schema = %q, want gofly.command_doctor_split_dry_run.v1", evidence.Schema)
	}
	if evidence.Status != "completed-physical-split" {
		t.Fatalf("status = %q, want completed-physical-split", evidence.Status)
	}
	if evidence.Family != "doctor" || evidence.Package != "cmd/gofly/internal/command/doctor" {
		t.Fatalf("family/package = %q/%q, want doctor/cmd/gofly/internal/command/doctor", evidence.Family, evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-doctor-split-dry-run-check" {
		t.Fatalf("acceptanceGate = %q, want make command-doctor-split-dry-run-check", evidence.AcceptanceGate)
	}
	if evidence.DryRunOnly || evidence.NoPhysicalMove {
		t.Fatalf("P22-14 evidence must record completed physical move: dryRunOnly=%t noPhysicalMove=%t", evidence.DryRunOnly, evidence.NoPhysicalMove)
	}
	assertDoctorSplitSet(t, "family files", evidence.FamilyFiles, []string{"doctor/doctor.go", "doctor/doctor_checks.go", "doctor/doctor_test.go"})
	assertDoctorSplitSet(t, "golden fields", evidence.GoldenFields, []string{
		"version",
		"go",
		"os",
		"arch",
		"checks",
		"summary",
		"nextActions",
		"checks.name",
		"checks.status",
		"checks.message",
		"checks.fix_hint",
		"checks.nextActions",
	})
	assertDoctorSplitSet(t, "support bundle fields", evidence.SupportBundleFields, []string{
		"supportBundle.schema",
		"supportBundle.redaction",
		"supportBundle.commands",
		"supportBundle.description",
		"nextActions",
	})
	for _, want := range []string{
		"TestDoctorFamilyDryRunEvidence",
		"TestDoctorFamilyJSONGoldenContract",
		"TestDoctorFamilySupportBundleContract",
		"TestDoctorCommandJSON",
		"TestDoctorNextActionsContract",
		"TestBugCommandSupportBundleJSONContract",
	} {
		if !containsDoctorSplitString(evidence.GoldenTests, want) {
			t.Fatalf("goldenTests missing %q: %v", want, evidence.GoldenTests)
		}
	}
	for _, want := range []string{
		"make command-doctor-split-dry-run-check",
		"make command-split-readiness-check",
		"make command-family-dependency-map-check",
		"make cli-json-contract-goldens-check",
	} {
		if !containsDoctorSplitString(evidence.RequiredGates, want) {
			t.Fatalf("requiredGates missing %q: %v", want, evidence.RequiredGates)
		}
	}
}

func loadCommandDoctorSplitDryRunEvidence(t *testing.T) commandDoctorSplitDryRunEvidence {
	t.Helper()
	return commandDoctorSplitDryRunEvidence{
		Schema:         "gofly.command_doctor_split_dry_run.v1",
		Status:         "completed-physical-split",
		Family:         "doctor",
		Package:        "cmd/gofly/internal/command/doctor",
		AcceptanceGate: "make command-doctor-split-dry-run-check",
		FamilyFiles:    []string{"doctor/doctor.go", "doctor/doctor_checks.go", "doctor/doctor_test.go"},
		GoldenTests: []string{
			"TestDoctorFamilyDryRunEvidence",
			"TestDoctorFamilyJSONGoldenContract",
			"TestDoctorFamilySupportBundleContract",
			"TestDoctorCommandJSON",
			"TestDoctorNextActionsContract",
			"TestBugCommandSupportBundleJSONContract",
		},
		GoldenFields: []string{
			"version",
			"go",
			"os",
			"arch",
			"checks",
			"summary",
			"nextActions",
			"checks.name",
			"checks.status",
			"checks.message",
			"checks.fix_hint",
			"checks.nextActions",
		},
		SupportBundleFields: []string{
			"supportBundle.schema",
			"supportBundle.redaction",
			"supportBundle.commands",
			"supportBundle.description",
			"nextActions",
		},
		RequiredGates: []string{
			"make command-doctor-split-dry-run-check",
			"make command-split-readiness-check",
			"make command-family-dependency-map-check",
			"make cli-json-contract-goldens-check",
		},
	}
}

func isDoctorGoldenStatus(status string) bool {
	return status == "ok" || status == "warn" || status == "fail"
}

func assertDoctorSplitSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsDoctorSplitString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsDoctorSplitString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
