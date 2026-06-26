package command

import "github.com/imajinyun/gofly/cmd/gofly/internal/generator"

type pluginListOutput struct {
	Internal  []string                    `json:"internal"`
	Installed []generator.InstalledPlugin `json:"installed"`
}

type pluginRegistrySearchOutput struct {
	Registry string                          `json:"registry"`
	Query    string                          `json:"query,omitempty"`
	Plugins  []generator.PluginRegistryEntry `json:"plugins"`
}

type pluginRunOutput struct {
	Plugins []pluginRunResult `json:"plugins"`
}

type pluginRunResult struct {
	Plugin  string `json:"plugin"`
	Message string `json:"message,omitempty"`
	Files   int    `json:"files"`
	Patches int    `json:"patches"`
}

type pluginUninstallOutput struct {
	Remote string `json:"remote"`
	Path   string `json:"path"`
}
