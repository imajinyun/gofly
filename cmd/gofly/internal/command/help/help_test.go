package help

import (
	"strings"
	"testing"
)

type recordingPrinter struct {
	lines []string
}

func (p *recordingPrinter) Println(args ...any) {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if s, ok := arg.(string); ok {
			parts = append(parts, s)
		}
	}
	p.lines = append(p.lines, strings.Join(parts, " "))
}

func TestCommandHelpTopic(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		args      []string
		wantTopic string
		wantOK    bool
	}{
		{
			name:      "help subcommand trims flags",
			command:   "gofly",
			args:      []string{"help", "model", "mysql", "ddl", "--dir", "."},
			wantTopic: "gofly model mysql ddl",
			wantOK:    true,
		},
		{
			name:      "inline long help uses leading positionals",
			command:   "gofly",
			args:      []string{"api", "format", "--help", "--write"},
			wantTopic: "gofly api format",
			wantOK:    true,
		},
		{
			name:      "inline short help stops before flag",
			command:   "gofly",
			args:      []string{"rpc", "client", "-h", "--file", "svc.proto"},
			wantTopic: "gofly rpc client",
			wantOK:    true,
		},
		{
			name:    "no help flag",
			command: "gofly",
			args:    []string{"api", "format"},
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTopic, gotOK := CommandHelpTopic(tt.command, tt.args)
			if gotOK != tt.wantOK {
				t.Fatalf("CommandHelpTopic ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotTopic != tt.wantTopic {
				t.Fatalf("CommandHelpTopic topic = %q, want %q", gotTopic, tt.wantTopic)
			}
		})
	}
}

func TestModelHelpSubcommandContract(t *testing.T) {
	for _, command := range []string{"gen", "mongo"} {
		if !IsModelHelpSubcommand(command) {
			t.Fatalf("model help command %q was not recognized", command)
		}
	}
	for _, command := range []string{"", "ddl", "unknown"} {
		if IsModelHelpSubcommand(command) {
			t.Fatalf("model help command %q was unexpectedly recognized", command)
		}
	}
}

