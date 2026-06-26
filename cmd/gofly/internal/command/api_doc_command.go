package command

import (
	"flag"
	"path/filepath"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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
