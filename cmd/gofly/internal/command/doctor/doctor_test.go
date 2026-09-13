package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorRunAllChecksPresent(t *testing.T) {
	report := Run("test-version", nil)
	if report.Version == "" {
		t.Error("expected Version to be set")
	}
	if report.Go != runtime.Version() {
		t.Errorf("Go version = %q, want %q", report.Go, runtime.Version())
	}
	if len(report.Checks) == 0 {
		t.Fatal("expected at least one check")
	}

	checkNames := map[string]bool{}
	for _, c := range report.Checks {
		checkNames[c.Name] = true
		if c.Status != "ok" && c.Status != "warn" && c.Status != "fail" {
			t.Errorf("check %q has invalid status %q", c.Name, c.Status)
		}
	}

	required := []string{"Go version", "Go modules", "GOPATH", "Core tools", "Git", "protoc", "Write permission"}
	for _, name := range required {
		if !checkNames[name] {
			t.Errorf("missing required check %q", name)
		}
	}
}

func TestDoctorRunSummary(t *testing.T) {
	report := Run("test-version", nil)
	if report.Summary == "" {
		t.Error("expected non-empty summary")
	}
	// In a healthy dev environment most checks should be ok.
	hasFail := false
	for _, c := range report.Checks {
		if c.Status == "fail" {
			hasFail = true
			break
		}
	}
	if hasFail && report.Summary == "all checks passed" {
		t.Error("summary says all passed but there are failures")
	}
}

func TestCheckGoModule(t *testing.T) {
	c := CheckGoModule()
	if c.Name != "Go modules" {
		t.Errorf("name = %q, want Go modules", c.Name)
	}
	// We cannot universally assert ok or fail because GO111MODULE may vary.
	if c.Status != "ok" && c.Status != "fail" {
		t.Errorf("unexpected status %q", c.Status)
	}
}

func TestCheckWritePermission(t *testing.T) {
	c := CheckWritePermission()
	if c.Name != "Write permission" {
		t.Errorf("name = %q, want Write permission", c.Name)
	}
	if c.Status != "ok" && c.Status != "fail" {
		t.Errorf("unexpected status %q", c.Status)
	}
}

func TestDoctorCommandJSON(t *testing.T) {
	var out bytes.Buffer
	if err := Command([]string{"--json"}, testHooks(&out)); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var report Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("doctor --json decode: %v\n%s", err, out.String())
	}
	if len(report.NextActions) == 0 {
		t.Fatalf("doctor --json nextActions = %#v, want troubleshooting next action guidance", report.NextActions)
	}
}

func TestDoctorCommandHelp(t *testing.T) {
	called := false
	hooks := testHooks(nil)
	hooks.PrintHelp = func(command string, args []string) bool {
		called = command == "doctor" && len(args) == 1 && args[0] == "--help"
		return called
	}
	if err := Command([]string{"--help"}, hooks); err != nil {
		t.Fatalf("doctor --help: %v", err)
	}
	if !called {
		t.Fatal("doctor --help did not route through help hook")
	}
}

func TestPrintDoctorReportWithAllStatuses(t *testing.T) {
	report := Report{
		Version: "v0.1.0",
		Go:      "go1.26",
		OS:      "linux",
		Arch:    "amd64",
		Checks: []Check{
			{Name: "ok-check", Status: "ok"},
			{Name: "warn-check", Status: "warn", Message: "warning msg", FixHint: "fix it"},
			{Name: "fail-check", Status: "fail", Message: "fail msg", FixHint: "fix it"},
		},
		Summary: "2 warning(s), 1 fail(s)",
	}
	PrintReport(report, func(string, ...any) {}, func(...any) {})
}

