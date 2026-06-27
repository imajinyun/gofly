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
	idea := fs.Bool("idea", false, "open generated project in IDE")
	client := fs.Bool("client", true, "generate client code")
	c := fs.Bool("c", true, "generate client code")
	verbose := fs.Bool("verbose", false, "print verbose output")
	v := fs.Bool("v", false, "print verbose output")
	quiet := fs.Bool("quiet", false, "suppress normal output")
	q := fs.Bool("q", false, "suppress normal output")
	nameFromFilename := fs.Bool("name-from-filename", false, "derive service name from filename")
	goOpt := fs.String("go_opt", "", "extra protoc-gen-go option")
	goGRPCOpt := fs.String("go-grpc_opt", "", "extra protoc-gen-go-grpc option")
	goGRPCOptUnderscore := fs.String("go_grpc_opt", "", "extra protoc-gen-go-grpc option")
	features := fs.String("feature", "", "feature names to enable, comma-separated")
	featuresAlias := fs.String("features", "", "alias for --feature")
	pluginArg := fs.String("plugin", "", "plugin executable (comma-separated for multiple)")
	rpcPluginArg := fs.String("rpc-plugin", "", "alias for --plugin")
	saveConfig := fs.Bool("save-config", true, "save resolved config back to --config path")
	dryRun := fs.Bool("dry-run", false, "print the planned filesystem changes without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	jsonOut := fs.Bool("json", false, "emit scaffold result as JSON")
	_ = idea
	_ = client
	_ = c
	_ = nameFromFilename
	_ = goOpt
	_ = goGRPCOpt
	_ = goGRPCOptUnderscore
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	normalizeNewScaffoldFlags(newScaffoldFlagNormalization{
		Name:          name,
		Dir:           dir,
		TemplateDir:   templateDir,
		TemplateHome:  home,
		Profile:       profile,
		ProfileAlias:  profileAlias,
		Plugin:        pluginArg,
		PluginAlias:   rpcPluginArg,
		Verbose:       verbose,
		VerboseAlias:  v,
		Quiet:         quiet,
		QuietAlias:    q,
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
		TemplateDir:    *templateDir,
		TemplateRemote: *remote,
		TemplateBranch: *branch,
		Features:       joinCSV(*features, *featuresAlias),
		Plugins:        *pluginArg,
		Kind:           "rpc",
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
	applyNewScaffoldDirFallback(dir, cfg)
	plugins := loadCtx.PluginNames
	resolvedProfile := resolveNewRPCProfile(cfg, *profile)
	output := newScaffoldPlanOutput{
		Command:     "new.rpc",
		DisplayName: "new rpc",
		Dir:         *dir,
		ConfigPath:  resolved,
		Config:      cfg,
		Plugins:     plugins,
		Contracts:   newServiceContractInputs{},
		SaveConfig:  *saveConfig,
	}
	if *dryRun || *plan {
		if err := validateNewServicePlanInputs(cfg); err != nil {
			return err
		}
		return output.printPlan(*jsonOut)
	}
	if err := generateNewRPCScaffold(cfg, newRPCScaffoldOptions{
		Dir:             *dir,
		ResolvedProfile: resolvedProfile,
		Plugins:         plugins,
	}); err != nil {
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
