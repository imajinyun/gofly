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

func buildAIResponseCachePolicy() aiResponseCachePolicy {
	return aiResponseCachePolicy{
		DefaultTTL:         "5m",
		DefaultMaxSize:     256,
		CacheKeyComponents: []string{"provider", "model", "prompt", "maxOutputTokens"},
		Hash:               "SHA-256",
		Coalescing:         "request-level; concurrent requests for the same cache key share one inflight provider call",
		Observable:         true,
		CacheScope:         "in-process memory; per-CachingProvider instance",
		CacheUnsupported:   []string{"stream", "embed"},
	}
}

func buildAIObservabilityPolicy() aiObservabilityPolicy {
	return aiObservabilityPolicy{
		Signals:                []string{"structured audit log", "JSON envelope", "stream event envelope", "doctor remediation report"},
		LowCardinalityFields:   []string{"operation", "provider", "model", "status", "error_class", "retryable", "provider_status_class", "provider_status_code", "cache_status", "failover_enabled"},
		ForbiddenFields:        []string{"prompt", "completion", "messages[].content", "metadata raw values", "secret values", "authorization headers", "provider response body"},
		CorrelationFields:      []string{"trace_id", "request_id", "idempotency_key"},
		MetricFieldGuidance:    "emit counters and histograms using only low-cardinality labels; never label metrics with raw prompts, user input, URLs, headers or secret values",
		TraceFieldGuidance:     "trace attributes may include provider/model/status and token counts; raw prompt/completion content stays outside traces",
		AuditCorrelation:       "audit records and JSON envelopes share provider, model, usage, error_class, retryable, provider_status_code, trace_id and request_id fields when available",
		RedactionBoundary:      "redaction occurs before provider calls, audit logging, doctor output and manifest-safe metadata exposure",
		CardinalityGuardrails:  "provider status is reduced to stable classes for automation; high-cardinality details belong in redacted diagnostics, not metrics labels",
		ProviderStatusGuidance: "provider_status_code is optional and bounded to numeric/status class diagnostics; callers should aggregate by provider_status_class or error_class first",
	}
}

func buildAICostPolicy() aiCostPolicy {
	return aiCostPolicy{
		AccountingFields:   []string{"input_tokens", "output_tokens", "total_tokens", "cache_status", "provider", "model", "operation", "failover_attempt"},
		BudgetFields:       []string{"max_input_tokens", "max_output_tokens", "max_total_tokens", "used_input", "used_output", "used_total", "remain_total"},
		CurrencyMode:       "disabled-by-default; manifest exposes token accounting but does not invent currency estimates without an explicit pricing table",
		PricingSource:      "provider/model pricing must come from an operator-maintained table outside secret/config values; unknown prices are reported as unpriced",
		CostDisclosure:     "JSON outputs expose token usage and budget snapshots so agents can estimate cost externally before retrying or expanding prompts",
		FailoverDisclosure: "manual failover records provider/model and shared budget usage per attempt; agents should account fallback attempts as additive token/cost risk",
		CacheAccounting:    "cache hits avoid provider calls but still disclose cached usage; cache_status should be used to distinguish provider spend from served-from-cache responses",
		AgentGuidance: []string{
			"inspect token usage before retrying retryable errors",
			"prefer smaller prompts or explicit max-total-tokens when budget is unknown",
			"treat failover as potentially additional provider spend unless the failed attempt returned zero usage",
			"do not fabricate currency costs for unpriced providers",
		},
		UnpricedProviderPolicy: "token counts remain authoritative; currency fields should be omitted or marked unpriced until an explicit pricing source is configured",
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
