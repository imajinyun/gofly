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
