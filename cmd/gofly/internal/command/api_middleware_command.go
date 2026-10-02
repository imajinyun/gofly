package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiMiddlewareCommand(args []string) error {
	leadingNames, args := splitLeadingNames(args)
	fs := flag.NewFlagSet("api middleware", flag.ContinueOnError)
	name := fs.String("name", "", "middleware name, comma-separated for multiple middlewares")
	api := registerAPIFileFlags(fs, "api file to discover middleware declarations")
	dir := fs.String("dir", ".", "service root directory")
	preset := fs.String("preset", "", "maintained middleware preset, comma-separated or all")
	list := fs.Bool("list", false, "list maintained middleware presets")
	dryRun := fs.Bool("dry-run", false, "plan preset files without writing")
	jsonOutput := registerCLIJSONOutputFlag(fs, "emit preset catalog or result as JSON")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		*name = strings.Join(leadingNames, ",")
	}
	fillNameFromArgs(name, remaining)
	apiFile := api.resolve("", nil)
	names := splitCSV(*name)
	switch {
	case len(leadingNames) > 0:
		names = append(names, remaining...)
	case *name != "" && len(remaining) > 1:
		names = append(names, remaining[1:]...)
	case *name == "":
		names = append(names, remaining...)
	}
	if *list {
		if *preset != "" || *dryRun || apiFile != "" || len(names) > 0 {
			return fmt.Errorf("%w: --list cannot be combined with middleware generation inputs", errUsage)
		}
		return printMiddlewarePresetCatalog(*jsonOutput || outputMode() == outputJSON)
	}
	if *preset != "" {
		if apiFile != "" || len(names) > 0 {
			return fmt.Errorf("%w: cannot combine --preset with middleware names or --api", errUsage)
		}
		result, err := generator.GenerateMiddlewarePresets(generator.MiddlewarePresetOptions{
			Names:  splitCSV(*preset),
			Dir:    *dir,
			DryRun: *dryRun,
		})
		if err != nil {
			return err
		}
		return printMiddlewarePresetResult(result, *jsonOutput || outputMode() == outputJSON)
	}
	if *dryRun {
		return fmt.Errorf("%w: --dry-run requires --preset", errUsage)
	}
	if apiFile != "" {
		apiNames, err := apiMiddlewareNames(apiFile)
		if err != nil {
			return err
		}
		names = append(names, apiNames...)
	}
	return generator.GenerateMiddleware(generator.MiddlewareOptions{Names: names, Dir: *dir})
}

func printMiddlewarePresetCatalog(jsonOutput bool) error {
	presets := generator.ListMiddlewarePresets()
	if jsonOutput {
		return printJSONEnvelope("api.middleware.list", map[string]any{
			"schema":  generator.MiddlewarePresetCatalogSchema,
			"presets": presets,
		})
	}
	cliOutputlnIf("Available middleware presets:")
	for _, preset := range presets {
		cliOutputfIf("  %-20s %-10s %s\n", preset.Name, preset.Kind, preset.Description)
	}
	cliOutputlnIf("  all                  bundle     install every preset")
	return nil
}

func printMiddlewarePresetResult(result generator.MiddlewarePresetResult, jsonOutput bool) error {
	if jsonOutput {
		return printJSONEnvelope("api.middleware.preset", result)
	}
	if result.DryRun {
		cliOutputlnIf("Middleware preset plan:")
	} else {
		cliOutputlnIf("Middleware presets applied:")
	}
	for _, file := range result.Files {
		cliOutputfIf("  %-10s %s\n", file.Status, file.Path)
	}
	if !result.DryRun {
		cliOutputlnIf("Review configuration and register the generated middleware explicitly; routes were not modified.")
	}
	return nil
}

func apiMiddlewareNames(path string) ([]string, error) {
	doc, err := generator.LoadAPI(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, service := range doc.Services {
		names = append(names, service.Server.Middleware...)
	}
	return uniqueStrings(names), nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
