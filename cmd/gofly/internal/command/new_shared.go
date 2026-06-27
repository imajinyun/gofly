package command

import (
	"flag"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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

type newScaffoldLoadOptions struct {
	ConfigPath     string
	Dir            string
	Name           string
	Module         string
	Style          string
	TemplateDir    string
	TemplateRemote string
	TemplateBranch string
	Features       string
	Plugins        string
	Kind           string
	Discovery      discoveryCLIFlagValues
}

type newScaffoldLoadContext struct {
	Config      *generator.Config
	ConfigPath  string
	PluginNames []string
}

type newScaffoldTemplateSourceFlags struct {
	TemplateDir *string
	Home        *string
	Remote      *string
	Branch      *string
}

func registerNewScaffoldTemplateSourceFlags(fs *flag.FlagSet) newScaffoldTemplateSourceFlags {
	return newScaffoldTemplateSourceFlags{
		TemplateDir: fs.String("template-dir", "", "override templates from this directory"),
		Home:        fs.String("home", "", "template home directory"),
		Remote:      fs.String("remote", "", "remote template repository"),
		Branch:      fs.String("branch", "", "remote template branch"),
	}
}

type newScaffoldExtensionFlags struct {
	Features      *string
	FeaturesAlias *string
	Plugin        *string
	PluginAlias   *string
}

func registerNewScaffoldExtensionFlags(fs *flag.FlagSet, pluginAlias string) newScaffoldExtensionFlags {
	flags := newScaffoldExtensionFlags{
		Features:      fs.String("feature", "", "feature names to enable, comma-separated"),
		FeaturesAlias: fs.String("features", "", "alias for --feature"),
		Plugin:        fs.String("plugin", "", "plugin executable (comma-separated for multiple)"),
	}
	if pluginAlias != "" {
		flags.PluginAlias = fs.String(pluginAlias, "", "alias for --plugin")
	}
	return flags
}

type newScaffoldExecutionFlags struct {
	SaveConfig *bool
	DryRun     *bool
	Plan       *bool
	JSON       *bool
}

func registerNewScaffoldExecutionFlags(fs *flag.FlagSet) newScaffoldExecutionFlags {
	return newScaffoldExecutionFlags{
		SaveConfig: fs.Bool("save-config", true, "save resolved config back to --config path"),
		DryRun:     fs.Bool("dry-run", false, "print the planned filesystem changes without writing files"),
		Plan:       fs.Bool("plan", false, "alias for --dry-run"),
		JSON:       fs.Bool("json", false, "emit scaffold result as JSON"),
	}
}

type newScaffoldProfileFlags struct {
	Profile      *string
	ProfileAlias *string
}

func registerNewScaffoldProfileFlags(fs *flag.FlagSet) newScaffoldProfileFlags {
	return newScaffoldProfileFlags{
		Profile:      fs.String("profile", "", "generation profile: gofly-ai, gozero-compatible, or kitex-compatible"),
		ProfileAlias: fs.String("generation-profile", "", "alias for --profile"),
	}
}

func loadNewScaffoldContext(opts newScaffoldLoadOptions) (newScaffoldLoadContext, error) {
	cfg, resolved, err := loadAndOverlay(opts.ConfigPath, opts.Dir, opts.Name, opts.Module, opts.Style, opts.TemplateDir, opts.TemplateRemote, opts.TemplateBranch, opts.Features, opts.Plugins, opts.Kind)
	if err != nil {
		return newScaffoldLoadContext{}, err
	}
	applyDiscoveryCLIOverlay(cfg, discoveryCLIOverlayFromFlags(opts.Discovery))
	return newScaffoldLoadContext{
		Config:      cfg,
		ConfigPath:  resolved,
		PluginNames: pluginListFromConfig(cfg, opts.Kind),
	}, nil
}

func saveNewScaffoldConfig(save bool, path string, cfg *generator.Config) error {
	if !save {
		return nil
	}
	return generator.SaveConfig(path, cfg)
}

func applyNewScaffoldStyleDefault(cfg *generator.Config, requestedStyle, defaultStyle string, forceWhenRequestedEmpty bool) {
	if cfg == nil {
		return
	}
	if cfg.Style == "" || isGoctlTemplateStyle(cfg.Style) || (forceWhenRequestedEmpty && requestedStyle == "") {
		cfg.Style = defaultStyle
	}
}

func applyNewScaffoldDirFallback(dir *string, cfg *generator.Config) {
	if dir == nil || cfg == nil {
		return
	}
	if *dir == "" && cfg.ServiceName != "" {
		*dir = cfg.ServiceName
	}
}

type newScaffoldFlagNormalization struct {
	Name          *string
	Dir           *string
	TemplateDir   *string
	TemplateHome  *string
	Profile       *string
	ProfileAlias  *string
	Plugin        *string
	PluginAlias   *string
	Verbose       *bool
	VerboseAlias  *bool
	Quiet         *bool
	QuietAlias    *bool
	LeadingName   string
	RemainingArgs []string
}

type newScaffoldVerbosityFlags struct {
	Verbose      *bool
	VerboseAlias *bool
	Quiet        *bool
	QuietAlias   *bool
}

func registerNewScaffoldVerbosityFlags(fs *flag.FlagSet) newScaffoldVerbosityFlags {
	return newScaffoldVerbosityFlags{
		Verbose:      fs.Bool("verbose", false, "print verbose output"),
		VerboseAlias: fs.Bool("v", false, "print verbose output"),
		Quiet:        fs.Bool("quiet", false, "suppress normal output"),
		QuietAlias:   fs.Bool("q", false, "suppress normal output"),
	}
}

func normalizeNewScaffoldFlags(flags newScaffoldFlagNormalization) {
	if flags.hasVerbosityFlags() {
		setVerbosity(resolveVerbosity(flags.Verbose, flags.VerboseAlias, flags.Quiet, flags.QuietAlias))
	}
	if valueFromStringFlag(flags.Name) == "" {
		setStringFlag(flags.Name, flags.LeadingName)
	}
	fillNameFromArgs(flags.Name, flags.RemainingArgs)
	if valueFromStringFlag(flags.PluginAlias) != "" {
		setStringFlag(flags.Plugin, valueFromStringFlag(flags.PluginAlias))
	}
	if valueFromStringFlag(flags.TemplateDir) == "" {
		setStringFlag(flags.TemplateDir, valueFromStringFlag(flags.TemplateHome))
	}
	if valueFromStringFlag(flags.Profile) == "" {
		setStringFlag(flags.Profile, valueFromStringFlag(flags.ProfileAlias))
	}
	if valueFromStringFlag(flags.Dir) == "" && valueFromStringFlag(flags.Name) != "" {
		setStringFlag(flags.Dir, valueFromStringFlag(flags.Name))
	}
}

func (flags newScaffoldFlagNormalization) hasVerbosityFlags() bool {
	return flags.Verbose != nil || flags.VerboseAlias != nil || flags.Quiet != nil || flags.QuietAlias != nil
}

func setStringFlag(target *string, value string) {
	if target == nil {
		return
	}
	*target = value
}

func isGoctlTemplateStyle(style string) bool {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "go_zero", "gozero", "go-zero", "http-compat", "rpc-compat":
		return true
	default:
		return false
	}
}
