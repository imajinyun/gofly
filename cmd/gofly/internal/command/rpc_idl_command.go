package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func rpcIDLCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc idl", flag.ContinueOnError)
	file := registerIDLFileFlags(fs, "proto or thrift idl file")
	formatName := registerCLIFormatFlag(fs, outputText, "output format: text or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	idlFile := file.resolve(leadingFile, remaining)
	if idlFile == "" {
		return fmt.Errorf("%w: idl file is required", errUsage)
	}
	doc, err := generator.ReadRPCIDL(idlFile)
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

func rpcLintCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("rpc lint", flag.ContinueOnError)
	file := registerIDLFileFlags(fs, "proto or thrift idl file")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	idlFile := file.resolve(leadingFile, remaining)
	if idlFile == "" {
		return fmt.Errorf("%w: idl file is required", errUsage)
	}
	doc, err := generator.ReadRPCIDL(idlFile)
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
	file := registerIDLFileFlags(fs, "proto or thrift idl file")
	formatName := registerCLIFormatFlag(fs, outputText, "output format: text or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	idlFile := file.resolve(leadingFile, remaining)
	if idlFile == "" {
		return fmt.Errorf("%w: idl file is required", errUsage)
	}
	doc, err := generator.ReadRPCIDL(idlFile)
	if err != nil {
		return err
	}
	report := generator.RPCIDLReportFor(doc)
	format, err := normalizeCLIFormat(formatName, outputText, outputText, outputJSON)
	if err != nil {
		return fmt.Errorf("%w: unsupported rpc deps format %q", errUsage, valueFromStringFlag(formatName))
	}
	switch format {
	case outputText:
		for _, dep := range report.Imports {
			cliOutputln(dep)
		}
		return nil
	case outputJSON:
		out, err := generator.FormatRPCIDLReport(doc, "json")
		if err != nil {
			return err
		}
		cliOutputln(strings.TrimRight(string(out), "\n"))
		return nil
	}
	return nil
}
