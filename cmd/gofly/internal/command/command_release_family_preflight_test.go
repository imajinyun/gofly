package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandReleaseFamilyPreflightEvidence struct {
	Schema                 string                                `json:"schema"`
	Status                 string                                `json:"status"`
	Package                string                                `json:"package"`
	AcceptanceGate         string                                `json:"acceptanceGate"`
	PreflightOnly          bool                                  `json:"preflightOnly"`
	NoPhysicalMove         bool                                  `json:"noPhysicalMove"`
	SelectedFamily         commandReleaseFamilyPreflightFamily   `json:"selectedFamily"`
	Contracts              commandReleaseFamilyPreflightContract `json:"contracts"`
	PhysicalSplitAdmission commandReleaseFamilyPhysicalAdmission `json:"physicalSplitAdmission"`
	GoldenTests            []string                              `json:"goldenTests"`
	RequiredGates          []string                              `json:"requiredGates"`
	NextStep               commandReleaseFamilyNextStep          `json:"nextStep"`
}

type commandReleaseFamilyPreflightFamily struct {
	ID                  string   `json:"id"`
	Status              string   `json:"status"`
	CurrentPackage      string   `json:"currentPackage"`
	FuturePackage       string   `json:"futurePackage"`
	Files               []string `json:"files"`
	BlockedMoves        []string `json:"blockedMoves"`
	RollbackRequirement string   `json:"rollbackRequirement"`
}

type commandReleaseFamilyPreflightContract struct {
	CommandRegistration    commandReleaseFamilyCommandRegistration `json:"commandRegistration"`
	HelpRouting            commandReleaseFamilyHelpRouting         `json:"helpRouting"`
	JSONEnvelope           commandReleaseFamilyJSONEnvelope        `json:"jsonEnvelope"`
	ReleaseChecks          []string                                `json:"releaseChecks"`
	LocalExecutionBoundary commandReleaseFamilyLocalBoundary       `json:"localExecutionBoundary"`
	OutputDiscipline       commandReleaseFamilyOutputDiscipline    `json:"outputDiscipline"`
}

type commandReleaseFamilyCommandRegistration struct {
	RootCommand   string   `json:"rootCommand"`
	ChildCommands []string `json:"childCommands"`
	ManifestEntry string   `json:"manifestEntry"`
}

type commandReleaseFamilyHelpRouting struct {
	Topics     []string `json:"topics"`
	Usage      string   `json:"usage"`
	StdoutOnly bool     `json:"stdoutOnly"`
}

type commandReleaseFamilyJSONEnvelope struct {
	Command                string   `json:"command"`
	ErrorCode              string   `json:"errorCode"`
	RequiredEnvelopeFields []string `json:"requiredEnvelopeFields"`
	RequiredDataFields     []string `json:"requiredDataFields"`
	NoDuplicateGlobalJSON  bool     `json:"noDuplicateGlobalJSON"`
}

type commandReleaseFamilyLocalBoundary struct {
	Status         string   `json:"status"`
	RunnerFiles    []string `json:"runnerFiles"`
	RenderingFiles []string `json:"renderingFiles"`
}

type commandReleaseFamilyOutputDiscipline struct {
	JSONStdoutOnly               bool `json:"jsonStdoutOnly"`
	ErrorsDoNotEmitDuplicateJSON bool `json:"errorsDoNotEmitDuplicateJSON"`
	TextOutputUsesCommandIO      bool `json:"textOutputUsesCommandIO"`
}

type commandReleaseFamilyPhysicalAdmission struct {
	Status          string   `json:"status"`
	NextStep        string   `json:"nextStep"`
	RequiredSignals []string `json:"requiredSignals"`
	RequiredGates   []string `json:"requiredGates"`
}

type commandReleaseFamilyNextStep struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

