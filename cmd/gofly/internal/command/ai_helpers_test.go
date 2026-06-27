package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/llm"
)

func TestIsAIHelpSubcommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "manifest", command: "manifest", want: true},
		{name: "plan", command: "plan", want: true},
		{name: "new", command: "new", want: true},
		{name: "complete", command: "complete", want: true},
		{name: "stream", command: "stream", want: true},
		{name: "doctor", command: "doctor", want: true},
		{name: "control-plane", command: "control-plane", want: true},
		{name: "ask is not supported", command: "ask", want: false},
		{name: "empty", command: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAIHelpSubcommand(tt.command); got != tt.want {
				t.Fatalf("isAIHelpSubcommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func manifestLinksContainPath(links []aiManifestLink, want string) bool {
	for _, link := range links {
		if link.Path == want {
			return true
		}
	}
	return false
}

func TestIsRetryableLLMError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "rate limited", err: llm.ErrRateLimited, want: true},
		{name: "wrapped provider request failed", err: errors.Join(errors.New("call failed"), llm.ErrProviderRequestFailed), want: true},
		{name: "http throttled", err: &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusTooManyRequests}, want: true},
		{name: "http server error", err: &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusBadGateway}, want: true},
		{name: "http unauthorized", err: &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusUnauthorized}, want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableLLMError(tt.err); got != tt.want {
				t.Fatalf("isRetryableLLMError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestCommandHelpSubcommandBoundaries(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) bool
		yes  string
		no   string
	}{
		{name: "gen", fn: isGenHelpSubcommand, yes: "handler", no: "unknown"},
		{name: "api", fn: isAPIHelpSubcommand, yes: "swagger", no: "unknown"},
		{name: "rpc", fn: isRPCHelpSubcommand, yes: "descriptor", no: "unknown"},
		{name: "model", fn: isModelHelpSubcommand, yes: "mongo", no: "ddl"},
		{name: "config", fn: isConfigHelpSubcommand, yes: "set", no: "delete"},
		{name: "feature", fn: isFeatureHelpSubcommand, yes: "run", no: "enable"},
		{name: "plugin", fn: isPluginHelpSubcommand, yes: "install", no: "enable"},
		{name: "kube", fn: isKubeHelpSubcommand, yes: "deploy", no: "delete"},
		{name: "template", fn: isTemplateHelpSubcommand, yes: "revert", no: "diff"},
		{name: "env", fn: isEnvHelpSubcommand, yes: "install", no: "doctor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.fn(tt.yes) {
				t.Fatalf("%s helper rejected %q", tt.name, tt.yes)
			}
			if tt.fn(tt.no) {
				t.Fatalf("%s helper accepted %q", tt.name, tt.no)
			}
		})
	}
	if !isModelDriverHelpSubcommand("mysql", "ddl") || !isModelDriverHelpSubcommand("pg", "datasource") || isModelDriverHelpSubcommand("sqlite", "ddl") {
		t.Fatal("model driver help subcommand boundaries mismatch")
	}
}

func TestCLICommandSurfaceManifestMatchesRegistries(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..", "..")
	data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "reference", "cli-command-surface.json"))
	if err != nil {
		t.Fatalf("read cli command surface manifest: %v", err)
	}
	type manifestRootCommand struct {
		Name         string   `json:"name"`
		Aliases      []string `json:"aliases"`
		Children     []string `json:"children"`
		JSONContract string   `json:"jsonContract"`
		HelpTopic    string   `json:"helpTopic"`
	}
	type manifestClosedGovernance struct {
		ID       string   `json:"id"`
		Task     string   `json:"task"`
		Subtasks []string `json:"subtasks"`
		Evidence []string `json:"evidence"`
		Gates    []string `json:"gates"`
	}
	var manifest struct {
		Schema           string                     `json:"schema"`
		AcceptanceGate   string                     `json:"acceptanceGate"`
		IgnoredPaths     []string                   `json:"ignoredPaths"`
		RootCommands     []manifestRootCommand      `json:"rootCommands"`
		ClosedGovernance []manifestClosedGovernance `json:"closedGovernance"`
		RecommendedOrder []string                   `json:"recommendedOrder"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode cli command surface manifest: %v", err)
	}
	if manifest.Schema != "gofly.cli_command_surface.v1" {
		t.Fatalf("schema = %q, want gofly.cli_command_surface.v1", manifest.Schema)
	}
	if manifest.AcceptanceGate != "make cli-command-surface-check" {
		t.Fatalf("acceptanceGate = %q, want make cli-command-surface-check", manifest.AcceptanceGate)
	}
	if !containsString(manifest.IgnoredPaths, "docs/superpowers/") {
		t.Fatalf("ignoredPaths = %v, want docs/superpowers/", manifest.IgnoredPaths)
	}

	byName := make(map[string]manifestRootCommand)
	for _, item := range manifest.RootCommands {
		byName[item.Name] = item
	}
	for _, spec := range rootCommands.primary {
		if _, ok := byName[spec.Name]; !ok {
			t.Fatalf("root command %q missing from cli command surface manifest", spec.Name)
		}
	}
	for _, item := range manifest.RootCommands {
		spec, ok := rootCommands.commands[item.Name]
		if !ok {
			t.Fatalf("manifest root command %q is not registered", item.Name)
		}
		for _, alias := range item.Aliases {
			aliasSpec, ok := rootCommands.commands[alias]
			if !ok || aliasSpec.Name != spec.Name {
				t.Fatalf("manifest alias %q for %q does not resolve to canonical command", alias, item.Name)
			}
		}
		if help := commandHelpFor(item.HelpTopic); help.Name == "" || help.Short == "" || help.Usage == "" || help.Short == "gofly command help." {
			t.Fatalf("manifest help topic %q for %q returned fallback or incomplete help: %#v", item.HelpTopic, item.Name, help)
		}
		switch item.Name {
		case "api":
			assertRegistryChildren(t, item.Name, apiCommands, item.Children)
		case "rpc":
			assertRegistryChildren(t, item.Name, rpcCommands, item.Children)
		case "model":
			assertRegistryChildren(t, item.Name, modelCommands, item.Children)
		case "plugin":
			for _, child := range item.Children {
				if !isPluginHelpSubcommand(child) {
					t.Fatalf("plugin child %q is registered in manifest but rejected by help boundary", child)
				}
			}
		}
		if item.JSONContract != "" {
			contractDoc, err := os.ReadFile(filepath.Join(repoRoot, "docs", "reference", "cli-json-contracts.md"))
			if err != nil {
				t.Fatalf("read cli json contracts: %v", err)
			}
			for _, needle := range strings.Split(item.JSONContract, ",") {
				needle = strings.TrimSpace(needle)
				if needle == "" || strings.Contains(needle, "...") {
					continue
				}
				if !strings.Contains(string(contractDoc), needle) {
					t.Fatalf("JSON contract %q for %q missing from cli-json-contracts.md", needle, item.Name)
				}
			}
		}
	}
	for alias, canonical := range topLevelHelpAliases {
		topic := commandHelpFor(alias)
		if topic.Name == "" || topic.Short == "" || topic.Usage == "" || topic.Short == "gofly command help." {
			t.Fatalf("top-level help alias %q returned fallback or incomplete help: %#v", alias, topic)
		}
		if got := canonicalHelpTopic(alias); got != canonical {
			t.Fatalf("top-level help alias %q canonical topic = %q, want %q", alias, got, canonical)
		}
	}
	for parent, aliases := range nestedHelpAliases {
		for alias, canonical := range aliases {
			aliasTopic := parent + " " + alias
			topic := commandHelpFor(aliasTopic)
			if topic.Name == "" || topic.Short == "" || topic.Usage == "" || topic.Short == "gofly command help." {
				t.Fatalf("nested help alias %q returned fallback or incomplete help: %#v", aliasTopic, topic)
			}
			wantTopic := parent + " " + canonical
			if got := canonicalHelpTopic(aliasTopic); got != wantTopic {
				t.Fatalf("nested help alias %q canonical topic = %q, want %q", aliasTopic, got, wantTopic)
			}
		}
	}
	for _, task := range []string{
		"GOFLY-P9-0-CLI-GOVERNANCE-ROADMAP",
		"GOFLY-P9-1-CLI-COMMAND-SURFACE-GATE",
		"GOFLY-P9-2-CLI-JSON-CONTRACT-GOLDENS",
		"GOFLY-P9-3-CLI-STDIO-AND-ERROR-DISCIPLINE",
	} {
		if !containsString(manifest.RecommendedOrder, task) {
			t.Fatalf("recommendedOrder missing %s", task)
		}
	}
	var stdio manifestClosedGovernance
	for _, item := range manifest.ClosedGovernance {
		if item.ID == "stdio-error-discipline" {
			stdio = item
			break
		}
	}
	if stdio.ID == "" {
		t.Fatal("closedGovernance missing stdio-error-discipline")
	}
	if stdio.Task != "GOFLY-P9-3-CLI-STDIO-AND-ERROR-DISCIPLINE" {
		t.Fatalf("stdio closeout task = %q", stdio.Task)
	}
	for _, want := range []string{"GOFLY-P9-3A-CLI-STDIO-EXIT-CONTRACT", "GOFLY-P9-3B-CLI-FLAG-DIAGNOSTICS", "GOFLY-P9-3C-CLI-GOVERNANCE-MANIFEST-CLOSEOUT"} {
		if !containsString(stdio.Subtasks, want) {
			t.Fatalf("stdio closeout subtasks missing %s", want)
		}
	}
	for _, want := range []string{"TestRunMainSTDIOExitContract", "TestRunMainFlagDiagnosticsContract", "TestCLISTDIOExitContract", "TestExecuteFlagParsingErrorsAreSilentUsageErrors", "TestCLIJSONErrorEnvelopeGolden"} {
		if !containsString(stdio.Evidence, want) {
			t.Fatalf("stdio closeout evidence missing %s", want)
		}
	}
	for _, want := range []string{"make cli-command-surface-check", "make cli-json-contract-goldens-check"} {
		if !containsString(stdio.Gates, want) {
			t.Fatalf("stdio closeout gates missing %s", want)
		}
	}
}

func assertRegistryChildren(t *testing.T, name string, registry commandRegistry, children []string) {
	t.Helper()
	for _, child := range children {
		if _, ok := registry.commands[child]; !ok {
			t.Fatalf("%s child %q missing from registry", name, child)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestNewServicePlanAndFlagParsingBoundaries(t *testing.T) {
	invalidConfigs := []struct {
		name string
		cfg  *generator.Config
		want string
	}{
		{name: "nil", cfg: nil, want: "service config is required"},
		{name: "missing name", cfg: &generator.Config{Module: "example.com/orders"}, want: "name is required"},
		{name: "missing module", cfg: &generator.Config{ServiceName: "orders"}, want: "module is required"},
		{name: "bad style", cfg: &generator.Config{ServiceName: "orders", Module: "example.com/orders", Style: "unknown"}, want: "unknown service style"},
	}
	for _, tt := range invalidConfigs {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNewServicePlanInputs(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateNewServicePlanInputs() error = %v, want %q", err, tt.want)
			}
		})
	}
	valid := &generator.Config{ServiceName: "orders", Module: "example.com/orders", Style: generator.ServiceStyleProduction, Features: []string{"http"}, TemplateDir: "tpl", TemplateRemote: "https://example.test/tpl"}
	if err := validateNewServicePlanInputs(valid); err != nil {
		t.Fatalf("validateNewServicePlanInputs(valid) = %v", err)
	}
	plan := buildNewServicePlan("new api", "out", ".gofly/config.json", valid, []string{"audit", "trace"}, newServiceContractInputs{}, true, true)
	if plan.Command != "new api" || !plan.DryRun || !plan.MutatesFilesystem || len(plan.Actions) != 4 || len(plan.Warnings) != 2 || plan.Inputs["features"] != "http" || plan.Inputs["plugins"] != "audit,trace" {
		t.Fatalf("buildNewServicePlan = %#v, want full dry-run plan", plan)
	}
	contractPlan := buildNewServicePlan("new service", "out", ".gofly/config.json", valid, nil, newServiceContractInputs{APIFile: "orders.api", ProtoFile: "orders.proto"}, true, true)
	if contractPlan.Inputs["api"] != "orders.api" || contractPlan.Inputs["proto"] != "orders.proto" || len(contractPlan.Actions) != 7 {
		t.Fatalf("buildNewServicePlan contracts = %#v, want contract inputs and materialization actions", contractPlan)
	}
	if err := validateNewServiceContractInputs(newServiceContractInputs{APIFile: "a.api", OpenAPIFile: "openapi.yaml"}); err == nil || !strings.Contains(err.Error(), "--api and --openapi") {
		t.Fatalf("validateNewServiceContractInputs api/openapi = %v, want mutually exclusive error", err)
	}
	if _, err := newServiceContractOutputPath("out", "../orders", ".api"); err == nil || !strings.Contains(err.Error(), "cannot be used as a contract filename") {
		t.Fatalf("newServiceContractOutputPath traversal = %v, want contract filename rejection", err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	name := fs.String("name", "", "")
	dryRun := fs.Bool("dry-run", false, "")
	count := fs.Int("count", 0, "")
	positionals, err := parseInterspersedFlags(fs, []string{"orders", "--name", "api", "--dry-run", "tail", "--count=3", "--", "--literal"})
	if err != nil {
		t.Fatalf("parseInterspersedFlags: %v", err)
	}
	if *name != "api" || !*dryRun || *count != 3 || strings.Join(positionals, ",") != "orders,tail,--literal" {
		t.Fatalf("parsed flags name=%q dry=%t count=%d positionals=%v", *name, *dryRun, *count, positionals)
	}
	if got := flagName("--name=value"); got != "name" {
		t.Fatalf("flagName = %q, want name", got)
	}
	if name, rest := splitLeadingName([]string{"svc", "--flag"}); name != "svc" || len(rest) != 1 || rest[0] != "--flag" {
		t.Fatalf("splitLeadingName = %q %v, want svc and flag", name, rest)
	}
	if name, rest := splitLeadingName([]string{"--flag"}); name != "" || len(rest) != 1 {
		t.Fatalf("splitLeadingName flag = %q %v, want unchanged flag args", name, rest)
	}
}

func TestNewServiceContractCopySafety(t *testing.T) {
	t.Run("copies explicit contract inside output root", func(t *testing.T) {
		tmp := t.TempDir()
		root := filepath.Join(tmp, "orders")
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatalf("mkdir root: %v", err)
		}
		src := filepath.Join(tmp, "contract.api")
		if err := os.WriteFile(src, []byte("service orders {}\n"), 0o600); err != nil {
			t.Fatalf("write source contract: %v", err)
		}
		dst := filepath.Join(root, "orders.api")
		if err := copyNewServiceContractFile(src, dst, root); err != nil {
			t.Fatalf("copyNewServiceContractFile: %v", err)
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read copied contract: %v", err)
		}
		if string(data) != "service orders {}\n" {
			t.Fatalf("copied contract = %q, want source content", data)
		}
	})

	t.Run("rejects symlink output root", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "contract.proto")
		if err := os.WriteFile(src, []byte("syntax = \"proto3\";\n"), 0o600); err != nil {
			t.Fatalf("write source contract: %v", err)
		}
		outside := filepath.Join(tmp, "outside")
		if err := os.MkdirAll(outside, 0o750); err != nil {
			t.Fatalf("mkdir outside: %v", err)
		}
		rootLink := filepath.Join(tmp, "orders")
		if err := os.Symlink(outside, rootLink); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		err := copyNewServiceContractFile(src, filepath.Join(rootLink, "orders.proto"), rootLink)
		if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("copy through symlink root error = %v, want symlink root rejection", err)
		}
		if _, err := os.Stat(filepath.Join(outside, "orders.proto")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("copy through symlink root wrote outside target or stat failed: %v", err)
		}
	})

	t.Run("rejects symlink parent below output root", func(t *testing.T) {
		tmp := t.TempDir()
		root := filepath.Join(tmp, "orders")
		outside := filepath.Join(tmp, "outside")
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatalf("mkdir root: %v", err)
		}
		if err := os.MkdirAll(outside, 0o750); err != nil {
			t.Fatalf("mkdir outside: %v", err)
		}
		src := filepath.Join(tmp, "contract.api")
		if err := os.WriteFile(src, []byte("service orders {}\n"), 0o600); err != nil {
			t.Fatalf("write source contract: %v", err)
		}
		link := filepath.Join(root, "contracts")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		err := copyNewServiceContractFile(src, filepath.Join(link, "orders.api"), root)
		if err == nil || !strings.Contains(err.Error(), "traverses symlink") {
			t.Fatalf("copy through symlink parent error = %v, want symlink traversal rejection", err)
		}
		if _, err := os.Stat(filepath.Join(outside, "orders.api")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("copy through symlink parent wrote outside target or stat failed: %v", err)
		}
	})

	t.Run("rejects symlink leaf target", func(t *testing.T) {
		tmp := t.TempDir()
		root := filepath.Join(tmp, "orders")
		outside := filepath.Join(tmp, "outside")
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatalf("mkdir root: %v", err)
		}
		if err := os.MkdirAll(outside, 0o750); err != nil {
			t.Fatalf("mkdir outside: %v", err)
		}
		src := filepath.Join(tmp, "contract.api")
		if err := os.WriteFile(src, []byte("service orders {}\n"), 0o600); err != nil {
			t.Fatalf("write source contract: %v", err)
		}
		outsideFile := filepath.Join(outside, "orders.api")
		if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
			t.Fatalf("write outside file: %v", err)
		}
		leaf := filepath.Join(root, "orders.api")
		if err := os.Symlink(outsideFile, leaf); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		err := copyNewServiceContractFile(src, leaf, root)
		if err == nil || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("copy to symlink leaf error = %v, want symlink leaf rejection", err)
		}
		data, err := os.ReadFile(outsideFile)
		if err != nil {
			t.Fatalf("read outside file: %v", err)
		}
		if string(data) != "outside" {
			t.Fatalf("symlink leaf copy mutated outside file: %q", data)
		}
	})

	t.Run("rejects target escaping output root", func(t *testing.T) {
		tmp := t.TempDir()
		root := filepath.Join(tmp, "orders")
		if err := os.MkdirAll(root, 0o750); err != nil {
			t.Fatalf("mkdir root: %v", err)
		}
		src := filepath.Join(tmp, "contract.api")
		if err := os.WriteFile(src, []byte("service orders {}\n"), 0o600); err != nil {
			t.Fatalf("write source contract: %v", err)
		}
		escape := filepath.Join(tmp, "escape.api")
		err := copyNewServiceContractFile(src, escape, root)
		if err == nil || !strings.Contains(err.Error(), "escapes root") {
			t.Fatalf("copy escaping root error = %v, want escape rejection", err)
		}
		if _, err := os.Stat(escape); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("copy escaping root wrote target or stat failed: %v", err)
		}
	})
}

func TestCommandIOAndAIDoctorBoundaries(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	verbose := true
	quiet := false
	if got := resolveVerbosity(&verbose, nil, nil, nil); got != verbosityVerbose {
		t.Fatalf("resolveVerbosity verbose = %d, want verbose", got)
	}
	if got := resolveVerbosity(nil, nil, &quiet, nil); got != verbosityNormal {
		t.Fatalf("resolveVerbosity false quiet = %d, want normal", got)
	}
	if got := normalizeOutputMode("xml"); got != "xml" {
		t.Fatalf("normalizeOutputMode custom = %q, want passthrough", got)
	}
	err := withCommandIO(IOStreams{Out: &out, Err: &errOut}, outputJSON, verbosityVerbose, func() error {
		cliOutputIf("visible")
		verboseOutputf("debug %d", 1)
		if OutputMode() != outputJSON || currentOut() != &out || currentErr() != &errOut {
			t.Fatalf("command IO state not applied")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withCommandIO error = %v", err)
	}
	if out.String() != "visible" || errOut.String() != "debug 1" {
		t.Fatalf("captured out=%q err=%q, want visible/debug", out.String(), errOut.String())
	}
	_ = withCommandIO(IOStreams{Out: &out}, outputText, verbosityQuiet, func() error {
		before := out.Len()
		cliOutputIf("quiet")
		cliOutputfIf("quiet")
		cliOutputlnIf("quiet")
		if out.Len() != before {
			t.Fatalf("quiet mode wrote %q", out.String()[before:])
		}
		return nil
	})

	t.Setenv("GOFLY_LLM_CACHE_TTL", "30s")
	t.Setenv("GOFLY_LLM_CACHE_MAX_SIZE", "8")
	cache := checkAIDoctorCache()
	if cache.Status != "ok" || !strings.Contains(cache.Message, "GOFLY_LLM_CACHE_TTL=30s") || !strings.Contains(cache.Message, "GOFLY_LLM_CACHE_MAX_SIZE=8") {
		t.Fatalf("checkAIDoctorCache = %#v, want env-backed ok status", cache)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".gofly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, generator.DefaultConfigFile), []byte(`{"llm":{"provider":"noop","model":"noop"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	config := checkAIDoctorConfig()
	if config.Status != "ok" || !strings.Contains(config.Message, "noop") {
		t.Fatalf("checkAIDoctorConfig = %#v, want workdir config", config)
	}
}

func TestAIExecutionFailoverAndPlanHelpers(t *testing.T) {
	ctx, cancel := aiExecutionContext(aiCompleteConfig{})
	cancel()
	if err := ctx.Err(); err != nil {
		t.Fatalf("aiExecutionContext without timeout ctx err = %v, want nil", err)
	}
	ctx, cancel = aiExecutionContext(aiCompleteConfig{Timeout: time.Nanosecond})
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for aiExecutionContext deadline")
	}

	retryable := errors.Join(errors.New("wrapped"), llm.ErrRateLimited)
	if !shouldAttemptManualFailover(aiCompleteConfig{AllowFailover: true, FailoverProviders: []string{"backup"}}, 0, retryable) {
		t.Fatal("shouldAttemptManualFailover rejected retryable primary failure")
	}
	if shouldAttemptManualFailover(aiCompleteConfig{AllowFailover: true, FailoverProviders: []string{"backup"}}, 1, retryable) || shouldAttemptManualFailover(aiCompleteConfig{AllowFailover: false, FailoverProviders: []string{"backup"}}, 0, retryable) || shouldAttemptManualFailover(aiCompleteConfig{AllowFailover: true}, 0, retryable) || shouldAttemptManualFailover(aiCompleteConfig{AllowFailover: true, FailoverProviders: []string{"backup"}}, 0, errors.New("fatal")) {
		t.Fatal("shouldAttemptManualFailover accepted non-primary, disabled, missing backup, or non-retryable failure")
	}
	if got := failoverFrom(0, "primary"); got != "" {
		t.Fatalf("failoverFrom primary = %q, want empty", got)
	}
	if got := failoverFrom(1, "primary"); got != "primary" {
		t.Fatalf("failoverFrom backup = %q, want primary", got)
	}
	key1 := aiFailoverIdempotencyKey("prompt", aiCompleteConfig{Provider: "noop", Model: "a", MaxInputTokens: 1})
	key2 := aiFailoverIdempotencyKey("prompt", aiCompleteConfig{Provider: "noop", Model: "b", MaxInputTokens: 1})
	if !strings.HasPrefix(key1, "gofly-ai-") || key1 == key2 {
		t.Fatalf("aiFailoverIdempotencyKey key1=%q key2=%q, want stable prefix and model-sensitive hash", key1, key2)
	}
	metadata := aiAttemptMetadata("ai complete", 1, "primary", "backup", key1, true)
	if metadata["provider_attempt"] != "2" || metadata["manual_failover"] != "true" || metadata["failover_from"] != "primary" || metadata["failover_to"] != "backup" || metadata["idempotency_key"] != key1 {
		t.Fatalf("aiAttemptMetadata failover = %#v", metadata)
	}
	metadata = aiAttemptMetadata("ai complete", 0, "primary", "primary", key1, false)
	if _, ok := metadata["manual_failover_allowed"]; ok || metadata["provider_attempt"] != "1" {
		t.Fatalf("aiAttemptMetadata primary = %#v", metadata)
	}

	resp := &jsonError{NextActions: []string{"existing"}}
	addProviderFailoverNextActions(resp)
	addProviderFailoverNextActions(resp)
	if len(resp.NextActions) != 4 || resp.NextActions[0] != "existing" {
		t.Fatalf("failover next actions = %#v, want deduplicated additions", resp.NextActions)
	}
	addProviderFailoverNextActions(nil)
	if got := appendMissingStrings([]string{"a"}, "", "a", "b"); strings.Join(got, ",") != "a,b" {
		t.Fatalf("appendMissingStrings = %v, want a,b", got)
	}
	if commandName(nil) != "root" || commandName([]string{"ai"}) != "ai" || commandName([]string{"ai", "stream", "ignored"}) != "ai.stream" {
		t.Fatal("commandName boundary mismatch")
	}
}

func TestAIStreamPlanJSONBoundary(t *testing.T) {
	var out bytes.Buffer
	err := withCommandIO(IOStreams{Out: &out}, outputJSON, verbosityNormal, func() error {
		return printAIStreamPlanFor("ai.stream", "ai stream", aiCompleteConfig{
			Provider:           "noop",
			Model:              "noop",
			ConfigPath:         ".gofly/config.json",
			MaxInputTokens:     10,
			MaxOutputTokens:    3,
			MaxTotalTokens:     13,
			RateLimitPerSecond: 2,
			RateLimitBurst:     4,
			Timeout:            time.Second,
			AllowFailover:      true,
			FailoverProviders:  []string{"noop", "missing"},
		}, 5, true)
	})
	if err != nil {
		t.Fatalf("printAIStreamPlanFor: %v", err)
	}
	planOutput := out.String()
	for _, want := range []string{"ai stream", "estimatedInputTokens", "plan-provider-failover", "GOFLY_LLM_FAILOVER_PROVIDERS", "missing"} {
		if !strings.Contains(planOutput, want) {
			t.Fatalf("stream plan output missing %q:\n%s", want, planOutput)
		}
	}
}

func TestRootControlVersionAndHelpBoundaries(t *testing.T) {
	output, verbosity, remaining, err := parseGlobalControls([]string{"--output=json", "-v", "version"})
	if err != nil || output != outputJSON || verbosity != verbosityVerbose || strings.Join(remaining, ",") != "version" {
		t.Fatalf("parseGlobalControls json verbose = output=%q verbosity=%d remaining=%v err=%v", output, verbosity, remaining, err)
	}
	output, verbosity, remaining, err = parseGlobalControls([]string{"--output", "text", "--quiet", "doctor"})
	if err != nil || output != outputText || verbosity != verbosityQuiet || strings.Join(remaining, ",") != "doctor" {
		t.Fatalf("parseGlobalControls text quiet = output=%q verbosity=%d remaining=%v err=%v", output, verbosity, remaining, err)
	}
	for _, args := range [][]string{{"--output"}, {"--output", "xml"}, {"--output=xml"}} {
		if _, _, _, err := parseGlobalControls(args); err == nil {
			t.Fatalf("parseGlobalControls(%v) succeeded, want error", args)
		}
	}
	if output, remaining, err := parseGlobalOutput([]string{"--output=json", "version"}); err != nil || output != outputJSON || len(remaining) != 1 || remaining[0] != "version" {
		t.Fatalf("parseGlobalOutput = output=%q remaining=%v err=%v", output, remaining, err)
	}

	if topic, ok := commandHelpTopic("api", []string{"help", "go", "--format"}); !ok || topic != "api go" {
		t.Fatalf("commandHelpTopic help = %q %t, want api go", topic, ok)
	}
	if topic, ok := commandHelpTopic("api", []string{"go", "--api", "svc.api", "--help"}); !ok || topic != "api go" {
		t.Fatalf("commandHelpTopic trailing = %q %t, want api go", topic, ok)
	}
	if got := leadingHelpTopicArgs([]string{"go", "", "ignored"}); len(got) != 1 || got[0] != "go" {
		t.Fatalf("leadingHelpTopicArgs = %v, want [go]", got)
	}
	if got := joinHelpTopic("api", nil); got != "api" {
		t.Fatalf("joinHelpTopic no parts = %q, want api", got)
	}
	if got := normalizeGoctlStyleFlags(nil); got != nil {
		t.Fatalf("normalizeGoctlStyleFlags(nil) = %v, want nil", got)
	}

	var errOut bytes.Buffer
	err = withCommandIO(IOStreams{Err: &errOut}, outputText, verbosityNormal, func() error {
		warnNoopFlag("cmd", "legacy", "")
		return nil
	})
	if err != nil {
		t.Fatalf("withCommandIO warnNoopFlag: %v", err)
	}
	if !strings.Contains(errOut.String(), "accepted for compatibility") {
		t.Fatalf("warnNoopFlag output = %q, want default reason", errOut.String())
	}

	var out bytes.Buffer
	err = withCommandIO(IOStreams{Out: &out}, outputJSON, verbosityNormal, func() error {
		return versionCommand(nil)
	})
	if err != nil {
		t.Fatalf("versionCommand json: %v", err)
	}
	if !strings.Contains(out.String(), `"command"`) || !strings.Contains(out.String(), `"version"`) || !strings.Contains(out.String(), `"tool"`) || !strings.Contains(out.String(), `"gofly"`) {
		t.Fatalf("versionCommand json output = %s", out.String())
	}
	if err := versionCommand([]string{"extra"}); err == nil {
		t.Fatal("versionCommand positional succeeded, want usage error")
	}

	var execOut bytes.Buffer
	err = ExecuteWithIO([]string{"--output=json", "unknown"}, IOStreams{Out: &execOut})
	if err == nil || !strings.Contains(execOut.String(), `"command"`) || !strings.Contains(execOut.String(), `"unknown"`) || !strings.Contains(execOut.String(), `"USAGE_ERROR"`) {
		t.Fatalf("ExecuteWithIO unknown err=%v out=%s, want JSON error envelope", err, execOut.String())
	}
}

func TestDoctorExampleAndRootHelperBoundaries(t *testing.T) {
	if got := checkGoModule(); got.Status != "ok" {
		t.Fatalf("checkGoModule default = %#v, want ok", got)
	}
	t.Setenv("GO111MODULE", "off")
	if got := checkGoModule(); got.Status != "fail" || !strings.Contains(got.Message, "GO111MODULE=off") {
		t.Fatalf("checkGoModule off = %#v, want fail", got)
	}
	t.Setenv("GO111MODULE", "")

	if got := checkGOPATH(); got.Name != "GOPATH" || got.Status == "" {
		t.Fatalf("checkGOPATH = %#v, want named status", got)
	}
	missingPath := filepath.Join(t.TempDir(), "missing-bin")
	t.Setenv("PATH", missingPath)
	if got := checkTools(); got.Status != "fail" || !strings.Contains(got.Message, "go") || !strings.Contains(got.Message, "git") {
		t.Fatalf("checkTools missing PATH = %#v, want missing go/git", got)
	}
	if got := checkGit(); got.Status != "fail" || !strings.Contains(got.Message, "not found") {
		t.Fatalf("checkGit missing PATH = %#v, want fail", got)
	}
	if got := checkProtoc(); got.Status != "warn" || !strings.Contains(got.Message, "not found") {
		t.Fatalf("checkProtoc missing PATH = %#v, want warn", got)
	}

	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing-tmp"))
	if got := checkWritePermission(); got.Status != "fail" || !strings.Contains(got.Message, "cannot write") {
		t.Fatalf("checkWritePermission missing temp = %#v, want fail", got)
	}

	report := runDoctor()
	if len(report.Checks) != 7 || report.Summary == "" || report.Version == "" || report.Go == "" || report.OS == "" || report.Arch == "" {
		t.Fatalf("runDoctor report = %#v, want complete diagnostic report", report)
	}

	if err := exampleCommand(nil); !errors.Is(err, errUsage) {
		t.Fatalf("exampleCommand nil error = %v, want errUsage", err)
	}
	if err := exampleRunCommand(nil); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "example name") {
		t.Fatalf("exampleRunCommand nil error = %v, want name usage", err)
	}
	if err := exampleRunCommand([]string{"missing-example"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "unknown example") {
		t.Fatalf("exampleRunCommand unknown error = %v, want unknown usage", err)
	}

	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return exampleListCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("exampleListCommand json: %v", err)
	}
	if !strings.Contains(out.String(), "gateway-discovery-rpc") || !strings.Contains(out.String(), "restserver") {
		t.Fatalf("example list JSON missing built-in examples: %s", out.String())
	}

	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "README.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyExampleDir(src, dst); err != nil {
		t.Fatalf("copyExampleDir nested: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "nested", "README.txt")); err != nil || string(got) != "nested" {
		t.Fatalf("copied nested file = %q, %v", string(got), err)
	}
	if err := copyExampleDir(filepath.Join(src, "missing"), filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("copyExampleDir missing source succeeded, want error")
	}

	parsed := parseKeyValueCSV(" env = prod ,empty=,flag,=ignored, team = core ")
	if len(parsed) != 4 || parsed["env"] != "prod" || parsed["empty"] != "" || parsed["flag"] != "" || parsed["team"] != "core" {
		t.Fatalf("parseKeyValueCSV = %#v", parsed)
	}
	filterCases := []struct {
		name     string
		template string
		category string
		filter   string
		want     bool
	}{
		{name: "api category", template: "api-handler.tpl", category: "api", want: true},
		{name: "kubernetes alias", template: "kube-deploy.tpl", category: "kubernetes", want: true},
		{name: "name without extension", template: "model.tpl", filter: "model", want: true},
		{name: "name mismatch", template: "rpc-client.tpl", filter: "api", want: false},
		{name: "substring category", template: "custom-worker.tpl", category: "worker", want: true},
	}
	for _, tt := range filterCases {
		t.Run(tt.name, func(t *testing.T) {
			if got := templateFilterMatch(tt.template, tt.category, tt.filter); got != tt.want {
				t.Fatalf("templateFilterMatch(%q,%q,%q) = %v, want %v", tt.template, tt.category, tt.filter, got, tt.want)
			}
		})
	}

	if inferTopLevelRisk("doctor") != "read" || inferTopLevelRisk("plugin") != "high" || inferTopLevelRisk("new") != "medium" || inferTopLevelRisk("unknown") != "medium" {
		t.Fatal("inferTopLevelRisk boundary mismatch")
	}
	if topLevelMayMutate("doctor") || !topLevelMayMutate("plugin") || !topLevelMayMutate("unknown") {
		t.Fatal("topLevelMayMutate boundary mismatch")
	}
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		if err := printJSONLine(map[string]string{"b": "2", "a": "1"}); err != nil {
			return err
		}
		return printJSONLine(make(chan int))
	}); err == nil || !strings.Contains(err.Error(), "marshal json line") || !strings.Contains(out.String(), `"a":"1"`) {
		t.Fatalf("printJSONLine mixed result err=%v out=%q, want first line and marshal error", err, out.String())
	}
}

