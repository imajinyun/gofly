package command

import (
	"flag"
	"fmt"
	"time"
)

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
