package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxVerificationOutputBytes = 4096

func runAIProjectVerification(dir string, verify []string, timeout time.Duration) ([]aiProjectVerificationResult, bool, error) {
	if timeout <= 0 {
		return nil, false, fmt.Errorf("%w: verification timeout must be positive", errUsage)
	}
	results := make([]aiProjectVerificationResult, 0, len(verify))
	passed := true
	for _, command := range verify {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		result := runAIProjectVerificationCommand(dir, command, timeout)
		if result.Status == "failed" {
			passed = false
		}
		results = append(results, result)
	}
	return results, passed, nil
}

func runAIProjectControlPlaneSnapshotAssertion(dir string, timeout time.Duration) aiProjectVerificationResult {
	const command = "control-plane snapshot"
	if timeout <= 0 {
		return newAIProjectVerificationResult(command, "failed", "", "verification timeout must be positive")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return newAIProjectVerificationResult(command, "failed", "", err.Error())
	}
	defer func() { _ = root.Close() }()
	testFile, err := root.Open(filepath.Join("internal", "config", "config_test.go"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newAIProjectVerificationResult(command, "skipped", "", "generated project does not expose a control-plane snapshot contract test")
		}
		return newAIProjectVerificationResult(command, "failed", "", err.Error())
	}
	data, err := io.ReadAll(testFile)
	_ = testFile.Close()
	if err != nil {
		return newAIProjectVerificationResult(command, "failed", "", err.Error())
	}
	if !strings.Contains(string(data), "TestControlPlaneSnapshotExposesGeneratedContract") {
		return newAIProjectVerificationResult(command, "skipped", "", "generated project does not expose a control-plane snapshot contract test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./internal/config", "-run", "TestControlPlaneSnapshotExposesGeneratedContract", "-count=1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := newAIProjectVerificationResult(command, "passed", string(out), "")
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "failed"
		result.Error = sanitizeVerificationText("control-plane snapshot assertion timed out")
		result.NextActions = aiProjectVerificationNextActions(command, result.Status)
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Error = sanitizeVerificationText(err.Error())
		result.NextActions = aiProjectVerificationNextActions(command, result.Status)
	}
	return result
}

func runAIProjectVerificationCommand(dir, command string, timeout time.Duration) aiProjectVerificationResult {
	name, args, ok := aiProjectVerificationCommandArgs(command)
	if !ok {
		return newAIProjectVerificationResult(command, "skipped", "", "unsupported verification command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// #nosec G204 -- verification commands are selected from aiProjectVerificationCommandArgs allow-list and never executed through a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if command == "gofly ai doctor --json" {
		if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
			cmd.Dir = frameworkPath
		}
	}
	out, err := cmd.CombinedOutput()
	result := newAIProjectVerificationResult(command, "passed", string(out), "")
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "failed"
		result.Error = sanitizeVerificationText("verification command timed out")
		result.NextActions = aiProjectVerificationNextActions(command, result.Status)
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Error = sanitizeVerificationText(err.Error())
		result.NextActions = aiProjectVerificationNextActions(command, result.Status)
	}
	return result
}

func aiProjectVerificationCommandArgs(command string) (string, []string, bool) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil, false
	}
	switch strings.Join(fields, " ") {
	case "gofmt":
		return "go", []string{"fmt", "./..."}, true
	case "go test ./...":
		return "go", []string{"test", "./..."}, true
	case "go mod tidy":
		return "go", []string{"mod", "tidy"}, true
	case "go vet ./...":
		return "go", []string{"vet", "./..."}, true
	case "gofly ai doctor --json":
		if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
			return "go", []string{"run", "./cmd/gofly", "ai", "doctor", "--json"}, true
		}
		return "gofly", []string{"ai", "doctor", "--json"}, true
	default:
		return "", nil, false
	}
}

func newAIProjectVerificationResult(command, status, output, errText string) aiProjectVerificationResult {
	return aiProjectVerificationResult{
		Command:     command,
		Status:      status,
		Output:      truncateVerificationOutput(output),
		Error:       sanitizeVerificationText(errText),
		NextActions: aiProjectVerificationNextActions(command, status),
	}
}

func truncateVerificationOutput(output string) string {
	output = strings.TrimSpace(sanitizeVerificationText(output))
	if len(output) <= maxVerificationOutputBytes {
		return output
	}
	const suffix = "\n... truncated ..."
	return output[:maxVerificationOutputBytes-len(suffix)] + suffix
}

func sanitizeVerificationText(text string) string {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	for idx, line := range lines {
		for _, marker := range []string{"Authorization", "Cookie", "Set-Cookie"} {
			if strings.Contains(line, marker+":") {
				lines[idx] = redactVerificationHeaderLine(line, marker)
				break
			}
		}
	}
	text = strings.Join(lines, "\n")
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "GOFLY_LLM_"} {
		text = redactVerificationAssignment(text, marker)
	}
	return text
}

func redactVerificationHeaderLine(line, header string) string {
	idx := strings.Index(line, header+":")
	if idx < 0 {
		return line
	}
	valueStart := idx + len(header) + 1
	end := len(line)
	for _, marker := range []string{" GOFLY_LLM_", " TOKEN", " SECRET", " PASSWORD"} {
		if markerIdx := strings.Index(strings.ToUpper(line[valueStart:]), marker); markerIdx >= 0 {
			end = min(end, valueStart+markerIdx)
		}
	}
	return strings.TrimRight(line[:idx+len(header)+1], " ") + " [REDACTED]" + line[end:]
}

func redactVerificationAssignment(text, marker string) string {
	fields := strings.Fields(text)
	for idx, field := range fields {
		upper := strings.ToUpper(field)
		if !strings.Contains(upper, marker) || !strings.Contains(field, "=") {
			continue
		}
		key := strings.SplitN(field, "=", 2)[0]
		fields[idx] = key + "=[REDACTED]"
	}
	if len(fields) == 0 {
		return text
	}
	return strings.Join(fields, " ")
}

func aiProjectVerificationNextActions(command, status string) []string {
	switch status {
	case "failed":
		return []string{
			"cd into the generated project output directory",
			"rerun `" + command + "` after fixing the reported error",
			"attach this bounded verification result to `gofly bug --json` support bundles if the failure persists",
		}
	case "skipped":
		return []string{
			"check the generated project template verify list before relying on this command",
			"run `gofly ai manifest --format json` to inspect supported verification commands",
		}
	default:
		return nil
	}
}
