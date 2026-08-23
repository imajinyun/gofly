package config

import (
	"fmt"
	"strconv"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// EnsureModelConfig returns cfg.Model, allocating it when missing.
func EnsureModelConfig(cfg *generator.Config) *generator.ModelConfig {
	return ensureModelConfig(cfg)
}

func ensureModelConfig(cfg *generator.Config) *generator.ModelConfig {
	if cfg.Model == nil {
		cfg.Model = &generator.ModelConfig{}
	}
	if cfg.Model.TypesMap == nil {
		cfg.Model.TypesMap = map[string]string{}
	}
	return cfg.Model
}

func ensureLLMConfig(cfg *generator.Config) *generator.LLMConfig {
	if cfg.LLM == nil {
		cfg.LLM = &generator.LLMConfig{Provider: "noop", Model: "noop"}
	}
	return cfg.LLM
}

func parseNonNegativeIntConfigValue(name, value string, usage error) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", usage, name)
	}
	return parsed, nil
}
