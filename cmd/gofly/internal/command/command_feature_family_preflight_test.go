package command

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandFeatureFamilyPreflightEvidence struct {
	Schema                 string                                `json:"schema"`
	Status                 string                                `json:"status"`
	Package                string                                `json:"package"`
	AcceptanceGate         string                                `json:"acceptanceGate"`
	PreflightOnly          bool                                  `json:"preflightOnly"`
	NoPhysicalMove         bool                                  `json:"noPhysicalMove"`
	SelectedFamily         commandFeatureFamilyPreflightFamily   `json:"selectedFamily"`
	Contracts              commandFeatureFamilyPreflightContract `json:"contracts"`
	PhysicalSplitAdmission commandFeatureFamilyPhysicalAdmission `json:"physicalSplitAdmission"`
	GoldenTests            []string                              `json:"goldenTests"`
	RequiredGates          []string                              `json:"requiredGates"`
	NextStep               commandFeatureFamilyNextStep          `json:"nextStep"`
}

type commandFeatureFamilyPreflightFamily struct {
	ID                  string   `json:"id"`
	Status              string   `json:"status"`
	CurrentPackage      string   `json:"currentPackage"`
	FuturePackage       string   `json:"futurePackage"`
	Files               []string `json:"files"`
	BlockedMoves        []string `json:"blockedMoves"`
	RollbackRequirement string   `json:"rollbackRequirement"`
}

type commandFeatureFamilyPreflightContract struct {
	CommandRegistration    commandFeatureFamilyCommandRegistration `json:"commandRegistration"`
	HelpRouting            commandFeatureFamilyHelpRouting         `json:"helpRouting"`
	JSONEnvelope           commandFeatureFamilyJSONEnvelope        `json:"jsonEnvelope"`
	LocalExecutionBoundary commandFeatureFamilyLocalBoundary       `json:"localExecutionBoundary"`
	OutputDiscipline       commandFeatureFamilyOutputDiscipline    `json:"outputDiscipline"`
}

type commandFeatureFamilyCommandRegistration struct {
	RootCommand   string   `json:"rootCommand"`
	ChildCommands []string `json:"childCommands"`
	ListAlias     string   `json:"listAlias"`
	ManifestEntry string   `json:"manifestEntry"`
}

type commandFeatureFamilyHelpRouting struct {
	Topics     []string `json:"topics"`
	Usage      string   `json:"usage"`
	StdoutOnly bool     `json:"stdoutOnly"`
}

type commandFeatureFamilyJSONEnvelope struct {
	ListCommand            string   `json:"listCommand"`
	RunCommand             string   `json:"runCommand"`
	UsageErrorCode         string   `json:"usageErrorCode"`
	UnknownFeatureCode     string   `json:"unknownFeatureCode"`
	RequiredEnvelopeFields []string `json:"requiredEnvelopeFields"`
	RequiredListDataFields []string `json:"requiredListDataFields"`
	RequiredRunDataFields  []string `json:"requiredRunDataFields"`
	NoDuplicateGlobalJSON  bool     `json:"noDuplicateGlobalJson"`
	PreviewDoesNotWrite    bool     `json:"previewDoesNotWrite"`
}

type commandFeatureFamilyLocalBoundary struct {
	Status         string   `json:"status"`
	RunnerFiles    []string `json:"runnerFiles"`
	RenderingFiles []string `json:"renderingFiles"`
}

type commandFeatureFamilyOutputDiscipline struct {
	JSONStdoutOnly               bool `json:"jsonStdoutOnly"`
	ErrorsDoNotEmitDuplicateJSON bool `json:"errorsDoNotEmitDuplicateJson"`
	TextOutputUsesCommandIO      bool `json:"textOutputUsesCommandIO"`
	PreviewOnly                  bool `json:"previewOnly"`
}

type commandFeatureFamilyPhysicalAdmission struct {
	Status          string   `json:"status"`
	NextStep        string   `json:"nextStep"`
	RequiredSignals []string `json:"requiredSignals"`
	RequiredGates   []string `json:"requiredGates"`
}

type commandFeatureFamilyNextStep struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

