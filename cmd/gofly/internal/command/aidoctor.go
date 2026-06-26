package command

import (
	"flag"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

type aiDoctorReport struct {
	Version   string         `json:"version"`
	Providers []aiDoctorItem `json:"providers"`
	EnvVars   []aiDoctorItem `json:"envVars"`
	Secrets   []aiDoctorItem `json:"secrets"`
	Failover  aiDoctorItem   `json:"failover"`
	Config    aiDoctorItem   `json:"config"`
	Cache     aiDoctorItem   `json:"cache"`
	Telemetry aiDoctorItem   `json:"telemetry"`
	Cost      aiDoctorItem   `json:"cost"`
	Summary   string         `json:"summary"`
}

type aiDoctorItem struct {
	Name        string   `json:"name"`
	Status      string   `json:"status"` // ok, warn, fail, info
	Severity    string   `json:"severity,omitempty"`
	Message     string   `json:"message,omitempty"`
	NextActions []string `json:"nextActions,omitempty"`
}

func aiDoctorCommand(args []string) error {
	if printCommandHelp("ai doctor", args) {
		return nil
	}
	fs := flag.NewFlagSet("ai doctor", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print diagnostic report as JSON")
	if _, err := parseInterspersedFlags(fs, args); err != nil {
		return err
	}

	report := runAIDoctor()
	if *jsonOutput || outputMode() == outputJSON {
		return printJSONEnvelope("ai.doctor", report)
	}
	printAIDoctorReport(report)
	return nil
}

func runAIDoctor() aiDoctorReport {
	registry := llm.NewDefaultProviderRegistry()

	providers := checkAIDoctorProviders(registry)
	envVars := checkAIDoctorEnvVars()
	secrets := checkAIDoctorSecrets(registry)
	failover := checkAIDoctorFailover(registry)
	config := checkAIDoctorConfig()
	cache := checkAIDoctorCache()
	telemetry := checkAIDoctorTelemetry()
	cost := checkAIDoctorCost()
	providers = enrichAIDoctorItems(providers)
	envVars = enrichAIDoctorItems(envVars)
	secrets = enrichAIDoctorItems(secrets)
	failover = enrichAIDoctorItem(failover)
	config = enrichAIDoctorItem(config)
	cache = enrichAIDoctorItem(cache)
	telemetry = enrichAIDoctorItem(telemetry)
	cost = enrichAIDoctorItem(cost)

	var warns, fails int
	for _, group := range [][]aiDoctorItem{providers, envVars, secrets, {failover}, {config}, {cache}, {telemetry}, {cost}} {
		for _, item := range group {
			switch item.Status {
			case "warn":
				warns++
			case "fail":
				fails++
			}
		}
	}

	summary := "all AI subsystem checks passed"
	if fails > 0 {
		summary = fmt.Sprintf("%d check(s) failed, %d warning(s)", fails, warns)
	} else if warns > 0 {
		summary = fmt.Sprintf("%d warning(s)", warns)
	}

	return aiDoctorReport{
		Version:   Version,
		Providers: providers,
		EnvVars:   envVars,
		Secrets:   secrets,
		Failover:  failover,
		Config:    config,
		Cache:     cache,
		Telemetry: telemetry,
		Cost:      cost,
		Summary:   summary,
	}
}

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
