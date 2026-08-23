package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

type commandConfigFamilyPreflightEvidence struct {
	Schema                 string                               `json:"schema"`
	Status                 string                               `json:"status"`
	Package                string                               `json:"package"`
	AcceptanceGate         string                               `json:"acceptanceGate"`
	PreflightOnly          bool                                 `json:"preflightOnly"`
	NoPhysicalMove         bool                                 `json:"noPhysicalMove"`
	SelectedFamily         commandConfigFamilyPreflightFamily   `json:"selectedFamily"`
	Contracts              commandConfigFamilyPreflightContract `json:"contracts"`
	PhysicalSplitAdmission commandConfigFamilyPhysicalAdmission `json:"physicalSplitAdmission"`
	GoldenTests            []string                             `json:"goldenTests"`
	RequiredGates          []string                             `json:"requiredGates"`
	NextStep               commandConfigFamilyNextStep          `json:"nextStep"`
}

type commandConfigFamilyPreflightFamily struct {
	ID                  string   `json:"id"`
	Status              string   `json:"status"`
	CurrentPackage      string   `json:"currentPackage"`
	FuturePackage       string   `json:"futurePackage"`
	Files               []string `json:"files"`
	BlockedMoves        []string `json:"blockedMoves"`
	RollbackRequirement string   `json:"rollbackRequirement"`
}

type commandConfigFamilyPreflightContract struct {
	CommandRegistration    commandConfigFamilyCommandRegistration `json:"commandRegistration"`
	HelpRouting            commandConfigFamilyHelpRouting         `json:"helpRouting"`
	JSONEnvelope           commandConfigFamilyJSONEnvelope        `json:"jsonEnvelope"`
	PathBoundary           commandConfigFamilyPathBoundary        `json:"pathBoundary"`
	LocalExecutionBoundary commandConfigFamilyLocalBoundary       `json:"localExecutionBoundary"`
	OutputDiscipline       commandConfigFamilyOutputDiscipline    `json:"outputDiscipline"`
}

type commandConfigFamilyCommandRegistration struct {
	RootCommand   string   `json:"rootCommand"`
	ChildCommands []string `json:"childCommands"`
	ManifestEntry string   `json:"manifestEntry"`
}

type commandConfigFamilyHelpRouting struct {
	Topics     []string `json:"topics"`
	Usage      string   `json:"usage"`
	StdoutOnly bool     `json:"stdoutOnly"`
}

type commandConfigFamilyJSONEnvelope struct {
	PlanCommands           []string `json:"planCommands"`
	ErrorCode              string   `json:"errorCode"`
	RequiredEnvelopeFields []string `json:"requiredEnvelopeFields"`
	NoImplicitJSON         bool     `json:"noImplicitJson"`
	NoDuplicateGlobalJSON  bool     `json:"noDuplicateGlobalJson"`
}

type commandConfigFamilyPathBoundary struct {
	ConfigFileConstant string `json:"configFileConstant"`
	UsesGeneratorPath  bool   `json:"usesGeneratorPath"`
}

type commandConfigFamilyLocalBoundary struct {
	Status         string   `json:"status"`
	RunnerFiles    []string `json:"runnerFiles"`
	FieldFiles     []string `json:"fieldFiles"`
	RenderingFiles []string `json:"renderingFiles"`
}

type commandConfigFamilyOutputDiscipline struct {
	JSONStdoutOnly               bool `json:"jsonStdoutOnly"`
	ErrorsDoNotEmitDuplicateJSON bool `json:"errorsDoNotEmitDuplicateJson"`
	TextOutputUsesCommandIO      bool `json:"textOutputUsesCommandIO"`
	NoImplicitJSON               bool `json:"noImplicitJson"`
}

type commandConfigFamilyPhysicalAdmission struct {
	Status          string   `json:"status"`
	NextStep        string   `json:"nextStep"`
	RequiredSignals []string `json:"requiredSignals"`
	RequiredGates   []string `json:"requiredGates"`
}

