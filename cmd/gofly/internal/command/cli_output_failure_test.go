package command

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type failingCLIOutput struct{ err error }

func (w failingCLIOutput) Write([]byte) (int, error) { return 0, w.err }

// See docs/reference/api-cleanup-safety.md; exercise the public CLI I/O boundary.
func TestExecuteJSONOutputFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		usage bool
	}{
		{"successful catalog", []string{"api", "middleware", "--list", "--json"}, false},
		{"usage error", []string{"api", "middleware", "--list", "--preset=auth", "--json"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := errors.New("output stream closed")
			err := ExecuteWithIO(tc.args, IOStreams{Out: failingCLIOutput{want}, Err: io.Discard})
			if !errors.Is(err, want) || ExitCode(err) == 0 || ErrorAlreadyReported(err) {
				t.Errorf("output failure lost: err=%v exit=%d reported=%t", err, ExitCode(err), ErrorAlreadyReported(err))
			}
			if tc.usage && (err == nil || !strings.Contains(err.Error(), "--list cannot be combined")) {
				t.Errorf("original usage failure lost: %v", err)
			}
			if err != nil {
				cliCoverageFile(t, t.ArtifactDir(), "output-error.txt", err.Error())
			}
		})
	}
}
