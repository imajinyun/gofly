package command

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandHelpDoctorSplitPreflightEvidence struct {
	Schema                   string                                   `json:"schema"`
	Status                   string                                   `json:"status"`
	Package                  string                                   `json:"package"`
	AcceptanceGate           string                                   `json:"acceptanceGate"`
	DryRunOnly               bool                                     `json:"dryRunOnly"`
	NoPhysicalMove           bool                                     `json:"noPhysicalMove"`
	HelpPhysicalSplitDone    bool                                     `json:"helpPhysicalSplitDone"`
	HelpPackage              string                                   `json:"helpPackage"`
	CommandAdapter           string                                   `json:"commandAdapter"`
	DoctorPhysicalSplitDone  bool                                     `json:"doctorPhysicalSplitDone"`
	DoctorPackage            string                                   `json:"doctorPackage"`
	DoctorCommandAdapter     string                                   `json:"doctorCommandAdapter"`
	SelectedNextFamily       string                                   `json:"selectedNextFamily"`
	DeferredNextFamily       string                                   `json:"deferredNextFamily"`
	DoctorPreflightRefreshed bool                                     `json:"doctorPreflightRefreshed"`
	HelpFiles                []string                                 `json:"helpFiles"`
	DoctorFiles              []string                                 `json:"doctorFiles"`
	PreflightContracts       []string                                 `json:"preflightContracts"`
	GoldenTests              []string                                 `json:"goldenTests"`
	RequiredGates            []string                                 `json:"requiredGates"`
	PhysicalSplitAdmission   commandHelpDoctorSplitPreflightAdmission `json:"physicalSplitAdmission"`
}

type commandHelpDoctorSplitPreflightAdmission struct {
	Status              string `json:"status"`
	NextAllowedAction   string `json:"nextAllowedAction"`
	BlockedAction       string `json:"blockedAction"`
	RollbackRequirement string `json:"rollbackRequirement"`
}

