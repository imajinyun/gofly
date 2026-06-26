package command

import (
	"flag"
	"fmt"
	"runtime"
)

// doctorCheck represents a single diagnostic check result.
type doctorCheck struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"` // ok, warn, fail
	Message     string   `json:"message,omitempty"`
	FixHint     string   `json:"fix_hint,omitempty"`
	NextActions []string `json:"nextActions,omitempty"`
}

// doctorReport aggregates all diagnostic checks.
type doctorReport struct {
	Version     string        `json:"version"`
	Go          string        `json:"go"`
	OS          string        `json:"os"`
	Arch        string        `json:"arch"`
	Checks      []doctorCheck `json:"checks"`
	Summary     string        `json:"summary"`
	NextActions []string      `json:"nextActions"`
}

func doctorCommand(args []string) error {
	if printCommandHelp("doctor", args) {
		return nil
	}
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print report as JSON")
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		return err
	}

	report := runDoctor()
	if *jsonOutput {
		return printJSON(report)
	}
	printDoctorReport(report)
	return nil
}

func runDoctor() doctorReport {
	checks := []doctorCheck{
		checkGoVersion(),
		checkGoModule(),
		checkGOPATH(),
		checkTools(),
		checkGit(),
		checkProtoc(),
		checkWritePermission(),
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

	return doctorReport{
		Version:     Version,
		Go:          runtime.Version(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Checks:      checks,
		Summary:     summary,
		NextActions: doctorNextActions(checks, fails, warns),
	}
}

func printDoctorReport(r doctorReport) {
	cliOutputf("gofly doctor %s\n", r.Version)
	cliOutputf("go: %s  os: %s/%s\n\n", r.Go, r.OS, r.Arch)
	for _, c := range r.Checks {
		switch c.Status {
		case "ok":
			cliOutputf("  \033[92m[OK]\033[0m   %s", c.Name)
		case "warn":
			cliOutputf("  \033[93m[WARN]\033[0m %s: %s", c.Name, c.Message)
		case "fail":
			cliOutputf("  \033[91m[FAIL]\033[0m %s: %s", c.Name, c.Message)
		}
		if c.FixHint != "" {
			cliOutputf("\n         \033[90m-> %s\033[0m", c.FixHint)
		}
		cliOutputln()
	}
	cliOutputf("\n%s\n", r.Summary)
}

func doctorNextActions(checks []doctorCheck, fails int, warns int) []string {
	var actions []string
	for _, check := range checks {
		if check.Status == "ok" {
			continue
		}
		actions = appendMissingStrings(actions, check.NextActions...)
		if check.FixHint != "" {
			actions = appendMissingStrings(actions, check.FixHint)
		}
	}
	switch {
	case fails > 0:
		actions = appendMissingStrings(actions,
			"fix failed doctor checks before generating or releasing services",
			"run `gofly bug --json` to collect a support bundle for issue reports",
		)
	case warns > 0:
		actions = appendMissingStrings(actions,
			"review warning checks before running release gates",
			"run `gofly release check --json --strict` before publishing",
		)
	default:
		actions = appendMissingStrings(actions,
			"run `gofly release check --json --strict` before publishing",
			"run `make governance-10-rounds` for full repository governance",
		)
	}
	return actions
}
