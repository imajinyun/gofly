package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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
