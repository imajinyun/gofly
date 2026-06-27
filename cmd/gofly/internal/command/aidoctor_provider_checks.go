package command

import (
	"fmt"
	"os"
	"strings"

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
		items = append(items, aiDoctorItem{
			Name:    "env." + env,
			Status:  status,
			Message: aiDoctorEnvValueMessage(env, value),
		})
	}
	return items
}

func aiDoctorEnvValueMessage(env, value string) string {
	if value == "" {
		return ""
	}
	if isAIDoctorRedactedEnv(env) {
		return "<set>"
	}
	return value
}

func isAIDoctorRedactedEnv(env string) bool {
	upper := strings.ToUpper(env)
	for _, marker := range []string{
		"API_KEY",
		"SECRET",
		"TOKEN",
		"PASSWORD",
		"CREDENTIAL",
		"BASE_URL",
		"ENDPOINT",
		"HOST",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
