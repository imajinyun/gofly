package command

import (
	"flag"
	"path/filepath"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func quickstartCommand(args []string) error {
	if printCommandHelp("quickstart", args) {
		return nil
	}
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("quickstart", flag.ContinueOnError)
	name := fs.String("name", "", "api service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	style := fs.String("style", generator.ServiceStyleBasic, "api scaffold style: minimal, basic, or production")
	apiSpec := fs.Bool("api-spec", true, "generate an .api file")
	serviceType := fs.String("service-type", "", "quickstart service type: mono or micro")
	t := fs.String("t", "", "quickstart service type")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *serviceType == "" {
		*serviceType = *t
	}
	if *serviceType == "micro" && *style == generator.ServiceStyleBasic {
		*style = generator.ServiceStyleProduction
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *dir == "" && *name != "" {
		*dir = *name
	}
	if err := generator.GenerateAPINew(generator.APINewOptions{
		Name:        *name,
		Module:      *module,
		Dir:         *dir,
		Style:       *style,
		SkipAPISpec: !*apiSpec,
	}); err != nil {
		return err
	}
	if !*apiSpec {
		return nil
	}
	apiFile := generator.APIOptions{
		APIFile: filepath.Join(*dir, *name+".api"),
		Dir:     *dir,
		Package: "api",
	}
	return generator.GenerateRESTFromAPI(apiFile)
}
