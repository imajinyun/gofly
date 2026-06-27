package command

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

func aiNewCommand(args []string) error {
	fs := flag.NewFlagSet("ai new", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "natural language project requirement")
	kind := fs.String("kind", "", "optional project kind hint, such as service, rpc, worker, cli, ai-agent, rag or gateway")
	templateID := fs.String("template", "", "explicit project template id; run `gofly template list --json` to inspect choices")
	name := fs.String("name", "", "project or service name")
	module := fs.String("module", "", "Go module path")
	dir := fs.String("dir", "", "output directory")
	outputFlags := registerCLIOutputFlags(fs, cliOutputFlagOptions{JSONUsage: "output JSON envelope"})
	preview := registerDryRunPlanFlagsWithDefaults(fs, true, false, "print the scaffold plan without writing files", "alias for --dry-run")
	apply := fs.Bool("apply", false, "apply the planned scaffold and write files")
	verify := fs.Bool("verify", false, "run supported post-generation verification commands after --apply")
	verifyTimeoutText := fs.String("verify-timeout", "2m", "timeout for each verification command")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai new accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" && strings.TrimSpace(*templateID) == "" {
		return fmt.Errorf("%w: --prompt, positional prompt text, or --template is required for `gofly ai new`", errUsage)
	}
	format, err := outputFlags.normalizedFormat(outputText)
	if err != nil {
		return err
	}
	if *apply && preview.enabled() && !flagWasProvided(fs, "dry-run") && !flagWasProvided(fs, "plan") {
		setBoolFlag(preview.DryRun, false)
	}
	if *apply && preview.enabled() {
		return fmt.Errorf("%w: --apply cannot be combined with --dry-run or --plan", errUsage)
	}
	verifyTimeout, err := time.ParseDuration(strings.TrimSpace(*verifyTimeoutText))
	if err != nil || verifyTimeout <= 0 {
		return fmt.Errorf("%w: --verify-timeout must be a positive duration", errUsage)
	}
	projectPlan, err := buildAIProjectNewPlan(*prompt, *kind, *templateID, *name, *module, *dir, !*apply || preview.enabled())
	if err != nil {
		return err
	}
	if !*apply {
		if outputFlags.useJSON(format) {
			return printJSONEnvelope("ai.new", projectPlan)
		}
		printAIProjectPlanText(projectPlan)
		return nil
	}
	result, err := applyAIProjectPlan(projectPlan, aiProjectApplyOptions{Verify: *verify, VerifyTimeout: verifyTimeout})
	if err != nil {
		return err
	}
	if outputFlags.useJSON(format) {
		return printJSONEnvelope("ai.new", result)
	}
	cliOutputfIf("applied template=%s kind=%s output=%s\n", result.Plan.Template.ID, result.Plan.ProjectType, result.OutputDir)
	cliOutputfIf("command=%s\n", result.ExecutedCommand)
	if len(result.GeneratedFeatures) > 0 {
		for _, feature := range result.GeneratedFeatures {
			cliOutputfIf("feature=%s files=%s\n", feature.Plugin, strings.Join(feature.Files, ","))
			if len(feature.Dependencies) > 0 {
				cliOutputfIf("  dependencies=%s\n", strings.Join(feature.Dependencies, ","))
			}
			for _, hint := range feature.ConfigHints {
				cliOutputfIf("  configHint=%s description=%q example=%q\n", hint.Key, hint.Description, hint.Example)
			}
			if len(feature.VerifyCommands) > 0 {
				cliOutputfIf("  verify=%s\n", strings.Join(feature.VerifyCommands, ","))
			}
		}
	}
	if result.VerifyRan {
		cliOutputfIf("verify=%t\n", result.VerifyPassed)
		for _, check := range result.Verification {
			cliOutputfIf("  - %s: %s\n", check.Command, check.Status)
		}
	}
	for _, warning := range result.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
	for _, next := range result.NextActions {
		cliOutputfIf("next: %s\n", next)
	}
	return nil
}