func TestCommandHelpDoctorSplitPreflightEvidence(t *testing.T) {
	evidence := loadCommandHelpDoctorSplitPreflightEvidence(t)
	if evidence.Schema != "gofly.command_help_doctor_split_preflight.v1" {
		t.Fatalf("schema = %q, want gofly.command_help_doctor_split_preflight.v1", evidence.Schema)
	}
	if evidence.Status != "help-and-doctor-physical-split-completed" {
		t.Fatalf("status = %q, want help-and-doctor-physical-split-completed", evidence.Status)
	}
	if evidence.Package != "cmd/gofly/internal/command" {
		t.Fatalf("package = %q, want cmd/gofly/internal/command", evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-help-doctor-split-preflight-check" {
		t.Fatalf("acceptanceGate = %q, want make command-help-doctor-split-preflight-check", evidence.AcceptanceGate)
	}
	if evidence.DryRunOnly || evidence.NoPhysicalMove || !evidence.HelpPhysicalSplitDone {
		t.Fatalf("P22-12 evidence must record help physical split completion: dryRunOnly=%t noPhysicalMove=%t helpPhysicalSplitDone=%t", evidence.DryRunOnly, evidence.NoPhysicalMove, evidence.HelpPhysicalSplitDone)
	}
	if evidence.HelpPackage != "cmd/gofly/internal/command/help" {
		t.Fatalf("helpPackage = %q, want cmd/gofly/internal/command/help", evidence.HelpPackage)
	}
	if evidence.CommandAdapter != "help_adapter.go" {
		t.Fatalf("commandAdapter = %q, want help_adapter.go", evidence.CommandAdapter)
	}
	if !evidence.DoctorPhysicalSplitDone {
		t.Fatal("doctorPhysicalSplitDone = false, want P22-14 doctor split completion recorded")
	}
	if evidence.DoctorPackage != "cmd/gofly/internal/command/doctor" {
		t.Fatalf("doctorPackage = %q, want cmd/gofly/internal/command/doctor", evidence.DoctorPackage)
	}
	if evidence.DoctorCommandAdapter != "doctor_adapter.go" {
		t.Fatalf("doctorCommandAdapter = %q, want doctor_adapter.go", evidence.DoctorCommandAdapter)
	}
	if evidence.SelectedNextFamily != "doctor" || evidence.DeferredNextFamily != "" {
		t.Fatalf("family sequence = %q/%q, want doctor/<empty>", evidence.SelectedNextFamily, evidence.DeferredNextFamily)
	}
	if !evidence.DoctorPreflightRefreshed {
		t.Fatal("doctorPreflightRefreshed = false, want P22-13 doctor preflight refresh recorded")
	}
	assertHelpDoctorPreflightSet(t, "help files", evidence.HelpFiles, []string{
		"help.go",
		"help_catalog.go",
		"help_catalog_ai.go",
		"help_catalog_api.go",
		"help_catalog_model.go",
		"help_catalog_plugin.go",
		"help_catalog_rpc.go",
		"help_metadata.go",
		"help_render.go",
		"help_topics.go",
		"help_usage.go",
	})
	assertHelpDoctorPreflightSet(t, "doctor files", evidence.DoctorFiles, []string{
		"doctor/doctor.go",
		"doctor/doctor_checks.go",
		"doctor/doctor_test.go",
	})
	assertHelpDoctorPreflightSet(t, "preflight contracts", evidence.PreflightContracts, []string{
		"help remains reachable through root help dispatch and command-specific help routing",
		"help output stays stdout-only through the command output adapter",
		"doctor remains reachable through root command dispatch",
		"doctor --json stays stdout-only with stable nextActions fields",
		"bug --json supportBundle remains available for doctor remediation guidance",
		"only help and doctor files moved into dedicated subpackages; shared files remain in the command package",
	})
	assertHelpDoctorPreflightSet(t, "golden tests", evidence.GoldenTests, []string{
		"TestCommandHelpDoctorSplitPreflightEvidence",
		"TestCommandHelpDoctorSplitPreflightContracts",
		"TestCommandHelpDoctorSplitPhysicalBoundary",
	})
	for _, want := range []string{
		"make command-help-doctor-split-preflight-check",
		"make command-output-json-adapter-dry-run-check",
		"make command-help-split-dry-run-check",
		"make command-doctor-split-dry-run-check",
		"make command-split-readiness-check",
		"make command-family-dependency-map-check",
		"make project-layout-governance-check",
	} {
		if !containsHelpDoctorPreflightString(evidence.RequiredGates, want) {
			t.Fatalf("requiredGates missing %q: %v", want, evidence.RequiredGates)
		}
	}
	if evidence.PhysicalSplitAdmission.Status != "completed-help-and-doctor-single-family-splits" {
		t.Fatalf("physicalSplitAdmission.status = %q, want completed-help-and-doctor-single-family-splits", evidence.PhysicalSplitAdmission.Status)
	}
	if !strings.Contains(evidence.PhysicalSplitAdmission.NextAllowedAction, "Refresh the next command family candidate") {
		t.Fatalf("nextAllowedAction = %q, want next candidate refresh", evidence.PhysicalSplitAdmission.NextAllowedAction)
	}
	if !strings.Contains(evidence.PhysicalSplitAdmission.BlockedAction, "Do not move shared helpers") {
		t.Fatalf("blockedAction = %q, want shared movement blocked", evidence.PhysicalSplitAdmission.BlockedAction)
	}
}

func TestCommandHelpDoctorSplitPreflightContracts(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("GOFLY_NO_COLOR", "1")

	if topic, ok := commandHelpTopic("doctor", []string{"--help"}); !ok || topic != "doctor" {
		t.Fatalf("printCommandHelp topic adapter = %q/%t, want doctor/true", topic, ok)
	}
	if got := commandUsage("doctor"); !strings.Contains(got, "Usage:\n  doctor [--json]") {
		t.Fatalf("commandUsage doctor contract missing usage:\n%s", got)
	}

	tests := []struct {
		name   string
		args   []string
		assert func(t *testing.T, stdout string, stderr string)
	}{
		{
			name: "root help dispatch",
			args: []string{"help", "doctor"},
			assert: func(t *testing.T, stdout string, stderr string) {
				t.Helper()
				if stderr != "" {
					t.Fatalf("help wrote stderr = %q, want stdout-only", stderr)
				}
				for _, want := range []string{
					"Diagnose local environment and toolchain readiness.",
					"Usage:\n  doctor [--json]",
					"Flags:\n  --json  print report as JSON",
				} {
					if !strings.Contains(stdout, want) {
						t.Fatalf("help output missing %q:\n%s", want, stdout)
					}
				}
			},
		},
		{
			name: "command-specific help routing",
			args: []string{"doctor", "--help"},
			assert: func(t *testing.T, stdout string, stderr string) {
				t.Helper()
				if stderr != "" {
					t.Fatalf("doctor help wrote stderr = %q, want stdout-only", stderr)
				}
				if !strings.Contains(stdout, "Usage:\n  doctor [--json]") {
					t.Fatalf("doctor help output missing usage:\n%s", stdout)
				}
			},
		},
		{
			name: "doctor json root dispatch",
			args: []string{"doctor", "--json"},
			assert: func(t *testing.T, stdout string, stderr string) {
				t.Helper()
				if stderr != "" {
					t.Fatalf("doctor --json wrote stderr = %q, want stdout-only JSON", stderr)
				}
				var report doctorReport
				if err := json.Unmarshal([]byte(stdout), &report); err != nil {
					t.Fatalf("decode doctor --json: %v\n%s", err, stdout)
				}
				if len(report.NextActions) == 0 {
					t.Fatalf("doctor nextActions = %#v, want remediation guidance", report.NextActions)
				}
			},
		},
		{
			name: "bug json support bundle remains available",
			args: []string{"bug", "--json"},
			assert: func(t *testing.T, stdout string, stderr string) {
				t.Helper()
				if stderr != "" {
					t.Fatalf("bug --json wrote stderr = %q, want stdout-only JSON", stderr)
				}
				var report bugReport
				if err := json.Unmarshal([]byte(stdout), &report); err != nil {
					t.Fatalf("decode bug --json: %v\n%s", err, stdout)
				}
				if report.SupportBundle.Schema != "gofly.support_bundle.v1" {
					t.Fatalf("supportBundle.schema = %q, want gofly.support_bundle.v1", report.SupportBundle.Schema)
				}
				if !containsHelpDoctorPreflightString(report.SupportBundle.Commands, "gofly doctor --json") {
					t.Fatalf("supportBundle.commands = %#v, want gofly doctor --json", report.SupportBundle.Commands)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := ExecuteWithIO(tt.args, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
				t.Fatalf("ExecuteWithIO(%v): %v", tt.args, err)
			}
			tt.assert(t, stdout.String(), stderr.String())
		})
	}

	var directDoctor bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &directDoctor}, outputText, verbosityNormal, func() error {
		return doctorCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("doctorCommand direct contract: %v", err)
	}
	if !json.Valid(directDoctor.Bytes()) {
		t.Fatalf("doctorCommand direct output is not JSON:\n%s", directDoctor.String())
	}

	var directBug bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &directBug}, outputText, verbosityNormal, func() error {
		return bugCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("bugCommand direct contract: %v", err)
	}
	if !strings.Contains(directBug.String(), "supportBundle") {
		t.Fatalf("bugCommand direct output missing supportBundle:\n%s", directBug.String())
	}
}

