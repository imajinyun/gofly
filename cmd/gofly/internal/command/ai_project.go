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

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

type aiProjectPlan struct {
	Prompt            string                    `json:"prompt"`
	ProjectType       string                    `json:"projectType"`
	Template          generator.ProjectTemplate `json:"template"`
	Features          []string                  `json:"features"`
	Command           string                    `json:"command"`
	RiskLevel         string                    `json:"riskLevel"`
	MutatesFilesystem bool                      `json:"mutatesFilesystem"`
	DryRun            bool                      `json:"dryRun"`
	Verify            []string                  `json:"verify"`
	Warnings          []string                  `json:"warnings,omitempty"`
	NextActions       []string                  `json:"nextActions"`
}

type aiProjectApplyResult struct {
	Plan              aiProjectPlan                    `json:"plan"`
	Applied           bool                             `json:"applied"`
	OutputDir         string                           `json:"outputDir"`
	ExecutedCommand   string                           `json:"executedCommand"`
	GeneratedFeatures []generator.ProjectFeatureResult `json:"generatedFeatures,omitempty"`
	Dependencies      []string                         `json:"dependencies,omitempty"`
	ConfigHints       []generator.ConfigHint           `json:"configHints,omitempty"`
	FeatureVerify     []string                         `json:"featureVerify,omitempty"`
	Verify            []string                         `json:"verify"`
	VerifyRan         bool                             `json:"verifyRan"`
	VerifyPassed      bool                             `json:"verifyPassed"`
	Verification      []aiProjectVerificationResult    `json:"verification,omitempty"`
	Warnings          []string                         `json:"warnings,omitempty"`
	NextActions       []string                         `json:"nextActions"`
	MutatesFilesystem bool                             `json:"mutatesFilesystem"`
}

type aiProjectVerificationResult struct {
	Command string `json:"command"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

type aiProjectApplyOptions struct {
	Verify        bool
	VerifyTimeout time.Duration
}

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
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: "verification timeout must be positive"}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	defer func() { _ = root.Close() }()
	testFile, err := root.Open(filepath.Join("internal", "config", "config_test.go"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "generated project does not expose a control-plane snapshot contract test"}
		}
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	data, err := io.ReadAll(testFile)
	_ = testFile.Close()
	if err != nil {
		return aiProjectVerificationResult{Command: command, Status: "failed", Error: err.Error()}
	}
	if !strings.Contains(string(data), "TestControlPlaneSnapshotExposesGeneratedContract") {
		return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "generated project does not expose a control-plane snapshot contract test"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./internal/config", "-run", "TestControlPlaneSnapshotExposesGeneratedContract", "-count=1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := aiProjectVerificationResult{Command: command, Status: "passed", Output: truncateVerificationOutput(string(out))}
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "failed"
		result.Error = "control-plane snapshot assertion timed out"
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	return result
}

func runAIProjectVerificationCommand(dir, command string, timeout time.Duration) aiProjectVerificationResult {
	name, args, ok := aiProjectVerificationCommandArgs(command)
	if !ok {
		return aiProjectVerificationResult{Command: command, Status: "skipped", Error: "unsupported verification command"}
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
	result := aiProjectVerificationResult{Command: command, Status: "passed", Output: truncateVerificationOutput(string(out))}
	if ctx.Err() == context.DeadlineExceeded {
		result.Status = "failed"
		result.Error = "verification command timed out"
		return result
	}
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
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

func truncateVerificationOutput(output string) string {
	const maxVerificationOutputBytes = 4096
	output = strings.TrimSpace(output)
	if len(output) <= maxVerificationOutputBytes {
		return output
	}
	return output[:maxVerificationOutputBytes] + "\n... truncated ..."
}

func printAIProjectPlanText(projectPlan aiProjectPlan) {
	cliOutputfIf("template=%s kind=%s risk=%s\n", projectPlan.Template.ID, projectPlan.ProjectType, projectPlan.RiskLevel)
	cliOutputfIf("features=%s\n", strings.Join(projectPlan.Features, ","))
	cliOutputfIf("command=%s\n", projectPlan.Command)
	for _, warning := range projectPlan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
}
