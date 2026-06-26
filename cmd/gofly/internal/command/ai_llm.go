package command

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/llm"
)

func runAICompleteWithFailover(resolved aiCompleteConfig, req llm.Request, prompt string) (llm.Response, llm.ProviderSpec, *llm.TokenBudget, bool, string, error) {
	budget := llm.NewTokenBudget(resolved.MaxInputTokens, resolved.MaxOutputTokens, resolved.MaxTotalTokens)
	auditor := llm.NewAuditLogger(slog.New(slog.NewJSONHandler(currentErr(), &slog.HandlerOptions{Level: slog.LevelInfo})), nil)
	options := aiGovernedProviderOptions(resolved, budget, auditor)
	ctx, cancel := aiExecutionContext(resolved)
	defer cancel()

	registry := llm.NewDefaultProviderRegistry()
	providers := []string{resolved.Provider}
	if resolved.AllowFailover {
		providers = append(providers, resolved.FailoverProviders...)
	}
	idempotencyKey := aiFailoverIdempotencyKey(prompt, resolved)
	var primaryErr error
	for index, providerName := range providers {
		attemptReq := req
		attemptReq.Provider = providerName
		attemptReq.Metadata = aiAttemptMetadata("ai.complete", index, resolved.Provider, providerName, idempotencyKey, resolved.AllowFailover)
		builtProvider, providerSpec, err := registry.Build(providerName, llm.ProviderConfig{
			Provider: providerName,
			Model:    resolved.Model,
			Secrets:  llm.EnvSecretResolver{},
			Metadata: attemptReq.Metadata,
		})
		if err != nil {
			return llm.Response{}, providerSpec, budget, index > 0, failoverFrom(index, resolved.Provider), err
		}
		providerClient := llm.NewGovernedProvider(builtProvider, options...)
		resp, err := providerClient.Complete(ctx, attemptReq)
		if err == nil {
			return resp, providerSpec, budget, index > 0, failoverFrom(index, resolved.Provider), nil
		}
		if index == 0 {
			primaryErr = err
		}
		if !shouldAttemptManualFailover(resolved, index, err) {
			return llm.Response{}, providerSpec, budget, index > 0, failoverFrom(index, resolved.Provider), err
		}
	}
	return llm.Response{}, llm.ProviderSpec{}, budget, false, "", primaryErr
}

func aiGovernedProviderOptions(resolved aiCompleteConfig, budget *llm.TokenBudget, auditor *llm.AuditLogger) []llm.Option {
	options := []llm.Option{llm.WithTokenBudget(budget), llm.WithAuditLogger(auditor), llm.WithCircuitBreaker(breaker.WithFailureThreshold(5), breaker.WithOpenTimeout(10*time.Second))}
	if resolved.RateLimitPerSecond > 0 {
		options = append(options, llm.WithRateLimiter(llm.NewRateLimiter(resolved.RateLimitPerSecond, resolved.RateLimitBurst)))
	}
	return options
}

func aiExecutionContext(resolved aiCompleteConfig) (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if resolved.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, resolved.Timeout)
}

func shouldAttemptManualFailover(resolved aiCompleteConfig, index int, err error) bool {
	return resolved.AllowFailover && index == 0 && len(resolved.FailoverProviders) > 0 && isRetryableLLMError(err)
}

func isRetryableLLMError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *llm.ProviderHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Retryable()
	}
	return errors.Is(err, llm.ErrProviderRequestFailed) || errors.Is(err, llm.ErrRateLimited)
}

func failoverFrom(index int, primary string) string {
	if index == 0 {
		return ""
	}
	return primary
}

func aiFailoverIdempotencyKey(prompt string, resolved aiCompleteConfig) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		prompt,
		resolved.Provider,
		resolved.Model,
		strconv.Itoa(resolved.MaxInputTokens),
		strconv.Itoa(resolved.MaxOutputTokens),
		strconv.Itoa(resolved.MaxTotalTokens),
	}, "\x00")))
	return fmt.Sprintf("gofly-ai-%x", sum[:12])
}

