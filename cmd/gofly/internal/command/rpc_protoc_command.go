package command

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/cmd/gofly/internal/spinner"
)

func rpcProtocCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc protoc", flag.ContinueOnError)
	file := registerIDLFileFlags(fs, "proto file")
	dir := fs.String("dir", ".", "output directory")
	protoPath := csvListFlag{}
	fs.Var(&protoPath, "proto_path", "comma-separated proto include paths; may be repeated")
	fs.Var(&protoPath, "proto-path", "comma-separated proto include paths; may be repeated")
	fs.Var(&protoPath, "I", "comma-separated proto include paths; may be repeated")
	goOut := fs.String("go_out", "", "protoc go output directory")
	goGRPCOut := fs.String("go-grpc_out", "", "protoc go-grpc output directory")
	zrpcOut := fs.String("zrpc_out", "", "service output directory")
	goOpt := csvListFlag{}
	goGRPCOpt := csvListFlag{}
	goGRPCOptUnderscore := csvListFlag{}
	fs.Var(&goOpt, "go_opt", "extra protoc-gen-go option; may be repeated")
	fs.Var(&goGRPCOpt, "go-grpc_opt", "extra protoc-gen-go-grpc option; may be repeated")
	fs.Var(&goGRPCOptUnderscore, "go_grpc_opt", "extra protoc-gen-go-grpc option; may be repeated")
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
	allowExternalPlugin := fs.Bool("allow-external-plugin", false, "forward non-gofly protoc plugin argv explicitly")
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
	goflyPlugin, useGoflyPlugin, externalPlugins := resolveGoflyProtocPlugin(*pluginArg)
	zrpcScaffold := *zrpcOut != "" && !useGoflyPlugin
	if *style != "go_zero" {
		if zrpcScaffold {
			return fmt.Errorf("%w: --zrpc_out currently requires --style go_zero", errUsage)
		}
		warnNoopFlag("rpc protoc", "style", "protoc and gofly plugin output are not style-aware")
	}
	if *home != "" || *remote != "" || *branch != "" {
		if zrpcScaffold {
			return fmt.Errorf("%w: --home, --remote, and --branch are not supported with --zrpc_out", errUsage)
		}
		warnNoopFlag("rpc protoc", "home/remote/branch", "template source does not affect protoc or gofly plugin output")
	}
	if (*multiple || *m) && !useGoflyPlugin && !zrpcScaffold {
		warnNoopFlag("rpc protoc", "multiple", "only affects --zrpc_out scaffold or --plugin gofly output")
	}
	if (flagProvided(fs, "client") || flagProvided(fs, "c")) && !useGoflyPlugin && !zrpcScaffold {
		warnNoopFlag("rpc protoc", "client", "only affects --zrpc_out scaffold or --plugin gofly output")
	}
	if *nameFromFilename && !useGoflyPlugin && !zrpcScaffold {
		warnNoopFlag("rpc protoc", "name-from-filename", "only affects --zrpc_out scaffold or --plugin gofly output")
	}
	if *module != "" && !useGoflyPlugin && !zrpcScaffold {
		warnNoopFlag("rpc protoc", "module", "module import paths are controlled by go_package and protoc options unless --plugin gofly is used")
	}
	if len(externalPlugins) > 0 {
		if !*allowExternalPlugin {
			warnNoopFlag("rpc protoc", "plugin", "external protoc plugins require --allow-external-plugin")
		} else if err := validateExternalProtocPlugins(externalPlugins); err != nil {
			return err
		}
	}
	protoFiles := file.resolveAll(leadingFile, remaining)
	if len(protoFiles) == 0 {
		return fmt.Errorf("%w: proto file is required", errUsage)
	}
	if zrpcScaffold && len(externalPlugins) > 0 {
		return fmt.Errorf("%w: external --plugin values are not supported with --zrpc_out scaffold", errUsage)
	}
	if *timeout <= 0 {
		return fmt.Errorf("%w: --timeout must be greater than zero", errUsage)
	}
	includePaths := []string{"."}
	if flagProvided(fs, "proto_path") || flagProvided(fs, "proto-path") || flagProvided(fs, "I") {
		includePaths = protoPath.values
	}
	if *zrpcOut != "" {
		*dir = *zrpcOut
	}
	if zrpcScaffold {
		name := ""
		if *nameFromFilename {
			name = strings.TrimSuffix(filepath.Base(protoFiles[0]), filepath.Ext(protoFiles[0]))
		}
		sp := spinner.New()
		if isQuiet() || outputMode() == outputJSON {
			sp.Disable()
		}
		sp.Start("generating zRPC-compatible scaffold...")
		err := generator.GenerateGRPCScaffold(context.Background(), generator.GRPCScaffoldOptions{
			ProtoFiles: protoFiles, ProtoPath: includePaths, Dir: *zrpcOut, Module: *module, Name: name,
			NameFromPackage: !*nameFromFilename, NoClient: !*client, Multiple: *multiple || *m, RequireMultiple: true, Protoc: *protoc, Timeout: *timeout,
		})
		sp.Stop()
		return err
	}
	if *goOut == "" {
		*goOut = *dir
	}
	if *goGRPCOut == "" {
		*goGRPCOut = *dir
	}
	extraArgs := splitCSV(*extra)
	for _, opt := range goOpt.values {
		extraArgs = append(extraArgs, "--go_opt="+opt)
	}
	for _, opt := range goGRPCOpt.values {
		extraArgs = append(extraArgs, "--go-grpc_opt="+opt)
	}
	for _, opt := range goGRPCOptUnderscore.values {
		extraArgs = append(extraArgs, "--go-grpc_opt="+opt)
	}
	if *verbose || *v {
		errorf("[gofly] rpc protoc: proto=%s go_out=%s go-grpc_out=%s proto_path=%s\n", strings.Join(protoFiles, ","), *goOut, *goGRPCOut, strings.Join(includePaths, ","))
	}
	goflyPluginOptions := buildGoflyProtocPluginOptions(useGoflyPlugin, goflyProtocPluginConfig{
		Dir:              *dir,
		Client:           *client,
		Multiple:         *multiple || *m,
		Module:           *module,
		NameFromFilename: *nameFromFilename,
	})

	sp := spinner.New()
	if isQuiet() || outputMode() == outputJSON {
		sp.Disable()
	}
	sp.Start("running protoc...")
	err = generator.GenerateStandardProto(context.Background(), generator.ProtocOptions{
		ProtoFiles:      protoFiles,
		ProtoPath:       includePaths,
		GoOut:           *goOut,
		GoGRPCOut:       *goGRPCOut,
		GoflyOut:        goflyPluginOptions.Out,
		GoflyPlugin:     goflyPlugin,
		GoflyOptions:    goflyPluginOptions.Options,
		ExternalPlugins: externalProtocPluginsForOptions(externalPlugins, *allowExternalPlugin),
		Protoc:          *protoc,
		ExtraArgs:       extraArgs,
		Env:             goflyPluginOptions.Env,
		Timeout:         *timeout,
	})
	sp.Stop()
	if err != nil {
		return err
	}
	return nil
}
