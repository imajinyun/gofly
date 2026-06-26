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
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	dryRun := fs.Bool("dry-run", true, "plan only without writing files")
	plan := fs.Bool("plan", true, "alias for --dry-run")
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
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	projectPlan := buildAIProjectPlan(*prompt, *kind, *name, *module, *dir, *dryRun || *plan)
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
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
