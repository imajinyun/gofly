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

type cliJSONGoldenManifest struct {
	Schema         string              `json:"schema"`
	AcceptanceGate string              `json:"acceptanceGate"`
	Cases          []cliJSONGoldenCase `json:"cases"`
}

type cliJSONGoldenCase struct {
	ID                     string   `json:"id"`
	Command                string   `json:"command"`
	Mode                   string   `json:"mode"`
	RequiredEnvelopeFields []string `json:"requiredEnvelopeFields"`
	RequiredDataFields     []string `json:"requiredDataFields"`
	RequiredFields         []string `json:"requiredFields"`
	RequiredErrorFields    []string `json:"requiredErrorFields"`
}

func TestCLIJSONContractGoldens(t *testing.T) {
	manifest := loadCLIJSONGoldenManifest(t)
	if manifest.Schema != "gofly.cli_json_contract_goldens.v1" {
		t.Fatalf("schema = %q, want gofly.cli_json_contract_goldens.v1", manifest.Schema)
	}
	if manifest.AcceptanceGate != "make cli-json-contract-goldens-check" {
		t.Fatalf("acceptanceGate = %q, want make cli-json-contract-goldens-check", manifest.AcceptanceGate)
	}

	cases := map[string]func(*testing.T) []byte{
		"version-envelope":          runVersionJSONGolden,
		"doctor-raw":                runDoctorJSONGolden,
		"release-check-envelope":    runReleaseCheckJSONGolden,
		"new-service-plan-envelope": runNewServicePlanJSONGolden,
		"api-gen-envelope":          runAPIGenJSONGolden,
		"rpc-gen-envelope":          runRPCGenJSONGolden,
		"model-gen-envelope":        runModelGenJSONGolden,
	}
	for _, item := range manifest.Cases {
		if item.Mode == "error-envelope" {
			continue
		}
		run, ok := cases[item.ID]
		if !ok {
			t.Fatalf("golden case %q has no executable test", item.ID)
		}
		t.Run(item.ID, func(t *testing.T) {
			stdout := run(t)
			value := decodeJSONObject(t, stdout)
			assertNoTextLeak(t, item.ID, stdout)
			switch item.Mode {
			case "envelope":
				assertRequiredFields(t, item.ID, value, item.RequiredEnvelopeFields)
				if item.ID != "release-check-envelope" && value["ok"] != true {
					t.Fatalf("%s ok = %v, want true", item.ID, value["ok"])
				}
				if item.ID == "release-check-envelope" {
					if _, ok := value["ok"].(bool); !ok {
						t.Fatalf("%s ok = %T, want bool", item.ID, value["ok"])
					}
				}
				if command, ok := value["command"].(string); !ok || command == "" {
					t.Fatalf("%s command = %v, want non-empty string", item.ID, value["command"])
				}
				data, ok := value["data"].(map[string]any)
				if !ok {
					t.Fatalf("%s data = %T, want object", item.ID, value["data"])
				}
				assertRequiredFields(t, item.ID+".data", data, item.RequiredDataFields)
			case "raw":
				assertRequiredFields(t, item.ID, value, item.RequiredFields)
			default:
				t.Fatalf("%s mode = %q, want envelope/raw", item.ID, item.Mode)
			}
		})
	}
}

func TestCLIJSONErrorEnvelopeGolden(t *testing.T) {
	manifest := loadCLIJSONGoldenManifest(t)
	var errorCase cliJSONGoldenCase
	for _, item := range manifest.Cases {
		if item.ID == "global-error-envelope" {
			errorCase = item
			break
		}
	}
	if errorCase.ID == "" {
		t.Fatal("global-error-envelope case missing from CLI JSON golden manifest")
	}

	var stdout, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"--output", "json", "example", "nonexistent"}, IOStreams{Out: &stdout, Err: &stderr})
	if err == nil {
		t.Fatal("global JSON unknown example returned nil, want usage error")
	}
	if stderr.Len() != 0 {
		t.Fatalf("global JSON error wrote stderr = %q, want empty", stderr.String())
	}
	if strings.Count(stdout.String(), `"ok"`) != 1 {
		t.Fatalf("global JSON error emitted duplicate envelopes:\n%s", stdout.String())
	}
	value := decodeJSONObject(t, stdout.Bytes())
	assertRequiredFields(t, errorCase.ID, value, errorCase.RequiredEnvelopeFields)
	if value["ok"] != false {
		t.Fatalf("global error ok = %v, want false", value["ok"])
	}
	errorObject, ok := value["error"].(map[string]any)
	if !ok {
		t.Fatalf("global error envelope error = %T, want object", value["error"])
	}
	assertRequiredFields(t, errorCase.ID+".error", errorObject, errorCase.RequiredErrorFields)
	if code, ok := errorObject["code"].(string); !ok || code == "" {
		t.Fatalf("global error code = %v, want non-empty string", errorObject["code"])
	}
}

