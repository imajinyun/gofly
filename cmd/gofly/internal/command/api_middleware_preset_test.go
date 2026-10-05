package command

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIMiddlewarePresetListJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := withCommandIO(IOStreams{Out: &stdout}, outputJSON, verbosityNormal, func() error {
		return apiMiddlewareCommand([]string{"--list"})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"command": "api.middleware.list"`, `"name": "auth"`, `"name": "websocket"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("list output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestAPIMiddlewarePresetDryRunAndWrite(t *testing.T) {
	dir := t.TempDir()
	writeMiddlewarePresetTestModule(t, dir)
	var stdout bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &stdout}, outputJSON, verbosityNormal, func() error {
		return apiMiddlewareCommand([]string{"--preset", "auth,cors", "--dir", dir, "--dry-run"})
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"dryRun": true`) || !strings.Contains(stdout.String(), `"status": "planned"`) {
		t.Fatalf("dry-run output = %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "middleware")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created middleware directory: %v", err)
	}

	stdout.Reset()
	if err := withCommandIO(IOStreams{Out: &stdout}, outputText, verbosityNormal, func() error {
		return apiMiddlewareCommand([]string{"--preset=auth,cors", "--dir", dir})
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"auth.go", "cors.go"} {
		if _, err := os.Stat(filepath.Join(dir, "internal", "middleware", path)); err != nil {
			t.Fatalf("preset output %s: %v", path, err)
		}
	}
}

func TestAPIMiddlewarePresetRejectsAmbiguousInputsAndOptionAlias(t *testing.T) {
	dir := t.TempDir()
	writeMiddlewarePresetTestModule(t, dir)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "preset and skeleton name", args: []string{"Custom", "--preset", "auth", "--dir", dir}, want: "cannot combine"},
		{name: "preset and api", args: []string{"--preset", "auth", "--api", "service.api", "--dir", dir}, want: "cannot combine"},
		{name: "dry-run without preset", args: []string{"Custom", "--dir", dir, "--dry-run"}, want: "requires --preset"},
		{name: "option alias rejected", args: []string{"--option=auth", "--dir", dir}, want: "flag provided but not defined"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := apiMiddlewareCommand(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func writeMiddlewarePresetTestModule(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/presets\n\ngo 1.26\n\nrequire github.com/imajinyun/gofly v0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
