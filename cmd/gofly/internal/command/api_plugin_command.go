package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// apiPluginCommandRunner implements `gofly api plugin <plugin> --file <.api> --dir <dir>`.
func apiPluginCommandRunner(args []string) error {
	leadingPlugin, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api plugin", flag.ContinueOnError)
	file := registerAPIFileFlags(fs, "api file")
	dir := fs.String("dir", ".", "plugin output directory")
	pluginArg := fs.String("plugin", "", "plugin executable name or path")
	p := fs.String("p", "", "plugin executable name or path")
	style := fs.String("style", "go_zero", "plugin style option")
	legacy := fs.Bool("legacy", false, "use plain CLI args instead of gofly plugin JSON protocol")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	apiFile := file.resolve("", nil)
	if *pluginArg == "" {
		*pluginArg = *p
	}
	if *pluginArg == "" {
		*pluginArg = leadingPlugin
	}
	extraArgs := remaining
	if *pluginArg == "" && len(remaining) > 0 {
		*pluginArg = remaining[0]
		extraArgs = remaining[1:]
	}
	if apiFile == "" {
		return fmt.Errorf("%w: api file is required", errUsage)
	}
	if *pluginArg == "" {
		return fmt.Errorf("%w: api plugin is required", errUsage)
	}
	if *legacy {
		return apiPluginCommandLegacy(apiFile, *pluginArg, *dir, *style, extraArgs)
	}
	if !*legacy && looksLikeShellScript(*pluginArg) {
		return apiPluginCommandLegacy(apiFile, *pluginArg, *dir, *style, extraArgs)
	}
	return runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "api",
		Style:   *style,
		Input:   map[string]string{"api": apiFile, "style": *style},
		Dir:     *dir,
	})
}

func apiPluginCommandLegacy(file, plugin, dir, style string, extraArgs []string) error {
	parts := strings.Fields(plugin)
	bin := parts[0]
	cmdArgs := append([]string{}, parts[1:]...)
	cmdArgs = append(cmdArgs, "-api", file, "-dir", dir)
	if style != "" {
		cmdArgs = append(cmdArgs, "-style", style)
	}
	cmdArgs = append(cmdArgs, extraArgs...)
	// #nosec G204 -- legacy API plugins are an explicit compatibility feature; command and args are argv-separated without shell evaluation.
	cmd := exec.CommandContext(context.Background(), bin, cmdArgs...)
	cmd.Env = append(os.Environ(), "GOFLY_API_FILE="+file, "GOFLY_API_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run api plugin: %w: %s", err, out)
	}
	if len(out) > 0 {
		cliOutput(string(out))
	}
	return nil
}

func apiPluginCommand(args []string) error {
	return apiPluginCommandRunner(args)
}