func TestLooksLikeShellScriptBoundaries(t *testing.T) {
	dir := t.TempDir()
	if looksLikeShellScript("") || looksLikeShellScript(filepath.Join(dir, "missing")) || looksLikeShellScript(dir) {
		t.Fatal("empty, missing, or directory path should not look like shell script")
	}
	if !looksLikeShellScript(filepath.Join(dir, "run.SH")) {
		t.Fatal(".SH extension should be treated as shell script")
	}
	shebang := filepath.Join(dir, "plugin")
	if err := os.WriteFile(shebang, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !looksLikeShellScript(shebang) {
		t.Fatal("shebang file should look like shell script")
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	if looksLikeShellScript(plain) {
		t.Fatal("plain file should not look like shell script")
	}
}

func TestPluginPlanAndConfigHelpers(t *testing.T) {
	plan := pluginRunPlan("generate", "/tmp/app", "orders", "example.com/orders", "https://example.test/plugin", "./plugin.so", []string{"a", "b"})
	if !plan.DryRun || !plan.MutatesFilesystem || plan.Command != "plugin run" || plan.Inputs["plugins"] != "a,b" || len(plan.Actions) != 3 || len(plan.Warnings) == 0 || len(plan.NextActions) == 0 {
		t.Fatalf("pluginRunPlan = %#v, want dry-run filesystem plan with actions", plan)
	}

	cfg := &generator.Config{}
	sets := map[string]string{
		"service-name":          "orders",
		"module":                "example.com/orders",
		"features":              "",
		"rpc.plugins":           "audit,trace",
		"rpc.transport":         "framed",
		"api.plugins":           "lint",
		"model.types-map":       "uuid=string,decimal=float64",
		"model.cache":           "yes",
		"model.strict":          "on",
		"llm.provider":          "openai",
		"llm.model":             "gpt-test",
		"llm.max-input-tokens":  "100",
		"llm.max-output-tokens": "20",
		"llm.max-total-tokens":  "120",
		"llm.rate-limit":        "2",
		"llm.rate-burst":        "3",
		"llm.timeout":           "5s",
		"extra.key":             "extra-value",
	}
	for key, value := range sets {
		if err := setConfigField(cfg, key, value); err != nil {
			t.Fatalf("setConfigField(%q): %v", key, err)
		}
	}
	checks := map[string]string{
		"service":               "orders",
		"module":                "example.com/orders",
		"features":              "",
		"rpc-plugins":           "audit,trace",
		"rpc-transport":         "framed",
		"api-plugins":           "lint",
		"model.typesmap":        "decimal=float64,uuid=string",
		"model-cache":           "true",
		"model-strict":          "true",
		"llm-provider":          "openai",
		"llm-model":             "gpt-test",
		"llm.max-input-tokens":  "100",
		"llm.max-output-tokens": "20",
		"llm.max-total-tokens":  "120",
		"llm-rate-limit":        "2",
		"llm-rate-burst":        "3",
		"llm-timeout":           "5s",
		"extra.key":             "extra-value",
	}
	for key, want := range checks {
		if got := getConfigField(cfg, key); got != want {
			t.Fatalf("getConfigField(%q) = %q, want %q", key, got, want)
		}
	}
	if err := setConfigField(cfg, "llm.max-input-tokens", "-1"); err == nil || !strings.Contains(err.Error(), "non-negative integer") {
		t.Fatalf("negative llm tokens error = %v, want non-negative integer", err)
	}
	if err := setConfigField(cfg, "llm.timeout", "not-duration"); err == nil || !strings.Contains(err.Error(), "invalid llm.timeout") {
		t.Fatalf("invalid timeout error = %v, want invalid timeout", err)
	}
	if parseBoolString("off") || !parseBoolString("YES") || !isConfigFeaturesKey(" features ") {
		t.Fatal("parseBoolString or isConfigFeaturesKey boundary mismatch")
	}
}

func TestAIDoctorNextActionsBoundaries(t *testing.T) {
	tests := []struct {
		name string
		item aiDoctorItem
		want string
	}{
		{name: "secret env specific", item: aiDoctorItem{Name: "secret.openai.OPENAI_API_KEY", Status: "fail"}, want: "OPENAI_API_KEY"},
		{name: "failover warn", item: aiDoctorItem{Name: "failover", Status: "warn"}, want: "remove invalid providers"},
		{name: "failover info", item: aiDoctorItem{Name: "failover", Status: "info"}, want: "manual failover"},
		{name: "config info", item: aiDoctorItem{Name: "config", Status: "info"}, want: "config init"},
		{name: "cache info", item: aiDoctorItem{Name: "cache", Status: "info"}, want: "GOFLY_LLM_CACHE_TTL"},
		{name: "telemetry", item: aiDoctorItem{Name: "telemetry", Status: "ok"}, want: "low-cardinality"},
		{name: "cost", item: aiDoctorItem{Name: "cost", Status: "warn"}, want: "totalTokens"},
		{name: "provider registry", item: aiDoctorItem{Name: "provider.registry", Status: "warn"}, want: "register at least one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := aiDoctorNextActions(tt.item)
			if len(actions) == 0 || !strings.Contains(strings.Join(actions, "\n"), tt.want) {
				t.Fatalf("aiDoctorNextActions(%#v) = %#v, want containing %q", tt.item, actions, tt.want)
			}
		})
	}
	if got := aiDoctorNextActions(aiDoctorItem{Name: "unknown", Status: "ok"}); got != nil {
		t.Fatalf("unknown next actions = %#v, want nil", got)
	}
}

func TestAIDoctorConfigAndLoadOverlayBoundaries(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".gofly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, generator.DefaultConfigFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workDir)

	homeAsFile := filepath.Join(t.TempDir(), "home-file")
	if err := os.WriteFile(homeAsFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeAsFile)
	if got := checkAIDoctorConfig(); got.Status != "info" || !strings.Contains(got.Message, "no "+generator.DefaultConfigFile) {
		t.Fatalf("checkAIDoctorConfig invalid workdir/home = %#v, want info", got)
	}

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	homeConfig := generator.DefaultConfig("home", "example.com/home")
	homeConfig.LLM = &generator.LLMConfig{Provider: "openai", Model: "gpt-home", MaxTotalTokens: 42}
	if err := generator.SaveConfig(filepath.Join(homeDir, generator.DefaultConfigFile), homeConfig); err != nil {
		t.Fatal(err)
	}
	if got := checkAIDoctorConfig(); got.Status != "ok" || !strings.Contains(got.Message, homeDir) || !strings.Contains(got.Message, "gpt-home") {
		t.Fatalf("checkAIDoctorConfig home config = %#v, want ok home config", got)
	}

	workConfig := generator.DefaultConfig("work", "example.com/work")
	workConfig.LLM = &generator.LLMConfig{Provider: "noop", Model: "work-model"}
	if err := generator.SaveConfig(filepath.Join(workDir, generator.DefaultConfigFile), workConfig); err != nil {
		t.Fatal(err)
	}
	if got := checkAIDoctorConfig(); got.Status != "ok" || !strings.Contains(got.Message, generator.DefaultConfigFile) || !strings.Contains(got.Message, "work-model") {
		t.Fatalf("checkAIDoctorConfig workdir config = %#v, want ok workdir config", got)
	}

	baseConfig := generator.DefaultConfig("base", "example.com/base")
	baseConfig.RPC.Plugins = []string{"base-rpc"}
	baseConfig.API.Plugins = []string{"base-api"}
	baseConfig.Features = []string{"base-feature"}
	configPath := filepath.Join(t.TempDir(), generator.DefaultConfigFile)
	if err := generator.SaveConfig(configPath, baseConfig); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLY_SERVICE_NAME", "env-service")
	t.Setenv("GOFLY_MODULE", "example.com/env")
	t.Setenv("GOFLY_FEATURES", "env-a, env-b")
	cfg, resolved, err := loadAndOverlay(configPath, "ignored-dir", "cli-service", "example.com/cli", "production", "./tpl", "https://example.test/tpl.git", "main", "cli-a,cli-b", "audit,trace", "rpc")
	if err != nil {
		t.Fatalf("loadAndOverlay rpc: %v", err)
	}
	if resolved != configPath || cfg.ServiceName != "cli-service" || cfg.Module != "example.com/cli" || cfg.Style != "production" || cfg.TemplateDir != "./tpl" || cfg.TemplateRemote == "" || cfg.TemplateBranch != "main" {
		t.Fatalf("loadAndOverlay resolved=%q cfg=%#v, want CLI overlays to win", resolved, cfg)
	}
	if strings.Join(cfg.Features, ",") != "env-a,env-b,cli-a,cli-b" || strings.Join(cfg.RPC.Plugins, ",") != "base-rpc,audit,trace" {
		t.Fatalf("loadAndOverlay features/plugins = %#v/%#v, want merged CLI overlays", cfg.Features, cfg.RPC.Plugins)
	}

	cfg, _, err = loadAndOverlay(configPath, "", "", "", "", "", "", "", "", "cache", "api")
	if err != nil {
		t.Fatalf("loadAndOverlay api: %v", err)
	}
	if strings.Join(cfg.API.Plugins, ",") != "base-api,cache" {
		t.Fatalf("loadAndOverlay api plugins = %#v, want base-api,cache", cfg.API.Plugins)
	}
	if _, _, err := loadAndOverlay(filepath.Join(t.TempDir(), "bad", "config.json"), "", "", "", "", "", "", "", "", "", "rpc"); err != nil {
		t.Fatalf("missing config should load defaults, got %v", err)
	}
}

func TestTemplateCatalogCommandsExposeJSON(t *testing.T) {
	t.Run("list filters project templates", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"template", "list", "--category", "rag", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("template list: %v", err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    []struct {
				ID       string   `json:"id"`
				Features []string `json:"features"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("template list JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "template.list" || len(envelope.Data) != 1 || envelope.Data[0].ID != "go-rag-service" {
			t.Fatalf("template list envelope = %+v, want go-rag-service only", envelope)
		}
	})

	t.Run("inspect returns template metadata", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"template", "inspect", "go-ai-agent", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("template inspect: %v", err)
		}
		var envelope struct {
			OK   bool `json:"ok"`
			Data struct {
				ID      string   `json:"id"`
				Kind    string   `json:"kind"`
				Command string   `json:"command"`
				Verify  []string `json:"verify"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("template inspect JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Data.ID != "go-ai-agent" || envelope.Data.Kind != "ai-agent" || !strings.Contains(envelope.Data.Command, "gofly new service") || len(envelope.Data.Verify) == 0 {
			t.Fatalf("template inspect envelope = %+v", envelope)
		}
	})
}

func TestAIPlanSelectsTemplateAndMaterializesCommand(t *testing.T) {
	var stdout bytes.Buffer
	args := []string{"ai", "plan", "需要一个 RAG 服务，使用 embedding 和 redis vector store", "--name", "kb", "--module", "example.com/kb", "--json"}
	if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
		t.Fatalf("ai plan: %v", err)
	}
	var envelope struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			ProjectType       string `json:"projectType"`
			Command           string `json:"command"`
			DryRun            bool   `json:"dryRun"`
			MutatesFilesystem bool   `json:"mutatesFilesystem"`
			Template          struct {
				ID string `json:"id"`
			} `json:"template"`
			Features []string `json:"features"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("ai plan JSON: %v\n%s", err, stdout.String())
	}
	if !envelope.OK || envelope.Command != "ai.plan" || envelope.Data.Template.ID != "go-rag-service" || envelope.Data.ProjectType != "rag" {
		t.Fatalf("ai plan envelope = %+v, want rag plan", envelope)
	}
	if !envelope.Data.DryRun || envelope.Data.MutatesFilesystem {
		t.Fatalf("ai plan should be dry-run only by default: %+v", envelope.Data)
	}
	if !strings.Contains(envelope.Data.Command, "example.com/kb") || !strings.Contains(envelope.Data.Command, "--dir kb") {
		t.Fatalf("ai plan command = %q, want materialized module and dir", envelope.Data.Command)
	}
}

func TestAINewPlansAndAppliesSelectedTemplate(t *testing.T) {
	t.Run("dry-run json plan does not write files", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "kb")
		var stdout bytes.Buffer
		args := []string{"ai", "new", "需要一个 RAG 服务，使用 embedding 和 redis vector store", "--name", "kb", "--module", "example.com/kb", "--dir", outDir, "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new dry-run: %v", err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				ProjectType       string   `json:"projectType"`
				Command           string   `json:"command"`
				DryRun            bool     `json:"dryRun"`
				MutatesFilesystem bool     `json:"mutatesFilesystem"`
				Warnings          []string `json:"warnings"`
				Template          struct {
					ID string `json:"id"`
				} `json:"template"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai new dry-run JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.new" || envelope.Data.Template.ID != "go-rag-service" || envelope.Data.ProjectType != "rag" {
			t.Fatalf("ai new dry-run envelope = %+v, want rag plan", envelope)
		}
		if !envelope.Data.DryRun || envelope.Data.MutatesFilesystem {
			t.Fatalf("ai new dry-run mutation flags = %+v, want dry-run without filesystem mutation", envelope.Data)
		}
		if !strings.Contains(envelope.Data.Command, outDir) || len(envelope.Data.Warnings) == 0 {
			t.Fatalf("ai new dry-run command/warnings = %+v", envelope.Data)
		}
		if _, err := os.Stat(filepath.Join(outDir, "go.mod")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ai new dry-run wrote go.mod or returned unexpected stat error: %v", err)
		}
	})

	t.Run("apply writes scaffold and reports result", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "hello")
		var stdout bytes.Buffer
		args := []string{"ai", "new", "--template", "go-rest-minimal", "--name", "hello", "--module", "example.com/hello", "--dir", outDir, "--apply", "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new apply: %v", err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Applied           bool   `json:"applied"`
				OutputDir         string `json:"outputDir"`
				ExecutedCommand   string `json:"executedCommand"`
				MutatesFilesystem bool   `json:"mutatesFilesystem"`
				GeneratedFeatures []struct {
					Plugin         string   `json:"plugin"`
					Files          []string `json:"files"`
					VerifyCommands []string `json:"verifyCommands"`
				} `json:"generatedFeatures"`
				ConfigHints []struct {
					Key string `json:"key"`
				} `json:"configHints"`
				FeatureVerify []string `json:"featureVerify"`
				Verify        []string `json:"verify"`
				VerifyRan     bool     `json:"verifyRan"`
				VerifyPassed  bool     `json:"verifyPassed"`
				Plan          struct {
					DryRun            bool `json:"dryRun"`
					MutatesFilesystem bool `json:"mutatesFilesystem"`
					Template          struct {
						ID string `json:"id"`
					} `json:"template"`
				} `json:"plan"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai new apply JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.new" || !envelope.Data.Applied || !envelope.Data.MutatesFilesystem || envelope.Data.OutputDir != outDir {
			t.Fatalf("ai new apply envelope = %+v, want applied result", envelope)
		}
		if envelope.Data.Plan.DryRun || !envelope.Data.Plan.MutatesFilesystem || envelope.Data.Plan.Template.ID != "go-rest-minimal" {
			t.Fatalf("ai new apply plan = %+v, want mutating go-rest-minimal plan", envelope.Data.Plan)
		}
		if !strings.Contains(envelope.Data.ExecutedCommand, "gofly new api hello") || len(envelope.Data.Verify) == 0 {
			t.Fatalf("ai new apply command/verify = %+v", envelope.Data)
		}
		if len(envelope.Data.GeneratedFeatures) == 0 || envelope.Data.GeneratedFeatures[0].Plugin != "observability" {
			t.Fatalf("ai new apply generatedFeatures = %+v, want observability plugin", envelope.Data.GeneratedFeatures)
		}
		if got := strings.Join(envelope.Data.FeatureVerify, ","); got != "go vet ./...,go test ./..." {
			t.Fatalf("ai new apply featureVerify = %q, want feature-specific verification declarations", got)
		}
		if got := strings.Join(envelope.Data.Verify, ","); got != "gofmt,go mod tidy,go test ./...,go vet ./..." {
			t.Fatalf("ai new apply verify = %q, want template and feature verify commands", got)
		}
		if len(envelope.Data.ConfigHints) == 0 || envelope.Data.ConfigHints[0].Key != "LOG_LEVEL" {
			t.Fatalf("ai new apply configHints = %+v, want LOG_LEVEL", envelope.Data.ConfigHints)
		}
		if envelope.Data.VerifyRan || envelope.Data.VerifyPassed {
			t.Fatalf("ai new apply without --verify verify flags = %+v, want false", envelope.Data)
		}
		if _, err := os.Stat(filepath.Join(outDir, "go.mod")); err != nil {
			t.Fatalf("ai new apply did not write go.mod: %v", err)
		}
		if _, err := os.Stat(filepath.Join(outDir, "docs", "openapi.yaml")); err != nil {
			t.Fatalf("ai new apply did not write openapi feature file: %v", err)
		}
	})

	t.Run("apply verify compiles generated rest project", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "verify-rest")
		withFrameworkPath(t, func() {
			var stdout bytes.Buffer
			args := []string{"ai", "new", "--template", "go-rest-minimal", "--name", "hello", "--module", "example.com/hello", "--dir", outDir, "--apply", "--verify", "--verify-timeout", "2m", "--json"}
			if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
				t.Fatalf("ai new apply --verify: %v", err)
			}
			var envelope struct {
				OK   bool `json:"ok"`
				Data struct {
					VerifyRan    bool `json:"verifyRan"`
					VerifyPassed bool `json:"verifyPassed"`
					Verification []struct {
						Command string `json:"command"`
						Status  string `json:"status"`
						Output  string `json:"output"`
						Error   string `json:"error"`
					} `json:"verification"`
				} `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatalf("ai new apply verify JSON: %v\n%s", err, stdout.String())
			}
			if !envelope.OK || !envelope.Data.VerifyRan || !envelope.Data.VerifyPassed {
				t.Fatalf("ai new apply verify envelope = %+v\n%s", envelope, stdout.String())
			}
			gotCommands := make([]string, 0, len(envelope.Data.Verification))
			for _, check := range envelope.Data.Verification {
				gotCommands = append(gotCommands, check.Command+":"+check.Status)
				if check.Status != "passed" {
					t.Fatalf("verification check failed: %+v\n%s", check, stdout.String())
				}
			}
			if strings.Join(gotCommands, ",") != "gofmt:passed,go mod tidy:passed,go test ./...:passed,go vet ./...:passed,control-plane snapshot:passed" {
				t.Fatalf("verification commands = %v, want gofmt/go mod tidy/go test/go vet/control-plane snapshot passed", gotCommands)
			}
		})
	})
}

