package command

import (
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

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
