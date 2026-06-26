package command

import (
	"flag"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

const defaultProtocTimeout = 2 * time.Minute

func apiGenCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api gen", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	rpcPkg := fs.String("rpc-package", "", "RPC generated package import path for gateway generation")
	profile := fs.String("profile", "", "generation profile: gofly-ai, gozero-compatible, or kitex-compatible")
	profileAlias := fs.String("generation-profile", "", "alias for --profile")
	pluginArg := fs.String("plugin", "", "additional plugin executable (comma-separated) to run after generation")
	test := fs.Bool("test", false, "generate test files")
	typeGroup := fs.Bool("type-group", false, "group generated types")
	jsonOut := fs.Bool("json", false, "emit generation result as JSON")
	registerGoctlTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *file == "" {
		*file = *api
	}
	if *file == "" {
		*file = leadingFile
	}
	if *profile == "" {
		*profile = *profileAlias
	}
	fillNameFromArgs(file, remaining)
	if err := generator.GenerateRESTFromAPI(generator.APIOptions{APIFile: *file, Dir: *dir, Package: *pkg, RPCPackage: *rpcPkg, Profile: *profile, Test: *test, TypeGroup: *typeGroup}); err != nil {
		return err
	}
	if err := runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "api",
		Input:   map[string]string{"api": *file, "package": *pkg},
		Dir:     *dir,
	}); err != nil {
		return err
	}
	if *jsonOut || outputMode() == outputJSON {
		inputs := map[string]string{"api": *file, "dir": *dir}
		if *pkg != "" {
			inputs["package"] = *pkg
		}
		if *rpcPkg != "" {
			inputs["rpcPackage"] = *rpcPkg
		}
		if *profile != "" {
			inputs["profile"] = *profile
		}
		if *test {
			inputs["test"] = "true"
		}
		if *typeGroup {
			inputs["typeGroup"] = "true"
		}
		return printJSONEnvelope("api.gen", buildIDLGeneratePlan("api gen", inputs, splitCSV(*pluginArg)))
	}
	return nil
}

func buildIDLGeneratePlan(command string, inputs map[string]string, plugins []string) cliPlan {
	dir := inputs["dir"]
	actions := []cliPlanAction{{Operation: "write-files", Target: dir, Description: "generate code under the output directory", RiskLevel: "medium"}}
	cleanPlugins := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		plugin = strings.TrimSpace(plugin)
		if plugin != "" {
			cleanPlugins = append(cleanPlugins, plugin)
		}
	}
	if len(cleanPlugins) > 0 {
		inputs["plugins"] = strings.Join(cleanPlugins, ",")
		actions = append(actions, cliPlanAction{Operation: "run-plugins", Target: strings.Join(cleanPlugins, ","), Description: "execute post-generation plugins and apply returned files or patches", RiskLevel: "high"})
	}
	return cliPlan{
		Command:           command,
		DryRun:            false,
		MutatesFilesystem: true,
		Inputs:            inputs,
		Actions:           actions,
		GeneratedFiles:    countGeneratedGoProjectFiles(dir),
		NextActions:       []string{"review generated diff", "go test ./..."},
	}
}

func registerGoctlTemplateFlags(fs *flag.FlagSet) {
	fs.String("style", "go_zero", "scaffold style option")
	fs.String("home", "", "template home directory")
	fs.String("remote", "", "remote template repository")
	fs.String("branch", "", "remote template branch")
	fs.Bool("multiple", false, "generate multiple service packages")
}