func TestAINewFlagValidationBoundaries(t *testing.T) {
	baseArgs := []string{"ai", "new", "--template", "go-rest-minimal", "--name", "hello", "--module", "example.com/hello", "--dir", filepath.Join(t.TempDir(), "hello")}
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "apply rejects explicit dry run",
			args:    append(append([]string{}, baseArgs...), "--apply", "--dry-run"),
			wantErr: "--apply cannot be combined",
		},
		{
			name:    "apply rejects plan alias",
			args:    append(append([]string{}, baseArgs...), "--apply", "--plan"),
			wantErr: "--apply cannot be combined",
		},
		{
			name:    "prompt flag rejects positional prompt",
			args:    []string{"ai", "new", "positional", "--prompt", "flag", "--name", "hello", "--module", "example.com/hello", "--dir", filepath.Join(t.TempDir(), "dupe")},
			wantErr: "either --prompt or positional prompt",
		},
		{
			name:    "requires prompt or template",
			args:    []string{"ai", "new", "--name", "hello", "--module", "example.com/hello", "--dir", filepath.Join(t.TempDir(), "missing")},
			wantErr: "--prompt, positional prompt text, or --template is required",
		},
		{
			name:    "rejects unsupported format",
			args:    append(append([]string{}, baseArgs...), "--format", "yaml"),
			wantErr: "unsupported --format",
		},
		{
			name:    "rejects invalid verify timeout",
			args:    append(append([]string{}, baseArgs...), "--verify-timeout", "0s"),
			wantErr: "--verify-timeout must be a positive duration",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ExecuteWithIO(tt.args, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
			if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ExecuteWithIO(%v) error = %v, want errUsage containing %q", tt.args, err, tt.wantErr)
			}
		})
	}

	t.Run("template without prompt is accepted for dry-run", func(t *testing.T) {
		var stdout bytes.Buffer
		args := append(append([]string{}, baseArgs...), "--format", "json")
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new template dry-run: %v", err)
		}
		var envelope struct {
			Command string `json:"command"`
			Data    struct {
				DryRun bool `json:"dryRun"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai new template dry-run JSON: %v\n%s", err, stdout.String())
		}
		if envelope.Command != "ai.new" || !envelope.Data.DryRun {
			t.Fatalf("ai new template dry-run envelope = %+v", envelope)
		}
	})

	t.Run("apply accepts dry-run false", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "apply-false")
		var stdout bytes.Buffer
		args := []string{"ai", "new", "--template", "go-rest-minimal", "--name", "hello", "--module", "example.com/hello", "--dir", outDir, "--apply", "--dry-run=false", "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new apply --dry-run=false: %v", err)
		}
		if _, err := os.Stat(filepath.Join(outDir, "go.mod")); err != nil {
			t.Fatalf("ai new apply --dry-run=false did not write go.mod: %v", err)
		}
	})
}

func TestAINewFeatureLibrary(t *testing.T) {
	t.Run("apply rag reports and writes concrete feature plugins", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "rag")
		var stdout bytes.Buffer
		args := []string{"ai", "new", "--template", "go-rag-service", "--name", "rag", "--module", "example.com/rag", "--dir", outDir, "--apply", "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new rag apply: %v", err)
		}
		var envelope struct {
			Data struct {
				GeneratedFeatures []struct {
					Plugin         string   `json:"plugin"`
					Tags           []string `json:"tags"`
					Files          []string `json:"files"`
					VerifyCommands []string `json:"verifyCommands"`
				} `json:"generatedFeatures"`
				ConfigHints []struct {
					Key         string `json:"key"`
					Description string `json:"description"`
					Example     string `json:"example"`
				} `json:"configHints"`
				FeatureVerify []string `json:"featureVerify"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai new rag JSON: %v\n%s", err, stdout.String())
		}
		plugins := make([]string, 0, len(envelope.Data.GeneratedFeatures))
		for _, feature := range envelope.Data.GeneratedFeatures {
			plugins = append(plugins, feature.Plugin)
		}
		if got := strings.Join(plugins, ","); got != "observability,rag-agent" {
			t.Fatalf("generated feature plugins = %q, want observability,rag-agent", got)
		}
		if got := strings.Join(envelope.Data.FeatureVerify, ","); got != "go vet ./...,go test ./..." {
			t.Fatalf("rag feature verify = %q, want deduplicated feature checks", got)
		}
		configKeys := make([]string, 0, len(envelope.Data.ConfigHints))
		for _, hint := range envelope.Data.ConfigHints {
			configKeys = append(configKeys, hint.Key)
		}
		if got := strings.Join(configKeys, ","); got != "LOG_LEVEL,OTEL_EXPORTER_OTLP_ENDPOINT,LLM_PROVIDER,VECTOR_STORE" {
			t.Fatalf("rag config hints = %q, want observability and RAG hints", got)
		}
		for _, rel := range []string{
			filepath.Join("internal", "ai", "rag.go"),
			filepath.Join("internal", "observability", "observability.go"),
		} {
			if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
				t.Fatalf("ai new rag did not write %s: %v", rel, err)
			}
		}
	})

	t.Run("text apply reports generated features", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "postgres")
		var stdout bytes.Buffer
		args := []string{"ai", "new", "--template", "go-rest-clean-postgres", "--name", "orders", "--module", "example.com/orders", "--dir", outDir, "--apply"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new postgres text apply: %v", err)
		}
		got := stdout.String()
		for _, want := range []string{
			"feature=ci-docker",
			"feature=observability",
			"feature=openapi",
			"feature=postgres-repository",
			"dependencies=github.com/jackc/pgx/v5@latest",
			"configHint=DATABASE_URL",
			"verify=go vet ./...",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("ai new postgres text output missing %q:\n%s", want, got)
			}
		}
		if _, err := os.Stat(filepath.Join(outDir, "internal", "repository", "postgres.go")); err != nil {
			t.Fatalf("ai new postgres did not write repository feature: %v", err)
		}
	})
}

