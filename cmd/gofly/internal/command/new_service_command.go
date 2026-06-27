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
	templateDir := fs.String("template-dir", "", "override templates from this directory")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	discovery := fs.String("discovery", "", "service discovery provider: memory, consul, or etcdv3")
	discoveryAddress := fs.String("discovery-address", "", "service discovery address, or comma-separated endpoints for etcdv3")
	discoveryEndpoints := fs.String("discovery-endpoints", "", "service discovery endpoints, comma-separated")
	discoveryPrefix := fs.String("discovery-prefix", "", "service discovery key prefix for etcdv3")
	discoveryTTL := fs.String("discovery-ttl", "", "service discovery registration TTL, e.g. 15s")
	discoveryDialTimeout := fs.String("discovery-dial-timeout", "", "service discovery dial timeout, e.g. 5s")
	discoveryTokenEnv := fs.String("discovery-token-env", "", "environment variable containing the Consul ACL token")
	discoveryUsernameEnv := fs.String("discovery-username-env", "", "environment variable containing the etcd username")
	discoveryPasswordEnv := fs.String("discovery-password-env", "", "environment variable containing the etcd password")
	features := fs.String("feature", "", "feature names to enable, comma-separated")
	featuresAlias := fs.String("features", "", "alias for --feature")
	pluginArg := fs.String("plugin", "", "plugin executable (comma-separated for multiple)")
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
		TemplateDir:   templateDir,
		TemplateHome:  home,
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
		TemplateDir:    *templateDir,
		TemplateRemote: *remote,
		TemplateBranch: *branch,
		Features:       joinCSV(*features, *featuresAlias),
		Plugins:        *pluginArg,
		Kind:           "service",
		Discovery: discoveryCLIFlagValues{
			Discovery:            discovery,
			DiscoveryAddress:     discoveryAddress,
			DiscoveryEndpoints:   discoveryEndpoints,
			DiscoveryPrefix:      discoveryPrefix,
			DiscoveryTTL:         discoveryTTL,
			DiscoveryDialTimeout: discoveryDialTimeout,
			DiscoveryTokenEnv:    discoveryTokenEnv,
			DiscoveryUsernameEnv: discoveryUsernameEnv,
			DiscoveryPasswordEnv: discoveryPasswordEnv,
		},
	})
	if err != nil {
		return err
	}
	cfg := loadCtx.Config
	resolved := loadCtx.ConfigPath
	applyNewScaffoldStyleDefault(cfg, *style, generator.ServiceStyleProduction, true)
	if *dir == "" && cfg.ServiceName != "" {
		*dir = cfg.ServiceName
	}
	plugins := loadCtx.PluginNames
	contractInputs := newServiceContractInputs{
		APIFile:     *apiFile,
		OpenAPIFile: *openAPIFile,
		ProtoFile:   *protoFile,
		ThriftFile:  *thriftFile,
	}
	output := newScaffoldPlanOutput{
		Command:     "new.service",
		DisplayName: "new service",
		Dir:         *dir,
		ConfigPath:  resolved,
		Config:      cfg,
		Plugins:     plugins,
		Contracts:   contractInputs,
		SaveConfig:  *saveConfig,
	}
	if *dryRun || *plan {
		if err := validateNewServicePlanInputs(cfg); err != nil {
			return err
		}
		if err := validateNewServiceContractInputs(contractInputs); err != nil {
			return err
		}
		return output.printPlan(*jsonOut)
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
	if *jsonOut || outputMode() == outputJSON {
		return output.printResult()
	}
	return nil
}
