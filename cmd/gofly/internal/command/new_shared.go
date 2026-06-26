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

func isGoctlTemplateStyle(style string) bool {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "go_zero", "gozero", "go-zero", "http-compat", "rpc-compat":
		return true
	default:
		return false
	}
}