func TestAINewTextHelpAndManifestContract(t *testing.T) {
	t.Run("text dry-run prints plan", func(t *testing.T) {
		var stdout bytes.Buffer
		args := []string{"ai", "new", "create a cli", "--kind", "cli", "--name", "tool", "--module", "example.com/tool", "--dir", "tool", "--format", "text"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new text dry-run: %v", err)
		}
		got := stdout.String()
		if !strings.Contains(got, "template=go-cli-cobra") || !strings.Contains(got, "command=gofly new api tool") || !strings.Contains(got, "warning:") {
			t.Fatalf("ai new text dry-run output = %s", got)
		}
	})

	t.Run("help includes ai new contract", func(t *testing.T) {
		aiHelp := commandUsage("ai")
		newHelp := commandUsage("ai new")
		streamHelp := commandUsage("ai stream")
		manifestHelp := commandUsage("ai manifest")
		controlPlaneHelp := commandUsage("ai control-plane")
		for _, want := range []string{"ai new", "ai stream", "ai control-plane", "--apply", "--dry-run", "--template", "--verify", "--schema jsonschema", "--allow-failover", "--from-checksum", "--from-snapshot", "--watch", "--max-events"} {
			if !strings.Contains(aiHelp+newHelp+streamHelp+manifestHelp+controlPlaneHelp, want) {
				t.Fatalf("ai help output missing %q\nai help:\n%s\nai new help:\n%s\nai stream help:\n%s\nai manifest help:\n%s\nai control-plane help:\n%s", want, aiHelp, newHelp, streamHelp, manifestHelp, controlPlaneHelp)
			}
		}
	})

	t.Run("AI manifest commands have help and flag parity", func(t *testing.T) {
		manifest := buildAIToolManifest()
		longFlagPattern := regexp.MustCompile(`--[a-zA-Z0-9-]+`)
		for _, cmd := range manifest.Commands {
			if !strings.HasPrefix(cmd.Name, "ai ") {
				continue
			}
			t.Run(strings.ReplaceAll(cmd.Name, " ", "_"), func(t *testing.T) {
				help := commandUsage(cmd.Name)
				if strings.Contains(help, "gofly command help.") {
					t.Fatalf("%s uses fallback help instead of a dedicated AI help contract:\n%s", cmd.Name, help)
				}
				if !strings.Contains(help, cmd.Name) {
					t.Fatalf("%s help does not name the command:\n%s", cmd.Name, help)
				}
				seenFlags := map[string]bool{}
				for _, flagName := range longFlagPattern.FindAllString(cmd.Usage, -1) {
					if seenFlags[flagName] {
						continue
					}
					seenFlags[flagName] = true
					if !strings.Contains(help, flagName) {
						t.Fatalf("%s manifest usage flag %s missing from help\nusage: %s\nhelp:\n%s", cmd.Name, flagName, cmd.Usage, help)
					}
				}
			})
		}
	})

	t.Run("manifest exposes mutating ai new", func(t *testing.T) {
		manifest := buildAIToolManifest()
		for _, cmd := range manifest.Commands {
			if cmd.Name != "ai new" {
				continue
			}
			if !cmd.SupportsDryRun || !cmd.MutatesFilesystem || cmd.RiskLevel != "medium" || cmd.InputSchema.Properties["apply"].Type != "boolean" || cmd.InputSchema.Properties["verify"].Type != "boolean" {
				t.Fatalf("ai new manifest command = %+v, want dry-run mutating medium command", cmd)
			}
			return
		}
		t.Fatal("ai new not found in AI tool manifest")
	})

	t.Run("manifest exposes governed feature library contract", func(t *testing.T) {
		manifest := buildAIToolManifest()
		if len(manifest.Docs) == 0 || len(manifest.Examples) == 0 || len(manifest.VerifyCommands) == 0 {
			t.Fatalf("manifest links and verify commands = docs:%+v examples:%+v verify:%+v", manifest.Docs, manifest.Examples, manifest.VerifyCommands)
		}
		for _, want := range []string{"docs/concepts/ai-manifest.md", "docs/reference/cli-json-contracts.md"} {
			if !manifestLinksContainPath(manifest.Docs, want) {
				t.Fatalf("manifest docs missing %q: %+v", want, manifest.Docs)
			}
		}
		for _, want := range []string{"examples/README.md", "examples/ai-governed-service/README.md"} {
			if !manifestLinksContainPath(manifest.Examples, want) {
				t.Fatalf("manifest examples missing %q: %+v", want, manifest.Examples)
			}
		}
		for _, want := range []string{"make docs-check", "make doc-manifest-sync-check"} {
			if !commandContainsString(manifest.VerifyCommands, want) {
				t.Fatalf("manifest verify commands missing %q: %+v", want, manifest.VerifyCommands)
			}
		}
		controlPlane := manifest.ControlPlane
		if controlPlane.Package != "github.com/imajinyun/gofly/core/controlplane" || controlPlane.SnapshotVersion != "gofly-control-plane.v1" || controlPlane.SnapshotChecksum == "" {
			t.Fatalf("control plane manifest = %+v, want package, version and stable checksum", controlPlane)
		}
		if controlPlane.SchemaID != aiControlPlaneSchemaID || controlPlane.SchemaCommand != "gofly ai control-plane --schema jsonschema" || controlPlane.SchemaChecksum == "" || controlPlane.SchemaChecksum != aiControlPlaneJSONSchemaChecksum() {
			t.Fatalf("control plane schema contract = %+v, want schema id, command and stable checksum", controlPlane)
		}
		for _, want := range []string{"version", "checksum", "services", "configs", "policies", "metadata"} {
			if !commandContainsString(controlPlane.SnapshotFields, want) {
				t.Fatalf("control plane snapshot fields missing %q: %+v", want, controlPlane.SnapshotFields)
			}
		}
		if !strings.Contains(controlPlane.SecretBoundary, "secret values") || !commandContainsString(controlPlane.ProviderContract, "Load(context.Context) (Snapshot, error)") {
			t.Fatalf("control plane boundaries = %+v", controlPlane)
		}
		if !commandContainsString(controlPlane.Capabilities, "consumer action dispatcher for runtime config planner, routing model, governance gates and capability cache refresh hooks") {
			t.Fatalf("control plane capabilities = %+v, want runtime consumer dispatcher", controlPlane.Capabilities)
		}
		if !commandContainsString(controlPlane.Capabilities, "rpc policy runtime enforcement for client timeout, retry backoff with context cancellation, circuit breaker gates, balancer selection, load shedding, fallback and hedging") {
			t.Fatalf("control plane capabilities = %+v, want rpc policy runtime enforcement", controlPlane.Capabilities)
		}
		if !commandContainsString(controlPlane.Capabilities, "control-plane contributor for rpc policy runtime state, cache counts and enforcement capabilities") {
			t.Fatalf("control plane capabilities = %+v, want rpc policy runtime contributor", controlPlane.Capabilities)
		}
		if !commandContainsString(controlPlane.Capabilities, "native REST admin control-plane endpoint with pluggable runtime contributors and sanitized REST runtime snapshots") {
			t.Fatalf("control plane capabilities = %+v, want native REST admin control-plane contributor", controlPlane.Capabilities)
		}
		if !commandContainsString(controlPlane.Capabilities, "control-plane contributor for REST governance runtime cache counts across rate limiters, concurrency limiters and breakers") {
			t.Fatalf("control plane capabilities = %+v, want REST governance runtime cache contributor", controlPlane.Capabilities)
		}
		if !commandContainsString(controlPlane.Capabilities, "generated project control-plane contributors for scaffold contract, sanitized runtime config and governance policy snapshots") || controlPlane.DefaultMetadata["generated.project.contract"] != "available" {
			t.Fatalf("control plane capabilities/metadata = %+v/%+v, want generated project control-plane contract", controlPlane.Capabilities, controlPlane.DefaultMetadata)
		}
		if !commandContainsString(controlPlane.Capabilities, "ai new --apply --verify runs generated project control-plane snapshot assertions when the scaffold exposes a snapshot contract test") || controlPlane.DefaultMetadata["generated.project.verify.controlplane"] != "available" || controlPlane.DefaultMetadata["rest.runtime"] != "available" || controlPlane.DefaultMetadata["rest.governance.runtime"] != "available" {
			t.Fatalf("control plane capabilities/metadata = %+v/%+v, want REST runtime and generated verify control-plane metadata", controlPlane.Capabilities, controlPlane.DefaultMetadata)
		}
		if len(controlPlane.ConsumerActions) == 0 {
			t.Fatalf("control plane manifest missing consumer actions: %+v", controlPlane)
		}
		consumerActions := map[string]controlplane.SnapshotConsumerAction{}
		for _, action := range controlPlane.ConsumerActions {
			consumerActions[action.ChangeType] = action
		}
		if consumerActions["none"].Action != "skip" || consumerActions["policy-change"].Action != "reload-governance-gates" || !consumerActions["mixed-change"].RequiresFullReconcile {
			t.Fatalf("control plane consumer actions = %+v", controlPlane.ConsumerActions)
		}
		library := manifest.FeatureLibrary
		if !library.Deterministic || !library.AppliesUnderDirOnly || len(library.Plugins) == 0 {
			t.Fatalf("feature library manifest = %+v, want deterministic under-dir plugin contracts", library)
		}
		for _, want := range []string{"generatedFeatures", "dependencies", "configHints", "featureVerify", "nextActions"} {
			if !commandContainsString(library.ResultFields, want) {
				t.Fatalf("feature library result fields missing %q: %+v", want, library.ResultFields)
			}
		}
		for _, want := range []string{"go test ./...", "go vet ./...", "go mod tidy"} {
			if !commandContainsString(library.VerifyAllowlist, want) {
				t.Fatalf("feature library verify allowlist missing %q: %+v", want, library.VerifyAllowlist)
			}
		}
		if !strings.Contains(library.DependencyPolicy, "not automatically added") {
			t.Fatalf("feature library dependency policy = %q, want explicit dependency review boundary", library.DependencyPolicy)
		}
		for _, want := range []string{"auth-jwt", "postgres-repository", "redis-cache"} {
			if !commandContainsString(library.Features, want) {
				t.Fatalf("feature library features missing %q: %+v", want, library.Features)
			}
		}
		for _, want := range []string{"go-rest-minimal", "go-rag-service", "go-rpc-grpc"} {
			if !commandContainsString(library.Templates, want) {
				t.Fatalf("feature library templates missing %q: %+v", want, library.Templates)
			}
		}
		if library.TemplateVerification.CatalogField != "verifyE2EValidated" || library.TemplateVerification.MatrixTarget != "make test-generated-matrix" || !library.TemplateVerification.CIRequired || !library.TemplateVerification.ZeroSkipRequired {
			t.Fatalf("feature library template verification contract = %+v", library.TemplateVerification)
		}
		validated := strings.Join(library.TemplateVerification.ValidatedTemplates, ",")
		for _, want := range []string{"go-rest-minimal", "go-gateway", "go-ai-agent"} {
			if !strings.Contains(validated, want) {
				t.Fatalf("feature library validated templates missing %q: %s", want, validated)
			}
		}
		plugins := make([]string, 0, len(library.Plugins))
		for _, plugin := range library.Plugins {
			plugins = append(plugins, plugin.Name)
		}
		if got := strings.Join(plugins, ","); !strings.Contains(got, "postgres-repository") || !strings.Contains(got, "redis-cache") || plugins[0] != "auth-jwt" {
			t.Fatalf("feature library plugins = %q, want stable built-in feature contracts", got)
		}
	})

	t.Run("manifest exposes AI command output contracts", func(t *testing.T) {
		manifest := buildAIToolManifest()
		contracts := map[string]*aiOutputContract{}
		for _, cmd := range manifest.Commands {
			if strings.HasPrefix(cmd.Name, "ai ") {
				contracts[cmd.Name] = cmd.OutputContract
			}
		}
		for _, name := range []string{"ai manifest", "ai control-plane", "ai plan", "ai new", "ai complete", "ai stream", "ai doctor"} {
			contract := contracts[name]
			if contract == nil || contract.Mode == "" || !commandContainsString(contract.Envelope, "ok") || len(contract.EventFields) == 0 {
				t.Fatalf("%s output contract = %+v", name, contract)
			}
		}
		if contracts["ai manifest"].Semantics["schema"] == "" || contracts["ai control-plane"].Semantics["schema"] == "" || contracts["ai control-plane"].Semantics["determinism"] == "" || contracts["ai control-plane"].Semantics["diff"] == "" || contracts["ai control-plane"].Semantics["consumerAction"] == "" || contracts["ai new"].Semantics["verification"] == "" || contracts["ai doctor"].Semantics["secrets"] == "" {
			t.Fatalf("AI output contract semantics = manifest:%+v control-plane:%+v new:%+v doctor:%+v", contracts["ai manifest"], contracts["ai control-plane"], contracts["ai new"], contracts["ai doctor"])
		}
	})

	t.Run("ai manifest emits JSON schema contract", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "manifest", "--schema", "jsonschema"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai manifest schema: %v", err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Schema         string         `json:"$schema"`
				ID             string         `json:"$id"`
				Title          string         `json:"title"`
				SchemaChecksum string         `json:"xSchemaChecksum"`
				Properties     map[string]any `json:"properties"`
				Required       []string       `json:"required"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai manifest schema output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.manifest.schema" || envelope.Data.Schema != "https://json-schema.org/draft/2020-12/schema" || envelope.Data.Title != "gofly AI tool manifest" {
			t.Fatalf("ai manifest schema envelope = %+v", envelope)
		}
		for _, want := range []string{"schemaVersion", "docs", "examples", "verifyCommands", "commands", "controlPlane", "llmGovernance", "featureLibrary"} {
			if _, ok := envelope.Data.Properties[want]; !ok {
				t.Fatalf("ai manifest schema missing property %q: %+v", want, envelope.Data.Properties)
			}
		}
	})

	t.Run("ai control-plane emits JSON schema contract", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "control-plane", "--schema", "jsonschema"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai control-plane schema: %v", err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Schema         string         `json:"$schema"`
				ID             string         `json:"$id"`
				Title          string         `json:"title"`
				SchemaChecksum string         `json:"xSchemaChecksum"`
				Properties     map[string]any `json:"properties"`
				Required       []string       `json:"required"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai control-plane schema output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.control_plane.schema" || envelope.Data.Schema != "https://json-schema.org/draft/2020-12/schema" || envelope.Data.ID != aiControlPlaneSchemaID || envelope.Data.Title != "gofly AI control-plane contract" || envelope.Data.SchemaChecksum != aiControlPlaneJSONSchemaChecksum() {
			t.Fatalf("ai control-plane schema envelope = %+v", envelope)
		}
		for _, want := range []string{"snapshot", "diff", "consumerAction", "snapshotResult", "watchEvent"} {
			if _, ok := envelope.Data.Properties[want]; !ok {
				t.Fatalf("ai control-plane schema missing property %q: %+v", want, envelope.Data.Properties)
			}
		}
	})

	t.Run("ai control-plane emits deterministic JSON snapshot", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"ai", "control-plane", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai control-plane --json: %v", err)
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Source         string                              `json:"source"`
				Snapshot       controlplane.Snapshot               `json:"snapshot"`
				Diff           controlplane.SnapshotDiff           `json:"diff"`
				ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
				AgentGuidance  []string                            `json:"agentGuidance"`
				SecretBoundary string                              `json:"secretBoundary"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("decode ai control-plane output: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.control_plane" || envelope.Data.Source != "ai-manifest" {
			t.Fatalf("ai control-plane envelope = %+v", envelope)
		}
		if envelope.Data.Snapshot.Version != "gofly-control-plane.v1" || envelope.Data.Snapshot.Checksum == "" {
			t.Fatalf("ai control-plane snapshot = %+v, want version and checksum", envelope.Data.Snapshot)
		}
		if !envelope.Data.Diff.Changed || envelope.Data.Diff.ChangeType != "initial-snapshot" || envelope.Data.Diff.ToChecksum != envelope.Data.Snapshot.Checksum {
			t.Fatalf("ai control-plane diff = %+v, want initial snapshot diff", envelope.Data.Diff)
		}
		if envelope.Data.ConsumerAction.Action != "load-baseline" || !envelope.Data.ConsumerAction.RequiresFullReconcile || !commandContainsString(envelope.Data.ConsumerAction.Scopes, "policy") {
			t.Fatalf("ai control-plane consumer action = %+v, want load-baseline policy", envelope.Data.ConsumerAction)
		}
		if len(envelope.Data.AgentGuidance) == 0 || !strings.Contains(envelope.Data.SecretBoundary, "secret values") {
			t.Fatalf("ai control-plane guidance/boundary = %+v/%q", envelope.Data.AgentGuidance, envelope.Data.SecretBoundary)
		}
	})

	t.Run("ai control-plane compares from checksum", func(t *testing.T) {
		checksum := defaultAIControlPlaneSnapshot().StableChecksum()
		var stdout bytes.Buffer
		args := []string{"ai", "control-plane", "--from-checksum", checksum, "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai control-plane --from-checksum: %v", err)
		}
		var envelope struct {
			Data struct {
				Snapshot       controlplane.Snapshot               `json:"snapshot"`
				Diff           controlplane.SnapshotDiff           `json:"diff"`
				ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("decode ai control-plane diff output: %v\n%s", err, stdout.String())
		}
		if envelope.Data.Diff.Changed || envelope.Data.Diff.ChangeType != "none" || envelope.Data.Diff.FromChecksum != checksum || envelope.Data.Diff.ToChecksum != envelope.Data.Snapshot.Checksum {
			t.Fatalf("ai control-plane checksum diff = %+v", envelope.Data.Diff)
		}
		if envelope.Data.ConsumerAction.Action != "skip" || envelope.Data.ConsumerAction.RequiresFullReconcile {
			t.Fatalf("ai control-plane checksum consumer action = %+v, want skip", envelope.Data.ConsumerAction)
		}
	})

	t.Run("ai control-plane compares from snapshot file semantically", func(t *testing.T) {
		previous := defaultAIControlPlaneSnapshot()
		previous.Metadata = map[string]string{
			"config":     "available",
			"discovery":  "available",
			"governance": "available",
			"gateway":    "planned",
			"llm":        "planned",
			"tool":       "available",
		}
		data, err := json.Marshal(previous.WithChecksum())
		if err != nil {
			t.Fatalf("marshal previous snapshot: %v", err)
		}
		path := filepath.Join(t.TempDir(), "previous-control-plane.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write previous snapshot: %v", err)
		}

		var stdout bytes.Buffer
		args := []string{"ai", "control-plane", "--from-snapshot", path, "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai control-plane --from-snapshot: %v", err)
		}
		var envelope struct {
			Data struct {
				Diff           controlplane.SnapshotDiff           `json:"diff"`
				ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("decode ai control-plane snapshot diff output: %v\n%s", err, stdout.String())
		}
		if !envelope.Data.Diff.Changed || envelope.Data.Diff.ChangeType != "metadata-change" || !commandContainsString(envelope.Data.Diff.ChangedFields, "metadata") {
			t.Fatalf("ai control-plane snapshot diff = %+v, want metadata-change", envelope.Data.Diff)
		}
		if envelope.Data.ConsumerAction.Action != "refresh-capability-cache" || envelope.Data.ConsumerAction.RequiresFullReconcile {
			t.Fatalf("ai control-plane snapshot consumer action = %+v, want capability refresh", envelope.Data.ConsumerAction)
		}
	})

	t.Run("ai control-plane reads runtime source URL", func(t *testing.T) {
		wantSnapshot := controlplane.Snapshot{
			Version: controlplane.DefaultSnapshotVersion,
			Metadata: map[string]string{
				"rest.runtime": "available",
			},
			Configs: map[string]json.RawMessage{
				"rest.runtime": []byte(`{"service":"orders","address":"127.0.0.1:8080","adminEnabled":true}`),
			},
		}.WithChecksum()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/admin/control-plane" {
				t.Fatalf("runtime source request = %s %s", r.Method, r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer runtime-token" {
				t.Fatalf("runtime source authorization = %q, want bearer token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(wantSnapshot); err != nil {
				t.Fatalf("encode runtime source snapshot: %v", err)
			}
		}))
		defer server.Close()

		var stdout bytes.Buffer
		args := []string{"ai", "control-plane", "--source", server.URL + "/admin/control-plane", "--admin-token", "runtime-token", "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai control-plane --source: %v", err)
		}
		var envelope struct {
			Data struct {
				Source         string                              `json:"source"`
				Snapshot       controlplane.Snapshot               `json:"snapshot"`
				Diff           controlplane.SnapshotDiff           `json:"diff"`
				ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("decode ai control-plane source output: %v\n%s", err, stdout.String())
		}
		if envelope.Data.Source != server.URL+"/admin/control-plane" || envelope.Data.Snapshot.Checksum != wantSnapshot.Checksum {
			t.Fatalf("runtime source envelope = %+v, want source URL and runtime checksum", envelope.Data)
		}
		if envelope.Data.Snapshot.Metadata["rest.runtime"] != "available" || !json.Valid(envelope.Data.Snapshot.Configs["rest.runtime"]) {
			t.Fatalf("runtime source snapshot = %+v, want REST runtime contract", envelope.Data.Snapshot)
		}
		if envelope.Data.Diff.ChangeType != "initial-snapshot" || envelope.Data.ConsumerAction.Action != "load-baseline" {
			t.Fatalf("runtime source diff/action = %+v/%+v, want initial baseline", envelope.Data.Diff, envelope.Data.ConsumerAction)
		}
	})

	t.Run("ai control-plane rejects ambiguous baseline flags", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "previous-control-plane.json")
		if err := os.WriteFile(path, []byte(`{"version":"gofly-control-plane.v1"}`), 0o600); err != nil {
			t.Fatalf("write previous snapshot: %v", err)
		}
		err := ExecuteWithIO([]string{"ai", "control-plane", "--from-checksum", "old", "--from-snapshot", path, "--json"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("ai control-plane ambiguous baseline error = %v", err)
		}
	})

	t.Run("ai control-plane watch emits bounded JSON events", func(t *testing.T) {
		var stdout bytes.Buffer
		args := []string{"ai", "control-plane", "--watch", "--max-events", "1", "--timeout", "2s", "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai control-plane watch --json: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("ai control-plane watch output lines = %d, want 1\n%s", len(lines), stdout.String())
		}
		var envelope struct {
			OK      bool   `json:"ok"`
			Command string `json:"command"`
			Data    struct {
				Index          int                                 `json:"index"`
				Source         string                              `json:"source"`
				Snapshot       controlplane.Snapshot               `json:"snapshot"`
				Diff           controlplane.SnapshotDiff           `json:"diff"`
				ConsumerAction controlplane.SnapshotConsumerAction `json:"consumerAction"`
				Error          string                              `json:"error"`
				SecretBoundary string                              `json:"secretBoundary"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &envelope); err != nil {
			t.Fatalf("decode ai control-plane watch output: %v\n%s", err, stdout.String())
		}
		if !envelope.OK || envelope.Command != "ai.control_plane.event" {
			t.Fatalf("ai control-plane watch envelope = %+v", envelope)
		}
		if envelope.Data.Index != 0 || envelope.Data.Source != "ai-manifest" {
			t.Fatalf("ai control-plane watch index/source = %+v", envelope.Data)
		}
		if envelope.Data.Snapshot.Checksum == "" || envelope.Data.Snapshot.Version != "gofly-control-plane.v1" {
			t.Fatalf("ai control-plane watch snapshot = %+v", envelope.Data.Snapshot)
		}
		if !envelope.Data.Diff.Changed || envelope.Data.Diff.ChangeType != "mixed-change" || envelope.Data.Diff.ToChecksum != envelope.Data.Snapshot.Checksum {
			t.Fatalf("ai control-plane watch diff = %+v", envelope.Data.Diff)
		}
		if envelope.Data.ConsumerAction.Action != "full-reconcile" || !envelope.Data.ConsumerAction.RequiresFullReconcile || !commandContainsString(envelope.Data.ConsumerAction.Scopes, "metadata") {
			t.Fatalf("ai control-plane watch consumer action = %+v, want full reconcile", envelope.Data.ConsumerAction)
		}
		if envelope.Data.Error != "" || !strings.Contains(envelope.Data.SecretBoundary, "secret values") {
			t.Fatalf("ai control-plane watch error/boundary = %q/%q", envelope.Data.Error, envelope.Data.SecretBoundary)
		}
	})

	t.Run("ai control-plane watch reads runtime source URL", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			snapshot := controlplane.Snapshot{
				Version: controlplane.DefaultSnapshotVersion,
				Metadata: map[string]string{
					"rest.runtime": "available",
					"call":         strconv.Itoa(callCount),
				},
			}.WithChecksum()
			if err := json.NewEncoder(w).Encode(snapshot); err != nil {
				t.Fatalf("encode runtime watch snapshot: %v", err)
			}
		}))
		defer server.Close()

		var stdout bytes.Buffer
		args := []string{"ai", "control-plane", "--source", server.URL, "--watch", "--max-events", "1", "--timeout", "2s", "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai control-plane --source --watch: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) != 1 {
			t.Fatalf("ai control-plane source watch output lines = %d, want 1\n%s", len(lines), stdout.String())
		}
		var envelope struct {
			Data struct {
				Source   string                    `json:"source"`
				Snapshot controlplane.Snapshot     `json:"snapshot"`
				Diff     controlplane.SnapshotDiff `json:"diff"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &envelope); err != nil {
			t.Fatalf("decode ai control-plane source watch output: %v\n%s", err, stdout.String())
		}
		if envelope.Data.Source != server.URL || envelope.Data.Snapshot.Metadata["rest.runtime"] != "available" || envelope.Data.Diff.ChangeType != "mixed-change" {
			t.Fatalf("source watch envelope = %+v, want runtime source mixed initial event", envelope.Data)
		}
	})

	t.Run("ai control-plane watch rejects non-positive max-events", func(t *testing.T) {
		err := ExecuteWithIO([]string{"ai", "control-plane", "--watch", "--max-events", "0", "--json"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
		if err == nil || !strings.Contains(err.Error(), "--max-events must be positive") {
			t.Fatalf("ai control-plane watch max-events error = %v", err)
		}
	})
}

func TestAIProjectApplyHelperErrorAndTextBranches(t *testing.T) {
	t.Run("text apply reports command and next actions", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "text-apply")
		var stdout bytes.Buffer
		args := []string{"ai", "new", "--template", "go-rest-minimal", "--name", "hello", "--module", "example.com/hello", "--dir", outDir, "--apply"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new text apply: %v", err)
		}
		got := stdout.String()
		for _, want := range []string{"applied template=go-rest-minimal", "command=gofly new api hello", "warning:", "next: cd " + outDir} {
			if !strings.Contains(got, want) {
				t.Fatalf("ai new text apply output missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("text apply reports feature governance contract", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "text-contract")
		var stdout bytes.Buffer
		args := []string{"ai", "new", "--template", "go-rest-clean-postgres", "--name", "orders", "--module", "example.com/orders", "--dir", outDir, "--apply"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new text contract apply: %v", err)
		}
		got := stdout.String()
		for _, want := range []string{
			"feature=postgres-repository",
			"dependencies=github.com/jackc/pgx/v5@latest",
			"configHint=DATABASE_URL",
			"verify=go test ./...",
			"next: review feature dependencies: go get github.com/jackc/pgx/v5@latest",
			"next: configure DATABASE_URL:",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("ai new text apply output missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("json apply reports aggregated feature governance fields", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "json-contract")
		var stdout bytes.Buffer
		args := []string{"ai", "new", "--template", "go-rest-clean-postgres", "--name", "orders", "--module", "example.com/orders", "--dir", outDir, "--apply", "--json"}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai new json contract apply: %v", err)
		}
		var envelope struct {
			Data struct {
				Dependencies  []string `json:"dependencies"`
				FeatureVerify []string `json:"featureVerify"`
				ConfigHints   []struct {
					Key string `json:"key"`
				} `json:"configHints"`
				GeneratedFeatures []struct {
					Plugin       string   `json:"plugin"`
					Dependencies []string `json:"dependencies"`
				} `json:"generatedFeatures"`
				NextActions []string `json:"nextActions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("ai new json contract output is not valid JSON: %v\n%s", err, stdout.String())
		}
		if got := strings.Join(envelope.Data.Dependencies, ","); got != "github.com/jackc/pgx/v5@latest" {
			t.Fatalf("ai new dependencies = %q, want pgx declaration", got)
		}
		if got := strings.Join(envelope.Data.FeatureVerify, ","); got != "go test ./...,go vet ./..." {
			t.Fatalf("ai new feature verify = %q, want deduplicated feature verification", got)
		}
		configKeys := make([]string, 0, len(envelope.Data.ConfigHints))
		for _, hint := range envelope.Data.ConfigHints {
			configKeys = append(configKeys, hint.Key)
		}
		if got := strings.Join(configKeys, ","); got != "LOG_LEVEL,OTEL_EXPORTER_OTLP_ENDPOINT,DATABASE_URL" {
			t.Fatalf("ai new config hints = %q, want observability and postgres hints", got)
		}
		plugins := make([]string, 0, len(envelope.Data.GeneratedFeatures))
		for _, feature := range envelope.Data.GeneratedFeatures {
			plugins = append(plugins, feature.Plugin)
			if feature.Plugin == "postgres-repository" && strings.Join(feature.Dependencies, ",") != "github.com/jackc/pgx/v5@latest" {
				t.Fatalf("postgres feature dependencies = %+v, want pgx declaration", feature.Dependencies)
			}
		}
		if got := strings.Join(plugins, ","); got != "ci-docker,observability,openapi,postgres-repository" {
			t.Fatalf("ai new generated features = %q, want stable feature plugin order", got)
		}
		if !commandContainsString(envelope.Data.NextActions, "review feature dependencies: go get github.com/jackc/pgx/v5@latest") {
			t.Fatalf("ai new next actions = %+v, want dependency review guidance", envelope.Data.NextActions)
		}
		goMod, err := os.ReadFile(filepath.Join(outDir, "go.mod"))
		if err != nil {
			t.Fatalf("read generated go.mod: %v", err)
		}
		if strings.Contains(string(goMod), "github.com/jackc/pgx") {
			t.Fatalf("generated go.mod unexpectedly auto-added feature dependency; dependencies must be reported for explicit review:\n%s", goMod)
		}
	})

	t.Run("apply input validation reports missing fields", func(t *testing.T) {
		base := aiProjectPlan{Template: generator.ProjectTemplate{ID: "go-rest-minimal", Command: "gofly new api <name> --module <module> --dir <dir>"}}
		tests := []struct {
			name string
			plan aiProjectPlan
			want string
		}{
			{name: "missing template", plan: aiProjectPlan{Command: "gofly new api hello --module example.com/hello --dir hello"}, want: "project template is required"},
			{name: "missing name", plan: aiProjectPlan{Template: base.Template, Command: "gofly new api --module example.com/hello --dir hello"}, want: "name is required"},
			{name: "missing module", plan: aiProjectPlan{Template: base.Template, Command: "gofly new api hello --dir hello"}, want: "module is required"},
			{name: "missing dir", plan: aiProjectPlan{Template: base.Template, Command: "gofly new api hello --module example.com/hello"}, want: "dir is required"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := validateAIProjectApplyInputs(tt.plan)
				if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("validateAIProjectApplyInputs error = %v, want %q", err, tt.want)
				}
			})
		}
	})

	t.Run("apply command dispatch rejects incomplete and unsupported commands", func(t *testing.T) {
		for _, args := range [][]string{{"new"}, {"plugin", "run", "tool"}} {
			if err := runAIProjectApplyCommand(args); !errors.Is(err, errUsage) {
				t.Fatalf("runAIProjectApplyCommand(%v) error = %v, want errUsage", args, err)
			}
		}
	})

	t.Run("apply plan propagates unsupported command errors", func(t *testing.T) {
		plan := aiProjectPlan{
			Template: generator.ProjectTemplate{ID: "unsupported", Command: "gofly plugin run <name> --module <module> --dir <dir>"},
			Command:  "gofly plugin run hello --module example.com/hello --dir hello",
		}
		if _, err := applyAIProjectPlan(plan, aiProjectApplyOptions{}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "unsupported scaffold command") {
			t.Fatalf("applyAIProjectPlan unsupported error = %v", err)
		}
	})
}

