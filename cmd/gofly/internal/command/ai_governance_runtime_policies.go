package command

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
