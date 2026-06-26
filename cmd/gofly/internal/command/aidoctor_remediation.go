package command

import "strings"

func enrichAIDoctorItems(items []aiDoctorItem) []aiDoctorItem {
	enriched := make([]aiDoctorItem, len(items))
	for i, item := range items {
		enriched[i] = enrichAIDoctorItem(item)
	}
	return enriched
}

func enrichAIDoctorItem(item aiDoctorItem) aiDoctorItem {
	if item.Severity == "" {
		item.Severity = aiDoctorSeverity(item)
	}
	if len(item.NextActions) == 0 {
		item.NextActions = aiDoctorNextActions(item)
	}
	return item
}

func aiDoctorSeverity(item aiDoctorItem) string {
	switch item.Status {
	case "fail":
		return "high"
	case "warn":
		return "medium"
	case "ok":
		return "info"
	default:
		return "info"
	}
}

func aiDoctorNextActions(item aiDoctorItem) []string {
	switch {
	case strings.HasPrefix(item.Name, "secret.") && item.Status == "fail":
		parts := strings.Split(item.Name, ".")
		env := "documented provider secret environment variable"
		if len(parts) >= 3 {
			env = parts[len(parts)-1]
		}
		return []string{
			"set environment variable " + env + " before executing network provider calls",
			"run `gofly ai doctor --json` again to verify the secret is resolvable without printing its value",
			"inspect `gofly ai manifest --format json` for provider secretEnvVars",
		}
	case item.Name == "failover" && item.Status == "warn":
		return []string{
			"remove invalid providers from GOFLY_LLM_FAILOVER_PROVIDERS",
			"inspect `gofly ai manifest --format json` for eligibleCompleteSpecs and eligibleStreamSpecs",
			"rerun ai complete or ai stream with --allow-failover only after fallback providers are valid",
		}
	case item.Name == "failover" && item.Status == "info":
		return []string{"set GOFLY_LLM_FAILOVER_PROVIDERS only when manual failover is required"}
	case item.Name == "config" && item.Status == "info":
		return []string{"run `gofly config init --dir . --dry-run` to preview creating a local configuration file"}
	case item.Name == "cache" && item.Status == "info":
		return []string{"set GOFLY_LLM_CACHE_TTL and GOFLY_LLM_CACHE_MAX_SIZE only when response caching is desired"}
	case item.Name == "telemetry":
		return []string{
			"emit only low-cardinality LLM telemetry fields such as provider, model, status, error_class and token counts",
			"do not add prompt, completion, raw metadata, headers or secret values to metrics, traces or audit fields",
			"inspect `gofly ai manifest --format json` llmGovernance.observabilityPolicy before adding new telemetry labels",
		}
	case item.Name == "cost":
		return []string{
			"use JSON usage.totalTokens and budget snapshots before retrying or enabling failover",
			"configure external provider/model pricing before displaying currency cost estimates",
			"treat manual failover attempts as additive token and cost risk unless an attempt returned zero usage",
		}
	case item.Name == "provider.registry" && item.Status == "warn":
		return []string{"register at least one LLM provider before executing governed AI calls"}
	default:
		return nil
	}
}
