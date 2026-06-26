package command

import (
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

var runUpgradeInstall = func(target string) ([]byte, error) {
	// #nosec G204 -- upgrade execute intentionally runs `go install` with a single module@version argv value, never through a shell.
	return exec.Command("go", "install", target).CombinedOutput()
}

type upgradePlan struct {
	Command          []string `json:"command"`
	Target           string   `json:"target"`
	Module           string   `json:"module"`
	Version          string   `json:"version"`
	Execute          bool     `json:"execute"`
	ProjectDir       string   `json:"projectDir,omitempty"`
	GeneratedProject bool     `json:"generatedProject,omitempty"`
	DiffCommand      []string `json:"diffCommand,omitempty"`
	VerifyCommand    []string `json:"verifyCommand,omitempty"`
	Output           string   `json:"output,omitempty"`
}

func upgradeCommand(args []string) error {
	if printCommandHelp("upgrade", args) {
		return nil
	}
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	version := fs.String("version", "latest", "version to install")
	module := fs.String("module", "github.com/imajinyun/gofly/cmd/gofly", "module path to install")
	projectDir := fs.String("project-dir", "", "generated project directory to include upgrade/diff verification commands")
	dir := fs.String("dir", "", "alias for --project-dir")
	execute := fs.Bool("execute", false, "execute go install instead of printing the upgrade command")
	jsonOutput := fs.Bool("json", false, "print upgrade plan/result as JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if len(remaining) > 0 {
		return fmt.Errorf("%w: upgrade does not accept positional arguments; use --version %s", errUsage, remaining[0])
	}
	*module = strings.TrimSpace(*module)
	*version = strings.TrimSpace(*version)
	if *module == "" {
		return fmt.Errorf("%w: upgrade module is required", errUsage)
	}
	if *version == "" {
		return fmt.Errorf("%w: upgrade version is required", errUsage)
	}
	if *projectDir == "" {
		*projectDir = strings.TrimSpace(*dir)
	}
	target := *module + "@" + *version
	plan := upgradePlan{
		Command:    []string{"go", "install", target},
		Target:     target,
		Module:     *module,
		Version:    *version,
		Execute:    *execute,
		ProjectDir: strings.TrimSpace(*projectDir),
	}
	if plan.ProjectDir != "" {
		plan.GeneratedProject = true
		plan.DiffCommand = []string{"gofly", "api", "diff", "--base", filepath.Join(plan.ProjectDir, "api.previous.api"), "--target", filepath.Join(plan.ProjectDir, "api.current.api"), "--format", "json"}
		plan.VerifyCommand = []string{"go", "test", "./..."}
	}
	if !*execute {
		if *jsonOutput {
			return printJSON(plan)
		}
		cliOutputf("go install %s\n", target)
		if plan.GeneratedProject {
			cliOutputf("# generated project diff: %s\n", strings.Join(plan.DiffCommand, " "))
			cliOutputf("# generated project verify: cd %s && %s\n", plan.ProjectDir, strings.Join(plan.VerifyCommand, " "))
		}
		return nil
	}
	if check := envToolCheck("go"); check.Status != "ok" {
		return fmt.Errorf("upgrade gofly: go tool is missing")
	}
	out, err := runUpgradeInstall(target)
	plan.Output = string(out)
	if len(out) > 0 && !*jsonOutput {
		cliOutput(string(out))
	}
	if err != nil {
		return fmt.Errorf("upgrade gofly: %w", err)
	}
	if *jsonOutput {
		return printJSON(plan)
	}
	return nil
}
