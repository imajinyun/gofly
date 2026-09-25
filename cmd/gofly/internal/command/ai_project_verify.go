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

func newAIProjectVerificationEnv() ([]string, func(), error) {
	// Governance owns these explicitly supplied directories. Reuse the pair only
	// when both are usable, and leave their lifetime to the caller.
	if aiProjectVerificationDirWritable(os.Getenv("GOCACHE")) && aiProjectVerificationDirWritable(os.Getenv("GOTMPDIR")) {
		return aiProjectVerificationEnvValue(os.Environ(), "GOWORK", "off"), func() {}, nil
	}
	root, err := os.MkdirTemp("", "gofly-ai-verify-go-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	cacheDir := filepath.Join(root, "gocache")
	tmpDir := filepath.Join(root, "gotmp")
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		cleanup()
		return nil, nil, err
	}
	env := aiProjectVerificationEnvValue(os.Environ(), "GOCACHE", cacheDir)
	env = aiProjectVerificationEnvValue(env, "GOTMPDIR", tmpDir)
	env = aiProjectVerificationEnvValue(env, "GOWORK", "off")
	return env, cleanup, nil
}

func aiProjectVerificationDirWritable(dir string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	// Probe an existing caller-supplied directory without creating it or changing
	// permissions. CreateTemp uses an exclusive random filename within that root.
	probe, err := os.CreateTemp(dir, ".gofly-verify-write-*")
	if err != nil {
		return false
	}
	closeErr := probe.Close()
	removeErr := os.Remove(probe.Name())
	return closeErr == nil && removeErr == nil
}

func aiProjectVerificationEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		filtered = append(filtered, item)
	}
	return append(filtered, prefix+value)
}

func runAIProjectVerification(dir string, verify []string, timeout time.Duration) ([]aiProjectVerificationResult, bool, error) {
	if timeout <= 0 {
		return nil, false, fmt.Errorf("%w: verification timeout must be positive", errUsage)
	}
	env, cleanup, err := newAIProjectVerificationEnv()
	if err != nil {
		return nil, false, err
	}
	defer cleanup()
	results := make([]aiProjectVerificationResult, 0, len(verify))
	passed := true
	for _, command := range verify {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		command = expandAIProjectVerificationCommand(dir, command)
		var result aiProjectVerificationResult
		if command == "control-plane snapshot" {
			result = runAIProjectControlPlaneSnapshotAssertionWithEnv(dir, timeout, env)
			if result.Status == "skipped" {
				continue
			}
		} else {
			result = runAIProjectVerificationCommandWithEnv(dir, command, timeout, env)
		}
		if result.Status == "failed" {
			passed = false
		}
		results = append(results, result)
	}
	return results, passed, nil
}

// runAIProjectVerificationWithSnapshot keeps the generated snapshot assertion in
// the same cache lifetime as the template's verification commands.
func runAIProjectVerificationWithSnapshot(dir string, verify []string, timeout time.Duration) ([]aiProjectVerificationResult, bool, error) {
	commands := append(append([]string(nil), verify...), "control-plane snapshot")
	return runAIProjectVerification(dir, commands, timeout)
}

func runAIProjectControlPlaneSnapshotAssertion(dir string, timeout time.Duration) aiProjectVerificationResult {
	return runAIProjectControlPlaneSnapshotAssertionWithEnv(dir, timeout, nil)
}

func runAIProjectControlPlaneSnapshotAssertionWithEnv(dir string, timeout time.Duration, env []string) aiProjectVerificationResult {
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
	if env == nil {
		var cleanup func()
		env, cleanup, err = newAIProjectVerificationEnv()
		if err != nil {
			return newAIProjectVerificationResult(command, "failed", "", err.Error())
		}
		defer cleanup()
	}
	cmd.Env = env
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
	return runAIProjectVerificationCommandWithEnv(dir, command, timeout, nil)
}

