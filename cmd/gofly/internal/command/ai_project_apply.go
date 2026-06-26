package command

import (
	"fmt"
	"io"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func applyAIProjectPlan(plan aiProjectPlan, opts aiProjectApplyOptions) (aiProjectApplyResult, error) {
	if err := validateAIProjectApplyInputs(plan); err != nil {
		return aiProjectApplyResult{}, err
	}
	name, module, dir := aiProjectPlanValues(plan)
	commandArgs, err := aiProjectApplyArgs(plan)
	if err != nil {
		return aiProjectApplyResult{}, err
	}
	if len(commandArgs) == 0 {
		return aiProjectApplyResult{}, fmt.Errorf("%w: no scaffold command generated", errUsage)
	}
	if err := withCommandIO(IOStreams{In: nil, Out: io.Discard, Err: currentErr()}, outputText, verbosityQuiet, func() error {
		return runAIProjectApplyCommand(commandArgs)
	}); err != nil {
		return aiProjectApplyResult{}, err
	}
	generatedFeatures, err := generator.ApplyProjectFeaturePlugins(generator.ProjectFeatureOptions{
		Dir:      dir,
		Name:     name,
		Module:   module,
		Features: plan.Features,
	})
	if err != nil {
		return aiProjectApplyResult{}, err
	}
	featureDependencies, featureConfigHints, featureVerify := aggregateProjectFeatureContract(generatedFeatures)
	verifyCommands := appendUniqueStrings(append([]string(nil), plan.Verify...), featureVerify...)
	warnings := append([]string(nil), plan.Warnings...)
	warnings = append(warnings, "ai new --apply writes scaffold files using built-in local generators only")
	verification := []aiProjectVerificationResult(nil)
	verifyPassed := false
	if opts.Verify {
		var err error
		verification, verifyPassed, err = runAIProjectVerification(dir, verifyCommands, opts.VerifyTimeout)
		if err != nil {
			return aiProjectApplyResult{}, err
		}
		controlPlaneResult := runAIProjectControlPlaneSnapshotAssertion(dir, opts.VerifyTimeout)
		if controlPlaneResult.Status != "skipped" {
			if controlPlaneResult.Status == "failed" {
				verifyPassed = false
			}
			verification = append(verification, controlPlaneResult)
		}
	} else {
		warnings = append(warnings, "generated verification commands are reported but not executed; pass --verify to run supported checks")
	}
	return aiProjectApplyResult{
		Plan:              plan,
		Applied:           true,
		OutputDir:         dir,
		ExecutedCommand:   "gofly " + strings.Join(commandArgs, " "),
		GeneratedFeatures: generatedFeatures,
		Dependencies:      featureDependencies,
		ConfigHints:       featureConfigHints,
		FeatureVerify:     featureVerify,
		Verify:            verifyCommands,
		VerifyRan:         opts.Verify,
		VerifyPassed:      verifyPassed,
		Verification:      verification,
		Warnings:          warnings,
		NextActions: aiProjectApplyNextActions(
			dir,
			verifyCommands,
			featureDependencies,
			featureConfigHints,
			opts.Verify,
			verifyPassed,
		),
		MutatesFilesystem: true,
	}, nil
}

func aggregateProjectFeatureContract(features []generator.ProjectFeatureResult) ([]string, []generator.ConfigHint, []string) {
	dependencies := []string{}
	configHints := []generator.ConfigHint{}
	verifyCommands := []string{}
	seenConfigHints := map[string]struct{}{}
	for _, feature := range features {
		dependencies = appendUniqueStrings(dependencies, feature.Dependencies...)
		verifyCommands = appendUniqueStrings(verifyCommands, feature.VerifyCommands...)
		for _, hint := range feature.ConfigHints {
			key := strings.ToLower(strings.TrimSpace(hint.Key))
			if key == "" {
				continue
			}
			if _, ok := seenConfigHints[key]; ok {
				continue
			}
			seenConfigHints[key] = struct{}{}
			configHints = append(configHints, hint)
		}
	}
	return dependencies, configHints, verifyCommands
}

func appendUniqueStrings(values []string, more ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(more))
	unique := make([]string, 0, len(values)+len(more))
	for _, value := range append(values, more...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func aiProjectApplyNextActions(
	dir string,
	verify []string,
	dependencies []string,
	configHints []generator.ConfigHint,
	verifyRan bool,
	verifyPassed bool,
) []string {
	next := []string{"cd " + dir}
	if len(dependencies) > 0 {
		next = append(next, "review feature dependencies: go get "+strings.Join(dependencies, " "))
	}
	for _, hint := range configHints {
		action := "configure " + hint.Key + ": " + hint.Description
		if hint.Example != "" {
			action += " (example: " + hint.Example + ")"
		}
		next = append(next, action)
	}
	if len(verify) == 0 {
		return next
	}
	if !verifyRan {
		return append(next, "run: "+strings.Join(verify, " && "))
	}
	if verifyPassed {
		return append(next, "review generated files and commit when ready")
	}
	return append(next, "fix failed verification output, then rerun: "+strings.Join(verify, " && "))
}

func runAIProjectApplyCommand(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("%w: scaffold command is incomplete", errUsage)
	}
	switch {
	case args[0] == "new" && args[1] == "service":
		return serviceNewCommand(args[2:])
	case args[0] == "new" && args[1] == "api":
		return apiNewCommand(args[2:])
	case args[0] == "new" && args[1] == "rpc":
		return rpcNewCommand(args[2:])
	case args[0] == "gen" && args[1] == "gateway":
		return gatewayGenCommand(args[2:])
	default:
		return fmt.Errorf("%w: unsupported scaffold command `gofly %s`", errUsage, strings.Join(args, " "))
	}
}