type commandConfigFamilyNextStep struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

func TestCommandConfigFamilyPreflightEvidence(t *testing.T) {
	evidence := loadCommandConfigFamilyPreflightEvidence(t)
	if evidence.Schema != "gofly.command_config_family_preflight.v1" {
		t.Fatalf("schema = %q, want gofly.command_config_family_preflight.v1", evidence.Schema)
	}
	if evidence.Status != "completed-config-family-preflight" {
		t.Fatalf("status = %q, want completed-config-family-preflight", evidence.Status)
	}
	if evidence.Package != "cmd/gofly/internal/command" {
		t.Fatalf("package = %q, want command package", evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-config-family-preflight-check" {
		t.Fatalf("acceptanceGate = %q, want make command-config-family-preflight-check", evidence.AcceptanceGate)
	}
	if !evidence.PreflightOnly || !evidence.NoPhysicalMove {
		t.Fatalf("config preflight must stay planning-only: preflightOnly=%t noPhysicalMove=%t", evidence.PreflightOnly, evidence.NoPhysicalMove)
	}
	assertConfigPreflightSet(t, "golden tests", evidence.GoldenTests, []string{
		"TestCommandConfigFamilyPreflightEvidence",
		"TestCommandConfigFamilyPreflightContracts",
	})
	for _, want := range []string{
		"make command-config-family-preflight-check",
		"make command-next-family-candidate-refresh-check",
		"make command-family-dependency-map-check",
		"make cli-json-contract-goldens-check",
		"make cli-configuration-governance-check",
		"make required-checks-drift-check",
	} {
		if !containsConfigPreflightString(evidence.RequiredGates, want) {
			t.Fatalf("requiredGates missing %q: %v", want, evidence.RequiredGates)
		}
	}
	if evidence.PhysicalSplitAdmission.Status != "preflight-complete-physical-split-not-authorized" {
		t.Fatalf("physicalSplitAdmission.status = %q, want preflight-complete-physical-split-not-authorized", evidence.PhysicalSplitAdmission.Status)
	}
	if evidence.NextStep.ID != "P22-19-command-config-family-split" {
		t.Fatalf("nextStep.id = %q, want P22-19-command-config-family-split", evidence.NextStep.ID)
	}
	if !strings.Contains(evidence.NextStep.Action, "until a dedicated physical-split change is authorized") {
		t.Fatalf("nextStep.action = %q, want unauthorized physical split hold", evidence.NextStep.Action)
	}
}

func TestCommandConfigFamilyPreflightContracts(t *testing.T) {
	evidence := loadCommandConfigFamilyPreflightEvidence(t)
	family := evidence.SelectedFamily
	if family.ID != "config" || family.Status != "completed-config-family-preflight" {
		t.Fatalf("selected family = %q/%q, want config/completed-config-family-preflight", family.ID, family.Status)
	}
	if family.CurrentPackage != "cmd/gofly/internal/command" || family.FuturePackage != "cmd/gofly/internal/command/config" {
		t.Fatalf("selected family packages = %q -> %q", family.CurrentPackage, family.FuturePackage)
	}
	assertConfigPreflightSet(t, "config files", family.Files, []string{
		"config_command.go",
		"config_fields.go",
		"config_field_setters.go",
		"config_field_helpers.go",
	})
	commandDir := filepath.Join("..", "..", "..", "..", "cmd", "gofly", "internal", "command")
	if _, err := os.Stat(filepath.Join(commandDir, "config")); !os.IsNotExist(err) {
		t.Fatalf("config subpackage must not exist during P22-18 preflight, stat err=%v", err)
	}
	for _, filename := range family.Files {
		if _, err := os.Stat(filepath.Join(commandDir, filename)); err != nil {
			t.Fatalf("config family file %s must remain in command package: %v", filename, err)
		}
	}
	for _, want := range []string{
		"do not move JSON error helpers",
		"do not move global output or IO helpers",
		"do not move generator config helpers",
		"do not move any non-config command family",
	} {
		if !containsConfigPreflightString(family.BlockedMoves, want) {
			t.Fatalf("blockedMoves missing %q: %v", want, family.BlockedMoves)
		}
	}
	if !strings.Contains(family.RollbackRequirement, "Keep config files") {
		t.Fatalf("rollbackRequirement = %q, want keep-in-place guidance", family.RollbackRequirement)
	}

	contracts := evidence.Contracts
	if contracts.CommandRegistration.RootCommand != "config" {
		t.Fatalf("command registration root = %q, want config", contracts.CommandRegistration.RootCommand)
	}
	assertConfigPreflightSet(t, "config children", contracts.CommandRegistration.ChildCommands, []string{
		"init", "show", "get", "set", "clean",
	})
	if !rootRegistryContains("config") {
		t.Fatalf("root registry must contain config command")
	}
	if !manifestContainsConfig() {
		t.Fatalf("root command manifest must contain config")
	}

	assertConfigPreflightSet(t, "help topics", contracts.HelpRouting.Topics, []string{
		"config", "config init", "config show", "config get", "config set", "config clean",
	})
	if commandUsage("config") == "" || commandUsage("config init") == "" {
		t.Fatalf("config help routing missing usage")
	}
	if !contracts.HelpRouting.StdoutOnly {
		t.Fatal("help routing must stay stdout-only")
	}

	var helpOut, helpErr bytes.Buffer
	if err := ExecuteWithIO([]string{"help", "config", "get"}, IOStreams{Out: &helpOut, Err: &helpErr}); err != nil {
		t.Fatalf("help config get: %v", err)
	}
	if !strings.Contains(helpOut.String(), "config get") {
		t.Fatalf("help config get stdout = %q, want usage", helpOut.String())
	}
	if helpErr.Len() != 0 {
		t.Fatalf("help config get stderr = %q, want empty", helpErr.String())
	}

	jsonContract := contracts.JSONEnvelope
	assertConfigPreflightSet(t, "plan commands", jsonContract.PlanCommands, []string{"config.init", "config.set", "config.clean"})
	if jsonContract.ErrorCode != "USAGE_ERROR" || !jsonContract.NoImplicitJSON || !jsonContract.NoDuplicateGlobalJSON {
		t.Fatalf("json contract = %+v, want usage-error duplicate-safe no-implicit-json contract", jsonContract)
	}
	assertConfigPreflightSet(t, "envelope fields", jsonContract.RequiredEnvelopeFields, []string{"ok", "command", "version", "data", "error"})
	if contracts.PathBoundary.ConfigFileConstant != generator.DefaultConfigFile || !contracts.PathBoundary.UsesGeneratorPath {
		t.Fatalf("path boundary = %+v, want generator.DefaultConfigFile", contracts.PathBoundary)
	}
	if contracts.LocalExecutionBoundary.Status != "preflight-complete-files-remain-in-command" {
		t.Fatalf("localExecutionBoundary.status = %q", contracts.LocalExecutionBoundary.Status)
	}
	assertConfigPreflightSet(t, "runner files", contracts.LocalExecutionBoundary.RunnerFiles, []string{"config_command.go"})
	assertConfigPreflightSet(t, "field files", contracts.LocalExecutionBoundary.FieldFiles, []string{
		"config_fields.go", "config_field_setters.go", "config_field_helpers.go",
	})
	if len(contracts.LocalExecutionBoundary.RenderingFiles) != 0 {
		t.Fatalf("rendering files = %v, want empty until an authorized adapter split", contracts.LocalExecutionBoundary.RenderingFiles)
	}
	if !contracts.OutputDiscipline.JSONStdoutOnly || !contracts.OutputDiscipline.ErrorsDoNotEmitDuplicateJSON || !contracts.OutputDiscipline.TextOutputUsesCommandIO || !contracts.OutputDiscipline.NoImplicitJSON {
		t.Fatalf("output discipline = %+v, want all true", contracts.OutputDiscipline)
	}

	dir := t.TempDir()
	var planOut, planErr bytes.Buffer
	if err := ExecuteWithIO([]string{"--output=json", "config", "init", "--dir", dir, "--name", "hello", "--module", "example.com/hello", "--dry-run"}, IOStreams{Out: &planOut, Err: &planErr}); err != nil {
		t.Fatalf("config init dry-run JSON: %v", err)
	}
	if planErr.Len() != 0 {
		t.Fatalf("config init dry-run JSON stderr = %q, want empty", planErr.String())
	}
	var planEnvelope struct {
		OK      bool    `json:"ok"`
		Command string  `json:"command"`
		Data    cliPlan `json:"data"`
		Error   *jsonError
	}
	decodeSingleJSONEnvelope(t, planOut.Bytes(), &planEnvelope)
	if !planEnvelope.OK || planEnvelope.Command != "config.init" || planEnvelope.Error != nil {
		t.Fatalf("config init dry-run envelope = %+v, want config.init plan", planEnvelope)
	}
	if !planEnvelope.Data.DryRun || len(planEnvelope.Data.Actions) == 0 || planEnvelope.Data.Actions[0].Operation != "write-config" {
		t.Fatalf("config init dry-run plan = %+v, want write-config action", planEnvelope.Data)
	}
	if _, err := os.Stat(filepath.Join(dir, generator.DefaultConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("config init --dry-run must not write %s, stat err=%v", generator.DefaultConfigFile, err)
	}

	var textOut, textErr bytes.Buffer
	if err := ExecuteWithIO([]string{"config", "init", "--dir", dir, "--name", "hello", "--module", "example.com/hello", "--dry-run"}, IOStreams{Out: &textOut, Err: &textErr}); err != nil {
		t.Fatalf("config init dry-run text: %v", err)
	}
	if textErr.Len() != 0 {
		t.Fatalf("config init dry-run text stderr = %q, want empty", textErr.String())
	}
	if !strings.Contains(textOut.String(), "config.init") || strings.HasPrefix(strings.TrimSpace(textOut.String()), "{") {
		t.Fatalf("config init dry-run text = %q, want non-JSON plan", textOut.String())
	}

	var usageOut, usageErr bytes.Buffer
	err := ExecuteWithIO([]string{"--output=json", "config"}, IOStreams{Out: &usageOut, Err: &usageErr})
	if err == nil || !errors.Is(err, errUsage) {
		t.Fatalf("config JSON usage error = %v, want errUsage", err)
	}
	if usageErr.Len() != 0 {
		t.Fatalf("config JSON usage stderr = %q, want empty", usageErr.String())
	}
	var usageEnvelope struct {
		OK      bool       `json:"ok"`
		Command string     `json:"command"`
		Error   *jsonError `json:"error"`
	}
	decodeSingleJSONEnvelope(t, usageOut.Bytes(), &usageEnvelope)
	if usageEnvelope.OK || usageEnvelope.Command != "config" || usageEnvelope.Error == nil || usageEnvelope.Error.Code != "USAGE_ERROR" {
		t.Fatalf("config usage envelope = %+v, want USAGE_ERROR", usageEnvelope)
	}
}

func loadCommandConfigFamilyPreflightEvidence(t *testing.T) commandConfigFamilyPreflightEvidence {
	t.Helper()
	return commandConfigFamilyPreflightEvidence{
		Schema:         "gofly.command_config_family_preflight.v1",
		Status:         "completed-config-family-preflight",
		Package:        "cmd/gofly/internal/command",
		AcceptanceGate: "make command-config-family-preflight-check",
		PreflightOnly:  true,
		NoPhysicalMove: true,
		SelectedFamily: commandConfigFamilyPreflightFamily{
			ID:             "config",
			Status:         "completed-config-family-preflight",
			CurrentPackage: "cmd/gofly/internal/command",
			FuturePackage:  "cmd/gofly/internal/command/config",
			Files: []string{
				"config_command.go",
				"config_fields.go",
				"config_field_setters.go",
				"config_field_helpers.go",
			},
			BlockedMoves: []string{
				"do not move JSON error helpers",
				"do not move global output or IO helpers",
				"do not move generator config helpers",
				"do not move any non-config command family",
			},
			RollbackRequirement: "Keep config files in cmd/gofly/internal/command if registration, help, or JSON behavior drifts",
		},
		Contracts: commandConfigFamilyPreflightContract{
			CommandRegistration: commandConfigFamilyCommandRegistration{
				RootCommand:   "config",
				ChildCommands: []string{"init", "show", "get", "set", "clean"},
				ManifestEntry: "config",
			},
			HelpRouting: commandConfigFamilyHelpRouting{
				Topics:     []string{"config", "config init", "config show", "config get", "config set", "config clean"},
				Usage:      "gofly config init|show|get|set|clean [flags]",
				StdoutOnly: true,
			},
			JSONEnvelope: commandConfigFamilyJSONEnvelope{
				PlanCommands:           []string{"config.init", "config.set", "config.clean"},
				ErrorCode:              "USAGE_ERROR",
				RequiredEnvelopeFields: []string{"ok", "command", "version", "data", "error"},
				NoImplicitJSON:         true,
				NoDuplicateGlobalJSON:  true,
			},
			PathBoundary: commandConfigFamilyPathBoundary{
				ConfigFileConstant: generator.DefaultConfigFile,
				UsesGeneratorPath:  true,
			},
			LocalExecutionBoundary: commandConfigFamilyLocalBoundary{
				Status:      "preflight-complete-files-remain-in-command",
				RunnerFiles: []string{"config_command.go"},
				FieldFiles:  []string{"config_fields.go", "config_field_setters.go", "config_field_helpers.go"},
			},
			OutputDiscipline: commandConfigFamilyOutputDiscipline{
				JSONStdoutOnly:               true,
				ErrorsDoNotEmitDuplicateJSON: true,
				TextOutputUsesCommandIO:      true,
				NoImplicitJSON:               true,
			},
		},
		PhysicalSplitAdmission: commandConfigFamilyPhysicalAdmission{
			Status:   "preflight-complete-physical-split-not-authorized",
			NextStep: "copy help/doctor/release adapter pattern only after an authorized P22-19 change",
			RequiredSignals: []string{
				"config command remains registered",
				"config dry-run JSON remains stdout-only",
				"config text commands stay non-JSON by default",
			},
			RequiredGates: []string{"go test ./cmd/gofly/internal/command -run TestCommandConfigFamilyPreflight"},
		},
		GoldenTests: []string{
			"TestCommandConfigFamilyPreflightEvidence",
			"TestCommandConfigFamilyPreflightContracts",
		},
		RequiredGates: []string{
			"make command-config-family-preflight-check",
			"make command-next-family-candidate-refresh-check",
			"make command-family-dependency-map-check",
			"make cli-json-contract-goldens-check",
			"make cli-configuration-governance-check",
			"make required-checks-drift-check",
		},
		NextStep: commandConfigFamilyNextStep{
			ID:     "P22-19-command-config-family-split",
			Action: "do not move config command files until a dedicated physical-split change is authorized",
		},
	}
}

func manifestContainsConfig() bool {
	for _, entry := range rootCommandManifestEntries() {
		if entry.Name == "config" {
			return true
		}
	}
	return false
}

func decodeSingleJSONEnvelope(t *testing.T, raw []byte, dest any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(dest); err != nil {
		t.Fatalf("decode JSON envelope: %v\n%s", err, raw)
	}
	if decoder.More() {
		t.Fatalf("duplicate JSON envelopes:\n%s", raw)
	}
}

func assertConfigPreflightSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsConfigPreflightString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsConfigPreflightString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
