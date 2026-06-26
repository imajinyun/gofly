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
	features := fs.String("feature", "", "feature names to enable, comma-separated")
	featuresAlias := fs.String("features", "", "alias for --feature")
	pluginArg := fs.String("plugin", "", "plugin executable (comma-separated for multiple)")
	apiPluginArg := fs.String("api-plugin", "", "alias for --plugin")
	saveConfig := fs.Bool("save-config", true, "save resolved config back to --config path")
	dryRun := fs.Bool("dry-run", false, "print the planned filesystem changes without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	jsonOut := fs.Bool("json", false, "emit scaffold result as JSON")
	_ = idea
	_ = client
	_ = c
	_ = nameFromFilename
	_ = goOpt
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	setVerbosity(resolveVerbosity(verbose, v, quiet, q))
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *apiPluginArg != "" {
		*pluginArg = *apiPluginArg
	}
	if *templateDir == "" {
		*templateDir = *home
	}
	if *profile == "" {
		*profile = *profileAlias
	}
	if *dir == "" && *name != "" {
		*dir = *name
	}
	verboseOutputf("new api: configuring service %q in %s\n", *name, *dir)
	cfg, resolved, err := loadAndOverlay(*configPath, *dir, *name, *module, *style, *templateDir, *remote, *branch, joinCSV(*features, *featuresAlias), *pluginArg, "api")
	if err != nil {
		return err
	}
	applyDiscoveryCLIOverlay(cfg, discoveryCLIOverlayFromFlags(discoveryCLIFlagValues{
		Discovery:            discovery,
		DiscoveryAddress:     discoveryAddress,
		DiscoveryEndpoints:   discoveryEndpoints,
		DiscoveryPrefix:      discoveryPrefix,
		DiscoveryTTL:         discoveryTTL,
		DiscoveryDialTimeout: discoveryDialTimeout,
		DiscoveryTokenEnv:    discoveryTokenEnv,
		DiscoveryUsernameEnv: discoveryUsernameEnv,
		DiscoveryPasswordEnv: discoveryPasswordEnv,
	}))
	if cfg.Style == "" || isGoctlTemplateStyle(cfg.Style) {
		cfg.Style = generator.ServiceStyleBasic
	}
	if *dir == "" && cfg.ServiceName != "" {
		*dir = cfg.ServiceName
	}
	plugins := pluginListFromConfig(cfg, "api")
	resolvedProfile, err := resolveNewAPIProfile(cfg, *profile)
	if err != nil {
		return err
	}
	output := newScaffoldPlanOutput{
		Command:     "new.api",
		DisplayName: "new api",
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
	if err := generateNewAPIScaffold(cfg, newAPIScaffoldOptions{
		Dir:             *dir,
		ResolvedProfile: resolvedProfile,
		Plugins:         plugins,
		SkipAPISpec:     !*apiSpec,
	}); err != nil {
		return err
	}
	if *saveConfig {
		if err := generator.SaveConfig(resolved, cfg); err != nil {
			return err
		}
	}
	if *jsonOut || outputMode() == outputJSON {
		return output.printResult()
	}
	return nil
}