func TestCanonicalTopic(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "top level alias", in: "generate rpc service.proto", want: "gen rpc"},
		{name: "nested api alias", in: "api typescript --file service.api", want: "api ts"},
		{name: "model postgres alias keeps driver command", in: "model postgres datasource orders --url mysql", want: "model pg datasource"},
		{name: "completion powershell alias", in: "complete handler pwsh extra", want: "complete handler powershell"},
		{name: "new service trims positional name", in: "new service orders --module example.com/orders", want: "new service"},
		{name: "examples run trims positional name", in: "examples run observability", want: "examples run"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanonicalTopic(tt.in); got != tt.want {
				t.Fatalf("CanonicalTopic(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCommandUsageRendering(t *testing.T) {
	t.Setenv("GOFLY_NO_COLOR", "1")

	usage := CommandUsage("api format")
	for _, want := range []string{"Format one .api file", "Usage:", "api format", "Flags:", "--write", "Examples:"} {
		if !strings.Contains(usage, want) {
			t.Fatalf("CommandUsage(api format) missing %q in:\n%s", want, usage)
		}
	}
	if strings.Contains(usage, "\x1b[") {
		t.Fatalf("CommandUsage should not include ANSI escapes when GOFLY_NO_COLOR is set: %q", usage)
	}
}

func TestPrintCommandHelp(t *testing.T) {
	t.Setenv("GOFLY_NO_COLOR", "1")

	printer := &recordingPrinter{}
	if ok := PrintCommandHelp(printer, "gofly", []string{"help", "doctor"}); !ok {
		t.Fatal("PrintCommandHelp returned false for help doctor")
	}
	if len(printer.lines) != 1 {
		t.Fatalf("PrintCommandHelp printed %d lines, want 1", len(printer.lines))
	}
	if !strings.Contains(printer.lines[0], "gofly doctor") {
		t.Fatalf("PrintCommandHelp output missing doctor usage:\n%s", printer.lines[0])
	}

	before := len(printer.lines)
	if ok := PrintCommandHelp(printer, "gofly", []string{"doctor"}); ok {
		t.Fatal("PrintCommandHelp returned true without help args")
	}
	if len(printer.lines) != before {
		t.Fatalf("PrintCommandHelp printed without help args: got %d lines, want %d", len(printer.lines), before)
	}
}

func TestHelpMetadataReturnsDefensiveCopies(t *testing.T) {
	top := TopLevelAliases()
	top["generate"] = "broken"
	if got := TopLevelAliases()["generate"]; got != "gen" {
		t.Fatalf("TopLevelAliases leaked mutable map, generate = %q", got)
	}

	nested := NestedAliases()
	nested["api"]["fmt"] = "broken"
	if got := NestedAliases()["api"]["fmt"]; got != "format" {
		t.Fatalf("NestedAliases leaked mutable map, api fmt = %q", got)
	}
}

func TestCompletionShellsReturnsDefensiveCopy(t *testing.T) {
	shells := CompletionShells()
	if len(shells) == 0 {
		t.Fatal("CompletionShells returned no shells")
	}
	shells[0] = "broken"
	if got := CompletionShells()[0]; got == "broken" {
		t.Fatal("CompletionShells leaked mutable slice")
	}
	if !IsCompletionShell(" pwsh ") {
		t.Fatal("IsCompletionShell should trim and accept pwsh")
	}
	if IsCompletionShell("nu") {
		t.Fatal("IsCompletionShell accepted unsupported shell")
	}
}

func TestTopicPredicates(t *testing.T) {
	trueCases := []struct {
		name string
		ok   bool
	}{
		{name: "gen gateway", ok: IsGenHelpSubcommand("gateway")},
		{name: "api kotlin", ok: IsAPIHelpSubcommand("kotlin")},
		{name: "rpc descriptor", ok: IsRPCHelpSubcommand("descriptor")},
		{name: "model mysql ddl", ok: IsModelDriverHelpSubcommand("mysql", "ddl")},
		{name: "config clean", ok: IsConfigHelpSubcommand("clean")},
		{name: "feature run", ok: IsFeatureHelpSubcommand("run")},
		{name: "plugin search", ok: IsPluginHelpSubcommand("search")},
		{name: "kube ingress", ok: IsKubeHelpSubcommand("ingress")},
		{name: "template update", ok: IsTemplateHelpSubcommand("update")},
		{name: "env check", ok: IsEnvHelpSubcommand("check")},
		{name: "completion zsh", ok: IsCompletionHelpSubcommand("zsh")},
		{name: "ai control plane", ok: IsAIHelpSubcommand("control-plane")},
		{name: "complete handler fish", ok: IsCompleteHandlerShell("fish")},
	}
	for _, tc := range trueCases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Fatal("predicate returned false")
			}
		})
	}

	falseCases := []struct {
		name string
		ok   bool
	}{
		{name: "gen unknown", ok: IsGenHelpSubcommand("unknown")},
		{name: "api unknown", ok: IsAPIHelpSubcommand("unknown")},
		{name: "rpc unknown", ok: IsRPCHelpSubcommand("unknown")},
		{name: "model sqlite ddl", ok: IsModelDriverHelpSubcommand("sqlite", "ddl")},
		{name: "plugin unknown", ok: IsPluginHelpSubcommand("unknown")},
		{name: "completion unknown", ok: IsCompletionHelpSubcommand("unknown")},
	}
	for _, tc := range falseCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ok {
				t.Fatal("predicate returned true")
			}
		})
	}
}

func TestRenderHelpers(t *testing.T) {
	t.Setenv("GOFLY_NO_COLOR", "1")

	if got := RightPad("api", 5); got != "api  " {
		t.Fatalf("RightPad short = %q, want %q", got, "api  ")
	}
	if got := RightPad("gateway", 3); got != "gateway" {
		t.Fatalf("RightPad long = %q, want gateway", got)
	}
	if got := ANSIColor("94", "Usage:"); got != "Usage:" {
		t.Fatalf("ANSIColor with GOFLY_NO_COLOR = %q, want Usage:", got)
	}
	if got := ANSIColor("94", ""); got != "" {
		t.Fatalf("ANSIColor empty = %q, want empty", got)
	}
}

