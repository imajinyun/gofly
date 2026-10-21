package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// See docs/reference/api-cleanup-safety.md for the prewritten failure inventory.
func TestExecuteAPICleanupSymlinkBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		execute bool
	}{{"dry run", false}, {"execute", true}} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			project := filepath.Join(base, "project")
			outside := cliCoverageFile(t, base, "outside/keep.go", "package outside\n")
			valid := cliCoverageFile(t, project, "internal/api/http/first.go", "package http\n")
			if err := os.Symlink(filepath.Dir(outside), filepath.Join(project, "internal/app")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			data, err := json.Marshal(map[string]any{"schema": "gofly.gozero_api_stale_files.v1", "staleHandlers": []string{"internal/api/http/first.go"}, "staleLogics": []string{"internal/app/keep.go"}})
			if err != nil {
				t.Fatal(err)
			}
			cliCoverageFile(t, project, ".gofly/stale-api-files.json", string(data))
			args := []string{"--output=json", "api", "cleanup", "stale", "--dir", project}
			if tc.execute {
				args = append(args, "--execute")
			}
			out, err := cliCoverageRun(t, args...)
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Errorf("cleanup must reject symlink parent: err=%v output=%s", err, out)
			}
			for path, want := range map[string]string{outside: "package outside\n", valid: "package http\n"} {
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Errorf("cleanup changed protected file %s: %q err=%v", path, got, err)
				}
			}
		})
	}
}
