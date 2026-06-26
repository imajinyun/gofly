package command

import (
	"fmt"
	"os"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

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
