package command

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

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
