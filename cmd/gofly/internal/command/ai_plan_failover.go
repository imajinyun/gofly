package command

import "github.com/imajinyun/gofly/core/llm"

func aiFailoverMode(providers []string, allow bool) string {
	if len(providers) == 0 {
		return "disabled"
	}
	if allow {
		return "manual"
	}
	return "plan-only"
}

func aiFailoverWarnings(providers []string) []string {
	if len(providers) == 0 {
		return nil
	}
	return []string{"GOFLY_LLM_FAILOVER_PROVIDERS is advisory and only disclosed in plans/governance; automatic provider switching is intentionally disabled"}
}

func aiFailoverPlanDescription(providers []string, allow bool) string {
	if len(providers) == 0 {
		return "no failover providers configured; runtime will not switch providers automatically"
	}
	if allow {
		return "manually retry retryable provider failures against declared fallback candidates with shared budget and audit metadata"
	}
	return "declare fallback candidates for operator review without automatic provider switching"
}

func aiFailoverIdempotencyDisclosure(config aiCompleteConfig) string {
	if !config.AllowFailover || len(config.FailoverProviders) == 0 {
		return "not-enabled"
	}
	return "stable per command execution and attached only to governed attempt metadata"
}

func aiProviderPlanDescription(spec llm.ProviderSpec) string {
	if spec.Name == "" {
		return "invoke selected provider when dry-run is disabled"
	}
	if spec.NetworkAccess {
		return "invoke network-capable provider when dry-run is disabled after environment-only secret and endpoint validation"
	}
	return "invoke deterministic built-in provider when dry-run is disabled"
}
