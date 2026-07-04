package command

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleCommandBranches(t *testing.T) {
	if err := exampleCommand([]string{"--help"}); err != nil {
		t.Fatalf("example --help: %v", err)
	}
	if err := exampleCommand([]string{}); err == nil {
		t.Fatal("example empty should error")
	}
	if err := exampleCommand([]string{"bogus"}); err == nil {
		t.Fatal("example bogus should error")
	}
}

func TestExampleListCommandText(t *testing.T) {
	// Just verify it does not error.
	if err := exampleListCommand([]string{}); err != nil {
		t.Fatalf("exampleListCommand: %v", err)
	}
}

func TestExampleListCommandJSON(t *testing.T) {
	if err := exampleListCommand([]string{"--json"}); err != nil {
		t.Fatalf("exampleListCommand --json: %v", err)
	}
}

func TestBuiltInExamplesPointToExistingCategorizedDirs(t *testing.T) {
	seen := map[string]bool{}
	for _, example := range builtInExamples {
		if seen[example.Name] {
			t.Fatalf("duplicate example name %q", example.Name)
		}
		seen[example.Name] = true
		if example.Path == "" || example.Description == "" {
			t.Fatalf("example %q has incomplete metadata: %#v", example.Name, example)
		}
		src, err := resolveExampleSourceDir(example.Path)
		if err != nil {
			t.Fatalf("resolve %s: %v", example.Name, err)
		}
		if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
			t.Fatalf("example %s missing go.mod at %s: %v", example.Name, src, err)
		}
	}
	for _, name := range []string{"restserver", "rpcserver", "cache-local", "http-middleware", "production-orders", "plugin-ecosystem", "migration-proof", "rpc-idl-matrix"} {
		if !seen[name] {
			t.Fatalf("built-in examples missing %q", name)
		}
	}
}

func TestExampleRunCommandSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	outDir := filepath.Join(tmpDir, "obs-demo")
	if err := exampleRunCommand([]string{"observability", "--dir", outDir}); err != nil {
		t.Fatalf("exampleRunCommand: %v", err)
	}
	mainFile := filepath.Join(outDir, "main.go")
	if _, err := os.Stat(mainFile); err != nil {
		t.Fatalf("expected %s to exist: %v", mainFile, err)
	}
}

func TestExampleRunCommandUnknownExample(t *testing.T) {
	err := exampleRunCommand([]string{"nonexistent-example"})
	if err == nil {
		t.Fatal("expected error for unknown example")
	}
}

func TestExampleRunCommandMissingName(t *testing.T) {
	err := exampleRunCommand([]string{})
	if err == nil {
		t.Fatal("expected error when name is missing")
	}
}

func TestResolveExampleSourceDir(t *testing.T) {
	src, err := resolveExampleSourceDir("examples/http/observability")
	if err != nil {
		t.Fatalf("resolveExampleSourceDir: %v", err)
	}
	mainFile := filepath.Join(src, "main.go")
	if _, err := os.Stat(mainFile); err != nil {
		t.Fatalf("expected %s to exist: %v", mainFile, err)
	}
}

func TestCopyExampleDir(t *testing.T) {
	src, err := resolveExampleSourceDir("examples/getting-started/restserver")
	if err != nil {
		t.Fatalf("resolveExampleSourceDir: %v", err)
	}
	dst := t.TempDir()
	if err := copyExampleDir(src, dst); err != nil {
		t.Fatalf("copyExampleDir: %v", err)
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected copied files, got none")
	}
}
