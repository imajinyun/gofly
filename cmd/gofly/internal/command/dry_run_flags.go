package command

import "flag"

type dryRunPlanFlags struct {
	DryRun *bool
	Plan   *bool
}

func registerDryRunPlanFlags(fs *flag.FlagSet, dryRunUsage string) dryRunPlanFlags {
	return registerDryRunPlanFlagsWithDefault(fs, false, dryRunUsage, "alias for --dry-run")
}

func registerDryRunPlanFlagsWithDefault(fs *flag.FlagSet, defaultValue bool, dryRunUsage, planUsage string) dryRunPlanFlags {
	if dryRunUsage == "" {
		dryRunUsage = "print the planned actions without applying changes"
	}
	if planUsage == "" {
		planUsage = "alias for --dry-run"
	}
	return dryRunPlanFlags{
		DryRun: fs.Bool("dry-run", defaultValue, dryRunUsage),
		Plan:   fs.Bool("plan", defaultValue, planUsage),
	}
}

func (f dryRunPlanFlags) enabled() bool {
	return valueFromBoolFlag(f.DryRun) || valueFromBoolFlag(f.Plan)
}
