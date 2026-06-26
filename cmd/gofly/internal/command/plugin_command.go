package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/cmd/gofly/internal/spinner"
)

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

// pluginCommand 暴露 `gofly plugin list|search|install|uninstall|run`。
func pluginCommand(args []string) error {
	if printCommandHelp("plugin", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly plugin list|search|install|uninstall|run`", errUsage)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		fs := flag.NewFlagSet("plugin list", flag.ContinueOnError)
		formatName := fs.String("format", "text", "output format: text or json")
		jsonOutput := fs.Bool("json", false, "output JSON")
		if _, err := parseInterspersedFlags(fs, rest); err != nil {
			return err
		}
		internal := generator.ListInternalPlugins()
		installed, err := generator.ListInstalledPlugins()
		if err != nil {
			return err
		}
		if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
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
	case "search":
		fs := flag.NewFlagSet("plugin search", flag.ContinueOnError)
		registry := fs.String("registry", "", "plugin registry JSON URL or path")
		query := fs.String("query", "", "search query")
		formatName := fs.String("format", "text", "output format: text or json")
		jsonOutput := fs.Bool("json", false, "output JSON")
		remaining, err := parseInterspersedFlags(fs, rest)
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
	case "install":
		fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
		remote := fs.String("remote", "", "remote plugin as <repo-or-url>@<version>")
		jsonOutput := fs.Bool("json", false, "output JSON")
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		fillNameFromArgs(remote, remaining)
		if *remote == "" {
			return fmt.Errorf("%w: --remote <repo-or-url>@<version> is required for `gofly plugin install`", errUsage)
		}
		sp := spinner.New()
		if isQuiet() || *jsonOutput || outputMode() == outputJSON {
			sp.Disable()
		}
		sp.Start("installing plugin...")
		info, err := generator.InstallRemotePlugin(*remote)
		sp.Stop()
		if err != nil {
			return err
		}
		if *jsonOutput {
			return printJSON(info)
		}
		cliOutputf("installed plugin %s@%s\nhash: %s\npath: %s\n", info.Remote, info.Version, info.Hash, info.Binary)
		if info.BinaryDigest != "" {
			cliOutputf("sha256: %s\n", info.BinaryDigest)
		}
		return nil
	case "uninstall", "remove", "rm":
		fs := flag.NewFlagSet("plugin uninstall", flag.ContinueOnError)
		remote := fs.String("remote", "", "remote plugin as <repo-or-url>@<version>")
		jsonOutput := fs.Bool("json", false, "output JSON")
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		fillNameFromArgs(remote, remaining)
		if *remote == "" {
			return fmt.Errorf("%w: --remote <repo-or-url>@<version> is required for `gofly plugin uninstall`", errUsage)
		}
		dir, err := generator.UninstallRemotePlugin(*remote)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return printJSON(pluginUninstallOutput{Remote: *remote, Path: dir})
		}
		cliOutputf("uninstalled plugin cache: %s\n", dir)
		return nil
	case "run":
		fs := flag.NewFlagSet("plugin run", flag.ContinueOnError)
		name := fs.String("name", "", "service name")
		module := fs.String("module", "", "module path")
		dir := fs.String("dir", ".", "service directory")
		command := fs.String("command", "service", "plugin command: service|handler|model")
		remote := fs.String("remote", "", "remote plugin as <repo-or-url>@<version>")
		goPlugin := fs.String("go-plugin", "", "plugin executable or directory to traverse")
		jsonOutput := fs.Bool("json", false, "output JSON")
		dryRun := fs.Bool("dry-run", false, "print the planned plugin execution without executing plugins or writing files")
		plan := fs.Bool("plan", false, "alias for --dry-run")
		plugin := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			plugin = rest[0]
			rest = rest[1:]
		}
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		if plugin == "" && len(remaining) > 0 {
			plugin = remaining[0]
		}
		previewOnly := *dryRun || *plan
		plugins := []string(nil)
		if *remote != "" {
			if previewOnly {
				plugins = append(plugins, *remote)
			} else {
				sp := spinner.New()
				if isQuiet() || *jsonOutput || outputMode() == outputJSON {
					sp.Disable()
				}
				sp.Start("resolving plugin...")
				info, err := generator.ResolveRemotePlugin(*remote)
				sp.Stop()
				if err != nil {
					return err
				}
				plugins = append(plugins, info.Binary)
			}
		}
		if *goPlugin != "" {
			if previewOnly {
				plugins = append(plugins, *goPlugin)
			} else {
				resolved, err := generator.ResolveGoPluginPaths(*goPlugin)
				if err != nil {
					return err
				}
				plugins = append(plugins, resolved...)
			}
		}
		if plugin != "" {
			plugins = append(plugins, plugin)
		}
		if len(plugins) == 0 {
			return fmt.Errorf("%w: expected `gofly plugin run <plugin-name-or-path>` or --remote/--go-plugin", errUsage)
		}
		if previewOnly {
			return printCLIPlan("plugin.run", pluginRunPlan(*command, *dir, *name, *module, *remote, *goPlugin, plugins), *jsonOutput)
		}
		runner := generator.NewPluginRunner()
		req := generator.PluginRequest{
			Command: *command,
			Service: *name,
			Module:  *module,
			Dir:     *dir,
		}
		results := make([]pluginRunResult, 0, len(plugins))
		for _, plugin := range plugins {
			resp, err := runner.Run(plugin, req)
			if err != nil {
				return err
			}
			if resp.Message != "" && !*jsonOutput {
				errorf("[gofly] plugin %s: %s\n", plugin, resp.Message)
			}
			writtenFiles, err := resp.WriteFiles(*dir)
			if err != nil {
				return err
			}
			if err := resp.ApplyPatches(*dir); err != nil {
				return err
			}
			results = append(results, pluginRunResult{Plugin: plugin, Message: resp.Message, Files: writtenFiles, Patches: len(resp.Patches)})
		}
		if *jsonOutput {
			return printJSON(pluginRunOutput{Plugins: results})
		}
		return nil
	default:
		return fmt.Errorf("%w: expected `gofly plugin list|search|install|uninstall|run`", errUsage)
	}
}

func pluginRunPlan(commandName, dir, name, module, remote, goPlugin string, plugins []string) cliPlan {
	inputs := map[string]string{
		"command": commandName,
		"dir":     dir,
		"name":    name,
		"module":  module,
	}
	if remote != "" {
		inputs["remote"] = remote
	}
	if goPlugin != "" {
		inputs["goPlugin"] = goPlugin
	}
	if len(plugins) > 0 {
		inputs["plugins"] = strings.Join(plugins, ",")
	}
	return cliPlan{
		Command:           "plugin run",
		DryRun:            true,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions: []cliPlanAction{
			{Operation: "resolve-plugins", Target: strings.Join(plugins, ","), Description: "resolve configured plugin inputs", RiskLevel: "medium"},
			{Operation: "execute-plugins", Target: strings.Join(plugins, ","), Description: "execute plugins with the requested plugin command", RiskLevel: "high"},
			{Operation: "apply-plugin-output", Target: dir, Description: "write files and apply patches returned by plugins", RiskLevel: "high"},
		},
		Warnings:    []string{"dry-run does not download remote plugins, execute plugin binaries, write files, or apply patches"},
		NextActions: []string{"rerun without --dry-run/--plan to apply these actions"},
	}
}
