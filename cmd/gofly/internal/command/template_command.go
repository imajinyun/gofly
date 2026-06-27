package command

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func templateCommand(args []string) error {
	if printCommandHelp("template", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly template init|list|inspect|clean|update|revert`", errUsage)
	}
	subcommand := args[0]
	fs := flag.NewFlagSet("template "+subcommand, flag.ContinueOnError)
	dir := fs.String("dir", "", "template output directory")
	home := fs.String("home", "", "template output directory")
	remote := fs.String("remote", "", "remote template repository or local directory")
	branch := fs.String("branch", "", "remote template branch")
	category := fs.String("category", "", "template category filter")
	c := fs.String("c", "", "template category filter")
	name := fs.String("name", "", "template name filter")
	n := fs.String("n", "", "template name filter")
	outputFlags := registerCLIOutputFlags(fs, cliOutputFlagOptions{})
	remaining, err := parseInterspersedFlags(fs, args[1:])
	if err != nil {
		return err
	}
	if *dir == "" {
		*dir = *home
	}
	if *category == "" {
		*category = *c
	}
	if *name == "" {
		*name = *n
	}
	if *name == "" && len(remaining) > 0 {
		*name = remaining[0]
	}
	useJSON := valueFromBoolFlag(outputFlags.JSON) || strings.EqualFold(strings.TrimSpace(valueFromStringFlag(outputFlags.Format)), outputJSON)
	opts := generator.TemplateOptions{Dir: *dir, Remote: *remote, Branch: *branch, StrictRemote: true}
	switch subcommand {
	case "init", "update":
		if *category != "" || *name != "" {
			warnNoopFlag("template "+subcommand, "category/name", "template init/update currently syncs the full template set")
		}
		return generator.GenerateTemplateInit(opts)
	case "revert":
		if *category != "" || *name != "" {
			warnNoopFlag("template revert", "category/name", "template revert currently restores the full default template set")
		}
		return generator.GenerateTemplateInit(generator.TemplateOptions{Dir: *dir})
	case "list", "ls":
		catalog := filterProjectTemplates(generator.ListProjectTemplates(), *category, *name)
		if useJSON {
			return printJSONEnvelope("template.list", catalog)
		}
		for _, tmpl := range catalog {
			cliOutputf("%s\t%s\t%s\t%s\n", tmpl.ID, tmpl.Kind, tmpl.Architecture, tmpl.Description)
		}
		for _, file := range generator.ListTemplates(opts) {
			if !templateFilterMatch(file.Name, *category, *name) {
				continue
			}
			cliOutputf("%s\t%s\n", file.Name, file.Path)
		}
		return nil
	case "inspect", "show", "describe":
		if *name == "" {
			return fmt.Errorf("%w: template id is required for `gofly template inspect`", errUsage)
		}
		tmpl, ok := generator.GetProjectTemplate(*name)
		if !ok {
			return fmt.Errorf("%w: unknown project template %q", errUsage, *name)
		}
		if useJSON {
			return printJSONEnvelope("template.inspect", tmpl)
		}
		cliOutputf("id: %s\nname: %s\nkind: %s\narchitecture: %s\nrisk: %s\ncommand: %s\n", tmpl.ID, tmpl.Name, tmpl.Kind, tmpl.Architecture, tmpl.RiskLevel, tmpl.Command)
		cliOutputf("features: %s\n", strings.Join(tmpl.Features, ","))
		return nil
	case "clean":
		if *category != "" || *name != "" {
			for _, file := range generator.ListTemplates(opts) {
				if !templateFilterMatch(file.Name, *category, *name) {
					continue
				}
				if err := os.Remove(file.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("clean template %s: %w", file.Path, err)
				}
			}
			return nil
		}
		return generator.CleanTemplates(opts)
	default:
		return fmt.Errorf("%w: expected `gofly template init|list|inspect|clean|update|revert`", errUsage)
	}
}