func aiAttemptMetadata(command string, index int, primary, provider, idempotencyKey string, allowFailover bool) map[string]string {
	metadata := map[string]string{"tool": "gofly", "command": command, "provider_attempt": strconv.Itoa(index + 1)}
	if allowFailover {
		metadata["manual_failover_allowed"] = "true"
		metadata["idempotency_key"] = idempotencyKey
	}
	if index > 0 {
		metadata["manual_failover"] = "true"
		metadata["failover_from"] = primary
		metadata["failover_to"] = provider
	}
	return metadata
}

func aiStreamCommand(args []string) error {
	fs := flag.NewFlagSet("ai stream", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "prompt text")
	provider := fs.String("provider", "", "provider mode; use ai manifest to inspect available providers")
	model := fs.String("model", "", "model label")
	maxInputTokens := fs.Int("max-input-tokens", 0, "maximum cumulative input tokens")
	maxOutputTokens := fs.Int("max-output-tokens", 0, "maximum cumulative output tokens")
	maxTotalTokens := fs.Int("max-total-tokens", 0, "maximum cumulative total tokens")
	rateLimitPerSecond := fs.Int("rate-limit", 0, "maximum LLM calls per second; zero disables rate limiting")
	rateLimitBurst := fs.Int("rate-burst", 0, "LLM rate limit burst; zero uses rate-limit")
	timeoutText := fs.String("timeout", "", "provider call timeout, for example 2s or 500ms")
	configPath := fs.String("config", "", "gofly config file path")
	dir := fs.String("dir", ".", "service root used to resolve .gofly/config.json when --config is omitted")
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output newline-delimited JSON envelopes")
	dryRun := fs.Bool("dry-run", false, "print the governance plan without invoking the provider")
	plan := fs.Bool("plan", false, "alias for --dry-run")
	allowFailover := fs.Bool("allow-failover", false, "manually retry retryable provider start failures against GOFLY_LLM_FAILOVER_PROVIDERS before emitting any stream events")
	failover := fs.Bool("failover", false, "alias for --allow-failover")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai stream accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("%w: --prompt or positional prompt text is required for `gofly ai stream`", errUsage)
	}
	format := strings.ToLower(strings.TrimSpace(*formatName))
	if format == "" {
		format = outputText
	}
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: unsupported --format %q", errUsage, *formatName)
	}
	resolved, err := resolveAICompleteConfig(fs, aiCompleteConfigFlags{
		Provider:           *provider,
		Model:              *model,
		MaxInputTokens:     *maxInputTokens,
		MaxOutputTokens:    *maxOutputTokens,
		MaxTotalTokens:     *maxTotalTokens,
		RateLimitPerSecond: *rateLimitPerSecond,
		RateLimitBurst:     *rateLimitBurst,
		Timeout:            *timeoutText,
		ConfigPath:         *configPath,
		Dir:                *dir,
		AllowFailover:      *allowFailover || *failover,
	})
	if err != nil {
		return err
	}
	inputTokens := llm.EstimateTokens(*prompt)
	if *dryRun || *plan {
		return printAIStreamPlan(resolved, inputTokens, format == outputJSON || *jsonOutput)
	}
	return runAIStream(resolved, *prompt, format == outputJSON || *jsonOutput, "ai.stream", "ai.stream")
}

