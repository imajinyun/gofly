package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/core/llm"
)

const commandTestProto = `syntax = "proto3";
package greeter.v1;
message HelloReq {
  string name = 1;
}
message HelloResp {
  string message = 1;
}
service Greeter {
  rpc Hello(HelloReq) returns (HelloResp);
}
`

const commandStreamingProto = `syntax = "proto3";
package chat.v1;
message ChatReq {
  string text = 1;
}
message ChatResp {
  string text = 1;
}
service Chat {
  rpc Talk(stream ChatReq) returns (stream ChatResp);
}
`

const commandTestAPI = `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  post /ping (PingReq) returns (PingResp)
}
`

func TestNormalizeGoctlStyleFlags(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "single dash long", arg: "-api", want: "--api"},
		{name: "single dash long with value", arg: "-dir=out", want: "--dir=out"},
		{name: "already double dash", arg: "--api", want: "--api"},
		{name: "short flag", arg: "-s", want: "-s"},
		{name: "plain arg", arg: "service.api", want: "service.api"},
		{name: "dash equals", arg: "-=value", want: "-=value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeGoctlStyleFlag(tt.arg); got != tt.want {
				t.Fatalf("normalizeGoctlStyleFlag(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

func TestExecuteColoredHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("GOFLY_NO_COLOR", "")
	rootOut := captureStdout(t, func() {
		if err := Execute([]string{"-h"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(rootOut, "\x1b[94mUsage:\x1b[0m") || !strings.Contains(rootOut, "  \x1b[92mapi") || !strings.Contains(rootOut, "  \x1b[95mrpc") {
		t.Fatalf("root help should contain command-family colors:\n%s", rootOut)
	}
	for _, want := range []string{
		"\n  \x1b[96mnew - Scaffold new production, API, or RPC services.\x1b[0m\n    \x1b[96mnew service <name>",
		"\n  \x1b[91mgen - Run unified code generators.\x1b[0m\n    \x1b[91mgen handler --name",
		"\n  \x1b[92mapi - Generate and manage API definition files.\x1b[0m\n    \x1b[92mapi format --file",
		"\n  \x1b[94mcomplete - Emit legacy completion scripts.\x1b[0m\n    \x1b[94mcomplete handler bash|zsh|fish|powershell|pwsh",
		"\n  \x1b[95mrpc - Generate and validate RPC services.\x1b[0m\n    \x1b[95mrpc new <name>",
	} {
		if !strings.Contains(rootOut, want) {
			t.Fatalf("root help missing grouped usage %q:\n%s", want, rootOut)
		}
	}
	if strings.Contains(rootOut, "  gofly ") || strings.Contains(rootOut, "    gofly ") {
		t.Fatalf("root help should not repeat gofly command prefix:\n%s", rootOut)
	}
	if strings.Contains(rootOut, "Manage .configuration") || !strings.Contains(rootOut, "Manage .gofly configuration") || !strings.Contains(rootOut, "release - Run release readiness checks") {
		t.Fatalf("root help should keep .gofly wording and include release command:\n%s", rootOut)
	}

	apiOut := captureStdout(t, func() {
		if err := Execute([]string{"api", "-h"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"\x1b[94mUsage:\x1b[0m", "\x1b[94mAvailable Commands:\x1b[0m", "go", "swagger"} {
		if !strings.Contains(apiOut, want) {
			t.Fatalf("api help missing %q:\n%s", want, apiOut)
		}
	}
	if strings.Contains(apiOut, "gofly api <command>") || strings.Contains(apiOut, "gofly api go") {
		t.Fatalf("api help should not repeat gofly command prefix:\n%s", apiOut)
	}
	if strings.Contains(apiOut, "Usage of api template") || strings.Contains(apiOut, "flag: help requested") {
		t.Fatalf("api -h should use gofly help renderer, got:\n%s", apiOut)
	}

	modelOut := captureStdout(t, func() {
		if err := Execute([]string{"help", "model"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(modelOut, "--style go_zero|gorm") || !strings.Contains(modelOut, "\x1b[") {
		t.Fatalf("model help should include colored model-specific flags:\n%s", modelOut)
	}
}

func TestExecuteNestedColoredHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("GOFLY_NO_COLOR", "")
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "api go trailing help",
			args: []string{"api", "go", "-h"},
			want: []string{"Generate REST service code", "\x1b[94mUsage:\x1b[0m", "\x1b[92mapi go --api"},
		},
		{
			name: "api go positional file before help",
			args: []string{"api", "go", "user.api", "-h"},
			want: []string{"Generate REST service code", "\x1b[92mapi go --api"},
		},
		{
			name: "api format alias trailing help",
			args: []string{"api", "fmt", "-h"},
			want: []string{"Format one .api file", "\x1b[92mapi format --api"},
		},
		{
			name: "api breaking alias trailing help",
			args: []string{"api", "break", "-h"},
			want: []string{"Detect breaking changes", "\x1b[92mapi breaking --base"},
		},
		{
			name: "api diff positional files before help",
			args: []string{"api", "diff", "old.api", "new.api", "--help"},
			want: []string{"Compare two .api files", "\x1b[92mapi diff --base"},
		},
		{
			name: "api routes alias trailing help",
			args: []string{"api", "routes", "--help"},
			want: []string{"Print or export route table", "\x1b[92mapi route --api"},
		},
		{
			name: "rpc gen trailing help",
			args: []string{"rpc", "gen", "--help"},
			want: []string{"Generate gofly/gRPC service code", "\x1b[95mrpc gen --src", "--profile <profile>", "--timeout <duration>"},
		},
		{
			name: "rpc check positional file before help",
			args: []string{"rpc", "check", "greeter.proto", "--help"},
			want: []string{"Validate protobuf syntax", "\x1b[95mrpc check --src"},
		},
		{
			name: "new service nested help",
			args: []string{"new", "service", "--help"},
			want: []string{"Create the golden-path production service scaffold", "\x1b[96mnew service <name>"},
		},
		{
			name: "new service positional name before help",
			args: []string{"new", "service", "orders", "--help"},
			want: []string{"Create the golden-path production service scaffold", "\x1b[96mnew service <name>"},
		},
		{
			name: "new api nested help",
			args: []string{"new", "api", "--help"},
			want: []string{"Create an API service scaffold", "\x1b[96mnew api <name>"},
		},
		{
			name: "new api positional name before help",
			args: []string{"new", "api", "hello", "--help"},
			want: []string{"Create an API service scaffold", "\x1b[96mnew api <name>"},
		},
		{
			name: "model gen trailing help",
			args: []string{"model", "gen", "-h"},
			want: []string{"Generate SQL model code from DDL", "\x1b[93mmodel gen --ddl"},
		},
		{
			name: "gen gateway positional before help",
			args: []string{"gen", "gateway", "edge", "-h"},
			want: []string{"Generate an API gateway scaffold", "\x1b[91mgen gateway <name>"},
		},
		{
			name: "handler gen positional before help",
			args: []string{"handler", "gen", "CreateOrder", "--help"},
			want: []string{"Generate REST handler skeletons", "\x1b[91mhandler gen <name>"},
		},
		{
			name: "gen api alias help",
			args: []string{"gen", "api", "-h"},
			want: []string{"Generate REST service code", "\x1b[91mgen api --api"},
		},
		{
			name: "gen rest alias help",
			args: []string{"gen", "rest", "user.api", "--help"},
			want: []string{"Generate REST service code", "\x1b[91mgen rest --api"},
		},
		{
			name: "config get positional before help",
			args: []string{"config", "get", "module", "--help"},
			want: []string{"Read one value from .gofly/config.json", "\x1b[94mconfig get <key>"},
		},
		{
			name: "config set positional before help",
			args: []string{"config", "set", "style", "production", "--help"},
			want: []string{"Update one value in .gofly/config.json", "\x1b[94mconfig set <key>"},
		},
		{
			name: "feature list alias help",
			args: []string{"feature", "ls", "-h"},
			want: []string{"List registered scaffold features", "\x1b[96mfeature list"},
		},
		{
			name: "feature run positional before help",
			args: []string{"feature", "run", "observability", "--help"},
			want: []string{"Preview generated files", "\x1b[96mfeature run <feature-name>"},
		},
		{
			name: "plugin list alias help",
			args: []string{"plugin", "ls", "-h"},
			want: []string{"List built-in generation plugins", "\x1b[95mplugin list"},
		},
		{
			name: "plugin search help",
			args: []string{"plugin", "search", "--help"},
			want: []string{"Search a plugin registry", "\x1b[95mplugin search"},
		},
		{
			name: "plugin run positional before help",
			args: []string{"plugin", "run", "./my-plugin", "--help"},
			want: []string{"Run a built-in or external", "\x1b[95mplugin run <plugin-name-or-path>"},
		},
		{
			name: "docker positional before help",
			args: []string{"docker", "hello", "--help"},
			want: []string{"Generate a Dockerfile", "\x1b[94mdocker <name>"},
		},
		{
			name: "kube shorthand kind positional before help",
			args: []string{"kube", "svc", "hello", "--help"},
			want: []string{"Generate a Kubernetes service manifest", "\x1b[96mkube service <name>"},
		},
		{
			name: "template subcommand positional before help",
			args: []string{"template", "init", "placeholder", "--help"},
			want: []string{"Manage local or remote generation templates", "\x1b[93mtemplate init"},
		},
		{
			name: "quickstart positional before help",
			args: []string{"quickstart", "checkout", "--help"},
			want: []string{"Create a runnable API service quickly", "\x1b[96mquickstart <name>"},
		},
		{
			name: "migrate create positional before help",
			args: []string{"migrate", "create", "add-users", "--help"},
			want: []string{"Create SQL migration files", "\x1b[95mmigrate create <name>"},
		},
		{
			name: "migration new alias positional before help",
			args: []string{"migration", "new", "add-users", "--help"},
			want: []string{"Create SQL migration files", "\x1b[95mmigrate create <name>"},
		},
		{
			name: "env check positional before help",
			args: []string{"env", "check", "placeholder", "--help"},
			want: []string{"Check local toolchain dependencies", "\x1b[92menv check"},
		},
		{
			name: "complete handler help",
			args: []string{"complete", "handler", "bash", "--help"},
			want: []string{"Emit bash completion script", "\x1b[94mcomplete handler bash"},
		},
		{
			name: "complete handler pwsh alias help",
			args: []string{"complete", "handler", "pwsh", "--help"},
			want: []string{"Emit powershell completion script", "\x1b[94mcomplete handler powershell"},
		},
		{
			name: "completion powershell help",
			args: []string{"completion", "powershell", "--help"},
			want: []string{"Emit powershell completion script", "\x1b[94mcompletion powershell"},
		},
		{
			name: "completion pwsh alias help",
			args: []string{"completion", "pwsh", "--help"},
			want: []string{"Emit powershell completion script", "\x1b[94mcompletion powershell"},
		},
		{
			name: "release help",
			args: []string{"release", "--help"},
			want: []string{"Run release readiness checks", "\x1b[97mrelease check"},
		},
		{
			name: "release check help",
			args: []string{"release", "check", "--help"},
			want: []string{"Aggregate API/RPC breaking checks", "\x1b[97mrelease check", "--strict"},
		},
		{
			name: "version help",
			args: []string{"version", "--help"},
			want: []string{"Print version and build metadata", "\x1b[97mversion [--json]"},
		},
		{
			name: "version positional before help",
			args: []string{"version", "extra", "--help"},
			want: []string{"Print version and build metadata", "\x1b[97mversion [--json]"},
		},
		{
			name: "root nested help topic",
			args: []string{"help", "api", "gen"},
			want: []string{"Generate REST service code", "api go --api"},
		},
		{
			name: "root generate alias help topic",
			args: []string{"help", "generate", "rest"},
			want: []string{"Generate REST service code", "gen rest --api"},
		},
		{
			name: "rpc template help topic",
			args: []string{"help", "rpc", "template"},
			want: []string{"Generate starter proto templates", "--remote <repo|dir>", "rpc template -o greeter.proto --remote"},
		},
		{
			name: "root migration alias help topic",
			args: []string{"help", "migration", "new"},
			want: []string{"Create SQL migration files", "migrate create <name>"},
		},
		{
			name: "multi level model help topic",
			args: []string{"model", "mysql", "datasource", "-h"},
			want: []string{"Generate SQL model code by introspecting", "model mysql datasource --url"},
		},
		{
			name: "multi level postgres alias help topic",
			args: []string{"help", "model", "postgresql", "datasource"},
			want: []string{"Generate SQL model code by introspecting", "model pg datasource --url"},
		},
		{
			name: "multi level model positional dsn before help",
			args: []string{"model", "mysql", "datasource", "user:pass@tcp(localhost:3306)/app", "-h"},
			want: []string{"Generate SQL model code by introspecting", "model mysql datasource --url"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if err := Execute(tt.args); err != nil {
					t.Fatal(err)
				}
			})
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Fatalf("nested help missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "Usage of ") || strings.Contains(out, "flag: help requested") {
				t.Fatalf("nested help should use gofly help renderer, got:\n%s", out)
			}
			if strings.Contains(out, "  gofly ") || strings.Contains(out, "\n  gofly ") {
				t.Fatalf("nested help should not repeat gofly prefix, got:\n%s", out)
			}
		})
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExecuteFlagParsingErrorsAreSilentUsageErrors(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{name: "api check unknown flag", args: []string{"api", "check", "--bad"}, wantMessage: "flag provided but not defined"},
		{name: "rpc gen unknown flag", args: []string{"rpc", "gen", "--bad"}, wantMessage: "flag provided but not defined"},
		{name: "version unknown flag", args: []string{"version", "--bad"}, wantMessage: "flag provided but not defined"},
		{name: "env unknown flag", args: []string{"env", "--bad"}, wantMessage: "flag provided but not defined"},
		{name: "upgrade unknown flag", args: []string{"upgrade", "--bad"}, wantMessage: "flag provided but not defined"},
		{name: "new api missing flag value", args: []string{"new", "api", "hello", "--module"}, wantMessage: "flag needs an argument"},
		{name: "api gen missing flag value", args: []string{"api", "gen", "--file"}, wantMessage: "flag needs an argument"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotErr error
			stderr := captureStderr(t, func() {
				gotErr = Execute(tt.args)
			})
			if gotErr == nil {
				t.Fatalf("Execute(%v) succeeded, want flag parsing error", tt.args)
			}
			if !strings.Contains(gotErr.Error(), tt.wantMessage) {
				t.Fatalf("Execute(%v) error = %v, want %q", tt.args, gotErr, tt.wantMessage)
			}
			if ExitCode(gotErr) != 2 {
				t.Fatalf("ExitCode(%v) = %d, want 2", gotErr, ExitCode(gotErr))
			}
			if stderr != "" {
				t.Fatalf("flag parsing errors should not print stdlib usage to stderr, got:\n%s", stderr)
			}
		})
	}
}

func TestExitCodeClassifiesUsageErrors(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Fatalf("ExitCode(nil) = %d, want 0", got)
	}

	usageErr := Execute([]string{"model"})
	if usageErr == nil {
		t.Fatal("Execute(model) succeeded, want usage error")
	}
	if got := ExitCode(usageErr); got != 2 {
		t.Fatalf("ExitCode(model usage error) = %d, want 2: %v", got, usageErr)
	}

	unknownCommandErr := Execute([]string{"unknown"})
	if unknownCommandErr == nil {
		t.Fatal("Execute(unknown) succeeded, want usage error")
	}
	if got := ExitCode(unknownCommandErr); got != 2 {
		t.Fatalf("ExitCode(unknown command) = %d, want 2: %v", got, unknownCommandErr)
	}

	unsupportedCompletionErr := Execute([]string{"completion", "unknown-shell"})
	if unsupportedCompletionErr == nil {
		t.Fatal("Execute(completion unknown-shell) succeeded, want usage error")
	}
	if got := ExitCode(unsupportedCompletionErr); got != 2 {
		t.Fatalf("ExitCode(unsupported completion shell) = %d, want 2: %v", got, unsupportedCompletionErr)
	}
	if !errors.Is(unsupportedCompletionErr, errUsage) {
		t.Fatalf("unsupported completion error should wrap errUsage: %v", unsupportedCompletionErr)
	}

	unsupportedCompleteErr := Execute([]string{"complete", "handler", "unknown-shell"})
	if unsupportedCompleteErr == nil {
		t.Fatal("Execute(complete handler unknown-shell) succeeded, want usage error")
	}
	if got := ExitCode(unsupportedCompleteErr); got != 2 {
		t.Fatalf("ExitCode(unsupported complete shell) = %d, want 2: %v", got, unsupportedCompleteErr)
	}
	if !strings.Contains(unsupportedCompleteErr.Error(), "bash|zsh|fish|powershell|pwsh") {
		t.Fatalf("unsupported complete shell error should mention pwsh alias: %v", unsupportedCompleteErr)
	}

	flagErr := Execute([]string{"version", "--bad"})
	if flagErr == nil {
		t.Fatal("Execute(version --bad) succeeded, want flag error")
	}
	if got := ExitCode(flagErr); got != 2 {
		t.Fatalf("ExitCode(flag error) = %d, want 2: %v", got, flagErr)
	}

	if got := ExitCode(errors.New("runtime failure")); got != 1 {
		t.Fatalf("ExitCode(runtime error) = %d, want 1", got)
	}
}

func TestExecuteRPCCommandsValidateRequiredInputsAndTimeout(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "rpc gen missing proto", args: []string{"rpc", "gen", "--dir", t.TempDir()}, want: "proto file is required"},
		{name: "rpc gen zero timeout", args: []string{"rpc", "gen", "greeter.proto", "--standard", "--timeout", "0"}, want: "--timeout must be greater than zero"},
		{name: "rpc gen negative timeout", args: []string{"rpc", "gen", "greeter.proto", "--standard", "--timeout=-1s"}, want: "--timeout must be greater than zero"},
		{name: "rpc protoc missing proto", args: []string{"rpc", "protoc"}, want: "proto file is required"},
		{name: "rpc protoc zero timeout", args: []string{"rpc", "protoc", "greeter.proto", "--timeout", "0"}, want: "--timeout must be greater than zero"},
		{name: "rpc protoc negative timeout", args: []string{"rpc", "protoc", "greeter.proto", "--timeout=-1s"}, want: "--timeout must be greater than zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Execute(tt.args)
			if err == nil {
				t.Fatalf("Execute(%v) succeeded, want usage error", tt.args)
			}
			if !errors.Is(err, errUsage) {
				t.Fatalf("Execute(%v) error = %v, want errUsage", tt.args, err)
			}
			if ExitCode(err) != 2 {
				t.Fatalf("ExitCode(%v) = %d, want 2", err, ExitCode(err))
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute(%v) error = %v, want %q", tt.args, err, tt.want)
			}
		})
	}
}

func TestExecuteCommandsRejectUnexpectedPositionals(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "version extra", args: []string{"version", "extra"}, want: "does not accept positional"},
		{name: "upgrade positional", args: []string{"upgrade", "v1.2.3"}, want: "use --version v1.2.3"},
		{name: "completion extra", args: []string{"completion", "bash", "extra"}, want: "exactly one shell"},
		{name: "complete handler missing shell", args: []string{"complete", "handler"}, want: "expected `gofly complete handler"},
		{name: "complete handler extra", args: []string{"complete", "handler", "bash", "extra"}, want: "exactly one shell"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Execute(tt.args)
			if err == nil {
				t.Fatalf("Execute(%v) succeeded, want usage error", tt.args)
			}
			if !errors.Is(err, errUsage) || ExitCode(err) != 2 {
				t.Fatalf("Execute(%v) error = %v, want usage exit code", tt.args, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute(%v) error = %v, want %q", tt.args, err, tt.want)
			}
		})
	}
}

func TestCompletionShellRegistry(t *testing.T) {
	wantShells := []string{"bash", "zsh", "fish", "powershell", "pwsh"}
	if got := strings.Join(completionShells, "|"); got != strings.Join(wantShells, "|") {
		t.Fatalf("completionShells = %q, want %q", got, strings.Join(wantShells, "|"))
	}
	if completionShellUsage != strings.Join(wantShells, "|") {
		t.Fatalf("completionShellUsage = %q, want %q", completionShellUsage, strings.Join(wantShells, "|"))
	}
	for _, shell := range wantShells {
		t.Run(shell, func(t *testing.T) {
			if !isCompletionShell(shell) {
				t.Fatalf("isCompletionShell(%q) = false, want true", shell)
			}
			if !isCompletionHelpSubcommand(shell) {
				t.Fatalf("isCompletionHelpSubcommand(%q) = false, want true", shell)
			}
			if !isCompleteHandlerShell(shell) {
				t.Fatalf("isCompleteHandlerShell(%q) = false, want true", shell)
			}
			if _, err := generator.GenerateCompletion(shell); err != nil {
				t.Fatalf("GenerateCompletion(%q) error = %v", shell, err)
			}
		})
	}
	if isCompletionShell("unknown-shell") {
		t.Fatal("isCompletionShell(unknown-shell) = true, want false")
	}
}

func TestExecuteVersionJSON(t *testing.T) {
	out := captureStdout(t, func() {
		if err := Execute([]string{"version", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "gofly ") || strings.Contains(out, "commit:") {
		t.Fatalf("version --json should not fall back to plain text output:\n%s", out)
	}

	var envelope struct {
		OK      bool        `json:"ok"`
		Command string      `json:"command"`
		Version string      `json:"version"`
		Data    versionInfo `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("version --json emitted invalid JSON envelope: %v\n%s", err, out)
	}
	if !envelope.OK || envelope.Command != "version" || envelope.Version != Version {
		t.Fatalf("version --json envelope = %+v", envelope)
	}
	info := envelope.Data
	if info.Tool != "gofly" || info.Version != Version || info.Commit != Commit || info.BuiltAt != BuiltAt {
		t.Fatalf("version info = %+v, want current build metadata", info)
	}
	if info.GoVersion == "" || info.GOOS == "" || info.GOARCH == "" {
		t.Fatalf("version info missing runtime metadata: %+v", info)
	}
}

func TestExecuteRPCGen(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"rpc", "gen", "--file", protoPath, "--dir", outDir, "--package", "greeterv1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "greeter.grpc.gofly.go")); err != nil {
		t.Fatalf("expected generated rpc file: %v", err)
	}
}

func TestExecuteRPCGenGoctlAliases(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"rpc", "gen", "--src", protoPath, "--out", outDir, "--package", "greeterv1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "greeter.grpc.gofly.go")); err != nil {
		t.Fatalf("expected generated rpc file via --src/--out aliases: %v", err)
	}
}

func TestExecuteRPCGenAcceptsGoctlTemplateFlags(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{
		"rpc", "gen",
		"--src", protoPath,
		"--out", outDir,
		"--package", "greeterv1",
		"--style", "go_zero",
		"--home", filepath.Join(dir, "templates"),
		"--remote", "https://example.invalid/templates.git",
		"--branch", "main",
		"--multiple",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "greeter.grpc.gofly.go")); err != nil {
		t.Fatalf("expected generated rpc file with template flags: %v", err)
	}
}

func TestExecuteRPCGenGoflyNoClientAndMultiple(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "multi.proto")
	protoContent := `syntax = "proto3";
package demo.v1;
message PingReq {
  string name = 1;
}
message PingResp {
  string message = 1;
}
service Greeter {
  rpc Ping(PingReq) returns (PingResp);
}
service Health {
  rpc Check(PingReq) returns (PingResp);
}
`
	if err := os.WriteFile(protoPath, []byte(protoContent), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{
		"rpc", "gen",
		"--file", protoPath,
		"--dir", outDir,
		"--package", "demov1",
		"--transport", "gofly",
		"--client=false",
		"--multiple",
	}); err != nil {
		t.Fatal(err)
	}
	greeterData, err := os.ReadFile(filepath.Join(outDir, "greeter", "multi.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(greeterData), "type Greeter interface") || strings.Contains(string(greeterData), "GreeterClient") {
		t.Fatalf("greeter no-client/multiple output:\n%s", greeterData)
	}
	if _, err := os.Stat(filepath.Join(outDir, "health", "multi.gofly.go")); err != nil {
		t.Fatalf("expected health split output: %v", err)
	}
}

func TestExecuteRPCGenPositionalProto(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "positional")
	if err := Execute([]string{"rpc", "gen", protoPath, "--out", outDir, "--package", "greeterv1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "greeter.grpc.gofly.go")); err != nil {
		t.Fatalf("expected generated rpc file via positional proto: %v", err)
	}
}

func TestExecuteGenRPCWithGRPCTransportSupportsStreaming(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "chat.proto")
	if err := os.WriteFile(protoPath, []byte(commandStreamingProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"gen", "rpc", "--file", protoPath, "--dir", outDir, "--package", "chatv1", "--transport", "grpc"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "chat.grpc.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "NewChatGRPCServer") {
		t.Fatalf("generated grpc file = %s", data)
	}
}

func TestExecuteRPCGenWithGoflyTransportSupportsStreamingAndOptions(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "chat.proto")
	if err := os.WriteFile(protoPath, []byte(commandStreamingProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"rpc", "gen", "--file", protoPath, "--dir", outDir, "--package", "chatv1", "--transport", "gofly", "--with-middleware", "--with-recovery", "--with-validator"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "chat.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"Talk(ctx context.Context, stream *rpc.Stream) error",
		"Streams: []rpc.StreamDesc",
		`Metadata: map[string]string{"request": "ChatReq", "response": "ChatResp", "clientStream": "true", "serverStream": "true"}`,
		"func ChatInterceptorChain(middlewares ...endpoint.Middleware) endpoint.Middleware",
		"func WithChatRecovery() ChatServerOption",
		"func WithChatValidator(validator ChatValidator) ChatServerOption",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated gofly streaming/options code missing %q:\n%s", want, out)
		}
	}
}

func TestExecuteRPCGenWithKitexCompatibleProfileEnablesGovernanceHelpers(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "chat.proto")
	if err := os.WriteFile(protoPath, []byte(commandStreamingProto), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"rpc", "gen", "--file", protoPath, "--dir", outDir, "--package", "chatv1", "--transport", "gofly", "--profile", "kitex-compatible"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "chat.gofly.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"func ChatInterceptorChain(middlewares ...endpoint.Middleware) endpoint.Middleware",
		"func WithChatKitexInterceptors(interceptors ...rpc.KitexInterceptor) ChatServerOption",
		"func ChatKitexEndpointChain(middlewares ...rpc.KitexMiddleware) rpc.KitexMiddleware",
		"func ChatObservabilityInterceptor(name string) rpc.KitexInterceptor",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated kitex-compatible gofly code missing %q:\n%s", want, out)
		}
	}
}

func TestExecuteRPCCheck(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"rpc", "check", "--file", protoPath}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"rpc", "check", "--src", protoPath}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"rpc", "check", protoPath}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteRPCKitexStyleIDLCommands(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := Execute([]string{"rpc", "idl", protoPath, "--format", "json"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, `"kind": "proto"`) || !strings.Contains(out, `"services": 1`) {
		t.Fatalf("rpc idl output = %s", out)
	}
	lintOut := captureStdout(t, func() {
		if err := Execute([]string{"rpc", "lint", "--file", protoPath}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(lintOut, "rpc idl ok") {
		t.Fatalf("rpc lint output = %s", lintOut)
	}
	clientDir := filepath.Join(dir, "client")
	if err := Execute([]string{"rpc", "client", protoPath, "--out", clientDir, "--package", "rpcclient"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clientDir, "greeter_client.go")); err != nil {
		t.Fatalf("expected generated rpc client: %v", err)
	}
	serverDir := filepath.Join(dir, "server")
	if err := Execute([]string{"rpc", "server", "--src", protoPath, "--dir", serverDir, "--package", "rpcimpl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(serverDir, "greeter_server.go")); err != nil {
		t.Fatalf("expected generated rpc server: %v", err)
	}
	serviceDir := filepath.Join(dir, "svc")
	if err := Execute([]string{"rpc", "middleware", "auth", "--dir", serviceDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(serviceDir, "internal", "rpc", "middleware", "auth.go")); err != nil {
		t.Fatalf("expected generated rpc middleware: %v", err)
	}
}

func TestExecuteRPCThriftAndDeps(t *testing.T) {
	dir := t.TempDir()
	thriftPath := filepath.Join(dir, "greeter.thrift")
	thrift := `namespace go example.com/greeter
include "base.thrift"

struct SayHelloReq {
  1: string name
}

struct SayHelloResp {
  1: string message
}

service Greeter {
  SayHelloResp SayHello(1: SayHelloReq req)
}`
	if err := os.WriteFile(thriftPath, []byte(thrift), 0o644); err != nil {
		t.Fatal(err)
	}
	depsOut := captureStdout(t, func() {
		if err := Execute([]string{"rpc", "deps", thriftPath}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(depsOut, "base.thrift") {
		t.Fatalf("rpc deps output = %s", depsOut)
	}
	outDir := filepath.Join(dir, "proto")
	if err := Execute([]string{"rpc", "thrift2proto", "--file", thriftPath, "--out", outDir}); err != nil {
		t.Fatal(err)
	}
	protoData, err := os.ReadFile(filepath.Join(outDir, "greeter.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(protoData), "service Greeter") || !strings.Contains(string(protoData), "rpc SayHello") {
		t.Fatalf("generated proto from thrift = %s", protoData)
	}
}

func TestExecuteRPCProtocAcceptsGoctlPositionalAndSrcAlias(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProtoc := filepath.Join(dir, "protoc")
	argsPath := filepath.Join(dir, "protoc.args")
	t.Setenv("PROTOC_ARGS_FILE", argsPath)
	if err := os.WriteFile(fakeProtoc, []byte("#!/bin/sh\nprintf '%s\n' \"$@\" > \"$PROTOC_ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	goOut := filepath.Join(dir, "pb")
	grpcOut := filepath.Join(dir, "grpc")
	if err := Execute([]string{
		"rpc", "protoc", protoPath,
		"--I", dir,
		"--go_out", goOut,
		"--go-grpc_out", grpcOut,
		"--protoc", fakeProtoc,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	argsText := string(data)
	for _, want := range []string{
		"-I\n" + dir,
		"--go_out=" + goOut,
		"--go-grpc_out=" + grpcOut,
		protoPath,
	} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("protoc args missing %q:\n%s", want, argsText)
		}
	}

	if err := Execute([]string{
		"rpc", "protoc",
		"--src", protoPath,
		"--dir", goOut,
		"--protoc", fakeProtoc,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteRPCProtocTimesOutHungProtoc(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProtoc := filepath.Join(dir, "protoc")
	if err := os.WriteFile(fakeProtoc, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Execute([]string{
		"rpc", "protoc",
		protoPath,
		"--protoc", fakeProtoc,
		"--timeout", "20ms",
	})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("rpc protoc timeout err = %v, want context deadline exceeded", err)
	}
}

func TestExecuteGenRPCStandardTimesOutHungProtoc(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProtoc := filepath.Join(dir, "protoc")
	if err := os.WriteFile(fakeProtoc, []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := Execute([]string{
		"gen", "rpc",
		protoPath,
		"--dir", filepath.Join(dir, "out"),
		"--standard",
		"--timeout", "20ms",
	})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("gen rpc --standard timeout err = %v, want context deadline exceeded", err)
	}
}

func TestExecuteRPCProtocAcceptsGoctlReservedFlags(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProtoc := filepath.Join(dir, "protoc")
	argsPath := filepath.Join(dir, "protoc.args")
	t.Setenv("PROTOC_ARGS_FILE", argsPath)
	if err := os.WriteFile(fakeProtoc, []byte("#!/bin/sh\nprintf '%s\n' \"$@\" > \"$PROTOC_ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	zrpcOut := filepath.Join(dir, "zrpc")
	if err := Execute([]string{
		"rpc", "protoc",
		"-src", protoPath,
		"-zrpc_out", zrpcOut,
		"-go_opt", "Mgoogle/protobuf/empty.proto=empty",
		"-go-grpc_opt", "require_unimplemented_servers=false",
		"-multiple",
		"-c=false",
		"-v",
		"-name-from-filename",
		"-plugin", "protoc-gen-api",
		"-style", "go_zero",
		"-home", filepath.Join(dir, "templates"),
		"-remote", "https://example.invalid/templates.git",
		"-branch", "main",
		"-module", "example.com/greeter",
		"-protoc", fakeProtoc,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	argsText := string(data)
	for _, want := range []string{
		"--go_out=" + zrpcOut,
		"--go-grpc_out=" + zrpcOut,
		"--go_opt=Mgoogle/protobuf/empty.proto=empty",
		"--go-grpc_opt=require_unimplemented_servers=false",
		protoPath,
	} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("protoc args missing %q:\n%s", want, argsText)
		}
	}
}

func TestExecuteRPCProtocGoflyPluginArgs(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProtoc := filepath.Join(dir, "protoc")
	argsPath := filepath.Join(dir, "protoc.args")
	envPath := filepath.Join(dir, "protoc.env")
	t.Setenv("PROTOC_ARGS_FILE", argsPath)
	t.Setenv("PROTOC_ENV_FILE", envPath)
	if err := os.WriteFile(fakeProtoc, []byte("#!/bin/sh\nprintf '%s\n' \"$@\" > \"$PROTOC_ARGS_FILE\"\nprintf '%s\n%s\n%s\n%s\n%s\n' \"$GOFLY_PLUGIN_MODE\" \"$GOFLY_MODULE\" \"$GOFLY_NAME_FROM_FILENAME\" \"$GOFLY_NO_CLIENT\" \"$GOFLY_MULTIPLE\" > \"$PROTOC_ENV_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	zrpcOut := filepath.Join(dir, "zrpc")
	if err := Execute([]string{
		"rpc", "protoc",
		protoPath,
		"--zrpc_out", zrpcOut,
		"--plugin", "gofly",
		"--module", "example.com/greeter",
		"--name-from-filename",
		"--protoc", fakeProtoc,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	argsText := string(data)
	for _, want := range []string{
		"--plugin=protoc-gen-gofly=",
		"--gofly_out=" + zrpcOut,
		"--gofly_opt=paths=source_relative",
		"--gofly_opt=module=example.com/greeter",
		"--gofly_opt=name_from_filename=true",
		protoPath,
	} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("protoc args missing %q:\n%s", want, argsText)
		}
	}
	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(envData)) != "protoc\nexample.com/greeter\ntrue" {
		t.Fatalf("gofly plugin env = %q", envData)
	}
}

func TestExecuteRPCProtocGoflyPluginNoClientMultipleArgs(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProtoc := filepath.Join(dir, "protoc")
	argsPath := filepath.Join(dir, "protoc.args")
	envPath := filepath.Join(dir, "protoc.env")
	t.Setenv("PROTOC_ARGS_FILE", argsPath)
	t.Setenv("PROTOC_ENV_FILE", envPath)
	if err := os.WriteFile(fakeProtoc, []byte("#!/bin/sh\nprintf '%s\n' \"$@\" > \"$PROTOC_ARGS_FILE\"\nprintf '%s\n%s\n%s\n' \"$GOFLY_PLUGIN_MODE\" \"$GOFLY_NO_CLIENT\" \"$GOFLY_MULTIPLE\" > \"$PROTOC_ENV_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Execute([]string{
		"rpc", "protoc",
		protoPath,
		"--plugin", "gofly",
		"--client=false",
		"--multiple",
		"--protoc", fakeProtoc,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	argsText := string(data)
	for _, want := range []string{
		"--gofly_opt=no_client=true",
		"--gofly_opt=multiple=true",
	} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("protoc args missing %q:\n%s", want, argsText)
		}
	}
	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(envData)) != "protoc\ntrue\ntrue" {
		t.Fatalf("gofly plugin env = %q", envData)
	}
}

func TestExecuteAPIRPCTemplateEntrypoints(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "hello.api")
	if err := Execute([]string{
		"api",
		"-o", apiPath,
		"-home", filepath.Join(dir, "templates"),
		"-remote", "https://example.invalid/templates.git",
		"-branch", "main",
	}); err != nil {
		t.Fatal(err)
	}
	apiData, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apiData), "service hello") || !strings.Contains(string(apiData), "@handler Ping") {
		t.Fatalf("api template = %s", apiData)
	}

	rpcPath := filepath.Join(dir, "greeter.proto")
	if err := Execute([]string{"rpc", "-o", rpcPath, "-home", filepath.Join(dir, "templates")}); err != nil {
		t.Fatal(err)
	}
	rpcData, err := os.ReadFile(rpcPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rpcData), `syntax = "proto3"`) || !strings.Contains(string(rpcData), "package greeter.v1") {
		t.Fatalf("rpc template = %s", rpcData)
	}

	rpcTemplatePath := filepath.Join(dir, "rpc-template.proto")
	if err := Execute([]string{"rpc", "template", "--o", rpcTemplatePath}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rpcTemplatePath); err != nil {
		t.Fatalf("expected rpc template file: %v", err)
	}
}

func TestExecuteAPICheck(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "check", "--file", apiPath}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteAPIValidateRejectsSemanticErrors(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "bad.api")
	api := `type PingReq {
  Name MissingType
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (MissingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Execute([]string{"api", "validate", "--api", apiPath})
	if err == nil {
		t.Fatal("api validate succeeded, want semantic validation error")
	}
	for _, want := range []string{
		"unknown field type PingReq.Name MissingType",
		"route Ping references unknown response type MissingResp",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("api validate error missing %q:\n%v", want, err)
		}
	}
}

func TestExecuteAPIGen(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  post /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"api", "gen", "--file", apiPath, "--dir", outDir, "--package", "handler"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "internal", "api", "v1", "types.go")); err != nil {
		t.Fatalf("expected generated api file: %v", err)
	}
}

func TestExecuteAPIGoAlias(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"api", "go", "--file", apiPath, "--dir", outDir, "--package", "handler"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "internal", "api", "v1", "types.go")); err != nil {
		t.Fatalf("expected generated api file from go alias: %v", err)
	}
}

func TestExecuteAPIGoAcceptsGoctlSingleDashFlags(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{
		"api", "go",
		"-api", apiPath,
		"-dir", outDir,
		"-package", "handler",
		"-style", "go_zero",
		"-home", filepath.Join(dir, "templates"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "internal", "api", "v1", "types.go")); err != nil {
		t.Fatalf("expected generated api file from single-dash flags: %v", err)
	}
}

func TestExecuteAPIGenAcceptsGoctlTemplateFlags(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  post /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{
		"api", "go",
		"--api", apiPath,
		"--dir", outDir,
		"--package", "handler",
		"--style", "go_zero",
		"--home", filepath.Join(dir, "templates"),
		"--remote", "https://example.invalid/templates.git",
		"--branch", "main",
		"--multiple",
		"--test",
		"--type-group",
	}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		filepath.Join("internal", "api", "v1", "types_ping_req.go"),
		filepath.Join("internal", "api", "v1", "types_ping_resp.go"),
		filepath.Join("internal", "api", "v1", "user_api", "routes_test.go"),
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Fatalf("expected generated api file %s with test/type-group flags: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "internal", "api", "v1", "types.go")); err == nil {
		t.Fatal("api gen --type-group should split DTOs instead of writing types.go")
	}
}

func TestExecuteAPICommandsAcceptLeadingPositionalFile(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type ListReq {
  Id string
  Page int
}
type UserResp {
  Id string
}
service user-api {
  @handler listUser
  get /users/{id} (ListReq) returns (UserResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Execute([]string{"api", "check", apiPath}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "format", apiPath, "-w=false"}); err != nil {
		t.Fatal(err)
	}

	genDir := filepath.Join(dir, "gen")
	if err := Execute([]string{"api", "go", apiPath, "--dir", genDir, "--package", "handler"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(genDir, "internal", "api", "v1", "types.go")); err != nil {
		t.Fatalf("expected generated api file from positional api: %v", err)
	}

	typesOut := filepath.Join(dir, "types.go")
	if err := Execute([]string{"api", "types", apiPath, "--o", typesOut, "--package", "types"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(typesOut); err != nil {
		t.Fatalf("expected generated types file from positional api and --o alias: %v", err)
	}
	mixedTypesOut := filepath.Join(dir, "mixed_types.go")
	if err := Execute([]string{"api", "types", "--package", "types", apiPath, "--o", mixedTypesOut}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mixedTypesOut); err != nil {
		t.Fatalf("expected generated types file from mixed positional api and --o alias: %v", err)
	}

	routesOut := filepath.Join(dir, "routes.json")
	if err := Execute([]string{"api", "route", apiPath, "--o", routesOut, "--format", "json"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(routesOut); err != nil {
		t.Fatalf("expected generated routes file from positional api: %v", err)
	}
	mixedRoutesOut := filepath.Join(dir, "mixed-routes.json")
	if err := Execute([]string{"api", "route", "--format", "json", apiPath, "--o", mixedRoutesOut}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mixedRoutesOut); err != nil {
		t.Fatalf("expected generated routes file from mixed positional api: %v", err)
	}

	docOut := filepath.Join(dir, "openapi.json")
	if err := Execute([]string{"api", "doc", apiPath, "--output", docOut, "--format", "openapi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(docOut); err != nil {
		t.Fatalf("expected generated doc file from positional api: %v", err)
	}

	clientOut := filepath.Join(dir, "client.ts")
	if err := Execute([]string{"api", "ts", apiPath, "--o", clientOut}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clientOut); err != nil {
		t.Fatalf("expected generated client file from positional api and --o alias: %v", err)
	}

	openAPIPath := filepath.Join(dir, "swagger.json")
	openAPI := `{
  "openapi": "3.0.3",
  "info": {"title": "Imported", "version": "1.0.0"},
  "paths": {
    "/ping": {
      "get": {
        "operationId": "ping",
        "responses": {"200": {"description": "OK"}}
      }
    }
  }
}`
	if err := os.WriteFile(openAPIPath, []byte(openAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	importOut := filepath.Join(dir, "imported.api")
	if err := Execute([]string{"api", "import", openAPIPath, "--o", importOut}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(importOut); err != nil {
		t.Fatalf("expected imported api file from positional swagger: %v", err)
	}
}

func TestExecuteAPINew(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"api", "new", "hello", "--module", "example.com/hello", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"go.mod",
		"Dockerfile",
		"Makefile",
		"hello.api",
		filepath.Join("cmd", "hello", "main.go"),
		filepath.Join("internal", "routes", "routes.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected api new file %s: %v", rel, err)
		}
	}
	for _, rel := range []string{
		filepath.Join("etc", "governance.json"),
		filepath.Join("internal", "rpc", "greeter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("api new basic should not generate production-only file %s", rel)
		}
	}
}

func TestExecuteAPINewWithGoZeroCompatibleProfile(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"api", "new", "hello", "--module", "example.com/hello", "--dir", dir, "--profile", "gozero-compatible"}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		filepath.Join("cmd", "hello", "main.go"),
		filepath.Join("internal", "config", "config.go"),
		filepath.Join("internal", "handler", "pinghandler.go"),
		filepath.Join("internal", "logic", "pinglogic.go"),
		filepath.Join("internal", "svc", "servicecontext.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected gozero-compatible API file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "routes", "routes.go")); err == nil {
		t.Fatal("gozero-compatible API profile should not generate legacy routes.go")
	}
	cfg, err := generator.LoadConfig(filepath.Join(dir, generator.DefaultConfigFile))
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if cfg.API == nil || cfg.API.Profile != string(generator.ProfileGoZeroCompatible) {
		t.Fatalf("generated api profile = %#v, want gozero-compatible", cfg.API)
	}
}

func TestExecuteAPINewUsesConfigProfileDefault(t *testing.T) {
	dir := t.TempDir()
	cfg := generator.DefaultConfig("hello", "example.com/hello")
	cfg.API.Profile = string(generator.ProfileGoZeroCompatible)
	if err := generator.SaveConfig(filepath.Join(dir, generator.DefaultConfigFile), cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"api", "new", "--config", filepath.Join(dir, generator.DefaultConfigFile), "--dir", dir, "--json"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "handler", "pinghandler.go")); err != nil {
		t.Fatalf("expected gozero-compatible API scaffold from config profile: %v", err)
	}
	assertNewEnvelopeInput(t, stdout.Bytes(), "new.api", "new api", "profile", string(generator.ProfileGoZeroCompatible))
	got, err := generator.LoadConfig(filepath.Join(dir, generator.DefaultConfigFile))
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got.API == nil || got.API.Profile != string(generator.ProfileGoZeroCompatible) {
		t.Fatalf("persisted api profile = %#v, want gozero-compatible", got.API)
	}
}

func TestExecuteAPINewRejectsUnknownProfile(t *testing.T) {
	dir := t.TempDir()
	err := Execute([]string{"api", "new", "hello", "--module", "example.com/hello", "--dir", dir, "--profile", "unknown-profile"})
	if err == nil || !strings.Contains(err.Error(), "unknown generation profile") {
		t.Fatalf("Execute err = %v, want unknown generation profile", err)
	}
}

func TestExecuteAPINewAcceptsGoctlReservedFlags(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{
		"new", "api", "hello",
		"-module", "example.com/hello",
		"-dir", dir,
		"-style", "go_zero",
		"-home", filepath.Join(dir, "templates"),
		"-remote", "https://example.invalid/templates.git",
		"-branch", "main",
		"-idea",
		"-client=false",
		"-verbose",
		"-name-from-filename",
		"-go_opt", "paths=source_relative",
		"-profile", "gozero-compatible",
	}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"go.mod",
		"hello.api",
		filepath.Join("cmd", "hello", "main.go"),
		filepath.Join("internal", "handler", "pinghandler.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected api new file %s with accepted extra flags: %v", rel, err)
		}
	}
}

func TestExecuteNewAcceptsGoctlHomeAlias(t *testing.T) {
	dir := t.TempDir()
	templateDir := filepath.Join(dir, "templates")
	apiDir := filepath.Join(dir, "api")
	if err := Execute([]string{"new", "api", "hello", "--module", "example.com/hello", "--dir", apiDir, "--home", templateDir}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(apiDir, ".gofly", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), templateDir) {
		t.Fatalf("new api config should persist --home as template dir: %s", data)
	}

	rpcDir := filepath.Join(dir, "rpc")
	if err := Execute([]string{"rpc", "new", "greeter", "--module", "example.com/greeter", "--dir", rpcDir, "--home", templateDir}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(rpcDir, ".gofly", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), templateDir) {
		t.Fatalf("rpc new config should persist --home as template dir: %s", data)
	}
}

func TestExecuteNewDefaultConfigPathUsesResolvedOutputDir(t *testing.T) {
	sandbox := t.TempDir()
	t.Chdir(sandbox)

	if err := Execute([]string{"new", "api", "hello", "--module", "example.com/hello"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sandbox, "hello", ".gofly", "config.json")); err != nil {
		t.Fatalf("new api should persist config under generated service dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sandbox, ".gofly", "config.json")); err == nil {
		t.Fatal("new api wrote config under current directory, want generated service directory")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat current directory config: %v", err)
	}

	if err := Execute([]string{"new", "rpc", "greeter", "--module", "example.com/greeter"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sandbox, "greeter", ".gofly", "config.json")); err != nil {
		t.Fatalf("new rpc should persist config under generated service dir: %v", err)
	}
}

func TestExecuteNewDefaultEcosystemCompatibilityFeature(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"new", "api", "hello", "--module", "example.com/hello", "--dir", dir}); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		filepath.Join("internal", "compat", "gozero", "adapter.go"),
		filepath.Join("internal", "compat", "kitex", "adapter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("default ecosystem compatibility should generate %s: %v", rel, err)
		}
	}

	cfg, err := generator.LoadConfig(filepath.Join(dir, ".gofly", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cfg.Features, ","), "ecosystem-compat") {
		t.Fatalf("saved config features = %v, want ecosystem-compat", cfg.Features)
	}
}

func TestExecuteNewMergesConfigAndCLICompatibilityFeatures(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := generator.SaveConfig(configPath, &generator.Config{
		ServiceName: "hello",
		Module:      "example.com/hello",
		Style:       generator.ServiceStyleMinimal,
		Features:    []string{"http-compat"},
	}); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{
		"new", "api", "hello",
		"--config", configPath,
		"--dir", outDir,
		"--feature", "rpc-compat",
	}); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{
		filepath.Join("internal", "compat", "gozero", "adapter.go"),
		filepath.Join("internal", "compat", "kitex", "adapter.go"),
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Fatalf("merged config and CLI features should generate %s: %v", rel, err)
		}
	}

	cfg, err := generator.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	features := strings.Join(cfg.Features, ",")
	for _, want := range []string{"http-compat", "rpc-compat"} {
		if !strings.Contains(features, want) {
			t.Fatalf("saved config features = %v, want %s", cfg.Features, want)
		}
	}
}

func TestExecuteNewMergesDiscoveryCLIOverlay(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := generator.SaveConfig(configPath, &generator.Config{
		ServiceName: "greeter",
		Module:      "example.com/greeter",
		Style:       generator.ServiceStyleProduction,
		Discovery:   &generator.DiscoveryConfig{Provider: "memory", TTL: "15s"},
	}); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{
		"new", "rpc", "greeter",
		"--config", configPath,
		"--dir", outDir,
		"--discovery", "etcdv3",
		"--discovery-endpoints", "127.0.0.1:2379,127.0.0.2:2379",
		"--discovery-prefix", "/gofly/test",
		"--discovery-ttl", "30s",
		"--discovery-dial-timeout", "2s",
		"--discovery-username-env", "ETCD_USERNAME",
		"--discovery-password-env", "ETCD_PASSWORD",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "internal", "discovery", "registry.go")); err != nil {
		t.Fatalf("expected generated discovery registry helper: %v", err)
	}
	cfg, err := generator.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Discovery == nil || cfg.Discovery.Provider != "etcdv3" || strings.Join(cfg.Discovery.Endpoints, ",") != "127.0.0.1:2379,127.0.0.2:2379" || cfg.Discovery.Prefix != "/gofly/test" || cfg.Discovery.TTL != "30s" || cfg.Discovery.DialTimeout != "2s" || cfg.Discovery.UsernameEnv != "ETCD_USERNAME" || cfg.Discovery.PasswordEnv != "ETCD_PASSWORD" {
		t.Fatalf("saved discovery config = %#v, want CLI overlay", cfg.Discovery)
	}

	mainData, err := os.ReadFile(filepath.Join(outDir, "cmd", "greeter", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`appdiscovery "example.com/greeter/internal/discovery"`,
		"appdiscovery.NewRegistry(ctx, c.Discovery)",
		"rpc.NewDiscoveryRegistrar(registry, c.Discovery.RegisterOptions()...)",
	} {
		if !strings.Contains(string(mainData), want) {
			t.Fatalf("generated main.go missing discovery wiring %q:\n%s", want, mainData)
		}
	}
}

func TestExecuteFeaturePreviewEcosystemCompatibility(t *testing.T) {
	listOut := captureStdout(t, func() {
		if err := Execute([]string{"feature", "list"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"ecosystem-compat", "http-compat", "rpc-compat"} {
		if !strings.Contains(listOut, want) {
			t.Fatalf("feature list missing %q:\n%s", want, listOut)
		}
	}
	for _, removed := range []string{"go-zero", "gozero", "kitex"} {
		if strings.Contains(listOut, removed) {
			t.Fatalf("feature list should not expose removed compatibility alias %q:\n%s", removed, listOut)
		}
	}

	runOut := captureStdout(t, func() {
		if err := Execute([]string{
			"feature", "run", "ecosystem-compat",
			"--name", "hello",
			"--module", "example.com/hello",
		}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		filepath.Join("internal", "compat", "gozero", "adapter.go"),
		filepath.Join("internal", "compat", "kitex", "adapter.go"),
	} {
		if !strings.Contains(runOut, want) {
			t.Fatalf("feature run missing %q:\n%s", want, runOut)
		}
	}
}

func TestExecuteFeaturePreviewJSONAndMultipleFeatures(t *testing.T) {
	listJSON := captureStdout(t, func() {
		if err := Execute([]string{"feature", "list", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var listed struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Features []string `json:"features"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(listJSON), &listed); err != nil {
		t.Fatalf("feature list --json output = %s, error = %v", listJSON, err)
	}
	if !listed.OK || listed.Command != "feature.list" {
		t.Fatalf("feature list --json envelope = %+v, want ok feature.list", listed)
	}
	for _, want := range []string{"ecosystem-compat", "http-compat", "rpc-compat"} {
		if !commandContainsString(listed.Data.Features, want) {
			t.Fatalf("feature list --json features = %v, want %s", listed.Data.Features, want)
		}
	}
	for _, removed := range []string{"go-zero", "gozero", "kitex"} {
		if commandContainsString(listed.Data.Features, removed) {
			t.Fatalf("feature list --json should not expose removed compatibility alias %q: %v", removed, listed.Data.Features)
		}
	}

	runJSON := captureStdout(t, func() {
		if err := Execute([]string{
			"feature", "run", "http-compat", "rpc-compat",
			"--name", "hello",
			"--module", "example.com/hello",
			"--format", "json",
		}); err != nil {
			t.Fatal(err)
		}
	})
	var preview struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Features []string `json:"features"`
			Files    []struct {
				Path  string `json:"path"`
				Bytes int    `json:"bytes"`
			} `json:"files"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(runJSON), &preview); err != nil {
		t.Fatalf("feature run --format json output = %s, error = %v", runJSON, err)
	}
	if !preview.OK || preview.Command != "feature.run" {
		t.Fatalf("feature run JSON envelope = %+v, want ok feature.run", preview)
	}
	if strings.Join(preview.Data.Features, ",") != "http-compat,rpc-compat" {
		t.Fatalf("feature run JSON features = %v, want http-compat,rpc-compat", preview.Data.Features)
	}
	paths := make([]string, 0, len(preview.Data.Files))
	for _, file := range preview.Data.Files {
		paths = append(paths, file.Path)
		if file.Bytes == 0 {
			t.Fatalf("feature preview file %s has zero bytes", file.Path)
		}
	}
	for _, want := range []string{
		filepath.Join("internal", "compat", "gozero", "adapter.go"),
		filepath.Join("internal", "compat", "kitex", "adapter.go"),
	} {
		if !commandContainsString(paths, want) {
			t.Fatalf("feature run JSON paths = %v, want %s", paths, want)
		}
	}
}

func TestExecuteNewAcceptsFeaturesAlias(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{
		"new", "api", "hello",
		"--module", "example.com/hello",
		"--dir", dir,
		"--features", "http-compat,rpc-compat",
	}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		filepath.Join("internal", "compat", "gozero", "adapter.go"),
		filepath.Join("internal", "compat", "kitex", "adapter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("new api --features should generate %s: %v", rel, err)
		}
	}
}

func TestExecuteFeatureRejectsRemovedCompatibilityAliases(t *testing.T) {
	for _, feature := range []string{"go-zero", "gozero", "kitex"} {
		t.Run(feature, func(t *testing.T) {
			err := Execute([]string{
				"feature", "run", feature,
				"--name", "hello",
				"--module", "example.com/hello",
			})
			if err == nil || !strings.Contains(err.Error(), `feature "`+feature+`" is not registered`) {
				t.Fatalf("feature run %q error = %v, want unregistered feature", feature, err)
			}
		})
	}
}

func TestExecuteFeatureJSONErrorEnvelope(t *testing.T) {
	jsonOut := captureStdout(t, func() {
		err := Execute([]string{
			"feature", "run", "go-zero",
			"--name", "hello",
			"--module", "example.com/hello",
			"--json",
		})
		if err == nil {
			t.Fatal("feature run removed alias should fail")
		}
	})
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Error   struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			Retryable   bool   `json:"retryable"`
			Remediation string `json:"remediation"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &envelope); err != nil {
		t.Fatalf("feature run --json error output = %s, error = %v", jsonOut, err)
	}
	if envelope.OK || envelope.Command != "feature.run" || envelope.Error.Code != "FEATURE_NOT_REGISTERED" || envelope.Error.Retryable {
		t.Fatalf("feature run error envelope = %+v", envelope)
	}
	if !strings.Contains(envelope.Error.Message, `feature "go-zero" is not registered`) || !strings.Contains(envelope.Error.Remediation, "http-compat") {
		t.Fatalf("feature run error envelope missing actionable message: %+v", envelope)
	}
}

func TestExecuteVerbosityControlsOutputStreams(t *testing.T) {
	t.Run("global quiet suppresses feature text output", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if err := ExecuteWithIO([]string{"--quiet", "feature", "list"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
			t.Fatal(err)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("quiet feature list stdout=%q stderr=%q, want both empty", stdout.String(), stderr.String())
		}
	})

	t.Run("local verbose writes diagnostics to stderr only", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		dir := t.TempDir()
		if err := ExecuteWithIO([]string{"new", "api", "hello", "--module", "example.com/hello", "--dir", dir, "--verbose"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
			t.Fatal(err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("verbose new api stdout = %q, want empty", stdout.String())
		}
		if !strings.Contains(stderr.String(), `new api: configuring service "hello"`) {
			t.Fatalf("verbose new api stderr = %q, want diagnostic", stderr.String())
		}
	})
}

func TestExecuteAIManifestJSONEnvelope(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := ExecuteWithIO([]string{"ai", "manifest", "--format", "json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("ai manifest stderr = %q, want empty", stderr.String())
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Version string `json:"version"`
		Data    struct {
			SchemaVersion string `json:"schemaVersion"`
			Tool          string `json:"tool"`
			Output        struct {
				Envelope    []string `json:"envelope"`
				ErrorFields []string `json:"errorFields"`
			} `json:"output"`
			LLMGovernance struct {
				Package           string   `json:"package"`
				Capabilities      []string `json:"capabilities"`
				Resilience        []string `json:"resilience"`
				TokenBudgetPolicy struct {
					DefaultMaxInputTokens  int      `json:"defaultMaxInputTokens"`
					DefaultMaxOutputTokens int      `json:"defaultMaxOutputTokens"`
					DefaultMaxTotalTokens  int      `json:"defaultMaxTotalTokens"`
					Configurable           bool     `json:"configurable"`
					CLIFlags               []string `json:"cliFlags"`
					EnvVars                []string `json:"envVars"`
					Enforcement            string   `json:"enforcement"`
					DeductionPoint         string   `json:"deductionPoint"`
					FailoverBudgetSharing  string   `json:"failoverBudgetSharing"`
					StreamAccounting       string   `json:"streamAccounting"`
					RejectionCode          string   `json:"rejectionCode"`
				} `json:"tokenBudgetPolicy"`
				RateLimitPolicy struct {
					DefaultRate  int    `json:"defaultRate"`
					DefaultBurst int    `json:"defaultBurst"`
					EnvVarRate   string `json:"envVarRate"`
					EnvVarBurst  string `json:"envVarBurst"`
					Strategy     string `json:"strategy"`
					Consequence  string `json:"consequence"`
					Configurable bool   `json:"configurable"`
					Scope        string `json:"scope"`
				} `json:"rateLimitPolicy"`
				OutputContractPolicy struct {
					EnvelopeFields          []string `json:"envelopeFields"`
					ErrorFields             []string `json:"errorFields"`
					NextActions             bool     `json:"nextActions"`
					JSONMode                string   `json:"jsonMode"`
					SchemaValidation        string   `json:"schemaValidation"`
					RetryableErrorSemantics string   `json:"retryableErrorSemantics"`
					StreamSemantics         string   `json:"streamSemantics"`
					PartialFailureSemantics string   `json:"partialFailureSemantics"`
				} `json:"outputContractPolicy"`
				ErrorContractPolicy struct {
					CodeFormat              string   `json:"codeFormat"`
					StableCodes             []string `json:"stableCodes"`
					RetryableCodes          []string `json:"retryableCodes"`
					NonRetryableCodes       []string `json:"nonRetryableCodes"`
					ProviderStatusClasses   []string `json:"providerStatusClasses"`
					NextActionTypes         []string `json:"nextActionTypes"`
					EnvelopePlacement       string   `json:"envelopePlacement"`
					DetailsPolicy           string   `json:"detailsPolicy"`
					RetryableSemantics      string   `json:"retryableSemantics"`
					ProviderFailureGuidance string   `json:"providerFailureGuidance"`
				} `json:"errorContractPolicy"`
				DataSafetyPolicy struct {
					SecretResolution    string   `json:"secretResolution"`
					Redaction           string   `json:"redaction"`
					PromptLogging       string   `json:"promptLogging"`
					ResponseLogging     string   `json:"responseLogging"`
					MetadataLogging     string   `json:"metadataLogging"`
					SecretValueLogging  string   `json:"secretValueLogging"`
					SensitiveEnvVarMode string   `json:"sensitiveEnvVarMode"`
					AuditBoundary       string   `json:"auditBoundary"`
					SafeToExpose        []string `json:"safeToExpose"`
				} `json:"dataSafetyPolicy"`
				ToolCallPolicy struct {
					DefaultMode                     string   `json:"defaultMode"`
					RequiresModelCapability         string   `json:"requiresModelCapability"`
					AllowedByDefault                []string `json:"allowedByDefault"`
					SideEffectToolsRequireApproval  bool     `json:"sideEffectToolsRequireApproval"`
					ArgumentSchemaValidation        bool     `json:"argumentSchemaValidation"`
					DryRunRequiredForMutation       bool     `json:"dryRunRequiredForMutation"`
					AuditToolArguments              string   `json:"auditToolArguments"`
					RejectedToolCallCode            string   `json:"rejectedToolCallCode"`
					UnsupportedCapabilityResolution string   `json:"unsupportedCapabilityResolution"`
				} `json:"toolCallPolicy"`
				ResponseCachePolicy struct {
					DefaultTTL         string   `json:"defaultTTL"`
					DefaultMaxSize     float64  `json:"defaultMaxSize"`
					CacheKeyComponents []string `json:"cacheKeyComponents"`
					Hash               string   `json:"hash"`
					Coalescing         string   `json:"coalescing"`
					Observable         bool     `json:"observable"`
					CacheScope         string   `json:"cacheScope"`
					CacheUnsupported   []string `json:"cacheUnsupported"`
				} `json:"responseCachePolicy"`
				ObservabilityPolicy struct {
					Signals                []string `json:"signals"`
					LowCardinalityFields   []string `json:"lowCardinalityFields"`
					ForbiddenFields        []string `json:"forbiddenFields"`
					CorrelationFields      []string `json:"correlationFields"`
					MetricFieldGuidance    string   `json:"metricFieldGuidance"`
					TraceFieldGuidance     string   `json:"traceFieldGuidance"`
					AuditCorrelation       string   `json:"auditCorrelation"`
					RedactionBoundary      string   `json:"redactionBoundary"`
					CardinalityGuardrails  string   `json:"cardinalityGuardrails"`
					ProviderStatusGuidance string   `json:"providerStatusGuidance"`
				} `json:"observabilityPolicy"`
				CostPolicy struct {
					AccountingFields       []string `json:"accountingFields"`
					BudgetFields           []string `json:"budgetFields"`
					CurrencyMode           string   `json:"currencyMode"`
					PricingSource          string   `json:"pricingSource"`
					CostDisclosure         string   `json:"costDisclosure"`
					FailoverDisclosure     string   `json:"failoverDisclosure"`
					CacheAccounting        string   `json:"cacheAccounting"`
					AgentGuidance          []string `json:"agentGuidance"`
					UnpricedProviderPolicy string   `json:"unpricedProviderPolicy"`
				} `json:"costPolicy"`
				GovernancePipeline []struct {
					Stage       string `json:"stage"`
					Description string `json:"description"`
					Optional    bool   `json:"optional"`
				} `json:"governancePipeline"`
				FailoverPolicy struct {
					EnvVar                string   `json:"envVar"`
					Mode                  string   `json:"mode"`
					AutomaticSwitching    bool     `json:"automaticSwitching"`
					ConfiguredProviders   []string `json:"configuredProviders"`
					EligibleCompleteSpecs []struct {
						Name         string   `json:"name"`
						Capabilities []string `json:"capabilities"`
					} `json:"eligibleCompleteSpecs"`
					EligibleStreamSpecs []struct {
						Name         string   `json:"name"`
						Capabilities []string `json:"capabilities"`
					} `json:"eligibleStreamSpecs"`
				} `json:"failoverPolicy"`
				AuditFields     []string `json:"auditFields"`
				TelemetryFields []string `json:"telemetryFields"`
				DefaultMode     string   `json:"defaultMode"`
				Providers       []struct {
					Name            string   `json:"name"`
					NetworkAccess   bool     `json:"networkAccess"`
					RequiresSecrets bool     `json:"requiresSecrets"`
					SecretEnvVars   []string `json:"secretEnvVars"`
					ConfigEnvVars   []string `json:"configEnvVars"`
					Capabilities    []string `json:"capabilities"`
				} `json:"providers"`
			} `json:"llmGovernance"`
			Commands []struct {
				Name              string   `json:"name"`
				Aliases           []string `json:"aliases"`
				RiskLevel         string   `json:"riskLevel"`
				SupportsDryRun    bool     `json:"supportsDryRun"`
				MutatesFilesystem bool     `json:"mutatesFilesystem"`
				OutputFormats     []string `json:"outputFormats"`
				OutputContract    struct {
					Mode        string            `json:"mode"`
					Envelope    []string          `json:"envelope"`
					EventFields []string          `json:"eventFields"`
					Semantics   map[string]string `json:"semantics"`
				} `json:"outputContract"`
			} `json:"commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("ai manifest output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.Command != "ai.manifest" || envelope.Version == "" {
		t.Fatalf("ai manifest envelope = %+v", envelope)
	}
	if envelope.Data.SchemaVersion != aiToolManifestSchemaVersion || envelope.Data.Tool != "gofly" {
		t.Fatalf("ai manifest metadata = %+v", envelope.Data)
	}
	if envelope.Data.LLMGovernance.Package != "github.com/imajinyun/gofly/core/llm" || !commandContainsString(envelope.Data.LLMGovernance.Capabilities, "token budget") || !commandContainsString(envelope.Data.LLMGovernance.Capabilities, "response caching") || !commandContainsString(envelope.Data.LLMGovernance.Capabilities, "cost-aware token accounting") || !commandContainsString(envelope.Data.LLMGovernance.Capabilities, "low-cardinality observability") || !commandContainsString(envelope.Data.LLMGovernance.Capabilities, "governance pipeline") || !commandContainsString(envelope.Data.LLMGovernance.Resilience, "circuit breaker") || !commandContainsString(envelope.Data.LLMGovernance.Resilience, "provider failover") || !commandContainsString(envelope.Data.LLMGovernance.Resilience, "retryability classification") || !commandContainsString(envelope.Data.LLMGovernance.Resilience, "request coalescing") || !commandContainsString(envelope.Data.LLMGovernance.AuditFields, "total_tokens") || !commandContainsString(envelope.Data.LLMGovernance.AuditFields, "error_class") || !commandContainsString(envelope.Data.LLMGovernance.TelemetryFields, "provider_status_code") || !commandContainsString(envelope.Data.LLMGovernance.TelemetryFields, "cache_status") || !commandContainsString(envelope.Data.LLMGovernance.TelemetryFields, "total_tokens") {
		t.Fatalf("ai manifest LLM governance = %+v", envelope.Data.LLMGovernance)
	}
	if envelope.Data.LLMGovernance.FailoverPolicy.EnvVar != "GOFLY_LLM_FAILOVER_PROVIDERS" || envelope.Data.LLMGovernance.FailoverPolicy.Mode != "disabled" || envelope.Data.LLMGovernance.FailoverPolicy.AutomaticSwitching || len(envelope.Data.LLMGovernance.FailoverPolicy.EligibleCompleteSpecs) != 2 || len(envelope.Data.LLMGovernance.FailoverPolicy.EligibleStreamSpecs) != 2 {
		t.Fatalf("ai manifest failover policy = %+v", envelope.Data.LLMGovernance.FailoverPolicy)
	}
	tbp := envelope.Data.LLMGovernance.TokenBudgetPolicy
	if !tbp.Configurable || tbp.DefaultMaxInputTokens != 0 || tbp.DefaultMaxOutputTokens != 0 || tbp.DefaultMaxTotalTokens != 0 || tbp.RejectionCode != "token_budget_exceeded" || !commandContainsString(tbp.CLIFlags, "--max-total-tokens") || !commandContainsString(tbp.EnvVars, "GOFLY_LLM_MAX_TOTAL_TOKENS") || !strings.Contains(tbp.FailoverBudgetSharing, "same TokenBudget") || !strings.Contains(tbp.StreamAccounting, "usage snapshots") {
		t.Fatalf("ai manifest token budget policy = %+v", tbp)
	}
	rlp := envelope.Data.LLMGovernance.RateLimitPolicy
	if rlp.Strategy != "token-bucket" || !rlp.Configurable || rlp.EnvVarRate != "GOFLY_LLM_RATE_LIMIT" || rlp.EnvVarBurst != "GOFLY_LLM_RATE_BURST" || rlp.DefaultRate != 0 || rlp.DefaultBurst != 0 || rlp.Scope == "" {
		t.Fatalf("ai manifest rate limit policy = %+v", rlp)
	}
	ocp := envelope.Data.LLMGovernance.OutputContractPolicy
	if !ocp.NextActions || !commandContainsString(ocp.EnvelopeFields, "nextActions") || !commandContainsString(ocp.ErrorFields, "retryable") || !strings.Contains(ocp.JSONMode, "--json") || !strings.Contains(ocp.StreamSemantics, "newline-delimited JSON") || !strings.Contains(ocp.PartialFailureSemantics, "final error envelope") {
		t.Fatalf("ai manifest output contract policy = %+v", ocp)
	}
	ecp := envelope.Data.LLMGovernance.ErrorContractPolicy
	if !strings.Contains(ecp.CodeFormat, "UPPER_SNAKE_CASE") || !commandContainsString(ecp.StableCodes, "LLM_PROVIDER_REQUEST_FAILED") || !commandContainsString(ecp.RetryableCodes, "LLM_RATE_LIMITED") || !commandContainsString(ecp.NonRetryableCodes, "LLM_PROVIDER_SECRET_MISSING") || !commandContainsString(ecp.ProviderStatusClasses, "rate_limit") || !commandContainsString(ecp.NextActionTypes, "enable_failover") || !strings.Contains(ecp.DetailsPolicy, "raw provider bodies") || !strings.Contains(ecp.RetryableSemantics, "retryable=true") {
		t.Fatalf("ai manifest error contract policy = %+v", ecp)
	}
	dsp := envelope.Data.LLMGovernance.DataSafetyPolicy
	if dsp.PromptLogging != "disabled-by-default" || dsp.ResponseLogging != "disabled-by-default" || dsp.SecretValueLogging != "forbidden" || !strings.Contains(dsp.SecretResolution, "environment-only") || !strings.Contains(dsp.Redaction, "redacted") || !commandContainsString(dsp.SafeToExpose, "environment variable names") {
		t.Fatalf("ai manifest data safety policy = %+v", dsp)
	}
	tcp := envelope.Data.LLMGovernance.ToolCallPolicy
	if tcp.RequiresModelCapability != "tool-call" || len(tcp.AllowedByDefault) != 0 || !tcp.SideEffectToolsRequireApproval || !tcp.ArgumentSchemaValidation || !tcp.DryRunRequiredForMutation || tcp.AuditToolArguments != "redacted" || tcp.RejectedToolCallCode != "tool_call_rejected" {
		t.Fatalf("ai manifest tool call policy = %+v", tcp)
	}
	rcp := envelope.Data.LLMGovernance.ResponseCachePolicy
	if rcp.DefaultTTL != "5m" || rcp.DefaultMaxSize != 256 || rcp.Hash != "SHA-256" || !rcp.Observable || !commandContainsString(rcp.CacheKeyComponents, "provider") || !commandContainsString(rcp.CacheKeyComponents, "prompt") || !commandContainsString(rcp.CacheKeyComponents, "maxOutputTokens") || !commandContainsString(rcp.CacheUnsupported, "stream") || !commandContainsString(rcp.CacheUnsupported, "embed") || !strings.Contains(rcp.Coalescing, "request-level") || !strings.Contains(rcp.CacheScope, "in-process") {
		t.Fatalf("ai manifest response cache policy = %+v", rcp)
	}
	op := envelope.Data.LLMGovernance.ObservabilityPolicy
	if !commandContainsString(op.Signals, "structured audit log") || !commandContainsString(op.LowCardinalityFields, "error_class") || !commandContainsString(op.LowCardinalityFields, "cache_status") || !commandContainsString(op.ForbiddenFields, "prompt") || !commandContainsString(op.CorrelationFields, "trace_id") || !strings.Contains(op.MetricFieldGuidance, "low-cardinality") || !strings.Contains(op.RedactionBoundary, "provider calls") || !strings.Contains(op.ProviderStatusGuidance, "provider_status_code") {
		t.Fatalf("ai manifest observability policy = %+v", op)
	}
	cp := envelope.Data.LLMGovernance.CostPolicy
	if !commandContainsString(cp.AccountingFields, "total_tokens") || !commandContainsString(cp.AccountingFields, "failover_attempt") || !commandContainsString(cp.BudgetFields, "remain_total") || !strings.Contains(cp.CurrencyMode, "disabled-by-default") || !strings.Contains(cp.PricingSource, "operator-maintained") || !strings.Contains(cp.FailoverDisclosure, "additive") || !strings.Contains(cp.CacheAccounting, "cache hits") || !commandContainsString(cp.AgentGuidance, "do not fabricate currency costs for unpriced providers") || !strings.Contains(cp.UnpricedProviderPolicy, "unpriced") {
		t.Fatalf("ai manifest cost policy = %+v", cp)
	}
	gp := envelope.Data.LLMGovernance.GovernancePipeline
	if len(gp) != 9 || gp[0].Stage != "request-redaction" || !gp[0].Optional || gp[4].Stage != "circuit-breaker" || gp[4].Optional || gp[5].Stage != "provider-call" || gp[8].Stage != "telemetry-emit" || gp[8].Optional {
		t.Fatalf("ai manifest governance pipeline = %+v", gp)
	}
	if len(envelope.Data.LLMGovernance.Providers) != 2 || envelope.Data.LLMGovernance.Providers[0].Name != "noop" || envelope.Data.LLMGovernance.Providers[0].RequiresSecrets || !commandContainsString(envelope.Data.LLMGovernance.Providers[0].Capabilities, "offline") {
		t.Fatalf("ai manifest providers = %+v", envelope.Data.LLMGovernance.Providers)
	}
	openAIProvider := envelope.Data.LLMGovernance.Providers[1]
	if openAIProvider.Name != "openai-compatible" || !openAIProvider.NetworkAccess || !openAIProvider.RequiresSecrets || !commandContainsString(openAIProvider.SecretEnvVars, "GOFLY_LLM_OPENAI_API_KEY") || !commandContainsString(openAIProvider.ConfigEnvVars, "GOFLY_LLM_OPENAI_BASE_URL") || !commandContainsString(openAIProvider.ConfigEnvVars, "GOFLY_LLM_OPENAI_MAX_RESPONSE_BYTES") || !commandContainsString(openAIProvider.Capabilities, "chat-completions") || !commandContainsString(openAIProvider.Capabilities, "stream") {
		t.Fatalf("ai manifest openai-compatible provider = %+v", openAIProvider)
	}
	if strings.Contains(stdout.String(), "super-secret") || strings.Contains(stdout.String(), "apiKey") {
		t.Fatalf("ai manifest leaked secret-like content: %s", stdout.String())
	}
	for _, want := range []string{"ok", "command", "version", "data", "error", "nextActions"} {
		if !commandContainsString(envelope.Data.Output.Envelope, want) {
			t.Fatalf("ai manifest output envelope missing %q: %+v", want, envelope.Data.Output.Envelope)
		}
	}
	commands := map[string]struct {
		RiskLevel         string
		SupportsDryRun    bool
		MutatesFilesystem bool
		OutputFormats     []string
		OutputContract    struct {
			Mode        string            `json:"mode"`
			Envelope    []string          `json:"envelope"`
			EventFields []string          `json:"eventFields"`
			Semantics   map[string]string `json:"semantics"`
		}
	}{}
	for _, command := range envelope.Data.Commands {
		commands[command.Name] = struct {
			RiskLevel         string
			SupportsDryRun    bool
			MutatesFilesystem bool
			OutputFormats     []string
			OutputContract    struct {
				Mode        string            `json:"mode"`
				Envelope    []string          `json:"envelope"`
				EventFields []string          `json:"eventFields"`
				Semantics   map[string]string `json:"semantics"`
			}
		}{RiskLevel: command.RiskLevel, SupportsDryRun: command.SupportsDryRun, MutatesFilesystem: command.MutatesFilesystem, OutputFormats: command.OutputFormats, OutputContract: command.OutputContract}
	}
	for _, want := range []string{"ai complete", "ai manifest", "ai stream", "feature run", "new service", "new api", "plugin run", "version"} {
		if _, ok := commands[want]; !ok {
			t.Fatalf("ai manifest commands missing %q: %+v", want, commands)
		}
	}
	if !commands["ai complete"].SupportsDryRun || commands["ai complete"].MutatesFilesystem || commands["ai complete"].RiskLevel != "read" {
		t.Fatalf("ai complete manifest should advertise read-only dry-run support: %+v", commands["ai complete"])
	}
	if !commands["ai stream"].SupportsDryRun || commands["ai stream"].MutatesFilesystem || commands["ai stream"].RiskLevel != "read" || !commandContainsString(commands["ai stream"].OutputFormats, "json") {
		t.Fatalf("ai stream manifest should advertise read-only JSON stream support: %+v", commands["ai stream"])
	}
	if !strings.Contains(commands["ai complete"].OutputContract.Semantics["stream"], "ai stream output contract") || !commandContainsString(commands["ai stream"].OutputContract.Envelope, "error") || !commandContainsString(commands["ai stream"].OutputContract.EventFields, "delta") || commands["ai stream"].OutputContract.Semantics["command"] != "ai.stream" {
		t.Fatalf("ai manifest stream contract = complete:%+v stream:%+v", commands["ai complete"].OutputContract, commands["ai stream"].OutputContract)
	}
	if !commands["feature run"].SupportsDryRun || commands["feature run"].MutatesFilesystem {
		t.Fatalf("feature run manifest should be preview-only: %+v", commands["feature run"])
	}
	if commands["plugin run"].RiskLevel != "high" || !commands["plugin run"].MutatesFilesystem {
		t.Fatalf("plugin run manifest should expose high-risk filesystem mutation: %+v", commands["plugin run"])
	}
	for _, name := range []string{"new service", "new api", "new rpc", "plugin run", "config set"} {
		if !commands[name].SupportsDryRun {
			t.Fatalf("%s manifest should advertise dry-run support: %+v", name, commands[name])
		}
	}
}

func TestExecuteAIManifestExposesFailoverEnvPolicy(t *testing.T) {
	t.Setenv("GOFLY_LLM_FAILOVER_PROVIDERS", "openai-compatible, noop, missing-provider, noop")
	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"ai", "manifest", "--format", "json"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			LLMGovernance struct {
				ProviderPluginContract struct {
					SchemaVersion  string   `json:"schemaVersion"`
					RequiredFields []string `json:"requiredFields"`
				} `json:"providerPluginContract"`
				FailoverPolicy struct {
					Mode                string   `json:"mode"`
					AutomaticSwitching  bool     `json:"automaticSwitching"`
					ManualOptInFlags    []string `json:"manualOptInFlags"`
					ExecutionGuardrails []string `json:"executionGuardrails"`
					ConfiguredProviders []string `json:"configuredProviders"`
					InvalidProviders    []string `json:"invalidProviders"`
					ConfiguredSpecs     []struct {
						Name            string   `json:"name"`
						RequiresSecrets bool     `json:"requiresSecrets"`
						Capabilities    []string `json:"capabilities"`
						Models          []struct {
							Name         string   `json:"name"`
							Capabilities []string `json:"capabilities"`
						} `json:"models"`
					} `json:"configuredSpecs"`
					EligibleJSONModeSpecs []struct {
						Name string `json:"name"`
					} `json:"eligibleJSONModeSpecs"`
					EligibleToolCallSpecs []struct {
						Name string `json:"name"`
					} `json:"eligibleToolCallSpecs"`
				} `json:"failoverPolicy"`
			} `json:"llmGovernance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("ai manifest failover output is not valid JSON: %v\n%s", err, stdout.String())
	}
	policy := envelope.Data.LLMGovernance.FailoverPolicy
	if !envelope.OK || policy.Mode != "plan-only" || policy.AutomaticSwitching || !reflect.DeepEqual(policy.ConfiguredProviders, []string{"openai-compatible", "noop"}) || !reflect.DeepEqual(policy.InvalidProviders, []string{"missing-provider"}) {
		t.Fatalf("ai manifest failover policy = %+v", policy)
	}
	if envelope.Data.LLMGovernance.ProviderPluginContract.SchemaVersion != llm.ProviderPluginManifestSchemaVersion || !commandContainsString(envelope.Data.LLMGovernance.ProviderPluginContract.RequiredFields, "models[].capabilities") {
		t.Fatalf("ai manifest provider plugin contract = %+v", envelope.Data.LLMGovernance.ProviderPluginContract)
	}
	if !commandContainsString(policy.ManualOptInFlags, "--allow-failover") || !commandContainsString(policy.ExecutionGuardrails, "failover attempts share the same token budget") {
		t.Fatalf("ai manifest failover guardrails = %+v %+v", policy.ManualOptInFlags, policy.ExecutionGuardrails)
	}
	if len(policy.ConfiguredSpecs) != 2 || policy.ConfiguredSpecs[0].Name != "openai-compatible" || !policy.ConfiguredSpecs[0].RequiresSecrets || !commandContainsString(policy.ConfiguredSpecs[0].Capabilities, "stream") || len(policy.ConfiguredSpecs[0].Models) == 0 || !commandContainsString(policy.ConfiguredSpecs[0].Models[0].Capabilities, "tool-call") {
		t.Fatalf("ai manifest failover specs = %+v", policy.ConfiguredSpecs)
	}
	if len(policy.EligibleJSONModeSpecs) == 0 || len(policy.EligibleToolCallSpecs) == 0 {
		t.Fatalf("ai manifest model capability specs missing: json=%+v tool=%+v", policy.EligibleJSONModeSpecs, policy.EligibleToolCallSpecs)
	}
	if strings.Contains(stdout.String(), "super-secret") || strings.Contains(stdout.String(), "apiKey") {
		t.Fatalf("ai manifest failover leaked secret-like content: %s", stdout.String())
	}
}

func TestExecuteDryRunPlansDoNotMutate(t *testing.T) {
	t.Run("ai complete emits JSON plan without printing prompt", func(t *testing.T) {
		var stdout bytes.Buffer
		prompt := "email user@example.com token=secret"
		if err := ExecuteWithIO([]string{"ai", "complete", "--prompt", prompt, "--max-total-tokens", "128", "--dry-run", "--format", "json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Command           string            `json:"command"`
				DryRun            bool              `json:"dryRun"`
				MutatesFilesystem bool              `json:"mutatesFilesystem"`
				Inputs            map[string]string `json:"inputs"`
				Actions           []struct {
					Operation string `json:"operation"`
				} `json:"actions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete plan output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.complete" || envelope.Data.Command != "ai complete" || !envelope.Data.DryRun || envelope.Data.MutatesFilesystem || len(envelope.Data.Actions) == 0 {
			t.Fatalf("ai complete plan envelope = %+v", envelope)
		}
		if envelope.Data.Inputs["estimatedInputTokens"] == "" || envelope.Data.Inputs["maxTotalTokens"] != "128" {
			t.Fatalf("ai complete plan inputs = %+v", envelope.Data.Inputs)
		}
		if strings.Contains(stdout.String(), "user@example.com") || strings.Contains(stdout.String(), "token=secret") {
			t.Fatalf("ai complete dry-run leaked raw prompt: %s", stdout.String())
		}
	})

	t.Run("ai stream emits JSON event plan without printing prompt", func(t *testing.T) {
		var stdout bytes.Buffer
		prompt := "email user@example.com token=secret"
		if err := ExecuteWithIO([]string{"ai", "stream", "--prompt", prompt, "--max-total-tokens", "128", "--dry-run", "--format", "json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Command           string            `json:"command"`
				DryRun            bool              `json:"dryRun"`
				MutatesFilesystem bool              `json:"mutatesFilesystem"`
				Inputs            map[string]string `json:"inputs"`
				Actions           []struct {
					Operation string `json:"operation"`
				} `json:"actions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai stream plan output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.stream" || envelope.Data.Command != "ai stream" || !envelope.Data.DryRun || envelope.Data.MutatesFilesystem || len(envelope.Data.Actions) == 0 {
			t.Fatalf("ai stream plan envelope = %+v", envelope)
		}
		if envelope.Data.Inputs["estimatedInputTokens"] == "" || envelope.Data.Inputs["maxTotalTokens"] != "128" {
			t.Fatalf("ai stream plan inputs = %+v", envelope.Data.Inputs)
		}
		if strings.Contains(stdout.String(), "user@example.com") || strings.Contains(stdout.String(), "token=secret") {
			t.Fatalf("ai stream dry-run leaked raw prompt: %s", stdout.String())
		}
	})

	t.Run("ai complete stream emits JSON event plan without printing prompt", func(t *testing.T) {
		var stdout bytes.Buffer
		prompt := "email user@example.com token=secret"
		if err := ExecuteWithIO([]string{"ai", "complete", "--stream", "--prompt", prompt, "--max-total-tokens", "128", "--dry-run", "--format", "json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Command           string            `json:"command"`
				DryRun            bool              `json:"dryRun"`
				MutatesFilesystem bool              `json:"mutatesFilesystem"`
				Inputs            map[string]string `json:"inputs"`
				Actions           []struct {
					Operation string `json:"operation"`
				} `json:"actions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete --stream plan output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.complete" || envelope.Data.Command != "ai complete --stream" || !envelope.Data.DryRun || envelope.Data.MutatesFilesystem || len(envelope.Data.Actions) == 0 {
			t.Fatalf("ai complete --stream plan envelope = %+v", envelope)
		}
		if envelope.Data.Inputs["estimatedInputTokens"] == "" || envelope.Data.Inputs["maxTotalTokens"] != "128" {
			t.Fatalf("ai complete --stream plan inputs = %+v", envelope.Data.Inputs)
		}
		if strings.Contains(stdout.String(), "user@example.com") || strings.Contains(stdout.String(), "token=secret") {
			t.Fatalf("ai complete --stream dry-run leaked raw prompt: %s", stdout.String())
		}
	})

	t.Run("ai stream text dry-run plan prints governance info", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "stream", "--prompt", "hello", "--dry-run"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		out := stdout.String()
		for _, want := range []string{"ai.stream plan", "estimate-tokens", "apply-governance", "invoke-stream-provider", "dry-run", "next"} {
			if !strings.Contains(out, want) {
				t.Fatalf("ai stream text plan missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("ai complete openai-compatible plan exposes provider boundary without requiring secret", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "complete", "--prompt", "token=secret", "--provider", "openai-compatible", "--dry-run", "--format", "json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OK   bool `json:"ok"`
			Data struct {
				Inputs      map[string]string `json:"inputs"`
				Warnings    []string          `json:"warnings"`
				NextActions []string          `json:"nextActions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete openai plan output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Data.Inputs["networkAccess"] != "true" || envelope.Data.Inputs["requiresSecrets"] != "true" || envelope.Data.Inputs["secretSource"] != "environment" {
			t.Fatalf("ai complete openai plan inputs = %+v", envelope.Data.Inputs)
		}
		if !strings.Contains(envelope.Data.Inputs["providerSecretEnvVars"], "GOFLY_LLM_OPENAI_API_KEY") || !strings.Contains(envelope.Data.Inputs["providerConfigEnvVars"], "GOFLY_LLM_OPENAI_BASE_URL") || !strings.Contains(envelope.Data.Inputs["providerCapabilities"], "chat-completions") {
			t.Fatalf("ai complete openai provider plan = %+v", envelope.Data.Inputs)
		}
		if !commandContainsString(envelope.Data.Warnings, "provider credentials are resolved from environment variables only and are not read from .gofly/config.json") || !commandContainsString(envelope.Data.NextActions, "export the required provider secret environment variables before executing without --dry-run") {
			t.Fatalf("ai complete openai plan warnings/next = %+v %+v", envelope.Data.Warnings, envelope.Data.NextActions)
		}
		if strings.Contains(stdout.String(), "token=secret") || strings.Contains(stdout.String(), "Bearer") {
			t.Fatalf("ai complete openai dry-run leaked secret-like content: %s", stdout.String())
		}
	})

	t.Run("ai complete failover env is plan-only", func(t *testing.T) {
		t.Setenv("GOFLY_LLM_FAILOVER_PROVIDERS", "openai-compatible, noop")
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "complete", "--prompt", "hello token=secret", "--dry-run", "--format", "json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OK   bool `json:"ok"`
			Data struct {
				Inputs   map[string]string `json:"inputs"`
				Warnings []string          `json:"warnings"`
				Actions  []struct {
					Operation string `json:"operation"`
					Target    string `json:"target"`
				} `json:"actions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete failover plan output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Data.Inputs["failoverMode"] != "plan-only" || envelope.Data.Inputs["failoverProviders"] != "openai-compatible" || envelope.Data.Inputs["failoverAutomatic"] != "false" || envelope.Data.Inputs["failoverEnvVar"] != "GOFLY_LLM_FAILOVER_PROVIDERS" {
			t.Fatalf("ai complete failover plan inputs = %+v", envelope.Data.Inputs)
		}
		if !commandContainsString(envelope.Data.Warnings, "GOFLY_LLM_FAILOVER_PROVIDERS is advisory and only disclosed in plans/governance; automatic provider switching is intentionally disabled") {
			t.Fatalf("ai complete failover warnings = %+v", envelope.Data.Warnings)
		}
		var found bool
		for _, action := range envelope.Data.Actions {
			if action.Operation == "plan-provider-failover" && action.Target == "openai-compatible" {
				found = true
			}
		}
		if !found {
			t.Fatalf("ai complete failover action missing: %+v", envelope.Data.Actions)
		}
		if strings.Contains(stdout.String(), "token=secret") {
			t.Fatalf("ai complete failover dry-run leaked raw prompt: %s", stdout.String())
		}
	})

	t.Run("ai complete manual failover opt-in is explicit", func(t *testing.T) {
		t.Setenv("GOFLY_LLM_FAILOVER_PROVIDERS", "noop")
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "complete", "--prompt", "hello token=secret", "--provider", "openai-compatible", "--allow-failover", "--dry-run", "--format", "json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OK   bool `json:"ok"`
			Data struct {
				Inputs  map[string]string `json:"inputs"`
				Actions []struct {
					Operation   string `json:"operation"`
					Description string `json:"description"`
				} `json:"actions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete manual failover plan output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Data.Inputs["failoverMode"] != "manual" || envelope.Data.Inputs["failoverAllowed"] != "true" || envelope.Data.Inputs["failoverIdempotency"] == "not-enabled" {
			t.Fatalf("ai complete manual failover inputs = %+v", envelope.Data.Inputs)
		}
		var found bool
		for _, action := range envelope.Data.Actions {
			if action.Operation == "plan-provider-failover" && strings.Contains(action.Description, "shared budget") {
				found = true
			}
		}
		if !found {
			t.Fatalf("ai complete manual failover action missing: %+v", envelope.Data.Actions)
		}
		if strings.Contains(stdout.String(), "token=secret") {
			t.Fatalf("ai complete manual failover dry-run leaked raw prompt: %s", stdout.String())
		}
	})

	t.Run("new api emits JSON plan without writing scaffold", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "svc")
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"--output", "json", "new", "api", "hello", "--module", "example.com/hello", "--dir", dir, "--dry-run"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		assertDryRunPlan(t, stdout.Bytes(), "new.api", "new api")
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("new api --dry-run wrote go.mod or returned unexpected stat error: %v", err)
		}
	})

	t.Run("config init emits text plan without writing config", func(t *testing.T) {
		dir := t.TempDir()
		out := captureStdout(t, func() {
			if err := Execute([]string{"config", "init", "--dir", dir, "--name", "hello", "--module", "example.com/hello", "--dry-run"}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "config.init plan") || !strings.Contains(out, "write-config") {
			t.Fatalf("config init dry-run output missing plan details:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(dir, generator.DefaultConfigFile)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("config init --dry-run wrote config or returned unexpected stat error: %v", err)
		}
	})

	t.Run("plugin run emits JSON plan without executing plugin", func(t *testing.T) {
		dir := t.TempDir()
		marker := filepath.Join(dir, "executed")
		pluginPath := filepath.Join(dir, "plugin.sh")
		script := "#!/bin/sh\n" + "touch \"" + marker + "\"\n" + "printf '{\"version\":\"gofly.plugin.v1\",\"files\":[{\"path\":\"from-plugin.txt\",\"content\":\"ok\"}]}'\n"
		if err := os.WriteFile(pluginPath, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"plugin", "run", pluginPath, "--name", "hello", "--module", "example.com/hello", "--dir", dir, "--dry-run", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		assertDryRunPlan(t, stdout.Bytes(), "plugin.run", "plugin run")
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("plugin run --dry-run executed plugin or returned unexpected stat error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "from-plugin.txt")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("plugin run --dry-run wrote plugin output or returned unexpected stat error: %v", err)
		}
	})
}

func assertDryRunPlan(t *testing.T, data []byte, command, planCommand string) {
	t.Helper()
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Command           string `json:"command"`
			DryRun            bool   `json:"dryRun"`
			MutatesFilesystem bool   `json:"mutatesFilesystem"`
			Actions           []struct {
				Operation string `json:"operation"`
			} `json:"actions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("dry-run output is not valid JSON: %v\n%s", err, string(data))
	}
	if !envelope.OK || envelope.Command != command || envelope.Data.Command != planCommand || !envelope.Data.DryRun || !envelope.Data.MutatesFilesystem || len(envelope.Data.Actions) == 0 {
		t.Fatalf("dry-run envelope = %+v", envelope)
	}
}

func TestNewCommandsEmitJSONEnvelope(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		command     string
		planCommand string
		wantFile    string
	}{
		{
			name:        "new service",
			args:        []string{"new", "service", "orders"},
			command:     "new.service",
			planCommand: "new service",
			wantFile:    "go.mod",
		},
		{
			name:        "new api",
			args:        []string{"new", "api", "hello"},
			command:     "new.api",
			planCommand: "new api",
			wantFile:    "go.mod",
		},
		{
			name:        "new rpc",
			args:        []string{"new", "rpc", "greeter"},
			command:     "new.rpc",
			planCommand: "new rpc",
			wantFile:    "go.mod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "svc")
			args := append([]string{}, tt.args...)
			args = append(args, "--module", "example.com/svc", "--dir", dir, "--json")
			var stdout bytes.Buffer
			if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
				t.Fatalf("ExecuteWithIO(%v): %v", args, err)
			}
			var envelope struct {
				OK      bool   `json:"ok"`
				Command string `json:"command"`
				Data    struct {
					Command           string            `json:"command"`
					DryRun            bool              `json:"dryRun"`
					MutatesFilesystem bool              `json:"mutatesFilesystem"`
					Inputs            map[string]string `json:"inputs"`
					Actions           []struct {
						Operation string `json:"operation"`
					} `json:"actions"`
					NextActions []string `json:"nextActions"`
				} `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatalf("new command output is not valid JSON: %v\n%s", err, stdout.String())
			}
			if !envelope.OK || envelope.Command != tt.command || envelope.Data.Command != tt.planCommand || envelope.Data.DryRun || !envelope.Data.MutatesFilesystem {
				t.Fatalf("new command envelope = %+v, want applied JSON result", envelope)
			}
			if envelope.Data.Inputs["dir"] != dir || envelope.Data.Inputs["module"] != "example.com/svc" || len(envelope.Data.Actions) == 0 || len(envelope.Data.NextActions) == 0 {
				t.Fatalf("new command plan data = %+v, want stable automation fields", envelope.Data)
			}
			if _, err := os.Stat(filepath.Join(dir, tt.wantFile)); err != nil {
				t.Fatalf("new command did not write %s: %v", tt.wantFile, err)
			}
		})
	}
}

func TestIDLGenerateCommandsEmitJSONEnvelope(t *testing.T) {
	t.Run("api gen", func(t *testing.T) {
		dir := t.TempDir()
		apiPath := filepath.Join(dir, "user.api")
		api := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
		if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(dir, "out")
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"api", "gen", "--file", apiPath, "--dir", outDir, "--package", "handler", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		assertGenerateEnvelope(t, stdout.Bytes(), "api.gen", "api gen", outDir)
		if _, err := os.Stat(filepath.Join(outDir, "internal", "api", "v1", "types.go")); err != nil {
			t.Fatalf("api gen --json did not write generated file: %v", err)
		}
	})

	t.Run("api gen profile", func(t *testing.T) {
		dir := t.TempDir()
		apiPath := filepath.Join(dir, "user.api")
		api := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
		if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(dir, "out-profile")
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"api", "gen", "--file", apiPath, "--dir", outDir, "--profile", "gozero-compatible", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		assertGenerateEnvelope(t, stdout.Bytes(), "api.gen", "api gen", outDir)
		assertGenerateEnvelopeInput(t, stdout.Bytes(), "profile", string(generator.ProfileGoZeroCompatible))
		if _, err := os.Stat(filepath.Join(outDir, "internal", "api", "v1", "types.go")); err != nil {
			t.Fatalf("api gen --profile did not write generated file: %v", err)
		}
	})

	t.Run("api gen invalid profile", func(t *testing.T) {
		dir := t.TempDir()
		apiPath := filepath.Join(dir, "user.api")
		if err := os.WriteFile(apiPath, []byte(commandTestAPI), 0o644); err != nil {
			t.Fatal(err)
		}
		err := Execute([]string{"api", "gen", "--file", apiPath, "--dir", filepath.Join(dir, "out"), "--profile", "unknown-profile"})
		if err == nil || !strings.Contains(err.Error(), "unknown generation profile") {
			t.Fatalf("Execute err = %v, want unknown generation profile", err)
		}
	})

	t.Run("rpc gen", func(t *testing.T) {
		dir := t.TempDir()
		protoPath := filepath.Join(dir, "greeter.proto")
		if err := os.WriteFile(protoPath, []byte(commandTestProto), 0o644); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(dir, "out")
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"rpc", "gen", "--file", protoPath, "--dir", outDir, "--package", "greeterv1", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		assertGenerateEnvelope(t, stdout.Bytes(), "rpc.gen", "rpc gen", outDir)
		if _, err := os.Stat(filepath.Join(outDir, "greeter.grpc.gofly.go")); err != nil {
			t.Fatalf("rpc gen --json did not write generated file: %v", err)
		}
	})
}

func assertGenerateEnvelope(t *testing.T, data []byte, command, planCommand, dir string) {
	t.Helper()
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Command           string            `json:"command"`
			DryRun            bool              `json:"dryRun"`
			MutatesFilesystem bool              `json:"mutatesFilesystem"`
			Inputs            map[string]string `json:"inputs"`
			Actions           []struct {
				Operation string `json:"operation"`
			} `json:"actions"`
			GeneratedFiles int      `json:"generatedFiles"`
			NextActions    []string `json:"nextActions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("generate output is not valid JSON: %v\n%s", err, string(data))
	}
	if !envelope.OK || envelope.Command != command || envelope.Data.Command != planCommand || envelope.Data.DryRun || !envelope.Data.MutatesFilesystem {
		t.Fatalf("generate envelope = %+v, want applied JSON result", envelope)
	}
	if envelope.Data.Inputs["dir"] != dir || len(envelope.Data.Actions) == 0 || envelope.Data.Actions[0].Operation != "write-files" || len(envelope.Data.NextActions) == 0 {
		t.Fatalf("generate plan data = %+v, want stable automation fields", envelope.Data)
	}
	if envelope.Data.GeneratedFiles == 0 {
		t.Fatalf("generate plan data = %+v, want non-zero generatedFiles", envelope.Data)
	}
}

func assertGenerateEnvelopeInput(t *testing.T, data []byte, key, want string) {
	t.Helper()
	var envelope struct {
		Data struct {
			Inputs map[string]string `json:"inputs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("generate output is not valid JSON: %v\n%s", err, string(data))
	}
	if got := envelope.Data.Inputs[key]; got != want {
		t.Fatalf("generate input %q = %q, want %q; inputs=%v", key, got, want, envelope.Data.Inputs)
	}
}

func assertNewEnvelopeInput(t *testing.T, data []byte, command, planCommand, key, want string) {
	t.Helper()
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Command string            `json:"command"`
			Inputs  map[string]string `json:"inputs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("new command output is not valid JSON: %v\n%s", err, string(data))
	}
	if !envelope.OK || envelope.Command != command || envelope.Data.Command != planCommand {
		t.Fatalf("new command envelope = %+v, want %s/%s", envelope, command, planCommand)
	}
	if got := envelope.Data.Inputs[key]; got != want {
		t.Fatalf("new command input %q = %q, want %q; inputs=%v", key, got, want, envelope.Data.Inputs)
	}
}

func TestExecuteAICompleteGovernedNoop(t *testing.T) {
	t.Run("json envelope reports usage budget and governance", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "complete", "--prompt", "hello user@example.com token=secret", "--model", "test-model", "--max-total-tokens", "128", "--json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stderr.String(), "user@example.com") || strings.Contains(stderr.String(), "secret") {
			t.Fatalf("ai complete audit stderr leaked raw prompt: %q", stderr.String())
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Usage    struct {
					TotalTokens int `json:"totalTokens"`
				} `json:"usage"`
				Budget struct {
					UsedTotal   int `json:"usedTotal"`
					RemainTotal int `json:"remainTotal"`
				} `json:"budget"`
				Governance struct {
					ProviderMode         string   `json:"providerMode"`
					ProviderCapabilities []string `json:"providerCapabilities"`
					TelemetryFields      []string `json:"telemetryFields"`
					NetworkAccess        bool     `json:"networkAccess"`
					RequiresSecrets      bool     `json:"requiresSecrets"`
					SecretSource         string   `json:"secretSource"`
					Redacted             bool     `json:"redacted"`
					BudgetEnforced       bool     `json:"budgetEnforced"`
					AuditLogged          bool     `json:"auditLogged"`
				} `json:"governance"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.complete" || envelope.Data.Provider != "noop" || envelope.Data.Model != "test-model" {
			t.Fatalf("ai complete envelope = %+v", envelope)
		}
		if envelope.Data.Usage.TotalTokens == 0 || envelope.Data.Budget.UsedTotal == 0 || envelope.Data.Budget.RemainTotal == 0 {
			t.Fatalf("ai complete usage/budget = %+v", envelope.Data)
		}
		if envelope.Data.Governance.ProviderMode != "noop" || envelope.Data.Governance.NetworkAccess || envelope.Data.Governance.RequiresSecrets || envelope.Data.Governance.SecretSource != "environment" || !commandContainsString(envelope.Data.Governance.ProviderCapabilities, "offline") || !commandContainsString(envelope.Data.Governance.TelemetryFields, "error_class") || !envelope.Data.Governance.Redacted || !envelope.Data.Governance.BudgetEnforced || !envelope.Data.Governance.AuditLogged {
			t.Fatalf("ai complete governance = %+v", envelope.Data.Governance)
		}
	})

	t.Run("budget exceeded returns structured JSON error", func(t *testing.T) {
		var stdout bytes.Buffer
		err := ExecuteWithIO([]string{"--output", "json", "ai", "complete", "--prompt", "this prompt is longer than one token", "--max-total-tokens", "1"}, IOStreams{Out: &stdout})
		if err == nil {
			t.Fatal("ai complete expected budget error")
		}
		var envelope struct {
			OK    bool `json:"ok"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete error output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if envelope.OK || envelope.Error.Code != "LLM_TOKEN_BUDGET_EXCEEDED" {
			t.Fatalf("ai complete error envelope = %+v", envelope)
		}
	})

	t.Run("openai-compatible missing secret returns structured JSON error", func(t *testing.T) {
		var stdout bytes.Buffer
		err := ExecuteWithIO([]string{"--output", "json", "ai", "complete", "--prompt", "hello", "--provider", "openai-compatible"}, IOStreams{Out: &stdout})
		if err == nil {
			t.Fatal("ai complete expected missing secret error")
		}
		var envelope struct {
			OK    bool `json:"ok"`
			Error struct {
				Code        string `json:"code"`
				Message     string `json:"message"`
				Remediation string `json:"remediation"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete missing secret output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if envelope.OK || envelope.Error.Code != "LLM_PROVIDER_SECRET_MISSING" || !strings.Contains(envelope.Error.Remediation, "environment") {
			t.Fatalf("ai complete missing secret envelope = %+v", envelope)
		}
		if strings.Contains(stdout.String(), "super-secret") || strings.Contains(stdout.String(), "Bearer") {
			t.Fatalf("ai complete missing secret leaked secret-like content: %s", stdout.String())
		}
	})

	t.Run("flag overrides env which overrides config", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, generator.DefaultConfigFile)
		cfg := generator.DefaultConfig("svc", "example.com/svc")
		cfg.LLM = &generator.LLMConfig{Provider: "noop", Model: "config-model", MaxTotalTokens: 200, RateLimitPerSecond: 1, Timeout: "3s"}
		if err := generator.SaveConfig(configPath, cfg); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOFLY_LLM_MODEL", "env-model")
		t.Setenv("GOFLY_LLM_MAX_TOTAL_TOKENS", "128")
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "complete", "--prompt", "hello", "--dir", dir, "--model", "flag-model", "--max-total-tokens", "64", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OK   bool `json:"ok"`
			Data struct {
				Model  string `json:"model"`
				Budget struct {
					MaxTotal    int `json:"maxTotal"`
					RemainTotal int `json:"remainTotal"`
				} `json:"budget"`
				Governance struct {
					RateLimited bool `json:"rateLimited"`
				} `json:"governance"`
				Metadata map[string]string `json:"metadata"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete config output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Data.Model != "flag-model" || envelope.Data.Budget.MaxTotal != 64 || envelope.Data.Budget.RemainTotal == 0 {
			t.Fatalf("ai complete config precedence = %+v", envelope.Data)
		}
		if !envelope.Data.Governance.RateLimited || envelope.Data.Metadata["configPath"] != configPath {
			t.Fatalf("ai complete config metadata/governance = %+v", envelope.Data)
		}
	})
}

func TestExecuteAIStreamGovernedNoop(t *testing.T) {
	t.Run("json stream emits newline-delimited event envelope", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "stream", "--prompt", "hello user@example.com token=secret", "--model", "test-model", "--max-total-tokens", "128", "--json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stderr.String(), "user@example.com") || strings.Contains(stderr.String(), "secret") {
			t.Fatalf("ai stream audit stderr leaked raw prompt: %q", stderr.String())
		}
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("ai stream JSON lines = %d, output:\n%s", len(lines), stdout.String())
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Index    int    `json:"index"`
				Done     bool   `json:"done"`
				Usage    struct {
					TotalTokens int `json:"totalTokens"`
				} `json:"usage"`
				Governance struct {
					ProviderMode         string   `json:"providerMode"`
					ProviderCapabilities []string `json:"providerCapabilities"`
					TelemetryFields      []string `json:"telemetryFields"`
					AuditLogged          bool     `json:"auditLogged"`
				} `json:"governance"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &envelope); err != nil {
			t.Fatalf("ai stream event is not valid JSON: %v\n%s", err, lines[0])
		}
		if !envelope.OK || envelope.Command != "ai.stream" || envelope.Data.Provider != "noop" || envelope.Data.Model != "test-model" || envelope.Data.Index != 0 || !envelope.Data.Done || envelope.Data.Usage.TotalTokens == 0 {
			t.Fatalf("ai stream event envelope = %+v", envelope)
		}
		if envelope.Data.Governance.ProviderMode != "noop" || !commandContainsString(envelope.Data.Governance.ProviderCapabilities, "stream") || !commandContainsString(envelope.Data.Governance.TelemetryFields, "stream_events") || !envelope.Data.Governance.AuditLogged {
			t.Fatalf("ai stream governance = %+v", envelope.Data.Governance)
		}
	})

	t.Run("complete stream flag emits governed event envelope", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "complete", "--stream", "--prompt", "hello user@example.com token=secret", "--model", "test-model", "--max-total-tokens", "128", "--json"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stderr.String(), "user@example.com") || strings.Contains(stderr.String(), "secret") {
			t.Fatalf("ai complete --stream audit stderr leaked raw prompt: %q", stderr.String())
		}
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("ai complete --stream JSON lines = %d, output:\n%s", len(lines), stdout.String())
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Index    int    `json:"index"`
				Done     bool   `json:"done"`
				Usage    struct {
					TotalTokens int `json:"totalTokens"`
				} `json:"usage"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &envelope); err != nil {
			t.Fatalf("ai complete --stream event is not valid JSON: %v\n%s", err, lines[0])
		}
		if !envelope.OK || envelope.Command != "ai.complete" || envelope.Data.Provider != "noop" || envelope.Data.Model != "test-model" || envelope.Data.Index != 0 || !envelope.Data.Done || envelope.Data.Usage.TotalTokens == 0 {
			t.Fatalf("ai complete --stream event envelope = %+v", envelope)
		}
	})

	t.Run("text output suppresses noop empty delta", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "stream", "hello", "world", "--max-total-tokens", "64"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("ai stream noop text output = %q, want empty", stdout.String())
		}
	})

	t.Run("openai-compatible missing secret returns structured JSON error", func(t *testing.T) {
		var stdout bytes.Buffer
		err := ExecuteWithIO([]string{"--output", "json", "ai", "stream", "--prompt", "hello", "--provider", "openai-compatible"}, IOStreams{Out: &stdout})
		if err == nil {
			t.Fatal("ai stream expected missing secret error")
		}
		var envelope struct {
			OK    bool `json:"ok"`
			Error struct {
				Code        string `json:"code"`
				Remediation string `json:"remediation"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai stream missing secret output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if envelope.OK || envelope.Error.Code != "LLM_PROVIDER_SECRET_MISSING" || !strings.Contains(envelope.Error.Remediation, "environment") {
			t.Fatalf("ai stream missing secret envelope = %+v", envelope)
		}
		if strings.Contains(stdout.String(), "super-secret") || strings.Contains(stdout.String(), "Bearer") {
			t.Fatalf("ai stream missing secret leaked secret-like content: %s", stdout.String())
		}
	})

	t.Run("complete stream openai-compatible missing secret fails before network", func(t *testing.T) {
		t.Setenv("GOFLY_LLM_OPENAI_BASE_URL", "https://127.0.0.1:1/v1")
		t.Setenv("GOFLY_LLM_OPENAI_ALLOWED_HOSTS", "127.0.0.1")
		var stdout bytes.Buffer
		err := ExecuteWithIO([]string{"--output", "json", "ai", "complete", "--stream", "--prompt", "hello", "--provider", "openai-compatible"}, IOStreams{Out: &stdout})
		if err == nil {
			t.Fatal("ai complete --stream expected missing secret error")
		}
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "127.0.0.1:1") {
			t.Fatalf("ai complete --stream attempted network before secret validation: %v", err)
		}
		var envelope struct {
			OK    bool `json:"ok"`
			Error struct {
				Code      string         `json:"code"`
				Details   map[string]any `json:"details"`
				Retryable bool           `json:"retryable"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai complete --stream missing secret output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if envelope.OK || envelope.Error.Code != "LLM_PROVIDER_SECRET_MISSING" || envelope.Error.Retryable || len(envelope.Error.Details) != 0 {
			t.Fatalf("ai complete --stream missing secret envelope = %+v", envelope)
		}
	})
}

func TestExecuteConfigLLMFields(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"config", "init", "--dir", dir, "--name", "svc", "--module", "example.com/svc"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "set", "llm.model", "local-model", "--dir", dir},
		{"config", "set", "llm.max-total-tokens", "256", "--dir", dir},
		{"config", "set", "llm.timeout", "2s", "--dir", dir},
	} {
		if err := Execute(args); err != nil {
			t.Fatalf("Execute(%v) error = %v", args, err)
		}
	}
	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"config", "get", "llm.model", "--dir", dir}, IOStreams{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "local-model" {
		t.Fatalf("config get llm.model = %q", stdout.String())
	}
	stdout.Reset()
	if err := ExecuteWithIO([]string{"config", "get", "llm.max-total-tokens", "--dir", dir}, IOStreams{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "256" {
		t.Fatalf("config get llm.max-total-tokens = %q", stdout.String())
	}
}

func TestAIDoctorCommandAndChecks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOFLY_LLM_OPENAI_API_KEY", "super-secret-provider-token")
	t.Setenv("GOFLY_LLM_OPENAI_BASE_URL", "https://internal.example.test/v1?token=endpoint-token")
	t.Setenv("GOFLY_LLM_OPENAI_ALLOWED_HOSTS", "internal.example.test")
	t.Setenv("GOFLY_LLM_FAILOVER_PROVIDERS", "noop,missing")
	t.Chdir(dir)

	cfg := generator.DefaultConfig("svc", "example.com/svc")
	cfg.LLM = &generator.LLMConfig{Provider: "noop", Model: "doctor-model", MaxTotalTokens: 64}
	if err := generator.SaveConfig(filepath.Join(dir, generator.DefaultConfigFile), cfg); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"ai", "doctor", "--json"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Providers []aiDoctorItem `json:"providers"`
			EnvVars   []aiDoctorItem `json:"envVars"`
			Secrets   []aiDoctorItem `json:"secrets"`
			Failover  aiDoctorItem   `json:"failover"`
			Config    aiDoctorItem   `json:"config"`
			Cache     aiDoctorItem   `json:"cache"`
			Telemetry aiDoctorItem   `json:"telemetry"`
			Cost      aiDoctorItem   `json:"cost"`
			Summary   string         `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("ai doctor output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.Command != "ai.doctor" || len(envelope.Data.Providers) == 0 || len(envelope.Data.EnvVars) == 0 || len(envelope.Data.Secrets) == 0 {
		t.Fatalf("ai doctor envelope = %+v", envelope)
	}
	if envelope.Data.Failover.Status != "warn" || !strings.Contains(envelope.Data.Failover.Message, "invalid=missing") {
		t.Fatalf("ai doctor failover = %+v", envelope.Data.Failover)
	}
	if envelope.Data.Failover.Severity != "medium" || !commandContainsString(envelope.Data.Failover.NextActions, "remove invalid providers from GOFLY_LLM_FAILOVER_PROVIDERS") || !commandContainsString(envelope.Data.Failover.NextActions, "rerun ai complete or ai stream with --allow-failover only after fallback providers are valid") {
		t.Fatalf("ai doctor failover remediation = %+v", envelope.Data.Failover)
	}
	if envelope.Data.Config.Status != "ok" || !strings.Contains(envelope.Data.Config.Message, "doctor-model") {
		t.Fatalf("ai doctor config = %+v", envelope.Data.Config)
	}
	if envelope.Data.Cache.Status != "info" || envelope.Data.Cache.Severity != "info" || !commandContainsString(envelope.Data.Cache.NextActions, "set GOFLY_LLM_CACHE_TTL and GOFLY_LLM_CACHE_MAX_SIZE only when response caching is desired") {
		t.Fatalf("ai doctor cache = %+v", envelope.Data.Cache)
	}
	if envelope.Data.Telemetry.Status != "ok" || envelope.Data.Telemetry.Severity != "info" || !strings.Contains(envelope.Data.Telemetry.Message, "lowCardinality") || !commandContainsString(envelope.Data.Telemetry.NextActions, "emit only low-cardinality LLM telemetry fields such as provider, model, status, error_class and token counts") {
		t.Fatalf("ai doctor telemetry = %+v", envelope.Data.Telemetry)
	}
	if envelope.Data.Cost.Status != "info" || envelope.Data.Cost.Severity != "info" || !strings.Contains(envelope.Data.Cost.Message, "unpriced") || !commandContainsString(envelope.Data.Cost.NextActions, "use JSON usage.totalTokens and budget snapshots before retrying or enabling failover") {
		t.Fatalf("ai doctor cost = %+v", envelope.Data.Cost)
	}
	if strings.Contains(stdout.String(), "super-secret-provider-token") {
		t.Fatalf("ai doctor leaked secret value: %s", stdout.String())
	}
	for _, leaked := range []string{"internal.example.test", "endpoint-token"} {
		if strings.Contains(stdout.String(), leaked) {
			t.Fatalf("ai doctor leaked endpoint config %q: %s", leaked, stdout.String())
		}
	}

	stdout.Reset()
	if err := ExecuteWithIO([]string{"ai", "doctor"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gofly ai doctor", "Providers:", "Environment:", "Secrets:", "Failover:", "Config:", "Cache:", "Telemetry:", "Cost:", "next:"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("ai doctor text output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestAIDoctorHelperEdgeCases(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOFLY_LLM_FAILOVER_PROVIDERS", "   ")
	t.Chdir(t.TempDir())
	if got := checkAIDoctorProviders(llm.NewProviderRegistry()); len(got) != 1 || got[0].Status != "warn" {
		t.Fatalf("checkAIDoctorProviders(empty) = %+v", got)
	}
	if got := checkAIDoctorFailover(llm.NewDefaultProviderRegistry()); got.Status != "warn" || !strings.Contains(got.Message, "empty") {
		t.Fatalf("checkAIDoctorFailover(empty parsed) = %+v", got)
	}
	if got := checkAIDoctorConfig(); got.Status != "info" && got.Status != "ok" {
		t.Fatalf("checkAIDoctorConfig() = %+v", got)
	}
	printAIDoctorItem(aiDoctorItem{Name: "custom", Status: "unknown", Message: "details"}, "")
}

func TestResolveAICompleteConfigLayersAndValidation(t *testing.T) {
	t.Run("defaults from missing config", func(t *testing.T) {
		cfg, err := resolveAICompleteConfig(flag.NewFlagSet("test", flag.ContinueOnError), aiCompleteConfigFlags{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Provider != "noop" || cfg.Model != "noop" || cfg.MaxTotalTokens != 0 || cfg.ConfigPath == "" {
			t.Fatalf("default ai complete config = %+v", cfg)
		}
	})

	t.Run("config env and flags merge", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, generator.DefaultConfigFile)
		cfg := generator.DefaultConfig("svc", "example.com/svc")
		cfg.LLM = &generator.LLMConfig{Provider: "noop", Model: "config", MaxInputTokens: 10, MaxOutputTokens: 11, MaxTotalTokens: 12, RateLimitPerSecond: 13, RateLimitBurst: 14, Timeout: "15s"}
		if err := generator.SaveConfig(path, cfg); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOFLY_LLM_MODEL", "env")
		t.Setenv("GOFLY_LLM_MAX_OUTPUT_TOKENS", "21")
		t.Setenv("GOFLY_LLM_RATE_BURST", "22")
		fs := newAICompleteTestFlagSet(t, "--model", "flag", "--max-total-tokens", "31", "--timeout", "32s")
		got, err := resolveAICompleteConfig(fs, aiCompleteConfigFlags{Dir: dir, Model: "flag", MaxTotalTokens: 31, Timeout: "32s"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Model != "flag" || got.MaxInputTokens != 10 || got.MaxOutputTokens != 21 || got.MaxTotalTokens != 31 || got.RateLimitPerSecond != 13 || got.RateLimitBurst != 22 || got.Timeout != 32*time.Second {
			t.Fatalf("merged ai complete config = %+v", got)
		}
	})

	t.Run("invalid env int", func(t *testing.T) {
		t.Setenv("GOFLY_LLM_MAX_TOTAL_TOKENS", "bad")
		_, err := resolveAICompleteConfig(flag.NewFlagSet("test", flag.ContinueOnError), aiCompleteConfigFlags{Dir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "GOFLY_LLM_MAX_TOTAL_TOKENS") {
			t.Fatalf("invalid env int error = %v", err)
		}
	})

	t.Run("invalid config timeout", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, generator.DefaultConfigFile)
		cfg := generator.DefaultConfig("svc", "example.com/svc")
		cfg.LLM = &generator.LLMConfig{Provider: "noop", Model: "noop", Timeout: "soon"}
		if err := generator.SaveConfig(path, cfg); err != nil {
			t.Fatal(err)
		}
		_, err := resolveAICompleteConfig(flag.NewFlagSet("test", flag.ContinueOnError), aiCompleteConfigFlags{Dir: dir})
		if err == nil || !strings.Contains(err.Error(), "invalid llm.timeout") {
			t.Fatalf("invalid config timeout error = %v", err)
		}
	})

	t.Run("unsupported provider", func(t *testing.T) {
		fs := newAICompleteTestFlagSet(t, "--provider", "remote")
		_, err := resolveAICompleteConfig(fs, aiCompleteConfigFlags{Provider: "remote", Dir: t.TempDir()})
		if err == nil || !errors.Is(err, llm.ErrProviderNotFound) || !strings.Contains(err.Error(), "available providers: noop,openai-compatible") {
			t.Fatalf("unsupported provider error = %v", err)
		}
	})

	t.Run("invalid flag timeout", func(t *testing.T) {
		fs := newAICompleteTestFlagSet(t, "--timeout", "bad")
		_, err := resolveAICompleteConfig(fs, aiCompleteConfigFlags{Dir: t.TempDir(), Timeout: "bad"})
		if err == nil || !strings.Contains(err.Error(), "invalid --timeout") {
			t.Fatalf("invalid flag timeout error = %v", err)
		}
	})

	t.Run("all env overlays", func(t *testing.T) {
		t.Setenv("GOFLY_LLM_PROVIDER", "noop")
		t.Setenv("GOFLY_LLM_MODEL", "env-model")
		t.Setenv("GOFLY_LLM_MAX_INPUT_TOKENS", "101")
		t.Setenv("GOFLY_LLM_MAX_OUTPUT_TOKENS", "102")
		t.Setenv("GOFLY_LLM_MAX_TOTAL_TOKENS", "203")
		t.Setenv("GOFLY_LLM_RATE_LIMIT", "4")
		t.Setenv("GOFLY_LLM_RATE_BURST", "5")
		t.Setenv("GOFLY_LLM_TIMEOUT", "6s")
		t.Setenv("GOFLY_LLM_FAILOVER_PROVIDERS", "openai-compatible,noop,openai-compatible")
		cfg, err := resolveAICompleteConfig(flag.NewFlagSet("test", flag.ContinueOnError), aiCompleteConfigFlags{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Provider != "noop" || cfg.Model != "env-model" || cfg.MaxInputTokens != 101 || cfg.MaxOutputTokens != 102 || cfg.MaxTotalTokens != 203 || cfg.RateLimitPerSecond != 4 || cfg.RateLimitBurst != 5 || cfg.Timeout != 6*time.Second || !reflect.DeepEqual(cfg.FailoverProviders, []string{"openai-compatible"}) {
			t.Fatalf("env ai complete config = %+v", cfg)
		}
	})

	t.Run("unsupported failover provider", func(t *testing.T) {
		t.Setenv("GOFLY_LLM_FAILOVER_PROVIDERS", "missing")
		_, err := resolveAICompleteConfig(flag.NewFlagSet("test", flag.ContinueOnError), aiCompleteConfigFlags{Dir: t.TempDir()})
		if err == nil || !errors.Is(err, llm.ErrProviderNotFound) || !strings.Contains(err.Error(), "failover provider") {
			t.Fatalf("unsupported failover provider error = %v", err)
		}
	})

	t.Run("negative values rejected", func(t *testing.T) {
		fs := newAICompleteTestFlagSet(t, "--max-input-tokens", "-1")
		_, err := resolveAICompleteConfig(fs, aiCompleteConfigFlags{Dir: t.TempDir(), MaxInputTokens: -1})
		if err == nil || !strings.Contains(err.Error(), "token budgets must be non-negative") {
			t.Fatalf("negative token error = %v", err)
		}

		fs = newAICompleteTestFlagSet(t, "--rate-limit", "-1")
		_, err = resolveAICompleteConfig(fs, aiCompleteConfigFlags{Dir: t.TempDir(), RateLimitPerSecond: -1})
		if err == nil || !strings.Contains(err.Error(), "rate limit values must be non-negative") {
			t.Fatalf("negative rate error = %v", err)
		}
	})
}

func newAICompleteTestFlagSet(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()
	fs := flag.NewFlagSet("ai complete test", flag.ContinueOnError)
	fs.String("provider", "", "")
	fs.String("model", "", "")
	fs.Int("max-input-tokens", 0, "")
	fs.Int("max-output-tokens", 0, "")
	fs.Int("max-total-tokens", 0, "")
	fs.Int("rate-limit", 0, "")
	fs.Int("rate-burst", 0, "")
	fs.String("timeout", "", "")
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestExecuteAICompleteValidationAndTextOutput(t *testing.T) {
	t.Run("text output uses configured noop provider", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "complete", "hello", "world", "--max-total-tokens", "64"}, IOStreams{Out: &stdout, Err: &stderr}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "provider=noop") || !strings.Contains(stdout.String(), "(noop provider returned no text)") {
			t.Fatalf("ai complete text output = %q", stdout.String())
		}
		if strings.Contains(stderr.String(), "hello world") {
			t.Fatalf("ai complete text audit leaked prompt: %q", stderr.String())
		}
	})

	t.Run("positional and prompt conflict", func(t *testing.T) {
		err := ExecuteWithIO([]string{"ai", "complete", "positional", "--prompt", "flag"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
		if err == nil || !strings.Contains(err.Error(), "either --prompt or positional prompt text") {
			t.Fatalf("conflict error = %v", err)
		}
	})

	t.Run("missing prompt", func(t *testing.T) {
		err := ExecuteWithIO([]string{"ai", "complete"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
		if err == nil || !strings.Contains(err.Error(), "--prompt or positional prompt text is required") {
			t.Fatalf("missing prompt error = %v", err)
		}
	})

	t.Run("unsupported format", func(t *testing.T) {
		err := ExecuteWithIO([]string{"ai", "complete", "hello", "--format", "yaml"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
		if err == nil || !strings.Contains(err.Error(), "unsupported --format") {
			t.Fatalf("unsupported format error = %v", err)
		}
	})
}

func TestCommandIOJSONErrorHelpers(t *testing.T) {
	var out bytes.Buffer
	WriteErrorJSON(&out, fmt.Errorf("%w: bad flag", errUsage))
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("WriteErrorJSON output is not valid JSON: %v\n%s", err, out.String())
	}
	if envelope.Error.Code != "USAGE_ERROR" || !strings.Contains(envelope.Error.Message, "bad flag") {
		t.Fatalf("WriteErrorJSON envelope = %+v", envelope)
	}

	out.Reset()
	WriteErrorJSON(&out, nil)
	if out.Len() != 0 {
		t.Fatalf("WriteErrorJSON(nil) wrote %q", out.String())
	}

	if errorCodeClass(nil) != "OK" || errorCodeClass(errors.New("boom")) != "INTERNAL_ERROR" {
		t.Fatalf("unexpected error code classes")
	}

	var stdout bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &stdout}, outputText, verbosityNormal, func() error {
		cliOutputIf("visible")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "visible" {
		t.Fatalf("cliOutputIf stdout = %q", stdout.String())
	}
}

func TestClassifyJSONErrorExtendedCodes(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantCode  string
		wantRetry bool
	}{
		{"nil error", nil, "", false},
		{"usage error", fmt.Errorf("%w: bad flag", errUsage), "USAGE_ERROR", false},
		{"budget exceeded", llm.ErrBudgetExceeded, "LLM_TOKEN_BUDGET_EXCEEDED", false},
		{"rate limited", llm.ErrRateLimited, "LLM_RATE_LIMITED", true},
		{"provider not found", llm.ErrProviderNotFound, "LLM_PROVIDER_NOT_FOUND", false},
		{"secret missing", llm.ErrSecretNotFound, "LLM_PROVIDER_SECRET_MISSING", false},
		{"endpoint rejected", llm.ErrProviderEndpointRejected, "LLM_PROVIDER_ENDPOINT_REJECTED", false},
		{"config invalid", llm.ErrProviderConfigInvalid, "LLM_PROVIDER_CONFIG_INVALID", false},
		{"capability unsupported", llm.ErrProviderCapabilityUnsupported, "LLM_PROVIDER_CAPABILITY_UNSUPPORTED", false},
		{"request failed", llm.ErrProviderRequestFailed, "LLM_PROVIDER_REQUEST_FAILED", true},
		{"request unauthorized", &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusUnauthorized}, "LLM_PROVIDER_REQUEST_FAILED", false},
		{"request throttled", &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusTooManyRequests}, "LLM_PROVIDER_REQUEST_FAILED", true},
		{"request server error", &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusBadGateway}, "LLM_PROVIDER_REQUEST_FAILED", true},
		{"response too large", llm.ErrProviderResponseTooLarge, "LLM_PROVIDER_RESPONSE_TOO_LARGE", false},
		{"already registered", llm.ErrProviderAlreadyRegistered, "LLM_PROVIDER_ALREADY_REGISTERED", false},
		{"feature not registered", fmt.Errorf("feature foo is not registered"), "FEATURE_NOT_REGISTERED", false},
		{"generic error", errors.New("something broke"), "COMMAND_ERROR", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyJSONError(tt.err)
			if tt.err == nil {
				if got != nil {
					t.Fatalf("classifyJSONError(nil) = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("classifyJSONError(%v) = nil, want non-nil", tt.err)
			}
			if got.Code != tt.wantCode {
				t.Fatalf("classifyJSONError(%v).Code = %q, want %q", tt.err, got.Code, tt.wantCode)
			}
			if got.Retryable != tt.wantRetry {
				t.Fatalf("classifyJSONError(%v).Retryable = %v, want %v", tt.err, got.Retryable, tt.wantRetry)
			}
			if got.Message == "" {
				t.Fatalf("classifyJSONError(%v).Message is empty", tt.err)
			}
			if tt.wantCode != "COMMAND_ERROR" && got.Remediation == "" {
				t.Fatalf("classifyJSONError(%v).Remediation is empty for code %s", tt.err, got.Code)
			}
			if tt.wantCode == "LLM_PROVIDER_REQUEST_FAILED" && tt.wantRetry && !commandContainsString(got.NextActions, "set GOFLY_LLM_FAILOVER_PROVIDERS and rerun with --allow-failover to manually retry retryable provider failures") {
				t.Fatalf("classifyJSONError(%v).NextActions = %+v, want failover hint", tt.err, got.NextActions)
			}
		})
	}
}

func TestVersionCommandJSONEnvelope(t *testing.T) {
	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"version", "--json"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Version string `json:"version"`
		Data    struct {
			Tool      string `json:"tool"`
			Version   string `json:"version"`
			Commit    string `json:"commit"`
			GoVersion string `json:"go_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("version --output json is not valid JSON envelope: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.Command != "version" || envelope.Version == "" {
		t.Fatalf("version --output json envelope = %+v", envelope)
	}
	if envelope.Data.Tool != "gofly" || envelope.Data.Version == "" || envelope.Data.Commit == "" || envelope.Data.GoVersion == "" {
		t.Fatalf("version --output json data = %+v", envelope.Data)
	}
}

func TestGlobalOutputJSONErrorsToStdoutOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := ExecuteWithIO([]string{"--output", "json", "example", "nonexistent"}, IOStreams{Out: &stdout, Err: &stderr})
	if err == nil {
		t.Fatal("example nonexistent should error")
	}
	if stderr.Len() != 0 {
		t.Fatalf("json mode stderr = %q, want empty", stderr.String())
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("json mode error output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if envelope.OK || envelope.Error.Code == "" || envelope.Error.Message == "" {
		t.Fatalf("json mode error envelope = %+v", envelope)
	}
}

func TestCLISTDIOExitContract(t *testing.T) {
	t.Run("text usage errors return exit 2 without writing streams", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := ExecuteWithIO([]string{"example", "nonexistent"}, IOStreams{Out: &stdout, Err: &stderr})
		if err == nil {
			t.Fatal("text usage command returned nil, want error")
		}
		if ExitCode(err) != 2 {
			t.Fatalf("ExitCode(text usage error) = %d, want 2: %v", ExitCode(err), err)
		}
		if stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("ExecuteWithIO text usage stdout=%q stderr=%q, want caller-owned error rendering", stdout.String(), stderr.String())
		}
	})

	t.Run("global JSON usage errors write one stdout envelope", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := ExecuteWithIO([]string{"--output", "json", "example", "nonexistent"}, IOStreams{Out: &stdout, Err: &stderr})
		if err == nil {
			t.Fatal("JSON usage command returned nil, want error")
		}
		if ExitCode(err) != 2 {
			t.Fatalf("ExitCode(JSON usage error) = %d, want 2: %v", ExitCode(err), err)
		}
		if stderr.Len() != 0 {
			t.Fatalf("JSON usage stderr = %q, want empty", stderr.String())
		}
		if strings.Count(stdout.String(), `"ok"`) != 1 {
			t.Fatalf("JSON usage stdout emitted duplicate envelopes:\n%s", stdout.String())
		}
		var envelope struct {
			OK    bool `json:"ok"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("JSON usage stdout is not valid envelope: %v\n%s", err, stdout.String())
		}
		if envelope.OK || envelope.Error.Code != "USAGE_ERROR" || envelope.Error.Message == "" {
			t.Fatalf("JSON usage envelope = %+v", envelope)
		}
	})

	t.Run("JSONOutputRequested survives ExecuteWithIO mode reset", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"--output", "json", "version"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatal(err)
		}
		if OutputMode() != outputText {
			t.Fatalf("OutputMode after ExecuteWithIO = %q, want restored text", OutputMode())
		}
		if !JSONOutputRequested([]string{"--output", "json", "example", "nonexistent"}) {
			t.Fatal("JSONOutputRequested(--output json ...) = false, want true after mode reset")
		}
		if JSONOutputRequested([]string{"version", "--json"}) {
			t.Fatal("JSONOutputRequested(command-local --json) = true, want false for global process error rendering")
		}
	})
}

func TestExecuteAIManifestAliasAndText(t *testing.T) {
	var stdout bytes.Buffer
	if err := ExecuteWithIO([]string{"tools", "manifest", "--format", "text"}, IOStreams{Out: &stdout}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"gofly AI tool manifest", "ai manifest", "feature run", "plugin run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tools manifest text missing %q:\n%s", want, out)
		}
	}
}

func TestExecuteFeatureHelpUsesNeutralCompatibilityNames(t *testing.T) {
	for _, args := range [][]string{{"feature", "--help"}, {"feature", "run", "--help"}} {
		out := captureStdout(t, func() {
			if err := Execute(args); err != nil {
				t.Fatal(err)
			}
		})
		for _, want := range []string{"http-compat", "rpc-compat"} {
			if !strings.Contains(out, want) {
				t.Fatalf("feature help %v missing neutral name %q:\n%s", args, want, out)
			}
		}
		for _, removed := range []string{"go-zero", "gozero", "kitex", "legacy aliases"} {
			if strings.Contains(out, removed) {
				t.Fatalf("feature help %v should not mention removed alias %q:\n%s", args, removed, out)
			}
		}
	}
}

func commandContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestExecuteNewAPIIsUnifiedScaffoldEntry(t *testing.T) {
	dir := t.TempDir()
	apiDir := filepath.Join(dir, "api")
	if err := Execute([]string{"new", "api", "hello", "--module", "example.com/hello", "--dir", apiDir}); err != nil {
		t.Fatal(err)
	}

	productionOnlyFiles := []string{
		filepath.Join("etc", "governance.json"),
		filepath.Join("internal", "rpc", "greeter.go"),
	}
	for _, rel := range productionOnlyFiles {
		if _, err := os.Stat(filepath.Join(apiDir, rel)); err == nil {
			t.Fatalf("new api should not generate production-only file %s", rel)
		}
	}
	for _, rel := range []string{"Dockerfile", "Makefile"} {
		if _, err := os.Stat(filepath.Join(apiDir, rel)); err != nil {
			t.Fatalf("new api basic should generate practical base file %s: %v", rel, err)
		}
	}

	if _, err := os.Stat(filepath.Join(apiDir, "hello.api")); err != nil {
		t.Fatalf("new api should generate api spec: %v", err)
	}
}

func TestExecuteNewAPIProductionReplacesNewServiceChoice(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"new", "api", "hello", "--module", "example.com/hello", "--dir", dir, "--style", "production"}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"hello.api",
		"Dockerfile",
		filepath.Join("etc", "governance.json"),
		filepath.Join("internal", "rpc", "greeter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected api new --style production file %s: %v", rel, err)
		}
	}
}

func TestExecuteNewServiceGoldenPath(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"new", "service", "orders", "--module", "example.com/orders", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"go.mod",
		"Dockerfile",
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("cmd", "orders", "main.go"),
		filepath.Join("etc", "governance.json"),
		filepath.Join("internal", "admin", "admin.go"),
		filepath.Join("internal", "config", "config_test.go"),
		filepath.Join("internal", "config", "discovery_test.go"),
		filepath.Join("internal", "discovery", "registry.go"),
		filepath.Join("internal", "rpc", "greeter.go"),
		filepath.Join("internal", "smoke", "service_smoke_test.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("new service should generate golden-path file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "orders.api")); err == nil {
		t.Fatal("new service should not generate API-only IDL file by default")
	}
}

func TestExecuteRPCNew(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"rpc", "new", "greeter", "--module", "example.com/greeter", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"go.mod",
		"greeter.proto",
		filepath.Join("internal", "rpc", "greeter.go"),
		"Dockerfile",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected rpc new file %s: %v", rel, err)
		}
	}
}

func TestExecuteRPCNewWithKitexCompatibleProfile(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"rpc", "new", "greeter", "--module", "example.com/greeter", "--dir", dir, "--profile", "kitex-compatible"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "compat", "kitex", "adapter.go")); err != nil {
		t.Fatalf("expected kitex-compatible adapter: %v", err)
	}
	cfg, err := generator.LoadConfig(filepath.Join(dir, generator.DefaultConfigFile))
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if cfg.RPC == nil || cfg.RPC.Profile != string(generator.ProfileKitexCompatible) {
		t.Fatalf("generated rpc profile = %#v, want kitex-compatible", cfg.RPC)
	}
}

func TestExecuteRPCNewUsesConfigProfileDefault(t *testing.T) {
	dir := t.TempDir()
	cfg := generator.DefaultConfig("greeter", "example.com/greeter")
	cfg.RPC.Profile = string(generator.ProfileKitexCompatible)
	if err := generator.SaveConfig(filepath.Join(dir, generator.DefaultConfigFile), cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := Execute([]string{"rpc", "new", "--config", filepath.Join(dir, generator.DefaultConfigFile), "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "compat", "kitex", "adapter.go")); err != nil {
		t.Fatalf("expected kitex-compatible adapter from config profile: %v", err)
	}
	got, err := generator.LoadConfig(filepath.Join(dir, generator.DefaultConfigFile))
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got.RPC == nil || got.RPC.Profile != string(generator.ProfileKitexCompatible) {
		t.Fatalf("persisted rpc profile = %#v, want kitex-compatible", got.RPC)
	}
}

func TestExecuteRPCNewAcceptsGoctlReservedFlags(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{
		"new", "rpc", "greeter",
		"-module", "example.com/greeter",
		"-dir", dir,
		"-style", "go_zero",
		"-home", filepath.Join(dir, "templates"),
		"-remote", "https://example.invalid/templates.git",
		"-branch", "main",
		"-idea",
		"-client=false",
		"-verbose",
		"-name-from-filename",
		"-go_opt", "paths=source_relative",
		"-go_grpc_opt", "require_unimplemented_servers=false",
	}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"go.mod",
		"greeter.proto",
		filepath.Join("internal", "rpc", "greeter.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected rpc new file %s with accepted extra flags: %v", rel, err)
		}
	}
}

func TestExecuteAPIGenWithRPCPackage(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "greeter.api")
	api := `type HelloReq {
  Name string
}
type HelloResp {
  Message string
}
service greeter {
  @handler Hello
  post /hello (HelloReq) returns (HelloResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{
		"api", "gen",
		"--file", apiPath,
		"--dir", outDir,
		"--package", "handler",
		"--rpc-package", "example.com/project/greeterpb",
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "internal", "api", "v1", "greeter", "routes.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "RegisterGreeterGatewayRoutes") {
		t.Fatalf("generated api gateway file = %s", data)
	}
}

func TestExecuteModelGen(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE users (
  id bigint primary key,
  name varchar(64) not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"model", "gen", "--ddl", ddlPath, "--dir", outDir, "--package", "model", "--module", "example.com/usersvc"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "user.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "NewUserRepo") {
		t.Fatalf("generated model file = %s", data)
	}
	if !strings.Contains(string(data), `"example.com/usersvc/model/entity"`) {
		t.Fatalf("generated model import = %s", data)
	}

	jsonDir := filepath.Join(dir, "json-out")
	jsonOut := captureStdout(t, func() {
		if err := Execute([]string{"model", "gen", "--ddl", ddlPath, "--dir", jsonDir, "--package", "model", "--module", "example.com/usersvc", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Command           string            `json:"command"`
			MutatesFilesystem bool              `json:"mutatesFilesystem"`
			Inputs            map[string]string `json:"inputs"`
			Actions           []struct {
				Operation string `json:"operation"`
			} `json:"actions"`
			GeneratedFiles int `json:"generatedFiles"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &envelope); err != nil {
		t.Fatalf("model gen --json output is not valid JSON: %v\n%s", err, jsonOut)
	}
	if !envelope.OK || envelope.Command != "model.gen" || envelope.Data.Command != "model gen" || !envelope.Data.MutatesFilesystem {
		t.Fatalf("model gen JSON envelope = %+v, want stable success envelope", envelope)
	}
	if envelope.Data.Inputs["ddl"] != ddlPath || envelope.Data.Inputs["dir"] != jsonDir || envelope.Data.Inputs["module"] != "example.com/usersvc" {
		t.Fatalf("model gen JSON inputs = %v, want ddl/dir/module", envelope.Data.Inputs)
	}
	if envelope.Data.GeneratedFiles < 2 || len(envelope.Data.Actions) == 0 || envelope.Data.Actions[0].Operation != "write-model-files" {
		t.Fatalf("model gen JSON data = %+v, want generated file count and write action", envelope.Data)
	}
}

func TestExecuteModelGenPositionalDirAndTrailingStyle(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE books (
  id bigint primary key,
  title varchar(128) not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "internal")
	out := captureStdout(t, func() {
		if err := Execute([]string{"model", "gen", "--ddl", ddlPath, outDir, "--style", "gorm", "--module", "example.com/booksvc"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "model generated:") || !strings.Contains(out, filepath.Join(outDir, "model")) {
		t.Fatalf("model gen should print generated output directory, got %q", out)
	}
	if !strings.Contains(out, filepath.Join(outDir, "model", "entity", "book_gen.go")) || !strings.Contains(out, filepath.Join(outDir, "model", "repo", "book.go")) {
		t.Fatalf("model gen should print generated file paths, got %q", out)
	}
	entityData, err := os.ReadFile(filepath.Join(outDir, "model", "entity", "book_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entityData), "gorm:") || !strings.Contains(string(entityData), "TableName() string") {
		t.Fatalf("expected gorm entity output, got:\n%s", entityData)
	}
	repoData, err := os.ReadFile(filepath.Join(outDir, "model", "repo", "book.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(repoData), `"gorm.io/gorm"`) || !strings.Contains(string(repoData), `"example.com/booksvc/model/entity"`) {
		t.Fatalf("expected gorm repo output, got:\n%s", repoData)
	}
}

func TestExecuteModelGenTablePlusMySQLDDL(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "larkink-test.sql")
	ddl := `-- TablePlus export
/*!40101 SET NAMES utf8mb4 */;
DROP TABLE IF EXISTS ` + "`message`" + `;
CREATE TABLE ` + "`message`" + ` (
  ` + "`message_id`" + ` bigint NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  ` + "`thread_id`" + ` bigint NOT NULL COMMENT '线程编号',
  ` + "`message_uuid`" + ` varchar(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci DEFAULT NULL COMMENT '唯一编号',
  ` + "`message_body`" + ` text CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci COMMENT '消息内容',
  ` + "`message_extra`" + ` json DEFAULT NULL COMMENT '扩展字段',
  ` + "`message_status`" + ` int NOT NULL COMMENT '会话状态',
  ` + "`is_deleted`" + ` tinyint(1) NOT NULL DEFAULT '0' COMMENT '删除状态',
  ` + "`created_at`" + ` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT '创建时间',
  ` + "`updated_at`" + ` datetime(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3) COMMENT '更新时间',
  PRIMARY KEY (` + "`message_id`" + `)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='消息表';`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "internal")
	out := captureStdout(t, func() {
		if err := Execute([]string{"model", "gen", "--ddl", ddlPath, "--dir", outDir, "--style", "gorm", "--module", "example.com/larkink"}); err != nil {
			t.Fatal(err)
		}
	})
	entityPath := filepath.Join(outDir, "model", "entity", "message_gen.go")
	repoPath := filepath.Join(outDir, "model", "repo", "message.go")
	if !strings.Contains(out, entityPath) || !strings.Contains(out, repoPath) {
		t.Fatalf("model gen should report generated message model files, got %q", out)
	}
	entityData, err := os.ReadFile(entityPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type Message struct",
		"MessageId     int64",
		"MessageExtra  *string",
		"gorm:\"column:message_id;primaryKey\"",
		"func (Message) TableName() string",
	} {
		if !strings.Contains(string(entityData), want) {
			t.Fatalf("generated message entity missing %q:\n%s", want, entityData)
		}
	}
	repoData, err := os.ReadFile(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(repoData), "func NewMessageRepo(db *gorm.DB) *MessageRepo") || !strings.Contains(string(repoData), `"example.com/larkink/model/entity"`) {
		t.Fatalf("generated message repo is incomplete:\n%s", repoData)
	}
}

func TestExecuteModelMySQLDDL(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE books (
  id bigint primary key,
  title varchar(128) not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"model", "mysql", "ddl", "--src", ddlPath, "--dir", outDir, "--package", "model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "model", "entity", "book_gen.go")); err != nil {
		t.Fatalf("expected mysql ddl generated entity file: %v", err)
	}
}

func TestExecuteModelPostgresDDL(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE public."accounts" (
  "id" bigserial primary key,
  "email" text not null,
  "created_at" timestamptz
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"model", "pg", "ddl", "--src", ddlPath, "--dir", outDir, "--package", "model"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "model", "entity", "account_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `import "time"`) || !strings.Contains(string(data), "CreatedAt *time.Time") {
		t.Fatalf("postgres generated entity = %s", data)
	}
}

func TestExecuteModelDatasourceUsesRunner(t *testing.T) {
	old := runModelDatasource
	defer func() { runModelDatasource = old }()
	var calls []generator.ModelDatasourceOptions
	runModelDatasource = func(opts generator.ModelDatasourceOptions) error {
		calls = append(calls, opts)
		return nil
	}

	if err := Execute([]string{"model", "mysql", "datasource", "--url", "user:pass@tcp(localhost:3306)/app", "--table", "users,orders", "--dir", "/tmp/out", "--package", "model", "--module", "example.com/app"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].Driver != "mysql" || calls[0].DSN == "" || calls[0].Dir != "/tmp/out" || calls[0].Package != "model" || calls[0].Module != "example.com/app" {
		t.Fatalf("mysql datasource opts = %+v", calls[0])
	}
	if len(calls[0].Tables) != 2 || calls[0].Tables[0] != "users" || calls[0].Tables[1] != "orders" {
		t.Fatalf("mysql datasource tables = %#v", calls[0].Tables)
	}

	if err := Execute([]string{"model", "pg", "datasource", "--url", "postgres://localhost/app", "--table", "accounts"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"model", "mysql", "datasource", "--table", "events", "user:pass@tcp(localhost:3306)/events", "--dir", "/tmp/events"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[1].Driver != "postgres" || len(calls[1].Tables) != 1 || calls[1].Tables[0] != "accounts" {
		t.Fatalf("postgres datasource opts = %+v", calls)
	}
	if calls[2].Driver != "mysql" || calls[2].DSN != "user:pass@tcp(localhost:3306)/events" || calls[2].Dir != "/tmp/events" || calls[2].Tables[0] != "events" {
		t.Fatalf("mysql mixed datasource opts = %+v", calls[2])
	}
}

func TestExecuteModelDatasourcePassesStyle(t *testing.T) {
	old := runModelDatasource
	defer func() { runModelDatasource = old }()
	var calls []generator.ModelDatasourceOptions
	runModelDatasource = func(opts generator.ModelDatasourceOptions) error {
		calls = append(calls, opts)
		return nil
	}

	if err := Execute([]string{"model", "mysql", "datasource", "--url", "user:pass@tcp(localhost:3306)/app", "--style", "gorm"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"model", "pg", "datasource", "--url", "postgres://localhost/app", "--style", "gorm"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	for _, call := range calls {
		if call.Style != "gorm" {
			t.Fatalf("datasource style = %q, want gorm in opts %+v", call.Style, call)
		}
	}
}

func TestExecuteModelGoctlCompatibleInputAliases(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE books (
  id bigint primary key,
  title varchar(128) not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{
		"model", "mysql", "ddl", ddlPath,
		"--dir", outDir,
		"--style", "go_zero",
		"--cache",
		"--home", filepath.Join(dir, "templates"),
		"--remote", "https://example.invalid/model.git",
		"--branch", "main",
		"--idea",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "model", "entity", "book_gen.go")); err != nil {
		t.Fatalf("expected generated model from positional ddl: %v", err)
	}

	old := runModelDatasource
	defer func() { runModelDatasource = old }()
	var calls []generator.ModelDatasourceOptions
	runModelDatasource = func(opts generator.ModelDatasourceOptions) error {
		calls = append(calls, opts)
		return nil
	}

	if err := Execute([]string{
		"model", "mysql", "datasource", "user:pass@tcp(localhost:3306)/app",
		"--tables", "users",
		"--database", "app",
		"--style", "go_zero",
		"--cache",
		"--home", filepath.Join(dir, "templates"),
		"--remote", "https://example.invalid/model.git",
		"--branch", "main",
		"--idea",
	}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{
		"model", "pg", "datasource",
		"--dsn", "postgres://localhost/app",
		"--tables", "accounts",
		"--schema", "public",
		"--home", filepath.Join(dir, "templates"),
		"--remote", "https://example.invalid/model.git",
		"--branch", "main",
		"--idea",
	}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"model", "postgresql", "datasource", "--datasource", "postgres://localhost/audit", "--table", "audit_logs", "--database", "audit", "--schema", "public"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("datasource calls = %d, want 3", len(calls))
	}
	if calls[0].Driver != "mysql" || calls[0].DSN != "user:pass@tcp(localhost:3306)/app" || calls[0].Tables[0] != "users" {
		t.Fatalf("mysql positional datasource opts = %+v", calls[0])
	}
	if len(calls[0].Tables) != 1 {
		t.Fatalf("mysql --tables parsed tables = %#v, want one table", calls[0].Tables)
	}
	if calls[0].Database != "app" || !calls[0].Cache {
		t.Fatalf("mysql datasource should preserve database/cache opts = %+v", calls[0])
	}
	if calls[1].Driver != "postgres" || calls[1].DSN != "postgres://localhost/app" || calls[1].Tables[0] != "accounts" {
		t.Fatalf("postgres --dsn datasource opts = %+v", calls[1])
	}
	if len(calls[1].Tables) != 1 {
		t.Fatalf("postgres --tables parsed tables = %#v, want one table", calls[1].Tables)
	}
	if calls[1].Schema != "public" {
		t.Fatalf("postgres datasource should preserve schema opts = %+v", calls[1])
	}
	if calls[2].Driver != "postgres" || calls[2].DSN != "postgres://localhost/audit" || calls[2].Tables[0] != "audit_logs" {
		t.Fatalf("postgres --datasource opts = %+v", calls[2])
	}
	if len(calls[2].Tables) != 1 {
		t.Fatalf("postgres --table parsed tables = %#v, want one table", calls[2].Tables)
	}
}

func TestExecuteModelAcceptsGoctlShortAndSingleDashFlags(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE books (
  id bigint primary key,
  title varchar(128) not null
);
CREATE TABLE authors (
  id bigint primary key,
  name varchar(64) not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "ddl-out")
	if err := Execute([]string{
		"model", "gen",
		"-s", ddlPath,
		"-d", outDir,
		"-t", "books",
		"-cache",
		"-strict",
		"-ignore-columns", "deleted_at",
		"-prefix", "pre_",
		"-database", "app",
		"-home", filepath.Join(dir, "templates"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "model", "entity", "book_gen.go")); err != nil {
		t.Fatalf("expected generated model from -s/-d/-t: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "model", "entity", "author_gen.go")); err == nil {
		t.Fatal("unexpected author entity file for -t filtered generation")
	}

	old := runModelDatasource
	defer func() { runModelDatasource = old }()
	var calls []generator.ModelDatasourceOptions
	runModelDatasource = func(opts generator.ModelDatasourceOptions) error {
		calls = append(calls, opts)
		return nil
	}

	if err := Execute([]string{
		"model", "mysql", "datasource",
		"-datasource", "user:pass@tcp(localhost:3306)/app",
		"-t", "users",
		"-d", filepath.Join(dir, "mysql-out"),
		"-c",
		"-p", "pre_",
		"-i", "deleted_at",
	}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{
		"model", "pg", "datasource", "postgres://localhost/app",
		"-t", "accounts",
		"-d", filepath.Join(dir, "pg-out"),
		"-s", "public",
		"-c",
		"-p", "pre_",
		"-i", "deleted_at",
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("datasource calls = %d, want 2", len(calls))
	}
	if calls[0].Driver != "mysql" || calls[0].DSN != "user:pass@tcp(localhost:3306)/app" || calls[0].Dir != filepath.Join(dir, "mysql-out") || calls[0].Tables[0] != "users" {
		t.Fatalf("mysql short flag datasource opts = %+v", calls[0])
	}
	if calls[1].Driver != "postgres" || calls[1].DSN != "postgres://localhost/app" || calls[1].Dir != filepath.Join(dir, "pg-out") || calls[1].Tables[0] != "accounts" {
		t.Fatalf("postgres short flag datasource opts = %+v", calls[1])
	}

	mongoDir := filepath.Join(dir, "mongo-out")
	if err := Execute([]string{
		"model", "mongo",
		"-t", "UserProfile",
		"-d", mongoDir,
		"-c",
		"-p", "pre_",
		"-e",
		"-style", "go_zero",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mongoDir, "user_profile.go")); err != nil {
		t.Fatalf("expected generated mongo model from -t/-d: %v", err)
	}
	mongoData, err := os.ReadFile(filepath.Join(mongoDir, "user_profile.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mongoData), "func NewCachedUserProfileRepo") || !strings.Contains(string(mongoData), "FindMany(ctx context.Context, filter any, limit int, offset int)") {
		t.Fatalf("mongo short flags should enable cache and real query helpers:\n%s", mongoData)
	}
}

func TestExecuteModelDatasourceRejectsEmptyURL(t *testing.T) {
	if err := Execute([]string{"model", "mysql", "datasource"}); err == nil || !strings.Contains(err.Error(), "datasource url is required") {
		t.Fatalf("mysql datasource empty url error = %v", err)
	}
	if err := Execute([]string{"model", "pg", "datasource"}); err == nil || !strings.Contains(err.Error(), "datasource url is required") {
		t.Fatalf("postgres datasource empty url error = %v", err)
	}
}

func TestExecuteModelGenWithTableFilter(t *testing.T) {
	dir := t.TempDir()
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE books (
  id bigint primary key,
  title varchar(128) not null
);
CREATE TABLE authors (
  id bigint primary key,
  name varchar(64) not null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := Execute([]string{"model", "gen", "--ddl", ddlPath, "--dir", outDir, "--tables", "books"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "model", "entity", "book_gen.go")); err != nil {
		t.Fatalf("expected filtered book entity file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "model", "entity", "author_gen.go")); err == nil {
		t.Fatal("unexpected author entity file for table-filtered generation")
	}
}

func TestExecuteModelMongo(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"model", "mongo", "--type", "UserProfile", "--dir", dir, "--package", "model"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "user_profile.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"NewUserProfileRepo",
		"FindMany(ctx context.Context, filter any, limit int, offset int)",
		"Count(ctx context.Context, filter any) (int64, error)",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated mongo model missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "NewCachedUserProfileRepo") {
		t.Fatalf("generated mongo model should not include cache helper without --cache:\n%s", data)
	}
}

func TestExecuteModelMongoCacheAndPrefix(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"model", "mongo", "--type", "PreUserProfile", "--prefix", "Pre", "--cache", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "user_profile.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "type UserProfile struct") || !strings.Contains(string(data), "func NewCachedUserProfileRepo") {
		t.Fatalf("generated mongo model = %s", data)
	}
}

func TestExecuteModelMongoDriverStyle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/shop\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"model", "mongo", "--type", "UserProfile", "--cache", "--style", "driver", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "user_profile.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"go.mongodb.org/mongo-driver/mongo"`) || !strings.Contains(string(data), "FindByHexID") {
		t.Fatalf("generated mongo driver model = %s", data)
	}
	goModData, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goModData), "go.mongodb.org/mongo-driver") {
		t.Fatalf("mongo driver style should update go.mod:\n%s", goModData)
	}
}

func TestExecuteAPIFormatAndDoc(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
Name string
}
type PingResp {
Message string
}
service user-api {
@handler ping
get /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "format", "--file", apiPath}); err != nil {
		t.Fatal(err)
	}
	formatted, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(formatted), "  Name string") {
		t.Fatalf("api format result = %s", formatted)
	}
	formatOut := filepath.Join(dir, "formatted.api")
	if err := Execute([]string{"api", "format", "--api", apiPath, "--o", formatOut, "--iu", "--declare"}); err != nil {
		t.Fatal(err)
	}
	outData, err := os.ReadFile(formatOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outData), "  Name string") {
		t.Fatalf("api format --o result = %s", outData)
	}
	dirFormatPath := filepath.Join(dir, "format-dir", "nested", "dir.api")
	if err := os.MkdirAll(filepath.Dir(dirFormatPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirFormatPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "format", "--dir", filepath.Join(dir, "format-dir"), "--iu"}); err != nil {
		t.Fatal(err)
	}
	dirFormatted, err := os.ReadFile(dirFormatPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dirFormatted), "  Name string") {
		t.Fatalf("api format --dir result = %s", dirFormatted)
	}
	if err := Execute([]string{"api", "format", "--dir", filepath.Join(dir, "format-dir"), "--o", filepath.Join(dir, "bad.api")}); err == nil {
		t.Fatal("api format --dir with --o succeeded, want error")
	}
	docDir := filepath.Join(dir, "docs")
	if err := Execute([]string{"api", "doc", "--file", apiPath, "--dir", docDir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "doc", "--file", apiPath, "--dir", docDir, "--format", "openapi"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "doc", "--file", apiPath, "--dir", docDir, "--format", "yaml"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "doc", "--file", apiPath, "--dir", docDir, "--oas3", "--json"}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"user.md", "user.json", "user.yaml"} {
		if _, err := os.Stat(filepath.Join(docDir, rel)); err != nil {
			t.Fatalf("expected api doc file %s: %v", rel, err)
		}
	}
}

func TestAPIFormatStdinAndControlPlaneVerificationCoverageBuffer(t *testing.T) {
	dir := t.TempDir()
	api := `type PingResp {
Message string
}
service user-api {
@handler ping
get /ping returns (PingResp)
}
`

	t.Run("api format stdin writes output file and stdout", func(t *testing.T) {
		oldStdin := os.Stdin
		readEnd, writeEnd, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe stdin: %v", err)
		}
		os.Stdin = readEnd
		defer func() { os.Stdin = oldStdin }()
		_, _ = writeEnd.WriteString(api)
		_ = writeEnd.Close()

		outFile := filepath.Join(dir, "stdin.api")
		if err := apiFormatCommand([]string{"--stdin", "--output", outFile}); err != nil {
			t.Fatalf("apiFormatCommand stdin output: %v", err)
		}
		data, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatalf("read stdin format output: %v", err)
		}
		if !strings.Contains(string(data), "  Message string") {
			t.Fatalf("stdin formatted output = %s", data)
		}

		readEnd2, writeEnd2, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe stdin second: %v", err)
		}
		os.Stdin = readEnd2
		_, _ = writeEnd2.WriteString(api)
		_ = writeEnd2.Close()
		var stdout bytes.Buffer
		if err := withCommandIO(IOStreams{Out: &stdout}, outputText, verbosityNormal, func() error {
			return apiFormatCommand([]string{"--stdin"})
		}); err != nil {
			t.Fatalf("apiFormatCommand stdin stdout: %v", err)
		}
		if !strings.Contains(stdout.String(), "service user-api") {
			t.Fatalf("stdin stdout output = %q", stdout.String())
		}
	})

	t.Run("control-plane snapshot assertion handles skipped and failed cases", func(t *testing.T) {
		if got := runAIProjectControlPlaneSnapshotAssertion(dir, 0); got.Status != "failed" || !strings.Contains(got.Error, "timeout") {
			t.Fatalf("zero timeout result = %+v, want failed timeout", got)
		}
		if got := runAIProjectControlPlaneSnapshotAssertion(filepath.Join(dir, "missing"), time.Second); got.Status != "failed" {
			t.Fatalf("missing project result = %+v, want failed", got)
		}
		project := filepath.Join(dir, "project")
		configDir := filepath.Join(project, "internal", "config")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := runAIProjectControlPlaneSnapshotAssertion(project, time.Second); got.Status != "skipped" || !strings.Contains(got.Error, "does not expose") {
			t.Fatalf("missing config test result = %+v, want skipped", got)
		}
		if err := os.WriteFile(filepath.Join(configDir, "config_test.go"), []byte("package config\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := runAIProjectControlPlaneSnapshotAssertion(project, time.Second); got.Status != "skipped" || !strings.Contains(got.Error, "does not expose") {
			t.Fatalf("config test without contract result = %+v, want skipped", got)
		}
		if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/project\n\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "config_test.go"), []byte(`package config

import "testing"

func TestControlPlaneSnapshotExposesGeneratedContract(t *testing.T) {
	t.Fatal("contract mismatch")
}
`), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := runAIProjectControlPlaneSnapshotAssertion(project, 30*time.Second); got.Status != "failed" || !strings.Contains(got.Output, "contract mismatch") {
			t.Fatalf("failing config test result = %+v, want failed contract mismatch output", got)
		}
	})
}

func TestExecuteAPISwaggerAliasDefaultsOpenAPI(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	docDir := filepath.Join(dir, "docs")
	if err := Execute([]string{"api", "swagger", "--api", apiPath, "--dir", docDir}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(docDir, "user.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"openapi": "3.0.3"`) {
		t.Fatalf("api swagger output = %s", data)
	}

	custom := filepath.Join(dir, "custom-openapi.json")
	if err := Execute([]string{"api", "swagger", "--api", apiPath, "--o", custom}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("expected custom swagger output: %v", err)
	}
	if err := Execute([]string{"api", "swagger", "--api", apiPath, "--dir", docDir, "--filename", "custom.yaml", "--yaml"}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(docDir, "custom.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "openapi: 3.0.3") {
		t.Fatalf("api swagger yaml output = %s", data)
	}
}

func TestExecuteAPIRoute(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "routes")
	if err := Execute([]string{"api", "route", "--api", apiPath, "--dir", outDir, "--format", "markdown"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "user.routes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "| GET | `/ping` | `Ping` |") {
		t.Fatalf("api route output = %s", data)
	}
}

func TestExecuteAPIImport(t *testing.T) {
	dir := t.TempDir()
	openAPIPath := filepath.Join(dir, "openapi.json")
	spec := `{
  "openapi": "3.0.3",
  "info": {"title": "Pet API"},
  "paths": {
    "/pets/{id}": {
      "get": {
        "operationId": "getPet",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/PetResp"}}}}}
      }
    }
  },
  "components": {"schemas": {"PetResp": {"type": "object", "properties": {"name": {"type": "string"}}}}}
}`
	if err := os.WriteFile(openAPIPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "api")
	if err := Execute([]string{"api", "import", "--src", openAPIPath, "--dir", outDir, "--service", "pet-api"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "pet_api.api"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "get /pets/{id} (GetPetReq) returns (PetResp)") {
		t.Fatalf("api import output = %s", data)
	}

	aliasDir := filepath.Join(dir, "api-from")
	if err := Execute([]string{"api", "import", "--from", openAPIPath, "--dir", aliasDir, "--service", "pet-api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(aliasDir, "pet_api.api")); err != nil {
		t.Fatalf("expected api import --from output: %v", err)
	}

	positionalDir := filepath.Join(dir, "api-positional")
	if err := Execute([]string{"api", "import", openAPIPath, "--dir", positionalDir, "--service", "pet-api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(positionalDir, "pet_api.api")); err != nil {
		t.Fatalf("expected api import positional source output: %v", err)
	}
}

func TestExecuteAPIDiff(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.api")
	targetPath := filepath.Join(dir, "target.api")
	base := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	target := `type PingResp {
  Message string
}
type PongResp {
  Ok bool
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
  @handler pong
  post /pong returns (PongResp)
}
`
	if err := os.WriteFile(basePath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte(target), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "diff")
	if err := Execute([]string{"api", "diff", "--base", basePath, "--target", targetPath, "--dir", outDir, "--format", "markdown"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "target.diff.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "## Added routes") || !strings.Contains(string(data), "| POST | `/pong` |") {
		t.Fatalf("api diff output = %s", data)
	}

	positionalOut := filepath.Join(dir, "positional-diff.json")
	if err := Execute([]string{"api", "diff", basePath, targetPath, "--o", positionalOut, "--format", "json"}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(positionalOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"method": "POST"`) || !strings.Contains(string(data), `"path": "/pong"`) {
		t.Fatalf("api diff positional output = %s", data)
	}

	mixedOut := filepath.Join(dir, "mixed-diff.json")
	if err := Execute([]string{"api", "diff", basePath, targetPath, "--format", "json", "--o", mixedOut}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(mixedOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"method": "POST"`) || !strings.Contains(string(data), `"path": "/pong"`) {
		t.Fatalf("api diff mixed positional/flags output = %s", data)
	}
}

func TestExecuteBreakingAcceptsMixedPositionalsAndFlags(t *testing.T) {
	dir := t.TempDir()
	baseAPIPath := filepath.Join(dir, "base.api")
	targetAPIPath := filepath.Join(dir, "target.api")
	baseAPI := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	targetAPI := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	if err := os.WriteFile(baseAPIPath, []byte(baseAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetAPIPath, []byte(targetAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	apiOut := captureStdout(t, func() {
		if err := Execute([]string{"api", "breaking", baseAPIPath, "--target", targetAPIPath}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(apiOut, "No breaking changes") {
		t.Fatalf("api breaking mixed positional/flags output = %s", apiOut)
	}

	baseProtoPath := filepath.Join(dir, "base.proto")
	targetProtoPath := filepath.Join(dir, "target.proto")
	baseProto := `syntax = "proto3";
package greeter.v1;
message HelloReq {
  string name = 1;
}
message HelloResp {
  string message = 1;
}
service Greeter {
  rpc Hello(HelloReq) returns (HelloResp);
}
`
	targetProto := baseProto
	if err := os.WriteFile(baseProtoPath, []byte(baseProto), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetProtoPath, []byte(targetProto), 0o644); err != nil {
		t.Fatal(err)
	}
	rpcOut := captureStdout(t, func() {
		if err := Execute([]string{"rpc", "breaking", baseProtoPath, "--target", targetProtoPath}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(rpcOut, "No breaking changes") {
		t.Fatalf("rpc breaking mixed positional/flags output = %s", rpcOut)
	}
}

func TestExecuteRPCDocGeneratesOpenAPIFromProto(t *testing.T) {
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	if err := os.WriteFile(protoPath, []byte(`syntax = "proto3";
package greeter.v1;
message HelloReq { string name = 1; }
message HelloResp { string message = 1; }
service Greeter {
  rpc Hello(HelloReq) returns (HelloResp) {
    option (google.api.http) = {
      get: "/v1/hello/{name}"
    };
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "greeter-openapi.json")
	if err := Execute([]string{"rpc", "doc", "--file", protoPath, "--output", outPath, "--format", "openapi"}); err != nil {
		t.Fatalf("rpc doc: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"openapi": "3.0.3"`, `"/v1/hello/{name}"`, `"get"`, `"HelloReq"`, `"HelloResp"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("rpc doc output missing %q:\n%s", want, data)
		}
	}
}

func TestExecuteRPCDescriptorCommand(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.json")
	targetPath := filepath.Join(dir, "target.json")
	base := `{"name":"greeter","version":"v1","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloResp","timeout":1000000000},{"name":"Legacy","request":"LegacyReq","response":"LegacyResp"}]}`
	target := `{"name":"greeter","version":"v2","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloRespV2","timeout":500000000},{"name":"Health","request":"HealthReq","response":"HealthResp"}]}`
	if err := os.WriteFile(basePath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte(target), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		err := Execute([]string{"rpc", "descriptor", basePath, "--target", targetPath})
		if !errors.Is(err, generator.ErrBreakingChanges) {
			t.Fatalf("rpc descriptor error = %v, want ErrBreakingChanges", err)
		}
	})
	if !strings.Contains(out, "Descriptor compatibility: 2 breaking, 1 warning(s)") || !strings.Contains(out, "greeter/SayHello response") || !strings.Contains(out, "greeter/Legacy") {
		t.Fatalf("rpc descriptor output = %s", out)
	}

	jsonOut := captureStdout(t, func() {
		err := Execute([]string{"rpc", "descriptor", "--base", basePath, "--target", targetPath, "--format", "json"})
		if !errors.Is(err, generator.ErrBreakingChanges) {
			t.Fatalf("rpc descriptor json error = %v, want ErrBreakingChanges", err)
		}
	})
	var report struct {
		Breaking int `json:"breaking"`
		Warnings int `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("unmarshal descriptor report: %v\n%s", err, jsonOut)
	}
	if report.Breaking != 2 || report.Warnings != 1 {
		t.Fatalf("descriptor report = %#v, want 2 breaking and 1 warning", report)
	}
}

func TestExecuteRPCBreakingUsesRuntimeDescriptorRules(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.proto")
	targetPath := filepath.Join(dir, "target.proto")
	base := `syntax = "proto3";
package greeter.v1;
message HelloReq {
  string name = 1;
}
message HelloResp {
  string message = 1;
}
service Greeter {
  rpc Hello(HelloReq) returns (HelloResp);
}
`
	target := `syntax = "proto3";
package greeter.v1;
message HelloReq {
  string name = 1;
}
message HelloRespV2 {
  string message = 1;
}
service Greeter {
  rpc Hello(HelloReq) returns (HelloRespV2);
  rpc Health(HelloReq) returns (HelloRespV2);
}
`
	if err := os.WriteFile(basePath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte(target), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		err := Execute([]string{"rpc", "breaking", "--base", basePath, "--target", targetPath})
		if !errors.Is(err, generator.ErrBreakingChanges) {
			t.Fatalf("rpc breaking error = %v, want ErrBreakingChanges", err)
		}
	})
	if !strings.Contains(out, "Descriptor compatibility: 2 breaking") || !strings.Contains(out, "greeter.v1.Greeter/Hello response") || !strings.Contains(out, "greeter.v1.Greeter/Health") || !strings.Contains(out, "message HelloResp") {
		t.Fatalf("rpc breaking descriptor output = %s", out)
	}
}

func TestExecuteAPIClientGeneration(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  post /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "client")
	if err := Execute([]string{"api", "client", "--file", apiPath, "--dir", outDir, "--language", "typescript", "--base-url", "http://127.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "ts", "--api", apiPath, "--dir", outDir, "--caller", "UserCaller", "--unwrap"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "js", "--file", apiPath, "--dir", outDir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "dart", "--file", apiPath, "--dir", outDir, "--legacy", "--hostname", "api.example.com", "--scheme", "https"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "java", "--file", apiPath, "--dir", outDir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "kotlin", "--file", apiPath, "--dir", outDir, "--pkg", "com.example.api"}); err != nil {
		t.Fatal(err)
	}
	tsData, err := os.ReadFile(filepath.Join(outDir, "user_client.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tsData), "export interface PingReq") || !strings.Contains(string(tsData), "async ping") {
		t.Fatalf("generated typescript client = %s", tsData)
	}
	if _, err := os.Stat(filepath.Join(outDir, "user_client.js")); err != nil {
		t.Fatalf("expected generated javascript client: %v", err)
	}
	for _, rel := range []string{"user_client.dart", "APIClient.java", "APIClient.kt"} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Fatalf("expected generated client %s: %v", rel, err)
		}
	}
}

func TestExecuteAPITypes(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingReq {
  Name string
}
type PingResp {
  Message string
}
service user-api {
  @handler ping
  post /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "types")
	if err := Execute([]string{"api", "types", "--api", apiPath, "--dir", outDir, "--package", "dto"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package dto") || !strings.Contains(string(data), "type PingReq struct") {
		t.Fatalf("generated api types = %s", data)
	}
}

func TestExecuteAPIPlugin(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(dir, "plugin.sh")
	plugin := `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in
    -dir) shift; out="$1" ;;
  esac
  shift
done
mkdir -p "$out"
printf '%s' "$GOFLY_API_FILE" > "$out/plugin.txt"
`
	if err := os.WriteFile(pluginPath, []byte(plugin), 0o755); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "plugin-out")
	if err := Execute([]string{"api", "plugin", "--api", apiPath, "--dir", outDir, "-p", pluginPath, "--style", "go_zero"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "plugin.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != apiPath {
		t.Fatalf("plugin saw api file %q, want %q", data, apiPath)
	}
}

func TestAPIPluginCommandLegacyPassesExtraArgsWithoutShell(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	outDir := filepath.Join(dir, "plugin-out")
	pluginPath := filepath.Join(dir, "plugin.sh")
	plugin := `#!/bin/sh
args="$*"
while [ $# -gt 0 ]; do
  case "$1" in
    -dir) shift; out="$1" ;;
  esac
  shift
done
mkdir -p "$out"
printf '%s\n' "$args" > "$out/args.txt"
`
	if err := os.WriteFile(apiPath, []byte("service user-api {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginPath, []byte(plugin), 0o755); err != nil {
		t.Fatal(err)
	}

	extraArg := ";touch " + filepath.Join(dir, "should-not-exist")
	if err := apiPluginCommandLegacy(apiPath, pluginPath, outDir, "go_zero", []string{extraArg}); err != nil {
		t.Fatalf("apiPluginCommandLegacy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("shell metacharacter argument was executed or stat failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), extraArg) {
		t.Fatalf("legacy plugin args = %q, want literal extra arg %q", data, extraArg)
	}
}

func TestExecutePluginInstallRunRemoteAndUninstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	pluginPath := filepath.Join(dir, "remote-plugin")
	plugin := `#!/bin/sh
printf '%s' '{"version":"1","files":[{"path":"remote.txt","content":"remote-ok"}],"message":"remote ran"}'
`
	if err := os.WriteFile(pluginPath, []byte(plugin), 0o755); err != nil {
		t.Fatal(err)
	}
	remote := pluginPath + "@v1.0.0"

	installJSON := captureStdout(t, func() {
		if err := Execute([]string{"plugin", "install", "--remote", remote, "--json"}); err != nil {
			t.Fatalf("plugin install: %v", err)
		}
	})
	var installed generator.InstalledPlugin
	if err := json.Unmarshal([]byte(installJSON), &installed); err != nil {
		t.Fatalf("plugin install --json output = %s, error = %v", installJSON, err)
	}
	if installed.Remote != pluginPath || installed.Version != "v1.0.0" || installed.BinaryDigest == "" {
		t.Fatalf("plugin install --json metadata = %+v, want remote/version/digest", installed)
	}
	var runJSON string
	runStderr := captureStderr(t, func() {
		runJSON = captureStdout(t, func() {
			if err := Execute([]string{"plugin", "run", "--remote", remote, "--dir", dir, "--name", "hello", "--module", "example.com/hello", "--json"}); err != nil {
				t.Fatalf("plugin run --remote: %v", err)
			}
		})
	})
	if runStderr != "" {
		t.Fatalf("plugin run --json stderr = %q, want quiet diagnostics", runStderr)
	}
	var runResult struct {
		Plugins []struct {
			Plugin  string `json:"plugin"`
			Message string `json:"message"`
			Files   int    `json:"files"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(runJSON), &runResult); err != nil {
		t.Fatalf("plugin run --json output = %s, error = %v", runJSON, err)
	}
	if len(runResult.Plugins) != 1 || runResult.Plugins[0].Files != 1 || runResult.Plugins[0].Message != "remote ran" {
		t.Fatalf("plugin run --json result = %+v, want one generated file and message", runResult)
	}
	data, err := os.ReadFile(filepath.Join(dir, "remote.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "remote-ok" {
		t.Fatalf("remote plugin output = %q, want remote-ok", data)
	}
	listOut := captureStdout(t, func() {
		if err := Execute([]string{"plugin", "list"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(listOut, "cached") || !strings.Contains(listOut, remote) || !strings.Contains(listOut, "sha256:") {
		t.Fatalf("plugin list output = %q, want cached remote", listOut)
	}
	listJSON := captureStdout(t, func() {
		if err := Execute([]string{"plugin", "list", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var listed struct {
		Installed []generator.InstalledPlugin `json:"installed"`
	}
	if err := json.Unmarshal([]byte(listJSON), &listed); err != nil {
		t.Fatalf("plugin list --json output = %s, error = %v", listJSON, err)
	}
	if len(listed.Installed) != 1 || listed.Installed[0].BinaryDigest == "" {
		t.Fatalf("plugin list --json installed = %+v, want digest", listed.Installed)
	}
	uninstallJSON := captureStdout(t, func() {
		if err := Execute([]string{"plugin", "uninstall", "--remote", remote, "--json"}); err != nil {
			t.Fatalf("plugin uninstall: %v", err)
		}
	})
	var uninstalled struct {
		Remote string `json:"remote"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal([]byte(uninstallJSON), &uninstalled); err != nil {
		t.Fatalf("plugin uninstall --json output = %s, error = %v", uninstallJSON, err)
	}
	if uninstalled.Remote != remote || uninstalled.Path == "" {
		t.Fatalf("plugin uninstall --json = %+v, want remote and cache path", uninstalled)
	}
}

func TestExecutePluginSearchRegistry(t *testing.T) {
	dir := t.TempDir()
	registry := filepath.Join(dir, "plugins.json")
	data := `{
  "version": "v1",
  "plugins": [
    {
      "name": "redis-cache",
      "remote": "https://example.com/redis-cache",
      "version": "v0.2.0",
      "protocol": "1",
      "checksum": "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "source": "https://github.com/example/gofly-redis-cache",
      "description": "Redis cache plugin",
      "tags": ["redis", "cache"],
      "manifest": {
        "name": "redis-cache",
        "version": "v0.2.0",
        "compatibleVersions": ["1"],
        "capabilities": ["generate:file"],
        "permissions": ["filesystem:write-relative"]
      }
    },
    {
      "name": "auth-jwt",
      "remote": "https://example.com/auth-jwt",
      "version": "v0.1.0",
      "protocol": "1",
      "checksum": "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
      "source": "https://github.com/example/gofly-auth-jwt",
      "description": "JWT auth plugin",
      "tags": ["auth", "jwt"],
      "manifest": {
        "name": "auth-jwt",
        "version": "v0.1.0",
        "compatibleVersions": ["1"],
        "capabilities": ["generate:file"],
        "permissions": ["filesystem:write-relative"]
      }
    }
  ]
}`
	if err := os.WriteFile(registry, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	textOut := captureStdout(t, func() {
		if err := Execute([]string{"plugin", "search", "--registry", registry, "redis"}); err != nil {
			t.Fatalf("plugin search: %v", err)
		}
	})
	if !strings.Contains(textOut, "redis-cache@v0.2.0") || strings.Contains(textOut, "auth-jwt") {
		t.Fatalf("plugin search text output = %q, want only redis-cache", textOut)
	}

	jsonOut := captureStdout(t, func() {
		if err := Execute([]string{"plugin", "search", "--registry", registry, "--query", "auth", "--json"}); err != nil {
			t.Fatalf("plugin search --json: %v", err)
		}
	})
	var got struct {
		Registry string                          `json:"registry"`
		Query    string                          `json:"query"`
		Plugins  []generator.PluginRegistryEntry `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("plugin search --json output = %s, error = %v", jsonOut, err)
	}
	if got.Registry != registry || got.Query != "auth" || len(got.Plugins) != 1 || got.Plugins[0].Name != "auth-jwt" {
		t.Fatalf("plugin search --json = %+v, want auth-jwt match", got)
	}
}

func TestExecutePluginRunJSONReportsWrittenFiles(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "plain-plugin")
	plugin := `#!/bin/sh
printf '%s' 'plain text that is not plugin response json'
`
	if err := os.WriteFile(pluginPath, []byte(plugin), 0o755); err != nil {
		t.Fatal(err)
	}

	runJSON := captureStdout(t, func() {
		if err := Execute([]string{"plugin", "run", pluginPath, "--dir", dir, "--json"}); err != nil {
			t.Fatalf("plugin run --json: %v", err)
		}
	})
	var runResult struct {
		Plugins []struct {
			Files int `json:"files"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(runJSON), &runResult); err != nil {
		t.Fatalf("plugin run --json output = %s, error = %v", runJSON, err)
	}
	if len(runResult.Plugins) != 1 || runResult.Plugins[0].Files != 0 {
		t.Fatalf("plugin run --json result = %+v, want zero written files", runResult)
	}
}

func TestExecutePluginRunGoPluginTraversesDirectory(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := map[string]string{
		filepath.Join(pluginDir, "a-plugin"): `#!/bin/sh
printf '%s' '{"version":"1","files":[{"path":"a.txt","content":"a"}]}'
`,
		filepath.Join(pluginDir, "nested", "b-plugin"): `#!/bin/sh
printf '%s' '{"version":"1","files":[{"path":"b.txt","content":"b"}]}'
`,
	}
	for path, content := range plugins {
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "README.txt"), []byte("skip"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Execute([]string{"plugin", "run", "--go-plugin", pluginDir, "--dir", dir, "--command", "service"}); err != nil {
		t.Fatalf("plugin run --go-plugin: %v", err)
	}
	for name, want := range map[string]string{"a.txt": "a", "b.txt": "b"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", name, data, want)
		}
	}
}

func TestExecuteAPIMiddleware(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"gen", "middleware", "Auth", "AuditLog", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		filepath.Join("internal", "middleware", "auth.go"),
		filepath.Join("internal", "middleware", "audit_log.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected generated middleware %s: %v", rel, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "internal", "middleware", "auth.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func AuthMiddleware() rest.Middleware") {
		t.Fatalf("generated middleware = %s", data)
	}
}

func TestExecuteAPIMiddlewareFromAPI(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `@server(
  middleware: Auth, AuditLog
)
type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "svc")
	if err := Execute([]string{"api", "middleware", "--api", apiPath, "--dir", outDir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		filepath.Join("internal", "middleware", "auth.go"),
		filepath.Join("internal", "middleware", "audit_log.go"),
	} {
		if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
			t.Fatalf("expected api middleware file %s: %v", rel, err)
		}
	}
}

func TestExecuteAPIMiddlewareFromPluralAPI(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `@server(middlewares: Auth, AuditLog)
type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "svc")
	if err := Execute([]string{"api", "middleware", "--api", apiPath, "--dir", outDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "internal", "middleware", "audit_log.go")); err != nil {
		t.Fatalf("expected plural api middleware file: %v", err)
	}
}

func TestMiddlewareNamesFromLine(t *testing.T) {
	got := middlewareNamesFromLine(`@server(middleware: Auth, AuditLog)`)
	if len(got) != 2 || got[0] != "Auth" || got[1] != "AuditLog" {
		t.Fatalf("middleware names = %v, want Auth/AuditLog", got)
	}
	got = middlewareNamesFromLine(`middlewares: [Trace, Metrics]`)
	if len(got) != 2 || got[0] != "Trace" || got[1] != "Metrics" {
		t.Fatalf("middleware names = %v, want Trace/Metrics", got)
	}
}

func TestExecuteAPIValidateAlias(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingResp {
  Message string
}
service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"api", "validate", "--file", apiPath}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteEnvAndCompletion(t *testing.T) {
	if err := Execute([]string{"env"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"env", "--write", "GOFLY_TEST_ENV=ok", "--verbose"}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("GOFLY_TEST_ENV"); got != "ok" {
		t.Fatalf("GOFLY_TEST_ENV = %q, want ok", got)
	}
	if err := Execute([]string{"env", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"env", "install"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"env", "check"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"env", "check", "--install"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"env", "check", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"completion", "bash"}); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteShellCompletionCoverage(t *testing.T) {
	completeBash := captureStdout(t, func() {
		if err := Execute([]string{"complete", "handler", "bash"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"version new gen generate handler rpc api model docker kube template quickstart migrate migration env bug upgrade config feature plugin completion complete",
		"gen) commands=\"handler rpc api rest middleware model gateway\"",
		"generate) commands=\"handler rpc api rest middleware model gateway\"",
		"api) commands=\"new go gen check validate breaking break format fmt doc docs swagger client ts typescript js javascript dart java kotlin kt types route routes import diff plugin middleware\"",
		"rpc) commands=\"new idl inspect thrift thrift2proto client server middleware lint deps gen protoc check doc docs swagger openapi breaking descriptor plugin template tpl\"",
		"rpc:template|rpc:tpl) commands=\"init list ls clean update revert\"",
		"model:mysql|model:pg|model:postgres|model:postgresql) commands=\"ddl datasource\"",
		"complete:handler) commands=\"bash zsh fish powershell pwsh\"",
		"kube) commands=\"deploy deployment service svc ingress ing configmap cm job\"",
		"config) commands=\"init show get set clean\"",
		"ai|tools) commands=\"manifest plan new complete stream doctor\"",
	} {
		if !strings.Contains(completeBash, want) {
			t.Fatalf("complete bash missing %q:\n%s", want, completeBash)
		}
	}

	completionBash := captureStdout(t, func() {
		if err := Execute([]string{"completion", "bash"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"config", "feature", "plugin", "complete", "migration", "ai", "tools"} {
		if !strings.Contains(completionBash, want) {
			t.Fatalf("completion bash missing %q:\n%s", want, completionBash)
		}
	}
	for _, want := range []string{
		"case \"$cmd\" in",
		"rpc) commands=\"new idl inspect thrift thrift2proto client server middleware lint deps gen protoc check doc docs swagger openapi breaking descriptor plugin template tpl\"",
		"api) commands=\"new go gen check validate breaking break format fmt doc docs swagger client ts typescript js javascript dart java kotlin kt types route routes import diff plugin middleware\"",
		"plugin) commands=\"list ls install uninstall remove rm run\"",
		"rpc:template|rpc:tpl) commands=\"init list ls clean update revert\"",
		"completion) commands=\"bash zsh fish powershell pwsh\"",
		"ai|tools) commands=\"manifest plan new complete stream doctor\"",
		"complete:handler) commands=\"bash zsh fish powershell pwsh\"",
	} {
		if !strings.Contains(completionBash, want) {
			t.Fatalf("completion bash missing subcommand completion %q:\n%s", want, completionBash)
		}
	}

	completionFish := captureStdout(t, func() {
		if err := Execute([]string{"completion", "fish"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		`__fish_seen_subcommand_from generate' -a "handler\tGenerate REST handler`,
		`__fish_seen_subcommand_from gen' -a "handler\tGenerate REST handler`,
		`__fish_seen_subcommand_from rpc' -a "new\tCreate RPC service`,
		`__fish_seen_subcommand_from rpc; and __fish_seen_subcommand_from tpl' -a "init\tWrite default templates`,
		`__fish_seen_subcommand_from api' -a "new\tCreate API service`,
		`__fish_seen_subcommand_from config' -a "init\tCreate config file`,
		`__fish_seen_subcommand_from plugin' -a "list\tList plugins`,
		`__fish_seen_subcommand_from model; and __fish_seen_subcommand_from postgres' -a "ddl\tGenerate from DDL`,
		`__fish_seen_subcommand_from completion' -a "bash\tBash completion`,
		`__fish_seen_subcommand_from ai' -a "manifest\tPrint AI tool manifest\nplan\tPlan AI-first project scaffold\nnew\tPlan or apply AI-first project scaffold\ncomplete\tRun governed noop completion\nstream\tRun governed streaming completion\ndoctor\tRun AI subsystem diagnostics"`,
		`__fish_seen_subcommand_from tools' -a "manifest\tPrint AI tool manifest alias\nplan\tPlan AI-first project scaffold alias\nnew\tPlan or apply AI-first project scaffold alias\ncomplete\tRun governed noop completion alias\nstream\tRun governed streaming completion alias\ndoctor\tRun AI subsystem diagnostics alias"`,
		`__fish_seen_subcommand_from complete; and __fish_seen_subcommand_from handler' -a "bash\tBash completion`,
	} {
		if !strings.Contains(completionFish, want) {
			t.Fatalf("completion fish missing %q:\n%s", want, completionFish)
		}
	}

	completionZsh := captureStdout(t, func() {
		if err := Execute([]string{"completion", "zsh"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"case \"$words[2]:$words[3]\" in",
		"gen|generate) commands=('handler:generate REST handler'",
		"rpc) commands=('new:create RPC service' 'idl:inspect IDL metadata' 'inspect:inspect IDL metadata alias' 'thrift:convert thrift to proto skeleton' 'thrift2proto:convert thrift alias' 'client:generate RPC client wrapper' 'server:generate RPC server stubs' 'middleware:generate gRPC middleware' 'lint:lint IDL contract' 'deps:list IDL imports' 'gen:generate RPC code' 'protoc:run protoc plugins' 'check:validate proto' 'doc:generate OpenAPI docs' 'docs:generate OpenAPI docs alias' 'swagger:generate OpenAPI docs alias' 'openapi:generate OpenAPI docs alias' 'breaking:compare compatibility' 'descriptor:compare runtime descriptors' 'plugin:run RPC plugin' 'template:manage templates' 'tpl:manage templates alias')",
		"typescript:generate TypeScript client",
		"routes:print routes",
		"plugin) commands=('list:list plugins' 'ls:list plugins' 'install:install remote plugin' 'uninstall:uninstall remote plugin' 'remove:uninstall remote plugin' 'rm:uninstall remote plugin' 'run:run plugin')",
		"completion) commands=('bash:bash completion' 'zsh:zsh completion' 'fish:fish completion' 'powershell:powershell completion' 'pwsh:powershell completion alias')",
		"ai|tools) commands=('manifest:print AI tool manifest' 'plan:plan AI-first project scaffold' 'new:plan or apply AI-first project scaffold' 'complete:run governed noop completion' 'stream:run governed streaming completion' 'doctor:run AI subsystem diagnostics')",
		"rpc:template|rpc:tpl) commands=('init:write templates'",
		"complete:handler) commands=('bash:bash completion' 'zsh:zsh completion' 'fish:fish completion' 'powershell:powershell completion' 'pwsh:powershell completion alias')",
	} {
		if !strings.Contains(completionZsh, want) {
			t.Fatalf("completion zsh missing %q:\n%s", want, completionZsh)
		}
	}

	powershell := captureStdout(t, func() {
		if err := Execute([]string{"completion", "powershell"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		`"config"`,
		`"complete"`,
		`"generate"`,
		`"rpc" { $commands = @("new", "idl", "inspect", "thrift", "thrift2proto", "client", "server", "middleware", "lint", "deps", "gen", "protoc", "check", "doc", "docs", "swagger", "openapi", "breaking", "descriptor", "plugin", "template", "tpl") }`,
		`"api" { $commands = @("new", "go", "gen", "check", "validate", "breaking", "break", "format", "fmt", "doc", "docs", "swagger", "client", "ts", "typescript", "js", "javascript", "dart", "java", "kotlin", "kt", "types", "route", "routes", "import", "diff", "plugin", "middleware") }`,
		`"plugin" { $commands = @("list", "ls", "install", "uninstall", "remove", "rm", "run") }`,
		`"completion" { $commands = @("bash", "zsh", "fish", "powershell", "pwsh") }`,
		`"ai" { $commands = @("manifest", "plan", "new", "complete", "stream", "doctor") }`,
		`"tools" { $commands = @("manifest", "plan", "new", "complete", "stream", "doctor") }`,
		`"rpc:template" { $commands = @("init", "list", "ls", "clean", "update", "revert") }`,
		`"rpc:tpl" { $commands = @("init", "list", "ls", "clean", "update", "revert") }`,
		`"complete:handler" { $commands = @("bash", "zsh", "fish", "powershell", "pwsh") }`,
	} {
		if !strings.Contains(powershell, want) {
			t.Fatalf("powershell completion missing %q:\n%s", want, powershell)
		}
	}

	completePowerShell := captureStdout(t, func() {
		if err := Execute([]string{"complete", "handler", "powershell"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(completePowerShell, `"complete:handler"`) || !strings.Contains(completePowerShell, `"pwsh"`) {
		t.Fatalf("complete handler powershell missing nested shell completion:\n%s", completePowerShell)
	}

	pwsh := captureStdout(t, func() {
		if err := Execute([]string{"completion", "pwsh"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(pwsh, "Register-ArgumentCompleter") || !strings.Contains(pwsh, `"pwsh"`) {
		t.Fatalf("completion pwsh should emit powershell completion with pwsh alias:\n%s", pwsh)
	}

	completePwsh := captureStdout(t, func() {
		if err := Execute([]string{"complete", "handler", "pwsh"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(completePwsh, "Register-ArgumentCompleter") || !strings.Contains(completePwsh, `"complete:handler"`) {
		t.Fatalf("complete handler pwsh should emit powershell completion:\n%s", completePwsh)
	}
}

func TestGenerateCompletionGoldenCommandSets(t *testing.T) {
	tests := []struct {
		name   string
		shell  string
		golden []string
	}{
		{
			name:  "bash command sets",
			shell: "bash",
			golden: []string{
				`commands="version new gen generate handler rpc api model docker kube template quickstart migrate migration env bug upgrade config feature plugin completion complete release doctor example examples ai tools"`,
				`plugin) commands="list ls install uninstall remove rm run" ;;`,
				`ai|tools) commands="manifest plan new complete stream doctor" ;;`,
				`completion) commands="bash zsh fish powershell pwsh" ;;`,
			},
		},
		{
			name:  "zsh command sets",
			shell: "zsh",
			golden: []string{
				`'plugin:list, install or run gofly plugins'`,
				`plugin) commands=('list:list plugins' 'ls:list plugins' 'install:install remote plugin' 'uninstall:uninstall remote plugin' 'remove:uninstall remote plugin' 'rm:uninstall remote plugin' 'run:run plugin') ;;`,
				`ai|tools) commands=('manifest:print AI tool manifest' 'plan:plan AI-first project scaffold' 'new:plan or apply AI-first project scaffold' 'complete:run governed noop completion' 'stream:run governed streaming completion' 'doctor:run AI subsystem diagnostics') ;;`,
				`completion) commands=('bash:bash completion' 'zsh:zsh completion' 'fish:fish completion' 'powershell:powershell completion' 'pwsh:powershell completion alias') ;;`,
			},
		},
		{
			name:  "fish command sets",
			shell: "fish",
			golden: []string{
				`complete -c gofly -f -a "version\tPrint version metadata`,
				`ai\tEmit AI tool manifest`,
				`tools\tEmit AI tool manifest alias`,
				`complete -c gofly -n '__fish_seen_subcommand_from plugin' -a "list\tList plugins`,
				`complete -c gofly -n '__fish_seen_subcommand_from ai' -a "manifest\tPrint AI tool manifest\nplan\tPlan AI-first project scaffold\nnew\tPlan or apply AI-first project scaffold\ncomplete\tRun governed noop completion\nstream\tRun governed streaming completion\ndoctor\tRun AI subsystem diagnostics"`,
				`complete -c gofly -n '__fish_seen_subcommand_from completion' -a "bash\tBash completion`,
			},
		},
		{
			name:  "powershell command sets",
			shell: "powershell",
			golden: []string{
				`$commands = @("version", "new", "gen", "generate", "handler", "rpc", "api", "model", "docker", "kube", "template", "quickstart", "migrate", "migration", "env", "bug", "upgrade", "config", "feature", "plugin", "completion", "complete", "release", "doctor", "example", "examples", "ai", "tools")`,
				`"plugin" { $commands = @("list", "ls", "install", "uninstall", "remove", "rm", "run") }`,
				`"ai" { $commands = @("manifest", "plan", "new", "complete", "stream", "doctor") }`,
				`"completion" { $commands = @("bash", "zsh", "fish", "powershell", "pwsh") }`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, err := generator.GenerateCompletion(tt.shell)
			if err != nil {
				t.Fatalf("GenerateCompletion(%q): %v", tt.shell, err)
			}
			for _, want := range tt.golden {
				if !strings.Contains(script, want) {
					t.Fatalf("GenerateCompletion(%q) missing golden snippet %q:\n%s", tt.shell, want, script)
				}
			}
		})
	}
}

func TestExecuteConfigShowAndGet(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"config", "init", "--dir", dir, "--name", "hello", "--module", "example.com/hello"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := Execute([]string{"config", "show", "--dir", dir}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "hello") {
		t.Fatalf("config show should contain service name: %q", out)
	}
	out = captureStdout(t, func() {
		if err := Execute([]string{"config", "get", "--dir", dir, "--key", "service"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "hello" {
		t.Fatalf("config get service = %q, want hello", out)
	}
}

func TestExecuteConfigSetAndGet(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"config", "init", "--dir", dir, "--name", "hello", "--module", "example.com/hello"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"config", "set", "--dir", dir, "--key", "style", "--value", "minimal"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := Execute([]string{"config", "get", "--dir", dir, "--key", "style"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "minimal" {
		t.Fatalf("config get style = %q, want minimal", out)
	}
}

func TestExecuteFeatureList(t *testing.T) {
	out := captureStdout(t, func() {
		if err := Execute([]string{"feature", "list"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "ecosystem-compat") {
		t.Fatalf("feature list should contain ecosystem-compat: %q", out)
	}
}

func TestExecuteConfigInvalidSubcommand(t *testing.T) {
	if err := Execute([]string{"config", "bogus"}); err == nil {
		t.Fatal("expected error for invalid config subcommand")
	}
}

func TestParseGlobalOutput(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantOut string
		wantRem []string
		wantErr bool
	}{
		{"empty", []string{}, outputText, []string{}, false},
		{"no output flag", []string{"version"}, outputText, []string{"version"}, false},
		{"output json next", []string{"--output", "json", "version"}, outputJSON, []string{"version"}, false},
		{"output text eq", []string{"--output=text", "version"}, outputText, []string{"version"}, false},
		{"output json eq", []string{"--output=json", "version"}, outputJSON, []string{"version"}, false},
		{"output missing value", []string{"--output"}, "", nil, true},
		{"output invalid", []string{"--output", "xml"}, "", nil, true},
		{"output eq invalid", []string{"--output=xml"}, "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, rem, err := parseGlobalOutput(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseGlobalOutput(%v) err = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if out != tt.wantOut {
				t.Fatalf("parseGlobalOutput(%v) out = %q, want %q", tt.args, out, tt.wantOut)
			}
			if !tt.wantErr && len(rem) != len(tt.wantRem) {
				t.Fatalf("parseGlobalOutput(%v) rem = %v, want %v", tt.args, rem, tt.wantRem)
			}
		})
	}
}

func TestPrintJSONError(t *testing.T) {
	if err := printJSON(make(chan int)); err == nil {
		t.Fatal("expected error for unmarshalable value")
	}
}

func TestAnsiColorAndRightPad(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("GOFLY_NO_COLOR", "")

	if got := ansiColor("31", ""); got != "" {
		t.Fatalf("ansiColor empty = %q", got)
	}
	if got := ansiColor("31", "x"); got != "\x1b[31mx\x1b[0m" {
		t.Fatalf("ansiColor = %q", got)
	}
	if got := rightPad("hello", 3); got != "hello" {
		t.Fatalf("rightPad no pad = %q", got)
	}
	if got := rightPad("hi", 5); got != "hi   " {
		t.Fatalf("rightPad = %q", got)
	}
}

func TestGenCommandBranches(t *testing.T) {
	if err := genCommand([]string{"gateway"}); err == nil {
		t.Fatal("gen gateway without args should error")
	}
	if err := genCommand([]string{"bogus"}); err == nil {
		t.Fatal("gen bogus should error")
	}
	if err := genCommand([]string{"middleware"}); err == nil {
		t.Fatal("gen middleware without args should error")
	}
	// help branch
	if err := genCommand([]string{"--help"}); err != nil {
		t.Fatalf("gen --help err = %v", err)
	}
	// empty args
	if err := genCommand([]string{}); err == nil {
		t.Fatal("gen empty should error")
	}
}

func TestExecuteConfigClean(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"config", "init", "--dir", dir, "--name", "hello", "--module", "example.com/hello"}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, ".gofly", "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config file: %v", err)
	}
	if err := Execute([]string{"config", "clean", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); err == nil {
		t.Fatal("config clean should remove config file")
	}
}

func TestExecuteConfigInitPersistsDefaultEcosystemFeature(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"config", "init", "--dir", dir, "--name", "hello", "--module", "example.com/hello"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := generator.LoadConfig(filepath.Join(dir, ".gofly", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cfg.Features, ","), "ecosystem-compat") {
		t.Fatalf("config init features = %v, want ecosystem-compat", cfg.Features)
	}
}

func TestExecuteConfigSetFeaturesValidatesAndAllowsEmptyList(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"config", "init", "--dir", dir, "--name", "hello", "--module", "example.com/hello"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"config", "set", "features", "missing-feature", "--dir", dir}); err == nil || !strings.Contains(err.Error(), `feature "missing-feature" is not registered`) {
		t.Fatalf("config set invalid feature error = %v", err)
	}
	if err := Execute([]string{"config", "set", "features", "", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := Execute([]string{
		"new", "api", "hello",
		"--module", "example.com/hello",
		"--dir", out,
		"--config", filepath.Join(dir, ".gofly", "config.json"),
		"--save-config=false",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "internal", "compat", "gozero", "adapter.go")); err == nil {
		t.Fatal("empty features config should explicitly disable default ecosystem compatibility adapters")
	}
}

func TestExecuteBugAndUpgrade(t *testing.T) {
	if err := Execute([]string{"bug"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"bug", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"upgrade"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"upgrade", "--version", "v0.1.0"}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"upgrade", "--version", "latest", "--json"}); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeCommandExecuteUsesInstallRunner(t *testing.T) {
	old := runUpgradeInstall
	t.Cleanup(func() { runUpgradeInstall = old })
	var gotTarget string
	var gotDeadline bool
	runUpgradeInstall = func(ctx context.Context, target string) ([]byte, error) {
		gotTarget = target
		_, gotDeadline = ctx.Deadline()
		return []byte("installed\n"), nil
	}
	if err := upgradeCommand([]string{"--execute", "--json", "--version", "v9.9.9", "--module", "example.com/gofly/cmd/gofly"}); err != nil {
		t.Fatal(err)
	}
	if gotTarget != "example.com/gofly/cmd/gofly@v9.9.9" {
		t.Fatalf("upgrade target = %q, want module@version", gotTarget)
	}
	if !gotDeadline {
		t.Fatal("upgrade install runner context has no deadline")
	}
}

func TestUpgradeCommandRejectsEmptyModuleOrVersion(t *testing.T) {
	if err := upgradeCommand([]string{"--module", "   "}); err == nil || !strings.Contains(err.Error(), "upgrade module is required") {
		t.Fatalf("empty module error = %v, want usage error", err)
	}
	if err := upgradeCommand([]string{"--version", "   "}); err == nil || !strings.Contains(err.Error(), "upgrade version is required") {
		t.Fatalf("empty version error = %v, want usage error", err)
	}
}

func TestExecuteDockerKubeAndTemplateInit(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"docker", "--name", "hello", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"kube", "--name", "hello", "--dir", dir, "--image", "example/hello:v1", "--namespace", "apps"}); err != nil {
		t.Fatal(err)
	}
	templateDir := filepath.Join(dir, "templates")
	if err := Execute([]string{"template", "init", "--dir", templateDir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"template", "list", "--dir", templateDir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"template", "update", "--dir", templateDir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"Dockerfile",
		"hello.yaml",
		filepath.Join("templates", "api.tpl"),
		filepath.Join("templates", "rpc.tpl"),
		filepath.Join("templates", "model.tpl"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected generated file %s: %v", rel, err)
		}
	}
	if err := Execute([]string{"template", "clean", "--dir", templateDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(templateDir); err == nil {
		t.Fatal("template clean should remove template directory")
	}
}

func TestExecuteRPCTemplateAlias(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := Execute([]string{"rpc", "template", "init", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rpc.tpl")); err != nil {
		t.Fatalf("expected rpc template from rpc template alias: %v", err)
	}
}

func TestExecuteTemplateRemoteLifecycle(t *testing.T) {
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(filepath.Join(remote, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	remoteAPI := "syntax = v1\n\nservice {{.Name}} {\n\t@handler RemotePing\n\tget /remote returns (string)\n}\n"
	if err := os.WriteFile(filepath.Join(remote, "templates", "api.tpl"), []byte(remoteAPI), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "templates", "rpc.tpl"), []byte("syntax = \"proto3\";\npackage {{.Name}}.remote;\nservice Remote{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	local := filepath.Join(dir, "templates")
	if err := Execute([]string{"template", "update", "--home", local, "--remote", remote, "--branch", "main"}); err != nil {
		t.Fatal(err)
	}
	apiOut := filepath.Join(dir, "hello.api")
	if err := Execute([]string{"api", "-o", apiOut, "--home", local}); err != nil {
		t.Fatal(err)
	}
	apiData, err := os.ReadFile(apiOut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apiData), "RemotePing") {
		t.Fatalf("api command should use synced remote template:\n%s", apiData)
	}
	if err := Execute([]string{"template", "revert", "--home", local}); err != nil {
		t.Fatal(err)
	}
	reverted, err := os.ReadFile(filepath.Join(local, "api.tpl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reverted), "RemotePing") || !strings.Contains(string(reverted), "@handler Ping") {
		t.Fatalf("template revert should restore builtin api template:\n%s", reverted)
	}
}

func TestExecuteDockerAndKubeGoctlOptions(t *testing.T) {
	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "Dockerfile.custom")
	if err := Execute([]string{
		"docker", "hello",
		"--go", "./cmd/server",
		"--exe", "server",
		"--base", "alpine:3.20",
		"--version", "1.25",
		"--port", "8080",
		"--tz", "Asia/Shanghai",
		"--home", filepath.Join(dir, "templates"),
		"--remote", "https://example.invalid/templates.git",
		"--branch", "main",
		"--o", dockerPath,
	}); err != nil {
		t.Fatal(err)
	}
	dockerData, err := os.ReadFile(dockerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"go build -o /out/server ./cmd/server", "FROM alpine:3.20", `ENTRYPOINT ["/app/server"]`} {
		if !strings.Contains(string(dockerData), want) {
			t.Fatalf("Dockerfile missing %q:\n%s", want, dockerData)
		}
	}
	kubePath := filepath.Join(dir, "deploy.yaml")
	if err := Execute([]string{
		"kube", "deploy", "hello",
		"--image", "example/hello:v2",
		"--namespace", "apps",
		"--secret", "regcred",
		"--requestCpu", "100m",
		"--requestMem", "128Mi",
		"--limitCpu", "500m",
		"--limitMem", "512Mi",
		"--replicas", "3",
		"--revisions", "5",
		"--targetPort", "9090",
		"--nodePort", "30090",
		"--minReplicas", "2",
		"--maxReplicas", "5",
		"--imagePullPolicy", "IfNotPresent",
		"--serviceAccount", "hello-sa",
		"--home", filepath.Join(dir, "templates"),
		"--remote", "https://example.invalid/templates.git",
		"--branch", "main",
		"--o", kubePath,
	}); err != nil {
		t.Fatal(err)
	}
	kubeData, err := os.ReadFile(kubePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"image: example/hello:v2",
		"containerPort: 9090",
		"revisionHistoryLimit: 5",
		"serviceAccountName: hello-sa",
		"imagePullSecrets:",
		"imagePullPolicy: IfNotPresent",
		"cpu: 100m",
		"memory: 128Mi",
		"cpu: 500m",
		"memory: 512Mi",
		"type: NodePort",
		"nodePort: 30090",
		"kind: HorizontalPodAutoscaler",
		"minReplicas: 2",
		"maxReplicas: 5",
	} {
		if !strings.Contains(string(kubeData), want) {
			t.Fatalf("kube yaml missing %q:\n%s", want, kubeData)
		}
	}
	if !strings.Contains(string(kubeData), "image: example/hello:v2") || !strings.Contains(string(kubeData), "containerPort: 9090") {
		t.Fatalf("kube yaml = %s", kubeData)
	}
}

func TestExecuteDockerKubeAndTemplatesAcceptMixedPositionalsAndFlags(t *testing.T) {
	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "Dockerfile.mixed")
	if err := Execute([]string{
		"docker",
		"--go", "./cmd/server",
		"hello",
		"--exe", "server",
		"--o", dockerPath,
	}); err != nil {
		t.Fatal(err)
	}
	dockerData, err := os.ReadFile(dockerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerData), "go build -o /out/server ./cmd/server") {
		t.Fatalf("Dockerfile = %s", dockerData)
	}

	kubePath := filepath.Join(dir, "kube-mixed.yaml")
	if err := Execute([]string{
		"kube",
		"--image", "example/hello:v3",
		"hello",
		"--targetPort", "9091",
		"--o", kubePath,
	}); err != nil {
		t.Fatal(err)
	}
	kubeData, err := os.ReadFile(kubePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kubeData), "name: hello") || !strings.Contains(string(kubeData), "containerPort: 9091") {
		t.Fatalf("kube yaml = %s", kubeData)
	}

	templateDir := filepath.Join(dir, "mixed-templates")
	if err := Execute([]string{"template", "init", "placeholder", "--dir", templateDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(templateDir, "api.tpl")); err != nil {
		t.Fatalf("expected mixed template init output: %v", err)
	}

	apiTemplatePath := filepath.Join(dir, "billing.api")
	if err := Execute([]string{"api", "--o", apiTemplatePath, "billing"}); err != nil {
		t.Fatal(err)
	}
	apiData, err := os.ReadFile(apiTemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apiData), "service billing") {
		t.Fatalf("api template = %s", apiData)
	}

	rpcTemplatePath := filepath.Join(dir, "orders.proto")
	if err := Execute([]string{"rpc", "--o", rpcTemplatePath, "orders"}); err != nil {
		t.Fatal(err)
	}
	rpcData, err := os.ReadFile(rpcTemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rpcData), "package orders.v1") {
		t.Fatalf("rpc template = %s", rpcData)
	}
}

func TestExecuteKubeResourceKinds(t *testing.T) {
	dir := t.TempDir()
	servicePath := filepath.Join(dir, "svc.yaml")
	if err := Execute([]string{"kube", "service", "hello", "--port", "9090", "--o", servicePath}); err != nil {
		t.Fatal(err)
	}
	serviceData, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serviceData), "kind: Service") || !strings.Contains(string(serviceData), "port: 9090") {
		t.Fatalf("service yaml = %s", serviceData)
	}

	ingressPath := filepath.Join(dir, "ingress.yaml")
	if err := Execute([]string{"kube", "ingress", "hello", "--host", "hello.example.com", "--path", "/api", "--o", ingressPath}); err != nil {
		t.Fatal(err)
	}
	ingressData, err := os.ReadFile(ingressPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ingressData), "kind: Ingress") || !strings.Contains(string(ingressData), "path: /api") {
		t.Fatalf("ingress yaml = %s", ingressData)
	}

	configPath := filepath.Join(dir, "configmap.yaml")
	if err := Execute([]string{"kube", "configmap", "hello", "--data", "MODE=prod,VERSION=v1", "--o", configPath}); err != nil {
		t.Fatal(err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), "kind: ConfigMap") || !strings.Contains(string(configData), `MODE: "prod"`) {
		t.Fatalf("configmap yaml = %s", configData)
	}
}

func TestExecuteQuickstart(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"quickstart", "hello", "--module", "example.com/hello", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"hello.api",
		"Dockerfile",
		filepath.Join("internal", "api", "v1", "types.go"),
		filepath.Join("internal", "api", "v1", "hello", "routes.go"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected quickstart file %s: %v", rel, err)
		}
	}
	microDir := filepath.Join(t.TempDir(), "micro")
	if err := Execute([]string{"quickstart", "greeter", "--module", "example.com/greeter", "--dir", microDir, "--service-type", "micro"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(microDir, "etc", "governance.json")); err != nil {
		t.Fatalf("expected micro quickstart production file: %v", err)
	}
}

func TestExecuteQuickstartMigrateAndGatewayAcceptMixedPositionalsAndFlags(t *testing.T) {
	sandbox := t.TempDir()
	t.Chdir(sandbox)

	quickstartDir := filepath.Join(sandbox, "quickstart")
	if err := Execute([]string{
		"quickstart",
		"--module", "example.com/checkout",
		"checkout",
		"--dir", quickstartDir,
		"--t", "micro",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(quickstartDir, "etc", "governance.json")); err != nil {
		t.Fatalf("expected mixed quickstart micro output: %v", err)
	}

	migrationDir := filepath.Join(sandbox, "migrations")
	if err := Execute([]string{"migrate", "create", "--name", "add-users", "ignored", "--dir", migrationDir}); err != nil {
		t.Fatal(err)
	}
	up, err := filepath.Glob(filepath.Join(migrationDir, "*_add_users.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(up) != 1 {
		t.Fatalf("expected mixed migration output, got %v", up)
	}

	gatewayDir := filepath.Join(sandbox, "edge-gateway")
	if err := Execute([]string{
		"gen", "gateway",
		"edge",
		"--module", "example.com/edge",
		"--dir", gatewayDir,
	}); err != nil {
		t.Fatal(err)
	}
	mainData, err := os.ReadFile(filepath.Join(gatewayDir, "cmd", "edge", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainData), `"example.com/edge/internal/routes"`) {
		t.Fatalf("gateway main = %s", mainData)
	}
}

func TestExecuteMigrateCreate(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"migrate", "create", "add-users", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	up, err := filepath.Glob(filepath.Join(dir, "*_add_users.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := filepath.Glob(filepath.Join(dir, "*_add_users.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(up) != 1 || len(down) != 1 {
		t.Fatalf("migration files up=%v down=%v", up, down)
	}
}

func TestTemplateListAndCleanFilters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := Execute([]string{"template", "init", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := Execute([]string{"template", "list", "--dir", dir, "--category", "kube"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "kube-deployment.tpl") || strings.Contains(out, "api.tpl") {
		t.Fatalf("template list filter output = %q", out)
	}
	if err := Execute([]string{"template", "clean", "--dir", dir, "--name", "docker.tpl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docker.tpl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("docker.tpl should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.tpl")); err != nil {
		t.Fatalf("api.tpl should remain after filtered clean: %v", err)
	}
}

func TestDockerUsesLocalTemplateSource(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "docker.tpl"), []byte("FROM scratch\n# {{.Name}} {{.Port}} {{.Timezone}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "Dockerfile")
	if err := Execute([]string{"docker", "hello", "--home", templates, "--port", "9090", "--tz", "Asia/Shanghai", "--o", out}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# hello 9090 Asia/Shanghai") {
		t.Fatalf("docker custom template not rendered with metadata:\n%s", data)
	}
}

func TestHandlerCompleteFromAPIIDL(t *testing.T) {
	dir := t.TempDir()
	apiPath := filepath.Join(dir, "user.api")
	api := `type PingResp {
  Message string
}
service user-api {
  @handler Ping
  get /ping returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}
	handlerPath := filepath.Join(dir, "handler.go")
	if err := Execute([]string{"handler", "complete", "--file", handlerPath, "--src", apiPath, "--receiver", "h", "--package", "handler"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(handlerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func (h *H) Ping()") || !strings.Contains(string(data), "TODO: implement GET /ping") {
		t.Fatalf("handler complete from api output:\n%s", data)
	}
}

func TestRPCDescriptorCommandReportsBreaking(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(base, []byte(`{"name":"greeter","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloResp"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"name":"greeter","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloRespV2"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		err := Execute([]string{"rpc", "descriptor", "--base", base, "--target", target})
		if !errors.Is(err, generator.ErrBreakingChanges) {
			t.Fatalf("Execute err = %v, want ErrBreakingChanges", err)
		}
	})
	if !strings.Contains(out, "Descriptor compatibility: 1 breaking") || !strings.Contains(out, "HelloRespV2") {
		t.Fatalf("rpc descriptor output = %q", out)
	}
}

func TestRPCDescriptorCommandJSONCompatible(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(base, []byte(`{"name":"greeter","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloResp"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"name":"greeter","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloResp"},{"name":"Health","request":"HealthReq","response":"HealthResp"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := Execute([]string{"rpc", "descriptor", base, target, "--format", "json"}); err != nil {
			t.Fatal(err)
		}
	})
	var report struct {
		Breaking int `json:"breaking"`
		Warnings int `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Breaking != 0 || report.Warnings != 0 {
		t.Fatalf("report = %#v, want compatible descriptor additions", report)
	}
}

func TestRPCDescriptorCommandRejectsInvalidDescriptor(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.json")
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(base, []byte(`{"name":"greeter","methods":[{"name":"Call"},{"name":"Call"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"name":"greeter","methods":[{"name":"Call"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Execute([]string{"rpc", "descriptor", base, target})
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("Execute err = %v, want duplicated descriptor error", err)
	}
}

func TestRPCDescriptorCommandReadsDescriptorURLs(t *testing.T) {
	basePayload := `{"name":"greeter","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloResp"}]}`
	targetPayload := `{"name":"greeter","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloResp"},{"name":"Health","request":"HealthReq","response":"HealthResp"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/base":
			_, _ = io.WriteString(w, basePayload)
		case "/target":
			_, _ = io.WriteString(w, targetPayload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	out := captureStdout(t, func() {
		if err := Execute([]string{"rpc", "descriptor", "--base", server.URL + "/base", "--target", server.URL + "/target", "--token", "secret"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "method greeter/Health: method was added") {
		t.Fatalf("rpc descriptor URL output = %q", out)
	}
}

func TestRPCDescriptorCommandReadsAdminBaseURL(t *testing.T) {
	basePayload := `{"name":"greeter","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloResp"}]}`
	targetPayload := `{"name":"greeter","methods":[{"name":"SayHello","request":"HelloReq","response":"HelloRespV2"}]}`
	targetPath := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(targetPath, []byte(targetPayload), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/rpc/admin/descriptors/greeter" {
			t.Fatalf("descriptor URL path = %q, want admin descriptor path", r.URL.Path)
		}
		_, _ = io.WriteString(w, basePayload)
	}))
	defer server.Close()

	err := Execute([]string{"rpc", "descriptor", "--url", server.URL + "/admin", "--service", "greeter", "--target", targetPath})
	if !errors.Is(err, generator.ErrBreakingChanges) {
		t.Fatalf("rpc descriptor admin URL err = %v, want ErrBreakingChanges", err)
	}
}

func TestRPCDescriptorCommandURLStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer server.Close()

	err := Execute([]string{"rpc", "descriptor", "--base", server.URL, "--target", server.URL})
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("Execute err = %v, want descriptor endpoint status error", err)
	}
}

func TestRPCDescriptorCommandRejectsMalformedURL(t *testing.T) {
	err := Execute([]string{"rpc", "descriptor", "--base", "http://", "--target", "http://"})
	if err == nil || !strings.Contains(err.Error(), "unsupported descriptor URL") {
		t.Fatalf("Execute err = %v, want unsupported descriptor URL", err)
	}
}

func TestConfigModelFields(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"config", "init", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"config", "set", "--dir", dir, "--key", "model.typesMap", "--value", "tinyint=bool,decimal=string"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := Execute([]string{"config", "get", "--dir", dir, "--key", "model.typesMap"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "decimal=string,tinyint=bool" {
		t.Fatalf("model typesMap = %q", out)
	}
}

func TestGetConfigFieldAllKeys(t *testing.T) {
	cfg := &generator.Config{
		ServiceName: "svc",
		Module:      "mod",
		Style:       "std",
		TemplateDir: "/tmpl",
		GoVersion:   "1.22",
		Features:    []string{"a", "b"},
		RPC:         &generator.RPCConfig{Plugins: []string{"p1"}, Transport: "grpc", Profile: string(generator.ProfileKitexCompatible)},
		API:         &generator.APIConfig{Plugins: []string{"p2"}, Profile: string(generator.ProfileGoZeroCompatible)},
		Model: &generator.ModelConfig{
			Style:         "gorm",
			IgnoreColumns: []string{"id"},
			TypesMap:      map[string]string{"tinyint": "bool"},
			Cache:         true,
			Strict:        true,
		},
		Extra: map[string]string{"custom": "val"},
	}

	tests := []struct {
		key  string
		want string
	}{
		{"service", "svc"},
		{"service-name", "svc"},
		{"serviceName", "svc"},
		{"module", "mod"},
		{"style", "std"},
		{"templateDir", "/tmpl"},
		{"template-dir", "/tmpl"},
		{"templates", "/tmpl"},
		{"goVersion", "1.22"},
		{"go-version", "1.22"},
		{"features", "a,b"},
		{"rpc.plugins", "p1"},
		{"rpc-plugins", "p1"},
		{"rpc.transport", "grpc"},
		{"rpc-transport", "grpc"},
		{"rpc.profile", string(generator.ProfileKitexCompatible)},
		{"rpc-profile", string(generator.ProfileKitexCompatible)},
		{"api.plugins", "p2"},
		{"api-plugins", "p2"},
		{"api.profile", string(generator.ProfileGoZeroCompatible)},
		{"api-profile", string(generator.ProfileGoZeroCompatible)},
		{"model.style", "gorm"},
		{"model-style", "gorm"},
		{"model.ignoreColumns", "id"},
		{"model-ignore-columns", "id"},
		{"model.typesMap", "tinyint=bool"},
		{"model-types-map", "tinyint=bool"},
		{"model.cache", "true"},
		{"model-cache", "true"},
		{"model.strict", "true"},
		{"model-strict", "true"},
		{"custom", "val"},
		{"missing", ""},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := getConfigField(cfg, tt.key); got != tt.want {
				t.Fatalf("getConfigField(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestGetConfigFieldNilSubstructs(t *testing.T) {
	cfg := &generator.Config{ServiceName: "svc"}
	if got := getConfigField(cfg, "rpc.plugins"); got != "" {
		t.Fatalf("nil rpc.plugins = %q, want empty", got)
	}
	if got := getConfigField(cfg, "rpc.profile"); got != "" {
		t.Fatalf("nil rpc.profile = %q, want empty", got)
	}
	if got := getConfigField(cfg, "api.plugins"); got != "" {
		t.Fatalf("nil api.plugins = %q, want empty", got)
	}
	if got := getConfigField(cfg, "api.profile"); got != "" {
		t.Fatalf("nil api.profile = %q, want empty", got)
	}
	if got := getConfigField(cfg, "model.style"); got != "" {
		t.Fatalf("nil model.style = %q, want empty", got)
	}
	if got := getConfigField(cfg, "model.cache"); got != "false" {
		t.Fatalf("nil model.cache = %q, want false", got)
	}
	if got := getConfigField(cfg, "model.strict"); got != "false" {
		t.Fatalf("nil model.strict = %q, want false", got)
	}
}

func TestSetConfigFieldAllKeys(t *testing.T) {
	cfg := &generator.Config{}
	if err := setConfigField(cfg, "service", "svc"); err != nil {
		t.Fatal(err)
	}
	if cfg.ServiceName != "svc" {
		t.Fatalf("ServiceName = %q", cfg.ServiceName)
	}

	if err := setConfigField(cfg, "module", "mod"); err != nil {
		t.Fatal(err)
	}
	if cfg.Module != "mod" {
		t.Fatalf("Module = %q", cfg.Module)
	}

	if err := setConfigField(cfg, "style", "std"); err != nil {
		t.Fatal(err)
	}
	if cfg.Style != "std" {
		t.Fatalf("Style = %q", cfg.Style)
	}

	if err := setConfigField(cfg, "templateDir", "/tmpl"); err != nil {
		t.Fatal(err)
	}
	if cfg.TemplateDir != "/tmpl" {
		t.Fatalf("TemplateDir = %q", cfg.TemplateDir)
	}

	if err := setConfigField(cfg, "goVersion", "1.22"); err != nil {
		t.Fatal(err)
	}
	if cfg.GoVersion != "1.22" {
		t.Fatalf("GoVersion = %q", cfg.GoVersion)
	}

	if err := setConfigField(cfg, "features", "ecosystem-compat,rpc-compat"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Features) != 2 || cfg.Features[0] != "ecosystem-compat" || cfg.Features[1] != "rpc-compat" {
		t.Fatalf("Features = %v", cfg.Features)
	}

	if err := setConfigField(cfg, "rpc.plugins", "p1,p2"); err != nil {
		t.Fatal(err)
	}
	if cfg.RPC == nil || len(cfg.RPC.Plugins) != 2 {
		t.Fatalf("RPC.Plugins = %v", cfg.RPC)
	}

	if err := setConfigField(cfg, "rpc.transport", "grpc"); err != nil {
		t.Fatal(err)
	}
	if cfg.RPC.Transport != "grpc" {
		t.Fatalf("RPC.Transport = %q", cfg.RPC.Transport)
	}

	if err := setConfigField(cfg, "rpc.profile", string(generator.ProfileKitexCompatible)); err != nil {
		t.Fatal(err)
	}
	if cfg.RPC.Profile != string(generator.ProfileKitexCompatible) {
		t.Fatalf("RPC.Profile = %q", cfg.RPC.Profile)
	}

	if err := setConfigField(cfg, "api.plugins", "p3"); err != nil {
		t.Fatal(err)
	}
	if cfg.API == nil || len(cfg.API.Plugins) != 1 {
		t.Fatalf("API.Plugins = %v", cfg.API)
	}

	if err := setConfigField(cfg, "api.profile", string(generator.ProfileGoZeroCompatible)); err != nil {
		t.Fatal(err)
	}
	if cfg.API.Profile != string(generator.ProfileGoZeroCompatible) {
		t.Fatalf("API.Profile = %q", cfg.API.Profile)
	}

	if err := setConfigField(cfg, "model.style", "gorm"); err != nil {
		t.Fatal(err)
	}
	if cfg.Model == nil || cfg.Model.Style != "gorm" {
		t.Fatalf("Model.Style = %v", cfg.Model)
	}

	if err := setConfigField(cfg, "model.ignoreColumns", "id,created_at"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Model.IgnoreColumns) != 2 {
		t.Fatalf("Model.IgnoreColumns = %v", cfg.Model.IgnoreColumns)
	}

	if err := setConfigField(cfg, "model.typesMap", "tinyint=bool"); err != nil {
		t.Fatal(err)
	}
	if cfg.Model.TypesMap["tinyint"] != "bool" {
		t.Fatalf("Model.TypesMap = %v", cfg.Model.TypesMap)
	}

	if err := setConfigField(cfg, "model.cache", "true"); err != nil {
		t.Fatal(err)
	}
	if !cfg.Model.Cache {
		t.Fatal("Model.Cache should be true")
	}

	if err := setConfigField(cfg, "model.strict", "true"); err != nil {
		t.Fatal(err)
	}
	if !cfg.Model.Strict {
		t.Fatal("Model.Strict should be true")
	}

	if err := setConfigField(cfg, "extraKey", "extraVal"); err != nil {
		t.Fatal(err)
	}
	if cfg.Extra["extraKey"] != "extraVal" {
		t.Fatalf("Extra = %v", cfg.Extra)
	}
}

func TestSetConfigFieldInvalidFeature(t *testing.T) {
	cfg := &generator.Config{}
	if err := setConfigField(cfg, "features", "invalid-feature-xyz"); err == nil {
		t.Fatal("expected error for invalid feature")
	}
}

func TestEnsureModelConfig(t *testing.T) {
	cfg := &generator.Config{}
	m := ensureModelConfig(cfg)
	if m == nil {
		t.Fatal("ensureModelConfig returned nil")
	}
	if cfg.Model == nil {
		t.Fatal("cfg.Model was not set")
	}
	if m.TypesMap == nil {
		t.Fatal("TypesMap was not initialized")
	}
	// second call should return same struct
	m2 := ensureModelConfig(cfg)
	if m2 != m {
		t.Fatal("second ensureModelConfig should return same pointer")
	}
}

func TestParseBoolString(t *testing.T) {
	trues := []string{"1", "t", "true", "y", "yes", "on", " TRUE ", "True"}
	for _, v := range trues {
		if !parseBoolString(v) {
			t.Fatalf("parseBoolString(%q) should be true", v)
		}
	}
	falses := []string{"", "0", "false", "no", "off", "maybe"}
	for _, v := range falses {
		if parseBoolString(v) {
			t.Fatalf("parseBoolString(%q) should be false", v)
		}
	}
}

func TestEncodeStringMap(t *testing.T) {
	if got := encodeStringMap(nil); got != "" {
		t.Fatalf("nil map = %q, want empty", got)
	}
	if got := encodeStringMap(map[string]string{}); got != "" {
		t.Fatalf("empty map = %q, want empty", got)
	}
	if got := encodeStringMap(map[string]string{"b": "2", "a": "1"}); got != "a=1,b=2" {
		t.Fatalf("map = %q, want a=1,b=2", got)
	}
}

func TestModelGenUsesConfigTypesMap(t *testing.T) {
	dir := t.TempDir()
	if err := Execute([]string{"config", "init", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"config", "set", "--dir", dir, "--key", "model.typesMap", "--value", "decimal=string,tinyint=bool"}); err != nil {
		t.Fatal(err)
	}
	ddlPath := filepath.Join(dir, "schema.sql")
	ddl := `CREATE TABLE orders (
  id bigint primary key,
  price decimal(10,2) not null,
  enabled tinyint(1) default null
);`
	if err := os.WriteFile(ddlPath, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"model", "gen", "--ddl", ddlPath, "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "model", "entity", "order_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Price   string",
		"Enabled *bool",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("generated model should use config typesMap %q:\n%s", want, data)
		}
	}
}
