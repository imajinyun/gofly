package command

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imajinyun/gofly/gateway"
)

func TestGatewayTextOutputContracts(t *testing.T) {
	tests := []struct {
		name   string
		print  func()
		wants  []string
		absent string
	}{
		{
			name: "profile compatible",
			print: func() {
				printGatewayProfileValidationText(gateway.TranscodeProfileValidationReport{
					OK:         true,
					Compatible: true,
				})
			},
			wants: []string{"gateway profile validation: compatible"},
		},
		{
			name: "profile breaking change details",
			print: func() {
				printGatewayProfileValidationText(gateway.TranscodeProfileValidationReport{
					OK:         true,
					Compatible: false,
					Changes: []gateway.TranscodeProfileChange{{
						Severity: "breaking",
						Scope:    "request",
						Kind:     "change_target",
						Source:   "body.id",
						Target:   "order.id",
						Message:  "target changed",
					}},
				})
			},
			wants: []string{
				"gateway profile validation: breaking",
				"breaking request change_target source=body.id target=order.id target changed",
			},
		},
		{
			name: "profile invalid",
			print: func() {
				printGatewayProfileValidationText(gateway.TranscodeProfileValidationReport{
					Errors: []string{"descriptor is required"},
					Changes: []gateway.TranscodeProfileChange{{
						Severity: "breaking",
						Scope:    "request",
						Kind:     "remove_mapping",
					}},
				})
			},
			wants:  []string{"gateway profile validation: invalid", "error: descriptor is required", "breaking request remove_mapping"},
			absent: "source=",
		},
		{
			name: "aggregation compatible",
			print: func() {
				printGatewayAggregationValidationText(gateway.AggregationValidationReport{
					OK:         true,
					Compatible: true,
				})
			},
			wants: []string{"gateway aggregation validation: compatible"},
		},
		{
			name: "aggregation breaking change details",
			print: func() {
				printGatewayAggregationValidationText(gateway.AggregationValidationReport{
					OK:         true,
					Compatible: false,
					Changes: []gateway.TranscodeProfileChange{{
						Severity: "breaking",
						Scope:    "aggregation_step",
						Kind:     "change_path",
						Source:   "/v1/orders",
						Target:   "/v2/orders",
						Message:  "path changed",
					}},
				})
			},
			wants: []string{
				"gateway aggregation validation: breaking",
				"breaking aggregation_step change_path source=/v1/orders target=/v2/orders path changed",
			},
		},
		{
			name: "aggregation invalid",
			print: func() {
				printGatewayAggregationValidationText(gateway.AggregationValidationReport{
					Errors: []string{"route not found"},
					Changes: []gateway.TranscodeProfileChange{{
						Severity: "breaking",
						Scope:    "aggregation",
						Kind:     "remove_step",
					}},
				})
			},
			wants:  []string{"gateway aggregation validation: invalid", "error: route not found", "breaking aggregation remove_step"},
			absent: "target=",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := withCommandIO(IOStreams{Out: &stdout}, outputText, verbosityNormal, func() error {
				test.print()
				return nil
			}); err != nil {
				t.Fatalf("print gateway validation: %v", err)
			}
			for _, want := range test.wants {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("output = %q, want %q", stdout.String(), want)
				}
			}
			if test.absent != "" && strings.Contains(stdout.String(), test.absent) {
				t.Fatalf("output = %q, do not want %q", stdout.String(), test.absent)
			}
		})
	}
}

func TestGatewayCommandRoutingAndInputBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
		want string
	}{
		{name: "gateway missing command", run: func() error { return gatewayCommand(nil) }, want: "gateway profile validate"},
		{name: "gateway unknown command", run: func() error { return gatewayCommand([]string{"unknown"}) }, want: "gateway profile validate"},
		{name: "profile missing command", run: func() error { return gatewayProfileCommand(nil) }, want: "gateway profile validate"},
		{name: "profile unknown command", run: func() error { return gatewayProfileCommand([]string{"unknown"}) }, want: "gateway profile validate"},
		{name: "aggregation missing command", run: func() error { return gatewayAggregationCommand(nil) }, want: "gateway aggregation validate"},
		{name: "aggregation unknown command", run: func() error { return gatewayAggregationCommand([]string{"unknown"}) }, want: "gateway aggregation validate"},
		{
			name: "openapi pair required",
			run: func() error {
				return gatewayAggregationValidateCommand([]string{"--openapi-base", "base.json"})
			},
			want: "--openapi-base and --openapi-candidate are required together",
		},
		{
			name: "aggregation files required",
			run: func() error {
				return gatewayAggregationValidateCommand(nil)
			},
			want: "--config and --candidate are required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if !errors.Is(err, errUsage) {
				t.Fatalf("error = %v, want errUsage", err)
			}
		})
	}

	if err := gatewayCommand([]string{"--help"}); err != nil {
		t.Fatalf("gateway help: %v", err)
	}
	if err := gatewayProfileCommand([]string{"--help"}); err == nil {
		t.Fatal("profile --help is not a profile subcommand and must return usage")
	}
	if err := gatewayAggregationCommand([]string{"--help"}); err == nil {
		t.Fatal("aggregation --help is not an aggregation subcommand and must return usage")
	}
}

