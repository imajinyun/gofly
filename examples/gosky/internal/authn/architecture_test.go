package authn

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// AUTHN-043: E2E can preserve behavior without noticing reversed dependencies.
func TestAuthenticationPersistenceBoundary(t *testing.T) {
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
			for _, spec := range source.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if path == "database/sql" || strings.HasPrefix(path, "github.com/imajinyun/gofly/core/storage") || strings.HasPrefix(path, "github.com/imajinyun/gofly/examples/gosky/internal/model/repo") || strings.HasPrefix(path, "github.com/imajinyun/gofly/examples/gosky/internal/repo") {
					t.Errorf("authentication depends on persistence implementation %q instead of its own contract", path)
				}
			}
		})
	}
}
