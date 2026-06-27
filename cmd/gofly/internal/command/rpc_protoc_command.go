package command

import (
	"context"
	"flag"
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/cmd/gofly/internal/spinner"
)

func rpcProtocCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc protoc", flag.ContinueOnError)
	file := registerIDLFileFlags(fs, "proto file")
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
	protoFile := file.resolve(leadingFile, remaining)
	if protoFile == "" {
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
		errorf("[gofly] rpc protoc: proto=%s go_out=%s go-grpc_out=%s proto_path=%s\n", protoFile, *goOut, *goGRPCOut, *protoPath)
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
		ProtoFile:    protoFile,
		ProtoPath:    splitCSV(*protoPath),
		GoOut:        *goOut,
		GoGRPCOut:    *goGRPCOut,
		GoflyOut:     goflyPluginOptions.Out,
		GoflyPlugin:  goflyPlugin,
		GoflyOptions: goflyPluginOptions.Options,
		Protoc:       *protoc,
		ExtraArgs:    extraArgs,
		Env:          goflyPluginOptions.Env,
		Timeout:      *timeout,
	})
	sp.Stop()
	if err != nil {
		return err
	}
	return nil
}
