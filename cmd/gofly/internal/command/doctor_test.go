package command

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorRun_AllChecksPresent(t *testing.T) {
	report := runDoctor()
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

func TestDoctorRun_Summary(t *testing.T) {
	report := runDoctor()
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
	c := checkGoModule()
	if c.Name != "Go modules" {
		t.Errorf("name = %q, want Go modules", c.Name)
	}
	// We cannot universally assert ok or fail because GO111MODULE may vary.
	if c.Status != "ok" && c.Status != "fail" {
		t.Errorf("unexpected status %q", c.Status)
	}
}

func TestCheckWritePermission(t *testing.T) {
	c := checkWritePermission()
	if c.Name != "Write permission" {
		t.Errorf("name = %q, want Write permission", c.Name)
	}
	if c.Status != "ok" && c.Status != "fail" {
		t.Errorf("unexpected status %q", c.Status)
	}
}

func TestDoctorCommandJSON(t *testing.T) {
	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return doctorCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("doctor --json: %v", err)
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("doctor --json decode: %v\n%s", err, out.String())
	}
	if len(report.NextActions) == 0 {
		t.Fatalf("doctor --json nextActions = %#v, want troubleshooting next action guidance", report.NextActions)
	}
}

func TestDoctorCommandHelp(t *testing.T) {
	if err := doctorCommand([]string{"--help"}); err != nil {
		t.Fatalf("doctor --help: %v", err)
	}
}

func TestPrintDoctorReportWithAllStatuses(t *testing.T) {
	report := doctorReport{
		Version: "v0.1.0",
		Go:      "go1.26",
		OS:      "linux",
		Arch:    "amd64",
		Checks: []doctorCheck{
			{Name: "ok-check", Status: "ok"},
			{Name: "warn-check", Status: "warn", Message: "warning msg", FixHint: "fix it"},
			{Name: "fail-check", Status: "fail", Message: "fail msg", FixHint: "fix it"},
		},
		Summary: "2 warning(s), 1 fail(s)",
	}
	printDoctorReport(report)
}

func TestCheckGoVersionBranches(t *testing.T) {
	// We cannot change runtime.Version(), but we can verify the function
	// returns a valid check struct for the current runtime.
	c := checkGoVersion()
	if c.Name != "Go version" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "warn" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestCheckGOPATH(t *testing.T) {
	c := checkGOPATH()
	if c.Name != "GOPATH" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "warn" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestCheckTools(t *testing.T) {
	c := checkTools()
	if c.Name != "Core tools" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "fail" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestCheckGit(t *testing.T) {
	c := checkGit()
	if c.Name != "Git" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "warn" && c.Status != "fail" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestCheckProtoc(t *testing.T) {
	c := checkProtoc()
	if c.Name != "protoc" {
		t.Fatalf("name = %q", c.Name)
	}
	if c.Status != "ok" && c.Status != "warn" {
		t.Fatalf("unexpected status %q", c.Status)
	}
}

func TestDoctorNextActionsContract(t *testing.T) {
	checks := []doctorCheck{
		{Name: "Go modules", Status: "fail", FixHint: "unset GO111MODULE", NextActions: []string{"unset GO111MODULE or set GO111MODULE=on"}},
		{Name: "protoc", Status: "warn", FixHint: "install protoc", NextActions: []string{"install protoc before running standard protobuf/gRPC generation"}},
	}
	actions := doctorNextActions(checks, 1, 1)
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

	healthy := doctorNextActions(nil, 0, 0)
	if !containsDoctorAction(healthy, "run `gofly release check --json --strict` before publishing") || !containsDoctorAction(healthy, "run `make governance-10-rounds` for full repository governance") {
		t.Fatalf("healthy doctorNextActions = %#v, want release and governance next actions", healthy)
	}
}

func TestBugCommandSupportBundleJSONContract(t *testing.T) {
	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return bugCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("bug --json: %v", err)
	}
	var report bugReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("bug --json decode: %v\n%s", err, out.String())
	}
	if report.SupportBundle.Schema != "gofly.support_bundle.v1" || len(report.SupportBundle.Commands) == 0 || len(report.SupportBundle.Redaction) == 0 {
		t.Fatalf("support bundle = %#v, want schema, commands, and redaction policy", report.SupportBundle)
	}
	if !containsDoctorAction(report.NextActions, "attach this support bundle when opening an issue or asking for help") {
		t.Fatalf("bug nextActions = %#v, want support bundle guidance", report.NextActions)
	}
	if !strings.Contains(strings.Join(report.SupportBundle.Commands, "\n"), "gofly doctor --json") {
		t.Fatalf("support bundle commands = %#v, want doctor command", report.SupportBundle.Commands)
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
