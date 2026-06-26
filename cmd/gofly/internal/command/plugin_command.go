package command

import "fmt"

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
		return pluginRunCommand(rest)
	default:
		return fmt.Errorf("%w: expected `gofly plugin list|search|install|uninstall|run`", errUsage)
	}
}
