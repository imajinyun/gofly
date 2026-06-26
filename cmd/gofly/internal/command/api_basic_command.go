package command

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiCommand(args []string) error {
	if printCommandHelp("api", args) {
		return nil
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return apiTemplateCommand(args)
	}
	return apiCommands.dispatch(args, "gofly api check|breaking|gen|go|types|route|import|diff|plugin|middleware|format|doc|client|new")
}

func apiTemplateCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api template", flag.ContinueOnError)
	output := fs.String("output", "", "output api template file")
	o := fs.String("o", "", "output api template file")
	name := fs.String("name", "", "api service name used in the template")
	home := fs.String("home", "", "template home directory")
	remote := fs.String("remote", "", "remote template repository")
	branch := fs.String("branch", "", "remote template branch")
	style := fs.String("style", "go_zero", "scaffold style option")
	multiple := fs.Bool("multiple", false, "generate multiple service packages")
	_ = style
	_ = multiple
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *output == "" {
		*output = *o
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	return generator.GenerateAPITemplate(generator.IDLTemplateOptions{Output: *output, Name: *name, TemplateDir: *home, Remote: *remote, Branch: *branch})
}

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

func apiRouteCommand(args []string) error {
	leadingFile, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api route", flag.ContinueOnError)
	file := fs.String("file", "", "api file")
	api := fs.String("api", "", "api file")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output routes file")
	o := fs.String("o", "", "output routes file")
	format := fs.String("format", "text", "route format: text, markdown, or json")
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
	return generator.GenerateAPIRoutes(generator.APIRouteOptions{APIFile: *file, Dir: *dir, Output: *output, Format: *format})
}

func apiImportCommand(args []string) error {
	leadingSource, args := splitLeadingName(args)
	fs := flag.NewFlagSet("api import", flag.ContinueOnError)
	src := fs.String("src", "", "OpenAPI/Swagger JSON or YAML file")
	from := fs.String("from", "", "OpenAPI/Swagger JSON or YAML file")
	swagger := fs.String("swagger", "", "Swagger JSON or YAML file, alias for --src")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output .api file")
	o := fs.String("o", "", "output .api file")
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
	if *output == "" {
		*output = *o
	}
	fillNameFromArgs(src, remaining)
	return generator.GenerateAPIFromOpenAPI(generator.APIImportOptions{Source: *src, Dir: *dir, Output: *output, Service: *service})
}

func apiDiffCommand(args []string) error {
	leadingFiles, args := splitLeadingNames(args)
	fs := flag.NewFlagSet("api diff", flag.ContinueOnError)
	base := fs.String("base", "", "base api file")
	old := fs.String("old", "", "base api file, alias for --base")
	target := fs.String("target", "", "target api file")
	newFile := fs.String("new", "", "target api file, alias for --target")
	dir := fs.String("dir", ".", "output directory")
	output := fs.String("output", "", "output diff file")
	o := fs.String("o", "", "output diff file")
	format := fs.String("format", "text", "diff format: text, markdown, or json")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *base == "" {
		*base = *old
	}
	if *target == "" {
		*target = *newFile
	}
	if *base == "" && len(leadingFiles) > 0 {
		*base = leadingFiles[0]
	}
	if *target == "" && len(leadingFiles) > 1 {
		*target = leadingFiles[1]
	}
	if *output == "" {
		*output = *o
	}
	if *base == "" && len(remaining) > 0 {
		*base = remaining[0]
		remaining = remaining[1:]
	}
	if *target == "" && len(remaining) > 0 {
		*target = remaining[0]
	}
	return generator.GenerateAPIDiff(generator.APIDiffOptions{Base: *base, Target: *target, Dir: *dir, Output: *output, Format: *format})
}

func gatewayGenCommand(args []string) error {
	leadingName, args := splitLeadingName(args)
	fs := flag.NewFlagSet("gateway gen", flag.ContinueOnError)
	name := fs.String("name", "", "gateway service name")
	module := fs.String("module", "", "go module path")
	dir := fs.String("dir", "", "output directory")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = leadingName
	}
	fillNameFromArgs(name, remaining)
	if *name == "" {
		*name = "gateway"
	}
	return generator.GenerateGateway(generator.GatewayOptions{Name: *name, Module: *module, Dir: *dir})
}
