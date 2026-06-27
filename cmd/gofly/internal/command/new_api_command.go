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
	normalizeNewScaffoldFlagGroups(newScaffoldNormalizeOptions{
		Base:          baseFlags,
		Template:      templateFlags,
		Profile:       &profileFlags,
		Extension:     &extensionFlags,
		Verbosity:     &verbosityFlags,
		LeadingName:   leadingName,
		RemainingArgs: remaining,
	})
	verboseOutputf("new api: configuring service %q in %s\n", *baseFlags.Name, *baseFlags.Dir)
	loadOpts := newScaffoldLoadOptionsFromFlags("api", baseFlags, templateFlags, extensionFlags, discoveryFlags)
	loadCtx, err := loadNewScaffoldContext(loadOpts)
	if err != nil {
		return err
	}
	cfg := loadCtx.Config
	applyNewScaffoldDefaults(cfg, baseFlags, generator.ServiceStyleBasic, false)
	resolvedProfile, err := resolveNewAPIProfile(cfg, *profileFlags.Profile)
	if err != nil {
		return err
	}
	output := newScaffoldPlanOutputFromContext("new.api", "new api", baseFlags, loadCtx, newServiceContractInputs{}, executionFlags)
	if handled, err := output.maybePrintDryRunPlan(executionFlags, false); handled || err != nil {
		return err
	}
	if err := generateNewAPIScaffold(cfg, newAPIScaffoldOptions{
		Dir:             *baseFlags.Dir,
		ResolvedProfile: resolvedProfile,
		Plugins:         loadCtx.PluginNames,
		SkipAPISpec:     !*apiSpec,
	}); err != nil {
		return err
	}
	return output.finalizeWithExecution(executionFlags, cfg)
}