func TestGatewayJSONReaderContracts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	directConfig := write("direct.json", `{"routes":[{"name":"home","pathPrefix":"/","targets":["http://127.0.0.1:1"]}]}`)
	config, err := readGatewayAggregationConfig(directConfig)
	if err != nil {
		t.Fatalf("read direct gateway config: %v", err)
	}
	if len(config.Routes) != 1 || config.Routes[0].Name != "home" {
		t.Fatalf("direct gateway config = %+v", config)
	}

	wrappedCandidate := write("wrapped-candidate.json", `{"aggregation":{"enabled":true,"steps":[{"name":"profile","path":"/profile"}]}}`)
	candidate, err := readGatewayAggregationCandidate(wrappedCandidate)
	if err != nil {
		t.Fatalf("read wrapped aggregation candidate: %v", err)
	}
	if !candidate.Enabled || len(candidate.Steps) != 1 {
		t.Fatalf("wrapped aggregation candidate = %+v", candidate)
	}

	for _, test := range []struct {
		name string
		read func(string) error
		want string
	}{
		{
			name: "profiles malformed",
			read: func(path string) error {
				_, err := readGatewayTranscodeProfiles(path)
				return err
			},
			want: "decode gateway config",
		},
		{
			name: "aggregation config malformed",
			read: func(path string) error {
				_, err := readGatewayAggregationConfig(path)
				return err
			},
			want: "decode gateway config",
		},
		{
			name: "aggregation candidate malformed",
			read: func(path string) error {
				_, err := readGatewayAggregationCandidate(path)
				return err
			},
			want: "decode candidate aggregation",
		},
		{
			name: "profile candidate malformed",
			read: func(path string) error {
				_, err := readGatewayTranscodeProfileCandidate(path)
				return err
			},
			want: "decode candidate profile",
		},
		{
			name: "openapi malformed",
			read: func(path string) error {
				_, err := readGatewayOpenAPIDocument(path)
				return err
			},
			want: "decode openapi document",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.read(write(test.name+".json", "{"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestReleaseEvidencePathAndJSONContracts(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "evidence.json"), []byte(`{"status":"pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	resolved, err := resolveReleaseEvidencePath("evidence.json")
	if err != nil {
		t.Fatalf("resolve evidence: %v", err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil {
		t.Fatalf("stat resolved evidence: %v", err)
	}
	expectedInfo, err := os.Stat(filepath.Join(root, "evidence.json"))
	if err != nil {
		t.Fatalf("stat expected evidence: %v", err)
	}
	if !os.SameFile(resolvedInfo, expectedInfo) {
		t.Fatalf("resolved path = %q, want evidence under %q", resolved, root)
	}
	evidence, err := readReleaseJSONFile("evidence.json", "runtime evidence")
	if err != nil {
		t.Fatalf("read release evidence: %v", err)
	}
	if evidence["status"] != "pass" {
		t.Fatalf("evidence = %#v", evidence)
	}

	for _, path := range []string{"", filepath.Join(root, "evidence.json")} {
		if _, err := resolveReleaseEvidencePath(path); err == nil || !strings.Contains(err.Error(), "must be relative") {
			t.Fatalf("resolveReleaseEvidencePath(%q) error = %v, want relative-path rejection", path, err)
		}
	}
	if _, err := resolveReleaseEvidencePath("missing.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing evidence error = %v, want os.ErrNotExist", err)
	}
	if _, err := readReleaseJSONFile("missing.json", "missing evidence"); err == nil || !strings.Contains(err.Error(), "resolve missing evidence") {
		t.Fatalf("missing release JSON error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "invalid.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReleaseJSONFile("invalid.json", "invalid evidence"); err == nil || !strings.Contains(err.Error(), "decode invalid evidence") {
		t.Fatalf("invalid release JSON error = %v", err)
	}
}

func TestReleaseGatewayContractTempDirectoryFailures(t *testing.T) {
	invalidTemp := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", invalidTemp)
	for _, test := range []struct {
		name string
		run  func() (releaseCheckItem, []string)
	}{
		{name: "profile", run: releaseGatewayProfileContractCheck},
		{name: "aggregation", run: releaseGatewayAggregationContractCheck},
	} {
		t.Run(test.name, func(t *testing.T) {
			item, blockers := test.run()
			if item.Status != "fail" || !item.Blocker || item.Detail == "" || len(blockers) != 1 {
				t.Fatalf("release check item=%+v blockers=%v", item, blockers)
			}
		})
	}
}

func TestCommandPathAndUsageHelperContracts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contract.api")
	if !sameFilePath(path, filepath.Clean(path)) {
		t.Fatalf("sameFilePath(%q) = false", path)
	}
	if sameFilePath(path, filepath.Join(filepath.Dir(path), "other.api")) {
		t.Fatal("sameFilePath accepted different files")
	}
	if text := usage(); !strings.Contains(text, "gofly") {
		t.Fatalf("usage output = %q, want gofly command", text)
	}
}

func TestReleaseGatewayReaderContracts(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	invalid := filepath.Join(dir, "invalid.json")
	missing := filepath.Join(dir, "missing.json")
	if err := os.WriteFile(valid, []byte(`{
		"gateway":{"routes":[{"name":"home","pathPrefix":"/","targets":["http://127.0.0.1:1"]}]},
		"enabled":true,
		"steps":[{"name":"profile","path":"/profile"}],
		"transcodeProfiles":[{"descriptor":"orders.OrderService","descriptorMethod":"GetOrder"}],
		"descriptor":"orders.OrderService",
		"descriptorMethod":"GetOrder"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := readReleaseGatewayConfig(valid)
	if err != nil || len(config.Routes) != 1 {
		t.Fatalf("release gateway config = %+v, err=%v", config, err)
	}
	aggregation, err := readReleaseGatewayAggregationCandidate(valid)
	if err != nil || !aggregation.Enabled || len(aggregation.Steps) != 1 {
		t.Fatalf("release aggregation = %+v, err=%v", aggregation, err)
	}
	profiles, err := readReleaseGatewayProfiles(valid)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("release profiles = %+v, err=%v", profiles, err)
	}
	profile, err := readReleaseGatewayProfileCandidate(valid)
	if err != nil || profile.DescriptorMethod != "GetOrder" {
		t.Fatalf("release profile = %+v, err=%v", profile, err)
	}

	tests := []struct {
		name string
		read func(string) error
	}{
		{name: "gateway config", read: func(path string) error { _, err := readReleaseGatewayConfig(path); return err }},
		{name: "aggregation", read: func(path string) error { _, err := readReleaseGatewayAggregationCandidate(path); return err }},
		{name: "profiles", read: func(path string) error { _, err := readReleaseGatewayProfiles(path); return err }},
		{name: "profile", read: func(path string) error { _, err := readReleaseGatewayProfileCandidate(path); return err }},
	}
	for _, test := range tests {
		t.Run(test.name+" missing", func(t *testing.T) {
			if err := test.read(missing); err == nil || !strings.Contains(err.Error(), "read ") {
				t.Fatalf("missing error = %v", err)
			}
		})
		t.Run(test.name+" malformed", func(t *testing.T) {
			if err := test.read(invalid); err == nil || !strings.Contains(err.Error(), "decode ") {
				t.Fatalf("malformed error = %v", err)
			}
		})
	}
}

func TestDoctorAndReleaseAdapterContracts(t *testing.T) {
	var stdout bytes.Buffer
	report := doctorReport{
		Version: "v-test",
		Go:      "go1.26.5",
		OS:      "linux",
		Arch:    "amd64",
		Checks: []doctorCheck{
			{Name: "toolchain", Status: "ok"},
			{Name: "protoc", Status: "warn", Message: "missing", FixHint: "install protoc"},
		},
		Summary: "1 warning(s)",
	}
	if err := withCommandIO(IOStreams{Out: &stdout}, outputText, verbosityNormal, func() error {
		printDoctorReport(report)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "gofly doctor v-test") || !strings.Contains(stdout.String(), "[WARN]") {
		t.Fatalf("doctor report output = %q", stdout.String())
	}
	actions := doctorNextActions(report.Checks, 0, 1)
	if len(actions) == 0 || !strings.Contains(strings.Join(actions, "\n"), "release gates") {
		t.Fatalf("doctor actions = %#v", actions)
	}

	checks := []doctorCheck{
		checkGoVersion(),
		checkGoModule(),
		checkGOPATH(),
		checkTools(),
		checkGit(),
		checkProtoc(),
		checkWritePermission(),
	}
	for _, check := range checks {
		if check.Name == "" || check.Status == "" {
			t.Fatalf("doctor check = %+v, want name and status", check)
		}
	}

	fs := flag.NewFlagSet("doctor-test", flag.ContinueOnError)
	value := fs.String("value", "", "value")
	remaining, err := parseDoctorFlags(fs, []string{"positional", "--value", "configured"})
	if err != nil || *value != "configured" || len(remaining) != 1 || remaining[0] != "positional" {
		t.Fatalf("parse doctor flags value=%q remaining=%v err=%v", *value, remaining, err)
	}

	cmd := apidiffCommand("", "-m", "example.com/test")
	if cmd.Path == "" || !strings.Contains(strings.Join(cmd.Args, " "), "tool apidiff -m example.com/test") {
		t.Fatalf("default apidiff command = %#v", cmd.Args)
	}
	cmd = apidiffCommand("custom-apidiff --mode strict", "-m", "example.com/test")
	if filepath.Base(cmd.Path) != "custom-apidiff" || strings.Join(cmd.Args[1:], " ") != "--mode strict -m example.com/test" {
		t.Fatalf("custom apidiff command path=%q args=%#v", cmd.Path, cmd.Args)
	}
}
