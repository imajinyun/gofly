package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiRouteCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api route", flag.ContinueOnError)
	file := registerAPIFileFlags(fs, "api file")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output routes file")
	format := registerCLIFormatFlag(fs, outputText, "route format: text, markdown, or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	return generator.GenerateAPIRoutes(generator.APIRouteOptions{APIFile: file.resolve(leadingFile, remaining), Dir: *dir, Output: output.resolve(), Format: *format})
}
