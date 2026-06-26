package command

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

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

func parseNonNegativeIntConfigValue(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", errUsage, name)
	}
	return parsed, nil
}

func parseBoolString(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}
