package command

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/gofly/gofly/cmd/gofly/internal/generator"
)

func newCommand(args []string) error {
	if printCommandHelp("new", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly new service|api|rpc`", errUsage)
	}
	switch args[0] {
	case "service":
		return serviceNewCommand(args[1:])
	case "api":
		return apiNewCommand(args[1:])
	case "rpc":
		return rpcNewCommand(args[1:])
	default:
		return fmt.Errorf("%w: expected `gofly new service|api|rpc`", errUsage)
	}
}

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
	saveConfig := fs.Bool("save-config", true, "save resolved config back to --config path")
	dryRun := fs.Bool("dry-run", false, "print the planned filesystem changes without writing files")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	jsonOut := fs.Bool("json", false, "emit scaffold result as JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *templateDir == "" {
		*templateDir = *home
	}
	if *dir == "" && *name != "" {
		*dir = *name
	}
	verboseOutputf("new service: configuring service %q in %s\n", *name, *dir)
	cfg, resolved, err := loadAndOverlay(*configPath, *dir, *name, *module, *style, *templateDir, *remote, *branch, joinCSV(*features, *featuresAlias), *pluginArg, "service")
	if err != nil {
		return err
	}
	applyDiscoveryCLIOverlay(cfg, discoveryCLIOverlay{
		Provider:    *discovery,
		Address:     *discoveryAddress,
		Endpoints:   *discoveryEndpoints,
		Prefix:      *discoveryPrefix,
		TTL:         *discoveryTTL,
		DialTimeout: *discoveryDialTimeout,
		TokenEnv:    *discoveryTokenEnv,
		UsernameEnv: *discoveryUsernameEnv,
		PasswordEnv: *discoveryPasswordEnv,
	})
	if cfg.Style == "" || isGoctlTemplateStyle(cfg.Style) {
		cfg.Style = generator.ServiceStyleProduction
	}
	if *style == "" {
		cfg.Style = generator.ServiceStyleProduction
	}
	if *dir == "" && cfg.ServiceName != "" {
		*dir = cfg.ServiceName
	}
	plugins := pluginListFromConfig(cfg, "service")
	if *dryRun || *plan {
		if err := validateNewServicePlanInputs(cfg); err != nil {
			return err
		}
		return printCLIPlan("new.service", buildNewServicePlan("new service", *dir, resolved, cfg, plugins, *saveConfig, true), *jsonOut)
	}
	if err := generator.GenerateServiceScaffold(generator.ServiceScaffoldOptions{
		Name:           cfg.ServiceName,
		Module:         cfg.Module,
		Dir:            *dir,
		Style:          cfg.Style,
		TemplateDir:    cfg.TemplateDir,
		TemplateRemote: cfg.TemplateRemote,
		TemplateBranch: cfg.TemplateBranch,
		Features:       cfg.Features,
		Plugins:        plugins,
		Kind:           "service",
	}); err != nil {
		return err
	}
	if *saveConfig {
		if err := generator.SaveConfig(resolved, cfg); err != nil {
			return err
		}
	}
	if *jsonOut || outputMode() == outputJSON {
		return printJSONEnvelope("new.service", buildNewServicePlan("new service", *dir, resolved, cfg, plugins, *saveConfig, false))
	}
	return nil
}

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
	if *dir == "" && *name != "" {
		*dir = *name
	}
	verboseOutputf("new api: configuring service %q in %s\n", *name, *dir)
	cfg, resolved, err := loadAndOverlay(*configPath, *dir, *name, *module, *style, *templateDir, *remote, *branch, joinCSV(*features, *featuresAlias), *pluginArg, "api")
	if err != nil {
		return err
	}
	applyDiscoveryCLIOverlay(cfg, discoveryCLIOverlay{
		Provider:    *discovery,
		Address:     *discoveryAddress,
		Endpoints:   *discoveryEndpoints,
		Prefix:      *discoveryPrefix,
		TTL:         *discoveryTTL,
		DialTimeout: *discoveryDialTimeout,
		TokenEnv:    *discoveryTokenEnv,
		UsernameEnv: *discoveryUsernameEnv,
		PasswordEnv: *discoveryPasswordEnv,
	})
	if cfg.Style == "" || isGoctlTemplateStyle(cfg.Style) {
		cfg.Style = generator.ServiceStyleBasic
	}
	if *dir == "" && cfg.ServiceName != "" {
		*dir = cfg.ServiceName
	}
	plugins := pluginListFromConfig(cfg, "api")
	if *dryRun || *plan {
		if err := validateNewServicePlanInputs(cfg); err != nil {
			return err
		}
		return printCLIPlan("new.api", buildNewServicePlan("new api", *dir, resolved, cfg, plugins, *saveConfig, true), *jsonOut)
	}
	if err := generator.GenerateServiceScaffold(generator.ServiceScaffoldOptions{
		Name:           cfg.ServiceName,
		Module:         cfg.Module,
		Dir:            *dir,
		Style:          cfg.Style,
		TemplateDir:    cfg.TemplateDir,
		TemplateRemote: cfg.TemplateRemote,
		TemplateBranch: cfg.TemplateBranch,
		Features:       cfg.Features,
		Plugins:        plugins,
		SkipAPISpec:    !*apiSpec,
		Kind:           "api",
	}); err != nil {
		return err
	}
	if *saveConfig {
		if err := generator.SaveConfig(resolved, cfg); err != nil {
			return err
		}
	}
	if *jsonOut || outputMode() == outputJSON {
		return printJSONEnvelope("new.api", buildNewServicePlan("new api", *dir, resolved, cfg, plugins, *saveConfig, false))
	}
	return nil
}

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
	setVerbosity(resolveVerbosity(verbose, v, quiet, q))
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *rpcPluginArg != "" {
		*pluginArg = *rpcPluginArg
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
	verboseOutputf("new rpc: configuring service %q in %s\n", *name, *dir)
	cfg, resolved, err := loadAndOverlay(*configPath, *dir, *name, *module, *style, *templateDir, *remote, *branch, joinCSV(*features, *featuresAlias), *pluginArg, "rpc")
	if err != nil {
		return err
	}
	applyDiscoveryCLIOverlay(cfg, discoveryCLIOverlay{
		Provider:    *discovery,
		Address:     *discoveryAddress,
		Endpoints:   *discoveryEndpoints,
		Prefix:      *discoveryPrefix,
		TTL:         *discoveryTTL,
		DialTimeout: *discoveryDialTimeout,
		TokenEnv:    *discoveryTokenEnv,
		UsernameEnv: *discoveryUsernameEnv,
		PasswordEnv: *discoveryPasswordEnv,
	})
	if cfg.Style == "" || isGoctlTemplateStyle(cfg.Style) {
		cfg.Style = generator.ServiceStyleProduction
	}
	// rpc new 默认生成 production 风格（包含 internal/rpc/greeter.go 等）。
	// 当 loadAndOverlay 从已有配置读取了 basic 时需要在此强制覆盖，
	// 除非用户显式通过 --style 指定了其他风格。
	if *style == "" {
		cfg.Style = generator.ServiceStyleProduction
	}
	if *dir == "" && cfg.ServiceName != "" {
		*dir = cfg.ServiceName
	}
	plugins := pluginListFromConfig(cfg, "rpc")
	resolvedProfile := strings.TrimSpace(*profile)
	if resolvedProfile == "" && cfg.RPC != nil {
		resolvedProfile = strings.TrimSpace(cfg.RPC.Profile)
	}
	if cfg.RPC == nil {
		cfg.RPC = &generator.RPCConfig{}
	}
	cfg.RPC.Profile = resolvedProfile
	if *dryRun || *plan {
		if err := validateNewServicePlanInputs(cfg); err != nil {
			return err
		}
		return printCLIPlan("new.rpc", buildNewServicePlan("new rpc", *dir, resolved, cfg, plugins, *saveConfig, true), *jsonOut)
	}
	if err := generator.GenerateServiceScaffold(generator.ServiceScaffoldOptions{
		Name:           cfg.ServiceName,
		Module:         cfg.Module,
		Dir:            *dir,
		Style:          cfg.Style,
		TemplateDir:    cfg.TemplateDir,
		TemplateRemote: cfg.TemplateRemote,
		TemplateBranch: cfg.TemplateBranch,
		Profile:        resolvedProfile,
		Features:       cfg.Features,
		Plugins:        plugins,
		Kind:           "rpc",
	}); err != nil {
		return err
	}
	if *saveConfig {
		if err := generator.SaveConfig(resolved, cfg); err != nil {
			return err
		}
	}
	if *jsonOut || outputMode() == outputJSON {
		return printJSONEnvelope("new.rpc", buildNewServicePlan("new rpc", *dir, resolved, cfg, plugins, *saveConfig, false))
	}
	return nil
}

// loadAndOverlay 负责「配置文件 + CLI 覆盖」的合并逻辑。
// 当 configPath 为空时，会按 dir/.gofly/config.json 作为默认路径。
func loadAndOverlay(configPath, dir, name, module, style, templateDir, templateRemote, templateBranch, features, plugins, kind string) (*generator.Config, string, error) {
	resolved := configPath
	if resolved == "" {
		root := dir
		if root == "" {
			root = "."
		}
		resolved = filepath.Join(root, generator.DefaultConfigFile)
	}
	cfg, err := generator.LoadConfig(resolved)
	if err != nil {
		return nil, "", err
	}
	// env layer: GOFLY_* overrides file defaults but is overridden by CLI flags.
	generator.ApplyEnvOverlay(cfg)
	featureList := splitCSV(features)
	cfg = cfg.ApplyOverlayWithTemplateSource(name, module, style, templateDir, templateRemote, templateBranch, featureList)
	switch strings.ToLower(kind) {
	case "rpc":
		if cfg.RPC == nil {
			cfg.RPC = &generator.RPCConfig{}
		}
		if pl := splitCSV(plugins); len(pl) > 0 {
			cfg.RPC.Plugins = mergeLists(cfg.RPC.Plugins, pl)
		}
	case "api":
		if cfg.API == nil {
			cfg.API = &generator.APIConfig{}
		}
		if pl := splitCSV(plugins); len(pl) > 0 {
			cfg.API.Plugins = mergeLists(cfg.API.Plugins, pl)
		}
	}
	return cfg, resolved, nil
}

// pluginListFromConfig 按 kind 从配置提取插件列表。
func pluginListFromConfig(cfg *generator.Config, kind string) []string {
	switch strings.ToLower(kind) {
	case "rpc":
		if cfg != nil && cfg.RPC != nil {
			return cfg.RPC.Plugins
		}
	case "api":
		if cfg != nil && cfg.API != nil {
			return cfg.API.Plugins
		}
	}
	return nil
}

type discoveryCLIOverlay struct {
	Provider    string
	Address     string
	Endpoints   string
	Prefix      string
	TTL         string
	DialTimeout string
	TokenEnv    string
	UsernameEnv string
	PasswordEnv string
}

func applyDiscoveryCLIOverlay(cfg *generator.Config, overlay discoveryCLIOverlay) {
	if cfg == nil || !overlay.hasValue() {
		return
	}
	if cfg.Discovery == nil {
		cfg.Discovery = &generator.DiscoveryConfig{}
	}
	if overlay.Provider != "" {
		cfg.Discovery.Provider = overlay.Provider
	}
	if overlay.Address != "" {
		cfg.Discovery.Address = overlay.Address
	}
	if overlay.Endpoints != "" {
		cfg.Discovery.Endpoints = splitCSV(overlay.Endpoints)
	}
	if overlay.Prefix != "" {
		cfg.Discovery.Prefix = overlay.Prefix
	}
	if overlay.TTL != "" {
		cfg.Discovery.TTL = overlay.TTL
	}
	if overlay.DialTimeout != "" {
		cfg.Discovery.DialTimeout = overlay.DialTimeout
	}
	if overlay.TokenEnv != "" {
		cfg.Discovery.TokenEnv = overlay.TokenEnv
	}
	if overlay.UsernameEnv != "" {
		cfg.Discovery.UsernameEnv = overlay.UsernameEnv
	}
	if overlay.PasswordEnv != "" {
		cfg.Discovery.PasswordEnv = overlay.PasswordEnv
	}
}

func (o discoveryCLIOverlay) hasValue() bool {
	return o.Provider != "" || o.Address != "" || o.Endpoints != "" || o.Prefix != "" || o.TTL != "" || o.DialTimeout != "" || o.TokenEnv != "" || o.UsernameEnv != "" || o.PasswordEnv != ""
}

func isGoctlTemplateStyle(style string) bool {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "go_zero", "gozero", "go-zero", "http-compat", "rpc-compat":
		return true
	default:
		return false
	}
}

func validateNewServicePlanInputs(cfg *generator.Config) error {
	if cfg == nil {
		return fmt.Errorf("%w: service config is required", errUsage)
	}
	if strings.TrimSpace(cfg.ServiceName) == "" {
		return fmt.Errorf("%w: name is required", errUsage)
	}
	if strings.TrimSpace(cfg.Module) == "" {
		return fmt.Errorf("%w: module is required", errUsage)
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Style)) {
	case "", generator.ServiceStyleMinimal, generator.ServiceStyleBasic, generator.ServiceStyleProduction:
		return nil
	default:
		return fmt.Errorf("%w: unknown service style %q", errUsage, cfg.Style)
	}
}

func buildNewServicePlan(command, dir, configPath string, cfg *generator.Config, plugins []string, saveConfig bool, dryRun bool) cliPlan {
	inputs := map[string]string{
		"dir": dir,
	}
	if cfg != nil {
		inputs["name"] = cfg.ServiceName
		inputs["module"] = cfg.Module
		inputs["style"] = cfg.Style
		if len(cfg.Features) > 0 {
			inputs["features"] = strings.Join(cfg.Features, ",")
		}
		if cfg.TemplateDir != "" {
			inputs["templateDir"] = cfg.TemplateDir
		}
		if cfg.TemplateRemote != "" {
			inputs["templateRemote"] = cfg.TemplateRemote
		}
		if cfg.RPC != nil && cfg.RPC.Profile != "" {
			inputs["profile"] = cfg.RPC.Profile
		}
		if cfg.Discovery != nil && cfg.Discovery.Provider != "" {
			inputs["discovery"] = cfg.Discovery.Provider
		}
	}
	if len(plugins) > 0 {
		inputs["plugins"] = strings.Join(plugins, ",")
	}

	actions := []cliPlanAction{
		{Operation: "create-directory", Target: dir, Description: "ensure the service output directory exists", RiskLevel: "low"},
		{Operation: "write-files", Target: dir, Description: "render scaffold files under the service output directory", RiskLevel: "medium"},
	}
	if saveConfig {
		actions = append(actions, cliPlanAction{Operation: "write-config", Target: configPath, Description: "save the resolved gofly config", RiskLevel: "low"})
	}
	if len(plugins) > 0 {
		actions = append(actions, cliPlanAction{Operation: "run-plugins", Target: strings.Join(plugins, ","), Description: "execute configured scaffold plugins and apply returned files or patches", RiskLevel: "high"})
	}

	warnings := []string(nil)
	if cfg != nil && cfg.TemplateRemote != "" {
		warnings = append(warnings, "dry-run does not download or validate remote templates")
	}
	if len(plugins) > 0 {
		warnings = append(warnings, "dry-run does not execute plugins; plugin-produced files and patches are not enumerated")
	}

	nextActions := []string{"cd " + dir, "go mod tidy", "go test ./..."}
	if dryRun {
		nextActions = []string{"rerun without --dry-run/--plan to apply these actions"}
	}

	return cliPlan{
		Command:           command,
		DryRun:            dryRun,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions:           actions,
		Warnings:          warnings,
		NextActions:       nextActions,
	}
}

// mergeLists 合并两组字符串，保持顺序并去重。
func mergeLists(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range b {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func fillNameFromArgs(name *string, args []string) {
	if name == nil || *name != "" || len(args) == 0 {
		return
	}
	*name = args[0]
}

func parseInterspersedFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if arg == "" || arg == "-" || arg[0] != '-' {
			positionals = append(positionals, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		flag := fs.Lookup(flagName(arg))
		if flag == nil || isBoolFlag(flag) {
			continue
		}
		if i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positionals, nil
}

func flagName(arg string) string {
	name := strings.TrimLeft(arg, "-")
	if before, _, ok := strings.Cut(name, "="); ok {
		return before
	}
	return name
}

func isBoolFlag(flag *flag.Flag) bool {
	type boolFlag interface {
		IsBoolFlag() bool
	}
	value, ok := flag.Value.(boolFlag)
	return ok && value.IsBoolFlag()
}

func splitLeadingName(args []string) (string, []string) {
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return "", args
	}
	return args[0], args[1:]
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func joinCSV(values ...string) string {
	parts := []string{}
	for _, value := range values {
		parts = append(parts, splitCSV(value)...)
	}
	return strings.Join(parts, ",")
}
