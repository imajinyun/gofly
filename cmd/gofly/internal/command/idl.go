package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofly/gofly/cmd/gofly/internal/generator"
	"github.com/gofly/gofly/cmd/gofly/internal/spinner"
	"github.com/gofly/gofly/rpc"
)

var runModelDatasource = generator.GenerateModelFromDatasource

const defaultProtocTimeout = 2 * time.Minute

func rpcCommand(args []string) error {
	if printCommandHelp("rpc", args) {
		return nil
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return rpcTemplateCommand(args)
	}
	return rpcCommands.dispatch(args, "gofly rpc idl|thrift|client|server|middleware|lint|deps|check|breaking|descriptor|gen|protoc|template|new")
}

func rpcIDLCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc idl", flag.ContinueOnError)
	file := fs.String("file", "", "proto or thrift idl file")
	src := fs.String("src", "", "proto or thrift idl file")
	formatName := fs.String("format", "text", "output format: text or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	resolveIDLFile(file, src, leadingFile, remaining)
	if *file == "" {
		return fmt.Errorf("%w: idl file is required", errUsage)
	}
	doc, err := generator.ReadRPCIDL(*file)
	if err != nil {
		return err
	}
	out, err := generator.FormatRPCIDLReport(doc, *formatName)
	if err != nil {
		return fmt.Errorf("%w: %v", errUsage, err)
	}
	cliOutputln(strings.TrimRight(string(out), "\n"))
	return nil
}

func rpcThriftCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc thrift", flag.ContinueOnError)
	file := fs.String("file", "", "thrift idl file")
	src := fs.String("src", "", "thrift idl file")
	dir := fs.String("dir", ".", "output directory")
	out := fs.String("out", "", "output directory")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	resolveIDLFile(file, src, leadingFile, remaining)
	if *out != "" {
		*dir = *out
	}
	if *file == "" {
		return fmt.Errorf("%w: thrift file is required", errUsage)
	}
	return generator.GenerateProtoFromThrift(generator.RPCScaffoldOptions{IDLFile: *file, Dir: *dir})
}

func rpcClientCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc client", flag.ContinueOnError)
	file := fs.String("file", "", "proto or thrift idl file")
	src := fs.String("src", "", "proto or thrift idl file")
	dir := fs.String("dir", ".", "output directory")
	out := fs.String("out", "", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	resolveIDLFile(file, src, leadingFile, remaining)
	if *out != "" {
		*dir = *out
	}
	if *file == "" {
		return fmt.Errorf("%w: idl file is required", errUsage)
	}
	return generator.GenerateRPCClient(generator.RPCScaffoldOptions{IDLFile: *file, Dir: *dir, Package: *pkg})
}

func rpcServerCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc server", flag.ContinueOnError)
	file := fs.String("file", "", "proto or thrift idl file")
	src := fs.String("src", "", "proto or thrift idl file")
	dir := fs.String("dir", ".", "output directory")
	out := fs.String("out", "", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	resolveIDLFile(file, src, leadingFile, remaining)
	if *out != "" {
		*dir = *out
	}
	if *file == "" {
		return fmt.Errorf("%w: idl file is required", errUsage)
	}
	return generator.GenerateRPCServer(generator.RPCScaffoldOptions{IDLFile: *file, Dir: *dir, Package: *pkg})
}

func rpcMiddlewareCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc middleware", flag.ContinueOnError)
	name := fs.String("name", "", "middleware name")
	dir := fs.String("dir", ".", "service root directory")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *name == "" {
		return fmt.Errorf("%w: middleware name is required", errUsage)
	}
	return generator.GenerateRPCMiddleware(generator.RPCMiddlewareOptions{Name: *name, Dir: *dir})
}

func rpcLintCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc lint", flag.ContinueOnError)
	file := fs.String("file", "", "proto or thrift idl file")
	src := fs.String("src", "", "proto or thrift idl file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	resolveIDLFile(file, src, leadingFile, remaining)
	if *file == "" {
		return fmt.Errorf("%w: idl file is required", errUsage)
	}
	doc, err := generator.ReadRPCIDL(*file)
	if err != nil {
		return err
	}
	if err := generator.LintRPCIDL(doc); err != nil {
		return err
	}
	cliOutputf("rpc idl ok: %d service(s), %d method(s)\n", len(doc.Services), generator.RPCIDLReportFor(doc).Methods)
	return nil
}

func rpcDepsCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc deps", flag.ContinueOnError)
	file := fs.String("file", "", "proto or thrift idl file")
	src := fs.String("src", "", "proto or thrift idl file")
	formatName := fs.String("format", "text", "output format: text or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	resolveIDLFile(file, src, leadingFile, remaining)
	if *file == "" {
		return fmt.Errorf("%w: idl file is required", errUsage)
	}
	doc, err := generator.ReadRPCIDL(*file)
	if err != nil {
		return err
	}
	report := generator.RPCIDLReportFor(doc)
	switch strings.ToLower(strings.TrimSpace(*formatName)) {
	case "", "text":
		for _, dep := range report.Imports {
			cliOutputln(dep)
		}
		return nil
	case "json":
		out, err := generator.FormatRPCIDLReport(doc, "json")
		if err != nil {
			return err
		}
		cliOutputln(strings.TrimRight(string(out), "\n"))
		return nil
	default:
		return fmt.Errorf("%w: unsupported rpc deps format %q", errUsage, *formatName)
	}
}

func resolveIDLFile(file *string, src *string, leading string, remaining []string) {
	if *file == "" {
		*file = *src
	}
	if *file == "" {
		*file = leading
	}
	fillNameFromArgs(file, remaining)
}

func isTemplateSubcommand(command string) bool {
	switch command {
	case "init", "list", "ls", "clean", "update", "revert":
		return true
	default:
		return false
	}
}

func rpcTemplateCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc template", flag.ContinueOnError)
	output := fs.String("output", "", "output proto template file")
	o := fs.String("o", "", "output proto template file")
	name := fs.String("name", "", "rpc service name used in the template")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	style := fs.String("style", "go_zero", "scaffold style option")
	multiple := fs.Bool("multiple", false, "generate multiple service packages")
	_ = style
	_ = multiple
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *output == "" {
		*output = *o
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateRPCTemplate(generator.IDLTemplateOptions{Output: *output, Name: *name, TemplateDir: *home, Remote: *remote, Branch: *branch})
}

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
	return runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "rpc",
		Input:   map[string]string{"proto": *file, "package": *pkg},
		Dir:     *dir,
	})
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

func rpcProtocCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc protoc", flag.ContinueOnError)
	file := fs.String("file", "", "proto file")
	src := fs.String("src", "", "proto file")
	dir := fs.String("dir", ".", "output directory")
	protoPath := fs.String("proto_path", ".", "comma-separated proto include paths")
	protoPathAlias := fs.String("proto-path", "", "comma-separated proto include paths")
	include := fs.String("I", "", "comma-separated proto include paths")
	goOut := fs.String("go_out", "", "protoc go output directory")
	goGRPCOut := fs.String("go-grpc_out", "", "protoc go-grpc output directory")
	zrpcOut := fs.String("zrpc_out", "", "service output directory")
	goOpt := fs.String("go_opt", "", "extra protoc-gen-go option")
	goGRPCOpt := fs.String("go-grpc_opt", "", "extra protoc-gen-go-grpc option")
	goGRPCOptUnderscore := fs.String("go_grpc_opt", "", "extra protoc-gen-go-grpc option")
	extra := fs.String("extra", "", "comma-separated extra protoc arguments")
	protoc := fs.String("protoc", "", "protoc binary path")
	multiple := fs.Bool("multiple", false, "generate multiple service packages")
	m := fs.Bool("m", false, "generate multiple service packages")
	client := fs.Bool("client", true, "generate client code")
	c := fs.Bool("c", true, "generate client code")
	verbose := fs.Bool("verbose", false, "print verbose output")
	v := fs.Bool("v", false, "print verbose output")
	nameFromFilename := fs.Bool("name-from-filename", false, "derive service name from filename")
	pluginArg := fs.String("plugin", "", "additional plugin executable")
	style := fs.String("style", "go_zero", "model style: go_zero/sql or gorm")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	module := fs.String("module", "", "Go module path")
	timeout := fs.Duration("timeout", defaultProtocTimeout, "maximum protoc execution time")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if flagProvided(fs, "c") {
		*client = *c
	}
	if *style != "go_zero" {
		warnNoopFlag("rpc protoc", "style", "standard protoc output is not style-aware")
	}
	if *home != "" || *remote != "" || *branch != "" {
		warnNoopFlag("rpc protoc", "home/remote/branch", "template source does not affect standard protoc output")
	}
	goflyPlugin, useGoflyPlugin, externalPlugins := resolveGoflyProtocPlugin(*pluginArg)
	if (*multiple || *m) && !useGoflyPlugin {
		warnNoopFlag("rpc protoc", "multiple", "only affects --plugin gofly output")
	}
	if (flagProvided(fs, "client") || flagProvided(fs, "c")) && !useGoflyPlugin {
		warnNoopFlag("rpc protoc", "client", "only affects --plugin gofly output")
	}
	if *nameFromFilename && !useGoflyPlugin {
		warnNoopFlag("rpc protoc", "name-from-filename", "only affects --plugin gofly output")
	}
	if *module != "" && !useGoflyPlugin {
		warnNoopFlag("rpc protoc", "module", "module import paths are controlled by go_package and protoc options unless --plugin gofly is used")
	}
	if len(externalPlugins) > 0 {
		warnNoopFlag("rpc protoc", "plugin", "external protoc plugins are not invoked by the compatibility wrapper yet")
	}
	if *file == "" {
		*file = *src
	}
	if *file == "" {
		*file = leadingFile
	}
	if *file == "" {
		return fmt.Errorf("%w: proto file is required", errUsage)
	}
	if *timeout <= 0 {
		return fmt.Errorf("%w: --timeout must be greater than zero", errUsage)
	}
	if *protoPathAlias != "" {
		*protoPath = *protoPathAlias
	}
	if *include != "" {
		*protoPath = *include
	}
	if *zrpcOut != "" {
		*dir = *zrpcOut
	}
	if *goOut == "" {
		*goOut = *dir
	}
	if *goGRPCOut == "" {
		*goGRPCOut = *dir
	}
	fillNameFromArgs(file, remaining)
	extraArgs := splitCSV(*extra)
	if *goOpt != "" {
		extraArgs = append(extraArgs, "--go_opt="+*goOpt)
	}
	if *goGRPCOpt != "" {
		extraArgs = append(extraArgs, "--go-grpc_opt="+*goGRPCOpt)
	}
	if *goGRPCOptUnderscore != "" {
		extraArgs = append(extraArgs, "--go-grpc_opt="+*goGRPCOptUnderscore)
	}
	if *verbose || *v {
		errorf("[gofly] rpc protoc: proto=%s go_out=%s go-grpc_out=%s proto_path=%s\n", *file, *goOut, *goGRPCOut, *protoPath)
	}
	goflyOptions := []string{"paths=source_relative"}
	goflyEnv := []string(nil)
	goflyOut := ""
	if useGoflyPlugin {
		goflyOut = *dir
		goflyEnv = append(goflyEnv, "GOFLY_PLUGIN_MODE=protoc")
		if !*client {
			goflyOptions = append(goflyOptions, "no_client=true")
			goflyEnv = append(goflyEnv, "GOFLY_NO_CLIENT=true")
		}
		if *multiple || *m {
			goflyOptions = append(goflyOptions, "multiple=true")
			goflyEnv = append(goflyEnv, "GOFLY_MULTIPLE=true")
		}
		if *module != "" {
			goflyOptions = append(goflyOptions, "module="+*module)
			goflyEnv = append(goflyEnv, "GOFLY_MODULE="+*module)
		}
		if *nameFromFilename {
			goflyOptions = append(goflyOptions, "name_from_filename=true")
			goflyEnv = append(goflyEnv, "GOFLY_NAME_FROM_FILENAME=true")
		}
	}

	sp := spinner.New()
	if isQuiet() || outputMode() == outputJSON {
		sp.Disable()
	}
	sp.Start("running protoc...")
	err = generator.GenerateStandardProto(context.Background(), generator.ProtocOptions{
		ProtoFile:    *file,
		ProtoPath:    splitCSV(*protoPath),
		GoOut:        *goOut,
		GoGRPCOut:    *goGRPCOut,
		GoflyOut:     goflyOut,
		GoflyPlugin:  goflyPlugin,
		GoflyOptions: goflyOptions,
		Protoc:       *protoc,
		ExtraArgs:    extraArgs,
		Env:          goflyEnv,
		Timeout:      *timeout,
	})
	sp.Stop()
	if err != nil {
		return err
	}
	return nil
}

