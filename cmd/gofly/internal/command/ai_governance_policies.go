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

// buildAIGovernancePipeline returns the ordered 9-stage pipeline that every
// governed LLM call passes through. Optional stages are elided at runtime
// when their preconditions are not met.
func buildAIGovernancePipeline() []aiPipelineStage {
	return []aiPipelineStage{
		{Stage: "request-redaction", Description: "redact secrets and sensitive metadata from the incoming prompt before any further processing", Optional: true},
		{Stage: "rate-limit", Description: "token-bucket rate limiting; rejected with ErrRateLimited if the bucket is empty", Optional: true},
		{Stage: "token-budget", Description: "check cumulative token budget; rejected with token_budget_exceeded if over limit", Optional: true},
		{Stage: "response-cache", Description: "look up response cache; skip provider call and use cached response on hit; coalesce concurrent misses", Optional: true},
		{Stage: "circuit-breaker", Description: "reject call immediately if the provider circuit is open; allow probe requests for half-open recovery", Optional: false},
		{Stage: "provider-call", Description: "forward the request to the LLM provider with configured timeout, retry and failover wrappers", Optional: false},
		{Stage: "usage-accounting", Description: "record token usage (input, output, total) against the token budget and emit usage deltas", Optional: false},
		{Stage: "audit-log", Description: "emit a structured audit log entry with operation, provider, model, status, duration, tokens and error metadata", Optional: false},
		{Stage: "telemetry-emit", Description: "emit low-cardinality metrics and trace fields for observability pipeline; cache_status, error_class, provider_status_code", Optional: false},
	}
}
