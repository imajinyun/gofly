package command

import (
	"flag"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// doctorCheck represents a single diagnostic check result.
type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok, warn, fail
	Message string `json:"message,omitempty"`
	FixHint string `json:"fix_hint,omitempty"`
}

// doctorReport aggregates all diagnostic checks.
type doctorReport struct {
	Version string        `json:"version"`
	Go      string        `json:"go"`
	OS      string        `json:"os"`
	Arch    string        `json:"arch"`
	Checks  []doctorCheck `json:"checks"`
	Summary string        `json:"summary"`
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
		Version: Version,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Checks:  checks,
		Summary: summary,
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

func checkGoVersion() doctorCheck {
	v := runtime.Version()
	if strings.HasPrefix(v, "go1.24") || strings.HasPrefix(v, "go1.25") || strings.HasPrefix(v, "go1.26") {
		return doctorCheck{Name: "Go version", Status: "ok"}
	}
	if strings.HasPrefix(v, "go1.23") {
		return doctorCheck{Name: "Go version", Status: "warn", Message: "Go 1.23 detected; gofly recommends 1.24+", FixHint: "upgrade Go to 1.24 or later"}
	}
	return doctorCheck{Name: "Go version", Status: "warn", Message: v + " may not support go.mod tool directives", FixHint: "upgrade Go to 1.24 or later"}
}

func checkGoModule() doctorCheck {
	if os.Getenv("GO111MODULE") == "off" {
		return doctorCheck{Name: "Go modules", Status: "fail", Message: "GO111MODULE=off", FixHint: "unset GO111MODULE or set it to on"}
	}
	return doctorCheck{Name: "Go modules", Status: "ok"}
}

func checkGOPATH() doctorCheck {
	gp := os.Getenv("GOPATH")
	if gp == "" {
		gp = build.Default.GOPATH
	}
	if gp == "" {
		return doctorCheck{Name: "GOPATH", Status: "warn", Message: "not set and default is empty", FixHint: "export GOPATH=$HOME/go"}
	}
	return doctorCheck{Name: "GOPATH", Status: "ok"}
}

func checkTools() doctorCheck {
	missing := []string{}
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		return doctorCheck{Name: "Core tools", Status: "fail", Message: "missing: " + strings.Join(missing, ", "), FixHint: "install missing tools via system package manager"}
	}
	return doctorCheck{Name: "Core tools", Status: "ok"}
}

func checkGit() doctorCheck {
	if _, err := exec.LookPath("git"); err != nil {
		return doctorCheck{Name: "Git", Status: "fail", Message: "not found in PATH", FixHint: "install git"}
	}
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		return doctorCheck{Name: "Git", Status: "warn", Message: "found but version check failed"}
	}
	v := strings.TrimSpace(string(out))
	return doctorCheck{Name: "Git", Status: "ok", Message: v}
}

func checkProtoc() doctorCheck {
	if _, err := exec.LookPath("protoc"); err != nil {
		return doctorCheck{Name: "protoc", Status: "warn", Message: "not found in PATH", FixHint: "install protoc (see https://grpc.io/docs/protoc-installation/)"}
	}
	out, err := exec.Command("protoc", "--version").Output()
	if err != nil {
		return doctorCheck{Name: "protoc", Status: "warn", Message: "found but version check failed"}
	}
	return doctorCheck{Name: "protoc", Status: "ok", Message: strings.TrimSpace(string(out))}
}

func checkWritePermission() doctorCheck {
	tmpDir := os.TempDir()
	f, err := os.CreateTemp(tmpDir, "gofly-doctor-*")
	if err != nil {
		return doctorCheck{Name: "Write permission", Status: "fail", Message: "cannot write to " + tmpDir, FixHint: "check TMPDIR / temp directory permissions"}
	}
	if err := f.Close(); err != nil {
		return doctorCheck{Name: "Write permission", Status: "fail", Message: "cannot close temp file in " + tmpDir, FixHint: "check TMPDIR / temp directory permissions"}
	}
	if err := os.Remove(f.Name()); err != nil {
		return doctorCheck{Name: "Write permission", Status: "warn", Message: "temp file cleanup failed: " + err.Error(), FixHint: "check TMPDIR / temp directory permissions"}
	}
	return doctorCheck{Name: "Write permission", Status: "ok"}
}
