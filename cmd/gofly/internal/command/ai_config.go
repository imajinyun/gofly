package command

import (
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
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
