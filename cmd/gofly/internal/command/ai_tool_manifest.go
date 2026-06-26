package command

import "github.com/imajinyun/gofly/core/llm"

func buildAIToolManifest() aiToolManifest {
	commands := buildAIToolManifestCommands()
	registry := llm.NewDefaultProviderRegistry()
	return aiToolManifest{
		SchemaVersion: aiToolManifestSchemaVersion,
		Tool:          "gofly",
		Version:       Version,
		Description:   "Machine-readable command manifest for LLM/Agent callers. Prefer --output json or command --json flags and inspect the JSON envelope before acting on results.",
		Invocation:    "gofly <command> [arguments]",
		Docs:          buildAIManifestDocs(),
		Examples:      buildAIManifestExamples(),
		VerifyCommands: []string{
			"make docs-check",
			"make examples-smoke",
			"make test-generated-matrix",
			"make doc-manifest-sync-check",
		},
		Output: aiOutputSchema{
			Mode:        "json-envelope when --output json, --json or --format json is used",
			Envelope:    []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
			ErrorFields: []string{"code", "message", "retryable", "remediation", "details"},
		},
		ControlPlane: buildAIControlPlaneManifest(),
		LLMGovernance: aiLLMGovernance{
			Package:                "github.com/imajinyun/gofly/core/llm",
			Capabilities:           []string{"provider abstraction", "provider registry", "capability manifest", "provider plugin manifest contract", "model-level capability negotiation", "environment-only secret resolver", "token budget", "rate limiting", "prompt and metadata redaction", "structured audit logging", "stream event size limits", "no-op provider", "response caching", "cost-aware token accounting", "low-cardinality observability", "governance pipeline"},
			Resilience:             []string{"circuit breaker", "provider failover", "manual provider failover", "bounded provider responses", "context cancellation", "timeout propagation", "retryability classification", "low-cardinality error classes", "provider status code capture", "HTTP status class classification", "request coalescing"},
			ProviderPluginContract: buildAIProviderPluginContract(),
			TokenBudgetPolicy:      buildAITokenBudgetPolicy(),
			RateLimitPolicy: aiRateLimitPolicy{
				DefaultRate:  0,
				DefaultBurst: 0,
				EnvVarRate:   "GOFLY_LLM_RATE_LIMIT",
				EnvVarBurst:  "GOFLY_LLM_RATE_BURST",
				Strategy:     "token-bucket",
				Consequence:  "requests exceeding the rate limit receive ErrRateLimited and are not forwarded to the provider; configurable per invocation via --rate-limit and --rate-burst flags",
				Configurable: true,
				Scope:        "per-governed-provider-instance; each NewGovernedProvider call creates an independent token bucket",
			},
			OutputContractPolicy: buildAIOutputContractPolicy(),
			ErrorContractPolicy:  buildAIErrorContractPolicy(),
			DataSafetyPolicy:     buildAIDataSafetyPolicy(),
			ToolCallPolicy:       buildAIToolCallPolicy(),
			FailoverPolicy:       buildAIFailoverPolicy(registry),
			ResponseCachePolicy:  buildAIResponseCachePolicy(),
			ObservabilityPolicy:  buildAIObservabilityPolicy(),
			CostPolicy:           buildAICostPolicy(),
			GovernancePipeline:   buildAIGovernancePipeline(),
			AuditFields:          []string{"operation", "provider", "model", "status", "duration", "input_tokens", "output_tokens", "total_tokens", "metadata", "error", "error_class", "retryable", "provider_status_code", "stream_events", "trace_id", "request_id"},
			TelemetryFields:      aiLLMTelemetryFields(),
			DefaultMode:          "redact prompts and metadata before provider calls; never audit raw prompts or completions",
			Providers:            registry.Specs(),
		},
		FeatureLibrary: buildAIFeatureLibraryManifest(),
		Commands:       commands,
	}
}
