package command

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
