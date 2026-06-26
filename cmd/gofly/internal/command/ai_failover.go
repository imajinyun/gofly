package command

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/llm"
)

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
