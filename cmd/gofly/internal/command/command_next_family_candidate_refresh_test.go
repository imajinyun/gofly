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

type commandNextFamilyCandidateRefreshEvidence struct {
	Schema                 string                             `json:"schema"`
	Status                 string                             `json:"status"`
	Package                string                             `json:"package"`
	AcceptanceGate         string                             `json:"acceptanceGate"`
	PlanningOnly           bool                               `json:"planningOnly"`
	NoPhysicalMove         bool                               `json:"noPhysicalMove"`
	CompletedPrerequisites []string                           `json:"completedPrerequisites"`
	SelectedCandidate      commandNextFamilyCandidate         `json:"selectedCandidate"`
	DeferredCandidates     []commandNextFamilyCandidateReason `json:"deferredCandidates"`
	BlockedFamilies        []commandNextFamilyCandidateReason `json:"blockedFamilies"`
	GoldenTests            []string                           `json:"goldenTests"`
	RequiredGates          []string                           `json:"requiredGates"`
	NextStep               commandNextFamilyCandidateNextStep `json:"nextStep"`
}

type commandNextFamilyCandidate struct {
	ID                      string   `json:"id"`
	Status                  string   `json:"status"`
	Package                 string   `json:"package"`
	Files                   []string `json:"files"`
	Reason                  string   `json:"reason"`
	RequiredPreSplitActions []string `json:"requiredPreSplitActions"`
	RequiredGates           []string `json:"requiredGates"`
	RollbackRequirement     string   `json:"rollbackRequirement"`
}

type commandNextFamilyCandidateReason struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type commandNextFamilyCandidateNextStep struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

