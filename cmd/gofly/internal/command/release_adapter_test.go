package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseCheckGlobalJSONDoesNotDuplicateError(t *testing.T) {
	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## v9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := ExecuteWithIO([]string{"--output=json", "release", "check", "--changelog", changelog}, IOStreams{Out: &out})
	if err == nil || !strings.Contains(err.Error(), "release check failed") || !errors.Is(err, errJSONAlreadyReported) {
		t.Fatalf("ExecuteWithIO release check error = %v, want reported release check failure", err)
	}
	var envelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
		Error   *jsonError         `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("ExecuteWithIO release check json decode: %v\n%s", err, out.String())
	}
	if envelope.OK || envelope.Command != "release.check" || envelope.Error == nil || envelope.Error.Code != "RELEASE_CHECK_FAILED" {
		t.Fatalf("ExecuteWithIO release check envelope = %+v, want one structured release failure", envelope)
	}
	if strings.Count(out.String(), `"command"`) != 1 {
		t.Fatalf("ExecuteWithIO release check emitted duplicate JSON envelopes:\n%s", out.String())
	}
}
