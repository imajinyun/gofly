package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// featureCommand 暴露 `gofly feature list` 和 `gofly feature run`。
// `run` 用于开发者测试某个已注册的 feature 对特定目录作用（不写文件，打印会生成的文件列表）。
func featureCommand(args []string) error {
	if printCommandHelp("feature", args) {
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("%w: expected `gofly feature list|run`", errUsage)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list", "ls":
		fs := flag.NewFlagSet("feature list", flag.ContinueOnError)
		formatName := fs.String("format", "text", "output format: text or json")
		jsonOutput := fs.Bool("json", false, "output JSON")
		if _, err := parseInterspersedFlags(fs, rest); err != nil {
			return err
		}
		names := generator.ListFeatures()
		if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
			return printJSONEnvelope("feature.list", featureListPreview{Features: names})
		}
		if len(names) == 0 {
			cliOutputlnIf("(no registered features)")
			return nil
		}
		for _, n := range names {
			cliOutputlnIf(n)
		}
		return nil
	case "run":
		fs := flag.NewFlagSet("feature run", flag.ContinueOnError)
		name := fs.String("name", "", "service name")
		module := fs.String("module", "", "module path")
		dir := fs.String("dir", ".", "service directory")
		style := fs.String("style", "basic", "service style")
		featureFlag := fs.String("feature", "", "feature names to enable, comma-separated")
		featuresFlag := fs.String("features", "", "alias for --feature")
		formatName := fs.String("format", "text", "output format: text or json")
		jsonOutput := fs.Bool("json", false, "output JSON")
		feature := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			feature = rest[0]
			rest = rest[1:]
		}
		remaining, err := parseInterspersedFlags(fs, rest)
		if err != nil {
			return err
		}
		if feature == "" && len(remaining) > 0 {
			feature = remaining[0]
			remaining = remaining[1:]
		}
		featureNames := splitCSV(joinCSV(feature, strings.Join(remaining, ","), *featureFlag, *featuresFlag))
		if len(featureNames) == 0 {
			err := fmt.Errorf("%w: expected `gofly feature run <feature-name>`", errUsage)
			if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
				_ = printJSONError("feature.run", err)
			}
			return err
		}
		if err := generator.ValidateFeatureNames(featureNames); err != nil {
			if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
				_ = printJSONError("feature.run", err)
			}
			return err
		}
		scope := generator.ExtensionScope{
			Name:   *name,
			Module: *module,
			Style:  *style,
			Dir:    *dir,
			Data:   map[string]string{"Name": *name, "Module": *module},
		}
		files, data, err := generator.ApplyFeatureNames(featureNames, scope, map[string]string{}, map[string]string{})
		if err != nil {
			return err
		}
		preview := buildFeatureRunPreview(featureNames, files, data)
		if *jsonOutput || strings.EqualFold(strings.TrimSpace(*formatName), "json") {
			return printJSONEnvelope("feature.run", preview)
		}
		for _, file := range preview.Files {
			cliOutputfIf("# file: %s (%d bytes)\n", file.Path, file.Bytes)
		}
		if len(preview.Data) > 0 {
			cliOutputlnIf("# data:")
			for _, item := range preview.Data {
				cliOutputfIf("  %s = %s\n", item.Key, item.Value)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: expected `gofly feature list|run`", errUsage)
	}
}
