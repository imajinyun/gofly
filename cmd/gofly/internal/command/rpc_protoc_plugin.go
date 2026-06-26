package command

import (
	"os"
	"path/filepath"
	"strings"
)

type goflyProtocPluginConfig struct {
	Dir              string
	Client           bool
	Multiple         bool
	Module           string
	NameFromFilename bool
}

type goflyProtocPluginOptions struct {
	Out     string
	Options []string
	Env     []string
}

func resolveGoflyProtocPlugin(pluginArg string) (string, bool, []string) {
	plugins := splitCSV(pluginArg)
	var external []string
	for _, plugin := range plugins {
		plugin = strings.TrimSpace(plugin)
		if plugin == "" {
			continue
		}
		if name, value, ok := strings.Cut(plugin, "="); ok && isGoflyProtocPluginName(name) {
			return strings.TrimSpace(value), true, external
		}
		if isGoflyProtocPluginName(plugin) {
			if strings.ContainsAny(plugin, `/\`) {
				return plugin, true, external
			}
			exe, err := os.Executable()
			if err == nil && exe != "" {
				return exe, true, external
			}
			return plugin, true, external
		}
		external = append(external, plugin)
	}
	return "", false, external
}

func buildGoflyProtocPluginOptions(enabled bool, config goflyProtocPluginConfig) goflyProtocPluginOptions {
	options := goflyProtocPluginOptions{
		Options: []string{"paths=source_relative"},
	}
	if !enabled {
		return options
	}
	options.Out = config.Dir
	options.Env = append(options.Env, "GOFLY_PLUGIN_MODE=protoc")
	if !config.Client {
		options.Options = append(options.Options, "no_client=true")
		options.Env = append(options.Env, "GOFLY_NO_CLIENT=true")
	}
	if config.Multiple {
		options.Options = append(options.Options, "multiple=true")
		options.Env = append(options.Env, "GOFLY_MULTIPLE=true")
	}
	if config.Module != "" {
		options.Options = append(options.Options, "module="+config.Module)
		options.Env = append(options.Env, "GOFLY_MODULE="+config.Module)
	}
	if config.NameFromFilename {
		options.Options = append(options.Options, "name_from_filename=true")
		options.Env = append(options.Env, "GOFLY_NAME_FROM_FILENAME=true")
	}
	return options
}

func isGoflyProtocPluginName(plugin string) bool {
	base := filepath.Base(strings.TrimSpace(plugin))
	switch strings.ToLower(base) {
	case "gofly", "protoc-gen-gofly":
		return true
	default:
		return false
	}
}
