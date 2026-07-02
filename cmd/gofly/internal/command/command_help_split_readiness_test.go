package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandHelpSplitDryRunEvidence struct {
	Schema         string   `json:"schema"`
	Status         string   `json:"status"`
	Family         string   `json:"family"`
	Package        string   `json:"package"`
	AcceptanceGate string   `json:"acceptanceGate"`
	DryRunOnly     bool     `json:"dryRunOnly"`
	NoPhysicalMove bool     `json:"noPhysicalMove"`
	FamilyFiles    []string `json:"familyFiles"`
	GoldenTests    []string `json:"goldenTests"`
	GoldenTopics   []string `json:"goldenTopics"`
	RequiredGates  []string `json:"requiredGates"`
}

func TestHelpFamilyGoldenOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("GOFLY_NO_COLOR", "1")

	tests := []struct {
		name     string
		topic    string
		required []string
		forbid   []string
	}{
		{
			name:  "doctor",
			topic: "doctor",
			required: []string{
				"Diagnose local environment and toolchain readiness.",
				"Usage:\n  doctor [--json]",
				"Flags:\n  --json  print report as JSON",
				"Examples:\n  doctor\n  doctor --json",
			},
			forbid: []string{"\x1b[", "gofly doctor"},
		},
		{
			name:  "api",
			topic: "api",
			required: []string{
				"Generate, validate, format, document, diff and extend API definition files.",
				"Usage:\n  api <command> [arguments]",
				"Available Commands:",
				"go                   generate REST service code from .api",
				"swagger              generate OpenAPI/Swagger document",
				"Flags:\n  -h, --help  show help for api\n  -o <file>   generate a starter .api template",
				"Examples:\n  api go -api user.api -dir . -style go_zero",
			},
			forbid: []string{"\x1b[", "gofly api <command>", "Usage of api"},
		},
		{
			name:  "rpc gen",
			topic: "rpc gen",
			required: []string{
				"Generate gofly/gRPC service code from a protobuf file.",
				"Usage:\n  rpc gen --src <service.proto> --out <dir> [flags]",
				"--transport grpc|gofly|both    transport targets",
				"--profile <profile>            generation profile: gofly-ai|gozero-compatible|kitex-compatible",
				"Examples:\n  rpc gen -src greeter.proto -out . -style go_zero",
			},
			forbid: []string{"\x1b[", "gofly rpc gen"},
		},
		{
			name:  "plugin run",
			topic: "plugin run",
			required: []string{
				"Run a built-in or external generation plugin, including cached plugins.",
				"Usage:\n  plugin run <plugin-name-or-path> --name <service> --module <module> --dir <dir> [--command <kind>] [--json]",
				"--remote <repo>@<version>      auto-install and run cached remote plugin",
				"--go-plugin <path-or-dir>      run one executable plugin or traverse a directory of executable plugins",
				"Examples:\n  plugin run ./my-plugin --name hello --module example.com/hello --dir . --json",
			},
			forbid: []string{"\x1b[", "gofly plugin run"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandUsage(tt.topic)
			for _, want := range tt.required {
				if !strings.Contains(got, want) {
					t.Fatalf("commandUsage(%q) missing golden snippet %q:\n%s", tt.topic, want, got)
				}
			}
			for _, forbidden := range tt.forbid {
				if strings.Contains(got, forbidden) {
					t.Fatalf("commandUsage(%q) contains forbidden snippet %q:\n%s", tt.topic, forbidden, got)
				}
			}
		})
	}
}

func TestHelpFamilyDryRunEvidence(t *testing.T) {
	evidence := loadCommandHelpSplitDryRunEvidence(t)
	if evidence.Schema != "gofly.command_help_split_dry_run.v1" {
		t.Fatalf("schema = %q, want gofly.command_help_split_dry_run.v1", evidence.Schema)
	}
	if evidence.Status != "completed-preflight" {
		t.Fatalf("status = %q, want completed-preflight", evidence.Status)
	}
	if evidence.Family != "help" || evidence.Package != "cmd/gofly/internal/command" {
		t.Fatalf("family/package = %q/%q, want help/cmd/gofly/internal/command", evidence.Family, evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-help-split-dry-run-check" {
		t.Fatalf("acceptanceGate = %q, want make command-help-split-dry-run-check", evidence.AcceptanceGate)
	}
	if !evidence.DryRunOnly || !evidence.NoPhysicalMove {
		t.Fatalf("dry-run evidence must not authorize a physical move: dryRunOnly=%t noPhysicalMove=%t", evidence.DryRunOnly, evidence.NoPhysicalMove)
	}

	assertStringSet(t, "family files", evidence.FamilyFiles, []string{
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
	assertStringSet(t, "golden topics", evidence.GoldenTopics, []string{"doctor", "api", "rpc gen", "plugin run"})
	for _, want := range []string{
		"TestHelpFamilyGoldenOutput",
		"TestHelpFamilyDryRunEvidence",
		"TestCommandHelpForTopicBoundaries",
		"TestExecuteColoredHelp",
		"TestExecuteNestedColoredHelp",
	} {
		if !containsHelpString(evidence.GoldenTests, want) {
			t.Fatalf("goldenTests missing %q: %v", want, evidence.GoldenTests)
		}
	}
	for _, want := range []string{
		"make command-help-split-dry-run-check",
		"make command-split-readiness-check",
		"make command-family-dependency-map-check",
		"make cli-command-surface-check",
	} {
		if !containsHelpString(evidence.RequiredGates, want) {
			t.Fatalf("requiredGates missing %q: %v", want, evidence.RequiredGates)
		}
	}
}

func loadCommandHelpSplitDryRunEvidence(t *testing.T) commandHelpSplitDryRunEvidence {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "docs", "reference", "command-help-split-dry-run.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read help split dry-run evidence: %v", err)
	}
	var evidence commandHelpSplitDryRunEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("decode help split dry-run evidence: %v", err)
	}
	return evidence
}

func assertStringSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsHelpString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsHelpString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
