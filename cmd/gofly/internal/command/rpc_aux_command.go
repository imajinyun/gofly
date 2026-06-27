package command

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// rpcPluginCommand implements `gofly rpc plugin <plugin> --file <.proto> --dir <dir>`.
func rpcPluginCommand(args []string) error {
	leadingPlugin, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc plugin", flag.ContinueOnError)
	file := registerIDLFileFlags(fs, "proto file")
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
	protoFile := file.resolve("", nil)
	if protoFile == "" {
		return fmt.Errorf("%w: --file is required for `gofly rpc plugin`", errUsage)
	}
	if *pluginArg == "" {
		return fmt.Errorf("%w: plugin is required for `gofly rpc plugin`", errUsage)
	}
	return runPostPlugins(*pluginArg, generator.PluginRequest{
		Command: "rpc",
		Input:   map[string]string{"proto": protoFile},
		Dir:     *dir,
	})
}

func rpcCheckCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc check", flag.ContinueOnError)
	file := registerIDLFileFlags(fs, "proto file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	protoFile := file.resolve(leadingFile, remaining)
	if protoFile == "" {
		return fmt.Errorf("%w: proto file is required", errUsage)
	}
	content, err := os.ReadFile(protoFile)
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
	file := registerIDLFileFlags(fs, "proto source file")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output file")
	filename := fs.String("filename", "", "output filename")
	docOutput := registerDocOutputFlags(fs, docOutputFlagOptions{
		DefaultFormat: "openapi",
		YAMLUsage:     "write OpenAPI as yaml",
		JSONUsage:     "write OpenAPI as json",
	})
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	protoFile := file.resolve(leadingFile, remaining)
	if protoFile == "" {
		return fmt.Errorf("%w: proto file is required", errUsage)
	}
	outputPath := output.resolve()
	docOutput.applyFormatAliases("openapi")
	if outputPath == "" && *filename != "" {
		outputPath = filepath.Join(*dir, *filename)
	}
	return generator.GenerateProtoDoc(generator.ProtoDocOptions{ProtoFile: protoFile, Dir: *dir, Output: outputPath, Format: valueFromStringFlag(docOutput.Format)})
}
