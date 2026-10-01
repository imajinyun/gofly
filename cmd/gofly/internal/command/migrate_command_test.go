package command

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestExecuteMigrateGenerationContract(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "schema.sql")
	out := filepath.Join(dir, "migrations")
	if err := os.WriteFile(input, []byte("CREATE TABLE users(id BIGINT PRIMARY KEY);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"--output", "json", "migrate", "gen", "init", "--from-ddl", input, "--dialect", "mysql", "--dir", out, "--version", "1"}, IOStreams{Out: &stdout, Err: &stderr})
	if err != nil {
		t.Fatalf("gen: %v; stdout=%s stderr=%s", err, &stdout, &stderr)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Files []string `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || !envelope.OK || len(envelope.Data.Files) != 2 {
		t.Fatalf("response = %s, %v", &stdout, err)
	}
	stdout.Reset()
	if err := ExecuteWithIO([]string{"migrate", "validate", "--dir", out, "--json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatalf("not JSON: %s", &stdout)
	}
}

func TestMigrationManifestExecutionBoundary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := ExecuteWithIO([]string{"ai", "manifest", "--json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data struct {
			Commands []struct {
				Name           string   `json:"name"`
				RiskLevel      string   `json:"riskLevel"`
				SideEffects    []string `json:"sideEffects"`
				SupportsDryRun bool     `json:"supportsDryRun"`
			} `json:"commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migrate up", "migrate down"} {
		found := false
		for _, command := range envelope.Data.Commands {
			if command.Name == name {
				found = true
				if command.RiskLevel != "high" || command.SupportsDryRun || !slices.Contains(command.SideEffects, "executes SQL and updates database migration history") {
					t.Fatalf("unsafe manifest contract: %+v", command)
				}
			}
		}
		if !found {
			t.Fatalf("missing manifest command %s", name)
		}
	}
}

func TestExecuteMigrateUsage(t *testing.T) {
	for _, args := range [][]string{
		{"migrate", "gen", "init"},
		{"migrate", "gen", "init", "--from-ddl", "schema.sql", "--dialect", "unknown"},
		{"migrate", "down", "--driver", "mysql"},
		{"migrate", "down", "--driver", "mysql", "--steps", "0"},
		{"migrate", "up", "--driver", "unknown"},
		{"migrate", "validate", "unexpected"},
		{"migrate", "create", "init", "--version", "-1"},
	} {
		t.Run(commandName(args)+args[len(args)-1], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := ExecuteWithIO(args, IOStreams{Out: &stdout, Err: &stderr})
			if ExitCode(err) != 2 {
				t.Fatalf("args %v: err=%v, exit=%d; want 2", args, err, ExitCode(err))
			}
		})
	}
}
