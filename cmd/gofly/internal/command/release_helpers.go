package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

func printReleaseCheckJSON(report releaseCheckReport, failed bool) error {
	envelope := jsonEnvelope{OK: !failed, Command: "release.check", Version: Version, Data: report}
	if failed {
		envelope.Error = &jsonError{Code: "RELEASE_CHECK_FAILED", Message: report.Summary, Retryable: false, Remediation: "Resolve blocking release checks before publishing.", Details: map[string]any{"blocking": report.Blocking}}
	}
	if err := printJSON(envelope); err != nil {
		return err
	}
	if failed {
		return fmt.Errorf("release check failed: %w", errJSONAlreadyReported)
	}
	return nil
}

// runAPIDiffCheck runs `go tool apidiff` or the APIDIFF_TOOL binary to detect
// incompatible public Go API changes against the base ref.
func runAPIDiffCheck() ([]byte, error) {
	goCmd := os.Getenv("GO")
	if goCmd == "" {
		goCmd = "go"
	}
	apidiffTool := os.Getenv("APIDIFF_TOOL")
	if apidiffTool == "" {
		apidiffTool = goCmd + " tool apidiff"
	}

	baseRef := os.Getenv("API_BASE_REF")
	if baseRef == "" {
		if out, err := exec.Command("git", "rev-parse", "--verify", "HEAD~1").CombinedOutput(); err == nil {
			baseRef = strings.TrimSpace(string(out))
		}
	}
	if baseRef == "" {
		// No base ref available — skip apidiff by returning empty success.
		return []byte("skipped: no base ref"), nil
	}

	// Verify base ref exists.
	// #nosec G204 G702 -- release governance verifies a git ref through argv, not shell expansion.
	if _, err := exec.Command("git", "rev-parse", "--verify", baseRef+"^{commit}").CombinedOutput(); err != nil {
		return []byte("skipped: base ref not available"), nil
	}

	tmp, err := os.MkdirTemp("", "gofly-release-check-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	// #nosec G204 G702 -- baseRef is passed as a single git argv entry after rev-parse validation.
	if out, err := exec.Command("git", "worktree", "add", "--detach", tmp+"/base", baseRef).CombinedOutput(); err != nil {
		return out, err
	}
	defer func() {
		// #nosec G204 -- tmp is created by os.MkdirTemp and passed as a single argv entry.
		_, _ = exec.Command("git", "worktree", "remove", "-f", tmp+"/base").CombinedOutput()
	}()

	// #nosec G204 G702 -- goCmd is the configured Go tool name for this local release check.
	module, err := exec.Command(goCmd, "list", "-m").CombinedOutput()
	if err != nil {
		return module, err
	}
	mod := strings.TrimSpace(string(module))

	cmdBase := apidiffCommand(apidiffTool, "-m", "-w", tmp+"/base.exp", mod)
	cmdBase.Dir = tmp + "/base"
	if out, err := cmdBase.CombinedOutput(); err != nil {
		return out, err
	}

	cmdCur := apidiffCommand(apidiffTool, "-m", "-w", tmp+"/current.exp", mod)
	if out, err := cmdCur.CombinedOutput(); err != nil {
		return out, err
	}

	cmdDiff := apidiffCommand(apidiffTool, "-m", "-incompatible", tmp+"/base.exp", tmp+"/current.exp")
	out, _ := cmdDiff.CombinedOutput()
	changes := strings.TrimSpace(string(out))
	if changes != "" {
		return []byte(changes), errors.New("incompatible changes detected")
	}
	return nil, nil
}

var semverHeaderRegex = regexp.MustCompile(`(?im)^##?\s*\[?v?(\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?)\]?`)

// parseChangelogVersion extracts the first SemVer-looking version from the
// changelog header lines.
func parseChangelogVersion(path string) (string, error) {
	// #nosec G304 -- the changelog path is an explicit CLI input for release governance.
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := semverHeaderRegex.FindSubmatch(data)
	if m == nil {
		return "", nil
	}
	return string(m[1]), nil
}

func apidiffCommand(tool string, args ...string) *exec.Cmd {
	parts := strings.Fields(tool)
	if len(parts) == 0 {
		parts = []string{"go", "tool", "apidiff"}
	}
	argv := append(parts[1:], args...)
	// #nosec G204 G702 -- APIDIFF_TOOL is an explicit local governance tool override; args are argv entries.
	return exec.Command(parts[0], argv...)
}

// recommendSemver suggests major/minor/patch based on blockers and warnings.
func recommendSemver(blockers, warnings []string) string {
	if len(blockers) > 0 {
		for _, b := range blockers {
			if strings.Contains(b, "breaking") || strings.Contains(b, "incompatible") {
				return "major"
			}
		}
		return "minor (with blockers)"
	}
	if len(warnings) > 0 {
		return "minor"
	}
	return "patch"
}
