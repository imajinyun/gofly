package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiImportCommand(args []string) error {
	leadingSource, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api import", flag.ContinueOnError)
	src := fs.String("src", "", "OpenAPI/Swagger JSON or YAML file")
	from := fs.String("from", "", "OpenAPI/Swagger JSON or YAML file")
	swagger := fs.String("swagger", "", "Swagger JSON or YAML file, alias for --src")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output .api file")
	service := fs.String("service", "", "service name for generated .api")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *src == "" {
		*src = *from
	}
	if *src == "" {
		*src = *swagger
	}
	if *src == "" {
		*src = leadingSource
	}
	fillNameFromArgs(src, remaining)
	return generator.GenerateAPIFromOpenAPI(generator.APIImportOptions{Source: *src, Dir: *dir, Output: output.resolve(), Service: *service})
}
