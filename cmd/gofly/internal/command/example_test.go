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

func TestExampleListCommand_Text(t *testing.T) {
	// Just verify it does not error.
	if err := exampleListCommand([]string{}); err != nil {
		t.Fatalf("exampleListCommand: %v", err)
	}
}

func TestExampleListCommand_JSON(t *testing.T) {
	if err := exampleListCommand([]string{"--json"}); err != nil {
		t.Fatalf("exampleListCommand --json: %v", err)
	}
}

func TestExampleRunCommand_Success(t *testing.T) {
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

func TestExampleRunCommand_UnknownExample(t *testing.T) {
	err := exampleRunCommand([]string{"nonexistent-example"})
	if err == nil {
		t.Fatal("expected error for unknown example")
	}
}

func TestExampleRunCommand_MissingName(t *testing.T) {
	err := exampleRunCommand([]string{})
	if err == nil {
		t.Fatal("expected error when name is missing")
	}
}

func TestResolveExampleSourceDir(t *testing.T) {
	src, err := resolveExampleSourceDir("examples/observability")
	if err != nil {
		t.Fatalf("resolveExampleSourceDir: %v", err)
	}
	mainFile := filepath.Join(src, "main.go")
	if _, err := os.Stat(mainFile); err != nil {
		t.Fatalf("expected %s to exist: %v", mainFile, err)
	}
}

func TestCopyExampleDir(t *testing.T) {
	src, err := resolveExampleSourceDir("examples/restserver")
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