func resolveGoflyProtocPlugin(pluginArg string) (string, bool, []string) {
	plugins := splitCSV(pluginArg)
	var external []string
	for _, plugin := range plugins {
		plugin = strings.TrimSpace(plugin)
		if plugin == "" {
			continue
		}
		if name, value, ok := strings.Cut(plugin, "="); ok && isGoflyProtocPluginName(name) {
			return strings.TrimSpace(value), true, external
		}
		if isGoflyProtocPluginName(plugin) {
			if strings.ContainsAny(plugin, `/\`) {
				return plugin, true, external
			}
			exe, err := os.Executable()
			if err == nil && exe != "" {
				return exe, true, external
			}
			return plugin, true, external
		}
		external = append(external, plugin)
	}
	return "", false, external
}

func isGoflyProtocPluginName(plugin string) bool {
	base := filepath.Base(strings.TrimSpace(plugin))
	switch strings.ToLower(base) {
	case "gofly", "protoc-gen-gofly":
		return true
	default:
		return false
	}
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

func apiCommand(args []string) error {
	if printCommandHelp("api", args) {
		return nil
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return apiTemplateCommand(args)
	}
	return apiCommands.dispatch(args, "gofly api check|breaking|gen|go|types|route|import|diff|plugin|middleware|format|doc|client|new")
}

func apiTemplateCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api template", flag.ContinueOnError)
	output := fs.String("output", "", "output api template file")
	o := fs.String("o", "", "output api template file")
	name := fs.String("name", "", "api service name used in the template")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	style := fs.String("style", "go_zero", "scaffold style option")
	multiple := fs.Bool("multiple", false, "generate multiple service packages")
	_ = style
	_ = multiple
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *output == "" {
		*output = *o
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateAPITemplate(generator.IDLTemplateOptions{Output: *output, Name: *name, TemplateDir: *home, Remote: *remote, Branch: *branch})
}

func apiCheckCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api check", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
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
	fillNameFromArgs(file, remaining)
	if *file == "" {
		return fmt.Errorf("%w: api file is required", errUsage)
	}
	content, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read api file: %w", err)
	}
	doc, err := generator.ParseAPI(string(content))
	if err != nil {
		return err
	}
	if err := generator.ValidateAPI(doc); err != nil {
		return err
	}
	cliOutputf("api ok: %d type(s), %d service(s)\n", len(doc.Messages), len(doc.Services))
	return nil
}

func apiFormatCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api format", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", "", "directory containing .api files")
	output := fs.String("output", "", "formatted output file")
	o := fs.String("o", "", "formatted output file")
	write := fs.Bool("write", true, "write result to source file")
	w := fs.Bool("w", true, "write result to source file")
	iu := fs.Bool("iu", false, "preserve import/use layout")
	stdin := fs.Bool("stdin", false, "read api content from stdin")
	declare := fs.Bool("declare", false, "format declarations only")
	_ = iu
	_ = declare
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if flagWasSet(fs, "w") {
		*write = *w
	}
	if *stdin {
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read api from stdin: %w", err)
		}
		doc, err := generator.ParseAPI(string(content))
		if err != nil {
			return err
		}
		formatted := generator.FormatAPI(doc)
		if *output == "" {
			*output = *o
		}
		if *output != "" {
			// #nosec G301 -- CLI formatting writes user-visible project artifacts that should remain traversable by tools.
			if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
				return fmt.Errorf("create api format output directory: %w", err)
			}
			// #nosec G306 -- formatted API files are generated project artifacts intentionally readable by collaborators and tooling.
			return os.WriteFile(*output, formatted, 0o644)
		}
		cliOutput(string(formatted))
		return nil
	}
	if *file == "" {
		*file = *api
	}
	if *file == "" {
		*file = leadingFile
	}
	if *output == "" {
		*output = *o
	}
	fillNameFromArgs(file, remaining)
	formatted, err := generator.FormatAPIFromFile(generator.APIFormatOptions{
		APIFile: *file,
		Dir:     *dir,
		Output:  *output,
		Write:   *write,
	})
	if err != nil {
		return err
	}
	if !*write && *output == "" && *dir == "" {
		cliOutput(string(formatted))
	}
	return nil
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			found = true
		}
	})
	return found
}

func apiDocCommand(command string, args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api doc", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output file")
	o := fs.String("o", "", "output file")
	filename := fs.String("filename", "", "swagger filename")
	yamlOut := fs.Bool("yaml", false, "write swagger as yaml")
	jsonOut := fs.Bool("json", false, "write swagger as json")
	oas3 := fs.Bool("oas3", false, "write OpenAPI v3 output")
	defaultFormat := "markdown"
	if command == "swagger" {
		defaultFormat = "openapi"
	}
	format := fs.String("format", defaultFormat, "doc format: markdown, openapi/json, or yaml")
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
	if *output == "" {
		*output = *o
	}
	if *yamlOut {
		*format = "yaml"
	}
	if *jsonOut {
		*format = "json"
	}
	if *oas3 && *format == "markdown" {
		*format = "openapi"
	}
	if *output == "" && *filename != "" {
		*output = filepath.Join(*dir, *filename)
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPIDoc(generator.APIDocOptions{APIFile: *file, Dir: *dir, Output: *output, Format: *format})
}

func apiClientCommand(command string, args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api client", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output file")
	o := fs.String("o", "", "output file")
	language := fs.String("language", "typescript", "client language: typescript, javascript, dart, java, or kotlin")
	baseURL := fs.String("base-url", "", "default API base URL")
	caller := fs.String("caller", "", "client caller name")
	unwrap := fs.Bool("unwrap", false, "unwrap response envelopes")
	legacy := fs.Bool("legacy", false, "use legacy client output layout")
	hostname := fs.String("hostname", "", "api hostname")
	scheme := fs.String("scheme", "", "api scheme")
	pkg := fs.String("pkg", "", "generated package name")
	if command == "ts" || command == "typescript" {
		*language = "typescript"
	}
	if command == "js" || command == "javascript" {
		*language = "javascript"
	}
	if command == "dart" {
		*language = "dart"
	}
	if command == "java" {
		*language = "java"
	}
	if command == "kotlin" || command == "kt" {
		*language = "kotlin"
	}
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *caller != "" {
		warnNoopFlag("api client", "caller", "client naming is derived from the service name")
	}
	if *unwrap {
		warnNoopFlag("api client", "unwrap", "generated clients currently preserve response shapes from the API spec")
	}
	if *legacy {
		warnNoopFlag("api client", "legacy", "gofly emits the current client layout")
	}
	if *pkg != "" {
		warnNoopFlag("api client", "pkg", "non-Go clients do not use package names; Go DTOs use api types")
	}
	if *file == "" {
		*file = *api
	}
	if *file == "" {
		*file = leadingFile
	}
	if *output == "" {
		*output = *o
	}
	if *baseURL == "" && *hostname != "" {
		if *scheme == "" {
			*scheme = "http"
		}
		*baseURL = *scheme + "://" + *hostname
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPIClient(generator.APIClientOptions{
		APIFile:  *file,
		Dir:      *dir,
		Output:   *output,
		Language: *language,
		BaseURL:  *baseURL,
	})
}

func apiTypesCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api types", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output file")
	o := fs.String("o", "", "output file")
	pkg := fs.String("package", "types", "generated Go package name")
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
	if *output == "" {
		*output = *o
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPITypes(generator.APITypesOptions{
		APIFile: *file,
		Dir:     *dir,
		Output:  *output,
		Package: *pkg,
	})
}

// apiPluginCommandRunner 实现 `gofly api plugin <plugin> --file <.api> --dir <dir>`，
// 使用 gofly 的 PluginRunner 协议（JSON stdin/stdout）。保留旧实现 `apiPluginCommand`
// 直接 exec 外部插件并传入 CLI 参数；当用户显式传入 `--legacy` 时启用。
func apiPluginCommandRunner(args []string) error {
	leadingPlugin, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api plugin", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "plugin output directory")
	pluginArg := fs.String("plugin", "", "plugin executable name or path")
	p := fs.String("p", "", "plugin executable name or path")
	style := fs.String("style", "go_zero", "plugin style option")
	legacy := fs.Bool("legacy", false, "use plain CLI args instead of gofly plugin JSON protocol")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *file == "" {
		*file = *api
	}
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
	if *file == "" {
		return fmt.Errorf("%w: api file is required", errUsage)
	}
	if *pluginArg == "" {
		return fmt.Errorf("%w: api plugin is required", errUsage)
	}
	if *legacy {
		return apiPluginCommandLegacy(*file, *pluginArg, *dir, *style, extraArgs)
	}
	// 自动检测：如果插件是文件系统上的 shell 脚本（.sh 或以 #! 开头），
	// 默认走 CLI 参数模式，方便执行普通 shell 插件。
	if !*legacy && looksLikeShellScript(*pluginArg) {
		return apiPluginCommandLegacy(*file, *pluginArg, *dir, *style, extraArgs)
	}
	return runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "api",
		Style:   *style,
		Input:   map[string]string{"api": *file, "style": *style},
		Dir:     *dir,
	})
}

// apiPluginCommandLegacy 直接 exec 外部程序，
// 以 `-api`、`-dir` 等 CLI 参数传递信息。保留以兼容旧插件。
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

// apiPluginCommand 保留为插件入口别名（默认走新协议）。
func apiPluginCommand(args []string) error {
	return apiPluginCommandRunner(args)
}

func apiMiddlewareCommand(args []string) error {
	leadingNames, args := splitLeadingNames(args)
	fs := flag.NewFlagSet("api middleware", flag.ContinueOnError)
	name := fs.String("name", "", "middleware name, comma-separated for multiple middlewares")
	api := fs.String("api", "", "api file to discover middleware declarations")
	file := fs.String("file", "", "api file, alias for --api")
	dir := fs.String("dir", ".", "service root directory")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = strings.Join(leadingNames, ",")
	}
	fillNameFromArgs(name, remaining)
	if *api == "" {
		*api = *file
	}
	names := splitCSV(*name)
	switch {
	case len(leadingNames) > 0:
		names = append(names, remaining...)
	case *name != "" && len(remaining) > 1:
		names = append(names, remaining[1:]...)
	case *name == "":
		names = append(names, remaining...)
	}
	if *api != "" {
		apiNames, err := apiMiddlewareNames(*api)
		if err != nil {
			return err
		}
		names = append(names, apiNames...)
	}
	return generator.GenerateMiddleware(generator.MiddlewareOptions{Names: names, Dir: *dir})
}

func apiMiddlewareNames(path string) ([]string, error) {
	// #nosec G304 -- middleware discovery reads an explicit API file path supplied to the CLI.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read api file: %w", err)
	}
	if _, err := generator.ParseAPI(string(content)); err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		names = append(names, middlewareNamesFromLine(line)...)
	}
	return names, nil
}

func splitLeadingNames(args []string) ([]string, []string) {
	names := make([]string, 0)
	for len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		names = append(names, args[0])
		args = args[1:]
	}
	return names, args
}

func middlewareNamesFromLine(line string) []string {
	lower := strings.ToLower(line)
	for _, marker := range []string{"middleware:", "middlewares:"} {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		value := line[idx+len(marker):]
		value = strings.Trim(value, " `\"[]{}()")
		return splitCSV(value)
	}
	return nil
}

func apiGenCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api gen", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	rpcPkg := fs.String("rpc-package", "", "RPC generated package import path for gateway generation")
	pluginArg := fs.String("plugin", "", "additional plugin executable (comma-separated) to run after generation")
	test := fs.Bool("test", false, "generate test files")
	typeGroup := fs.Bool("type-group", false, "group generated types")
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
	fillNameFromArgs(file, remaining)
	if err := generator.GenerateRESTFromAPI(generator.APIOptions{APIFile: *file, Dir: *dir, Package: *pkg, RPCPackage: *rpcPkg, Test: *test, TypeGroup: *typeGroup}); err != nil {
		return err
	}
	return runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "api",
		Input:   map[string]string{"api": *file, "package": *pkg},
		Dir:     *dir,
	})
}

