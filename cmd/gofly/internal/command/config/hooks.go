package config

import (
	"errors"
	"flag"
	"fmt"
)

// Hooks injects command-package collaborators so the config family can live
// in its own package without importing command.
type Hooks struct {
	PrintHelp           func(command string, args []string) bool
	ParseFlags          func(fs *flag.FlagSet, args []string) ([]string, error)
	RegisterDryRunFlags func(fs *flag.FlagSet, usage string) func() bool
	PrintPlan           func(command string, plan Plan) error
	PrintTextf          func(format string, args ...any)
	PrintTextln         func(args ...any)
	Usage               error
}

// Plan is the dry-run/plan payload for config mutations.
type Plan struct {
	Command           string            `json:"command"`
	DryRun            bool              `json:"dryRun"`
	MutatesFilesystem bool              `json:"mutatesFilesystem"`
	Inputs            map[string]string `json:"inputs,omitempty"`
	Actions           []PlanAction      `json:"actions"`
	NextActions       []string          `json:"nextActions,omitempty"`
}

// PlanAction describes one filesystem mutation in a config plan.
type PlanAction struct {
	Operation   string `json:"operation"`
	Target      string `json:"target"`
	Description string `json:"description"`
	RiskLevel   string `json:"riskLevel"`
}

func normalizeHooks(hooks Hooks) Hooks {
	if hooks.PrintHelp == nil {
		hooks.PrintHelp = func(string, []string) bool { return false }
	}
	if hooks.ParseFlags == nil {
		hooks.ParseFlags = func(fs *flag.FlagSet, args []string) ([]string, error) {
			if err := fs.Parse(args); err != nil {
				return nil, err
			}
			return fs.Args(), nil
		}
	}
	if hooks.RegisterDryRunFlags == nil {
		hooks.RegisterDryRunFlags = func(fs *flag.FlagSet, usage string) func() bool {
			dryRun := fs.Bool("dry-run", false, usage)
			plan := fs.Bool("plan", false, "alias for --dry-run")
			return func() bool {
				return valueFromBoolFlag(dryRun) || valueFromBoolFlag(plan)
			}
		}
	}
	if hooks.PrintPlan == nil {
		hooks.PrintPlan = func(command string, plan Plan) error {
			fmt.Printf("%s plan (dry-run=%t, mutates-filesystem=%t)\n", command, plan.DryRun, plan.MutatesFilesystem)
			return nil
		}
	}
	if hooks.PrintTextf == nil {
		hooks.PrintTextf = func(format string, args ...any) { fmt.Printf(format, args...) }
	}
	if hooks.PrintTextln == nil {
		hooks.PrintTextln = func(args ...any) { fmt.Println(args...) }
	}
	if hooks.Usage == nil {
		hooks.Usage = errors.New("invalid usage")
	}
	return hooks
}

func valueFromBoolFlag(value *bool) bool {
	return value != nil && *value
}
