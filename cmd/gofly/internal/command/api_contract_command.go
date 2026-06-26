package command

import (
	"flag"
	"fmt"
	"os"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiCheckCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api check", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
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
	if *file == "" {
		return fmt.Errorf("%w: api file is required", errUsage)
	}
	content, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read api file: %w", err)
	}
	doc, err := generator.ParseAPI(string(content))
	if err != nil {
		return err
	}
	if err := generator.ValidateAPI(doc); err != nil {
		return err
	}
	cliOutputf("api ok: %d type(s), %d service(s)\n", len(doc.Messages), len(doc.Services))
	return nil
}

func splitLeadingNames(args []string) ([]string, []string) {
	names := make([]string, 0)
	for len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		names = append(names, args[0])
		args = args[1:]
	}
	return names, args
}
