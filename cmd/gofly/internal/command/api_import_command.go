package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

type apiImportSourceFlags struct {
	Src     *string
	From    *string
	Swagger *string
}

func registerAPIImportSourceFlags(fs *flag.FlagSet) apiImportSourceFlags {
	return apiImportSourceFlags{
		Src:     fs.String("src", "", "OpenAPI/Swagger JSON or YAML file"),
		From:    fs.String("from", "", "OpenAPI/Swagger JSON or YAML file"),
		Swagger: fs.String("swagger", "", "Swagger JSON or YAML file, alias for --src"),
	}
}

func (f apiImportSourceFlags) normalize(leadingSource string, remaining []string) {
	if valueFromStringFlag(f.Src) == "" {
		setStringFlag(f.Src, valueFromStringFlag(f.From))
	}
	if valueFromStringFlag(f.Src) == "" {
		setStringFlag(f.Src, valueFromStringFlag(f.Swagger))
	}
	if valueFromStringFlag(f.Src) == "" {
		setStringFlag(f.Src, leadingSource)
	}
	fillNameFromArgs(f.Src, remaining)
}

func apiImportCommand(args []string) error {
	leadingSource, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api import", flag.ContinueOnError)
	source := registerAPIImportSourceFlags(fs)
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output .api file")
	service := fs.String("service", "", "service name for generated .api")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	source.normalize(leadingSource, remaining)
	return generator.GenerateAPIFromOpenAPI(generator.APIImportOptions{Source: *source.Src, Dir: *dir, Output: output.resolve(), Service: *service})
}
