package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiGenCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api gen", flag.ContinueOnError)
	file := registerAPIFileFlags(fs, "api file")
	dir := fs.String("dir", ".", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	rpcPkg := fs.String("rpc-package", "", "RPC generated package import path for gateway generation")
	profile := fs.String("profile", "", "generation profile: gofly-ai, gozero-compatible, or kitex-compatible")
	profileAlias := fs.String("generation-profile", "", "alias for --profile")
	pluginArg := fs.String("plugin", "", "additional plugin executable (comma-separated) to run after generation")
	test := fs.Bool("test", false, "generate test files")
	typeGroup := fs.Bool("type-group", false, "group generated types")
	jsonOut := registerCLIJSONOutputFlag(fs, "emit generation result as JSON")
	registerGoctlTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	apiFile := file.resolve(leadingFile, remaining)
	if *profile == "" {
		*profile = *profileAlias
	}
	if err := generator.GenerateRESTFromAPI(generator.APIOptions{APIFile: apiFile, Dir: *dir, Package: *pkg, RPCPackage: *rpcPkg, Profile: *profile, Test: *test, TypeGroup: *typeGroup}); err != nil {
		return err
	}
	if err := runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "api",
		Input:   map[string]string{"api": apiFile, "package": *pkg},
		Dir:     *dir,
	}); err != nil {
		return err
	}
	if *jsonOut || outputMode() == outputJSON {
		inputs := map[string]string{"api": apiFile, "dir": *dir}
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