func registerGoctlTemplateFlags(fs *flag.FlagSet) {
	fs.String("style", "go_zero", "scaffold style option")
	fs.String("home", "", "template home directory")
	fs.String("remote", "", "remote template repository")
	fs.String("branch", "", "remote template branch")
	fs.Bool("multiple", false, "generate multiple service packages")
}

func apiRouteCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api route", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output routes file")
	o := fs.String("o", "", "output routes file")
	format := fs.String("format", "text", "route format: text, markdown, or json")
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
	if *output == "" {
		*output = *o
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPIRoutes(generator.APIRouteOptions{APIFile: *file, Dir: *dir, Output: *output, Format: *format})
}

func apiImportCommand(args []string) error {
	leadingSource, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api import", flag.ContinueOnError)
	src := fs.String("src", "", "OpenAPI/Swagger JSON or YAML file")
	from := fs.String("from", "", "OpenAPI/Swagger JSON or YAML file")
	swagger := fs.String("swagger", "", "Swagger JSON or YAML file, alias for --src")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output .api file")
	o := fs.String("o", "", "output .api file")
	service := fs.String("service", "", "service name for generated .api")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *src == "" {
		*src = *from
	}
	if *src == "" {
		*src = *swagger
	}
	if *src == "" {
		*src = leadingSource
	}
	if *output == "" {
		*output = *o
	}
	fillNameFromArgs(src, remaining)
	return generator.GenerateAPIFromOpenAPI(generator.APIImportOptions{Source: *src, Dir: *dir, Output: *output, Service: *service})
}

