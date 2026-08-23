package release

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
)

// Hooks injects command-package collaborators so the release family can live
// in its own package without importing command.
type Hooks struct {
	PrintHelp       func(command string, args []string) bool
	ParseFlags      func(fs *flag.FlagSet, args []string) ([]string, error)
	PrintJSON       func(value any) error
	PrintText       func(args ...any)
	PrintTextf      func(format string, args ...any)
	PrintTextln     func(args ...any)
	Version         string
	OutputJSON      func() bool
	AlreadyReported error
}

type jsonEnvelope struct {
	OK      bool       `json:"ok"`
	Command string     `json:"command"`
	Version string     `json:"version"`
	Data    any        `json:"data,omitempty"`
	Error   *jsonError `json:"error,omitempty"`
}

type jsonError struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Retryable   bool           `json:"retryable"`
	Remediation string         `json:"remediation,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
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
	if hooks.PrintJSON == nil {
		hooks.PrintJSON = func(value any) error {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal json: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}
	}
	if hooks.PrintText == nil {
		hooks.PrintText = func(args ...any) { fmt.Print(args...) }
	}
	if hooks.PrintTextf == nil {
		hooks.PrintTextf = func(format string, args ...any) { fmt.Printf(format, args...) }
	}
	if hooks.PrintTextln == nil {
		hooks.PrintTextln = func(args ...any) { fmt.Println(args...) }
	}
	if hooks.OutputJSON == nil {
		hooks.OutputJSON = func() bool { return false }
	}
	if hooks.AlreadyReported == nil {
		hooks.AlreadyReported = errors.New("json error already reported")
	}
	return hooks
}
