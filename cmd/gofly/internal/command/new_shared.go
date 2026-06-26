package command

import (
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
