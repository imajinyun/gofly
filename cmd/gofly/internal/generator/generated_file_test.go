package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedFileSafeTargetValidation(t *testing.T) {
	t.Run("rejects missing root or target", func(t *testing.T) {
		if _, err := SafeTarget("", "service.go", "generated file"); err == nil || !strings.Contains(err.Error(), "root is required") {
			t.Fatalf("SafeTarget empty root err = %v, want root required", err)
		}
		if _, err := SafeTarget(t.TempDir(), " ", "generated file"); err == nil || !strings.Contains(err.Error(), "target path is required") {
			t.Fatalf("SafeTarget empty target err = %v, want target required", err)
		}
	})

	t.Run("accepts absolute target inside root", func(t *testing.T) {
		root := t.TempDir()
		want := filepath.Join(root, "internal", "service.go")
		got, err := SafeTarget(root, want, "generated file")
		if err != nil {
			t.Fatalf("SafeTarget absolute inside root: %v", err)
		}
		if got != want {
			t.Fatalf("SafeTarget absolute inside root = %q, want %q", got, want)
		}
	})

	t.Run("normalizes backslash relative target", func(t *testing.T) {
		root := t.TempDir()
		got, err := SafeTarget(root, `internal\api\handler.go`, "generated file")
		if err != nil {
			t.Fatalf("SafeTarget backslash target: %v", err)
		}
		want := filepath.Join(root, "internal", "api", "handler.go")
		if got != want {
			t.Fatalf("SafeTarget backslash target = %q, want %q", got, want)
		}
	})
}

func TestGeneratedFileSafeRelativeTargetValidation(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name       string
		target     string
		wantErrSub string
	}{
		{name: "empty target", target: "", wantErrSub: "must be relative"},
		{name: "absolute target", target: filepath.Join(root, "handler.go"), wantErrSub: "must be relative"},
		{name: "windows drive target", target: `C:\tmp\handler.go`, wantErrSub: "must be relative"},
		{name: "parent escape", target: filepath.Join("..", "handler.go"), wantErrSub: "escapes output directory"},
		{name: "backslash parent escape", target: `..\handler.go`, wantErrSub: "escapes output directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := safeRelativeTarget(root, tt.target, "generated file"); err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("safeRelativeTarget(%q) err = %v, want substring %q", tt.target, err, tt.wantErrSub)
			}
		})
	}
}

func TestGeneratedFileRootReadWriteAndCopy(t *testing.T) {
	t.Run("ensure directory creates root and rejects symlink root", func(t *testing.T) {
		base := t.TempDir()
		root := filepath.Join(base, "service")
		if err := EnsureDirectoryUnderRoot(root, ".", 0o750, "generated file"); err != nil {
			t.Fatalf("EnsureDirectoryUnderRoot root: %v", err)
		}
		info, err := os.Stat(root)
		if err != nil {
			t.Fatalf("stat root: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o750 {
			t.Fatalf("root mode = %v, want 0750", got)
		}

		outside := t.TempDir()
		rootLink := filepath.Join(base, "root-link")
		if err := os.Symlink(outside, rootLink); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if err := EnsureDirectoryUnderRoot(rootLink, ".", 0o750, "generated file"); err == nil || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("EnsureDirectoryUnderRoot symlink root err = %v, want symlink rejection", err)
		}
	})

	t.Run("write and read nested file with requested permissions", func(t *testing.T) {
		root := t.TempDir()
		if err := WriteFileUnderRoot(root, filepath.Join("internal", "api", "handler.go"), []byte("package api\n"), 0o600, 0o750, "generated file"); err != nil {
			t.Fatalf("WriteFileUnderRoot: %v", err)
		}
		path := filepath.Join(root, "internal", "api", "handler.go")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat written file: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("written file mode = %v, want 0600", got)
		}
		data, err := ReadFileUnderRoot(root, filepath.Join("internal", "api", "handler.go"), "generated file")
		if err != nil {
			t.Fatalf("ReadFileUnderRoot: %v", err)
		}
		if string(data) != "package api\n" {
			t.Fatalf("ReadFileUnderRoot data = %q, want generated content", data)
		}
	})

	t.Run("copy under root preserves content and requested file mode", func(t *testing.T) {
		srcRoot := t.TempDir()
		dstRoot := t.TempDir()
		if err := WriteFileUnderRoot(srcRoot, filepath.Join("contracts", "orders.proto"), []byte("syntax = \"proto3\";\n"), 0o644, 0o755, "contract source"); err != nil {
			t.Fatalf("write source: %v", err)
		}
		if err := CopyFileUnderRoot(srcRoot, filepath.Join("contracts", "orders.proto"), dstRoot, filepath.Join("api", "orders.proto"), 0o600, 0o750, "contract"); err != nil {
			t.Fatalf("CopyFileUnderRoot: %v", err)
		}
		copied := filepath.Join(dstRoot, "api", "orders.proto")
		data, err := os.ReadFile(copied)
		if err != nil {
			t.Fatalf("read copied file: %v", err)
		}
		if string(data) != "syntax = \"proto3\";\n" {
			t.Fatalf("copied content = %q, want source content", data)
		}
		info, err := os.Stat(copied)
		if err != nil {
			t.Fatalf("stat copied file: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("copied file mode = %v, want 0600", got)
		}
	})

	t.Run("copy under root noops when source and destination are same file", func(t *testing.T) {
		root := t.TempDir()
		name := "same.txt"
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("keep\n"), 0o644); err != nil {
			t.Fatalf("write same file: %v", err)
		}
		if err := CopyFileUnderRoot(root, name, root, name, 0o600, 0o700, "generated file"); err != nil {
			t.Fatalf("CopyFileUnderRoot same file: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read same file: %v", err)
		}
		if string(data) != "keep\n" {
			t.Fatalf("same file content = %q, want unchanged content", data)
		}
	})
}

func TestGeneratedFileCopyRejectsSymlinkTargets(t *testing.T) {
	t.Run("copy under root rejects symlink destination leaf", func(t *testing.T) {
		srcRoot := t.TempDir()
		dstRoot := t.TempDir()
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(srcRoot, "source.txt"), []byte("source\n"), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		outsideTarget := filepath.Join(outside, "target.txt")
		if err := os.Symlink(outsideTarget, filepath.Join(dstRoot, "target.txt")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if err := CopyFileUnderRoot(srcRoot, "source.txt", dstRoot, "target.txt", 0o644, 0o755, "generated file"); err == nil || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("CopyFileUnderRoot symlink destination err = %v, want symlink rejection", err)
		}
		if _, err := os.Stat(outsideTarget); err == nil {
			t.Fatalf("CopyFileUnderRoot wrote through symlink destination")
		}
	})

	t.Run("copy file to root rejects symlink destination leaf", func(t *testing.T) {
		base := t.TempDir()
		src := filepath.Join(base, "source.txt")
		if err := os.WriteFile(src, []byte("source\n"), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		dstRoot := filepath.Join(base, "dst")
		if err := os.MkdirAll(dstRoot, 0o755); err != nil {
			t.Fatalf("create destination root: %v", err)
		}
		outsideTarget := filepath.Join(t.TempDir(), "target.txt")
		if err := os.Symlink(outsideTarget, filepath.Join(dstRoot, "target.txt")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if err := CopyFileToRoot(src, dstRoot, "target.txt", 0o644, 0o755, "generated file"); err == nil || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("CopyFileToRoot symlink destination err = %v, want symlink rejection", err)
		}
		if _, err := os.Stat(outsideTarget); err == nil {
			t.Fatalf("CopyFileToRoot wrote through symlink destination")
		}
	})
}