func TestCommandConfigFeaturePluginCoverageBuffer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	serviceDir := t.TempDir()

	t.Run("config command text dry-run and apply branches", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"config", "init", "--dir", serviceDir, "--name", "orders", "--module", "example.com/orders", "--style", "production", "--dry-run"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("config init dry-run: %v", err)
		}
		if got := stdout.String(); !strings.Contains(got, "config.init") || !strings.Contains(got, "write-config") {
			t.Fatalf("config init dry-run output missing plan details:\n%s", got)
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"config", "init", "--dir", serviceDir, "--name", "orders", "--module", "example.com/orders", "--style", "production"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("config init apply: %v", err)
		}
		if !strings.Contains(stdout.String(), "wrote gofly config") {
			t.Fatalf("config init output = %q, want write confirmation", stdout.String())
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"config", "show", "--dir", serviceDir}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("config show: %v", err)
		}
		if !strings.Contains(stdout.String(), "orders") {
			t.Fatalf("config show output = %q, want service name", stdout.String())
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"config", "get", "style", "--dir", serviceDir}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("config get style: %v", err)
		}
		if strings.TrimSpace(stdout.String()) != "production" {
			t.Fatalf("config get style = %q, want production", stdout.String())
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"config", "set", "features", "", "--dir", serviceDir, "--dry-run"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("config set empty features dry-run: %v", err)
		}
		if got := stdout.String(); !strings.Contains(got, "config.set") || !strings.Contains(got, "update-config") {
			t.Fatalf("config set dry-run output missing plan details:\n%s", got)
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"config", "clean", "--dir", serviceDir, "--dry-run"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("config clean dry-run: %v", err)
		}
		if !strings.Contains(stdout.String(), "remove-config") {
			t.Fatalf("config clean dry-run output = %q, want remove-config", stdout.String())
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"config", "clean", "--dir", serviceDir}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("config clean apply: %v", err)
		}
		if !strings.Contains(stdout.String(), "removed gofly config") {
			t.Fatalf("config clean output = %q, want removal confirmation", stdout.String())
		}

		for _, args := range [][]string{
			{"config"},
			{"config", "get", "--dir", serviceDir},
			{"config", "set", "--dir", serviceDir},
			{"config", "unknown", "--dir", serviceDir},
		} {
			err := ExecuteWithIO(args, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
			if !errors.Is(err, errUsage) {
				t.Fatalf("ExecuteWithIO(%v) error = %v, want errUsage", args, err)
			}
		}
	})

	t.Run("feature command text and json branches", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"feature", "list"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("feature list text: %v", err)
		}
		if !strings.Contains(stdout.String(), "http-compat") {
			t.Fatalf("feature list output = %q, want registered features", stdout.String())
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"feature", "run", "http-compat", "--name", "orders", "--module", "example.com/orders", "--dir", serviceDir}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("feature run text: %v", err)
		}
		if got := stdout.String(); !strings.Contains(got, "# file:") || !strings.Contains(got, "internal") {
			t.Fatalf("feature run text output missing file preview:\n%s", got)
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"feature", "run", "http-compat", "--features", "rpc-compat", "--name", "orders", "--module", "example.com/orders", "--dir", serviceDir, "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("feature run json: %v", err)
		}
		if got := stdout.String(); !strings.Contains(got, `"feature.run"`) || !strings.Contains(got, `"features"`) || !strings.Contains(got, `"data"`) {
			t.Fatalf("feature run json output missing preview fields:\n%s", got)
		}

		err := ExecuteWithIO([]string{"feature", "run", "--json"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
		if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "feature run <feature-name>") {
			t.Fatalf("feature run missing feature error = %v, want usage", err)
		}
		for _, args := range [][]string{
			{"feature"},
			{"feature", "unknown"},
		} {
			err := ExecuteWithIO(args, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
			if !errors.Is(err, errUsage) {
				t.Fatalf("ExecuteWithIO(%v) error = %v, want errUsage", args, err)
			}
		}
		if err := ExecuteWithIO([]string{"feature", "run", "missing-feature"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("feature run missing feature error = %v, want not registered", err)
		}
	})

	t.Run("plugin command registry and dry-run branches", func(t *testing.T) {
		registryPath := filepath.Join(t.TempDir(), "plugins.json")
		registryJSON := `{
  "version":"v1",
  "plugins":[{
    "name":"auth-jwt",
    "remote":"https://example.com/auth-jwt",
    "version":"v0.1.0",
    "protocol":"1",
    "checksum":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "source":"https://github.com/example/gofly-auth-jwt",
    "description":"JWT auth generator",
    "tags":["auth","jwt"],
    "manifest":{"name":"auth-jwt","version":"v0.1.0","compatibleVersions":["1"],"capabilities":["generate:file"],"permissions":["filesystem:write-relative"]}
  }]
}`
		if err := os.WriteFile(registryPath, []byte(registryJSON), 0o600); err != nil {
			t.Fatalf("write plugin registry: %v", err)
		}

		var stdout bytes.Buffer
		if err := ExecuteWithIO([]string{"plugin", "list", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("plugin list json: %v", err)
		}
		if got := stdout.String(); !strings.Contains(got, `"internal"`) || !strings.Contains(got, `"installed"`) {
			t.Fatalf("plugin list json output = %q, want internal and installed lists", got)
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"plugin", "search", "--registry", registryPath, "auth"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("plugin search text: %v", err)
		}
		if !strings.Contains(stdout.String(), "auth-jwt@v0.1.0") {
			t.Fatalf("plugin search output = %q, want auth-jwt match", stdout.String())
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"plugin", "search", "--registry", registryPath, "missing"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("plugin search no matches: %v", err)
		}
		if !strings.Contains(stdout.String(), "no plugins matched") {
			t.Fatalf("plugin search no-match output = %q", stdout.String())
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"plugin", "run", "--remote", "https://example.com/auth-jwt@v0.1.0", "--name", "orders", "--module", "example.com/orders", "--dir", serviceDir, "--dry-run", "--json"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("plugin run remote dry-run json: %v", err)
		}
		if got := stdout.String(); !strings.Contains(got, `"plugin.run"`) || !strings.Contains(got, "resolve-plugins") || !strings.Contains(got, "dry-run does not download") {
			t.Fatalf("plugin run dry-run json output missing governance plan:\n%s", got)
		}

		stdout.Reset()
		if err := ExecuteWithIO([]string{"plugin", "run", "local-plugin", "--go-plugin", "./plugins", "--name", "orders", "--module", "example.com/orders", "--dir", serviceDir, "--plan"}, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("plugin run go-plugin plan: %v", err)
		}
		if got := stdout.String(); !strings.Contains(got, "local-plugin") || !strings.Contains(got, "./plugins") || !strings.Contains(got, "execute-plugins") {
			t.Fatalf("plugin run go-plugin plan output missing inputs/actions:\n%s", got)
		}

		err := ExecuteWithIO([]string{"plugin", "search"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
		if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "--registry") {
			t.Fatalf("plugin search missing registry error = %v, want usage", err)
		}
		for _, args := range [][]string{
			{"plugin"},
			{"plugin", "install"},
			{"plugin", "uninstall"},
			{"plugin", "run"},
			{"plugin", "unknown"},
		} {
			err := ExecuteWithIO(args, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
			if !errors.Is(err, errUsage) {
				t.Fatalf("ExecuteWithIO(%v) error = %v, want errUsage", args, err)
			}
		}
	})
}

func TestAIControlPlaneTextAndErrorCoverageBuffer(t *testing.T) {
	t.Run("text snapshot prints metadata guidance and checksum diff", func(t *testing.T) {
		checksum := defaultAIControlPlaneSnapshot().StableChecksum()
		var stdout bytes.Buffer
		args := []string{"ai", "control-plane", "--format", "text", "--from-checksum", checksum}
		if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
			t.Fatalf("ai control-plane text: %v", err)
		}
		got := stdout.String()
		for _, want := range []string{"gofly AI control-plane snapshot", "diff changed=false", "consumerAction=skip", "metadata.", "next:"} {
			if !strings.Contains(got, want) {
				t.Fatalf("ai control-plane text output missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("invalid flags and provider source validation", func(t *testing.T) {
		cases := []struct {
			name string
			args []string
			want string
		}{
			{name: "bad format", args: []string{"ai", "control-plane", "--format", "xml"}, want: "unsupported --format"},
			{name: "bad schema", args: []string{"ai", "control-plane", "--schema", "openapi"}, want: "unsupported --schema"},
			{name: "relative source", args: []string{"ai", "control-plane", "--source", "/admin/control-plane"}, want: "absolute http(s) URL"},
			{name: "bad scheme", args: []string{"ai", "control-plane", "--source", "ftp://example.com/control-plane.json"}, want: "only http and https"},
			{name: "bad timeout", args: []string{"ai", "control-plane", "--watch", "--timeout", "0s"}, want: "positive duration"},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				err := ExecuteWithIO(tt.args, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
				if !errors.Is(err, errUsage) || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("ExecuteWithIO(%v) error = %v, want %q", tt.args, err, tt.want)
				}
			})
		}
	})

	t.Run("runtime source error branches", func(t *testing.T) {
		badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not-json`))
		}))
		defer badJSONServer.Close()
		if err := ExecuteWithIO([]string{"ai", "control-plane", "--source", badJSONServer.URL, "--json"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "decode control-plane source") {
			t.Fatalf("bad json source error = %v, want decode control-plane source", err)
		}

		statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		defer statusServer.Close()
		if err := ExecuteWithIO([]string{"ai", "control-plane", "--source", statusServer.URL, "--json"}, IOStreams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}); err == nil || !strings.Contains(err.Error(), "status 403") {
			t.Fatalf("status source error = %v, want status 403", err)
		}
	})
}

func TestConfigFieldCoverageBuffer(t *testing.T) {
	cfg := &generator.Config{}
	setters := map[string]string{
		"service":                 "orders",
		"module":                  "example.com/orders",
		"style":                   "production",
		"templates":               "templates",
		"go-version":              "1.26",
		"features":                "http-compat,rpc-compat",
		"rpc.plugins":             "kitex",
		"rpc.transport":           "grpc",
		"rpc.profile":             "kitex-compatible",
		"api.plugins":             "auth",
		"model.style":             "gorm",
		"model.ignore-columns":    "password,secret",
		"model.types-map":         "uuid=string,jsonb=[]byte",
		"model.cache":             "true",
		"model.strict":            "true",
		"llm.provider":            "noop",
		"llm.model":               "noop-model",
		"llm.max-input-tokens":    "11",
		"llm.max-output-tokens":   "12",
		"llm.max-total-tokens":    "23",
		"llm.rate-limit":          "3",
		"llm.rate-burst":          "5",
		"llm.timeout":             "2s",
		"custom.extra.governance": "enabled",
	}
	for key, value := range setters {
		if err := setConfigField(cfg, key, value); err != nil {
			t.Fatalf("setConfigField(%q): %v", key, err)
		}
	}

	getters := map[string]string{
		"service":                 "orders",
		"module":                  "example.com/orders",
		"style":                   "production",
		"templates":               "templates",
		"go-version":              "1.26",
		"features":                "http-compat,rpc-compat",
		"rpc.plugins":             "kitex",
		"rpc.transport":           "grpc",
		"rpc.profile":             "kitex-compatible",
		"api.plugins":             "auth",
		"model.style":             "gorm",
		"model.ignore-columns":    "password,secret",
		"model.cache":             "true",
		"model.strict":            "true",
		"llm.provider":            "noop",
		"llm.model":               "noop-model",
		"llm.max-input-tokens":    "11",
		"llm.max-output-tokens":   "12",
		"llm.max-total-tokens":    "23",
		"llm.rate-limit":          "3",
		"llm.rate-burst":          "5",
		"llm.timeout":             "2s",
		"custom.extra.governance": "enabled",
	}
	for key, want := range getters {
		if got := getConfigField(cfg, key); got != want {
			t.Fatalf("getConfigField(%q) = %q, want %q", key, got, want)
		}
	}
	if got := getConfigField(&generator.Config{}, "llm.max-total-tokens"); got != "0" {
		t.Fatalf("nil LLM max-total getter = %q, want 0", got)
	}
	for _, key := range []string{"rpc.plugins", "rpc.transport", "rpc.profile", "api.plugins", "model.style", "model.ignore-columns", "model.types-map", "llm.provider", "llm.model", "llm.timeout", "unknown"} {
		if got := getConfigField(&generator.Config{}, key); got != "" {
			t.Fatalf("nil config getConfigField(%q) = %q, want empty", key, got)
		}
	}
	if err := setConfigField(&generator.Config{}, "llm.max-input-tokens", "-1"); !errors.Is(err, errUsage) {
		t.Fatalf("set negative llm token budget error = %v, want errUsage", err)
	}
	if err := setConfigField(&generator.Config{}, "llm.timeout", "bad-duration"); !errors.Is(err, errUsage) {
		t.Fatalf("set bad llm timeout error = %v, want errUsage", err)
	}
}

func TestAINewGeneratedArtifactsAreDeterministicAndIdempotent(t *testing.T) {
	firstDir := filepath.Join(t.TempDir(), "first")
	secondDir := filepath.Join(t.TempDir(), "second")
	first := applyAINewAndSnapshot(t, firstDir)
	second := applyAINewAndSnapshot(t, secondDir)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("ai new generated artifacts are not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}

	reapplied := applyAINewAndSnapshot(t, firstDir)
	if !reflect.DeepEqual(first, reapplied) {
		t.Fatalf("ai new generated artifacts are not idempotent:\nfirst=%+v\nreapplied=%+v", first, reapplied)
	}
}

func TestAIProjectFeatureAggregationBoundaries(t *testing.T) {
	features := []generator.ProjectFeatureResult{
		{
			Plugin:         "one",
			Dependencies:   []string{" github.com/example/one@v1.0.0 ", "github.com/example/two@latest"},
			VerifyCommands: []string{"go test ./...", "go vet ./..."},
			ConfigHints: []generator.ConfigHint{
				{Key: "DATABASE_URL", Description: "database DSN", Example: "postgres://localhost/db"},
				{Key: "", Description: "ignored"},
			},
		},
		{
			Plugin:         "two",
			Dependencies:   []string{"github.com/example/ONE@v1.0.0", "", "github.com/example/three@latest"},
			VerifyCommands: []string{"GO TEST ./...", "", "go mod tidy"},
			ConfigHints: []generator.ConfigHint{
				{Key: "database_url", Description: "duplicate should be ignored"},
				{Key: "REDIS_ADDR", Description: "redis address"},
			},
		},
	}
	deps, hints, verify := aggregateProjectFeatureContract(features)
	if got := strings.Join(deps, ","); got != "github.com/example/one@v1.0.0,github.com/example/two@latest,github.com/example/three@latest" {
		t.Fatalf("aggregate dependencies = %q, want trimmed case-insensitive unique order", got)
	}
	if got := strings.Join(verify, ","); got != "go test ./...,go vet ./...,go mod tidy" {
		t.Fatalf("aggregate verify = %q, want trimmed case-insensitive unique order", got)
	}
	if len(hints) != 2 || hints[0].Key != "DATABASE_URL" || hints[1].Key != "REDIS_ADDR" {
		t.Fatalf("aggregate config hints = %+v, want non-empty case-insensitive unique hints", hints)
	}

	if next := aiProjectApplyNextActions("/tmp/out", nil, nil, nil, false, false); strings.Join(next, ",") != "cd /tmp/out" {
		t.Fatalf("next actions without verify = %+v, want only cd", next)
	}
	failedNext := aiProjectApplyNextActions("/tmp/out", []string{"go test ./..."}, nil, nil, true, false)
	if !commandContainsString(failedNext, "fix failed verification output, then rerun: go test ./...") {
		t.Fatalf("failed verify next actions = %+v, want rerun guidance", failedNext)
	}
}

func TestAINewGeneratedProjectVerificationMatrix(t *testing.T) {
	withFrameworkPath(t, func() {
		for _, tt := range []struct {
			name                  string
			template              string
			wantVerify            []string
			wantFiles             []string
			wantGeneratedFeatures []string
		}{
			{
				name:                  "hello",
				template:              "go-rest-minimal",
				wantVerify:            []string{"gofmt", "go mod tidy", "go test ./...", "go vet ./...", "control-plane snapshot"},
				wantFiles:             []string{"go.mod", filepath.Join("cmd", "hello", "main.go"), filepath.Join("docs", "openapi.yaml"), filepath.Join("internal", "observability", "observability.go")},
				wantGeneratedFeatures: []string{"observability", "openapi"},
			},
			{
				name:                  "orders",
				template:              "go-rest-clean-postgres",
				wantVerify:            []string{"gofmt", "go mod tidy", "go test ./...", "go vet ./...", "control-plane snapshot"},
				wantFiles:             []string{"go.mod", filepath.Join("cmd", "orders", "main.go"), filepath.Join("internal", "repository", "postgres.go"), filepath.Join("migrations", "000001_init.sql")},
				wantGeneratedFeatures: []string{"ci-docker", "observability", "openapi", "postgres-repository"},
			},
			{
				name:                  "greeter",
				template:              "go-rpc-grpc",
				wantVerify:            []string{"gofmt", "go mod tidy", "go test ./...", "go vet ./...", "control-plane snapshot"},
				wantFiles:             []string{"go.mod", filepath.Join("cmd", "greeter", "main.go"), filepath.Join("internal", "observability", "observability.go"), "Dockerfile"},
				wantGeneratedFeatures: []string{"ci-docker", "observability"},
			},
			{
				name:                  "worker",
				template:              "go-worker-mq",
				wantVerify:            []string{"gofmt", "go mod tidy", "go test ./...", "go vet ./...", "control-plane snapshot"},
				wantFiles:             []string{"go.mod", filepath.Join("cmd", "worker", "main.go"), filepath.Join("internal", "observability", "observability.go"), filepath.Join("internal", "worker", "worker.go")},
				wantGeneratedFeatures: []string{"observability", "queue-worker"},
			},
			{
				name:                  "tool",
				template:              "go-cli-cobra",
				wantVerify:            []string{"gofmt", "go mod tidy", "go test ./...", "control-plane snapshot"},
				wantFiles:             []string{"go.mod", filepath.Join("cmd", "tool", "main.go"), filepath.Join("internal", "config", "config.go"), filepath.Join("internal", "service", "ping.go")},
				wantGeneratedFeatures: nil,
			},
			{
				name:                  "edge",
				template:              "go-gateway",
				wantVerify:            []string{"gofmt", "go mod tidy", "go test ./...", "go vet ./..."},
				wantFiles:             []string{"go.mod", filepath.Join("cmd", "edge", "main.go"), filepath.Join("internal", "routes", "routes.go"), filepath.Join("internal", "observability", "observability.go")},
				wantGeneratedFeatures: []string{"observability"},
			},
			{
				name:                  "rag",
				template:              "go-rag-service",
				wantVerify:            []string{"gofmt", "go mod tidy", "go test ./...", "go vet ./...", "control-plane snapshot"},
				wantFiles:             []string{"go.mod", filepath.Join("cmd", "rag", "main.go"), filepath.Join("internal", "ai", "rag.go"), filepath.Join("internal", "observability", "observability.go")},
				wantGeneratedFeatures: []string{"observability", "rag-agent"},
			},
			{
				name:                  "agent",
				template:              "go-ai-agent",
				wantVerify:            []string{"gofmt", "go mod tidy", "go test ./...", "gofly ai doctor --json", "go vet ./...", "control-plane snapshot"},
				wantFiles:             []string{"go.mod", filepath.Join("cmd", "agent", "main.go"), filepath.Join("internal", "ai", "rag.go"), filepath.Join("internal", "observability", "observability.go")},
				wantGeneratedFeatures: []string{"observability", "rag-agent"},
			},
		} {
			t.Run(tt.template, func(t *testing.T) {
				outDir := filepath.Join(t.TempDir(), tt.name)
				tmpl, ok := generator.GetProjectTemplate(tt.template)
				if !ok {
					t.Fatalf("project template %q not found", tt.template)
				}
				var stdout bytes.Buffer
				args := []string{"ai", "new", "--template", tt.template, "--name", tt.name, "--module", "example.com/" + tt.name, "--dir", outDir, "--apply", "--verify", "--verify-timeout", "2m", "--json"}
				if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
					t.Fatalf("ai new %s --verify: %v\n%s", tt.template, err, stdout.String())
				}
				var envelope struct {
					Data struct {
						VerifyRan         bool     `json:"verifyRan"`
						VerifyPassed      bool     `json:"verifyPassed"`
						Verify            []string `json:"verify"`
						GeneratedFeatures []struct {
							Plugin string `json:"plugin"`
						} `json:"generatedFeatures"`
						Verification []struct {
							Command string `json:"command"`
							Status  string `json:"status"`
							Output  string `json:"output"`
							Error   string `json:"error"`
						} `json:"verification"`
					} `json:"data"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatalf("ai new %s JSON: %v\n%s", tt.template, err, stdout.String())
				}
				if !envelope.Data.VerifyRan || !envelope.Data.VerifyPassed {
					t.Fatalf("ai new %s verification flags = ran:%v passed:%v\n%s", tt.template, envelope.Data.VerifyRan, envelope.Data.VerifyPassed, stdout.String())
				}
				declaredVerify := tt.wantVerify
				if len(declaredVerify) > 0 && declaredVerify[len(declaredVerify)-1] == "control-plane snapshot" {
					declaredVerify = declaredVerify[:len(declaredVerify)-1]
				}
				if got := strings.Join(envelope.Data.Verify, ","); got != strings.Join(declaredVerify, ",") {
					t.Fatalf("ai new %s verify commands = %q, want %q", tt.template, got, strings.Join(declaredVerify, ","))
				}
				verification := make([]string, 0, len(envelope.Data.Verification))
				for _, check := range envelope.Data.Verification {
					verification = append(verification, check.Command)
					if check.Status != "passed" {
						t.Fatalf("ai new %s verification check = %+v\n%s", tt.template, check, stdout.String())
					}
				}
				if got := strings.Join(verification, ","); got != strings.Join(tt.wantVerify, ",") {
					t.Fatalf("ai new %s executed verification = %q, want %q", tt.template, got, strings.Join(tt.wantVerify, ","))
				}
				plugins := make([]string, 0, len(envelope.Data.GeneratedFeatures))
				for _, feature := range envelope.Data.GeneratedFeatures {
					plugins = append(plugins, feature.Plugin)
				}
				if got := strings.Join(plugins, ","); got != strings.Join(tt.wantGeneratedFeatures, ",") {
					t.Fatalf("ai new %s generated features = %q, want %q", tt.template, got, strings.Join(tt.wantGeneratedFeatures, ","))
				}
				for _, file := range tt.wantFiles {
					if _, err := os.Stat(filepath.Join(outDir, file)); err != nil {
						t.Fatalf("ai new %s missing generated file %s: %v", tt.template, file, err)
					}
				}
				for _, file := range tmpl.Files {
					file = strings.ReplaceAll(file, "<name>", tt.name)
					if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(file))); err != nil {
						t.Fatalf("ai new %s catalog declared file %s was not generated: %v", tt.template, file, err)
					}
				}
			})
		}
	})
}