func TestCheckGoVersionBranches(t *testing.T) {
	// We cannot change runtime.Version(), but we can verify the function
	// returns a valid check struct for the current runtime.
	c := CheckGoVersion()
	if c.Name != "Go version" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "warn" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestCheckGOPATH(t *testing.T) {
	c := CheckGOPATH()
	if c.Name != "GOPATH" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "warn" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestCheckTools(t *testing.T) {
	c := CheckTools()
	if c.Name != "Core tools" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "fail" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestCheckGit(t *testing.T) {
	c := CheckGit()
	if c.Name != "Git" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "warn" && c.Status != "fail" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestCheckProtoc(t *testing.T) {
	c := CheckProtoc()
	if c.Name != "protoc" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "warn" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestDoctorNextActionsContract(t *testing.T) {
	checks := []Check{
		{Name: "Go modules", Status: "fail", FixHint: "unset GO111MODULE", NextActions: []string{"unset GO111MODULE or set GO111MODULE=on"}},
		{Name: "protoc", Status: "warn", FixHint: "install protoc", NextActions: []string{"install protoc before running standard protobuf/gRPC generation"}},
	}
	actions := NextActions(checks, 1, 1, nil)
	for _, want := range []string{
		"unset GO111MODULE or set GO111MODULE=on",
		"install protoc before running standard protobuf/gRPC generation",
		"fix failed doctor checks before generating or releasing services",
		"run `gofly bug --json` to collect a support bundle for issue reports",
	} {
		if !containsDoctorAction(actions, want) {
			t.Fatalf("doctorNextActions = %#v, want %q", actions, want)
		}
	}

	healthy := NextActions(nil, 0, 0, nil)
	if !containsDoctorAction(healthy, "run `gofly release check --json --strict` before publishing") || !containsDoctorAction(healthy, "run `make governance-10-rounds` for full repository governance") {
		t.Fatalf("healthy doctorNextActions = %#v, want release and governance next actions", healthy)
	}
}

func containsDoctorAction(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestDoctorIsolatedEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		installed, executable        bool
		modules                      string
		wantGit, wantProtoc, summary string
	}{
		{"missing tools", false, false, "off", "fail", "warn", "3 check(s) failed, 1 warning(s)"},
		{"broken tools", true, false, "on", "warn", "warn", "2 warning(s)"},
		{"healthy", true, true, "on", "ok", "ok", "all checks passed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			t.Setenv("GO111MODULE", tc.modules)
			t.Setenv("GOPATH", dir)
			t.Setenv("TMPDIR", dir)
			if tc.installed {
				for _, name := range []string{"go", "git", "protoc"} {
					body := "#!/bin/sh\nexit 1\n"
					if tc.executable {
						body = "#!/bin/sh\nprintf 'test-version\\n'\n"
					}
					if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			report := Run("test", nil)
			if report.Summary != tc.summary {
				t.Fatalf("summary=%q want=%q checks=%+v", report.Summary, tc.summary, report.Checks)
			}
			if got := CheckGit(); got.Status != tc.wantGit {
				t.Fatalf("git=%+v", got)
			}
			if got := CheckProtoc(); got.Status != tc.wantProtoc {
				t.Fatalf("protoc=%+v", got)
			}
			if err := Command(nil, Hooks{}); err != nil {
				t.Fatal(err)
			}
			if err := Command([]string{"--json"}, Hooks{}); err != nil {
				t.Fatal(err)
			}
			want := errors.New("json output failure")
			if err := Command([]string{"--json"}, Hooks{PrintJSON: func(any) error { return want }}); !errors.Is(err, want) {
				t.Fatalf("output err=%v", err)
			}
		})
	}
	t.Run("unwritable temp root", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "file")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", path)
		if got := CheckWritePermission(); got.Status != "fail" || !strings.Contains(got.Message, "cannot write") {
			t.Fatalf("write check=%+v", got)
		}
	})
	t.Run("missing gopath default", func(t *testing.T) {
		previous := build.Default.GOPATH
		t.Cleanup(func() { build.Default.GOPATH = previous })
		build.Default.GOPATH = ""
		t.Setenv("GOPATH", "")
		if got := CheckGOPATH(); got.Status != "warn" {
			t.Fatalf("gopath=%+v", got)
		}
	})
	t.Run("invalid flag", func(t *testing.T) {
		if err := Command([]string{"--unknown"}, Hooks{}); err == nil {
			t.Fatal("unknown flag accepted")
		}
	})
	t.Run("nil printers", func(t *testing.T) { PrintReport(Report{Summary: "empty"}, nil, nil) })
	t.Run("deduplicated actions", func(t *testing.T) {
		got := appendMissingStrings([]string{"a"}, "", "a", "b")
		if !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Fatalf("actions=%v", got)
		}
	})
}

func testHooks(out *bytes.Buffer) Hooks {
	if out == nil {
		out = &bytes.Buffer{}
	}
	return Hooks{
		PrintHelp: func(string, []string) bool { return false },
		PrintJSON: func(value any) error {
			return json.NewEncoder(out).Encode(value)
		},
		PrintTextf: func(format string, args ...any) {
			_, _ = out.WriteString(format)
		},
		PrintTextln: func(...any) {
			_, _ = out.WriteString("\n")
		},
		Version: "test-version",
	}
}
