package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// rpcNewCommand 实现 `gofly new rpc` 与 `gofly rpc new`。
func rpcNewCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("new rpc", flag.ContinueOnError)
	name := fs.String("name", "", "rpc service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	style := fs.String("style", "", "rpc scaffold style: minimal, basic, or production")
	profile := fs.String("profile", "", "generation profile: gofly-ai, gozero-compatible, or kitex-compatible")
	profileAlias := fs.String("generation-profile", "", "alias for --profile")
	configPath := fs.String("config", "", "gofly config file path")
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
	normalizeNewScaffoldFlags(newScaffoldFlagNormalization{
		Name:          name,
		Dir:           dir,
		TemplateDir:   templateFlags.TemplateDir,
		TemplateHome:  templateFlags.Home,
		Profile:       profile,
		ProfileAlias:  profileAlias,
		Plugin:        extensionFlags.Plugin,
		PluginAlias:   extensionFlags.PluginAlias,
		Verbose:       verbosityFlags.Verbose,
		VerboseAlias:  verbosityFlags.VerboseAlias,
		Quiet:         verbosityFlags.Quiet,
		QuietAlias:    verbosityFlags.QuietAlias,
		LeadingName:   leadingName,
		RemainingArgs: remaining,
	})
	verboseOutputf("new rpc: configuring service %q in %s\n", *name, *dir)
	loadCtx, err := loadNewScaffoldContext(newScaffoldLoadOptions{
		ConfigPath:     *configPath,
		Dir:            *dir,
		Name:           *name,
		Module:         *module,
		Style:          *style,
		TemplateDir:    *templateFlags.TemplateDir,
		TemplateRemote: *templateFlags.Remote,
		TemplateBranch: *templateFlags.Branch,
		Features:       joinCSV(*extensionFlags.Features, *extensionFlags.FeaturesAlias),
		Plugins:        *extensionFlags.Plugin,
		Kind:           "rpc",
		Discovery:      discoveryFlags,
	})
	if err != nil {
		return err
	}
	cfg := loadCtx.Config
	resolved := loadCtx.ConfigPath
	applyNewScaffoldStyleDefault(cfg, *style, generator.ServiceStyleProduction, true)
	applyNewScaffoldDirFallback(dir, cfg)
	plugins := loadCtx.PluginNames
	resolvedProfile := resolveNewRPCProfile(cfg, *profile)
	output := newScaffoldPlanOutputFor("new.rpc", "new rpc", *dir, resolved, cfg, plugins, newServiceContractInputs{}, *executionFlags.SaveConfig)
	if *executionFlags.DryRun || *executionFlags.Plan {
		return output.printDryRunPlan(*executionFlags.JSON, false)
	}
	if err := generateNewRPCScaffold(cfg, newRPCScaffoldOptions{
		Dir:             *dir,
		ResolvedProfile: resolvedProfile,
		Plugins:         plugins,
	}); err != nil {
		return err
	}
	if err := saveNewScaffoldConfig(*executionFlags.SaveConfig, resolved, cfg); err != nil {
		return err
	}
	return output.printResultWhenRequested(*executionFlags.JSON)
}