func TestNewServiceGeneratedProjectSmokeMatrix(t *testing.T) {
	withFrameworkPath(t, func() {
		outDir := filepath.Join(t.TempDir(), "orders")
		if err := Execute([]string{"new", "service", "orders", "--module", "example.com/orders", "--dir", outDir}); err != nil {
			t.Fatalf("new service golden path: %v", err)
		}
		for _, rel := range []string{
			"go.mod",
			filepath.Join("cmd", "orders", "main.go"),
			filepath.Join("internal", "smoke", "service_smoke_test.go"),
			filepath.Join("internal", "admin", "admin.go"),
			filepath.Join("internal", "discovery", "registry.go"),
		} {
			if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
				t.Fatalf("new service missing generated file %s: %v", rel, err)
			}
		}
		results, passed, err := runAIProjectVerification(outDir, []string{"go mod tidy", "go test ./..."}, 3*time.Minute)
		if err != nil {
			t.Fatalf("new service generated project verification: %v", err)
		}
		if !passed {
			t.Fatalf("new service generated project verification failed: %+v", results)
		}
	})
}

func TestNewServiceContractInputMatrix(t *testing.T) {
	withFrameworkPath(t, func() {
		for _, tt := range []struct {
			name      string
			flag      string
			ext       string
			contract  string
			wantFiles []string
		}{
			{
				name: "api-first-api",
				flag: "--api",
				ext:  ".api",
				contract: `type PingResp {
  Message string
}

service user-api {
  @handler ping
  get /ping returns (PingResp)
}
`,
				wantFiles: []string{filepath.Join("internal", "api", "v1", "user_api", "routes.go"), filepath.Join("internal", "api", "v1", "user_api", "routes_test.go")},
			},
			{
				name:      "api-first-openapi",
				flag:      "--openapi",
				ext:       ".json",
				contract:  `{"openapi":"3.0.3","info":{"title":"orders","version":"1.0.0"},"paths":{"/orders":{"get":{"operationId":"ListOrders","responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/OrderResp"}}}}}}}},"components":{"schemas":{"OrderResp":{"type":"object","properties":{"id":{"type":"string"}}}}}}`,
				wantFiles: []string{"orders.api", filepath.Join("internal", "api", "v1", "orders", "routes.go")},
			},
			{
				name: "rpc-first-proto",
				flag: "--proto",
				ext:  ".proto",
				contract: `syntax = "proto3";
package demo;
message HelloReq { string name = 1; }
message HelloResp { string message = 1; }
service Greeter { rpc SayHello (HelloReq) returns (HelloResp); }
`,
				wantFiles: []string{"orders.proto", filepath.Join("internal", "rpc", "orders.gofly.go")},
			},
			{
				name: "rpc-first-thrift",
				flag: "--thrift",
				ext:  ".thrift",
				contract: `namespace go example.com/orders
struct HelloReq {
  1: string name
}
struct HelloResp {
  1: string message
}
service Greeter {
  HelloResp SayHello(1: HelloReq req)
}
`,
				wantFiles: []string{"orders.proto", filepath.Join("internal", "rpc", "orders.gofly.go")},
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				tmp := t.TempDir()
				contractPath := filepath.Join(tmp, "contract"+tt.ext)
				if err := os.WriteFile(contractPath, []byte(tt.contract), 0o600); err != nil {
					t.Fatal(err)
				}
				outDir := filepath.Join(tmp, "orders")
				if err := Execute([]string{"new", "service", "orders", "--module", "example.com/orders", "--dir", outDir, tt.flag, contractPath}); err != nil {
					t.Fatalf("new service %s: %v", tt.flag, err)
				}
				for _, rel := range append([]string{filepath.Join("deploy", "k8s", "orders.yaml")}, tt.wantFiles...) {
					if _, err := os.Stat(filepath.Join(outDir, rel)); err != nil {
						t.Fatalf("new service %s missing generated file %s: %v", tt.flag, rel, err)
					}
				}
			})
		}
	})
}

