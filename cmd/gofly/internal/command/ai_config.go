package command

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/core/llm"
)

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
