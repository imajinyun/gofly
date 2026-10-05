package generator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMiddlewarePresetTargetDependency(t *testing.T) {
	for _, test := range []struct {
		name, mod string
		valid     bool
	}{
		{"bare module", "module example.com/service\n\ngo 1.26\n", false},
		{"replace without require", "module example.com/service\nreplace github.com/imajinyun/gofly => ../gofly\n", false},
		{"malformed require", "module example.com/service\nrequire (\n", false},
		{"invalid version", "module example.com/service\nrequire github.com/imajinyun/gofly nonsense\n", false},
		{"direct require", "module example.com/service\nrequire github.com/imajinyun/gofly v0.1.0\n", true},
		{"block require with replacement", "module example.com/service\nrequire (\n github.com/imajinyun/gofly v0.0.0\n)\nreplace github.com/imajinyun/gofly => ../gofly\n", true},
		{"framework itself", "module github.com/imajinyun/gofly\ngo 1.26\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "go.mod")
			if err := os.WriteFile(path, []byte(test.mod), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := GenerateMiddlewarePresets(MiddlewarePresetOptions{Names: []string{"auth"}, Dir: dir})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t error=%v", test.valid, err)
			}
			if !test.valid {
				if _, statErr := os.Stat(filepath.Join(dir, "internal")); !os.IsNotExist(statErr) {
					t.Fatalf("invalid dependency caused writes: %v", statErr)
				}
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || string(data) != test.mod {
				t.Fatalf("go.mod changed: %q error=%v", data, readErr)
			}
		})
	}
}
