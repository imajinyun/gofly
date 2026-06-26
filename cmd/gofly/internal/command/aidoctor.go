package command

import (
	"flag"
	"fmt"

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
