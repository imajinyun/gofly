package routes

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ARCH-001: runtime tests cannot detect a valid but nonstandard package layout.
func TestDefaultModelLayout(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, tc := range []struct{ name, path, pkg string }{
		{"entities", "internal/model/entity", "entity"},
		{"repositories", "internal/model/repo", "repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := os.ReadDir(filepath.Join(root, tc.path))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
					continue
				}
				file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, tc.path, entry.Name()), nil, parser.PackageClauseOnly)
				if err != nil {
					t.Fatal(err)
				}
				if file.Name.Name != tc.pkg {
					t.Errorf("%s package=%q, want %q", entry.Name(), file.Name.Name, tc.pkg)
				}
				count++
			}
			if count == 0 {
				t.Error("model directory contains no Go files")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "repo")); !os.IsNotExist(err) {
		t.Errorf("legacy internal/repo must not exist, stat error=%v", err)
	}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		if filepath.Dir(path) == filepath.Join(root, "internal", "model") {
			t.Errorf("Go model file must live under entity or repo: %s", path)
		}
		for _, spec := range file.Imports {
			value, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if value == "github.com/imajinyun/gofly/examples/gosky/internal/model" || value == "github.com/imajinyun/gofly/examples/gosky/internal/repo" || strings.HasPrefix(value, "github.com/imajinyun/gofly/examples/gosky/internal/repo/") {
				t.Errorf("%s imports retired model layout %q", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