func runAIStream(resolved aiCompleteConfig, prompt string, forceJSON bool, envelopeCommand, metadataCommand string) error {
	budget := llm.NewTokenBudget(resolved.MaxInputTokens, resolved.MaxOutputTokens, resolved.MaxTotalTokens)
	auditor := llm.NewAuditLogger(slog.New(slog.NewJSONHandler(currentErr(), &slog.HandlerOptions{Level: slog.LevelInfo})), nil)
	options := aiGovernedProviderOptions(resolved, budget, auditor)
	ctx, cancel := aiExecutionContext(resolved)
	defer cancel()
	registry := llm.NewDefaultProviderRegistry()
	providers := []string{resolved.Provider}
	if resolved.AllowFailover {
		providers = append(providers, resolved.FailoverProviders...)
	}
	idempotencyKey := aiFailoverIdempotencyKey(prompt, resolved)
	var stream <-chan llm.StreamEvent
	var providerSpec llm.ProviderSpec
	var failoverUsed bool
	var failoverSource string
	for index, providerName := range providers {
		metadata := aiAttemptMetadata(metadataCommand, index, resolved.Provider, providerName, idempotencyKey, resolved.AllowFailover)
		builtProvider, spec, err := registry.Build(providerName, llm.ProviderConfig{
			Provider: providerName,
			Model:    resolved.Model,
			Secrets:  llm.EnvSecretResolver{},
			Metadata: metadata,
		})
		if err != nil {
			return err
		}
		providerClient := llm.NewGovernedProvider(builtProvider, options...)
		req := llm.Request{Provider: providerName, Model: resolved.Model, Prompt: prompt, MaxOutputTokens: resolved.MaxOutputTokens, Metadata: metadata}
		stream, err = providerClient.Stream(ctx, req)
		if err == nil {
			providerSpec = spec
			failoverUsed = index > 0
			failoverSource = failoverFrom(index, resolved.Provider)
			break
		}
		if !shouldAttemptManualFailover(resolved, index, err) {
			return err
		}
	}
	if stream == nil {
		return fmt.Errorf("%w: stream was not created", llm.ErrProviderRequestFailed)
	}
	jsonStream := forceJSON || outputMode() == outputJSON
	governance := aiCompleteGovernance{
		ProviderMode:         providerSpec.Name,
		ProviderCapabilities: providerSpec.Capabilities,
		TelemetryFields:      aiLLMTelemetryFields(),
		FailoverProviders:    resolved.FailoverProviders,
		FailoverMode:         aiFailoverMode(resolved.FailoverProviders, resolved.AllowFailover),
		FailoverAllowed:      resolved.AllowFailover && len(resolved.FailoverProviders) > 0,
		FailoverUsed:         failoverUsed,
		FailoverFrom:         failoverSource,
		IdempotencyKeySet:    resolved.AllowFailover && len(resolved.FailoverProviders) > 0,
		NetworkAccess:        providerSpec.NetworkAccess,
		RequiresSecrets:      providerSpec.RequiresSecrets,
		SecretSource:         "environment",
		Redacted:             true,
		BudgetEnforced:       resolved.MaxInputTokens > 0 || resolved.MaxOutputTokens > 0 || resolved.MaxTotalTokens > 0,
		RateLimited:          resolved.RateLimitPerSecond > 0,
		AuditLogged:          true,
	}
	index := 0
	printedText := false
	for event := range stream {
		if event.Err != nil {
			if jsonStream && outputMode() != outputJSON {
				_ = printJSONLine(jsonEnvelope{OK: false, Command: envelopeCommand, Version: Version, Error: classifyJSONError(event.Err)})
			}
			return event.Err
		}
		result := aiStreamEventResult{Provider: providerSpec.Name, Model: resolved.Model, Index: index, Delta: event.Delta, Done: event.Done, Usage: event.Usage, Budget: budget.Snapshot(), Governance: governance}
		if jsonStream {
			if err := printJSONLine(jsonEnvelope{OK: true, Command: envelopeCommand, Version: Version, Data: result}); err != nil {
				return err
			}
		} else if event.Delta != "" {
			cliOutputIf(event.Delta)
			printedText = true
		}
		index++
	}
	if !jsonStream && printedText {
		cliOutputlnIf()
	}
	return nil
}

type aiCompleteConfigFlags struct {
	Provider           string
	Model              string
	AllowFailover      bool
	MaxInputTokens     int
	MaxOutputTokens    int
	MaxTotalTokens     int
	RateLimitPerSecond int
	RateLimitBurst     int
	Timeout            string
	ConfigPath         string
	Dir                string
}