func TestCommandFeatureFamilyPreflightEvidence(t *testing.T) {
	evidence := loadCommandFeatureFamilyPreflightEvidence(t)
	if evidence.Schema != "gofly.command_feature_family_preflight.v1" {
		t.Fatalf("schema = %q, want gofly.command_feature_family_preflight.v1", evidence.Schema)
	}
	if evidence.Status != "completed-feature-family-preflight" {
		t.Fatalf("status = %q, want completed-feature-family-preflight", evidence.Status)
	}
	if evidence.Package != "cmd/gofly/internal/command" {
		t.Fatalf("package = %q, want command package", evidence.Package)
	}
	if evidence.AcceptanceGate != "make command-feature-family-preflight-check" {
		t.Fatalf("acceptanceGate = %q, want make command-feature-family-preflight-check", evidence.AcceptanceGate)
	}
	if !evidence.PreflightOnly || !evidence.NoPhysicalMove {
		t.Fatalf("feature preflight must stay planning-only: preflightOnly=%t noPhysicalMove=%t", evidence.PreflightOnly, evidence.NoPhysicalMove)
	}
	assertFeaturePreflightSet(t, "golden tests", evidence.GoldenTests, []string{
		"TestCommandFeatureFamilyPreflightEvidence",
		"TestCommandFeatureFamilyPreflightContracts",
	})
	for _, want := range []string{
		"make command-feature-family-preflight-check",
		"make command-next-family-candidate-refresh-check",
		"make command-family-dependency-map-check",
		"make cli-json-contract-goldens-check",
		"make required-checks-drift-check",
	} {
		if !containsFeaturePreflightString(evidence.RequiredGates, want) {
			t.Fatalf("requiredGates missing %q: %v", want, evidence.RequiredGates)
		}
	}
	if evidence.PhysicalSplitAdmission.Status != "preflight-complete-physical-split-not-authorized" {
		t.Fatalf("physicalSplitAdmission.status = %q, want preflight-complete-physical-split-not-authorized", evidence.PhysicalSplitAdmission.Status)
	}
	if evidence.NextStep.ID != "P22-21-command-feature-family-split" {
		t.Fatalf("nextStep.id = %q, want P22-21-command-feature-family-split", evidence.NextStep.ID)
	}
	if !strings.Contains(evidence.NextStep.Action, "until a dedicated physical-split change is authorized") {
		t.Fatalf("nextStep.action = %q, want unauthorized physical split hold", evidence.NextStep.Action)
	}
}

