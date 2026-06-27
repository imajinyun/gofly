package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiTemplateCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api template", flag.ContinueOnError)
	output := registerOutputPathFlags(fs, "output api template file")
	name := fs.String("name", "", "api service name used in the template")
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
	return generator.GenerateAPITemplate(generator.IDLTemplateOptions{
		Output:      output.resolve(),
		Name:        *name,
		TemplateDir: *templateSource.Home,
		Remote:      *templateSource.Remote,
		Branch:      *templateSource.Branch,
	})
}
