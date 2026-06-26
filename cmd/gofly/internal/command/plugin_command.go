package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/cmd/gofly/internal/spinner"
)

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
		return pluginListCommand(rest)
	case "search":
		return pluginSearchCommand(rest)
	case "install":
		return pluginInstallCommand(rest)
	case "uninstall", "remove", "rm":
		return pluginUninstallCommand(rest)
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
