package command

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiFormatCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api format", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", "", "directory containing .api files")
	output := fs.String("output", "", "formatted output file")
	o := fs.String("o", "", "formatted output file")
	write := fs.Bool("write", true, "write result to source file")
	w := fs.Bool("w", true, "write result to source file")
	iu := fs.Bool("iu", false, "preserve import/use layout")
	stdin := fs.Bool("stdin", false, "read api content from stdin")
	declare := fs.Bool("declare", false, "format declarations only")
	_ = iu
	_ = declare
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if flagWasSet(fs, "w") {
		*write = *w
	}
	if *stdin {
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read api from stdin: %w", err)
		}
		doc, err := generator.ParseAPI(string(content))
		if err != nil {
			return err
		}
		formatted := generator.FormatAPI(doc)
		if *output == "" {
			*output = *o
		}
		if *output != "" {
			// #nosec G301 -- CLI formatting writes user-visible project artifacts that should remain traversable by tools.
			if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
				return fmt.Errorf("create api format output directory: %w", err)
			}
			// #nosec G306 -- formatted API files are generated project artifacts intentionally readable by collaborators and tooling.
			return os.WriteFile(*output, formatted, 0o644)
		}
		cliOutput(string(formatted))
		return nil
	}
	if *file == "" {
		*file = *api
	}
	if *file == "" {
		*file = leadingFile
	}
	if *output == "" {
		*output = *o
	}
	fillNameFromArgs(file, remaining)
	formatted, err := generator.FormatAPIFromFile(generator.APIFormatOptions{
		APIFile: *file,
		Dir:     *dir,
		Output:  *output,
		Write:   *write,
	})
	if err != nil {
		return err
	}
	if !*write && *output == "" && *dir == "" {
		cliOutput(string(formatted))
	}
	return nil
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			found = true
		}
	})
	return found
}