func TestCommandNextFamilyCandidateRefreshEvidence(t *testing.T) {
	evidence := loadCommandNextFamilyCandidateRefreshEvidence(t)
	if evidence.Schema != "gofly.command_next_family_candidate_refresh.v1" {
		t.Fatalf("schema = %q, want gofly.command_next_family_candidate_refresh.v1", evidence.Schema)
	}
	if evidence.Status != "completed-candidate-refresh" {
		t.Fatalf("status = %q, want completed-candidate-refresh", evidence.Status)
	}
	if evidence.Package != "cmd/gofly/internal/command" {
		t.Fatalf("package = %q, want cmd/gofly/internal/command", evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-next-family-candidate-refresh-check" {
		t.Fatalf("acceptanceGate = %q, want make command-next-family-candidate-refresh-check", evidence.AcceptanceGate)
	}
	if !evidence.PlanningOnly || !evidence.NoPhysicalMove {
		t.Fatalf("candidate refresh must be planning-only with no physical move: planningOnly=%t noPhysicalMove=%t", evidence.PlanningOnly, evidence.NoPhysicalMove)
	}
	assertNextFamilyCandidateSet(t, "completed prerequisites", evidence.CompletedPrerequisites, []string{
		"help physical split is complete and still routed through help_adapter.go",
		"doctor physical split is complete and still routed through doctor_adapter.go",
		"shared helper movement remains blocked after help and doctor extraction",
		"command family dependency map accounts for every command file exactly once",
	})
	assertNextFamilyCandidateSet(t, "golden tests", evidence.GoldenTests, []string{
		"TestCommandNextFamilyCandidateRefreshEvidence",
		"TestCommandNextFamilyCandidateRefreshContracts",
	})
	for _, want := range []string{
		"make command-next-family-candidate-refresh-check",
		"make command-family-dependency-map-check",
		"make command-split-readiness-check",
		"make command-help-doctor-split-preflight-check",
		"make command-output-json-adapter-dry-run-check",
		"make cli-json-contract-goldens-check",
	} {
		if !containsNextFamilyCandidateString(evidence.RequiredGates, want) {
			t.Fatalf("requiredGates missing %q: %v", want, evidence.RequiredGates)
		}
	}
	if evidence.NextStep.ID != "P22-16-command-release-family-preflight" {
		t.Fatalf("nextStep.id = %q, want P22-16-command-release-family-preflight", evidence.NextStep.ID)
	}
	if !strings.Contains(evidence.NextStep.Action, "before moving any release command files") {
		t.Fatalf("nextStep.action = %q, want no move before release preflight", evidence.NextStep.Action)
	}
}

func TestCommandNextFamilyCandidateRefreshContracts(t *testing.T) {
	evidence := loadCommandNextFamilyCandidateRefreshEvidence(t)
	candidate := evidence.SelectedCandidate
	if candidate.ID != "release" || candidate.Status != "candidate-after-json-golden" {
		t.Fatalf("selected candidate = %q/%q, want release/candidate-after-json-golden", candidate.ID, candidate.Status)
	}
	if candidate.Package != "cmd/gofly/internal/command" {
		t.Fatalf("selected candidate package = %q, want command package", candidate.Package)
	}
	assertNextFamilyCandidateSet(t, "release files", candidate.Files, []string{
		"release.go",
		"release_contract_checks.go",
		"release_helpers.go",
		"release_local_checks.go",
		"release_output.go",
		"release_test.go",
		"release_types.go",
	})
	commandDir := filepath.Join("..", "..", "..", "..", "cmd", "gofly", "internal", "command")
	for _, filename := range candidate.Files {
		if _, err := os.Stat(filepath.Join(commandDir, filename)); err != nil {
			t.Fatalf("release candidate file %s must remain in command package during P22-15: %v", filename, err)
		}
	}
	if _, err := os.Stat(filepath.Join(commandDir, "release")); !os.IsNotExist(err) {
		t.Fatalf("release subpackage must not exist during P22-15 candidate refresh, stat err=%v", err)
	}
	for _, want := range []string{
		"bounded file family",
		"JSON golden coverage",
		"release contract tests",
	} {
		if !strings.Contains(candidate.Reason, want) {
			t.Fatalf("candidate reason missing %q: %s", want, candidate.Reason)
		}
	}
	for _, want := range []string{
		"pin release check JSON output, error envelope, artifact evidence, and skip-gate behavior",
		"isolate local shell command execution from release output rendering through a small adapter",
	} {
		if !containsNextFamilyCandidateString(candidate.RequiredPreSplitActions, want) {
			t.Fatalf("candidate requiredPreSplitActions missing %q: %v", want, candidate.RequiredPreSplitActions)
		}
	}
	for _, want := range []string{
		"make command-next-family-candidate-refresh-check",
		"make cli-json-contract-goldens-check",
		"make required-checks-drift-check",
	} {
		if !containsNextFamilyCandidateString(candidate.RequiredGates, want) {
			t.Fatalf("candidate requiredGates missing %q: %v", want, candidate.RequiredGates)
		}
	}
	if !strings.Contains(candidate.RollbackRequirement, "Restore release to deferred status") {
		t.Fatalf("candidate rollbackRequirement = %q, want restore guidance", candidate.RollbackRequirement)
	}

	deferred := map[string]string{}
	for _, item := range evidence.DeferredCandidates {
		deferred[item.ID] = item.Reason
	}
	for _, id := range []string{"config", "plugin", "api", "rpc", "model", "new"} {
		if deferred[id] == "" {
			t.Fatalf("deferred candidate %q must explain why it is not next: %#v", id, deferred)
		}
	}
	blocked := map[string]string{}
	for _, item := range evidence.BlockedFamilies {
		blocked[item.ID] = item.Reason
	}
	for _, id := range []string{"ai", "shared"} {
		if blocked[id] == "" {
			t.Fatalf("blocked family %q must explain blocker: %#v", id, blocked)
		}
	}

	var out bytes.Buffer
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## 9.9.9\n\n- release note\n"), 0o644); err != nil {
		t.Fatalf("write changelog: %v", err)
	}
	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	err := withCommandIO(IOStreams{Out: &out}, outputJSON, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--changelog", changelog, "--json"})
	})
	if err != nil && !errors.Is(err, errJSONAlreadyReported) {
		t.Fatalf("releaseCheckCommand json golden smoke: %v", err)
	}
	var envelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
		Error   *struct {
			Code string `json:"code"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("releaseCheckCommand JSON smoke decode: %v\n%s", err, out.String())
	}
	if envelope.Command != "release.check" || envelope.Error == nil || envelope.Error.Code != "RELEASE_CHECK_FAILED" {
		t.Fatalf("releaseCheckCommand JSON smoke envelope = %+v, want release.check blocker envelope", envelope)
	}
	if strings.Count(out.String(), `"command"`) != 1 {
		t.Fatalf("releaseCheckCommand JSON smoke emitted duplicate envelopes:\n%s", out.String())
	}
}

func loadCommandNextFamilyCandidateRefreshEvidence(t *testing.T) commandNextFamilyCandidateRefreshEvidence {
	t.Helper()
	return commandNextFamilyCandidateRefreshEvidence{
		Schema:         "gofly.command_next_family_candidate_refresh.v1",
		Status:         "completed-candidate-refresh",
		Package:        "cmd/gofly/internal/command",
		AcceptanceGate: "make command-next-family-candidate-refresh-check",
		PlanningOnly:   true,
		NoPhysicalMove: true,
		CompletedPrerequisites: []string{
			"help physical split is complete and still routed through help_adapter.go",
			"doctor physical split is complete and still routed through doctor_adapter.go",
			"shared helper movement remains blocked after help and doctor extraction",
			"command family dependency map accounts for every command file exactly once",
		},
		SelectedCandidate: commandNextFamilyCandidate{
			ID:      "release",
			Status:  "candidate-after-json-golden",
			Package: "cmd/gofly/internal/command",
			Files: []string{
				"release.go",
				"release_contract_checks.go",
				"release_helpers.go",
				"release_local_checks.go",
				"release_output.go",
				"release_test.go",
				"release_types.go",
			},
			Reason: "bounded file family with JSON golden coverage and release contract tests",
			RequiredPreSplitActions: []string{
				"pin release check JSON output, error envelope, artifact evidence, and skip-gate behavior",
				"isolate local shell command execution from release output rendering through a small adapter",
			},
			RequiredGates: []string{
				"make command-next-family-candidate-refresh-check",
				"make cli-json-contract-goldens-check",
				"make required-checks-drift-check",
			},
			RollbackRequirement: "Restore release to deferred status if JSON, help, or registration behavior drifts",
		},
		DeferredCandidates: []commandNextFamilyCandidateReason{
			{ID: "config", Reason: "config still shares path and output helpers with generator surfaces"},
			{ID: "plugin", Reason: "plugin touches external process and cache safety boundaries"},
			{ID: "api", Reason: "api remains high-coupling with generator and compatibility aliases"},
			{ID: "rpc", Reason: "rpc remains high-coupling with protobuf, descriptor and template paths"},
			{ID: "model", Reason: "model is actively changing for goctl-compatible generation"},
			{ID: "new", Reason: "new service scaffold owns broad generated-project contracts"},
		},
		BlockedFamilies: []commandNextFamilyCandidateReason{
			{ID: "ai", Reason: "AI manifest and provider governance span multiple packages"},
			{ID: "shared", Reason: "shared helpers must stay centralized until adapter boundaries are stable"},
		},
		GoldenTests: []string{
			"TestCommandNextFamilyCandidateRefreshEvidence",
			"TestCommandNextFamilyCandidateRefreshContracts",
		},
		RequiredGates: []string{
			"make command-next-family-candidate-refresh-check",
			"make command-family-dependency-map-check",
			"make command-split-readiness-check",
			"make command-help-doctor-split-preflight-check",
			"make command-output-json-adapter-dry-run-check",
			"make cli-json-contract-goldens-check",
		},
		NextStep: commandNextFamilyCandidateNextStep{
			ID:     "P22-16-command-release-family-preflight",
			Action: "run release family preflight before moving any release command files",
		},
	}
}

func assertNextFamilyCandidateSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsNextFamilyCandidateString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsNextFamilyCandidateString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