func apiDiffCommand(args []string) error {
	leadingFiles, args := splitLeadingNames(args)
	fs := flag.NewFlagSet("api diff", flag.ContinueOnError)
	base := fs.String("base", "", "base api file")
	old := fs.String("old", "", "base api file, alias for --base")
	target := fs.String("target", "", "target api file")
	newFile := fs.String("new", "", "target api file, alias for --target")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output diff file")
	o := fs.String("o", "", "output diff file")
	format := fs.String("format", "text", "diff format: text, markdown, or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" {
		*base = *old
	}
	if *target == "" {
		*target = *newFile
	}
	if *base == "" && len(leadingFiles) > 0 {
		*base = leadingFiles[0]
	}
	if *target == "" && len(leadingFiles) > 1 {
		*target = leadingFiles[1]
	}
	if *output == "" {
		*output = *o
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	return generator.GenerateAPIDiff(generator.APIDiffOptions{Base: *base, Target: *target, Dir: *dir, Output: *output, Format: *format})
}

func modelCommand(args []string) error {
	if printCommandHelp("model", args) {
		return nil
	}
	return modelCommands.dispatch(args, "gofly model gen|mysql|pg|mongo")
}

func modelGenCommand(args []string) error {
	leadingDDL, args := splitLeadingName(args)
	fs := flag.NewFlagSet("model gen", flag.ContinueOnError)
	ddl := fs.String("ddl", "", "SQL DDL file")
	src := fs.String("src", "", "SQL DDL file")
	s := fs.String("s", "", "SQL DDL file")
	dir := fs.String("dir", ".", "output directory")
	d := fs.String("d", "", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	module := fs.String("module", "", "go module path, inferred from go.mod when empty")
	table := fs.String("table", "", "comma-separated table names to generate")
	tables := fs.String("tables", "", "comma-separated table names to generate, alias for --table")
	t := fs.String("t", "", "comma-separated table names to generate")
	database := fs.String("database", "", "database name")
	strict := fs.Bool("strict", false, "enable strict generation checks")
	ignoreColumns := fs.String("ignore-columns", "", "columns to ignore during generation")
	i := fs.String("i", "", "columns to ignore during generation")
	prefix := fs.String("prefix", "", "table prefix to trim")
	p := fs.String("p", "", "table prefix to trim")
	style := fs.String("style", "go_zero", "model style")
	cache := fs.Bool("cache", false, "generate cache helpers")
	c := fs.Bool("c", false, "generate cache helpers")
	configPath := fs.String("config", "", "gofly config file path")
	registerGoctlModelTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *ddl == "" {
		*ddl = *src
	}
	if *ddl == "" {
		*ddl = *s
	}
	if *d != "" {
		*dir = *d
	}
	if *ddl == "" {
		*ddl = leadingDDL
	}
	if *ddl == "" && len(remaining) > 0 {
		*ddl = remaining[0]
		remaining = remaining[1:]
	}
	if *d == "" && *dir == "." && len(remaining) > 0 {
		*dir = remaining[0]
	}
	if *table == "" {
		*table = *tables
	}
	if *table == "" {
		*table = *t
	}
	if *ignoreColumns == "" {
		*ignoreColumns = *i
	}
	if *prefix == "" {
		*prefix = *p
	}
	if *c {
		*cache = true
	}
	typesMap, err := modelTypesMapFromConfig(*configPath, *dir)
	if err != nil {
		return err
	}
	fillNameFromArgs(ddl, remaining)
	if err := generator.GenerateModelFromDDL(generator.ModelOptions{
		DDLFile:       *ddl,
		Dir:           *dir,
		Package:       *pkg,
		Module:        *module,
		Tables:        splitCSV(*table),
		Style:         *style,
		Database:      *database,
		IgnoreColumns: splitCSV(*ignoreColumns),
		Prefix:        *prefix,
		Strict:        *strict,
		Cache:         *cache,
		TypesMap:      typesMap,
	}); err != nil {
		return err
	}
	printModelGenerated(*dir)
	return nil
}

func printModelGenerated(dir string) {
	modelDir := filepath.Join(dir, "model")
	displayDir := modelDir
	if absDir, err := filepath.Abs(modelDir); err == nil {
		displayDir = absDir
	}
	cliOutputf("model generated: %s\n", displayDir)
	files := generatedModelFiles(modelDir)
	if len(files) == 0 {
		return
	}
	cliOutputln("model files:")
	for _, file := range files {
		displayFile := file
		if absFile, err := filepath.Abs(file); err == nil {
			displayFile = absFile
		}
		cliOutputf("  %s\n", displayFile)
	}
}

func generatedModelFiles(modelDir string) []string {
	patterns := []string{
		filepath.Join(modelDir, "entity", "*.go"),
		filepath.Join(modelDir, "repo", "*.go"),
	}
	files := make([]string, 0)
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	return files
}

func modelMySQLCommand(args []string) error {
	if len(args) == 0 || args[0] == "ddl" {
		if len(args) > 0 {
			args = args[1:]
		}
		return modelGenCommand(args)
	}
	if args[0] == "datasource" {
		return modelMySQLDatasourceCommand(args[1:])
	}
	return fmt.Errorf("%w: expected `gofly model mysql ddl` or `gofly model mysql datasource`", errUsage)
}

func modelMySQLDatasourceCommand(args []string) error {
	leadingURL, args := splitLeadingName(args)
	fs := flag.NewFlagSet("model mysql datasource", flag.ContinueOnError)
	url := fs.String("url", "", "database datasource url")
	dsn := fs.String("dsn", "", "database datasource url, alias for --url")
	datasource := fs.String("datasource", "", "database datasource url")
	table := fs.String("table", "", "table name filter")
	tables := fs.String("tables", "", "table name filter, alias for --table")
	t := fs.String("t", "", "table name filter")
	dir := fs.String("dir", ".", "output directory")
	d := fs.String("d", "", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	module := fs.String("module", "", "go module path, inferred from go.mod when empty")
	database := fs.String("database", "", "database name")
	schema := fs.String("schema", "", "schema name")
	strict := fs.Bool("strict", false, "enable strict generation checks")
	ignoreColumns := fs.String("ignore-columns", "", "columns to ignore during generation")
	i := fs.String("i", "", "columns to ignore during generation")
	prefix := fs.String("prefix", "", "table prefix to trim")
	p := fs.String("p", "", "table prefix to trim")
	style := fs.String("style", "go_zero", "model style: go_zero/sql or gorm")
	cache := fs.Bool("cache", false, "generate cache helpers")
	c := fs.Bool("c", false, "generate cache helpers")
	configPath := fs.String("config", "", "gofly config file path")
	registerGoctlModelTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *d != "" {
		*dir = *d
	}
	if *url == "" {
		*url = *dsn
	}
	if *url == "" {
		*url = *datasource
	}
	if *url == "" {
		*url = leadingURL
	}
	if *table == "" {
		*table = *tables
	}
	if *table == "" {
		*table = *t
	}
	if *ignoreColumns == "" {
		*ignoreColumns = *i
	}
	if *prefix == "" {
		*prefix = *p
	}
	if *c {
		*cache = true
	}
	typesMap, err := modelTypesMapFromConfig(*configPath, *dir)
	if err != nil {
		return err
	}
	fillNameFromArgs(url, remaining)
	if *url == "" {
		return fmt.Errorf("%w: datasource url is required", errUsage)
	}
	return runModelDatasource(generator.ModelDatasourceOptions{
		Driver:        "mysql",
		DSN:           *url,
		Dir:           *dir,
		Package:       *pkg,
		Module:        *module,
		Tables:        splitCSV(*table),
		Style:         *style,
		Database:      *database,
		Schema:        *schema,
		IgnoreColumns: splitCSV(*ignoreColumns),
		Prefix:        *prefix,
		Strict:        *strict,
		Cache:         *cache,
		TypesMap:      typesMap,
	})
}

func modelPostgresCommand(args []string) error {
	if len(args) == 0 || args[0] == "ddl" {
		if len(args) > 0 {
			args = args[1:]
		}
		return modelGenCommand(args)
	}
	if args[0] == "datasource" {
		return modelPostgresDatasourceCommand(args[1:])
	}
	return fmt.Errorf("%w: expected `gofly model pg ddl` or `gofly model pg datasource`", errUsage)
}

func modelPostgresDatasourceCommand(args []string) error {
	leadingURL, args := splitLeadingName(args)
	fs := flag.NewFlagSet("model pg datasource", flag.ContinueOnError)
	url := fs.String("url", "", "database datasource url")
	dsn := fs.String("dsn", "", "database datasource url, alias for --url")
	datasource := fs.String("datasource", "", "database datasource url")
	table := fs.String("table", "", "table name filter")
	tables := fs.String("tables", "", "table name filter, alias for --table")
	t := fs.String("t", "", "table name filter")
	dir := fs.String("dir", ".", "output directory")
	d := fs.String("d", "", "output directory")
	pkg := fs.String("package", "", "generated Go package name")
	module := fs.String("module", "", "go module path, inferred from go.mod when empty")
	database := fs.String("database", "", "database name")
	schema := fs.String("schema", "", "schema name")
	s := fs.String("s", "", "schema name")
	strict := fs.Bool("strict", false, "enable strict generation checks")
	ignoreColumns := fs.String("ignore-columns", "", "columns to ignore during generation")
	i := fs.String("i", "", "columns to ignore during generation")
	prefix := fs.String("prefix", "", "table prefix to trim")
	p := fs.String("p", "", "table prefix to trim")
	style := fs.String("style", "go_zero", "model style: go_zero/sql or gorm")
	cache := fs.Bool("cache", false, "generate cache helpers")
	c := fs.Bool("c", false, "generate cache helpers")
	configPath := fs.String("config", "", "gofly config file path")
	registerGoctlModelTemplateFlags(fs)
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *d != "" {
		*dir = *d
	}
	if *url == "" {
		*url = *dsn
	}
	if *url == "" {
		*url = *datasource
	}
	if *url == "" {
		*url = leadingURL
	}
	if *table == "" {
		*table = *tables
	}
	if *table == "" {
		*table = *t
	}
	if *schema == "" {
		*schema = *s
	}
	if *ignoreColumns == "" {
		*ignoreColumns = *i
	}
	if *prefix == "" {
		*prefix = *p
	}
	if *c {
		*cache = true
	}
	typesMap, err := modelTypesMapFromConfig(*configPath, *dir)
	if err != nil {
		return err
	}
	fillNameFromArgs(url, remaining)
	if *url == "" {
		return fmt.Errorf("%w: datasource url is required", errUsage)
	}
	return runModelDatasource(generator.ModelDatasourceOptions{
		Driver:        "postgres",
		DSN:           *url,
		Dir:           *dir,
		Package:       *pkg,
		Module:        *module,
		Tables:        splitCSV(*table),
		Style:         *style,
		Database:      *database,
		Schema:        *schema,
		IgnoreColumns: splitCSV(*ignoreColumns),
		Prefix:        *prefix,
		Strict:        *strict,
		Cache:         *cache,
		TypesMap:      typesMap,
	})
}

func modelTypesMapFromConfig(configPath, dir string) (map[string]string, error) {
	path := strings.TrimSpace(configPath)
	explicitPath := path != ""
	if path == "" {
		path = filepath.Join(dir, ".gofly", "config.json")
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicitPath {
			return nil, nil
		}
		return nil, err
	}
	cfg, err := generator.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	if cfg.Model == nil || len(cfg.Model.TypesMap) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(cfg.Model.TypesMap))
	for key, value := range cfg.Model.TypesMap {
		out[key] = value
	}
	return out, nil
}

func registerGoctlModelTemplateFlags(fs *flag.FlagSet) {
	fs.String("home", "", "template home directory")
	fs.String("remote", "", "remote template repository")
	fs.String("branch", "", "remote template branch")
	fs.Bool("idea", false, "open generated project in IDE")
}

func modelMongoCommand(args []string) error {
	fs := flag.NewFlagSet("model mongo", flag.ContinueOnError)
	typeName := fs.String("type", "", "mongo model type name")
	t := fs.String("t", "", "mongo model type name")
	dir := fs.String("dir", ".", "output directory")
	d := fs.String("d", "", "output directory")
	pkg := fs.String("package", "model", "generated Go package name")
	cache := fs.Bool("cache", false, "generate cache helpers")
	c := fs.Bool("c", false, "generate cache helpers")
	prefix := fs.String("prefix", "", "model prefix to trim")
	p := fs.String("p", "", "model prefix to trim")
	easy := fs.Bool("easy", false, "use simplified mongo output")
	e := fs.Bool("e", false, "use simplified mongo output")
	style := fs.String("style", "go_zero", "model style")
	registerGoctlModelTemplateFlags(fs)
	_ = easy
	_ = e
	_ = style
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *typeName == "" {
		*typeName = *t
	}
	if *d != "" {
		*dir = *d
	}
	if *prefix == "" {
		*prefix = *p
	}
	if *c {
		*cache = true
	}
	fillNameFromArgs(typeName, remaining)
	return generator.GenerateMongoModel(generator.MongoModelOptions{Type: *typeName, Dir: *dir, Package: *pkg, Prefix: *prefix, Cache: *cache, Style: *style})
}

func gatewayGenCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("gateway gen", flag.ContinueOnError)
	name := fs.String("name", "", "gateway service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *name == "" {
		*name = "gateway"
	}
	return generator.GenerateGateway(generator.GatewayOptions{Name: *name, Module: *module, Dir: *dir})
}

// runPostPlugins 是统一的「运行生成后插件」函数：允许用户以逗号分隔多个插件。
func runPostPlugins(pluginArg string, req generator.PluginRequest) error {
	pluginArg = strings.TrimSpace(pluginArg)
	if pluginArg == "" {
		return nil
	}
	req = enrichPluginRequestIDL(req)
	runner := generator.NewPluginRunner()
	for _, p := range splitCSV(pluginArg) {
		if strings.TrimSpace(p) == "" {
			continue
		}
		resp, err := runner.Run(p, req)
		if err != nil {
			return fmt.Errorf("run plugin %s: %w", p, err)
		}
		if resp.Message != "" {
			errorf("[gofly] plugin %s: %s\n", p, resp.Message)
		}
		if _, err := resp.WriteFiles(req.Dir); err != nil {
			return fmt.Errorf("write plugin %s files: %w", p, err)
		}
		if err := resp.ApplyPatches(req.Dir); err != nil {
			return fmt.Errorf("apply plugin %s patches: %w", p, err)
		}
	}
	return nil
}

func enrichPluginRequestIDL(req generator.PluginRequest) generator.PluginRequest {
	if len(req.IDL) > 0 || req.Input == nil {
		return req
	}
	for key, format := range map[string]string{"api": "api", "proto": "proto", "thrift": "thrift", "openapi": "openapi"} {
		path := strings.TrimSpace(req.Input[key])
		if path == "" {
			continue
		}
		// #nosec G304 -- post-plugin IDL enrichment reads explicit generator input files already supplied by the CLI.
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		req.IDL = data
		req.IDLFormat = format
		return req
	}
	return req
}

// handlerCompleteCommand 实现 `gofly handler complete --file <handler.go> --method <name> [--body <Go stmt>] [--comment "..."]`。
// 使用 generator.HandlerCompleter 做增量合并：跳过已存在的方法，把新增方法追加到文件末尾。
func handlerCompleteCommand(args []string) error {
	fs := flag.NewFlagSet("handler complete", flag.ContinueOnError)
	file := fs.String("file", "", "handler Go source file path")
	src := fs.String("src", "", "api/proto/thrift IDL file used to infer missing handlers")
	idl := fs.String("idl", "", "api/proto/thrift IDL file used to infer missing handlers")
	name := fs.String("method", "", "handler / method name")
	receiver := fs.String("receiver", "", "Go receiver name (optional, inferred from filename)")
	pkg := fs.String("package", "", "package name when creating a new file")
	body := fs.String("body", "", "method body Go statements; if empty renders a TODO placeholder")
	comment := fs.String("comment", "", "optional comment attached to the method")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("%w: --file is required for `gofly handler complete`", errUsage)
	}
	if *src == "" {
		*src = *idl
	}
	if *src != "" {
		n, err := generator.CompleteHandlersFromIDL(generator.HandlerCompleteOptions{
			File:     *file,
			IDLFile:  *src,
			Receiver: *receiver,
			Package:  *pkg,
		})
		if err != nil {
			return err
		}
		cliOutputf("added %d method(s) to %s from %s\n", n, *file, *src)
		return nil
	}
	if *name == "" {
		*name = strings.Join(remaining, " ")
	}
	if *name == "" {
		return fmt.Errorf("%w: --method is required for `gofly handler complete`", errUsage)
	}
	completer := generator.NewHandlerCompleter(*file, *receiver, *pkg, nil)
	n, err := completer.Complete([]generator.Method{{
		Name:    *name,
		Body:    *body,
		Comment: *comment,
	}})
	if err != nil {
		return err
	}
	if n > 0 {
		cliOutputf("added %d method(s) to %s\n", n, *file)
	} else {
		cliOutputf("nothing to do: %s already contains %s\n", *file, *name)
	}
	return nil
}

// looksLikeShellScript 通过扩展名或文件前两个字节的 shebang（#!）
// 判断插件是否是接收普通 CLI 参数的 shell 脚本。
func looksLikeShellScript(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasSuffix(strings.ToLower(path), ".sh") {
		return true
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return false
	}
	// #nosec G304 -- plugin compatibility probing reads the explicit plugin path supplied by the operator.
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 2)
	if n, _ := f.Read(buf); n < 2 {
		return false
	}
	return buf[0] == '#' && buf[1] == '!'
}

