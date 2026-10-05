package command

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func apiMiddlewareCommand(args []string) (err error) {
	leadingNames, args := splitLeadingNames(args)
	fs := flag.NewFlagSet("api middleware", flag.ContinueOnError)
	name := fs.String("name", "", "middleware name, comma-separated for multiple middlewares")
	api := registerAPIFileFlags(fs, "api file to discover middleware declarations")
	dir := fs.String("dir", ".", "service root directory")
	preset := fs.String("preset", "", "maintained middleware preset, comma-separated or all")
	list := fs.Bool("list", false, "list maintained middleware presets")
	dryRun := fs.Bool("dry-run", false, "plan preset files without writing")
	jsonOutput := registerCLIJSONOutputFlag(fs, "emit preset/catalog results and errors as JSON")
	jsonRequested := middlewareJSONOutputRequested(fs, args)
	defer func() {
		if err == nil || !jsonRequested || outputMode() == outputJSON || ErrorAlreadyReported(err) {
			return
		}
		if reportErr := printJSONError("api.middleware", err); reportErr != nil {
			err = errors.Join(err, reportErr)
			return
		}
		err = errors.Join(errJSONAlreadyReported, err)
	}()
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

// Resolve output intent before parsing so even a preceding invalid flag can
// produce JSON. Values consumed by known string flags are never output flags.
func middlewareJSONOutputRequested(fs *flag.FlagSet, args []string) bool {
	requested := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name := flagName(arg)
		_, value, inline := strings.Cut(arg, "=")
		if name == "json" {
			if !inline {
				requested = true
			} else if parsed, err := strconv.ParseBool(value); err == nil {
				requested = parsed
			}
			continue
		}
		if f := fs.Lookup(name); f != nil && !inline && !isBoolFlag(f) {
			i++
		}
	}
	return requested
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
