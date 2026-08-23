package command

import (
	"flag"

	commandconfig "github.com/imajinyun/gofly/cmd/gofly/internal/command/config"
	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func configCommand(args []string) error {
	return commandconfig.Command(args, configHooks())
}

func configHooks() commandconfig.Hooks {
	return commandconfig.Hooks{
		PrintHelp:  printCommandHelp,
		ParseFlags: parseInterspersedFlags,
		RegisterDryRunFlags: func(fs *flag.FlagSet, usage string) func() bool {
			preview := registerDryRunPlanFlags(fs, usage)
			return preview.enabled
		},
		PrintPlan: func(command string, plan commandconfig.Plan) error {
			return printCLIPlan(command, configPlanToCLI(plan))
		},
		PrintTextf:  cliOutputf,
		PrintTextln: cliOutputln,
		Usage:       errUsage,
	}
}

func configPlanToCLI(plan commandconfig.Plan) cliPlan {
	actions := make([]cliPlanAction, len(plan.Actions))
	for i, action := range plan.Actions {
		actions[i] = cliPlanAction{
			Operation:   action.Operation,
			Target:      action.Target,
			Description: action.Description,
			RiskLevel:   action.RiskLevel,
		}
	}
	return cliPlan{
		Command:           plan.Command,
		DryRun:            plan.DryRun,
		MutatesFilesystem: plan.MutatesFilesystem,
		Inputs:            plan.Inputs,
		Actions:           actions,
		NextActions:       plan.NextActions,
	}
}

func getConfigField(cfg *generator.Config, key string) string {
	return commandconfig.GetField(cfg, key)
}

func setConfigField(cfg *generator.Config, key, value string) error {
	return commandconfig.SetField(cfg, key, value, errUsage)
}

func isConfigFeaturesKey(key string) bool {
	return commandconfig.IsFeaturesKey(key)
}

func ensureModelConfig(cfg *generator.Config) *generator.ModelConfig {
	return commandconfig.EnsureModelConfig(cfg)
}
