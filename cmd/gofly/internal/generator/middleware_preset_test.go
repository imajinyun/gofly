package generator

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMiddlewarePresetCatalog(t *testing.T) {
	presets := ListMiddlewarePresets()
	want := []string{
		"auth",
		"cors",
		"csrf",
		"jwt",
		"observability",
		"opentelemetry",
		"prometheus",
		"session",
		"sse",
		"stability",
		"validation",
		"web-security",
		"websocket",
	}
	if len(presets) != len(want) {
		t.Fatalf("preset count = %d, want %d", len(presets), len(want))
	}
	for index, preset := range presets {
		if preset.Name != want[index] {
			t.Fatalf("preset[%d] = %q, want %q", index, preset.Name, want[index])
		}
		if preset.Description == "" || preset.Kind == "" || len(preset.Files) == 0 {
			t.Fatalf("preset %q has incomplete metadata: %#v", preset.Name, preset)
		}
	}
	presets[0].Files[0] = "mutated.go"
	if got := ListMiddlewarePresets()[0].Files[0]; got == "mutated.go" {
		t.Fatal("ListMiddlewarePresets returned mutable registry state")
	}
}

func TestMiddlewarePresetTemplatesMatchExampleSources(t *testing.T) {
	root := repositoryRoot(t)
	exampleRoots := []string{
		filepath.Join(root, "examples", "http", "http-middleware", "middlewares"),
		filepath.Join(root, "examples", "gosky", "internal", "middleware"),
	}
	for _, preset := range ListMiddlewarePresets() {
		for _, fileName := range preset.Files {
			t.Run(preset.Name+"/"+fileName, func(t *testing.T) {
				templateData, err := middlewarePresetTemplates.ReadFile(filepath.ToSlash(filepath.Join("middleware_presets", fileName+".tmpl")))
				if err != nil {
					t.Fatal(err)
				}
				templateData, err = format.Source(templateData)
				if err != nil {
					t.Fatal(err)
				}
				for _, exampleRoot := range exampleRoots {
					exampleData, err := os.ReadFile(filepath.Join(exampleRoot, fileName))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(templateData, exampleData) {
						t.Fatalf("preset template drifted from %s: %s", exampleRoot, fileName)
					}
				}
			})
		}
	}
}

func TestGenerateMiddlewarePresetsDryRunWriteAndRepeat(t *testing.T) {
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/presets")
	goModBefore, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}

	dryRun, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{
		Names:  []string{"auth", "cors", "auth"},
		Dir:    dir,
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || strings.Join(dryRun.Presets, ",") != "auth,cors" {
		t.Fatalf("dry-run result = %#v", dryRun)
	}
	for _, file := range dryRun.Files {
		if file.Status != "planned" {
			t.Fatalf("dry-run file = %#v, want planned", file)
		}
		if _, err := os.Stat(filepath.Join(dir, file.Path)); !os.IsNotExist(err) {
			t.Fatalf("dry-run created %s: %v", file.Path, err)
		}
	}

	result, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"auth", "cors"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		if file.Status != "written" {
			t.Fatalf("generated file = %#v, want written", file)
		}
	}
	auth, err := os.ReadFile(filepath.Join(dir, "internal", "middleware", "auth.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(auth), "func APIKeyMiddleware") || !strings.Contains(string(auth), "func RBACMiddleware") {
		t.Fatalf("auth preset is incomplete:\n%s", auth)
	}

	repeat, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"cors", "auth"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range repeat.Files {
		if file.Status != "unchanged" {
			t.Fatalf("repeat file = %#v, want unchanged", file)
		}
	}
	goModAfter, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(goModBefore, goModAfter) {
		t.Fatalf("middleware preset generation modified go.mod:\n%s", goModAfter)
	}
}

func TestGenerateMiddlewarePresetsRejectsConflictsBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/presetconflict")
	targetDir := filepath.Join(dir, "internal", "middleware")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "cors.go"), []byte("package middleware\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"auth", "cors"}, Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "cors.go") {
		t.Fatalf("conflict error = %v, want cors.go", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "auth.go")); !os.IsNotExist(err) {
		t.Fatalf("auth.go was written before conflict detection: %v", err)
	}
}

func TestGenerateMiddlewarePresetsValidation(t *testing.T) {
	if _, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"auth"}, Dir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("missing module error = %v", err)
	}

	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/presetvalidation")
	if _, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"unknown"}, Dir: dir}); err == nil || !strings.Contains(err.Error(), "available") {
		t.Fatalf("unknown preset error = %v", err)
	}
	if _, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"all", "auth"}, Dir: dir}); err == nil || !strings.Contains(err.Error(), "all") {
		t.Fatalf("mixed all preset error = %v", err)
	}

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "internal", "middleware")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"auth"}, Dir: dir}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestGenerateAllMiddlewarePresetsCompiles(t *testing.T) {
	dir := t.TempDir()
	writeGeneratedModule(t, dir, "example.com/allpresets")
	result, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"all"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != len(ListMiddlewarePresets()) {
		t.Fatalf("generated files = %d, want %d", len(result.Files), len(ListMiddlewarePresets()))
	}
	runGoCommand(t, dir, 3*time.Minute, "mod", "tidy")
	runGoCommand(t, dir, 3*time.Minute, "test", "./...")
}
