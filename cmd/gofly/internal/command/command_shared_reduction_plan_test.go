package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type commandSharedReductionPlanEvidence struct {
	Schema                 string                              `json:"schema"`
	Status                 string                              `json:"status"`
	Family                 string                              `json:"family"`
	Package                string                              `json:"package"`
	AcceptanceGate         string                              `json:"acceptanceGate"`
	PlanningOnly           bool                                `json:"planningOnly"`
	NoPhysicalMove         bool                                `json:"noPhysicalMove"`
	RecommendedOrder       []string                            `json:"recommendedOrder"`
	ReductionDomains       []commandSharedReductionPlanDomain  `json:"reductionDomains"`
	GoldenTests            []string                            `json:"goldenTests"`
	RequiredGates          []string                            `json:"requiredGates"`
	PhysicalSplitAdmission commandSharedReductionPlanAdmission `json:"physicalSplitAdmission"`
}

type commandSharedReductionPlanDomain struct {
	ID                  string   `json:"id"`
	Files               []string `json:"files"`
	CurrentCoupling     string   `json:"currentCoupling"`
	TargetBoundary      string   `json:"targetBoundary"`
	FirstAction         string   `json:"firstAction"`
	PhysicalMoveAllowed bool     `json:"physicalMoveAllowed"`
	RequiredGates       []string `json:"requiredGates"`
}

type commandSharedReductionPlanAdmission struct {
	Status              string `json:"status"`
	NextAllowedAction   string `json:"nextAllowedAction"`
	RollbackRequirement string `json:"rollbackRequirement"`
}

func TestCommandSharedReductionPlanEvidence(t *testing.T) {
	evidence := loadCommandSharedReductionPlanEvidence(t)
	if evidence.Schema != "gofly.command_shared_reduction_plan.v1" {
		t.Fatalf("schema = %q, want gofly.command_shared_reduction_plan.v1", evidence.Schema)
	}
	if evidence.Status != "completed-preflight" {
		t.Fatalf("status = %q, want completed-preflight", evidence.Status)
	}
	if evidence.Family != "shared" || evidence.Package != "cmd/gofly/internal/command" {
		t.Fatalf("family/package = %q/%q, want shared/cmd/gofly/internal/command", evidence.Family, evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-shared-reduction-plan-check" {
		t.Fatalf("acceptanceGate = %q, want make command-shared-reduction-plan-check", evidence.AcceptanceGate)
	}
	if !evidence.PlanningOnly || !evidence.NoPhysicalMove {
		t.Fatalf("shared reduction plan must not authorize a physical move: planningOnly=%t noPhysicalMove=%t", evidence.PlanningOnly, evidence.NoPhysicalMove)
	}
	assertSharedReductionSet(t, "golden tests", evidence.GoldenTests, []string{
		"TestCommandSharedReductionPlanEvidence",
		"TestCommandSharedReductionPlanDomains",
	})
	assertSharedReductionSet(t, "required gates", evidence.RequiredGates, []string{
		"make command-shared-reduction-plan-check",
		"make command-split-readiness-check",
		"make command-family-dependency-map-check",
		"make project-layout-governance-check",
		"make cli-command-surface-check",
		"make cli-json-contract-goldens-check",
		"GOCACHE=$PWD/.tmp-test/gocache GOTMPDIR=$PWD/.tmp-test/gotmp go test -shuffle=on ./cmd/gofly/internal/command -run 'TestCommandSharedReductionPlan(Evidence|Domains)' -count=1",
		"git diff --check",
	})
	if evidence.PhysicalSplitAdmission.Status != "blocked-until-adapters" {
		t.Fatalf("physicalSplitAdmission.status = %q, want blocked-until-adapters", evidence.PhysicalSplitAdmission.Status)
	}
	if evidence.PhysicalSplitAdmission.RollbackRequirement == "" {
		t.Fatal("physicalSplitAdmission.rollbackRequirement must not be empty")
	}
}

func TestCommandSharedReductionPlanDomains(t *testing.T) {
	evidence := loadCommandSharedReductionPlanEvidence(t)
	assertSharedReductionSet(t, "recommended order", evidence.RecommendedOrder, []string{
		"output-io",
		"json-envelope",
		"root-wiring",
		"path-flags",
		"template-source",
	})

	domains := map[string]commandSharedReductionPlanDomain{}
	for _, domain := range evidence.ReductionDomains {
		if domain.ID == "" {
			t.Fatal("reduction domain id must not be empty")
		}
		domains[domain.ID] = domain
		if domain.PhysicalMoveAllowed {
			t.Fatalf("domain %s allows physical move before adapters", domain.ID)
		}
		if domain.CurrentCoupling == "" || domain.TargetBoundary == "" || domain.FirstAction == "" {
			t.Fatalf("domain %s must include coupling, target boundary, and first action: %+v", domain.ID, domain)
		}
		for _, file := range domain.Files {
			path := filepath.Join(".", file)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("domain %s file %s is not present in command package: %v", domain.ID, file, err)
			}
		}
	}

	expectedFiles := map[string][]string{
		"output-io":       {"io.go", "output_flags.go"},
		"json-envelope":   {"json_error.go", "json_error_writer.go", "json_error_classify.go"},
		"root-wiring":     {"root.go", "root_execute.go", "registry.go", "command_args.go"},
		"path-flags":      {"path_flags.go", "file_input.go", "dry_run_flags.go"},
		"template-source": {"template_source_flags.go", "template_filters.go", "template_command.go"},
	}
	for id, files := range expectedFiles {
		domain, ok := domains[id]
		if !ok {
			t.Fatalf("missing reduction domain %s", id)
		}
		assertSharedReductionSet(t, id+" files", domain.Files, files)
	}
}

func loadCommandSharedReductionPlanEvidence(t *testing.T) commandSharedReductionPlanEvidence {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "docs", "reference", "command-shared-reduction-plan.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read shared reduction plan evidence: %v", err)
	}
	var evidence commandSharedReductionPlanEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("decode shared reduction plan evidence: %v", err)
	}
	return evidence
}

func assertSharedReductionSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsSharedReductionString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsSharedReductionString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
