package feature

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

func Command(args []string, hooks Hooks) error {
	hooks = normalizeHooks(hooks)
	if hooks.PrintHelp("feature", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly feature list|run`", hooks.Usage)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		fs := flag.NewFlagSet("feature list", flag.ContinueOnError)
		outputFlags := hooks.RegisterOutputFlags(fs)
		if _, err := hooks.ParseFlags(fs, rest); err != nil {
			return err
		}
		names := generator.ListFeatures()
		if hooks.UseJSON(outputFlags) {
			return hooks.PrintJSONEnvelope("feature.list", featureListPreview{Features: names})
		}
		if len(names) == 0 {
			hooks.PrintTextlnIf("(no registered features)")
			return nil
		}
		for _, name := range names {
			hooks.PrintTextlnIf(name)
		}
		return nil
	case "run":
		fs := flag.NewFlagSet("feature run", flag.ContinueOnError)
		name := fs.String("name", "", "service name")
		module := fs.String("module", "", "module path")
		dir := fs.String("dir", ".", "service directory")
		style := fs.String("style", "basic", "service style")
		featureFlag := fs.String("feature", "", "feature names to enable, comma-separated")
		featuresFlag := fs.String("features", "", "alias for feature")
		outputFlags := hooks.RegisterOutputFlags(fs)
		featureName := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			featureName, rest = rest[0], rest[1:]
		}
		remaining, err := hooks.ParseFlags(fs, rest)
		if err != nil {
			return err
		}
		if featureName == "" && len(remaining) > 0 {
			featureName, remaining = remaining[0], remaining[1:]
		}
		featureNames := splitCSV(joinCSV(featureName, strings.Join(remaining, ","), *featureFlag, *featuresFlag))
		useJSON := hooks.UseJSON(outputFlags)
		if len(featureNames) == 0 {
			err := fmt.Errorf("%w: expected `gofly feature run <feature-name>`", hooks.Usage)
			if useJSON {
				_ = hooks.PrintJSONError("feature.run", err)
			}
			return err
		}
		if err := generator.ValidateFeatureNames(featureNames); err != nil {
			if useJSON {
				_ = hooks.PrintJSONError("feature.run", err)
			}
			return err
		}
		scope := generator.ExtensionScope{Name: *name, Module: *module, Style: *style, Dir: *dir, Data: map[string]string{"Name": *name, "Module": *module}}
		files, data, err := generator.ApplyFeatureNames(featureNames, scope, map[string]string{}, map[string]string{})
		if err != nil {
			return err
		}
		preview := buildFeatureRunPreview(featureNames, files, data)
		if useJSON {
			return hooks.PrintJSONEnvelope("feature.run", preview)
		}
		for _, file := range preview.Files {
			hooks.PrintTextfIf("# file: %s (%d bytes)\n", file.Path, file.Bytes)
		}
		if len(preview.Data) > 0 {
			hooks.PrintTextlnIf("# data:")
			for _, item := range preview.Data {
				hooks.PrintTextfIf("  %s = %s\n", item.Key, item.Value)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: expected `gofly feature list|run`", hooks.Usage)
	}
}