func TestCommandFeatureFamilyPreflightContracts(t *testing.T) {
	evidence := loadCommandFeatureFamilyPreflightEvidence(t)
	family := evidence.SelectedFamily
	if family.ID != "feature" || family.Status != "completed-feature-family-preflight" {
		t.Fatalf("selected family = %q/%q, want feature/completed-feature-family-preflight", family.ID, family.Status)
	}
	if family.CurrentPackage != "cmd/gofly/internal/command" || family.FuturePackage != "cmd/gofly/internal/command/feature" {
		t.Fatalf("selected family packages = %q -> %q", family.CurrentPackage, family.FuturePackage)
	}
	assertFeaturePreflightSet(t, "feature files", family.Files, []string{
		"feature_command.go",
		"feature_preview.go",
	})
	commandDir := filepath.Join("..", "..", "..", "..", "cmd", "gofly", "internal", "command")
	if _, err := os.Stat(filepath.Join(commandDir, "feature")); !os.IsNotExist(err) {
		t.Fatalf("feature subpackage must not exist during P22-20 preflight, stat err=%v", err)
	}
	for _, filename := range family.Files {
		if _, err := os.Stat(filepath.Join(commandDir, filename)); err != nil {
			t.Fatalf("feature family file %s must remain in command package: %v", filename, err)
		}
	}
	for _, want := range []string{
		"do not move JSON error helpers",
		"do not move global output or IO helpers",
		"do not move generator feature helpers",
		"do not move any non-feature command family",
	} {
		if !containsFeaturePreflightString(family.BlockedMoves, want) {
			t.Fatalf("blockedMoves missing %q: %v", want, family.BlockedMoves)
		}
	}
	if !strings.Contains(family.RollbackRequirement, "Keep feature files") {
		t.Fatalf("rollbackRequirement = %q, want keep-in-place guidance", family.RollbackRequirement)
	}

	contracts := evidence.Contracts
	if contracts.CommandRegistration.RootCommand != "feature" || contracts.CommandRegistration.ListAlias != "ls" {
		t.Fatalf("command registration = %+v, want feature with ls alias", contracts.CommandRegistration)
	}
	assertFeaturePreflightSet(t, "feature children", contracts.CommandRegistration.ChildCommands, []string{"list", "run"})
	if !rootRegistryContains("feature") {
		t.Fatalf("root registry must contain feature command")
	}
	if !manifestContainsFeature() {
		t.Fatalf("root command manifest must contain feature")
	}

	assertFeaturePreflightSet(t, "help topics", contracts.HelpRouting.Topics, []string{
		"feature", "feature list", "feature run",
	})
	if commandUsage("feature") == "" || commandUsage("feature run") == "" {
		t.Fatalf("feature help routing missing usage")
	}
	if !contracts.HelpRouting.StdoutOnly {
		t.Fatal("help routing must stay stdout-only")
	}

	var helpOut, helpErr bytes.Buffer
	if err := ExecuteWithIO([]string{"help", "feature", "run"}, IOStreams{Out: &helpOut, Err: &helpErr}); err != nil {
		t.Fatalf("help feature run: %v", err)
	}
	if !strings.Contains(helpOut.String(), "feature run") {
		t.Fatalf("help feature run stdout = %q, want usage", helpOut.String())
	}
	if helpErr.Len() != 0 {
		t.Fatalf("help feature run stderr = %q, want empty", helpErr.String())
	}

	jsonContract := contracts.JSONEnvelope
	if jsonContract.ListCommand != "feature.list" || jsonContract.RunCommand != "feature.run" {
		t.Fatalf("json commands = %+v, want feature.list/feature.run", jsonContract)
	}
	if jsonContract.UsageErrorCode != "USAGE_ERROR" || jsonContract.UnknownFeatureCode != "FEATURE_NOT_REGISTERED" {
		t.Fatalf("json error codes = %+v", jsonContract)
	}
	if !jsonContract.NoDuplicateGlobalJSON || !jsonContract.PreviewDoesNotWrite {
		t.Fatalf("json contract = %+v, want duplicate-safe preview-only contract", jsonContract)
	}
	assertFeaturePreflightSet(t, "envelope fields", jsonContract.RequiredEnvelopeFields, []string{"ok", "command", "version", "data", "error"})
	assertFeaturePreflightSet(t, "list data fields", jsonContract.RequiredListDataFields, []string{"features"})
	assertFeaturePreflightSet(t, "run data fields", jsonContract.RequiredRunDataFields, []string{"features", "files"})
	if contracts.LocalExecutionBoundary.Status != "preflight-complete-files-remain-in-command" {
		t.Fatalf("localExecutionBoundary.status = %q", contracts.LocalExecutionBoundary.Status)
	}
	assertFeaturePreflightSet(t, "runner files", contracts.LocalExecutionBoundary.RunnerFiles, []string{"feature_command.go"})
	assertFeaturePreflightSet(t, "rendering files", contracts.LocalExecutionBoundary.RenderingFiles, []string{"feature_preview.go"})
	if !contracts.OutputDiscipline.JSONStdoutOnly || !contracts.OutputDiscipline.ErrorsDoNotEmitDuplicateJSON || !contracts.OutputDiscipline.TextOutputUsesCommandIO || !contracts.OutputDiscipline.PreviewOnly {
		t.Fatalf("output discipline = %+v, want all true", contracts.OutputDiscipline)
	}

	var listOut, listErr bytes.Buffer
	if err := ExecuteWithIO([]string{"feature", "list", "--json"}, IOStreams{Out: &listOut, Err: &listErr}); err != nil {
		t.Fatalf("feature list --json: %v", err)
	}
	if listErr.Len() != 0 {
		t.Fatalf("feature list --json stderr = %q, want empty", listErr.String())
	}
	var listEnvelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Features []string `json:"features"`
		} `json:"data"`
		Error *jsonError `json:"error"`
	}
	decodeSingleJSONEnvelope(t, listOut.Bytes(), &listEnvelope)
	if !listEnvelope.OK || listEnvelope.Command != "feature.list" || listEnvelope.Error != nil {
		t.Fatalf("feature list envelope = %+v, want feature.list", listEnvelope)
	}
	if !containsFeaturePreflightString(listEnvelope.Data.Features, "ecosystem-compat") {
		t.Fatalf("feature list features = %v, want ecosystem-compat", listEnvelope.Data.Features)
	}

	var textOut, textErr bytes.Buffer
	if err := ExecuteWithIO([]string{"feature", "list"}, IOStreams{Out: &textOut, Err: &textErr}); err != nil {
		t.Fatalf("feature list text: %v", err)
	}
	if textErr.Len() != 0 {
		t.Fatalf("feature list text stderr = %q, want empty", textErr.String())
	}
	if !strings.Contains(textOut.String(), "ecosystem-compat") || strings.HasPrefix(strings.TrimSpace(textOut.String()), "{") {
		t.Fatalf("feature list text = %q, want non-JSON names", textOut.String())
	}

	dir := t.TempDir()
	var runOut, runErr bytes.Buffer
	if err := ExecuteWithIO([]string{"feature", "run", "ecosystem-compat", "--name", "hello", "--module", "example.com/hello", "--dir", dir, "--json"}, IOStreams{Out: &runOut, Err: &runErr}); err != nil {
		t.Fatalf("feature run --json: %v", err)
	}
	if runErr.Len() != 0 {
		t.Fatalf("feature run --json stderr = %q, want empty", runErr.String())
	}
	var runEnvelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Features []string `json:"features"`
			Files    []struct {
				Path  string `json:"path"`
				Bytes int    `json:"bytes"`
			} `json:"files"`
		} `json:"data"`
		Error *jsonError `json:"error"`
	}
	decodeSingleJSONEnvelope(t, runOut.Bytes(), &runEnvelope)
	if !runEnvelope.OK || runEnvelope.Command != "feature.run" || runEnvelope.Error != nil {
		t.Fatalf("feature run envelope = %+v, want feature.run", runEnvelope)
	}
	if !containsFeaturePreflightString(runEnvelope.Data.Features, "ecosystem-compat") || len(runEnvelope.Data.Files) == 0 {
		t.Fatalf("feature run preview = %+v, want files", runEnvelope.Data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read preview dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("feature run preview wrote %d entries under %s, want none", len(entries), dir)
	}

	var usageOut, usageErr bytes.Buffer
	err = ExecuteWithIO([]string{"feature", "run", "--json"}, IOStreams{Out: &usageOut, Err: &usageErr})
	if err == nil || !errors.Is(err, errUsage) {
		t.Fatalf("feature run JSON usage error = %v, want errUsage", err)
	}
	if usageErr.Len() != 0 {
		t.Fatalf("feature run JSON usage stderr = %q, want empty", usageErr.String())
	}
	var usageEnvelope struct {
		OK      bool       `json:"ok"`
		Command string     `json:"command"`
		Error   *jsonError `json:"error"`
	}
	decodeSingleJSONEnvelope(t, usageOut.Bytes(), &usageEnvelope)
	if usageEnvelope.OK || usageEnvelope.Command != "feature.run" || usageEnvelope.Error == nil || usageEnvelope.Error.Code != "USAGE_ERROR" {
		t.Fatalf("feature run usage envelope = %+v, want USAGE_ERROR", usageEnvelope)
	}

	var unknownOut, unknownErr bytes.Buffer
	err = ExecuteWithIO([]string{"feature", "run", "missing-feature", "--json"}, IOStreams{Out: &unknownOut, Err: &unknownErr})
	if err == nil {
		t.Fatal("feature run missing-feature --json error = nil, want FEATURE_NOT_REGISTERED")
	}
	if unknownErr.Len() != 0 {
		t.Fatalf("feature run unknown JSON stderr = %q, want empty", unknownErr.String())
	}
	var unknownEnvelope struct {
		OK      bool       `json:"ok"`
		Command string     `json:"command"`
		Error   *jsonError `json:"error"`
	}
	decodeSingleJSONEnvelope(t, unknownOut.Bytes(), &unknownEnvelope)
	if unknownEnvelope.OK || unknownEnvelope.Command != "feature.run" || unknownEnvelope.Error == nil || unknownEnvelope.Error.Code != "FEATURE_NOT_REGISTERED" {
		t.Fatalf("feature run unknown envelope = %+v, want FEATURE_NOT_REGISTERED", unknownEnvelope)
	}
}

