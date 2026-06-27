package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// rpcNewCommand 实现 `gofly new rpc` 与 `gofly rpc new`。
func rpcNewCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("new rpc", flag.ContinueOnError)
	baseFlags := registerNewScaffoldBaseFlags(fs, newScaffoldBaseFlagOptions{
		NameUsage:   "rpc service name",
		StyleUsage:  "rpc scaffold style: minimal, basic, or production",
		ConfigUsage: "gofly config file path",
	})
	profileFlags := registerNewScaffoldProfileFlags(fs)
	templateFlags := registerNewScaffoldTemplateSourceFlags(fs)
	discoveryFlags := registerDiscoveryCLIFlags(fs)
	compatFlags := registerNewRPCCompatFlags(fs)
	verbosityFlags := registerNewScaffoldVerbosityFlags(fs)
	extensionFlags := registerNewScaffoldExtensionFlags(fs, "rpc-plugin")
	executionFlags := registerNewScaffoldExecutionFlags(fs)
	_ = compatFlags
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	normalizeNewScaffoldFlagGroups(newScaffoldNormalizeOptions{
		Base:          baseFlags,
		Template:      templateFlags,
		Profile:       &profileFlags,
		Extension:     &extensionFlags,
		Verbosity:     &verbosityFlags,
		LeadingName:   leadingName,
		RemainingArgs: remaining,
	})
	verboseOutputf("new rpc: configuring service %q in %s\n", *baseFlags.Name, *baseFlags.Dir)
	loadOpts := newScaffoldLoadOptionsFromFlags("rpc", baseFlags, templateFlags, extensionFlags, discoveryFlags)
	loadCtx, err := loadNewScaffoldContext(loadOpts)
	if err != nil {
		return err
	}
	cfg := loadCtx.Config
	resolved := loadCtx.ConfigPath
	applyNewScaffoldDefaults(cfg, baseFlags, generator.ServiceStyleProduction, true)
	plugins := loadCtx.PluginNames
	resolvedProfile := resolveNewRPCProfile(cfg, *profileFlags.Profile)
	output := newScaffoldPlanOutputFor("new.rpc", "new rpc", *baseFlags.Dir, resolved, cfg, plugins, newServiceContractInputs{}, *executionFlags.SaveConfig)
	if *executionFlags.DryRun || *executionFlags.Plan {
		return output.printDryRunPlan(*executionFlags.JSON, false)
	}
	if err := generateNewRPCScaffold(cfg, newRPCScaffoldOptions{
		Dir:             *baseFlags.Dir,
		ResolvedProfile: resolvedProfile,
		Plugins:         plugins,
	}); err != nil {
		return err
	}
	return output.finalize(*executionFlags.SaveConfig, cfg, *executionFlags.JSON)
}
