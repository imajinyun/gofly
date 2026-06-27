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
	baseFlags := registerNewScaffoldBaseFlags(fs, newScaffoldBaseFlagOptions{
		NameUsage:    "service name",
		StyleDefault: generator.ServiceStyleProduction,
		StyleUsage:   "service scaffold style: minimal, basic, or production",
		ConfigUsage:  "gofly config file path (defaults to <dir>/.gofly/config.json)",
	})
	templateFlags := registerNewScaffoldTemplateSourceFlags(fs)
	discoveryFlags := registerDiscoveryCLIFlags(fs)
	extensionFlags := registerNewScaffoldExtensionFlags(fs, "")
	apiFile := fs.String("api", "", "API-first .api contract used to generate REST handlers")
	openAPIFile := fs.String("openapi", "", "OpenAPI/Swagger contract used to generate a REST project")
	protoFile := fs.String("proto", "", "RPC-first protobuf contract used to generate RPC code")
	thriftFile := fs.String("thrift", "", "RPC-first thrift contract converted to proto and RPC code")
	executionFlags := registerNewScaffoldExecutionFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	normalizeNewScaffoldFlags(newScaffoldFlagNormalization{
		Name:          baseFlags.Name,
		Dir:           baseFlags.Dir,
		TemplateDir:   templateFlags.TemplateDir,
		TemplateHome:  templateFlags.Home,
		LeadingName:   leadingName,
		RemainingArgs: remaining,
	})
	verboseOutputf("new service: configuring service %q in %s\n", *baseFlags.Name, *baseFlags.Dir)
	loadOpts := newScaffoldLoadOptionsFromFlags("service", baseFlags, templateFlags, extensionFlags, discoveryFlags)
	loadCtx, err := loadNewScaffoldContext(loadOpts)
	if err != nil {
		return err
	}
	cfg := loadCtx.Config
	resolved := loadCtx.ConfigPath
	applyNewScaffoldStyleDefault(cfg, *baseFlags.Style, generator.ServiceStyleProduction, true)
	applyNewScaffoldDirFallback(baseFlags.Dir, cfg)
	plugins := loadCtx.PluginNames
	contractInputs := newServiceContractInputs{
		APIFile:     *apiFile,
		OpenAPIFile: *openAPIFile,
		ProtoFile:   *protoFile,
		ThriftFile:  *thriftFile,
	}
	output := newScaffoldPlanOutputFor("new.service", "new service", *baseFlags.Dir, resolved, cfg, plugins, contractInputs, *executionFlags.SaveConfig)
	if *executionFlags.DryRun || *executionFlags.Plan {
		return output.printDryRunPlan(*executionFlags.JSON, true)
	}
	if err := generateNewServiceScaffold(cfg, newServiceScaffoldOptions{
		Dir:     *baseFlags.Dir,
		Plugins: plugins,
	}); err != nil {
		return err
	}
	if err := applyNewServiceContractInputs(contractInputs, cfg.ServiceName, *baseFlags.Dir); err != nil {
		return err
	}
	if err := saveNewScaffoldConfig(*executionFlags.SaveConfig, resolved, cfg); err != nil {
		return err
	}
	return output.printResultWhenRequested(*executionFlags.JSON)
}
