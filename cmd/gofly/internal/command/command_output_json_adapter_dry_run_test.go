package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type commandOutputJSONAdapterDryRunEvidence struct {
	Schema                 string                                  `json:"schema"`
	Status                 string                                  `json:"status"`
	Family                 string                                  `json:"family"`
	Package                string                                  `json:"package"`
	AcceptanceGate         string                                  `json:"acceptanceGate"`
	DryRunOnly             bool                                    `json:"dryRunOnly"`
	NoPhysicalMove         bool                                    `json:"noPhysicalMove"`
	SourceFiles            []string                                `json:"sourceFiles"`
	AdapterContracts       []string                                `json:"adapterContracts"`
	GoldenTests            []string                                `json:"goldenTests"`
	RequiredGates          []string                                `json:"requiredGates"`
	PhysicalSplitAdmission commandOutputJSONAdapterDryRunAdmission `json:"physicalSplitAdmission"`
}

type commandOutputJSONAdapterDryRunAdmission struct {
	Status              string `json:"status"`
	NextAllowedAction   string `json:"nextAllowedAction"`
	RollbackRequirement string `json:"rollbackRequirement"`
}

func TestCommandOutputJSONAdapterDryRunEvidence(t *testing.T) {
	evidence := loadCommandOutputJSONAdapterDryRunEvidence(t)
	if evidence.Schema != "gofly.command_output_json_adapter_dry_run.v1" {
		t.Fatalf("schema = %q, want gofly.command_output_json_adapter_dry_run.v1", evidence.Schema)
	}
	if evidence.Status != "completed-preflight" {
		t.Fatalf("status = %q, want completed-preflight", evidence.Status)
	}
	if evidence.Family != "shared" || evidence.Package != "cmd/gofly/internal/command" {
		t.Fatalf("family/package = %q/%q, want shared/cmd/gofly/internal/command", evidence.Family, evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-output-json-adapter-dry-run-check" {
		t.Fatalf("acceptanceGate = %q, want make command-output-json-adapter-dry-run-check", evidence.AcceptanceGate)
	}
	if !evidence.DryRunOnly || !evidence.NoPhysicalMove {
		t.Fatalf("adapter dry-run must not authorize a physical move: dryRunOnly=%t noPhysicalMove=%t", evidence.DryRunOnly, evidence.NoPhysicalMove)
	}
	assertOutputJSONAdapterSet(t, "source files", evidence.SourceFiles, []string{
		"io.go",
		"output_flags.go",
		"json_error.go",
		"json_error_writer.go",
		"json_error_classify.go",
		"doctor_adapter.go",
		"bug.go",
	})
	assertOutputJSONAdapterSet(t, "golden tests", evidence.GoldenTests, []string{
		"TestCommandOutputJSONAdapterDryRunEvidence",
		"TestCommandOutputAdapterContract",
		"TestCommandJSONAdapterContract",
		"TestCommandOutputJSONAdapterDoctorAndBugContracts",
	})
	assertOutputJSONAdapterSet(t, "required gates", evidence.RequiredGates, []string{
		"make command-output-json-adapter-dry-run-check",
		"make command-shared-reduction-plan-check",
		"make command-split-readiness-check",
		"make command-family-dependency-map-check",
		"make project-layout-governance-check",
		"make cli-command-surface-check",
		"make cli-json-contract-goldens-check",
		"GOCACHE=$PWD/.tmp-test/gocache GOTMPDIR=$PWD/.tmp-test/gotmp go test -shuffle=on ./cmd/gofly/internal/command -run 'TestCommandOutput(JSONAdapterDryRunEvidence|AdapterContract)|TestCommandJSONAdapterContract|TestCommandOutputJSONAdapterDoctorAndBugContracts' -count=1",
		"git diff --check",
	})
	if evidence.PhysicalSplitAdmission.Status != "candidate-for-help-doctor-preflight" {
		t.Fatalf("physicalSplitAdmission.status = %q, want candidate-for-help-doctor-preflight", evidence.PhysicalSplitAdmission.Status)
	}
}

func TestCommandOutputAdapterContract(t *testing.T) {
	previousMode := OutputMode()
	var stdout, stderr bytes.Buffer
	err := withCommandIO(IOStreams{Out: &stdout, Err: &stderr}, outputJSON, verbosityVerbose, func() error {
		cliOutput("visible")
		verboseOutputf("diagnostic")
		if OutputMode() != outputJSON {
			t.Fatalf("OutputMode() = %q, want json", OutputMode())
		}
		if currentOut() != &stdout || currentErr() != &stderr {
			t.Fatal("withCommandIO did not install stdout/stderr writers")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withCommandIO verbose contract: %v", err)
	}
	if stdout.String() != "visible" || stderr.String() != "diagnostic" {
		t.Fatalf("output contract stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
	if got := OutputMode(); got != previousMode {
		t.Fatalf("withCommandIO did not restore output mode: got %q want %q", got, previousMode)
	}

	stdout.Reset()
	stderr.Reset()
	err = withCommandIO(IOStreams{Out: &stdout, Err: &stderr}, outputText, verbosityQuiet, func() error {
		cliOutputIf("hidden")
		errorf("error")
		return nil
	})
	if err != nil {
		t.Fatalf("withCommandIO quiet contract: %v", err)
	}
	if stdout.Len() != 0 || stderr.String() != "error" {
		t.Fatalf("quiet contract stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
}

func TestCommandJSONAdapterContract(t *testing.T) {
	var stdout bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &stdout}, outputJSON, verbosityNormal, func() error {
		return printJSONEnvelope("adapter.test", map[string]string{"ok": "yes"})
	}); err != nil {
		t.Fatalf("printJSONEnvelope: %v", err)
	}
	var envelope struct {
		OK      bool              `json:"ok"`
		Command string            `json:"command"`
		Version string            `json:"version"`
		Data    map[string]string `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode printJSONEnvelope output: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.Command != "adapter.test" || envelope.Version != Version || envelope.Data["ok"] != "yes" {
		t.Fatalf("printJSONEnvelope contract = %+v", envelope)
	}

	stdout.Reset()
	if err := withCommandIO(IOStreams{Out: &stdout}, outputJSON, verbosityNormal, func() error {
		return printJSONError("adapter.test", fmtUsageErrorForAdapterDryRun())
	}); err != nil {
		t.Fatalf("printJSONError: %v", err)
	}
	var errorEnvelope struct {
		OK          bool       `json:"ok"`
		Command     string     `json:"command"`
		Version     string     `json:"version"`
		Error       *jsonError `json:"error"`
		NextActions []string   `json:"nextActions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &errorEnvelope); err != nil {
		t.Fatalf("decode printJSONError output: %v\n%s", err, stdout.String())
	}
	if errorEnvelope.OK || errorEnvelope.Command != "adapter.test" || errorEnvelope.Version != Version || errorEnvelope.Error == nil {
		t.Fatalf("printJSONError envelope = %+v", errorEnvelope)
	}
	if errorEnvelope.Error.Code != "USAGE_ERROR" || errorEnvelope.Error.Retryable {
		t.Fatalf("printJSONError classified error = %+v", errorEnvelope.Error)
	}
	if !strings.Contains(errorEnvelope.Error.Remediation, "Check command usage") {
		t.Fatalf("printJSONError remediation = %q", errorEnvelope.Error.Remediation)
	}

	stdout.Reset()
	WriteErrorJSON(&stdout, fmtUsageErrorForAdapterDryRun())
	var legacy struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &legacy); err != nil {
		t.Fatalf("decode WriteErrorJSON output: %v\n%s", err, stdout.String())
	}
	if legacy.Error.Code != "USAGE_ERROR" || !strings.Contains(legacy.Error.Message, "bad adapter flag") {
		t.Fatalf("WriteErrorJSON contract = %+v", legacy)
	}
}

func TestCommandOutputJSONAdapterDoctorAndBugContracts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &stdout, Err: &stderr}, outputText, verbosityNormal, func() error {
		return doctorCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor --json wrote stderr = %q, want stdout-only JSON", stderr.String())
	}
	var doctorReport doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &doctorReport); err != nil {
		t.Fatalf("doctor --json decode: %v\n%s", err, stdout.String())
	}
	if len(doctorReport.NextActions) == 0 {
		t.Fatalf("doctor --json nextActions = %#v, want stable remediation guidance", doctorReport.NextActions)
	}

	stdout.Reset()
	stderr.Reset()
	if err := withCommandIO(IOStreams{Out: &stdout, Err: &stderr}, outputText, verbosityNormal, func() error {
		return bugCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("bug --json: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("bug --json wrote stderr = %q, want stdout-only JSON", stderr.String())
	}
	var bugReport bugReport
	if err := json.Unmarshal(stdout.Bytes(), &bugReport); err != nil {
		t.Fatalf("bug --json decode: %v\n%s", err, stdout.String())
	}
	if bugReport.SupportBundle.Schema != "gofly.support_bundle.v1" {
		t.Fatalf("supportBundle.schema = %q", bugReport.SupportBundle.Schema)
	}
	if !containsOutputJSONAdapterString(bugReport.SupportBundle.Redaction, "Authorization") ||
		!containsOutputJSONAdapterString(bugReport.SupportBundle.Redaction, "Cookie") {
		t.Fatalf("supportBundle.redaction = %#v, want sensitive header guidance", bugReport.SupportBundle.Redaction)
	}
}

func fmtUsageErrorForAdapterDryRun() error {
	return errors.Join(errUsage, errors.New("bad adapter flag"))
}

func loadCommandOutputJSONAdapterDryRunEvidence(t *testing.T) commandOutputJSONAdapterDryRunEvidence {
	t.Helper()
	return commandOutputJSONAdapterDryRunEvidence{
		Schema:         "gofly.command_output_json_adapter_dry_run.v1",
		Status:         "completed-preflight",
		Family:         "shared",
		Package:        "cmd/gofly/internal/command",
		AcceptanceGate: "make command-output-json-adapter-dry-run-check",
		DryRunOnly:     true,
		NoPhysicalMove: true,
		SourceFiles: []string{
			"io.go",
			"output_flags.go",
			"json_error.go",
			"json_error_writer.go",
			"json_error_classify.go",
			"doctor_adapter.go",
			"bug.go",
		},
		AdapterContracts: []string{
			"withCommandIO restores output mode and stdout/stderr writers",
			"printJSONEnvelope emits a single stdout JSON object",
			"printJSONError classifies usage errors",
			"doctor --json and bug --json stay stdout-only",
		},
		GoldenTests: []string{
			"TestCommandOutputJSONAdapterDryRunEvidence",
			"TestCommandOutputAdapterContract",
			"TestCommandJSONAdapterContract",
			"TestCommandOutputJSONAdapterDoctorAndBugContracts",
		},
		RequiredGates: []string{
			"make command-output-json-adapter-dry-run-check",
			"make command-shared-reduction-plan-check",
			"make command-split-readiness-check",
			"make command-family-dependency-map-check",
			"make project-layout-governance-check",
			"make cli-command-surface-check",
			"make cli-json-contract-goldens-check",
			"GOCACHE=$PWD/.tmp-test/gocache GOTMPDIR=$PWD/.tmp-test/gotmp go test -shuffle=on ./cmd/gofly/internal/command -run 'TestCommandOutput(JSONAdapterDryRunEvidence|AdapterContract)|TestCommandJSONAdapterContract|TestCommandOutputJSONAdapterDoctorAndBugContracts' -count=1",
			"git diff --check",
		},
		PhysicalSplitAdmission: commandOutputJSONAdapterDryRunAdmission{
			Status:              "candidate-for-help-doctor-preflight",
			NextAllowedAction:   "split one command family only after output and JSON boundaries stay covered",
			RollbackRequirement: "restore shared adapter files before retrying a command family move",
		},
	}
}

func assertOutputJSONAdapterSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsOutputJSONAdapterString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsOutputJSONAdapterString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
