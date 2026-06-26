package command

import (
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

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