func TestCommandCatalogCoversPrimaryTopics(t *testing.T) {
	t.Setenv("GOFLY_NO_COLOR", "1")

	tests := []struct {
		name  string
		topic string
		want  []string
	}{
		{name: "api root", topic: "api", want: []string{"Generate, validate", "go", "swagger"}},
		{name: "api go", topic: "api go", want: []string{"Generate REST service", "--rpc-package"}},
		{name: "api check", topic: "api check", want: []string{"Validate an .api file", "--file"}},
		{name: "api swagger", topic: "api swagger", want: []string{"Generate API documentation", "--oas3"}},
		{name: "api route", topic: "api route", want: []string{"route table", "--format text|markdown|json"}},
		{name: "api import", topic: "api import", want: []string{"Convert OpenAPI", "--service"}},
		{name: "api diff", topic: "api diff", want: []string{"Compare two .api", "--target"}},
		{name: "api breaking", topic: "api breaking", want: []string{"Detect breaking changes", "--base"}},
		{name: "api types", topic: "api types", want: []string{"Generate Go DTO", "--package"}},
		{name: "api new", topic: "api new", want: []string{"Create an API service", "--profile"}},
		{name: "api client", topic: "api client", want: []string{"typed API client", "--language"}},
		{name: "api plugin", topic: "api plugin", want: []string{"Run an API generation plugin", "--plugin"}},
		{name: "api middleware", topic: "api middleware", want: []string{"Generate middleware", "--dir"}},
		{name: "gen api", topic: "gen api", want: []string{"Generate REST service", "--style go_zero"}},
		{name: "gen rest", topic: "gen rest", want: []string{"Generate REST service", "--style go_zero"}},
		{name: "gen middleware", topic: "gen middleware", want: []string{"Generate middleware", "gen middleware"}},

		{name: "rpc root", topic: "rpc", want: []string{"Generate, check", "descriptor"}},
		{name: "rpc gen", topic: "rpc gen", want: []string{"protobuf file", "--transport grpc|gofly|both"}},
		{name: "rpc idl", topic: "rpc idl", want: []string{"Inspect proto or thrift", "--format text|json"}},
		{name: "rpc thrift", topic: "rpc thrift", want: []string{"Convert a thrift", "--out"}},
		{name: "rpc client", topic: "rpc client", want: []string{"client wrapper", "--package"}},
		{name: "rpc server", topic: "rpc server", want: []string{"server implementation", "--out"}},
		{name: "rpc middleware", topic: "rpc middleware", want: []string{"gRPC unary middleware", "--name"}},
		{name: "rpc lint", topic: "rpc lint", want: []string{"Lint proto", "--file"}},
		{name: "rpc deps", topic: "rpc deps", want: []string{"List proto imports", "--format text|json"}},
		{name: "gen rpc", topic: "gen rpc", want: []string{"Generate gofly/gRPC", "--profile"}},
		{name: "rpc protoc", topic: "rpc protoc", want: []string{"standard protoc", "--go-grpc_out"}},
		{name: "rpc check", topic: "rpc check", want: []string{"Validate protobuf", "--src"}},
		{name: "rpc breaking", topic: "rpc breaking", want: []string{"Detect breaking", "--target"}},
		{name: "rpc descriptor", topic: "rpc descriptor", want: []string{"runtime RPC descriptors", "--service"}},
		{name: "rpc plugin", topic: "rpc plugin", want: []string{"RPC plugin", "--plugin"}},
		{name: "rpc template", topic: "rpc template", want: []string{"starter proto", "--remote"}},
		{name: "rpc new", topic: "rpc new", want: []string{"Create an RPC", "--profile"}},

		{name: "model root", topic: "model", want: []string{"SQL or Mongo", "mysql datasource"}},
		{name: "model mysql", topic: "model mysql", want: []string{"Generate SQL model", "datasource"}},
		{name: "model pg", topic: "model pg", want: []string{"PostgreSQL", "--style"}},
		{name: "model gen", topic: "model gen", want: []string{"Generate SQL model code from DDL", "--cache"}},
		{name: "gen model", topic: "gen model", want: []string{"Generate SQL model code from DDL", "--ddl"}},
		{name: "model mysql ddl", topic: "model mysql ddl", want: []string{"Generate SQL model code from DDL", "--style"}},
		{name: "model pg datasource", topic: "model pg datasource", want: []string{"introspecting a database", "--schema"}},
		{name: "model mongo", topic: "model mongo", want: []string{"Mongo repository", "--type"}},

		{name: "plugin root", topic: "plugin", want: []string{"generation plugins", "install"}},
		{name: "plugin list", topic: "plugin list", want: []string{"List built-in", "--json"}},
		{name: "plugin search", topic: "plugin search", want: []string{"Search a plugin registry", "--registry"}},
		{name: "plugin install", topic: "plugin install", want: []string{"version-pinned remote", "--remote"}},
		{name: "plugin uninstall", topic: "plugin uninstall", want: []string{"Remove a version-pinned", "--json"}},
		{name: "plugin run", topic: "plugin run", want: []string{"Run a built-in", "--go-plugin"}},

		{name: "ai root", topic: "ai", want: []string{"machine-readable", "control-plane"}},
		{name: "ai manifest", topic: "ai manifest", want: []string{"AI tool manifest", "--schema"}},
		{name: "ai plan", topic: "ai plan", want: []string{"without writing files", "--kind"}},
		{name: "ai new", topic: "ai new", want: []string{"AI-first project scaffold", "--apply"}},
		{name: "ai complete", topic: "ai complete", want: []string{"governed no-op", "--max-total-tokens"}},
		{name: "ai stream", topic: "ai stream", want: []string{"streaming completion", "--allow-failover"}},
		{name: "ai doctor", topic: "ai doctor", want: []string{"AI subsystem", "--json"}},
		{name: "ai control-plane", topic: "ai control-plane", want: []string{"deterministic AI control-plane", "--watch"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CommandUsage(tt.topic)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("CommandUsage(%q) missing %q in:\n%s", tt.topic, want, got)
				}
			}
		})
	}
}

