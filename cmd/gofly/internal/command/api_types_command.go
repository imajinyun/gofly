package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiTypesCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api types", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output file")
	pkg := fs.String("package", "types", "generated Go package name")
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
	return generator.GenerateAPITypes(generator.APITypesOptions{
		APIFile: *file,
		Dir:     *dir,
		Output:  output.resolve(),
		Package: *pkg,
	})
}