func loadCommandFeatureFamilyPreflightEvidence(t *testing.T) commandFeatureFamilyPreflightEvidence {
	t.Helper()
	return commandFeatureFamilyPreflightEvidence{
		Schema:         "gofly.command_feature_family_preflight.v1",
		Status:         "completed-feature-family-preflight",
		Package:        "cmd/gofly/internal/command",
		AcceptanceGate: "make command-feature-family-preflight-check",
		PreflightOnly:  true,
		NoPhysicalMove: true,
		SelectedFamily: commandFeatureFamilyPreflightFamily{
			ID:             "feature",
			Status:         "completed-feature-family-preflight",
			CurrentPackage: "cmd/gofly/internal/command",
			FuturePackage:  "cmd/gofly/internal/command/feature",
			Files: []string{
				"feature_command.go",
				"feature_preview.go",
			},
			BlockedMoves: []string{
				"do not move JSON error helpers",
				"do not move global output or IO helpers",
				"do not move generator feature helpers",
				"do not move any non-feature command family",
			},
			RollbackRequirement: "Keep feature files in cmd/gofly/internal/command if registration, help, or JSON behavior drifts",
		},
		Contracts: commandFeatureFamilyPreflightContract{
			CommandRegistration: commandFeatureFamilyCommandRegistration{
				RootCommand:   "feature",
				ChildCommands: []string{"list", "run"},
				ListAlias:     "ls",
				ManifestEntry: "feature",
			},
			HelpRouting: commandFeatureFamilyHelpRouting{
				Topics:     []string{"feature", "feature list", "feature run"},
				Usage:      "gofly feature list | gofly feature run <feature> [features...] [flags]",
				StdoutOnly: true,
			},
			JSONEnvelope: commandFeatureFamilyJSONEnvelope{
				ListCommand:            "feature.list",
				RunCommand:             "feature.run",
				UsageErrorCode:         "USAGE_ERROR",
				UnknownFeatureCode:     "FEATURE_NOT_REGISTERED",
				RequiredEnvelopeFields: []string{"ok", "command", "version", "data", "error"},
				RequiredListDataFields: []string{"features"},
				RequiredRunDataFields:  []string{"features", "files"},
				NoDuplicateGlobalJSON:  true,
				PreviewDoesNotWrite:    true,
			},
			LocalExecutionBoundary: commandFeatureFamilyLocalBoundary{
				Status:         "preflight-complete-files-remain-in-command",
				RunnerFiles:    []string{"feature_command.go"},
				RenderingFiles: []string{"feature_preview.go"},
			},
			OutputDiscipline: commandFeatureFamilyOutputDiscipline{
				JSONStdoutOnly:               true,
				ErrorsDoNotEmitDuplicateJSON: true,
				TextOutputUsesCommandIO:      true,
				PreviewOnly:                  true,
			},
		},
		PhysicalSplitAdmission: commandFeatureFamilyPhysicalAdmission{
			Status:   "preflight-complete-physical-split-not-authorized",
			NextStep: "copy help/doctor/release/config adapter pattern only after an authorized P22-21 change",
			RequiredSignals: []string{
				"feature command remains registered",
				"feature list JSON remains stdout-only",
				"feature run stays preview-only and does not write files",
			},
			RequiredGates: []string{"go test ./cmd/gofly/internal/command -run TestCommandFeatureFamilyPreflight"},
		},
		GoldenTests: []string{
			"TestCommandFeatureFamilyPreflightEvidence",
			"TestCommandFeatureFamilyPreflightContracts",
		},
		RequiredGates: []string{
			"make command-feature-family-preflight-check",
			"make command-next-family-candidate-refresh-check",
			"make command-family-dependency-map-check",
			"make cli-json-contract-goldens-check",
			"make required-checks-drift-check",
		},
		NextStep: commandFeatureFamilyNextStep{
			ID:     "P22-21-command-feature-family-split",
			Action: "do not move feature command files until a dedicated physical-split change is authorized",
		},
	}
}

func manifestContainsFeature() bool {
	for _, entry := range rootCommandManifestEntries() {
		if entry.Name == "feature" {
			return true
		}
	}
	return false
}

func assertFeaturePreflightSet(t *testing.T, label string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d: %v", label, len(got), len(want), got)
	}
	for _, item := range want {
		if !containsFeaturePreflightString(got, item) {
			t.Fatalf("%s missing %q: %v", label, item, got)
		}
	}
}

func containsFeaturePreflightString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