func resolveAICompleteConfig(fs *flag.FlagSet, flags aiCompleteConfigFlags) (aiCompleteConfig, error) {
	path := flags.ConfigPath
	if path == "" {
		base := flags.Dir
		if base == "" {
			base = "."
		}
		path = filepath.Join(base, generator.DefaultConfigFile)
	}
	cfg, err := generator.LoadConfig(path)
	if err != nil {
		return aiCompleteConfig{}, err
	}
	resolved := aiCompleteConfig{Provider: "noop", Model: "noop", ConfigPath: path}
	if cfg != nil && cfg.LLM != nil {
		resolved.Provider = cfg.LLM.Provider
		resolved.Model = cfg.LLM.Model
		resolved.MaxInputTokens = cfg.LLM.MaxInputTokens
		resolved.MaxOutputTokens = cfg.LLM.MaxOutputTokens
		resolved.MaxTotalTokens = cfg.LLM.MaxTotalTokens
		resolved.RateLimitPerSecond = cfg.LLM.RateLimitPerSecond
		resolved.RateLimitBurst = cfg.LLM.RateLimitBurst
		if cfg.LLM.Timeout != "" {
			timeout, err := time.ParseDuration(cfg.LLM.Timeout)
			if err != nil {
				return aiCompleteConfig{}, fmt.Errorf("%w: invalid llm.timeout %q: %v", errUsage, cfg.LLM.Timeout, err)
			}
			resolved.Timeout = timeout
		}
	}
	if err := applyAICompleteEnv(&resolved); err != nil {
		return aiCompleteConfig{}, err
	}
	if err := applyAICompleteFlagOverlay(fs, flags, &resolved); err != nil {
		return aiCompleteConfig{}, err
	}
	return normalizeAICompleteConfig(resolved)
}

func applyAICompleteEnv(cfg *aiCompleteConfig) error {
	if value := os.Getenv("GOFLY_LLM_PROVIDER"); value != "" {
		cfg.Provider = value
	}
	if value := os.Getenv("GOFLY_LLM_MODEL"); value != "" {
		cfg.Model = value
	}
	if value := os.Getenv("GOFLY_LLM_FAILOVER_PROVIDERS"); value != "" {
		cfg.FailoverProviders = parseAIProviderList(value)
	}
	intEnvs := []struct {
		name string
		set  func(int)
	}{
		{name: "GOFLY_LLM_MAX_INPUT_TOKENS", set: func(v int) { cfg.MaxInputTokens = v }},
		{name: "GOFLY_LLM_MAX_OUTPUT_TOKENS", set: func(v int) { cfg.MaxOutputTokens = v }},
		{name: "GOFLY_LLM_MAX_TOTAL_TOKENS", set: func(v int) { cfg.MaxTotalTokens = v }},
		{name: "GOFLY_LLM_RATE_LIMIT", set: func(v int) { cfg.RateLimitPerSecond = v }},
		{name: "GOFLY_LLM_RATE_BURST", set: func(v int) { cfg.RateLimitBurst = v }},
	}
	for _, env := range intEnvs {
		value := os.Getenv(env.name)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%w: %s must be an integer", errUsage, env.name)
		}
		env.set(parsed)
	}
	if value := os.Getenv("GOFLY_LLM_TIMEOUT"); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("%w: GOFLY_LLM_TIMEOUT must be a duration", errUsage)
		}
		cfg.Timeout = timeout
	}
	return nil
}

