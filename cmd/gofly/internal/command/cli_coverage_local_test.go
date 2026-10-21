package command

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCLICoverageCleanupRepeatablePlan(t *testing.T) {
	dir := t.TempDir()
	const stale = "internal/api/http/obsolete.go"
	const missing = "internal/app/removed.go"
	cliCoverageFile(t, dir, stale, "package http\n")
	untouched := cliCoverageFile(t, dir, "internal/app/keep.go", "package app\n")
	report := `{"schema":"gofly.gozero_api_stale_files.v1","staleHandlers":[" ","` + stale + `","` + stale + `"],"staleLogics":["` + missing + `"]}`
	cliCoverageFile(t, dir, ".gofly/stale-api-files.json", report)
	// These are successive stages of one workflow, so they share one test case.
	{
		out, err := cliCoverageRun(t, "api", "cleanup", "stale", "--dir", dir, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Data struct {
				Execute    bool     `json:"execute"`
				Planned    []string `json:"planned"`
				StaleCount int      `json:"staleCount"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.Execute || envelope.Data.StaleCount != 2 || !reflect.DeepEqual(envelope.Data.Planned, []string{stale, missing}) {
			t.Fatalf("unexpected plan: %s", out)
		}
		if _, err := os.Stat(filepath.Join(dir, stale)); err != nil {
			t.Fatalf("plan removed stale file: %v", err)
		}
	}
	{
		out, err := cliCoverageRun(t, "api", "cleanup", "stale", "--dir", dir, "--execute")
		if err != nil || !strings.Contains(out, "api cleanup stale (execute): 2 stale file(s)") || !strings.Contains(out, "deleted:\n  "+stale) {
			t.Fatalf("execute result: err=%v; output=%s", err, out)
		}
		if _, err := os.Stat(filepath.Join(dir, stale)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale file still exists: %v", err)
		}
	}
	{
		out, err := cliCoverageRun(t, "api", "cleanup", "stale", "--dir", dir, "--execute", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Data struct {
				Deleted []string `json:"deleted"`
				Skipped []string `json:"skipped"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data.Deleted) != 0 || !reflect.DeepEqual(envelope.Data.Skipped, []string{stale, missing}) {
			t.Fatalf("repeat execution result = %s", out)
		}
	}
	if data, err := os.ReadFile(untouched); err != nil || string(data) != "package app\n" {
		t.Fatalf("cleanup changed unrelated file: %s; err=%v", data, err)
	}
}

func TestCLICoverageCleanupRejectsUnsafePlans(t *testing.T) {
	for _, tc := range []struct {
		name, invalid, want string
	}{
		{"not Go", "internal/app/notes.txt", "is not a .go file"},
		{"outside allowed directories", "cmd/main.go", "outside internal/api/http"},
		{"protected route registration", "internal/api/http/routes.go", "must not remove routes.go"},
		{"parent traversal", "../outside.go", "unsafe stale api file"},
		{"absolute path", "/outside.go", "unsafe stale api file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const valid = "internal/app/first.go"
			path := cliCoverageFile(t, dir, valid, "package app\n")
			report, err := json.Marshal(map[string]any{"schema": "gofly.gozero_api_stale_files.v1", "staleHandlers": []string{valid, tc.invalid}})
			if err != nil {
				t.Fatal(err)
			}
			cliCoverageFile(t, dir, ".gofly/stale-api-files.json", string(report))
			cliCoverageJSONError(t, tc.want, "api", "cleanup", "stale", "--dir", dir, "--execute")
			if data, err := os.ReadFile(path); err != nil || string(data) != "package app\n" {
				t.Fatalf("invalid plan deleted preceding valid entry: %s; err=%v", data, err)
			}
		})
	}
}

