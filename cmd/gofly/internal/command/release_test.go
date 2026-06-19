package command

import (
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