func applyAICompleteFlagOverlay(fs *flag.FlagSet, flags aiCompleteConfigFlags, cfg *aiCompleteConfig) error {
	if flagProvided(fs, "provider") {
		cfg.Provider = flags.Provider
	}
	if flagProvided(fs, "model") {
		cfg.Model = flags.Model
	}
	if flagProvided(fs, "allow-failover") || flagProvided(fs, "failover") {
		cfg.AllowFailover = flags.AllowFailover
	}
	if flagProvided(fs, "max-input-tokens") {
		cfg.MaxInputTokens = flags.MaxInputTokens
	}
	if flagProvided(fs, "max-output-tokens") {
		cfg.MaxOutputTokens = flags.MaxOutputTokens
	}
	if flagProvided(fs, "max-total-tokens") {
		cfg.MaxTotalTokens = flags.MaxTotalTokens
	}
	if flagProvided(fs, "rate-limit") {
		cfg.RateLimitPerSecond = flags.RateLimitPerSecond
	}
	if flagProvided(fs, "rate-burst") {
		cfg.RateLimitBurst = flags.RateLimitBurst
	}
	if flagProvided(fs, "timeout") {
		cfg.Timeout = 0
		if flags.Timeout != "" {
			timeout, err := time.ParseDuration(flags.Timeout)
			if err != nil {
				return fmt.Errorf("%w: invalid --timeout %q: %v", errUsage, flags.Timeout, err)
			}
			cfg.Timeout = timeout
		}
	}
	return nil
}

func normalizeAICompleteConfig(cfg aiCompleteConfig) (aiCompleteConfig, error) {
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfg.Provider == "" {
		cfg.Provider = "noop"
	}
	registry := llm.NewDefaultProviderRegistry()
	spec, ok := registry.Spec(cfg.Provider)
	if !ok {
		return aiCompleteConfig{}, fmt.Errorf("%w: %w: %q; available providers: %s", errUsage, llm.ErrProviderNotFound, cfg.Provider, strings.Join(registry.ProviderNames(), ","))
	}
	failoverProviders, err := normalizeAIFailoverProviders(registry, cfg.Provider, cfg.FailoverProviders)
	if err != nil {
		return aiCompleteConfig{}, err
	}
	cfg.FailoverProviders = failoverProviders
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = spec.DefaultModel
	}
	if cfg.MaxInputTokens < 0 || cfg.MaxOutputTokens < 0 || cfg.MaxTotalTokens < 0 {
		return aiCompleteConfig{}, fmt.Errorf("%w: token budgets must be non-negative", errUsage)
	}
	if cfg.RateLimitPerSecond < 0 || cfg.RateLimitBurst < 0 {
		return aiCompleteConfig{}, fmt.Errorf("%w: rate limit values must be non-negative", errUsage)
	}
	if cfg.RateLimitPerSecond == 0 {
		cfg.RateLimitBurst = 0
	}
	return cfg, nil
}

func parseAIProviderList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	providers := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			providers = append(providers, part)
		}
	}
	return providers
}

