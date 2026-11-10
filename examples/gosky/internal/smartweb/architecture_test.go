package smartweb

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// AUTHN-042: HTTP behavior alone cannot prove dependency direction. Inspect
// imports rather than implementation text so formatting and renames are safe.
func TestCompatibilityPersistenceBoundary(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		t.Run(file.Name(), func(t *testing.T) {
			source, err := parser.ParseFile(token.NewFileSet(), file.Name(), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range source.Imports {
				path, err := strconv.Unquote(item.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if path == "database/sql" || strings.HasPrefix(path, "github.com/imajinyun/gofly/core/storage") || (strings.HasPrefix(path, "github.com/imajinyun/gofly/examples/gosky/internal/repo") || strings.HasPrefix(path, "github.com/imajinyun/gofly/examples/gosky/internal/model/repo")) {
					t.Errorf("compatibility adapter imports persistence package %q; call a Gosky application service", path)
				}
			}
		})
	}
}
