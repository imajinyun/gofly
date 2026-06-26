package command

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

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
