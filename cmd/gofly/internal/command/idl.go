package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

const defaultProtocTimeout = 2 * time.Minute

func rpcGenCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc gen", flag.ContinueOnError)
	file := fs.String("file", "", "proto file")
	src := fs.String("src", "", "proto file")
	dir := fs.String("dir", ".", "output directory")
	out := fs.String("out", "", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	profile := fs.String("profile", "", "generation profile: gofly-ai, gozero-compatible, or kitex-compatible")
	profileAlias := fs.String("generation-profile", "", "alias for --profile")
	transport := fs.String("transport", "grpc", "RPC transport to generate: grpc, gofly, or both")
	standard := fs.Bool("standard", false, "also run standard protoc Go and gRPC plugins")
	pluginArg := fs.String("plugin", "", "additional plugin executable (comma-separated) to run after generation")
	client := fs.Bool("client", true, "generate gofly RPC client code")
	c := fs.Bool("c", true, "generate gofly RPC client code")
	m := fs.Bool("m", false, "generate multiple service packages")
	withMiddleware := fs.Bool("with-middleware", false, "generate gofly RPC middleware/interceptor chain helpers")
	withRecovery := fs.Bool("with-recovery", false, "generate gofly RPC recovery middleware option helpers")
	withValidator := fs.Bool("with-validator", false, "generate gofly RPC request validator option helpers")
	timeout := fs.Duration("timeout", defaultProtocTimeout, "maximum protoc execution time when --standard is enabled")
	jsonOut := fs.Bool("json", false, "emit generation result as JSON")
	registerGoctlTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if flagProvided(fs, "c") {
		*client = *c
	}
	multiple := *m
	if f := fs.Lookup("multiple"); f != nil && parseBoolString(f.Value.String()) {
		multiple = true
	}
	if *file == "" {
		*file = *src
	}
	if *file == "" {
		*file = leadingFile
	}
	if *profile == "" {
		*profile = *profileAlias
	}
	fillNameFromArgs(file, remaining)
	if *out != "" {
		*dir = *out
	}
	if *file == "" {
		return fmt.Errorf("%w: proto file is required", errUsage)
	}
	if *timeout <= 0 {
		return fmt.Errorf("%w: --timeout must be greater than zero", errUsage)
	}
	var genErr error
	rpcOpts := generator.RPCOptions{ProtoFile: *file, Dir: *dir, Package: *pkg, Profile: *profile, NoClient: !*client, Multiple: multiple, WithMiddleware: *withMiddleware, WithRecovery: *withRecovery, WithValidator: *withValidator}
	switch *transport {
	case "", "grpc", "standard":
		genErr = generator.GenerateGRPCFromProto(generator.GRPCOptions{ProtoFile: *file, Dir: *dir, Package: *pkg})
	case "gofly":
		genErr = generator.GenerateRPCFromProto(rpcOpts)
	case "both":
		if err := generator.GenerateRPCFromProto(rpcOpts); err != nil {
			return err
		}
		genErr = generator.GenerateGRPCFromProto(generator.GRPCOptions{ProtoFile: *file, Dir: *dir, Package: *pkg})
	default:
		return fmt.Errorf("%w: unsupported rpc transport %q", errUsage, *transport)
	}
	if genErr != nil {
		return genErr
	}
	if *standard {
		if err := generator.GenerateStandardProto(context.Background(), generator.ProtocOptions{
			ProtoFile: *file,
			ProtoPath: []string{"."},
			GoOut:     *dir,
			GoGRPCOut: *dir,
			Timeout:   *timeout,
		}); err != nil {
			return err
		}
	}
	if err := runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "rpc",
		Input:   map[string]string{"proto": *file, "package": *pkg},
		Dir:     *dir,
	}); err != nil {
		return err
	}
	if *jsonOut || outputMode() == outputJSON {
		inputs := map[string]string{"proto": *file, "dir": *dir, "transport": *transport}
		if *pkg != "" {
			inputs["package"] = *pkg
		}
		if *profile != "" {
			inputs["profile"] = *profile
		}
		if *standard {
			inputs["standard"] = "true"
		}
		return printJSONEnvelope("rpc.gen", buildIDLGeneratePlan("rpc gen", inputs, splitCSV(*pluginArg)))
	}
	return nil
}

// rpcPluginCommand 实现 `gofly rpc plugin <plugin> --file <.proto> --dir <dir>`。
func rpcPluginCommand(args []string) error {
	leadingPlugin, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc plugin", flag.ContinueOnError)
	file := fs.String("file", "", "proto file")
	src := fs.String("src", "", "proto file")
	dir := fs.String("dir", ".", "output directory")
	pluginArg := fs.String("plugin", "", "plugin executable name or path")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *pluginArg == "" {
		*pluginArg = leadingPlugin
	}
	fillNameFromArgs(pluginArg, remaining)
	if *file == "" {
		*file = *src
	}
	if *file == "" {
		return fmt.Errorf("%w: --file is required for `gofly rpc plugin`", errUsage)
	}
	if *pluginArg == "" {
		return fmt.Errorf("%w: plugin is required for `gofly rpc plugin`", errUsage)
	}
	return runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "rpc",
		Input:   map[string]string{"proto": *file},
		Dir:     *dir,
	})
}

func rpcCheckCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc check", flag.ContinueOnError)
	file := fs.String("file", "", "proto file")
	src := fs.String("src", "", "proto file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *file == "" {
		*file = *src
	}
	if *file == "" {
		*file = leadingFile
	}
	fillNameFromArgs(file, remaining)
	if *file == "" {
		return fmt.Errorf("%w: proto file is required", errUsage)
	}
	content, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read proto file: %w", err)
	}
	doc, err := generator.ParseProto(string(content))
	if err != nil {
		return err
	}
	if _, err := generator.GenerateRPCCode(doc, ""); err != nil {
		return err
	}
	cliOutputf("proto ok: %d message(s), %d enum(s), %d service(s)\n", len(doc.Messages), len(doc.Enums), len(doc.Services))
	return nil
}

func rpcDocCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc doc", flag.ContinueOnError)
	file := fs.String("file", "", "proto source file")
	src := fs.String("src", "", "proto source file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output file")
	o := fs.String("o", "", "output file")
	filename := fs.String("filename", "", "output filename")
	yamlOut := fs.Bool("yaml", false, "write OpenAPI as yaml")
	jsonOut := fs.Bool("json", false, "write OpenAPI as json")
	format := fs.String("format", "openapi", "doc format: openapi/json, yaml, or markdown")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	resolveIDLFile(file, src, leadingFile, remaining)
	if *file == "" {
		return fmt.Errorf("%w: proto file is required", errUsage)
	}
	if *output == "" {
		*output = *o
	}
	if *yamlOut {
		*format = "yaml"
	}
	if *jsonOut {
		*format = "openapi"
	}
	if *output == "" && *filename != "" {
		*output = filepath.Join(*dir, *filename)
	}
	return generator.GenerateProtoDoc(generator.ProtoDocOptions{ProtoFile: *file, Dir: *dir, Output: *output, Format: *format})
}

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
