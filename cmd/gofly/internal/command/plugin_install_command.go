package command

import (
	"flag"
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/cmd/gofly/internal/spinner"
)

func pluginInstallCommand(args []string) error {
	fs := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	remote := fs.String("remote", "", "remote plugin as <repo-or-url>@<version>")
	jsonOutput := fs.Bool("json", false, "output JSON")
	remaining, err := parseInterspersedFlags(fs, args)
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
}

func pluginUninstallCommand(args []string) error {
	fs := flag.NewFlagSet("plugin uninstall", flag.ContinueOnError)
	remote := fs.String("remote", "", "remote plugin as <repo-or-url>@<version>")
	jsonOutput := fs.Bool("json", false, "output JSON")
	remaining, err := parseInterspersedFlags(fs, args)
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
}