func runAIProjectVerificationCommandWithEnv(dir, command string, timeout time.Duration, env []string) aiProjectVerificationResult {
	name, args, ok := aiProjectVerificationCommandArgs(command)
	if !ok {
		return newAIProjectVerificationResult(command, "skipped", "", "unsupported verification command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// #nosec G204 -- verification commands are selected from aiProjectVerificationCommandArgs allow-list and never executed through a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env == nil {
		var cleanup func()
		var err error
		env, cleanup, err = newAIProjectVerificationEnv()
		if err != nil {
			return newAIProjectVerificationResult(command, "failed", "", err.Error())
		}
		defer cleanup()
	}
	cmd.Env = env
	if command == "gofmt" {
		// Fresh generated modules may not have go.sum entries yet. Permit this
		// first verification step to resolve them before the explicit tidy
		// check, even when the parent governance process uses readonly flags.
		cmd.Env = aiProjectVerificationEnvValue(cmd.Env, "GOFLAGS", goFlagsWithModuleUpdates(os.Getenv("GOFLAGS")))
	}
	if command == "gofly ai doctor --json" ||
		strings.HasPrefix(command, "gofly gateway profile validate ") ||
		strings.HasPrefix(command, "gofly gateway aggregation validate ") {
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

func goFlagsWithModuleUpdates(flags string) string {
	fields := strings.Fields(flags)
	filtered := fields[:0]
	for _, field := range fields {
		if strings.HasPrefix(field, "-mod=") {
			continue
		}
		filtered = append(filtered, field)
	}
	return strings.Join(append(filtered, "-mod=mod"), " ")
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
		if len(fields) == 9 &&
			fields[0] == "gofly" &&
			fields[1] == "gateway" &&
			fields[2] == "profile" &&
			fields[3] == "validate" &&
			fields[4] == "--config" &&
			fields[6] == "--candidate" &&
			fields[8] == "--json" {
			if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
				return "go", []string{"run", "./cmd/gofly", "gateway", "profile", "validate", "--config", fields[5], "--candidate", fields[7], "--json"}, true
			}
			return "gofly", fields[1:], true
		}
		if len(fields) == 11 &&
			fields[0] == "gofly" &&
			fields[1] == "gateway" &&
			fields[2] == "aggregation" &&
			fields[3] == "validate" &&
			fields[4] == "--config" &&
			fields[6] == "--route" &&
			fields[8] == "--candidate" &&
			fields[10] == "--json" {
			if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
				return "go", []string{"run", "./cmd/gofly", "gateway", "aggregation", "validate", "--config", fields[5], "--route", fields[7], "--candidate", fields[9], "--json"}, true
			}
			return "gofly", fields[1:], true
		}
		if len(fields) == 11 &&
			fields[0] == "gofly" &&
			fields[1] == "gateway" &&
			fields[2] == "aggregation" &&
			fields[3] == "validate" &&
			fields[4] == "--openapi-base" &&
			fields[6] == "--openapi-candidate" &&
			fields[8] == "--route" &&
			fields[10] == "--json" {
			if frameworkPath := strings.TrimSpace(os.Getenv("GOFLY_FRAMEWORK_PATH")); frameworkPath != "" {
				return "go", []string{"run", "./cmd/gofly", "gateway", "aggregation", "validate", "--openapi-base", fields[5], "--openapi-candidate", fields[7], "--route", fields[9], "--json"}, true
			}
			return "gofly", fields[1:], true
		}
		return "", nil, false
	}
}

func expandAIProjectVerificationCommand(dir string, command string) string {
	name := strings.TrimSpace(filepath.Base(dir))
	if name == "." || name == string(filepath.Separator) {
		return command
	}
	command = strings.ReplaceAll(command, "<name>", name)
	return absolutizeGatewayValidateInputs(dir, command)
}

func absolutizeGatewayValidateInputs(dir string, command string) string {
	fields := strings.Fields(command)
	if len(fields) == 9 &&
		fields[0] == "gofly" &&
		fields[1] == "gateway" &&
		fields[2] == "profile" &&
		fields[3] == "validate" &&
		fields[4] == "--config" &&
		fields[6] == "--candidate" &&
		fields[8] == "--json" {
		return absolutizeGatewayValidateFieldInputs(dir, fields, []int{5, 7})
	}
	if len(fields) == 11 &&
		fields[0] == "gofly" &&
		fields[1] == "gateway" &&
		fields[2] == "aggregation" &&
		fields[3] == "validate" &&
		fields[4] == "--config" &&
		fields[6] == "--route" &&
		fields[8] == "--candidate" &&
		fields[10] == "--json" {
		return absolutizeGatewayValidateFieldInputs(dir, fields, []int{5, 9})
	}
	if len(fields) == 11 &&
		fields[0] == "gofly" &&
		fields[1] == "gateway" &&
		fields[2] == "aggregation" &&
		fields[3] == "validate" &&
		fields[4] == "--openapi-base" &&
		fields[6] == "--openapi-candidate" &&
		fields[8] == "--route" &&
		fields[10] == "--json" {
		return absolutizeGatewayValidateFieldInputs(dir, fields, []int{5, 7})
	}
	return command
}

func absolutizeGatewayValidateFieldInputs(dir string, fields []string, indexes []int) string {
	for _, index := range indexes {
		if !filepath.IsAbs(fields[index]) {
			fields[index] = filepath.Join(dir, fields[index])
		}
	}
	return strings.Join(fields, " ")
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
