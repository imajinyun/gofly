package command

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imajinyun/gofly/cmd/gofly/internal/generator"
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

// checkAIDoctorProviders checks registered providers and their capabilities.
func checkAIDoctorProviders(registry *llm.ProviderRegistry) []aiDoctorItem {
	items := make([]aiDoctorItem, 0)
	for _, spec := range registry.Specs() {
		capStr := strings.Join(spec.Capabilities, ", ")
		msg := fmt.Sprintf("built-in=%t network=%t capabilities=[%s]", spec.BuiltIn, spec.NetworkAccess, capStr)
		if spec.RequiresSecrets {
			msg += fmt.Sprintf(" secrets=[%s]", strings.Join(spec.SecretEnvVars, ", "))
		}
		if len(spec.ConfigEnvVars) > 0 {
			msg += fmt.Sprintf(" config=[%s]", strings.Join(spec.ConfigEnvVars, ", "))
		}
		if len(spec.Models) > 0 {
			modelDescs := make([]string, 0, len(spec.Models))
			for _, m := range spec.Models {
				modelCapStr := strings.Join(m.Capabilities, ", ")
				modelDescs = append(modelDescs, fmt.Sprintf("%s[%s]", m.Name, modelCapStr))
			}
			msg += fmt.Sprintf(" models=%s", strings.Join(modelDescs, " "))
		}
		status := "info"
		if !spec.BuiltIn {
			status = "info"
		}
		items = append(items, aiDoctorItem{
			Name:    "provider." + spec.Name,
			Status:  status,
			Message: msg,
		})
	}
	if len(items) == 0 {
		items = append(items, aiDoctorItem{
			Name:    "provider.registry",
			Status:  "warn",
			Message: "no providers registered",
		})
	}
	return items
}

// checkAIDoctorEnvVars checks which GOFLY_LLM_* environment variables are set.
func checkAIDoctorEnvVars() []aiDoctorItem {
	llmEnvVars := []string{
		"GOFLY_LLM_PROVIDER",
		"GOFLY_LLM_MODEL",
		"GOFLY_LLM_MAX_INPUT_TOKENS",
		"GOFLY_LLM_MAX_OUTPUT_TOKENS",
		"GOFLY_LLM_MAX_TOTAL_TOKENS",
		"GOFLY_LLM_RATE_LIMIT",
		"GOFLY_LLM_RATE_BURST",
		"GOFLY_LLM_TIMEOUT",
		"GOFLY_LLM_FAILOVER_PROVIDERS",
		"GOFLY_LLM_OPENAI_API_KEY",
		"GOFLY_LLM_OPENAI_BASE_URL",
		"GOFLY_LLM_OPENAI_ALLOWED_HOSTS",
		"GOFLY_LLM_OPENAI_MAX_RESPONSE_BYTES",
	}
	items := make([]aiDoctorItem, 0, len(llmEnvVars))
	for _, env := range llmEnvVars {
		value := os.Getenv(env)
		if value == "" {
			items = append(items, aiDoctorItem{
				Name:   "env." + env,
				Status: "info",
			})
			continue
		}
		status := "ok"
		msg := value
		if strings.Contains(env, "API_KEY") || strings.Contains(env, "SECRET") || strings.Contains(env, "TOKEN") {
			msg = "<set>"
		}
		items = append(items, aiDoctorItem{
			Name:    "env." + env,
			Status:  status,
			Message: msg,
		})
	}
	return items
}

// checkAIDoctorSecrets checks which provider-required secrets are resolvable.
func checkAIDoctorSecrets(registry *llm.ProviderRegistry) []aiDoctorItem {
	items := make([]aiDoctorItem, 0)
	resolver := llm.EnvSecretResolver{}
	for _, spec := range registry.Specs() {
		if !spec.RequiresSecrets || len(spec.SecretEnvVars) == 0 {
			continue
		}
		for _, env := range spec.SecretEnvVars {
			_, ok := resolver.LookupSecret(env)
			if ok {
				items = append(items, aiDoctorItem{
					Name:    "secret." + spec.Name + "." + env,
					Status:  "ok",
					Message: "resolved",
				})
			} else {
				items = append(items, aiDoctorItem{
					Name:    "secret." + spec.Name + "." + env,
					Status:  "fail",
					Message: "not set — provider " + spec.Name + " will fail on Build()",
				})
			}
		}
	}
	if len(items) == 0 {
		items = append(items, aiDoctorItem{
			Name:    "secret.registry",
			Status:  "info",
			Message: "no provider requires secrets",
		})
	}
	return items
}

// checkAIDoctorFailover checks the failover configuration.
func checkAIDoctorFailover(registry *llm.ProviderRegistry) aiDoctorItem {
	failoverEnv := os.Getenv("GOFLY_LLM_FAILOVER_PROVIDERS")
	if failoverEnv == "" {
		return aiDoctorItem{
			Name:    "failover",
			Status:  "info",
			Message: "GOFLY_LLM_FAILOVER_PROVIDERS not set; failover is disabled",
		}
	}
	providers := parseAIProviderList(failoverEnv)
	if len(providers) == 0 {
		return aiDoctorItem{
			Name:    "failover",
			Status:  "warn",
			Message: "GOFLY_LLM_FAILOVER_PROVIDERS set but empty after parsing",
		}
	}
	valid := make([]string, 0)
	invalid := make([]string, 0)
	for _, p := range providers {
		if registry.ProviderSupportsCapability(p, "complete") || registry.ProviderSupportsCapability(p, "stream") {
			valid = append(valid, p)
		} else {
			invalid = append(invalid, p)
		}
	}
	parts := make([]string, 0, 2)
	if len(valid) > 0 {
		parts = append(parts, fmt.Sprintf("valid=%s", strings.Join(valid, ",")))
	}
	if len(invalid) > 0 {
		parts = append(parts, fmt.Sprintf("invalid=%s", strings.Join(invalid, ",")))
	}
	status := "ok"
	if len(invalid) > 0 {
		status = "warn"
	}
	return aiDoctorItem{
		Name:    "failover",
		Status:  status,
		Message: strings.Join(parts, "; "),
	}
}

