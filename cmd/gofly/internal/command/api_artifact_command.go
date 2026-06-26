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

func apiDocCommand(command string, args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api doc", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output file")
	o := fs.String("o", "", "output file")
	filename := fs.String("filename", "", "swagger filename")
	yamlOut := fs.Bool("yaml", false, "write swagger as yaml")
	jsonOut := fs.Bool("json", false, "write swagger as json")
	oas3 := fs.Bool("oas3", false, "write OpenAPI v3 output")
	defaultFormat := "markdown"
	if command == "swagger" {
		defaultFormat = "openapi"
	}
	format := fs.String("format", defaultFormat, "doc format: markdown, openapi/json, or yaml")
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
	if *output == "" {
		*output = *o
	}
	if *yamlOut {
		*format = "yaml"
	}
	if *jsonOut {
		*format = "json"
	}
	if *oas3 && *format == "markdown" {
		*format = "openapi"
	}
	if *output == "" && *filename != "" {
		*output = filepath.Join(*dir, *filename)
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPIDoc(generator.APIDocOptions{APIFile: *file, Dir: *dir, Output: *output, Format: *format})
}

func apiClientCommand(command string, args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api client", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output file")
	o := fs.String("o", "", "output file")
	language := fs.String("language", "typescript", "client language: typescript, javascript, dart, java, or kotlin")
	baseURL := fs.String("base-url", "", "default API base URL")
	caller := fs.String("caller", "", "client caller name")
	unwrap := fs.Bool("unwrap", false, "unwrap response envelopes")
	legacy := fs.Bool("legacy", false, "use legacy client output layout")
	hostname := fs.String("hostname", "", "api hostname")
	scheme := fs.String("scheme", "", "api scheme")
	pkg := fs.String("pkg", "", "generated package name")
	if command == "ts" || command == "typescript" {
		*language = "typescript"
	}
	if command == "js" || command == "javascript" {
		*language = "javascript"
	}
	if command == "dart" {
		*language = "dart"
	}
	if command == "java" {
		*language = "java"
	}
	if command == "kotlin" || command == "kt" {
		*language = "kotlin"
	}
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *caller != "" {
		warnNoopFlag("api client", "caller", "client naming is derived from the service name")
	}
	if *unwrap {
		warnNoopFlag("api client", "unwrap", "generated clients currently preserve response shapes from the API spec")
	}
	if *legacy {
		warnNoopFlag("api client", "legacy", "gofly emits the current client layout")
	}
	if *pkg != "" {
		warnNoopFlag("api client", "pkg", "non-Go clients do not use package names; Go DTOs use api types")
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
	if *baseURL == "" && *hostname != "" {
		if *scheme == "" {
			*scheme = "http"
		}
		*baseURL = *scheme + "://" + *hostname
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPIClient(generator.APIClientOptions{
		APIFile:  *file,
		Dir:      *dir,
		Output:   *output,
		Language: *language,
		BaseURL:  *baseURL,
	})
}

func apiTypesCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api types", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output file")
	o := fs.String("o", "", "output file")
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
	if *output == "" {
		*output = *o
	}
	fillNameFromArgs(file, remaining)
	return generator.GenerateAPITypes(generator.APITypesOptions{
		APIFile: *file,
		Dir:     *dir,
		Output:  *output,
		Package: *pkg,
	})
}
