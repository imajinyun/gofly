package command

import (
	"flag"
	"path/filepath"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiDocCommand(command string, args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api doc", flag.ContinueOnError)
	file := registerAPIFileFlags(fs, "api file")
	dir := fs.String("dir", ".", "output directory")
	output := registerOutputPathFlags(fs, "output file")
	filename := fs.String("filename", "", "swagger filename")
	oas3 := fs.Bool("oas3", false, "write OpenAPI v3 output")
	defaultFormat := "markdown"
	if command == "swagger" {
		defaultFormat = "openapi"
	}
	docOutput := registerDocOutputFlags(fs, docOutputFlagOptions{
		DefaultFormat: defaultFormat,
		FormatUsage:   "doc format: markdown, openapi/json, or yaml",
		YAMLUsage:     "write swagger as yaml",
		JSONUsage:     "write swagger as json",
	})
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	outputPath := output.resolve()
	docOutput.applyFormatAliases("json")
	if *oas3 && valueFromStringFlag(docOutput.Format) == "markdown" {
		setStringFlag(docOutput.Format, "openapi")
	}
	if outputPath == "" && *filename != "" {
		outputPath = filepath.Join(*dir, *filename)
	}
	return generator.GenerateAPIDoc(generator.APIDocOptions{APIFile: file.resolve(leadingFile, remaining), Dir: *dir, Output: outputPath, Format: valueFromStringFlag(docOutput.Format)})
}
