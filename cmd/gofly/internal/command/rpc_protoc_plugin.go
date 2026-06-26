package command

import (
	"os"
	"path/filepath"
	"strings"
)

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

func isGoflyProtocPluginName(plugin string) bool {
	base := filepath.Base(strings.TrimSpace(plugin))
	switch strings.ToLower(base) {
	case "gofly", "protoc-gen-gofly":
		return true
	default:
		return false
	}
}
