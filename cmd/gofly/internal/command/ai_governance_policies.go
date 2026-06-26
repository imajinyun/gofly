package command

import (
	"os"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

func buildAIFailoverPolicy(registry *llm.ProviderRegistry) aiFailoverPolicy {
	configured := parseAIProviderList(os.Getenv("GOFLY_LLM_FAILOVER_PROVIDERS"))
	valid := make([]string, 0, len(configured))
	invalid := make([]string, 0)
	configuredSpecs := make([]llm.ProviderSpec, 0, len(configured))
	seen := map[string]struct{}{}
	for _, provider := range configured {
		name := strings.ToLower(strings.TrimSpace(provider))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		spec, ok := registry.Spec(name)
		if !ok {
			invalid = append(invalid, name)
			continue
		}
		valid = append(valid, name)
		configuredSpecs = append(configuredSpecs, spec)
	}
	return aiFailoverPolicy{
		EnvVar:                "GOFLY_LLM_FAILOVER_PROVIDERS",
		Mode:                  aiFailoverMode(valid, false),
		AutomaticSwitching:    false,
		ManualOptInFlags:      []string{"--allow-failover", "--failover"},
		ExecutionGuardrails:   []string{"manual opt-in is required", "only retryable provider failures are eligible", "failover attempts share the same token budget", "attempt metadata includes a stable idempotency key", "stream failover is limited to pre-event provider start failures"},
		ConfiguredProviders:   valid,
		InvalidProviders:      invalid,
		ConfiguredSpecs:       configuredSpecs,
		EligibleCompleteSpecs: registry.SpecsWithCapability("complete"),
		EligibleStreamSpecs:   registry.SpecsWithCapability("stream"),
		EligibleJSONModeSpecs: registry.SpecsWithModelCapability("json-mode"),
		EligibleToolCallSpecs: registry.SpecsWithModelCapability("tool-call"),
	}
}