func loadCLIJSONGoldenManifest(t *testing.T) cliJSONGoldenManifest {
	t.Helper()
	repoRoot := filepath.Join("..", "..", "..", "..")
	path := filepath.Join(repoRoot, "docs", "reference", "cli-json-contract-goldens.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CLI JSON golden manifest: %v", err)
	}
	var manifest cliJSONGoldenManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode CLI JSON golden manifest: %v", err)
	}
	return manifest
}

func runVersionJSONGolden(t *testing.T) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := ExecuteWithIO([]string{"version", "--json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
		t.Fatalf("version --json: %v", err)
	}
	assertNoJSONCommandStderr(t, "version --json", stderr)
	return stdout.Bytes()
}

func runDoctorJSONGolden(t *testing.T) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := ExecuteWithIO([]string{"doctor", "--json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	assertNoJSONCommandStderr(t, "doctor --json", stderr)
	return stdout.Bytes()
}

func runReleaseCheckJSONGolden(t *testing.T) []byte {
	t.Helper()
	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\nUnreleased notes only.\n"), 0o600); err != nil {
		t.Fatalf("write changelog: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"release", "check", "--changelog", changelog, "--json"}, IOStreams{Out: &stdout, Err: &stderr})
	if err != nil && !errors.Is(err, errJSONAlreadyReported) {
		t.Fatalf("release check --json: %v", err)
	}
	assertNoJSONCommandStderr(t, "release check --json", stderr)
	return stdout.Bytes()
}

func runNewServicePlanJSONGolden(t *testing.T) []byte {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "orders")
	var stdout, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"new", "service", "orders", "--module", "example.com/orders", "--dir", dir, "--dry-run", "--json"}, IOStreams{Out: &stdout, Err: &stderr})
	if err != nil {
		t.Fatalf("new service --json --dry-run: %v", err)
	}
	assertNoJSONCommandStderr(t, "new service --json --dry-run", stderr)
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new service --dry-run wrote go.mod or stat failed: %v", err)
	}
	return stdout.Bytes()
}

func runAPIGenJSONGolden(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	if err := os.WriteFile(apiPath, []byte(commandTestAPI), 0o600); err != nil {
		t.Fatalf("write api fixture: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"api", "gen", "--file", apiPath, "--dir", outDir, "--package", "handler", "--json"}, IOStreams{Out: &stdout, Err: &stderr})
	if err != nil {
		t.Fatalf("api gen --json: %v", err)
	}
	assertNoJSONCommandStderr(t, "api gen --json", stderr)
	return stdout.Bytes()
}

func runRPCGenJSONGolden(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o600); err != nil {
		t.Fatalf("write proto fixture: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"rpc", "gen", "--file", protoPath, "--dir", outDir, "--package", "greeterv1", "--json"}, IOStreams{Out: &stdout, Err: &stderr})
	if err != nil {
		t.Fatalf("rpc gen --json: %v", err)
	}
	assertNoJSONCommandStderr(t, "rpc gen --json", stderr)
	return stdout.Bytes()
}

func runModelGenJSONGolden(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := "CREATE TABLE users (id BIGINT PRIMARY KEY, name VARCHAR(64) NOT NULL);\n"
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o600); err != nil {
		t.Fatalf("write ddl fixture: %v", err)
	}
	outDir := filepath.Join(dir, "out")
	var stdout, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"model", "gen", "--ddl", ddlPath, "--dir", outDir, "--module", "example.com/model", "--json"}, IOStreams{Out: &stdout, Err: &stderr})
	if err != nil {
		t.Fatalf("model gen --json: %v", err)
	}
	assertNoJSONCommandStderr(t, "model gen --json", stderr)
	return stdout.Bytes()
}

func decodeJSONObject(t *testing.T, data []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("output is not a JSON object: %v\n%s", err, string(data))
	}
	if decoder.More() {
		t.Fatalf("output contains more than one JSON value:\n%s", string(data))
	}
	return value
}

func assertRequiredFields(t *testing.T, label string, value map[string]any, fields []string) {
	t.Helper()
	for _, field := range fields {
		if _, ok := value[field]; !ok {
			t.Fatalf("%s missing field %q in %#v", label, field, value)
		}
	}
}

func assertNoJSONCommandStderr(t *testing.T, command string, stderr bytes.Buffer) {
	t.Helper()
	if stderr.Len() != 0 {
		t.Fatalf("%s wrote stderr = %q, want empty", command, stderr.String())
	}
}

func assertNoTextLeak(t *testing.T, label string, data []byte) {
	t.Helper()
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		t.Fatalf("%s emitted empty JSON output", label)
	}
	if !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("%s output does not start with JSON object: %q", label, trimmed)
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		for _, marker := range []string{"Usage:", "[gofly]", "warning:"} {
			if strings.HasPrefix(line, marker) {
				t.Fatalf("%s output leaked text line %q:\n%s", label, marker, trimmed)
			}
		}
	}
}
