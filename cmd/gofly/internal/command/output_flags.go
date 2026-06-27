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