func TestCLICoverageCleanupInputFailures(t *testing.T) {
	for _, tc := range []struct {
		name, report, want string
	}{
		{"missing report", "", "read stale api report"},
		{"malformed report", "{", "decode stale api report"},
		{"unknown schema", `{"schema":"future"}`, "unsupported stale api report schema"},
		{"nonempty directory", `{"schema":"gofly.gozero_api_stale_files.v1","staleHandlers":["internal/app/stale.go"]}`, "remove stale api file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.report != "" {
				cliCoverageFile(t, dir, ".gofly/stale-api-files.json", tc.report)
			}
			child := cliCoverageFile(t, dir, "internal/app/stale.go/keep.txt", "keep")
			cliCoverageJSONError(t, tc.want, "api", "cleanup", "stale", "--dir", dir, "--execute")
			if data, err := os.ReadFile(child); err != nil || string(data) != "keep" {
				t.Fatalf("failed cleanup changed directory contents: %s; err=%v", data, err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing operation", []string{"api", "cleanup"}, "expected"},
		{"unknown operation", []string{"api", "cleanup", "everything"}, "expected"},
		{"unknown flag", []string{"api", "cleanup", "stale", "--unknown"}, "flag provided but not defined"},
	} {
		t.Run(tc.name, func(t *testing.T) { cliCoverageJSONError(t, tc.want, tc.args...) })
	}
}

func TestCLICoverageMiddlewareCatalogAndPlan(t *testing.T) {
	t.Run("text catalog", func(t *testing.T) {
		out, err := cliCoverageRun(t, "api", "middleware", "--list")
		if err != nil || !strings.Contains(out, "Available middleware presets:") || !strings.Contains(out, "websocket") || !strings.Contains(out, "install every preset") {
			t.Fatalf("catalog: err=%v; output=%s", err, out)
		}
	})
	for _, tc := range []struct {
		name  string
		quiet bool
	}{
		{"text plan", false},
		{"quiet plan", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			module := "module example.com/presetplan\n\ngo 1.26\n\nrequire github.com/imajinyun/gofly v0.1.0\n"
			cliCoverageFile(t, dir, "go.mod", module)
			args := []string{"api", "middleware", "--preset=auth,cors", "--dir", dir, "--dry-run"}
			if tc.quiet {
				args = append([]string{"--quiet"}, args...)
			}
			out, err := cliCoverageRun(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if tc.quiet && out != "" {
				t.Fatalf("quiet plan output = %q", out)
			}
			if !tc.quiet && (!strings.Contains(out, "Middleware preset plan:") || !strings.Contains(out, "auth.go") || !strings.Contains(out, "cors.go")) {
				t.Fatalf("preset plan missing planned files: %s", out)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "go.mod" {
				t.Fatalf("dry run modified project: entries=%v; err=%v", entries, err)
			}
			if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(data) != module {
				t.Fatalf("dry run changed module: %s; err=%v", data, err)
			}
		})
	}
}

func TestCLICoverageMiddlewareLocalJSONErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		json bool
		want string
	}{
		{"list plus generation", []string{"--list", "--preset=auth", "--json"}, true, "--list cannot be combined"},
		{"unknown preset", []string{"--preset=unknown", "--json=true"}, true, "unknown middleware preset"},
		{"missing API", []string{"--api=missing.api", "--json"}, true, "missing.api"},
		{"unknown preceding flag", []string{"--unknown", "--json"}, true, "flag provided but not defined"},
		{"JSON disabled", []string{"--list", "--preset=auth", "--json=false"}, false, "--list cannot be combined"},
		{"invalid bool", []string{"--list", "--json=invalid"}, false, "invalid boolean value"},
		{"terminator", []string{"--list", "--", "--json"}, false, "--list cannot be combined"},
		{"JSON-looking string flag value", []string{"--api", "--json"}, false, "--json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			cliCoverageFile(t, dir, "go.mod", "module example.com/middleware\n\ngo 1.26\n")
			args := append([]string{"api", "middleware", "--dir", dir}, tc.args...)
			out, err := cliCoverageRun(t, args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("command error = %v, want %q; output=%s", err, tc.want, out)
			}
			if tc.json {
				var envelope struct {
					OK      bool            `json:"ok"`
					Command string          `json:"command"`
					Error   json.RawMessage `json:"error"`
				}
				if err := json.Unmarshal([]byte(out), &envelope); err != nil {
					t.Fatalf("expected one JSON envelope: %v; output=%s", err, out)
				}
				if envelope.OK || envelope.Command != "api.middleware" || len(envelope.Error) == 0 || !ErrorAlreadyReported(err) {
					t.Fatalf("invalid middleware error envelope: err=%v; output=%s", err, out)
				}
			} else if out != "" {
				t.Fatalf("JSON was not requested; output=%s", out)
			}
		})
	}
}

func TestCLICoverageExampleCatalog(t *testing.T) {
	for _, alias := range []string{"list", "ls"} {
		t.Run(alias, func(t *testing.T) {
			out, err := cliCoverageRun(t, "example", alias, "--json")
			if err != nil {
				t.Fatal(err)
			}
			var examples []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(out), &examples); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, example := range examples {
				if example.Name == "gosky" && example.Path == "examples/gosky" {
					found = true
				}
			}
			if !found {
				t.Fatalf("catalog omits the reference service: %s", out)
			}
		})
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"invalid list flag", []string{"example", "list", "--unknown"}, "flag provided but not defined"},
		{"invalid run flag", []string{"example", "run", "--unknown"}, "flag provided but not defined"},
		{"name after flags", []string{"example", "run", "--json", "missing-example"}, "unknown example"},
	} {
		t.Run(tc.name, func(t *testing.T) { cliCoverageJSONError(t, tc.want, tc.args...) })
	}
}
