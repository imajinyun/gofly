package generator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateMiddlewarePresetsRuntime(t *testing.T) {
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/presetruntime")
	if _, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"all"}, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join(repositoryRoot(t), "testdata", "api", "middleware-presets", "runtime_test.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "middleware", "runtime_test.go"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-shuffle=on", "-race", "-json", "./internal/middleware")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLY_CACHE_DISABLED=true")
	output, runErr := cmd.CombinedOutput()
	if reportDir := os.Getenv("GOFLY_MIDDLEWARE_REPORT_DIR"); reportDir != "" {
		if err := os.MkdirAll(reportDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(reportDir, "runtime.jsonl"), output, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if runErr != nil {
		t.Fatalf("generated middleware runtime: %v\n%s", runErr, output)
	}
	t.Logf("generated middleware runtime evidence:\n%s", output)
}

func TestGenerateMiddlewarePresetsIndividualCompilation(t *testing.T) {
	for _, preset := range ListMiddlewarePresets() {
		t.Run(preset.Name, func(t *testing.T) {
			dir := t.TempDir()
			writeGeneratedModule(t, dir, fmt.Sprintf("example.com/preset/%s", preset.Name))
			if _, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{preset.Name}, Dir: dir}); err != nil {
				t.Fatal(err)
			}
			runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
			runGoCommand(t, dir, 3*time.Minute, "test", "./...")
		})
	}
}