func normalizeAIFailoverProviders(registry *llm.ProviderRegistry, primary string, providers []string) ([]string, error) {
	if len(providers) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{primary: {}}
	normalized := make([]string, 0, len(providers))
	for _, provider := range providers {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		if _, ok := registry.Spec(name); !ok {
			return nil, fmt.Errorf("%w: %w: failover provider %q; available providers: %s", errUsage, llm.ErrProviderNotFound, name, strings.Join(registry.ProviderNames(), ","))
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	return normalized, nil
}

func printAICompletePlan(config aiCompleteConfig, inputTokens int, forceJSON bool) error {
	registry := llm.NewDefaultProviderRegistry()
	spec, _ := registry.Spec(config.Provider)
	inputs := map[string]string{
		"provider":                config.Provider,
		"model":                   config.Model,
		"configPath":              config.ConfigPath,
		"estimatedInputTokens":    fmt.Sprint(inputTokens),
		"maxInputTokens":          fmt.Sprint(config.MaxInputTokens),
		"maxOutputTokens":         fmt.Sprint(config.MaxOutputTokens),
		"maxTotalTokens":          fmt.Sprint(config.MaxTotalTokens),
		"rateLimit":               fmt.Sprint(config.RateLimitPerSecond),
		"rateBurst":               fmt.Sprint(config.RateLimitBurst),
		"timeout":                 config.Timeout.String(),
		"networkAccess":           fmt.Sprint(spec.NetworkAccess),
		"requiresSecrets":         fmt.Sprint(spec.RequiresSecrets),
		"secretSource":            "environment",
		"providerCapabilities":    strings.Join(spec.Capabilities, ","),
		"providerSecretEnvVars":   strings.Join(spec.SecretEnvVars, ","),
		"providerConfigEnvVars":   strings.Join(spec.ConfigEnvVars, ","),
		"providerSecretsResolved": "not-checked-in-dry-run",
		"failoverMode":            aiFailoverMode(config.FailoverProviders, config.AllowFailover),
		"failoverProviders":       strings.Join(config.FailoverProviders, ","),
		"failoverEnvVar":          "GOFLY_LLM_FAILOVER_PROVIDERS",
		"failoverAutomatic":       "false",
		"failoverAllowed":         fmt.Sprint(config.AllowFailover && len(config.FailoverProviders) > 0),
		"failoverIdempotency":     aiFailoverIdempotencyDisclosure(config),
	}
	warnings := []string{"dry-run does not call an LLM provider and never prints raw prompt text"}
	warnings = append(warnings, aiFailoverWarnings(config.FailoverProviders)...)
	if spec.RequiresSecrets {
		warnings = append(warnings, "provider credentials are resolved from environment variables only and are not read from .gofly/config.json")
	}
	if spec.NetworkAccess {
		warnings = append(warnings, "provider may perform network access when dry-run is disabled; endpoint settings are disclosed only as environment variable names")
	}
	nextActions := []string{"run without --dry-run to execute the governed provider"}
	if spec.RequiresSecrets {
		nextActions = append([]string{"export the required provider secret environment variables before executing without --dry-run"}, nextActions...)
	}
	return printCLIPlan("ai.complete", cliPlan{
		Command:           "ai complete",
		DryRun:            true,
		MutatesFilesystem: false,
		Inputs:            inputs,
		Actions: []cliPlanAction{
			{Operation: "estimate-tokens", Target: "prompt", Description: "estimate input tokens without storing or printing prompt text", RiskLevel: "read"},
			{Operation: "apply-governance", Target: "github.com/imajinyun/gofly/core/llm", Description: "apply token budget, redaction and audit controls before provider invocation", RiskLevel: "read"},
			{Operation: "plan-provider-failover", Target: strings.Join(config.FailoverProviders, ","), Description: aiFailoverPlanDescription(config.FailoverProviders, config.AllowFailover), RiskLevel: "read"},
			{Operation: "invoke-provider", Target: config.Provider, Description: aiProviderPlanDescription(spec), RiskLevel: "read"},
		},
		Warnings:    warnings,
		NextActions: nextActions,
	}, forceJSON)
}

func printAIStreamPlan(config aiCompleteConfig, inputTokens int, forceJSON bool) error {
	return printAIStreamPlanFor("ai.stream", "ai stream", config, inputTokens, forceJSON)
}

func printAIStreamPlanFor(envelopeCommand, displayCommand string, config aiCompleteConfig, inputTokens int, forceJSON bool) error {
	registry := llm.NewDefaultProviderRegistry()
	spec, _ := registry.Spec(config.Provider)
	inputs := map[string]string{
		"provider":                config.Provider,
		"model":                   config.Model,
		"configPath":              config.ConfigPath,
		"estimatedInputTokens":    fmt.Sprint(inputTokens),
		"maxInputTokens":          fmt.Sprint(config.MaxInputTokens),
		"maxOutputTokens":         fmt.Sprint(config.MaxOutputTokens),
		"maxTotalTokens":          fmt.Sprint(config.MaxTotalTokens),
		"rateLimit":               fmt.Sprint(config.RateLimitPerSecond),
		"rateBurst":               fmt.Sprint(config.RateLimitBurst),
		"timeout":                 config.Timeout.String(),
		"networkAccess":           fmt.Sprint(spec.NetworkAccess),
		"requiresSecrets":         fmt.Sprint(spec.RequiresSecrets),
		"secretSource":            "environment",
		"providerCapabilities":    strings.Join(spec.Capabilities, ","),
		"providerSecretEnvVars":   strings.Join(spec.SecretEnvVars, ","),
		"providerConfigEnvVars":   strings.Join(spec.ConfigEnvVars, ","),
		"providerSecretsResolved": "not-checked-in-dry-run",
		"failoverMode":            aiFailoverMode(config.FailoverProviders, config.AllowFailover),
		"failoverProviders":       strings.Join(config.FailoverProviders, ","),
		"failoverEnvVar":          "GOFLY_LLM_FAILOVER_PROVIDERS",
		"failoverAutomatic":       "false",
		"failoverAllowed":         fmt.Sprint(config.AllowFailover && len(config.FailoverProviders) > 0),
		"failoverIdempotency":     aiFailoverIdempotencyDisclosure(config),
	}
	warnings := []string{"dry-run does not call an LLM provider and never prints raw prompt text", "JSON stream mode emits one JSON envelope per event"}
	warnings = append(warnings, aiFailoverWarnings(config.FailoverProviders)...)
	if spec.RequiresSecrets {
		warnings = append(warnings, "provider credentials are resolved from environment variables only and are not read from .gofly/config.json")
	}
	if spec.NetworkAccess {
		warnings = append(warnings, "provider may perform network access when dry-run is disabled; endpoint settings are disclosed only as environment variable names")
	}
	nextActions := []string{"run without --dry-run to execute the governed streaming provider"}
	if spec.RequiresSecrets {
		nextActions = append([]string{"export the required provider secret environment variables before executing without --dry-run"}, nextActions...)
	}
	return printCLIPlan(envelopeCommand, cliPlan{
		Command:           displayCommand,
		DryRun:            true,
		MutatesFilesystem: false,
		Inputs:            inputs,
		Actions: []cliPlanAction{
			{Operation: "estimate-tokens", Target: "prompt", Description: "estimate input tokens without storing or printing prompt text", RiskLevel: "read"},
			{Operation: "apply-governance", Target: "github.com/imajinyun/gofly/core/llm", Description: "apply token budget, redaction, event size limits and audit controls before provider streaming", RiskLevel: "read"},
			{Operation: "plan-provider-failover", Target: strings.Join(config.FailoverProviders, ","), Description: aiFailoverPlanDescription(config.FailoverProviders, config.AllowFailover), RiskLevel: "read"},
			{Operation: "invoke-stream-provider", Target: config.Provider, Description: aiProviderPlanDescription(spec), RiskLevel: "read"},
		},
		Warnings:    warnings,
		NextActions: nextActions,
	}, forceJSON)
}

func aiFailoverMode(providers []string, allow bool) string {
	if len(providers) == 0 {
		return "disabled"
	}
	if allow {
		return "manual"
	}
	return "plan-only"
}

func aiFailoverWarnings(providers []string) []string {
	if len(providers) == 0 {
		return nil
	}
	return []string{"GOFLY_LLM_FAILOVER_PROVIDERS is advisory and only disclosed in plans/governance; automatic provider switching is intentionally disabled"}
}

func aiFailoverPlanDescription(providers []string, allow bool) string {
	if len(providers) == 0 {
		return "no failover providers configured; runtime will not switch providers automatically"
	}
	if allow {
		return "manually retry retryable provider failures against declared fallback candidates with shared budget and audit metadata"
	}
	return "declare fallback candidates for operator review without automatic provider switching"
}

func aiFailoverIdempotencyDisclosure(config aiCompleteConfig) string {
	if !config.AllowFailover || len(config.FailoverProviders) == 0 {
		return "not-enabled"
	}
	return "stable per command execution and attached only to governed attempt metadata"
}

func aiProviderPlanDescription(spec llm.ProviderSpec) string {
	if spec.Name == "" {
		return "invoke selected provider when dry-run is disabled"
	}
	if spec.NetworkAccess {
		return "invoke network-capable provider when dry-run is disabled after environment-only secret and endpoint validation"
	}
	return "invoke deterministic built-in provider when dry-run is disabled"
}
