package command

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
)

// checkAIDoctorConfig checks whether the config file exists and has an LLM section.
func checkAIDoctorConfig() aiDoctorItem {
	configPath := filepath.Join(".", generator.DefaultConfigFile)
	cfg, err := generator.LoadConfig(configPath)
	if err == nil && cfg != nil && cfg.LLM != nil {
		data, _ := json.Marshal(cfg.LLM)
		return aiDoctorItem{
			Name:    "config",
			Status:  "ok",
			Message: fmt.Sprintf("%s exists with llm config: %s", generator.DefaultConfigFile, string(data)),
		}
	}

	home, homeErr := os.UserHomeDir()
	if homeErr == nil {
		homeConfigPath := filepath.Join(home, generator.DefaultConfigFile)
		homeCfg, homeErr := generator.LoadConfig(homeConfigPath)
		if homeErr == nil && homeCfg != nil && homeCfg.LLM != nil {
			data, _ := json.Marshal(homeCfg.LLM)
			return aiDoctorItem{
				Name:    "config",
				Status:  "ok",
				Message: fmt.Sprintf("%s exists with llm config: %s", homeConfigPath, string(data)),
			}
		}
	}

	return aiDoctorItem{
		Name:    "config",
		Status:  "info",
		Message: fmt.Sprintf("no %s with llm config found in workdir or home", generator.DefaultConfigFile),
	}
}

// checkAIDoctorCache reports the response caching infrastructure status.
func checkAIDoctorCache() aiDoctorItem {
	envTTL := os.Getenv("GOFLY_LLM_CACHE_TTL")
	envMaxSize := os.Getenv("GOFLY_LLM_CACHE_MAX_SIZE")

	parts := []string{
		"CachingProvider available in core/llm",
		"defaultTTL=5m",
		"defaultMaxSize=256",
	}
	if envTTL != "" {
		parts = append(parts, "env.GOFLY_LLM_CACHE_TTL="+envTTL)
	}
	if envMaxSize != "" {
		parts = append(parts, "env.GOFLY_LLM_CACHE_MAX_SIZE="+envMaxSize)
	}

	status := "info"
	if envTTL != "" || envMaxSize != "" {
		status = "ok"
	}
	return aiDoctorItem{
		Name:    "cache",
		Status:  status,
		Message: strings.Join(parts, "; "),
	}
}

func checkAIDoctorTelemetry() aiDoctorItem {
	fields := aiLLMTelemetryFields()
	parts := []string{
		"structured audit logging available",
		"lowCardinality=" + strings.Join(fields, ","),
		"forbidden=prompt,completion,secret values,provider response body",
	}
	return aiDoctorItem{
		Name:    "telemetry",
		Status:  "ok",
		Message: strings.Join(parts, "; "),
	}
}

func checkAIDoctorCost() aiDoctorItem {
	return aiDoctorItem{
		Name:    "cost",
		Status:  "info",
		Message: "token usage and budget snapshots are exposed; currency pricing is disabled-by-default and unpriced without an operator-maintained pricing table",
	}
}
