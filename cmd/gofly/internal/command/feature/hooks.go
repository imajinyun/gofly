package feature

import (
	"errors"
	"flag"
	"fmt"
)

// Hooks inject command-package collaborators so the feature family can live in its own package.
type Hooks struct {
	PrintHelp           func(command string, args []string) bool
	ParseFlags          func(fs *flag.FlagSet, args []string) ([]string, error)
	RegisterOutputFlags func(fs *flag.FlagSet) OutputFlags
	UseJSON             func(flags OutputFlags) bool
	PrintJSONEnvelope   func(command string, value any) error
	PrintJSONError      func(command string, err error) error
	PrintTextlnIf       func(args ...any)
	PrintTextfIf        func(format string, args ...any)
	Usage               error
}

type OutputFlags struct {
	Format *string
	JSON   *bool
}

func normalizeHooks(hooks Hooks) Hooks {
	if hooks.PrintHelp == nil {
		hooks.PrintHelp = func(string, []string) bool { return false }
	}
	if hooks.ParseFlags == nil {
		hooks.ParseFlags = func(fs *flag.FlagSet, args []string) ([]string, error) {
			if err := fs.Parse(args); err != nil {
				return nil, err
			}
			return fs.Args(), nil
		}
	}
	if hooks.RegisterOutputFlags == nil {
		hooks.RegisterOutputFlags = func(fs *flag.FlagSet) OutputFlags {
			return OutputFlags{Format: fs.String("format", "text", "output format: text or json"), JSON: fs.Bool("json", false, "output JSON")}
		}
	}
	if hooks.UseJSON == nil {
		hooks.UseJSON = func(flags OutputFlags) bool { return flags.JSON != nil && *flags.JSON }
	}
	if hooks.PrintJSONEnvelope == nil {
		hooks.PrintJSONEnvelope = func(string, any) error { return nil }
	}
	if hooks.PrintJSONError == nil {
		hooks.PrintJSONError = func(command string, err error) error { return fmt.Errorf("%s: %w", command, err) }
	}
	if hooks.PrintTextlnIf == nil {
		hooks.PrintTextlnIf = func(args ...any) { fmt.Println(args...) }
	}
	if hooks.PrintTextfIf == nil {
		hooks.PrintTextfIf = func(format string, args ...any) { fmt.Printf(format, args...) }
	}
	if hooks.Usage == nil {
		hooks.Usage = errors.New("invalid usage")
	}
	return hooks
}
