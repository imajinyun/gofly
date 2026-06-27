package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

func aiCompleteCommand(args []string) error {
	fs := flag.NewFlagSet("ai complete", flag.ContinueOnError)
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
	outputFlags := registerCLIOutputFlags(fs, cliOutputFlagOptions{JSONUsage: "output JSON envelope"})
	preview := registerDryRunPlanFlags(fs, "print the governance plan without invoking the provider")
	stream := fs.Bool("stream", false, "stream completion events; compatible alias for `gofly ai stream`")
	allowFailover := fs.Bool("allow-failover", false, "manually retry retryable provider failures against GOFLY_LLM_FAILOVER_PROVIDERS")
	failover := fs.Bool("failover", false, "alias for --allow-failover")
	remaining, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return err
	}
	if *prompt == "" && len(remaining) > 0 {
		*prompt = strings.Join(remaining, " ")
	} else if len(remaining) > 0 {
		return fmt.Errorf("%w: ai complete accepts either --prompt or positional prompt text, not both", errUsage)
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("%w: --prompt or positional prompt text is required for `gofly ai complete`", errUsage)
	}
	format, err := outputFlags.normalizedFormat(outputText)
	if err != nil {
		return err
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
	req := llm.Request{Provider: resolved.Provider, Model: resolved.Model, Prompt: *prompt, MaxOutputTokens: resolved.MaxOutputTokens, Metadata: map[string]string{"tool": "gofly", "command": "ai.complete"}}
	inputTokens := llm.EstimateTokens(*prompt)
	if *stream {
		if preview.enabled() {
			return printAIStreamPlanFor("ai.complete", "ai complete --stream", resolved, inputTokens, outputFlags.useJSON(format))
		}
		return runAIStream(resolved, *prompt, outputFlags.useJSON(format), "ai.complete", "ai.complete")
	}
	if preview.enabled() {
		return printAICompletePlan(resolved, inputTokens, outputFlags.useJSON(format))
	}
	resp, providerSpec, budget, failoverUsed, failoverFrom, err := runAICompleteWithFailover(resolved, req, *prompt)
	if err != nil {
		return err
	}
	warnings := []string{}
	if providerSpec.Name == "noop" {
		warnings = append(warnings, "built-in noop provider does not call external LLM services or return generated text")
	}
	if providerSpec.RequiresSecrets {
		warnings = append(warnings, "provider credentials are resolved from environment variables and are never read from .gofly/config.json or included in output")
	}
	result := aiCompleteResult{
		Provider: providerSpec.Name,
		Model:    resolved.Model,
		Text:     resp.Text,
		Usage:    resp.Usage,
		Budget:   budget.Snapshot(),
		Governance: aiCompleteGovernance{
			ProviderMode:         providerSpec.Name,
			ProviderCapabilities: providerSpec.Capabilities,
			TelemetryFields:      aiLLMTelemetryFields(),
			FailoverProviders:    resolved.FailoverProviders,
			FailoverMode:         aiFailoverMode(resolved.FailoverProviders, resolved.AllowFailover),
			FailoverAllowed:      resolved.AllowFailover && len(resolved.FailoverProviders) > 0,
			FailoverUsed:         failoverUsed,
			FailoverFrom:         failoverFrom,
			IdempotencyKeySet:    resolved.AllowFailover && len(resolved.FailoverProviders) > 0,
			NetworkAccess:        providerSpec.NetworkAccess,
			RequiresSecrets:      providerSpec.RequiresSecrets,
			SecretSource:         "environment",
			Redacted:             true,
			BudgetEnforced:       resolved.MaxInputTokens > 0 || resolved.MaxOutputTokens > 0 || resolved.MaxTotalTokens > 0,
			RateLimited:          resolved.RateLimitPerSecond > 0,
			AuditLogged:          true,
		},
		Metadata: map[string]string{"configPath": resolved.ConfigPath},
		Warnings: warnings,
	}
	if outputFlags.useJSON(format) {
		return printJSONEnvelope("ai.complete", result)
	}
	cliOutputfIf("provider=%s model=%s total_tokens=%d\n", result.Provider, result.Model, result.Usage.TotalTokens)
	if result.Text != "" {
		cliOutputlnIf(result.Text)
		return nil
	}
	cliOutputlnIf("(noop provider returned no text)")
	return nil
}
