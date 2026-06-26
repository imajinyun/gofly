package command

import (
	"flag"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiCommand(args []string) error {
	if printCommandHelp("api", args) {
		return nil
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return apiTemplateCommand(args)
	}
	return apiCommands.dispatch(args, "gofly api check|breaking|gen|go|types|route|import|diff|plugin|middleware|format|doc|client|new")
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
