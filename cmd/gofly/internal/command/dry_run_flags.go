package command

import "flag"

type dryRunPlanFlags struct {
	DryRun *bool
	Plan   *bool
}

func registerDryRunPlanFlags(fs *flag.FlagSet, dryRunUsage string) dryRunPlanFlags {
	if dryRunUsage == "" {
		dryRunUsage = "print the planned actions without applying changes"
	}
	return dryRunPlanFlags{
		DryRun: fs.Bool("dry-run", false, dryRunUsage),
		Plan:   fs.Bool("plan", false, "alias for --dry-run"),
	}
}

func (f dryRunPlanFlags) enabled() bool {
	return valueFromBoolFlag(f.DryRun) || valueFromBoolFlag(f.Plan)
}
