package command

import "github.com/imajinyun/gofly/core/llm"

func buildAITokenBudgetPolicy() aiTokenBudgetPolicy {
	return aiTokenBudgetPolicy{
		DefaultMaxInputTokens:  0,
		DefaultMaxOutputTokens: 0,
		DefaultMaxTotalTokens:  0,
		Configurable:           true,
		CLIFlags:               []string{"--max-input-tokens", "--max-output-tokens", "--max-total-tokens"},
		EnvVars:                []string{"GOFLY_LLM_MAX_INPUT_TOKENS", "GOFLY_LLM_MAX_OUTPUT_TOKENS", "GOFLY_LLM_MAX_TOTAL_TOKENS"},
		Enforcement:            "requests exceeding configured token budgets return ErrTokenBudgetExceeded before additional provider work is accepted",
		DeductionPoint:         "token usage is checked by the governed provider after provider usage accounting; dry-run only discloses configured limits",
		FailoverBudgetSharing:  "manual failover attempts share the same TokenBudget instance and idempotency key",
		StreamAccounting:       "streaming responses account provider-emitted usage snapshots; missing usage is represented as zero-valued usage and does not fabricate token counts",
		RejectionCode:          "token_budget_exceeded",
	}
}

func buildAIProviderPluginContract() aiProviderPluginContract {
	return aiProviderPluginContract{
		SchemaVersion:  llm.ProviderPluginManifestSchemaVersion,
		RequiredFields: []string{"schemaVersion", "provider.name", "provider.capabilities", "provider.requiresSecrets", "provider.secretEnvVars", "provider.configEnvVars", "models[].name", "models[].capabilities"},
		SafeFields:     []string{"provider display/default metadata", "environment variable names", "network access boolean", "provider-level capabilities", "model-level capabilities", "embedding dimensions"},
		SecretBoundary: "provider plugin manifests must disclose only secret environment variable names; secret values are resolved at build time from environment-backed SecretResolver",
	}
}

func buildAIOutputContractPolicy() aiOutputContractPolicy {
	return aiOutputContractPolicy{
		EnvelopeFields:          []string{"ok", "command", "version", "data", "error", "diagnostics", "warnings", "nextActions"},
		ErrorFields:             []string{"code", "message", "retryable", "remediation", "details"},
		NextActions:             true,
		JSONMode:                "prefer --json, --output json or --format json for machine-readable calls; text output is human-oriented",
		SchemaValidation:        "manifest declares stable envelope and command output fields; command-specific JSON should be inspected before side effects",
		RetryableErrorSemantics: "retryable errors include low-cardinality classes and nextActions; non-retryable errors should not be retried without user/config changes",
		StreamSemantics:         "stream JSON output is newline-delimited JSON; each line is an independently parseable envelope",
		PartialFailureSemantics: "stream errors are emitted as a final error envelope when possible; failover is limited to failures before any stream event is emitted",
	}
}

func buildAIErrorContractPolicy() aiErrorContractPolicy {
	return aiErrorContractPolicy{
		CodeFormat: "UPPER_SNAKE_CASE stable JSON error.code values; callers must treat unknown future codes as non-retryable unless retryable is true",
		StableCodes: []string{
			"COMMAND_ERROR",
			"USAGE_ERROR",
			"LLM_TOKEN_BUDGET_EXCEEDED",
			"LLM_RATE_LIMITED",
			"LLM_PROVIDER_NOT_FOUND",
			"LLM_PROVIDER_SECRET_MISSING",
			"LLM_PROVIDER_ENDPOINT_REJECTED",
			"LLM_PROVIDER_CONFIG_INVALID",
			"LLM_PROVIDER_CAPABILITY_UNSUPPORTED",
			"LLM_PROVIDER_REQUEST_FAILED",
			"LLM_PROVIDER_RESPONSE_TOO_LARGE",
			"LLM_PROVIDER_ALREADY_REGISTERED",
		},
		RetryableCodes:    []string{"LLM_RATE_LIMITED", "LLM_PROVIDER_REQUEST_FAILED"},
		NonRetryableCodes: []string{"USAGE_ERROR", "LLM_TOKEN_BUDGET_EXCEEDED", "LLM_PROVIDER_NOT_FOUND", "LLM_PROVIDER_SECRET_MISSING", "LLM_PROVIDER_ENDPOINT_REJECTED", "LLM_PROVIDER_CONFIG_INVALID", "LLM_PROVIDER_CAPABILITY_UNSUPPORTED", "LLM_PROVIDER_RESPONSE_TOO_LARGE", "LLM_PROVIDER_ALREADY_REGISTERED"},
		ProviderStatusClasses: []string{
			"auth",
			"rate_limit",
			"client",
			"server",
			"unknown",
		},
		NextActionTypes:         []string{"retry", "run_doctor", "set_env", "choose_provider", "choose_model", "enable_failover", "reduce_prompt", "increase_budget", "inspect_manifest"},
		EnvelopePlacement:       "error details are duplicated only as structured error and top-level nextActions; command data remains omitted on failure",
		DetailsPolicy:           "details must stay low-cardinality and may include provider, statusCode and statusClass; raw provider bodies, prompts, completions and secret values are omitted",
		RetryableSemantics:      "retryable=true means the same command may be retried after waiting or resolving provider availability; retryable=false requires user/config/model changes first",
		ProviderFailureGuidance: "retryable provider request failures include nextActions for retry, optional manual failover and manifest inspection",
	}
}