// checkAIDoctorConfig checks whether the config file exists and has an LLM section.
func checkAIDoctorConfig() aiDoctorItem {
	// Check working directory config file
	configPath := filepath.Join(".", generator.DefaultConfigFile)
	cfg, err := generator.LoadConfig(configPath)
	if err == nil && cfg != nil && cfg.LLM != nil {
		data, _ := json.Marshal(cfg.LLM)
		return aiDoctorItem{
			Name:    "config",
			Status:  "ok",
			Message: fmt.Sprintf("%s exists with llm config: %s", generator.DefaultConfigFile, string(data)),
		}
	}

	// Check home directory config file
	home, homeErr := os.UserHomeDir()
	if homeErr == nil {
		homeConfigPath := filepath.Join(home, generator.DefaultConfigFile)
		homeCfg, homeErr := generator.LoadConfig(homeConfigPath)
		if homeErr == nil && homeCfg != nil && homeCfg.LLM != nil {
			data, _ := json.Marshal(homeCfg.LLM)
			return aiDoctorItem{
				Name:    "config",
				Status:  "ok",
				Message: fmt.Sprintf("%s exists with llm config: %s", homeConfigPath, string(data)),
			}
		}
	}

	return aiDoctorItem{
		Name:    "config",
		Status:  "info",
		Message: fmt.Sprintf("no %s with llm config found in workdir or home", generator.DefaultConfigFile),
	}
}

func printAIDoctorReport(r aiDoctorReport) {
	cliOutputf("gofly ai doctor %s\n", r.Version)
	cliOutputln()

	cliOutputf("Providers:\n")
	for _, p := range r.Providers {
		printAIDoctorItem(p, "  ")
	}

	cliOutputf("\nEnvironment:\n")
	for _, e := range r.EnvVars {
		printAIDoctorItem(e, "  ")
	}

	cliOutputf("\nSecrets:\n")
	for _, s := range r.Secrets {
		printAIDoctorItem(s, "  ")
	}

	cliOutputf("\nFailover:\n")
	printAIDoctorItem(r.Failover, "  ")

	cliOutputf("\nConfig:\n")
	printAIDoctorItem(r.Config, "  ")

	cliOutputf("\nCache:\n")
	printAIDoctorItem(r.Cache, "  ")

	cliOutputf("\nTelemetry:\n")
	printAIDoctorItem(r.Telemetry, "  ")

	cliOutputf("\nCost:\n")
	printAIDoctorItem(r.Cost, "  ")

	cliOutputf("\n%s\n", r.Summary)
}

func printAIDoctorItem(item aiDoctorItem, indent string) {
	switch item.Status {
	case "ok":
		cliOutputf("%s\033[92m[OK]\033[0m   %s", indent, item.Name)
	case "warn":
		cliOutputf("%s\033[93m[WARN]\033[0m %s: %s", indent, item.Name, item.Message)
	case "fail":
		cliOutputf("%s\033[91m[FAIL]\033[0m %s: %s", indent, item.Name, item.Message)
	default:
		cliOutputf("%s\033[90m[INFO]\033[0m %s", indent, item.Name)
	}
	if item.Message != "" && (item.Status == "ok" || item.Status == "info") {
		cliOutputf(": %s", item.Message)
	}
	cliOutputln()
	for _, next := range item.NextActions {
		cliOutputf("%s       next: %s\n", indent, next)
	}
}

// checkAIDoctorCache reports the response caching infrastructure status.
func checkAIDoctorCache() aiDoctorItem {
	envTTL := os.Getenv("GOFLY_LLM_CACHE_TTL")
	envMaxSize := os.Getenv("GOFLY_LLM_CACHE_MAX_SIZE")

	parts := []string{
		"CachingProvider available in core/llm",
		"defaultTTL=5m",
		"defaultMaxSize=256",
	}
	if envTTL != "" {
		parts = append(parts, "env.GOFLY_LLM_CACHE_TTL="+envTTL)
	}
	if envMaxSize != "" {
		parts = append(parts, "env.GOFLY_LLM_CACHE_MAX_SIZE="+envMaxSize)
	}

	status := "info"
	if envTTL != "" || envMaxSize != "" {
		status = "ok"
	}
	return aiDoctorItem{
		Name:    "cache",
		Status:  status,
		Message: strings.Join(parts, "; "),
	}
}

func checkAIDoctorTelemetry() aiDoctorItem {
	fields := aiLLMTelemetryFields()
	parts := []string{
		"structured audit logging available",
		"lowCardinality=" + strings.Join(fields, ","),
		"forbidden=prompt,completion,secret values,provider response body",
	}
	return aiDoctorItem{
		Name:    "telemetry",
		Status:  "ok",
		Message: strings.Join(parts, "; "),
	}
}

func checkAIDoctorCost() aiDoctorItem {
	return aiDoctorItem{
		Name:    "cost",
		Status:  "info",
		Message: "token usage and budget snapshots are exposed; currency pricing is disabled-by-default and unpriced without an operator-maintained pricing table",
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