// apiBreakingCommand 实现 `gofly api breaking --base old.api --target new.api`。
// 如命中至少一条 SeverityBreaking，返回 generator.ErrBreakingChanges（退出码非 0）。
func apiBreakingCommand(args []string) error {
	fs := flag.NewFlagSet("api breaking", flag.ContinueOnError)
	base := fs.String("base", "", "base api file")
	target := fs.String("target", "", "target api file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	report, err := generator.DetectAPIChanges(generator.APIBreakingOptions{Base: *base, Target: *target})
	if err != nil {
		return err
	}
	cliOutput(string(generator.FormatBreakingText(report)))
	if report.HasBreaking() {
		return generator.ErrBreakingChanges
	}
	return nil
}

// rpcBreakingCommand 实现 `gofly rpc breaking --base old.proto --target new.proto`。
func rpcBreakingCommand(args []string) error {
	fs := flag.NewFlagSet("rpc breaking", flag.ContinueOnError)
	base := fs.String("base", "", "base proto file")
	target := fs.String("target", "", "target proto file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	report, err := generator.DetectProtoDescriptorChanges(generator.ProtoBreakingOptions{Base: *base, Target: *target})
	if err != nil {
		return err
	}
	cliOutput(formatRPCDescriptorCompatibilityText(report))
	if report.HasBreaking() {
		return generator.ErrBreakingChanges
	}
	return nil
}

func rpcDescriptorCommand(args []string) error {
	fs := flag.NewFlagSet("rpc descriptor", flag.ContinueOnError)
	base := fs.String("base", "", "base descriptor json file")
	target := fs.String("target", "", "target descriptor json file")
	remoteURL := fs.String("url", "", "remote admin descriptor URL or admin base URL")
	service := fs.String("service", "", "service name when --url points at an admin base URL")
	formatName := fs.String("format", "text", "output format: text or json")
	token := fs.String("token", "", "bearer token for descriptor URL sources")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	if *remoteURL != "" {
		if *base == "" {
			*base = *remoteURL
		} else if *target == "" {
			*target = *remoteURL
		}
	}
	if *base == "" || *target == "" {
		return fmt.Errorf("%w: base and target descriptor sources are required", errUsage)
	}
	baseDescriptor, err := readRPCDescriptorSource(*base, *token, *service)
	if err != nil {
		return fmt.Errorf("read base descriptor: %w", err)
	}
	targetDescriptor, err := readRPCDescriptorSource(*target, *token, *service)
	if err != nil {
		return fmt.Errorf("read target descriptor: %w", err)
	}
	report := rpc.CompareDescriptors(baseDescriptor, targetDescriptor)
	switch strings.ToLower(strings.TrimSpace(*formatName)) {
	case "", "text":
		cliOutput(formatRPCDescriptorCompatibilityText(report))
	case "json":
		if err := printJSON(report); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unsupported rpc descriptor format %q", errUsage, *formatName)
	}
	if report.HasBreaking() {
		return generator.ErrBreakingChanges
	}
	return nil
}

func readRPCDescriptorSource(source string, token string, service string) (rpc.Descriptor, error) {
	if descriptorSourceIsURL(source) {
		return readRPCDescriptorURL(source, token, service)
	}
	return readRPCDescriptorFile(source)
}

func descriptorSourceIsURL(source string) bool {
	lower := strings.ToLower(strings.TrimSpace(source))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func readRPCDescriptorURL(source string, token string, service string) (rpc.Descriptor, error) {
	parsed, err := url.Parse(source)
	if err != nil {
		return rpc.Descriptor{}, err
	}
	scheme := strings.ToLower(parsed.Scheme)
	if parsed.Host == "" || (scheme != "http" && scheme != "https") {
		return rpc.Descriptor{}, fmt.Errorf("unsupported descriptor URL %q", source)
	}
	if err := normalizeRPCDescriptorURL(parsed, service); err != nil {
		return rpc.Descriptor{}, err
	}
	client := http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		return rpc.Descriptor{}, err
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := client.Do(req)
	if err != nil {
		return rpc.Descriptor{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return rpc.Descriptor{}, fmt.Errorf("descriptor endpoint returned status %d", resp.StatusCode)
	}
	return decodeRPCDescriptor(resp.Body)
}

func normalizeRPCDescriptorURL(parsed *url.URL, service string) error {
	plainPath := strings.TrimRight(parsed.Path, "/")
	service = strings.TrimSpace(service)
	switch {
	case strings.Contains(plainPath, "/rpc/admin/descriptors/"):
		return nil
	case strings.HasSuffix(plainPath, "/rpc/admin/descriptors"):
		if service == "" {
			return fmt.Errorf("--service is required when descriptor URL points at %s", parsed.String())
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + url.PathEscape(service)
		return nil
	case service != "":
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/rpc/admin/descriptors/" + url.PathEscape(service)
		return nil
	case strings.HasSuffix(plainPath, "/admin"):
		return fmt.Errorf("--service is required when descriptor URL points at admin base %s", parsed.String())
	default:
		return nil
	}
}

func readRPCDescriptorFile(path string) (rpc.Descriptor, error) {
	// #nosec G304 -- descriptor comparison reads explicit descriptor JSON files supplied to the CLI.
	f, err := os.Open(path)
	if err != nil {
		return rpc.Descriptor{}, err
	}
	defer func() { _ = f.Close() }()
	return decodeRPCDescriptor(f)
}

func decodeRPCDescriptor(r io.Reader) (rpc.Descriptor, error) {
	var descriptor rpc.Descriptor
	if err := json.NewDecoder(r).Decode(&descriptor); err != nil {
		return rpc.Descriptor{}, err
	}
	if err := descriptor.Validate(); err != nil {
		return rpc.Descriptor{}, err
	}
	return descriptor, nil
}

func formatRPCDescriptorCompatibilityText(report rpc.DescriptorCompatibilityReport) string {
	if len(report.Changes) == 0 {
		return "No breaking changes\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Descriptor compatibility: %d breaking, %d warning(s), %d change(s)\n", report.Breaking, report.Warnings, len(report.Changes))
	for _, change := range report.Changes {
		fmt.Fprintf(&b, "[%s] %s %s: %s\n", change.Severity, change.Category, change.Subject, change.Description)
	}
	return b.String()
}
