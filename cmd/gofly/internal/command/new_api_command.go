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
	name := fs.String("name", "", "api service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	style := fs.String("style", "", "api scaffold style: minimal, basic, or production")
	profile := fs.String("profile", "", "generation profile: gofly-ai, gozero-compatible, or kitex-compatible")
	profileAlias := fs.String("generation-profile", "", "alias for --profile")
	apiSpec := fs.Bool("api-spec", true, "generate an .api file")
	configPath := fs.String("config", "", "gofly config file path (defaults to <dir>/.gofly/config.json)")
	templateFlags := registerNewScaffoldTemplateSourceFlags(fs)
	discoveryFlags := registerDiscoveryCLIFlags(fs)
	compatFlags := registerNewAPICompatFlags(fs)
	verbosityFlags := registerNewScaffoldVerbosityFlags(fs)
	features := fs.String("feature", "", "feature names to enable, comma-separated")
	featuresAlias := fs.String("features", "", "alias for --feature")
	pluginArg := fs.String("plugin", "", "plugin executable (comma-separated for multiple)")
	apiPluginArg := fs.String("api-plugin", "", "alias for --plugin")
	saveConfig := fs.Bool("save-config", true, "save resolved config back to --config path")
	dryRun := fs.Bool("dry-run", false, "print the planned filesystem changes without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	jsonOut := fs.Bool("json", false, "emit scaffold result as JSON")
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
		Plugin:        pluginArg,
		PluginAlias:   apiPluginArg,
		Verbose:       verbosityFlags.Verbose,
		VerboseAlias:  verbosityFlags.VerboseAlias,
		Quiet:         verbosityFlags.Quiet,
		QuietAlias:    verbosityFlags.QuietAlias,
		LeadingName:   leadingName,
		RemainingArgs: remaining,
	})
	verboseOutputf("new api: configuring service %q in %s\n", *name, *dir)
	loadCtx, err := loadNewScaffoldContext(newScaffoldLoadOptions{
		ConfigPath:     *configPath,
		Dir:            *dir,
		Name:           *name,
		Module:         *module,
		Style:          *style,
		TemplateDir:    *templateFlags.TemplateDir,
		TemplateRemote: *templateFlags.Remote,
		TemplateBranch: *templateFlags.Branch,
		Features:       joinCSV(*features, *featuresAlias),
		Plugins:        *pluginArg,
		Kind:           "api",
		Discovery:      discoveryFlags,
	})
	if err != nil {
		return err
	}
	cfg := loadCtx.Config
	resolved := loadCtx.ConfigPath
	applyNewScaffoldStyleDefault(cfg, *style, generator.ServiceStyleBasic, false)
	applyNewScaffoldDirFallback(dir, cfg)
	plugins := loadCtx.PluginNames
	resolvedProfile, err := resolveNewAPIProfile(cfg, *profile)
	if err != nil {
		return err
	}
	output := newScaffoldPlanOutputFor("new.api", "new api", *dir, resolved, cfg, plugins, newServiceContractInputs{}, *saveConfig)
	if *dryRun || *plan {
		return output.printDryRunPlan(*jsonOut, false)
	}
	if err := generateNewAPIScaffold(cfg, newAPIScaffoldOptions{
		Dir:             *dir,
		ResolvedProfile: resolvedProfile,
		Plugins:         plugins,
		SkipAPISpec:     !*apiSpec,
	}); err != nil {
		return err
	}
	if err := saveNewScaffoldConfig(*saveConfig, resolved, cfg); err != nil {
		return err
	}
	return output.printResultWhenRequested(*jsonOut)
}
