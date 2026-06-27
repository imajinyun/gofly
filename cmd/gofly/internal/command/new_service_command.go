package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// serviceNewCommand implements the golden-path production service scaffold.
// It intentionally routes through the same generator as `new api`/`new rpc`,
// but defaults to the full production template with REST, RPC, OpenAPI,
// governance, admin control-plane, discovery, config tests and smoke tests.
func serviceNewCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("new service", flag.ContinueOnError)
	name := fs.String("name", "", "service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	style := fs.String("style", generator.ServiceStyleProduction, "service scaffold style: minimal, basic, or production")
	configPath := fs.String("config", "", "gofly config file path (defaults to <dir>/.gofly/config.json)")
	templateFlags := registerNewScaffoldTemplateSourceFlags(fs)
	discoveryFlags := registerDiscoveryCLIFlags(fs)
	extensionFlags := registerNewScaffoldExtensionFlags(fs, "")
	apiFile := fs.String("api", "", "API-first .api contract used to generate REST handlers")
	openAPIFile := fs.String("openapi", "", "OpenAPI/Swagger contract used to generate a REST project")
	protoFile := fs.String("proto", "", "RPC-first protobuf contract used to generate RPC code")
	thriftFile := fs.String("thrift", "", "RPC-first thrift contract converted to proto and RPC code")
	saveConfig := fs.Bool("save-config", true, "save resolved config back to --config path")
	dryRun := fs.Bool("dry-run", false, "print the planned filesystem changes without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	jsonOut := fs.Bool("json", false, "emit scaffold result as JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	normalizeNewScaffoldFlags(newScaffoldFlagNormalization{
		Name:          name,
		Dir:           dir,
		TemplateDir:   templateFlags.TemplateDir,
		TemplateHome:  templateFlags.Home,
		LeadingName:   leadingName,
		RemainingArgs: remaining,
	})
	verboseOutputf("new service: configuring service %q in %s\n", *name, *dir)
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
		Kind:           "service",
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
	contractInputs := newServiceContractInputs{
		APIFile:     *apiFile,
		OpenAPIFile: *openAPIFile,
		ProtoFile:   *protoFile,
		ThriftFile:  *thriftFile,
	}
	output := newScaffoldPlanOutputFor("new.service", "new service", *dir, resolved, cfg, plugins, contractInputs, *saveConfig)
	if *dryRun || *plan {
		return output.printDryRunPlan(*jsonOut, true)
	}
	if err := generateNewServiceScaffold(cfg, newServiceScaffoldOptions{
		Dir:     *dir,
		Plugins: plugins,
	}); err != nil {
		return err
	}
	if err := applyNewServiceContractInputs(contractInputs, cfg.ServiceName, *dir); err != nil {
		return err
	}
	if err := saveNewScaffoldConfig(*saveConfig, resolved, cfg); err != nil {
		return err
	}
	return output.printResultWhenRequested(*jsonOut)
}
