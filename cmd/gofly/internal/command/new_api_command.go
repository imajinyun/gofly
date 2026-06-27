package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// apiNewCommand 实现 `gofly new api` 与 `gofly api new`。
// 除了基本的 --name / --module / --dir / --style / --api-spec 外，
// 还支持「配置驱动」选项：--config / --template-dir / --feature / --plugin / --save-config。
func apiNewCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("new api", flag.ContinueOnError)
	baseFlags := registerNewScaffoldBaseFlags(fs, newScaffoldBaseFlagOptions{
		NameUsage:   "api service name",
		StyleUsage:  "api scaffold style: minimal, basic, or production",
		ConfigUsage: "gofly config file path (defaults to <dir>/.gofly/config.json)",
	})
	profileFlags := registerNewScaffoldProfileFlags(fs)
	apiSpec := fs.Bool("api-spec", true, "generate an .api file")
	templateFlags := registerNewScaffoldTemplateSourceFlags(fs)
	discoveryFlags := registerDiscoveryCLIFlags(fs)
	compatFlags := registerNewAPICompatFlags(fs)
	verbosityFlags := registerNewScaffoldVerbosityFlags(fs)
	extensionFlags := registerNewScaffoldExtensionFlags(fs, "api-plugin")
	executionFlags := registerNewScaffoldExecutionFlags(fs)
	_ = compatFlags
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	normalizeNewScaffoldFlags(newScaffoldFlagNormalization{
		Name:          baseFlags.Name,
		Dir:           baseFlags.Dir,
		TemplateDir:   templateFlags.TemplateDir,
		TemplateHome:  templateFlags.Home,
		Profile:       profileFlags.Profile,
		ProfileAlias:  profileFlags.ProfileAlias,
		Plugin:        extensionFlags.Plugin,
		PluginAlias:   extensionFlags.PluginAlias,
		Verbose:       verbosityFlags.Verbose,
		VerboseAlias:  verbosityFlags.VerboseAlias,
		Quiet:         verbosityFlags.Quiet,
		QuietAlias:    verbosityFlags.QuietAlias,
		LeadingName:   leadingName,
		RemainingArgs: remaining,
	})
	verboseOutputf("new api: configuring service %q in %s\n", *baseFlags.Name, *baseFlags.Dir)
	loadCtx, err := loadNewScaffoldContext(newScaffoldLoadOptions{
		ConfigPath:     *baseFlags.ConfigPath,
		Dir:            *baseFlags.Dir,
		Name:           *baseFlags.Name,
		Module:         *baseFlags.Module,
		Style:          *baseFlags.Style,
		TemplateDir:    *templateFlags.TemplateDir,
		TemplateRemote: *templateFlags.Remote,
		TemplateBranch: *templateFlags.Branch,
		Features:       joinCSV(*extensionFlags.Features, *extensionFlags.FeaturesAlias),
		Plugins:        *extensionFlags.Plugin,
		Kind:           "api",
		Discovery:      discoveryFlags,
	})
	if err != nil {
		return err
	}
	cfg := loadCtx.Config
	resolved := loadCtx.ConfigPath
	applyNewScaffoldStyleDefault(cfg, *baseFlags.Style, generator.ServiceStyleBasic, false)
	applyNewScaffoldDirFallback(baseFlags.Dir, cfg)
	plugins := loadCtx.PluginNames
	resolvedProfile, err := resolveNewAPIProfile(cfg, *profileFlags.Profile)
	if err != nil {
		return err
	}
	output := newScaffoldPlanOutputFor("new.api", "new api", *baseFlags.Dir, resolved, cfg, plugins, newServiceContractInputs{}, *executionFlags.SaveConfig)
	if *executionFlags.DryRun || *executionFlags.Plan {
		return output.printDryRunPlan(*executionFlags.JSON, false)
	}
	if err := generateNewAPIScaffold(cfg, newAPIScaffoldOptions{
		Dir:             *baseFlags.Dir,
		ResolvedProfile: resolvedProfile,
		Plugins:         plugins,
		SkipAPISpec:     !*apiSpec,
	}); err != nil {
		return err
	}
	if err := saveNewScaffoldConfig(*executionFlags.SaveConfig, resolved, cfg); err != nil {
		return err
	}
	return output.printResultWhenRequested(*executionFlags.JSON)
}
