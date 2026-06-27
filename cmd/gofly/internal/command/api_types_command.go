package command

import (
	"flag"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiTypesCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api types", flag.ContinueOnError)
	file := registerAPIFileFlags(fs, "api file")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output file")
	pkg := fs.String("package", "types", "generated Go package name")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	return generator.GenerateAPITypes(generator.APITypesOptions{
		APIFile: file.resolve(leadingFile, remaining),
		Dir:     *dir,
		Output:  output.resolve(),
		Package: *pkg,
	})
}
