package command

import (
	"flag"
	"fmt"
	"strings"
)

func aiPlanCommand(args []string) error {
	fs := flag.NewFlagSet("ai plan", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "natural language project requirement")
	kind := fs.String("kind", "", "optional project kind hint, such as service, rpc, worker, cli, ai-agent, rag or gateway")
	name := fs.String("name", "", "project or service name used in the generated command")
	module := fs.String("module", "", "Go module path used in the generated command")
	dir := fs.String("dir", "", "output directory used in the generated command")
	outputFlags := registerCLIOutputFlags(fs, cliOutputFlagOptions{JSONUsage: "output JSON envelope"})
	preview := registerDryRunPlanFlagsWithDefault(fs, true, "plan only without writing files", "alias for --dry-run")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai plan accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("%w: --prompt or positional prompt text is required for `gofly ai plan`", errUsage)
	}
	format, err := outputFlags.normalizedFormat(outputText)
	if err != nil {
		return err
	}
	projectPlan := buildAIProjectPlan(*prompt, *kind, *name, *module, *dir, preview.enabled())
	if outputFlags.useJSON(format) {
		return printJSONEnvelope("ai.plan", projectPlan)
	}
	cliOutputfIf("template=%s kind=%s risk=%s\n", projectPlan.Template.ID, projectPlan.ProjectType, projectPlan.RiskLevel)
	cliOutputfIf("features=%s\n", strings.Join(projectPlan.Features, ","))
	cliOutputfIf("command=%s\n", projectPlan.Command)
	for _, warning := range projectPlan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
	return nil
}
