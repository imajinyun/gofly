package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func pluginListCommand(args []string) error {
	fs := flag.NewFlagSet("plugin list", flag.ContinueOnError)
	outputFlags := registerCLIOutputFlags(fs, cliOutputFlagOptions{})
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		return err
	}
	internal := generator.ListInternalPlugins()
	installed, err := generator.ListInstalledPlugins()
	if err != nil {
		return err
	}
	if valueFromBoolFlag(outputFlags.JSON) || strings.EqualFold(strings.TrimSpace(valueFromStringFlag(outputFlags.Format)), outputJSON) {
		return printJSON(pluginListOutput{Internal: internal, Installed: installed})
	}
	if len(internal) == 0 && len(installed) == 0 {
		cliOutputln("(no registered internal plugins; external plugins are discovered at runtime)")
		return nil
	}
	for _, n := range internal {
		cliOutputf("internal\t%s\n", n)
	}
	for _, p := range installed {
		cliOutputf("cached\t%s@%s\t%s\tsha256:%s\n", p.Remote, p.Version, p.Binary, p.BinaryDigest)
	}
	return nil
}

func pluginSearchCommand(args []string) error {
	fs := flag.NewFlagSet("plugin search", flag.ContinueOnError)
	registry := fs.String("registry", "", "plugin registry JSON URL or path")
	query := fs.String("query", "", "search query")
	formatName := fs.String("format", "text", "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	fillNameFromArgs(query, remaining)
	if *registry == "" {
		return fmt.Errorf("%w: --registry <url-or-path> is required for `gofly plugin search`", errUsage)
	}
	index, err := generator.LoadPluginRegistryIndex(*registry)
	if err != nil {
		return err
	}
	matches := generator.FilterPluginRegistryEntries(index.Plugins, *query)
	if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
		return printJSON(pluginRegistrySearchOutput{Registry: *registry, Query: *query, Plugins: matches})
	}
	if len(matches) == 0 {
		cliOutputln("(no plugins matched)")
		return nil
	}
	for _, plugin := range matches {
		cliOutputf("%s@%s\t%s\t%s\n", plugin.Name, plugin.Version, plugin.Remote, plugin.Description)
	}
	return nil
}
