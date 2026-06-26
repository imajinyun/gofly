package command

import (
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
