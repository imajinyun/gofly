package command

import (
	"flag"
	"fmt"
	"strings"
)

type cliOutputFlags struct {
	Format *string
	JSON   *bool
}

type cliOutputFlagOptions struct {
	DefaultFormat string
	FormatUsage   string
	JSONUsage     string
}

func registerCLIOutputFlags(fs *flag.FlagSet, opts cliOutputFlagOptions) cliOutputFlags {
	defaultFormat := opts.DefaultFormat
	if defaultFormat == "" {
		defaultFormat = outputText
	}
	formatUsage := opts.FormatUsage
	if formatUsage == "" {
		formatUsage = "output format: text or json"
	}
	jsonUsage := opts.JSONUsage
	if jsonUsage == "" {
		jsonUsage = "output JSON"
	}
	return cliOutputFlags{
		Format: fs.String("format", defaultFormat, formatUsage),
		JSON:   fs.Bool("json", false, jsonUsage),
	}
}

func registerCLIJSONOutputFlag(fs *flag.FlagSet, usage string) *bool {
	if usage == "" {
		usage = "output JSON"
	}
	return fs.Bool("json", false, usage)
}

type docOutputFlags struct {
	Format *string
	YAML   *bool
	JSON   *bool
}

type docOutputFlagOptions struct {
	DefaultFormat string
	FormatUsage   string
	YAMLUsage     string
	JSONUsage     string
}

func registerDocOutputFlags(fs *flag.FlagSet, opts docOutputFlagOptions) docOutputFlags {
	formatUsage := opts.FormatUsage
	if formatUsage == "" {
		formatUsage = "doc format: openapi/json, yaml, or markdown"
	}
	yamlUsage := opts.YAMLUsage
	if yamlUsage == "" {
		yamlUsage = "write output as yaml"
	}
	jsonUsage := opts.JSONUsage
	if jsonUsage == "" {
		jsonUsage = "write output as json"
	}
	return docOutputFlags{
		Format: fs.String("format", opts.DefaultFormat, formatUsage),
		YAML:   fs.Bool("yaml", false, yamlUsage),
		JSON:   fs.Bool("json", false, jsonUsage),
	}
}

func (f docOutputFlags) applyFormatAliases(jsonFormat string) {
	if valueFromBoolFlag(f.YAML) {
		setStringFlag(f.Format, "yaml")
	}
	if valueFromBoolFlag(f.JSON) {
		setStringFlag(f.Format, jsonFormat)
	}
}

func (f cliOutputFlags) normalizedFormat(fallback string) (string, error) {
	if fallback == "" {
		fallback = outputText
	}
	format := strings.ToLower(strings.TrimSpace(valueFromStringFlag(f.Format)))
	if format == "" {
		format = fallback
	}
	if format != outputText && format != outputJSON {
		return "", fmt.Errorf("%w: unsupported --format %q", errUsage, valueFromStringFlag(f.Format))
	}
	return format, nil
}

func (f cliOutputFlags) useJSON(format string) bool {
	return valueFromBoolFlag(f.JSON) || outputMode() == outputJSON || format == outputJSON
}
