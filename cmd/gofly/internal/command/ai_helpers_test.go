package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/cmd/gofly/internal/generator"
	"github.com/gofly/gofly/core/llm"
)

func TestIsAIHelpSubcommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "manifest", command: "manifest", want: true},
		{name: "plan", command: "plan", want: true},
		{name: "complete", command: "complete", want: true},
		{name: "stream", command: "stream", want: true},
		{name: "doctor", command: "doctor", want: true},
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

func TestCommandHelpSubcommandBoundaries_BitsUT(t *testing.T) {
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
		{name: "plugin", fn: isPluginHelpSubcommand, yes: "list", no: "install"},
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

func TestNewServicePlanAndFlagParsingBoundaries_BitsUT(t *testing.T) {
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
	plan := buildNewServicePlan("new api", "out", ".gofly/config.json", valid, []string{"audit", "trace"}, true, true)
	if plan.Command != "new api" || !plan.DryRun || !plan.MutatesFilesystem || len(plan.Actions) != 4 || len(plan.Warnings) != 2 || plan.Inputs["features"] != "http" || plan.Inputs["plugins"] != "audit,trace" {
		t.Fatalf("buildNewServicePlan = %#v, want full dry-run plan", plan)
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

func TestCommandIOAndAIDoctorBoundaries_BitsUT(t *testing.T) {
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

func TestAIExecutionFailoverAndPlanHelpers_BitsUT(t *testing.T) {
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

func TestAIStreamPlanJSONBoundary_BitsUT(t *testing.T) {
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

func TestRootControlVersionAndHelpBoundaries_BitsUT(t *testing.T) {
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

func TestDoctorExampleAndRootHelperBoundaries_BitsUT(t *testing.T) {
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

func TestLooksLikeShellScriptBoundaries_BitsUT(t *testing.T) {
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

func TestPluginPlanAndConfigHelpers_BitsUT(t *testing.T) {
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

func TestAIDoctorNextActionsBoundaries_BitsUT(t *testing.T) {
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

func TestAIDoctorConfigAndLoadOverlayBoundaries_BitsUT(t *testing.T) {
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
		if !envelope.OK || envelope.Data.ID != "go-ai-agent" || envelope.Data.Kind != "ai-agent" || !strings.Contains(envelope.Data.Command, "gofly new api") || len(envelope.Data.Verify) == 0 {
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

func TestAIPlanAndTemplateCatalogHelperBoundaries_BitsUT(t *testing.T) {
	templates := generator.ListProjectTemplates()
	if len(templates) == 0 {
		t.Fatal("ListProjectTemplates returned no templates")
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

func TestTemplateCatalogTextAndUsageBranches_BitsUT(t *testing.T) {
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

func TestCommandCLIHelperBoundaries_BitsUT(t *testing.T) {
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

func TestRootUtilityCommandsGenerateArtifactsAndReports_BitsUT(t *testing.T) {
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
	if err := upgradeCommand([]string{"unexpected"}); !errors.Is(err, errUsage) || !strings.Contains(err.Error(), "does not accept positional") {
		t.Fatalf("upgradeCommand positional error = %v, want usage", err)
	}
}

func TestIDLCommandHelperBoundaries_BitsUT(t *testing.T) {
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

func TestCommandRegistryBoundaries_BitsUT(t *testing.T) {
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

func TestFillNameAndEnrichPluginRequestIDL_BitsUT(t *testing.T) {
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

func TestPluginCommandUsageBoundaries_BitsUT(t *testing.T) {
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

func TestCommandHelpForTopicBoundaries_BitsUT(t *testing.T) {
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

func TestRPCDescriptorURLAndAIDoctorConfigBoundaries_BitsUT(t *testing.T) {
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
