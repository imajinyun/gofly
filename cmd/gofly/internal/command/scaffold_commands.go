package command

import (
	"flag"
	"fmt"
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

func migrateCommand(args []string) error {
	if printCommandHelp("migrate", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly migrate create <name>`", errUsage)
	}
	subcommand := args[0]
	if subcommand != "create" && subcommand != "new" {
		return fmt.Errorf("%w: expected `gofly migrate create <name>`", errUsage)
	}
	leadingName, rest := splitLeadingName(args[1:])
	fs := flag.NewFlagSet("migrate create", flag.ContinueOnError)
	name := fs.String("name", "", "migration name")
	dir := fs.String("dir", filepath.Join(".", "migrations"), "migration output directory")
	remaining, err := parseInterspersedFlags(fs, rest)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateMigration(generator.MigrationOptions{Name: *name, Dir: *dir})
}
