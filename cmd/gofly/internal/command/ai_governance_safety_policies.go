package command

func buildAIDataSafetyPolicy() aiDataSafetyPolicy {
	return aiDataSafetyPolicy{
		SecretResolution:    "environment-only SecretResolver; manifests disclose secret environment variable names but never values",
		Redaction:           "prompts and metadata are redacted before provider calls and audit logging",
		PromptLogging:       "disabled-by-default",
		ResponseLogging:     "disabled-by-default",
		MetadataLogging:     "redacted",
		SecretValueLogging:  "forbidden",
		SensitiveEnvVarMode: "presence/status only; values are never emitted by manifest or doctor output",
		AuditBoundary:       "audit records low-cardinality operational fields, token usage, status, retryability and provider attribution without raw prompt/completion content",
		SafeToExpose:        []string{"provider names", "model names", "capability names", "environment variable names", "token budget limits", "rate limit settings", "provider status classes"},
	}
}

func buildAIToolCallPolicy() aiToolCallPolicy {
	return aiToolCallPolicy{
		DefaultMode:                     "disabled-unless-model-and-command-contract-explicitly-enable-tool-call",
		RequiresModelCapability:         "tool-call",
		AllowedByDefault:                []string{},
		SideEffectToolsRequireApproval:  true,
		ArgumentSchemaValidation:        true,
		DryRunRequiredForMutation:       true,
		AuditToolArguments:              "redacted",
		RejectedToolCallCode:            "tool_call_rejected",
		UnsupportedCapabilityResolution: "select a model from eligibleToolCallSpecs or rerun without tool calling",
	}
}
