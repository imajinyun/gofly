package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChangelogVersion(t *testing.T) {
	dir := t.TempDir()

	// No file.
	if _, err := parseChangelogVersion(filepath.Join(dir, "nope.md")); err == nil {
		t.Fatal("expected error for missing file")
	}

	// File without version header.
	plain := filepath.Join(dir, "plain.md")
	if err := os.WriteFile(plain, []byte("# Changelog\n\nSome text.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := parseChangelogVersion(plain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "" {
		t.Fatalf("expected empty version, got %q", v)
	}

	// File with version header.
	versioned := filepath.Join(dir, "versioned.md")
	if err := os.WriteFile(versioned, []byte("# Changelog\n\n## v1.2.3\n\n- fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err = parseChangelogVersion(versioned)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "1.2.3" {
		t.Fatalf("expected 1.2.3, got %q", v)
	}
}

func TestRecommendSemver(t *testing.T) {
	cases := []struct {
		blockers []string
		warnings []string
		want     string
	}{
		{nil, nil, "patch"},
		{nil, []string{"w"}, "minor"},
		{[]string{"API breaking: 1 change(s)"}, nil, "major"},
		{[]string{"something else"}, nil, "minor (with blockers)"},
	}
	for _, tc := range cases {
		got := recommendSemver(tc.blockers, tc.warnings)
		if got != tc.want {
			t.Fatalf("recommendSemver(%v, %v) = %q, want %q", tc.blockers, tc.warnings, got, tc.want)
		}
	}
}

func TestReleaseCheckCommandWithBreakingAPI(t *testing.T) {
	dir := t.TempDir()
	baseAPI := filepath.Join(dir, "base.api")
	targetAPI := filepath.Join(dir, "target.api")
	if err := os.WriteFile(baseAPI, []byte(`type User { ID int Name string }
service UserService { @handler getUser GET /users/{id} (User) returns (User) }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetAPI, []byte(`type User { ID int }
service UserService { @handler getUser POST /users/{id} (User) returns (User) }`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Capture output by redirecting stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := releaseCheckCommand([]string{
		"--api-base", baseAPI,
		"--api-target", targetAPI,
		"--changelog", filepath.Join(dir, "no-changelog"),
	})

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected release check to fail with breaking API changes")
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "BLOCKED") {
		t.Fatalf("expected BLOCKED in output, got:\n%s", out)
	}
	if !strings.Contains(out, "api-breaking") {
		t.Fatalf("expected api-breaking in output, got:\n%s", out)
	}
}

func TestReleaseCheckCommandJSONAndChangelogBlocker(t *testing.T) {
	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\nUnreleased notes only.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--changelog", changelog, "--json"})
	}); err != nil {
		t.Fatalf("releaseCheckCommand json pass: %v", err)
	}
	var passEnvelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &passEnvelope); err != nil {
		t.Fatalf("releaseCheckCommand json pass decode: %v\n%s", err, out.String())
	}
	if !passEnvelope.OK || passEnvelope.Command != "release.check" || !strings.Contains(passEnvelope.Data.Summary, "PASS") || len(passEnvelope.Data.Checks) == 0 {
		t.Fatalf("releaseCheckCommand json pass envelope = %+v, want ok release.check report", passEnvelope)
	}
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n## v9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--changelog", changelog, "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "release check failed") {
		t.Fatalf("releaseCheckCommand changelog blocker error = %v, want release check failed", err)
	}
	var failEnvelope struct {
		OK      bool               `json:"ok"`
		Command string             `json:"command"`
		Data    releaseCheckReport `json:"data"`
		Error   *jsonError         `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &failEnvelope); err != nil {
		t.Fatalf("releaseCheckCommand blocker json decode: %v\n%s", err, out.String())
	}
	if failEnvelope.OK || failEnvelope.Command != "release.check" || failEnvelope.Error == nil || failEnvelope.Error.Code != "RELEASE_CHECK_FAILED" || !strings.Contains(failEnvelope.Data.Summary, "BLOCKED") || !strings.Contains(out.String(), `9.9.9`) {
		t.Fatalf("releaseCheckCommand blocker envelope = %+v, want structured blocker", failEnvelope)
	}
}

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
	if strings.Count(out.String(), `"ok"`) != 1 {
		t.Fatalf("ExecuteWithIO release check emitted duplicate JSON envelopes:\n%s", out.String())
	}
}

func TestReleaseCheckCommandAPIAndRPCPassAndErrorBranches(t *testing.T) {
	t.Setenv("API_BASE_REF", "definitely-missing-release-base-ref")
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte("# Changelog\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseAPI := filepath.Join(dir, "base.api")
	targetAPI := filepath.Join(dir, "target.api")
	if err := os.WriteFile(baseAPI, []byte(`type PingResp {
  Message string
}
service ping-api {
  @handler ping
  get /ping returns (PingResp)
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetAPI, []byte(`type PingResp {
  Message string
}
type PongResp {
  Message string
}
service ping-api {
  @handler ping
  get /ping returns (PingResp)
  @handler pong
  get /pong returns (PongResp)
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	baseProto := filepath.Join(dir, "base.proto")
	targetProto := filepath.Join(dir, "target.proto")
	if err := os.WriteFile(baseProto, []byte(`syntax = "proto3";
package demo;
message PingReq { string name = 1; }
message PingResp { string message = 1; }
service Greeter { rpc Ping (PingReq) returns (PingResp); }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetProto, []byte(`syntax = "proto3";
package demo;
message PingReq { string name = 1; }
message PingResp { string message = 1; }
message PongReq { string name = 1; }
message PongResp { string message = 1; }
service Greeter {
  rpc Ping (PingReq) returns (PingResp);
  rpc Pong (PongReq) returns (PongResp);
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--api-base", baseAPI, "--api-target", targetAPI, "--rpc-base", baseProto, "--rpc-target", targetProto, "--changelog", changelog})
	}); err != nil {
		t.Fatalf("releaseCheckCommand added API/RPC pass: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "PASS") || !strings.Contains(out.String(), "api-breaking") || !strings.Contains(out.String(), "rpc-breaking") || !strings.Contains(out.String(), "go-mod-tidy") {
		t.Fatalf("release output = %s, want pass report with api/rpc/tidy", out.String())
	}

	removedProto := filepath.Join(dir, "removed.proto")
	if err := os.WriteFile(removedProto, []byte(`syntax = "proto3";
package demo;
message PingReq { string name = 1; }
message PingResp { string message = 1; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--rpc-base", baseProto, "--rpc-target", removedProto, "--changelog", changelog})
	})
	if err == nil || !strings.Contains(err.Error(), "release check failed") {
		t.Fatalf("releaseCheckCommand rpc breaking error = %v, want release check failed", err)
	}
	if !strings.Contains(out.String(), "RPC breaking") || !strings.Contains(out.String(), "Blocking:") || !strings.Contains(out.String(), "[BLOCKER]") {
		t.Fatalf("rpc breaking release output = %s, want rpc blocker report", out.String())
	}

	badProto := filepath.Join(dir, "bad.proto")
	if err := os.WriteFile(badProto, []byte("syntax = \"proto3\"; service"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return releaseCheckCommand([]string{"--rpc-base", baseProto, "--rpc-target", badProto, "--changelog", changelog})
	})
	if err == nil || !strings.Contains(err.Error(), "release check failed") {
		t.Fatalf("releaseCheckCommand bad rpc error = %v, want release check failed", err)
	}
	if !strings.Contains(out.String(), "rpc breaking check error") && !strings.Contains(out.String(), "rpc-breaking") {
		t.Fatalf("bad rpc release output = %s, want rpc error branch", out.String())
	}
}

func TestGoReleaserUsesCurrentScriptPath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", ".goreleaser.yml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "sh bin/scripts/check-mod-tidy.sh") {
		t.Fatalf("goreleaser config missing current tidy script path:\n%s", content)
	}
	if strings.Contains(content, "sh scripts/check-mod-tidy.sh") {
		t.Fatalf("goreleaser config still uses stale script path:\n%s", content)
	}
}
