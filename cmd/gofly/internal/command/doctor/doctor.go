package doctor

import (
	"flag"
	"fmt"
	"runtime"
)

type Hooks struct {
	PrintHelp     func(command string, args []string) bool
	ParseFlags    func(fs *flag.FlagSet, args []string) ([]string, error)
	PrintJSON     func(value any) error
	PrintTextf    func(format string, args ...any)
	PrintTextln   func(args ...any)
	Version       string
	AppendMissing func(values []string, additions ...string) []string
}

// Check represents a single diagnostic check result.
type Check struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"` // ok, warn, fail
	Message     string   `json:"message,omitempty"`
	FixHint     string   `json:"fix_hint,omitempty"`
	NextActions []string `json:"nextActions,omitempty"`
}

// Report aggregates all diagnostic checks.
type Report struct {
	Version     string   `json:"version"`
	Go          string   `json:"go"`
	OS          string   `json:"os"`
	Arch        string   `json:"arch"`
	Checks      []Check  `json:"checks"`
	Summary     string   `json:"summary"`
	NextActions []string `json:"nextActions"`
}

func Command(args []string, hooks Hooks) error {
	hooks = normalizeHooks(hooks)
	if hooks.PrintHelp("doctor", args) {
		return nil
	}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print report as JSON")
	if _, err := hooks.ParseFlags(fs, args); err != nil {
		return err
	}

	report := Run(hooks.Version, hooks.AppendMissing)
	if *jsonOutput {
		return hooks.PrintJSON(report)
	}
	PrintReport(report, hooks.PrintTextf, hooks.PrintTextln)
	return nil
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
		hooks.PrintJSON = func(any) error { return nil }
	}
	if hooks.PrintTextf == nil {
		hooks.PrintTextf = func(string, ...any) {}
	}
	if hooks.PrintTextln == nil {
		hooks.PrintTextln = func(...any) {}
	}
	if hooks.AppendMissing == nil {
		hooks.AppendMissing = appendMissingStrings
	}
	return hooks
}

func Run(version string, appendMissing func([]string, ...string) []string) Report {
	if appendMissing == nil {
		appendMissing = appendMissingStrings
	}
	checks := []Check{
		CheckGoVersion(),
		CheckGoModule(),
		CheckGOPATH(),
		CheckTools(),
		CheckGit(),
		CheckProtoc(),
		CheckWritePermission(),
	}

	var warns, fails int
	for _, c := range checks {
		switch c.Status {
		case "warn":
			warns++
		case "fail":
			fails++
		}
	}

	summary := "all checks passed"
	if fails > 0 {
		summary = fmt.Sprintf("%d check(s) failed, %d warning(s)", fails, warns)
	} else if warns > 0 {
		summary = fmt.Sprintf("%d warning(s)", warns)
	}

	return Report{
		Version:     version,
		Go:          runtime.Version(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Checks:      checks,
		Summary:     summary,
		NextActions: NextActions(checks, fails, warns, appendMissing),
	}
}

func PrintReport(r Report, printf func(string, ...any), println func(...any)) {
	if printf == nil {
		printf = func(string, ...any) {}
	}
	if println == nil {
		println = func(...any) {}
	}
	printf("gofly doctor %s\n", r.Version)
	printf("go: %s  os: %s/%s\n\n", r.Go, r.OS, r.Arch)
	for _, c := range r.Checks {
		switch c.Status {
		case "ok":
			printf("  \033[92m[OK]\033[0m   %s", c.Name)
		case "warn":
			printf("  \033[93m[WARN]\033[0m %s: %s", c.Name, c.Message)
		case "fail":
			printf("  \033[91m[FAIL]\033[0m %s: %s", c.Name, c.Message)
		}
		if c.FixHint != "" {
			printf("\n         \033[90m-> %s\033[0m", c.FixHint)
		}
		println()
	}
	printf("\n%s\n", r.Summary)
}

func NextActions(checks []Check, fails int, warns int, appendMissing func([]string, ...string) []string) []string {
	if appendMissing == nil {
		appendMissing = appendMissingStrings
	}
	var actions []string
	for _, check := range checks {
		if check.Status == "ok" {
			continue
		}
		actions = appendMissing(actions, check.NextActions...)
		if check.FixHint != "" {
			actions = appendMissing(actions, check.FixHint)
		}
	}
	switch {
	case fails > 0:
		actions = appendMissing(actions,
			"fix failed doctor checks before generating or releasing services",
			"run `gofly bug --json` to collect a support bundle for issue reports",
		)
	case warns > 0:
		actions = appendMissing(actions,
			"review warning checks before running release gates",
			"run `gofly release check --json --strict` before publishing",
		)
	default:
		actions = appendMissing(actions,
			"run `gofly release check --json --strict` before publishing",
			"run `make governance-10-rounds` for full repository governance",
		)
	}
	return actions
}

func appendMissingStrings(values []string, additions ...string) []string {
	for _, addition := range additions {
		if addition == "" {
			continue
		}
		found := false
		for _, value := range values {
			if value == addition {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
}
