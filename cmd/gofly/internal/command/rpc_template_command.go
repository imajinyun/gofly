package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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
