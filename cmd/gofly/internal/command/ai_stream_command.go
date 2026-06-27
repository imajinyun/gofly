package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

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
	outputFlags := registerCLIOutputFlags(fs, cliOutputFlagOptions{JSONUsage: "output newline-delimited JSON envelopes"})
	preview := registerDryRunPlanFlags(fs, "print the governance plan without invoking the provider")
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
	jsonMode := outputFlags.useJSON(format)
	inputTokens := llm.EstimateTokens(*prompt)
	if preview.enabled() {
		return printAIStreamPlan(resolved, inputTokens, jsonMode)
	}
	return runAIStream(resolved, *prompt, jsonMode, "ai.stream", "ai.stream")
}