func TestCommandReleaseFamilyPreflightEvidence(t *testing.T) {
	evidence := loadCommandReleaseFamilyPreflightEvidence(t)
	if evidence.Schema != "gofly.command_release_family_preflight.v1" {
		t.Fatalf("schema = %q, want gofly.command_release_family_preflight.v1", evidence.Schema)
	}
	if evidence.Status != "completed-release-family-preflight" {
		t.Fatalf("status = %q, want completed-release-family-preflight", evidence.Status)
	}
	if evidence.Package != "cmd/gofly/internal/command" {
		t.Fatalf("package = %q, want command package", evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-release-family-preflight-check" {
		t.Fatalf("acceptanceGate = %q, want make command-release-family-preflight-check", evidence.AcceptanceGate)
	}
	if !evidence.PreflightOnly || !evidence.NoPhysicalMove {
		t.Fatalf("release preflight must not move files: preflightOnly=%t noPhysicalMove=%t", evidence.PreflightOnly, evidence.NoPhysicalMove)
	}
	assertReleasePreflightSet(t, "golden tests", evidence.GoldenTests, []string{
		"TestCommandReleaseFamilyPreflightEvidence",
		"TestCommandReleaseFamilyPreflightContracts",
	})
	for _, want := range []string{
		"make command-release-family-preflight-check",
		"make command-next-family-candidate-refresh-check",
		"make command-family-dependency-map-check",
		"make cli-json-contract-goldens-check",
		"make required-checks-drift-check",
	} {
		if !containsReleasePreflightString(evidence.RequiredGates, want) {
			t.Fatalf("requiredGates missing %q: %v", want, evidence.RequiredGates)
		}
	}
	if evidence.PhysicalSplitAdmission.Status != "ready-for-single-family-split" {
		t.Fatalf("physicalSplitAdmission.status = %q, want ready-for-single-family-split", evidence.PhysicalSplitAdmission.Status)
	}
	if evidence.NextStep.ID != "P22-17-command-release-single-family-split" {
		t.Fatalf("nextStep.id = %q, want P22-17-command-release-single-family-split", evidence.NextStep.ID)
	}
}

func TestCommandReleaseFamilyPreflightContracts(t *testing.T) {
	evidence := loadCommandReleaseFamilyPreflightEvidence(t)
	family := evidence.SelectedFamily
	if family.ID != "release" || family.Status != "ready-for-release-single-family-split" {
		t.Fatalf("selected family = %q/%q, want release/ready-for-release-single-family-split", family.ID, family.Status)
	}
	if family.CurrentPackage != "cmd/gofly/internal/command" || family.FuturePackage != "cmd/gofly/internal/command/release" {
		t.Fatalf("selected family packages = %q -> %q", family.CurrentPackage, family.FuturePackage)
	}
	assertReleasePreflightSet(t, "release files", family.Files, []string{
		"release.go",
		"release_contract_checks.go",
		"release_helpers.go",
		"release_local_checks.go",
		"release_output.go",
		"release_test.go",
		"release_types.go",
	})
	commandDir := filepath.Join("..", "..", "..", "..", "cmd", "gofly", "internal", "command")
	for _, filename := range family.Files {
		if _, err := os.Stat(filepath.Join(commandDir, filename)); err != nil {
			t.Fatalf("release preflight file %s must remain in command package during P22-16: %v", filename, err)
		}
	}
	if _, err := os.Stat(filepath.Join(commandDir, "release")); !os.IsNotExist(err) {
		t.Fatalf("release subpackage must not exist during P22-16 preflight, stat err=%v", err)
	}
	for _, want := range []string{
		"do not move JSON error helpers",
		"do not move global output or IO helpers",
		"do not move any non-release command family",
	} {
		if !containsReleasePreflightString(family.BlockedMoves, want) {
			t.Fatalf("blockedMoves missing %q: %v", want, family.BlockedMoves)
		}
	}
	if !strings.Contains(family.RollbackRequirement, "Restore release files") {
		t.Fatalf("rollbackRequirement = %q, want release file restore guidance", family.RollbackRequirement)
	}

	contracts := evidence.Contracts
	if contracts.CommandRegistration.RootCommand != "release" || !containsReleasePreflightString(contracts.CommandRegistration.ChildCommands, "check") {
		t.Fatalf("command registration = %+v, want release/check", contracts.CommandRegistration)
	}
	if !rootRegistryContains("release") {
		t.Fatalf("root registry must contain release command")
	}
	if _, ok := releaseCommands.commands["check"]; !ok {
		t.Fatalf("release command registry must contain check")
	}
	if !manifestContainsRelease() {
		t.Fatalf("root command manifest must contain release")
	}

	assertReleasePreflightSet(t, "help topics", contracts.HelpRouting.Topics, []string{"release", "release check"})
	if commandUsage("release") == "" {
		t.Fatalf("release help routing missing release usage")
	}
	var helpOut, helpErr bytes.Buffer
	if err := ExecuteWithIO([]string{"help", "release", "check"}, IOStreams{Out: &helpOut, Err: &helpErr}); err != nil {
		t.Fatalf("help release check: %v", err)
	}
	if !strings.Contains(helpOut.String(), "release check") || !strings.Contains(helpOut.String(), "--json") {
		t.Fatalf("help release check stdout = %q, want usage", helpOut.String())
	}
	if helpErr.Len() != 0 {
		t.Fatalf("help release check stderr = %q, want empty", helpErr.String())
	}

	jsonContract := contracts.JSONEnvelope
	if jsonContract.Command != "release.check" || jsonContract.ErrorCode != "RELEASE_CHECK_FAILED" || !jsonContract.NoDuplicateGlobalJSON {
		t.Fatalf("json contract = %+v, want release.check duplicate-safe blocker contract", jsonContract)
	}
	assertReleasePreflightSet(t, "release checks", contracts.ReleaseChecks, []string{
		"api-breaking",
		"rpc-breaking",
		"go-api-compat",
		"changelog-version",
		"go-mod-tidy",
		"gateway-profile-contract",
		"gateway-aggregation-contract",
		"rpc-mux-adapter-evidence",
		"generated-rpc-mux-retry-smoke",
	})
	if contracts.LocalExecutionBoundary.Status != "file-separated-before-package-split" {
		t.Fatalf("localExecutionBoundary.status = %q", contracts.LocalExecutionBoundary.Status)
	}
	assertReleasePreflightSet(t, "runner files", contracts.LocalExecutionBoundary.RunnerFiles, []string{"release_helpers.go", "release_local_checks.go"})
	assertReleasePreflightSet(t, "rendering files", contracts.LocalExecutionBoundary.RenderingFiles, []string{"release_output.go"})
	if !contracts.OutputDiscipline.JSONStdoutOnly || !contracts.OutputDiscipline.ErrorsDoNotEmitDuplicateJSON || !contracts.OutputDiscipline.TextOutputUsesCommandIO {
		t.Fatalf("output discipline = %+v, want all true", contracts.OutputDiscipline)
	}

	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## v9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write changelog: %v", err)
	}
	var out, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"--output=json", "release", "check", "--changelog", changelog}, IOStreams{Out: &out, Err: &stderr})
	if err == nil || !errors.Is(err, errJSONAlreadyReported) {
		t.Fatalf("ExecuteWithIO release check error = %v, want errJSONAlreadyReported", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("release check JSON stderr = %q, want empty", stderr.String())
	}
	var envelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
		Error   *jsonError         `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("release check JSON decode: %v\n%s", err, out.String())
	}
	if envelope.OK || envelope.Command != "release.check" || envelope.Error == nil || envelope.Error.Code != "RELEASE_CHECK_FAILED" {
		t.Fatalf("release check envelope = %+v, want release.check blocker", envelope)
	}
	if strings.Count(out.String(), `"command"`) != 1 {
		t.Fatalf("release check emitted duplicate JSON envelopes:\n%s", out.String())
	}
}

func loadCommandReleaseFamilyPreflightEvidence(t *testing.T) commandReleaseFamilyPreflightEvidence {
	t.Helper()
	return commandReleaseFamilyPreflightEvidence{
		Schema:         "gofly.command_release_family_preflight.v1",
		Status:         "completed-release-family-preflight",
		Package:        "cmd/gofly/internal/command",
		AcceptanceGate: "make command-release-family-preflight-check",
		PreflightOnly:  true,
		NoPhysicalMove: true,
		SelectedFamily: commandReleaseFamilyPreflightFamily{
			ID:             "release",
			Status:         "ready-for-release-single-family-split",
			CurrentPackage: "cmd/gofly/internal/command",
			FuturePackage:  "cmd/gofly/internal/command/release",
			Files: []string{
				"release.go",
				"release_contract_checks.go",
				"release_helpers.go",
				"release_local_checks.go",
				"release_output.go",
				"release_test.go",
				"release_types.go",
			},
			BlockedMoves: []string{
				"do not move JSON error helpers",
				"do not move global output or IO helpers",
				"do not move any non-release command family",
			},
			RollbackRequirement: "Restore release files to cmd/gofly/internal/command if registration, help, or JSON behavior drifts",
		},
		Contracts: commandReleaseFamilyPreflightContract{
			CommandRegistration: commandReleaseFamilyCommandRegistration{
				RootCommand:   "release",
				ChildCommands: []string{"check"},
				ManifestEntry: "release",
			},
			HelpRouting: commandReleaseFamilyHelpRouting{
				Topics:     []string{"release", "release check"},
				Usage:      "gofly release check [--json]",
				StdoutOnly: true,
			},
			JSONEnvelope: commandReleaseFamilyJSONEnvelope{
				Command:                "release.check",
				ErrorCode:              "RELEASE_CHECK_FAILED",
				RequiredEnvelopeFields: []string{"ok", "command", "version", "data", "error"},
				RequiredDataFields:     []string{"version", "summary", "checks", "blocking", "warnings", "recommended"},
				NoDuplicateGlobalJSON:  true,
			},
			ReleaseChecks: []string{
				"api-breaking",
				"rpc-breaking",
				"go-api-compat",
				"changelog-version",
				"go-mod-tidy",
				"gateway-profile-contract",
				"gateway-aggregation-contract",
				"rpc-mux-adapter-evidence",
				"generated-rpc-mux-retry-smoke",
			},
			LocalExecutionBoundary: commandReleaseFamilyLocalBoundary{
				Status:         "file-separated-before-package-split",
				RunnerFiles:    []string{"release_helpers.go", "release_local_checks.go"},
				RenderingFiles: []string{"release_output.go"},
			},
			OutputDiscipline: commandReleaseFamilyOutputDiscipline{
				JSONStdoutOnly:               true,
				ErrorsDoNotEmitDuplicateJSON: true,
				TextOutputUsesCommandIO:      true,
			},
		},
		PhysicalSplitAdmission: commandReleaseFamilyPhysicalAdmission{
			Status:   "ready-for-single-family-split",
			NextStep: "move only release files after this preflight remains green",
			RequiredSignals: []string{
				"release command remains registered",
				"release check JSON remains stdout-only",
			},
			RequiredGates: []string{"go test ./cmd/gofly/internal/command"},
		},
		GoldenTests: []string{
			"TestCommandReleaseFamilyPreflightEvidence",
			"TestCommandReleaseFamilyPreflightContracts",
		},
		RequiredGates: []string{
			"make command-release-family-preflight-check",
			"make command-next-family-candidate-refresh-check",
			"make command-family-dependency-map-check",
			"make cli-json-contract-goldens-check",
			"make required-checks-drift-check",
		},
		NextStep: commandReleaseFamilyNextStep{
			ID:     "P22-17-command-release-single-family-split",
			Action: "move release command files only after registration, help, JSON and local execution boundaries stay green",
		},
	}
}

func rootRegistryContains(name string) bool {
	_, ok := rootCommands.commands[name]
	return ok
}

func manifestContainsRelease() bool {
	for _, entry := range rootCommandManifestEntries() {
		if entry.Name == "release" {
			return true
		}
	}
	return false
}

func assertReleasePreflightSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsReleasePreflightString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsReleasePreflightString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