func applyAINewAndSnapshot(t *testing.T, outDir string) map[string]string {
	t.Helper()
	var stdout bytes.Buffer
	args := []string{"ai", "new", "--template", "go-rest-clean-postgres", "--name", "orders", "--module", "example.com/orders", "--dir", outDir, "--apply", "--json"}
	if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
		t.Fatalf("ai new deterministic apply: %v", err)
	}
	var envelope struct {
		Data struct {
			GeneratedFeatures []struct {
				Files []string `json:"files"`
			} `json:"generatedFeatures"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("ai new deterministic JSON: %v\n%s", err, stdout.String())
	}
	files := []string{
		"go.mod",
		filepath.Join("cmd", "orders", "main.go"),
		"Dockerfile",
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join("internal", "routes", "routes.go"),
		filepath.Join("internal", "config", "config.go"),
	}
	for _, feature := range envelope.Data.GeneratedFeatures {
		for _, file := range feature.Files {
			files = append(files, filepath.FromSlash(file))
		}
	}
	snapshot := map[string]string{}
	for _, file := range appendUniqueStrings(nil, files...) {
		data, err := os.ReadFile(filepath.Join(outDir, file))
		if err != nil {
			t.Fatalf("read generated artifact %s: %v", file, err)
		}
		snapshot[filepath.ToSlash(file)] = string(data)
	}
	return snapshot
}

func TestAIProjectApplyVerificationScaffoldBoundaries(t *testing.T) {
	t.Run("apply verify compiles rpc and gateway templates", func(t *testing.T) {
		withFrameworkPath(t, func() {
			for _, tt := range []struct {
				name             string
				template         string
				wantVerification int
			}{
				{name: "greeter", template: "go-rpc-grpc", wantVerification: 5},
				{name: "edge", template: "go-gateway", wantVerification: 4},
			} {
				t.Run(tt.template, func(t *testing.T) {
					outDir := filepath.Join(t.TempDir(), tt.name)
					var stdout bytes.Buffer
					args := []string{"ai", "new", "--template", tt.template, "--name", tt.name, "--module", "example.com/" + tt.name, "--dir", outDir, "--apply", "--verify", "--verify-timeout", "2m", "--json"}
					if err := ExecuteWithIO(args, IOStreams{Out: &stdout}); err != nil {
						t.Fatalf("ai new %s --verify: %v", tt.template, err)
					}
					var envelope struct {
						Data struct {
							VerifyRan    bool `json:"verifyRan"`
							VerifyPassed bool `json:"verifyPassed"`
							Verification []struct {
								Command string `json:"command"`
								Status  string `json:"status"`
							} `json:"verification"`
						} `json:"data"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
						t.Fatalf("ai new %s JSON: %v\n%s", tt.template, err, stdout.String())
					}
					if !envelope.Data.VerifyRan || !envelope.Data.VerifyPassed || len(envelope.Data.Verification) != tt.wantVerification {
						t.Fatalf("ai new %s verification = %+v\n%s", tt.template, envelope.Data, stdout.String())
					}
					for _, check := range envelope.Data.Verification {
						if check.Status != "passed" {
							t.Fatalf("ai new %s verification check = %+v\n%s", tt.template, check, stdout.String())
						}
					}
				})
			}
		})
	})

	t.Run("apply rejects traversal output directories", func(t *testing.T) {
		parent := t.TempDir()
		outDir := parent + string(filepath.Separator) + "project" + string(filepath.Separator) + ".." + string(filepath.Separator) + "escape"
		var stdout bytes.Buffer
		err := ExecuteWithIO([]string{"ai", "new", "--template", "go-rest-minimal", "--name", "hello", "--module", "example.com/hello", "--dir", outDir, "--apply", "--json"}, IOStreams{Out: &stdout, Err: &bytes.Buffer{}})
		if err == nil || !strings.Contains(err.Error(), "project directory must not contain parent traversal") {
			t.Fatalf("ai new traversal error = %v, want traversal rejection", err)
		}
		if _, statErr := os.Stat(filepath.Join(parent, "escape", "go.mod")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("ai new traversal wrote outside intended directory or stat failed: %v", statErr)
		}
	})

	t.Run("verification output is truncated before JSON reporting", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/bad\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bad_test.go"), []byte("package bad\nimport \"testing\"\nfunc TestHuge(t *testing.T) { t.Fatal(\""+strings.Repeat("x", 6000)+"\") }\n"), 0o644); err != nil {
			t.Fatalf("write bad_test.go: %v", err)
		}
		result := runAIProjectVerificationCommand(dir, "go test ./...", 30*time.Second)
		if result.Status != "failed" || len(result.Output) >= 6000 || !strings.Contains(result.Output, "truncated") {
			t.Fatalf("verification truncation result = status:%s len:%d error:%q", result.Status, len(result.Output), result.Error)
		}
	})
}

func withFrameworkPath(t *testing.T, fn func()) {
	t.Helper()
	t.Setenv("GOFLY_FRAMEWORK_PATH", commandRepositoryRoot(t))
	fn()
}

func commandRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root: runtime caller unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read framework go.mod: %v", err)
	}
	if !strings.Contains(string(data), "module github.com/imajinyun/gofly") {
		t.Fatalf("framework root %s has unexpected go.mod:\n%s", root, data)
	}
	return root
}

func TestAIProjectVerificationHelpers(t *testing.T) {
	t.Run("all template verify commands are supported", func(t *testing.T) {
		withFrameworkPath(t, func() {
			for _, tmpl := range generator.ListProjectTemplates() {
				for _, command := range tmpl.Verify {
					if _, _, ok := aiProjectVerificationCommandArgs(command); !ok {
						t.Fatalf("template %s verify command %q is not supported by ai project verification", tmpl.ID, command)
					}
				}
			}
		})
	})

	t.Run("runs supported checks and skips unsupported checks", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/verify\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main( ){}\n"), 0o644); err != nil {
			t.Fatalf("write main.go: %v", err)
		}
		results, passed, err := runAIProjectVerification(dir, []string{"gofmt", "gofly plugin run unsupported"}, 5*time.Second)
		if err != nil {
			t.Fatalf("runAIProjectVerification: %v", err)
		}
		if !passed || len(results) != 2 || results[0].Status != "passed" || results[1].Status != "skipped" {
			t.Fatalf("verification results = %+v passed=%v, want passed+skipped", results, passed)
		}
	})

	t.Run("reports failing supported checks", func(t *testing.T) {
		results, passed, err := runAIProjectVerification(t.TempDir(), []string{"go test ./..."}, 5*time.Second)
		if err != nil {
			t.Fatalf("runAIProjectVerification failed check: %v", err)
		}
		if passed || len(results) != 1 || results[0].Status != "failed" || results[0].Error == "" {
			t.Fatalf("verification failure results = %+v passed=%v, want failed result", results, passed)
		}
	})

	t.Run("rejects invalid timeout", func(t *testing.T) {
		if _, _, err := runAIProjectVerification(t.TempDir(), []string{"gofmt"}, 0); !errors.Is(err, errUsage) {
			t.Fatalf("runAIProjectVerification invalid timeout error = %v, want errUsage", err)
		}
	})

	t.Run("maps allowlisted commands", func(t *testing.T) {
		name, args, ok := aiProjectVerificationCommandArgs("go mod tidy")
		if !ok || name != "go" || strings.Join(args, " ") != "mod tidy" {
			t.Fatalf("aiProjectVerificationCommandArgs go mod tidy = %q %v %v", name, args, ok)
		}
		withFrameworkPath(t, func() {
			name, args, ok := aiProjectVerificationCommandArgs("gofly ai doctor --json")
			if !ok || name != "go" || len(args) != 5 || args[0] != "run" || args[2] != "ai" || args[3] != "doctor" || args[4] != "--json" {
				t.Fatalf("aiProjectVerificationCommandArgs ai doctor = %q %v %v", name, args, ok)
			}
		})
		if _, _, ok := aiProjectVerificationCommandArgs("rm -rf ."); ok {
			t.Fatal("aiProjectVerificationCommandArgs accepted unsupported command")
		}
	})

	t.Run("truncates large verification output", func(t *testing.T) {
		got := truncateVerificationOutput(strings.Repeat("x", 5000))
		if len(got) >= 5000 || !strings.Contains(got, "truncated") {
			t.Fatalf("truncateVerificationOutput length=%d suffix=%q", len(got), got[len(got)-20:])
		}
	})
}

func TestAIPlanAndTemplateCatalogHelperBoundaries(t *testing.T) {
	templates := generator.ListProjectTemplates()
	if len(templates) == 0 {
		t.Fatal("ListProjectTemplates returned no templates")
	}
	for _, tmpl := range templates {
		if !tmpl.VerifyE2EValidated {
			t.Fatalf("template %s verifyE2EValidated=false, want generated project matrix contract coverage", tmpl.ID)
		}
	}
	firstID := templates[0].ID
	templates[0].ID = "mutated"
	if again := generator.ListProjectTemplates(); again[0].ID != firstID {
		t.Fatalf("ListProjectTemplates leaked mutation: got %q want %q", again[0].ID, firstID)
	}
	if _, ok := generator.GetProjectTemplate("  GO-RAG-SERVICE  "); !ok {
		t.Fatal("GetProjectTemplate should find case-insensitive trimmed id")
	}
	if _, ok := generator.GetProjectTemplate("missing-template"); ok {
		t.Fatal("GetProjectTemplate found unknown template")
	}
	if got := generator.RecommendProjectTemplate("build a retrieval augmented generation service with vector store", "").ID; got != "go-rag-service" {
		t.Fatalf("RecommendProjectTemplate rag id = %q, want go-rag-service", got)
	}
	if got := generator.RecommendProjectTemplate("anything", "gateway").Kind; got != "gateway" {
		t.Fatalf("RecommendProjectTemplate kind = %q, want gateway", got)
	}
	if got := materializeTemplateCommand("cmd <name> <module> <dir>", "", "", ""); got != "cmd demo example.com/demo demo" {
		t.Fatalf("materializeTemplateCommand defaults = %q", got)
	}
	plan := buildAIProjectPlan("create a gateway", "gateway", "edge", "example.com/edge", "edge-dir", false)
	if !plan.MutatesFilesystem || plan.DryRun || plan.ProjectType != "gateway" || !strings.Contains(plan.Command, "edge-dir") {
		t.Fatalf("buildAIProjectPlan mutating gateway = %#v", plan)
	}
	if _, err := buildAIProjectNewPlan("", "", "missing-template", "edge", "example.com/edge", "edge", true); !errors.Is(err, errUsage) {
		t.Fatal("buildAIProjectNewPlan should reject unknown template id")
	}
	unsafeCommands := []generator.ProjectTemplate{
		{ID: "shell", Command: "bash -c rm"},
		{ID: "short", Command: "gofly new"},
		{ID: "unsupported", Command: "gofly plugin run tool"},
		{ID: "metachar", Command: "gofly new api <name> ; rm"},
	}
	for _, tmpl := range unsafeCommands {
		if err := validateAIProjectTemplateCommand(tmpl); !errors.Is(err, errUsage) {
			t.Fatalf("validateAIProjectTemplateCommand(%+v) error = %v, want errUsage", tmpl, err)
		}
	}
	if args, err := aiProjectApplyArgs(plan); err != nil || strings.Join(args, " ") != "gen gateway edge --module example.com/edge --dir edge-dir" {
		t.Fatalf("aiProjectApplyArgs gateway = %v, %v", args, err)
	}
	if got := stripCommandFlags([]string{"new", "api", "hello", "--dry-run", "--json=false", "--module", "example.com/hello"}, "--dry-run", "--json"); strings.Join(got, " ") != "new api hello --module example.com/hello" {
		t.Fatalf("stripCommandFlags = %q", strings.Join(got, " "))
	}
	if got := stripCommandFlags([]string{"new", "api", "hello", "--dry-run", "false", "--plan", "false", "--module=example.com/hello"}, "--dry-run", "--plan"); strings.Join(got, " ") != "new api hello --module=example.com/hello" {
		t.Fatalf("stripCommandFlags bool values = %q", strings.Join(got, " "))
	}
	if got := templateInputValue("gofly new api hello --module=example.com/hello --dir out", "--module"); got != "example.com/hello" {
		t.Fatalf("templateInputValue inline module = %q", got)
	}

	filtered := filterProjectTemplates(generator.ListProjectTemplates(), "vector-store", "")
	if len(filtered) == 0 {
		t.Fatal("filterProjectTemplates vector-store returned no templates")
	}
	if templateCatalogFilterMatch(generator.ProjectTemplate{ID: "x", Name: "X", Kind: "service"}, "missing", "nope") {
		t.Fatal("templateCatalogFilterMatch accepted non-matching category/name")
	}

	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return aiPlanCommand([]string{"--prompt", "create a cli", "--kind", "cli", "--name", "tool"})
	}); err != nil {
		t.Fatalf("aiPlanCommand text returned error: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "template=go-cli-cobra") || !strings.Contains(got, "command=") {
		t.Fatalf("aiPlanCommand text output = %s", got)
	}

	usageCases := [][]string{
		{},
		{"--prompt", "one", "extra positional"},
		{"--prompt", "one", "--format", "xml"},
	}
	for _, args := range usageCases {
		if err := aiPlanCommand(args); !errors.Is(err, errUsage) {
			t.Fatalf("aiPlanCommand(%v) error = %v, want errUsage", args, err)
		}
	}
}

func TestTemplateCatalogTextAndUsageBranches(t *testing.T) {
	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return templateCommand([]string{"list", "--category", "rag", "--name", "rag"})
	}); err != nil {
		t.Fatalf("template list text returned error: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "go-rag-service") {
		t.Fatalf("template list text output = %s", got)
	}

	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return templateCommand([]string{"inspect", "go-rag-service"})
	}); err != nil {
		t.Fatalf("template inspect text returned error: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "id: go-rag-service") || !strings.Contains(got, "features:") {
		t.Fatalf("template inspect text output = %s", got)
	}

	for _, args := range [][]string{{}, {"inspect"}, {"inspect", "missing-template"}, {"unknown"}} {
		if err := templateCommand(args); !errors.Is(err, errUsage) {
			t.Fatalf("templateCommand(%v) error = %v, want errUsage", args, err)
		}
	}
}

func TestCommandCLIHelperBoundaries(t *testing.T) {
	if err := completeCommand(nil); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "complete handler") {
		t.Fatalf("completeCommand nil error = %v, want usage", err)
	}
	if err := completeCommand([]string{"wrong"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "complete handler") {
		t.Fatalf("completeCommand wrong subcommand error = %v, want usage", err)
	}
	if err := completeCommand([]string{"handler"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), completionShellUsage) {
		t.Fatalf("completeCommand missing shell error = %v, want shell usage", err)
	}
	if err := completeCommand([]string{"handler", "bash", "zsh"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("completeCommand too many shells error = %v, want exactly one", err)
	}
	if err := completeCommand([]string{"handler", "unknown"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("completeCommand unknown shell error = %v, want usage", err)
	}
	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return completeCommand([]string{"handler", "bash"})
	}); err != nil {
		t.Fatalf("completeCommand bash: %v", err)
	}
	if !strings.Contains(out.String(), "gofly") || !strings.Contains(out.String(), "complete") {
		t.Fatalf("completeCommand bash output = %q, want completion script", out.String())
	}

	configDir := t.TempDir()
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return configCommand([]string{"init", "--dir", configDir, "--name", "orders", "--module", "example.com/orders", "--style", "minimal", "--dry-run"})
	}); err != nil {
		t.Fatalf("config init dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "config.init") || !strings.Contains(out.String(), "write-config") {
		t.Fatalf("config init dry-run output = %q, want plan", out.String())
	}
	if _, err := os.Stat(filepath.Join(configDir, generator.DefaultConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("config init dry-run stat error = %v, want no config file", err)
	}
	if err := configCommand([]string{"init", "--dir", configDir, "--name", "orders", "--module", "example.com/orders"}); err != nil {
		t.Fatalf("config init: %v", err)
	}
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return configCommand([]string{"get", "--dir", configDir, "service-name"})
	}); err != nil {
		t.Fatalf("config get positional key: %v", err)
	}
	if strings.TrimSpace(out.String()) != "orders" {
		t.Fatalf("config get service-name = %q, want orders", out.String())
	}
	if err := configCommand([]string{"set", "--dir", configDir, "features", ""}); err != nil {
		t.Fatalf("config set empty features via positional value: %v", err)
	}
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return configCommand([]string{"clean", "--dir", configDir, "--plan"})
	}); err != nil {
		t.Fatalf("config clean plan: %v", err)
	}
	if !strings.Contains(out.String(), "config.clean") || !strings.Contains(out.String(), "remove-config") {
		t.Fatalf("config clean plan output = %q, want remove plan", out.String())
	}
	if err := releaseCommand(nil); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "gofly release check") {
		t.Fatalf("releaseCommand nil error = %v, want release usage", err)
	}
}

