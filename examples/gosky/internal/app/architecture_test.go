package app

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// AUTHZ-054: behavior tests cannot establish dependency direction. Application
// services consume domain interfaces; concrete SQL repositories belong outside.
func TestApplicationPersistenceBoundary(t *testing.T) {
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
					t.Errorf("application package imports persistence implementation %q; consume a repository interface", path)
				}
			}
		})
	}
}
