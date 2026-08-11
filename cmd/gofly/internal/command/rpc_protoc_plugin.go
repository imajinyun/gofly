package command

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
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

func externalProtocPluginsForOptions(plugins []string, allow bool) []string {
	if !allow {
		return nil
	}
	out := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		plugin = strings.TrimSpace(plugin)
		if plugin != "" {
			out = append(out, plugin)
		}
	}
	return out
}

func validateExternalProtocPlugins(plugins []string) error {
	for _, plugin := range plugins {
		if err := validateExternalProtocPlugin(plugin); err != nil {
			return err
		}
	}
	return nil
}

func validateExternalProtocPlugin(plugin string) error {
	plugin = strings.TrimSpace(plugin)
	switch {
	case plugin == "":
		return fmt.Errorf("unsafe external protoc plugin: empty value")
	case strings.HasPrefix(plugin, "-"):
		return fmt.Errorf("unsafe external protoc plugin %q: plugin value must not look like a flag", plugin)
	case strings.Contains(plugin, "://"):
		return fmt.Errorf("unsafe external protoc plugin %q: URL schemes are not supported", plugin)
	case strings.ContainsAny(plugin, " \t\n\r;&|$`<>"):
		return fmt.Errorf("unsafe external protoc plugin %q: contains whitespace or shell metacharacter", plugin)
	}
	for _, r := range plugin {
		if unicode.IsControl(r) {
			return fmt.Errorf("unsafe external protoc plugin %q: contains control character", plugin)
		}
	}
	return nil
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