func TestGeneralCatalogTopics(t *testing.T) {
	t.Setenv("GOFLY_NO_COLOR", "1")

	topics := []string{
		"new", "new service", "gen", "handler", "gen handler", "handler complete",
		"gen gateway", "version", "docker", "kube", "kube deploy", "template",
		"template init", "quickstart", "migrate", "bug", "upgrade", "env",
		"env check", "config", "config init", "config show", "config get",
		"config set", "config clean", "feature", "feature list", "feature run",
		"complete", "completion", "completion bash", "release", "release check",
		"complete handler bash", "doctor", "example", "example list", "example run",
		"unknown topic",
	}

	for _, topic := range topics {
		t.Run(topic, func(t *testing.T) {
			got := CommandUsage(topic)
			if !strings.Contains(got, "Usage:") {
				t.Fatalf("CommandUsage(%q) missing Usage section:\n%s", topic, got)
			}
		})
	}
}

func TestUsageAndColorizedText(t *testing.T) {
	t.Setenv("GOFLY_NO_COLOR", "1")

	usage := Usage()
	for _, want := range []string{"gofly is the flycli-style", "Usage:", "new service", "rpc", "api", "model"} {
		if !strings.Contains(usage, want) {
			t.Fatalf("Usage missing %q", want)
		}
	}
	if got := LeadingTopicArgs([]string{"api", "format", "--write", "ignored"}); strings.Join(got, " ") != "api format" {
		t.Fatalf("LeadingTopicArgs = %v, want [api format]", got)
	}
	if got := JoinTopic("gofly", nil); got != "gofly" {
		t.Fatalf("JoinTopic without parts = %q, want gofly", got)
	}
	if got := JoinTopic("gofly", []string{"api", "format"}); got != "gofly api format" {
		t.Fatalf("JoinTopic with parts = %q, want gofly api format", got)
	}

	printer := &recordingPrinter{}
	Print(printer, "")
	if len(printer.lines) != 1 || !strings.Contains(printer.lines[0], "gofly is the flycli-style") {
		t.Fatalf("Print root usage lines=%v", printer.lines)
	}
}