func TestRootUtilityCommandsGenerateArtifactsAndReports(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := dockerCommand([]string{"orders", "--output", dockerfile, "--go", "./cmd/orders", "--exe", "orders", "--version", "1.26", "--base", "scratch", "--port", "8080", "--tz", "UTC"}); err != nil {
		t.Fatalf("dockerCommand: %v", err)
	}
	if data, err := os.ReadFile(dockerfile); err != nil || !strings.Contains(string(data), "FROM golang:1.26") || !strings.Contains(string(data), "EXPOSE 8080") {
		t.Fatalf("Dockerfile = %q, %v; want generated docker metadata", string(data), err)
	}

	kubeOut := filepath.Join(dir, "kube.yaml")
	if err := kubeCommand([]string{"configmap", "orders", "--output", kubeOut, "--namespace", "prod", "--data", "A=1,B=two"}); err != nil {
		t.Fatalf("kubeCommand configmap: %v", err)
	}
	if data, err := os.ReadFile(kubeOut); err != nil || !strings.Contains(string(data), "kind: ConfigMap") || !strings.Contains(string(data), "A: \"1\"") {
		t.Fatalf("kube configmap = %q, %v; want generated configmap", string(data), err)
	}

	migrationDir := filepath.Join(dir, "migrations")
	if err := migrateCommand([]string{"create", "create_orders", "--dir", migrationDir}); err != nil {
		t.Fatalf("migrateCommand create: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(migrationDir, "*_create_orders.up.sql"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("migration files = %v, %v; want one up migration", matches, err)
	}

	key := "GOFLY_BITS_UT_ENV_COMMAND"
	t.Setenv(key, "")
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return envCommand([]string{"--write", key + "=first", "--verbose", "--json"})
	}); err != nil {
		t.Fatalf("envCommand write json: %v", err)
	}
	if got := os.Getenv(key); got != "first" || !strings.Contains(out.String(), "GOFLY_VERSION") {
		t.Fatalf("envCommand got env=%q output=%q, want json env info", got, out.String())
	}
	if err := envCommand([]string{"--write", key + "=second"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("envCommand overwrite error = %v, want usage", err)
	}
	if err := envCommand([]string{"--write", key + "=second", "--force"}); err != nil {
		t.Fatalf("envCommand force overwrite: %v", err)
	}

	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return bugCommand([]string{"--json"})
	}); err != nil {
		t.Fatalf("bugCommand json: %v", err)
	}
	if !strings.Contains(out.String(), `"tool": "gofly"`) || !strings.Contains(out.String(), `"checks"`) {
		t.Fatalf("bugCommand output = %q, want bug report json", out.String())
	}

	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return upgradeCommand([]string{"--json", "--version", "v9.9.9", "--module", "example.com/gofly"})
	}); err != nil {
		t.Fatalf("upgradeCommand json plan: %v", err)
	}
	if !strings.Contains(out.String(), `"target": "example.com/gofly@v9.9.9"`) || !strings.Contains(out.String(), `"execute": false`) {
		t.Fatalf("upgradeCommand output = %q, want upgrade plan", out.String())
	}
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return upgradeCommand([]string{"--json", "--version", "v9.9.9", "--module", "example.com/gofly", "--project-dir", "/tmp/generated-orders"})
	}); err != nil {
		t.Fatalf("upgradeCommand generated project json plan: %v", err)
	}
	for _, want := range []string{`"generatedProject": true`, `"projectDir": "/tmp/generated-orders"`, `"diffCommand"`, `"api"`, `"diff"`, `"verifyCommand"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("upgradeCommand generated project output missing %q: %q", want, out.String())
		}
	}
	if err := upgradeCommand([]string{"unexpected"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "does not accept positional") {
		t.Fatalf("upgradeCommand positional error = %v, want usage", err)
	}
}

func TestIDLCommandHelperBoundaries(t *testing.T) {
	if err := rpcDepsCommand(nil); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "idl file is required") {
		t.Fatalf("rpcDepsCommand nil error = %v, want idl usage", err)
	}
	dir := t.TempDir()
	protoPath := filepath.Join(dir, "greeter.proto")
	proto := `syntax = "proto3";
package demo;
import "google/protobuf/timestamp.proto";
message HelloReq { string name = 1; }
message HelloResp { string message = 1; }
service Greeter { rpc SayHello (HelloReq) returns (HelloResp); }
`
	if err := os.WriteFile(protoPath, []byte(proto), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return rpcDepsCommand([]string{protoPath})
	}); err != nil {
		t.Fatalf("rpcDepsCommand text: %v", err)
	}
	if !strings.Contains(out.String(), "google/protobuf/timestamp.proto") {
		t.Fatalf("rpcDepsCommand text output = %q, want import", out.String())
	}
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return rpcDepsCommand([]string{"--file", protoPath, "--format", "json"})
	}); err != nil {
		t.Fatalf("rpcDepsCommand json: %v", err)
	}
	if !strings.Contains(out.String(), "google/protobuf/timestamp.proto") || !strings.Contains(out.String(), `"services": 1`) {
		t.Fatalf("rpcDepsCommand json output = %q, want import and service count", out.String())
	}
	if err := rpcDepsCommand([]string{"--file", protoPath, "--format", "yaml"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "unsupported rpc deps format") {
		t.Fatalf("rpcDepsCommand unsupported format error = %v, want usage", err)
	}

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
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return apiFormatCommand([]string{apiPath, "--write=false"})
	}); err != nil {
		t.Fatalf("apiFormatCommand stdout: %v", err)
	}
	if !strings.Contains(out.String(), "service user-api") || !strings.Contains(out.String(), "@handler Ping") {
		t.Fatalf("apiFormatCommand output = %q, want formatted api", out.String())
	}
	outFile := filepath.Join(dir, "formatted", "user.api")
	if err := apiFormatCommand([]string{"--file", apiPath, "--output", outFile}); err != nil {
		t.Fatalf("apiFormatCommand output file: %v", err)
	}
	if data, err := os.ReadFile(outFile); err != nil || !strings.Contains(string(data), "service user-api") {
		t.Fatalf("formatted api file = %q, %v; want service", string(data), err)
	}

	if err := handlerCompleteCommand(nil); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "--file is required") {
		t.Fatalf("handlerCompleteCommand nil error = %v, want file usage", err)
	}
	handlerPath := filepath.Join(dir, "handler.go")
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return handlerCompleteCommand([]string{"--file", handlerPath, "--package", "handler", "--receiver", "h", "--method", "ping", "--comment", "Ping handles ping.", "--body", "\t// done"})
	}); err != nil {
		t.Fatalf("handlerCompleteCommand create: %v", err)
	}
	data, err := os.ReadFile(handlerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "func (h *H) Ping()") || !strings.Contains(out.String(), "added 1 method") {
		t.Fatalf("handlerCompleteCommand create output=%q file=%s, want generated method", out.String(), string(data))
	}
	out.Reset()
	if err := withCommandIO(IOStreams{Out: &out}, outputText, verbosityNormal, func() error {
		return handlerCompleteCommand([]string{"--file", handlerPath, "--receiver", "h", "Ping"})
	}); err != nil {
		t.Fatalf("handlerCompleteCommand existing: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Fatalf("handlerCompleteCommand existing output = %q, want nothing to do", out.String())
	}

	if err := runPostPlugins("", generator.PluginRequest{Dir: dir}); err != nil {
		t.Fatalf("runPostPlugins empty: %v", err)
	}
	if err := runPostPlugins(" , ", generator.PluginRequest{Dir: dir}); err != nil {
		t.Fatalf("runPostPlugins empty CSV: %v", err)
	}
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("before\nanchor\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(dir, "post-plugin.sh")
	plugin := `#!/bin/sh
printf '%s' '{"version":"1","message":"post ok","files":[{"path":"generated.txt","content":"generated"}],"patches":[{"path":"target.txt","insertAfter":"anchor","patch":"inserted"}]}'
`
	if err := os.WriteFile(pluginPath, []byte(plugin), 0o755); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	if err := withCommandIO(IOStreams{Err: &errOut}, outputText, verbosityNormal, func() error {
		return runPostPlugins(pluginPath, generator.PluginRequest{Dir: dir, Input: map[string]string{"api": apiPath}})
	}); err != nil {
		t.Fatalf("runPostPlugins success: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "generated.txt")); err != nil || string(data) != "generated" {
		t.Fatalf("runPostPlugins generated file = %q, %v", string(data), err)
	}
	if data, err := os.ReadFile(target); err != nil || !strings.Contains(string(data), "anchor\ninserted") {
		t.Fatalf("runPostPlugins patched file = %q, %v", string(data), err)
	}
	if !strings.Contains(errOut.String(), "post ok") {
		t.Fatalf("runPostPlugins stderr = %q, want plugin message", errOut.String())
	}
	badPlugin := filepath.Join(dir, "bad-plugin.sh")
	if err := os.WriteFile(badPlugin, []byte("#!/bin/sh\nprintf '%s' '{\"version\":\"bad\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runPostPlugins(badPlugin, generator.PluginRequest{Dir: dir}); err == nil || !strings.Contains(err.Error(), "run plugin") || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("runPostPlugins incompatible error = %v, want wrapped incompatible plugin error", err)
	}
}

func TestCommandRegistryBoundaries(t *testing.T) {
	var calls []string
	registry := newCommandRegistry(
		commandSpec{Name: "", Run: func([]string) error { return nil }},
		commandSpec{Name: "missing-run"},
		commandSpec{
			Name:    "serve",
			Aliases: []string{"s", ""},
			Run: func(args []string) error {
				calls = append(calls, strings.Join(args, ","))
				return nil
			},
		},
		commandSpec{
			Name: "fail",
			Run: func([]string) error {
				return errors.New("boom")
			},
		},
	)

	if got := registry.expected(); got != "serve|s||fail" {
		t.Fatalf("expected() = %q, want serve|s||fail", got)
	}
	if _, ok := registry.commands[""]; ok {
		t.Fatal("empty command or alias should not be registered")
	}
	if _, ok := registry.commands["missing-run"]; ok {
		t.Fatal("command without Run should not be registered")
	}

	if err := registry.dispatch([]string{"s", "--port", "8080"}, "serve|s"); err != nil {
		t.Fatalf("dispatch(alias): %v", err)
	}
	if len(calls) != 1 || calls[0] != "--port,8080" {
		t.Fatalf("alias dispatch calls = %#v, want forwarded args", calls)
	}

	if err := registry.dispatch(nil, "serve|s"); err == nil || !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "expected serve|s") {
		t.Fatalf("dispatch(nil) error = %v, want usage expected error", err)
	}
	if err := registry.dispatch([]string{"unknown"}, "serve|s"); err == nil || !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("dispatch(unknown) error = %v, want usage unknown command", err)
	}
	if err := registry.dispatch([]string{"fail"}, "serve|s"); err == nil || err.Error() != "boom" {
		t.Fatalf("dispatch(fail) error = %v, want boom", err)
	}

	fallbackCalls := 0
	fallback := func(args []string) error {
		fallbackCalls++
		if len(args) > 0 && args[0] == "--bad" {
			return errors.New("fallback")
		}
		return nil
	}
	if err := registry.dispatchDefault(nil, "serve|s", fallback); err != nil {
		t.Fatalf("dispatchDefault(nil): %v", err)
	}
	if err := registry.dispatchDefault([]string{"--bad"}, "serve|s", fallback); err == nil || err.Error() != "fallback" {
		t.Fatalf("dispatchDefault(flag) error = %v, want fallback", err)
	}
	if err := registry.dispatchDefault([]string{"serve", "positional"}, "serve|s", fallback); err != nil {
		t.Fatalf("dispatchDefault(command): %v", err)
	}
	if fallbackCalls != 2 {
		t.Fatalf("fallback calls = %d, want 2", fallbackCalls)
	}
}

func TestFillNameAndEnrichPluginRequestIDL(t *testing.T) {
	fillNameFromArgs(nil, []string{"ignored"})
	existing := "kept"
	fillNameFromArgs(&existing, []string{"new"})
	if existing != "kept" {
		t.Fatalf("fillNameFromArgs overwrote existing name: %q", existing)
	}
	empty := ""
	fillNameFromArgs(&empty, nil)
	if empty != "" {
		t.Fatalf("fillNameFromArgs with no args = %q, want empty", empty)
	}
	fillNameFromArgs(&empty, []string{"orders", "ignored"})
	if empty != "orders" {
		t.Fatalf("fillNameFromArgs filled %q, want orders", empty)
	}

	dir := t.TempDir()
	protoFile := filepath.Join(dir, "svc.proto")
	if err := os.WriteFile(protoFile, []byte("syntax = \"proto3\";"), 0o644); err != nil {
		t.Fatal(err)
	}

	existingIDL := generator.PluginRequest{IDL: []byte("existing"), IDLFormat: "api", Input: map[string]string{"proto": protoFile}}
	if got := enrichPluginRequestIDL(existingIDL); string(got.IDL) != "existing" || got.IDLFormat != "api" {
		t.Fatalf("enrichPluginRequestIDL(existing) = (%q, %q), want existing api", string(got.IDL), got.IDLFormat)
	}
	if got := enrichPluginRequestIDL(generator.PluginRequest{}); got.IDL != nil || got.IDLFormat != "" {
		t.Fatalf("enrichPluginRequestIDL(nil input) = (%q, %q), want empty", string(got.IDL), got.IDLFormat)
	}
	missing := enrichPluginRequestIDL(generator.PluginRequest{Input: map[string]string{"proto": filepath.Join(dir, "missing.proto")}})
	if missing.IDL != nil || missing.IDLFormat != "" {
		t.Fatalf("enrichPluginRequestIDL(missing) = (%q, %q), want empty", string(missing.IDL), missing.IDLFormat)
	}
	got := enrichPluginRequestIDL(generator.PluginRequest{Input: map[string]string{"proto": "  " + protoFile + "  "}})
	if string(got.IDL) != "syntax = \"proto3\";" || got.IDLFormat != "proto" {
		t.Fatalf("enrichPluginRequestIDL(proto) = (%q, %q), want proto contents", string(got.IDL), got.IDLFormat)
	}
}

func TestPluginCommandUsageBoundaries(t *testing.T) {
	tests := []struct {
		name string
		fn   func([]string) error
		args []string
		want string
	}{
		{name: "rpc plugin missing file", fn: rpcPluginCommand, args: []string{"audit"}, want: "--file is required"},
		{name: "rpc plugin missing plugin", fn: rpcPluginCommand, args: []string{"--file", "svc.proto"}, want: "plugin is required"},
		{name: "api plugin missing file", fn: apiPluginCommand, args: []string{"audit"}, want: "api file is required"},
		{name: "api plugin missing plugin", fn: apiPluginCommand, args: []string{"--file", "svc.api"}, want: "api plugin is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn(tt.args)
			if err == nil || !errors.Is(err, errUsage) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("plugin command error = %v, want usage containing %q", err, tt.want)
			}
		})
	}
}

func TestCommandHelpForTopicBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "top-level alias tools", input: "tools manifest extra", want: "ai manifest"},
		{name: "api gen alias", input: "api gen users extra", want: "api go"},
		{name: "api fmt alias", input: "api fmt users", want: "api format"},
		{name: "api docs alias", input: "api docs users", want: "api doc"},
		{name: "api kt alias", input: "api kt users", want: "api kotlin"},
		{name: "rpc inspect alias", input: "rpc inspect greeter", want: "rpc idl"},
		{name: "rpc thrift2proto alias", input: "rpc thrift2proto greeter", want: "rpc thrift"},
		{name: "rpc template list trims", input: "rpc tpl list name", want: "rpc template"},
		{name: "model postgres alias", input: "model postgresql datasource accounts", want: "model pg datasource"},
		{name: "new api trims positional", input: "new api hello", want: "new api"},
		{name: "gen rest", input: "gen rest", want: "gen rest"},
		{name: "handler gen", input: "handler gen create", want: "handler gen"},
		{name: "kube deploy", input: "kube deploy orders", want: "kube deploy"},
		{name: "template update", input: "template update local", want: "template update"},
		{name: "migrate new alias", input: "migration new add_users", want: "migrate create"},
		{name: "complete pwsh alias", input: "complete handler pwsh", want: "complete handler powershell"},
		{name: "completion shell", input: "completion fish", want: "completion fish"},
		{name: "feature run", input: "feature run ecosystem-compat", want: "feature run"},
		{name: "plugin install default", input: "plugin install remote", want: "plugin install remote"},
		{name: "example plural run", input: "examples run restserver", want: "example run"},
		{name: "unknown keeps topic", input: "unknown topic", want: "unknown topic"},
		{name: "api check", input: "api check", want: "api check"},
		{name: "api swagger", input: "api swagger", want: "api swagger"},
		{name: "api route", input: "api route", want: "api route"},
		{name: "api import", input: "api import", want: "api import"},
		{name: "api diff", input: "api diff", want: "api diff"},
		{name: "api breaking", input: "api breaking", want: "api breaking"},
		{name: "api types", input: "api types", want: "api types"},
		{name: "api client", input: "api client", want: "api client"},
		{name: "api plugin", input: "api plugin", want: "api plugin"},
		{name: "api middleware", input: "api middleware", want: "api middleware"},
		{name: "gen middleware", input: "gen middleware", want: "gen middleware"},
		{name: "rpc gen", input: "rpc gen", want: "rpc gen"},
		{name: "rpc idl", input: "rpc idl", want: "rpc idl"},
		{name: "rpc thrift", input: "rpc thrift", want: "rpc thrift"},
		{name: "rpc client", input: "rpc client", want: "rpc client"},
		{name: "rpc server", input: "rpc server", want: "rpc server"},
		{name: "rpc middleware", input: "rpc middleware", want: "rpc middleware"},
		{name: "rpc lint", input: "rpc lint", want: "rpc lint"},
		{name: "rpc deps", input: "rpc deps", want: "rpc deps"},
		{name: "gen rpc", input: "gen rpc", want: "gen rpc"},
		{name: "rpc protoc", input: "rpc protoc", want: "rpc protoc"},
		{name: "rpc check", input: "rpc check", want: "rpc check"},
		{name: "rpc breaking", input: "rpc breaking", want: "rpc breaking"},
		{name: "rpc descriptor", input: "rpc descriptor", want: "rpc descriptor"},
		{name: "rpc plugin", input: "rpc plugin", want: "rpc plugin"},
		{name: "rpc new", input: "rpc new greeter", want: "rpc new"},
		{name: "model gen", input: "model gen", want: "model gen"},
		{name: "model mysql ddl", input: "model mysql ddl", want: "model mysql ddl"},
		{name: "model pg ddl", input: "model pg ddl", want: "model pg ddl"},
		{name: "gen model", input: "gen model", want: "gen model"},
		{name: "model mysql datasource", input: "model mysql datasource", want: "model mysql datasource"},
		{name: "model pg datasource", input: "model pg datasource", want: "model pg datasource"},
		{name: "model mongo", input: "model mongo", want: "model mongo"},
		{name: "top api", input: "api", want: "api"},
		{name: "top rpc", input: "rpc", want: "rpc"},
		{name: "top model", input: "model", want: "model"},
		{name: "top new", input: "new", want: "new"},
		{name: "top gen", input: "gen", want: "gen"},
		{name: "version", input: "version", want: "version"},
		{name: "docker", input: "docker service", want: "docker"},
		{name: "kube", input: "kube", want: "kube"},
		{name: "kube service", input: "kube service svc", want: "kube service"},
		{name: "template", input: "template", want: "template"},
		{name: "template init", input: "template init", want: "template init"},
		{name: "quickstart", input: "quickstart hello", want: "quickstart"},
		{name: "migrate", input: "migrate", want: "migrate"},
		{name: "env", input: "env", want: "env"},
		{name: "env check", input: "env check", want: "env check"},
		{name: "config", input: "config", want: "config"},
		{name: "config init", input: "config init", want: "config init"},
		{name: "config show", input: "config show", want: "config show"},
		{name: "config get", input: "config get", want: "config get"},
		{name: "config set", input: "config set", want: "config set"},
		{name: "config clean", input: "config clean", want: "config clean"},
		{name: "feature", input: "feature", want: "feature"},
		{name: "feature list", input: "feature list", want: "feature list"},
		{name: "plugin", input: "plugin", want: "plugin"},
		{name: "plugin list", input: "plugin list", want: "plugin list"},
		{name: "plugin run", input: "plugin run", want: "plugin run"},
		{name: "complete", input: "complete", want: "complete"},
		{name: "completion", input: "completion", want: "completion"},
		{name: "completion bash", input: "completion bash", want: "completion bash"},
		{name: "release", input: "release", want: "release"},
		{name: "release check", input: "release check", want: "release check"},
		{name: "doctor", input: "doctor", want: "doctor"},
		{name: "ai", input: "ai", want: "ai"},
		{name: "ai complete", input: "ai complete", want: "ai complete"},
		{name: "ai doctor", input: "ai doctor", want: "ai doctor"},
		{name: "example", input: "example", want: "example"},
		{name: "example list", input: "example list", want: "example list"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandHelpFor(tt.input)
			if got.Name != tt.want {
				t.Fatalf("commandHelpFor(%q).Name = %q, want %q", tt.input, got.Name, tt.want)
			}
			if got.Short == "" || got.Usage == "" {
				t.Fatalf("commandHelpFor(%q) returned incomplete help: %#v", tt.input, got)
			}
		})
	}
}

func TestRPCDescriptorURLAndAIDoctorConfigBoundaries(t *testing.T) {
	urlTests := []struct {
		name        string
		raw         string
		service     string
		wantPath    string
		wantErrPart string
	}{
		{name: "already service descriptor", raw: "http://127.0.0.1/rpc/admin/descriptors/greeter", wantPath: "/rpc/admin/descriptors/greeter"},
		{name: "descriptor collection requires service", raw: "http://127.0.0.1/rpc/admin/descriptors/", wantErrPart: "--service is required"},
		{name: "descriptor collection appends escaped service", raw: "http://127.0.0.1/rpc/admin/descriptors", service: "user service", wantPath: "/rpc/admin/descriptors/user%20service"},
		{name: "admin base requires service", raw: "http://127.0.0.1/admin", wantErrPart: "admin base"},
		{name: "custom base appends service descriptor", raw: "http://127.0.0.1/custom/", service: "greeter", wantPath: "/custom/rpc/admin/descriptors/greeter"},
		{name: "custom base without service unchanged", raw: "http://127.0.0.1/custom/", wantPath: "/custom/"},
	}
	for _, tt := range urlTests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatal(err)
			}
			err = normalizeRPCDescriptorURL(parsed, tt.service)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("normalizeRPCDescriptorURL() error = %v, want containing %q", err, tt.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeRPCDescriptorURL(): %v", err)
			}
			if parsed.Path != tt.wantPath {
				t.Fatalf("normalized path = %q, want %q", parsed.Path, tt.wantPath)
			}
		})
	}

	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", filepath.Join(dir, "home"))
	if err := generator.SaveConfig(filepath.Join(dir, generator.DefaultConfigFile), &generator.Config{LLM: &generator.LLMConfig{Provider: "noop", Model: "unit"}}); err != nil {
		t.Fatal(err)
	}
	if got := checkAIDoctorConfig(); got.Status != "ok" || !strings.Contains(got.Message, "unit") {
		t.Fatalf("checkAIDoctorConfig(workdir) = %+v, want ok with unit model", got)
	}
}
