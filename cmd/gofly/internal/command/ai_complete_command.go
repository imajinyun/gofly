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
	formatName := fs.String("format", outputText, "output format: text or json")
	jsonOutput := fs.Bool("json", false, "output JSON envelope")
	dryRun := fs.Bool("dry-run", false, "print the governance plan without invoking the provider")
	plan := fs.Bool("plan", false, "alias for --dry-run")
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
	req := llm.Request{Provider: resolved.Provider, Model: resolved.Model, Prompt: *prompt, MaxOutputTokens: resolved.MaxOutputTokens, Metadata: map[string]string{"tool": "gofly", "command": "ai.complete"}}
	inputTokens := llm.EstimateTokens(*prompt)
	if *stream {
		if *dryRun || *plan {
			return printAIStreamPlanFor("ai.complete", "ai complete --stream", resolved, inputTokens, format == outputJSON || *jsonOutput)
		}
		return runAIStream(resolved, *prompt, format == outputJSON || *jsonOutput, "ai.complete", "ai.complete")
	}
	if *dryRun || *plan {
		return printAICompletePlan(resolved, inputTokens, format == outputJSON || *jsonOutput)
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
	if *jsonOutput || outputMode() == outputJSON || format == outputJSON {
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
