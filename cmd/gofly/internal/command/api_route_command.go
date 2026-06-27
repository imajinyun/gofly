package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiRouteCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api route", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output routes file")
	format := registerCLIFormatFlag(fs, outputText, "route format: text, markdown, or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *file == "" {
		*file = *api
	}
	if *file == "" {
		*file = leadingFile
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPIRoutes(generator.APIRouteOptions{APIFile: *file, Dir: *dir, Output: output.resolve(), Format: *format})
}
