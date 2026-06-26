package command

import (
	"flag"
	"fmt"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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
