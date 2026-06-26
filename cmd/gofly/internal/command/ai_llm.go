package command

import (
	"fmt"
	"log/slog"

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
