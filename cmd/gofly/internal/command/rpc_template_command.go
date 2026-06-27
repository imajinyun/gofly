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
	output := registerOutputPathFlags(fs, "output proto template file")
	name := fs.String("name", "", "rpc service name used in the template")
	templateSource := registerTemplateSourceFlags(fs, "", "", "")
	style := fs.String("style", "go_zero", "scaffold style option")
	multiple := fs.Bool("multiple", false, "generate multiple service packages")
	_ = style
	_ = multiple
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateRPCTemplate(generator.IDLTemplateOptions{
		Output:      output.resolve(),
		Name:        *name,
		TemplateDir: *templateSource.Home,
		Remote:      *templateSource.Remote,
		Branch:      *templateSource.Branch,
	})
}
