package command

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
	"github.com/imajinyun/gofly/core/llm"
)

// checkAIDoctorProviders checks registered providers and their capabilities.
func checkAIDoctorProviders(registry *llm.ProviderRegistry) []aiDoctorItem {
	items := make([]aiDoctorItem, 0)
	for _, spec := range registry.Specs() {
		capStr := strings.Join(spec.Capabilities, ", ")
		msg := fmt.Sprintf("built-in=%t network=%t capabilities=[%s]", spec.BuiltIn, spec.NetworkAccess, capStr)
		if spec.RequiresSecrets {
			msg += fmt.Sprintf(" secrets=[%s]", strings.Join(spec.SecretEnvVars, ", "))
		}
		if len(spec.ConfigEnvVars) > 0 {
			msg += fmt.Sprintf(" config=[%s]", strings.Join(spec.ConfigEnvVars, ", "))
		}
		if len(spec.Models) > 0 {
			modelDescs := make([]string, 0, len(spec.Models))
			for _, m := range spec.Models {
				modelCapStr := strings.Join(m.Capabilities, ", ")
				modelDescs = append(modelDescs, fmt.Sprintf("%s[%s]", m.Name, modelCapStr))
			}
			msg += fmt.Sprintf(" models=%s", strings.Join(modelDescs, " "))
		}
		status := "info"
		if !spec.BuiltIn {
			status = "info"
		}
		items = append(items, aiDoctorItem{
			Name:    "provider." + spec.Name,
			Status:  status,
			Message: msg,
		})
	}
	if len(items) == 0 {
		items = append(items, aiDoctorItem{
			Name:    "provider.registry",
			Status:  "warn",
			Message: "no providers registered",
		})
	}
	return items
}

// checkAIDoctorEnvVars checks which GOFLY_LLM_* environment variables are set.
func checkAIDoctorEnvVars() []aiDoctorItem {
	llmEnvVars := []string{
		"GOFLY_LLM_PROVIDER",
		"GOFLY_LLM_MODEL",
		"GOFLY_LLM_MAX_INPUT_TOKENS",
		"GOFLY_LLM_MAX_OUTPUT_TOKENS",
		"GOFLY_LLM_MAX_TOTAL_TOKENS",
		"GOFLY_LLM_RATE_LIMIT",
		"GOFLY_LLM_RATE_BURST",
		"GOFLY_LLM_TIMEOUT",
		"GOFLY_LLM_FAILOVER_PROVIDERS",
		"GOFLY_LLM_OPENAI_API_KEY",
		"GOFLY_LLM_OPENAI_BASE_URL",
		"GOFLY_LLM_OPENAI_ALLOWED_HOSTS",
		"GOFLY_LLM_OPENAI_MAX_RESPONSE_BYTES",
	}
	items := make([]aiDoctorItem, 0, len(llmEnvVars))
	for _, env := range llmEnvVars {
		value := os.Getenv(env)
		if value == "" {
			items = append(items, aiDoctorItem{
				Name:   "env." + env,
				Status: "info",
			})
			continue
		}
		status := "ok"
		msg := value
		if strings.Contains(env, "API_KEY") || strings.Contains(env, "SECRET") || strings.Contains(env, "TOKEN") {
			msg = "<set>"
		}
		items = append(items, aiDoctorItem{
			Name:    "env." + env,
			Status:  status,
			Message: msg,
		})
	}
	return items
}

// checkAIDoctorSecrets checks which provider-required secrets are resolvable.
func checkAIDoctorSecrets(registry *llm.ProviderRegistry) []aiDoctorItem {
	items := make([]aiDoctorItem, 0)
	resolver := llm.EnvSecretResolver{}
	for _, spec := range registry.Specs() {
		if !spec.RequiresSecrets || len(spec.SecretEnvVars) == 0 {
			continue
		}
		for _, env := range spec.SecretEnvVars {
			_, ok := resolver.LookupSecret(env)
			if ok {
				items = append(items, aiDoctorItem{
					Name:    "secret." + spec.Name + "." + env,
					Status:  "ok",
					Message: "resolved",
				})
			} else {
				items = append(items, aiDoctorItem{
					Name:    "secret." + spec.Name + "." + env,
					Status:  "fail",
					Message: "not set — provider " + spec.Name + " will fail on Build()",
				})
			}
		}
	}
	if len(items) == 0 {
		items = append(items, aiDoctorItem{
			Name:    "secret.registry",
			Status:  "info",
			Message: "no provider requires secrets",
		})
	}
	return items
}

// checkAIDoctorFailover checks the failover configuration.
func checkAIDoctorFailover(registry *llm.ProviderRegistry) aiDoctorItem {
	failoverEnv := os.Getenv("GOFLY_LLM_FAILOVER_PROVIDERS")
	if failoverEnv == "" {
		return aiDoctorItem{
			Name:    "failover",
			Status:  "info",
			Message: "GOFLY_LLM_FAILOVER_PROVIDERS not set; failover is disabled",
		}
	}
	providers := parseAIProviderList(failoverEnv)
	if len(providers) == 0 {
		return aiDoctorItem{
			Name:    "failover",
			Status:  "warn",
			Message: "GOFLY_LLM_FAILOVER_PROVIDERS set but empty after parsing",
		}
	}
	valid := make([]string, 0)
	invalid := make([]string, 0)
	for _, p := range providers {
		if registry.ProviderSupportsCapability(p, "complete") || registry.ProviderSupportsCapability(p, "stream") {
			valid = append(valid, p)
		} else {
			invalid = append(invalid, p)
		}
	}
	parts := make([]string, 0, 2)
	if len(valid) > 0 {
		parts = append(parts, fmt.Sprintf("valid=%s", strings.Join(valid, ",")))
	}
	if len(invalid) > 0 {
		parts = append(parts, fmt.Sprintf("invalid=%s", strings.Join(invalid, ",")))
	}
	status := "ok"
	if len(invalid) > 0 {
		status = "warn"
	}
	return aiDoctorItem{
		Name:    "failover",
		Status:  status,
		Message: strings.Join(parts, "; "),
	}
}

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