func TestCommandHelpDoctorSplitPhysicalBoundary(t *testing.T) {
	evidence := loadCommandHelpDoctorSplitPreflightEvidence(t)
	commandDir := filepath.Join("..", "..", "..", "..", "cmd", "gofly", "internal", "command")
	helpDir := filepath.Join(commandDir, "help")
	for _, filename := range evidence.HelpFiles {
		path := filepath.Join(helpDir, filename)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist in help subpackage after P22-12 split: %v", filename, err)
		}
	}
	for _, filename := range evidence.DoctorFiles {
		path := filepath.Join(commandDir, filename)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist in doctor subpackage after P22-14 split: %v", filename, err)
		}
	}
	if _, err := os.Stat(filepath.Join(commandDir, evidence.CommandAdapter)); err != nil {
		t.Fatalf("expected command help adapter after help-only split: %v", err)
	}
	if _, err := os.Stat(filepath.Join(commandDir, evidence.DoctorCommandAdapter)); err != nil {
		t.Fatalf("expected command doctor adapter after doctor split: %v", err)
	}
	if _, err := os.Stat(filepath.Join(commandDir, "help.go")); !os.IsNotExist(err) {
		t.Fatalf("expected root help.go to move into help subpackage, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(commandDir, "doctor.go")); !os.IsNotExist(err) {
		t.Fatalf("expected root doctor.go to move into doctor subpackage, stat err=%v", err)
	}
}

func loadCommandHelpDoctorSplitPreflightEvidence(t *testing.T) commandHelpDoctorSplitPreflightEvidence {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "docs", "reference", "command-help-doctor-split-preflight.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read help/doctor split preflight evidence: %v", err)
	}
	var evidence commandHelpDoctorSplitPreflightEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("decode help/doctor split preflight evidence: %v", err)
	}
	return evidence
}

func assertHelpDoctorPreflightSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsHelpDoctorPreflightString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsHelpDoctorPreflightString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
