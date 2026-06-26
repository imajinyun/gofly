package command

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

var (
	errUsage               = errors.New("invalid usage")
	errJSONAlreadyReported = errors.New("json error already reported")
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// ExitCode maps command errors to stable Unix-style process exit codes.
func ExitCode(err error) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, errUsage) || errors.Is(err, flag.ErrHelp) || isFlagUsageError(err) {
		return exitUsage
	}
	return exitError
}

func isFlagUsageError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "flag provided but not defined") ||
		strings.Contains(message, "invalid value") ||
		strings.Contains(message, "flag needs an argument")
}

func printJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	cliOutputln(string(data))
	return nil
}

func printJSONLine(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal json line: %w", err)
	}
	cliOutputln(string(data))
	return nil
}

func printCLIPlan(command string, plan cliPlan, forceJSON ...bool) error {
	if plan.Command == "" {
		plan.Command = command
	}
	jsonOutput := outputMode() == outputJSON
	for _, force := range forceJSON {
		jsonOutput = jsonOutput || force
	}
	if jsonOutput {
		return printJSONEnvelope(command, plan)
	}
	cliOutputfIf("%s plan (dry-run=%t, mutates-filesystem=%t)\n", command, plan.DryRun, plan.MutatesFilesystem)
	if len(plan.Inputs) > 0 {
		keys := make([]string, 0, len(plan.Inputs))
		for key := range plan.Inputs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		cliOutputlnIf("inputs:")
		for _, key := range keys {
			cliOutputfIf("  %s: %s\n", key, plan.Inputs[key])
		}
	}
	if len(plan.Actions) > 0 {
		cliOutputlnIf("actions:")
		for _, action := range plan.Actions {
			cliOutputfIf("  - %s %s (%s): %s\n", action.Operation, action.Target, action.RiskLevel, action.Description)
		}
	}
	for _, warning := range plan.Warnings {
		cliOutputfIf("warning: %s\n", warning)
	}
	for _, next := range plan.NextActions {
		cliOutputfIf("next: %s\n", next)
	}
	return nil
}

type jsonEnvelope struct {
	OK          bool       `json:"ok"`
	Command     string     `json:"command"`
	Version     string     `json:"version"`
	Data        any        `json:"data,omitempty"`
	Error       *jsonError `json:"error,omitempty"`
	Diagnostics []string   `json:"diagnostics,omitempty"`
	Warnings    []string   `json:"warnings,omitempty"`
	NextActions []string   `json:"nextActions,omitempty"`
}

type jsonError struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Retryable   bool           `json:"retryable"`
	Remediation string         `json:"remediation,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	NextActions []string       `json:"nextActions,omitempty"`
}

func printJSONEnvelope(command string, data any) error {
	return printJSON(jsonEnvelope{OK: true, Command: command, Version: Version, Data: data})
}

func printJSONError(command string, err error) error {
	classified := classifyJSONError(err)
	var nextActions []string
	if classified != nil {
		nextActions = classified.NextActions
	}
	return printJSON(jsonEnvelope{OK: false, Command: command, Version: Version, Error: classified, NextActions: nextActions})
}

func classifyJSONError(err error) *jsonError {
	if err == nil {
		return nil
	}
	message := err.Error()
	resp := &jsonError{Code: "COMMAND_ERROR", Message: message, Retryable: false}
	if ExitCode(err) == exitUsage {
		resp.Code = "USAGE_ERROR"
		resp.Remediation = "Check command usage and required flags."
	}
	if errors.Is(err, llm.ErrBudgetExceeded) {
		resp.Code = "LLM_TOKEN_BUDGET_EXCEEDED"
		resp.Remediation = "Increase the token budget flags or reduce the prompt/output token limits."
	}
	if errors.Is(err, llm.ErrRateLimited) {
		resp.Code = "LLM_RATE_LIMITED"
		resp.Retryable = true
		resp.Remediation = "Retry after the configured LLM provider rate limit allows another call."
	}
	if errors.Is(err, llm.ErrProviderNotFound) {
		resp.Code = "LLM_PROVIDER_NOT_FOUND"
		resp.Remediation = "Use `gofly ai manifest --format json` to inspect available providers."
	}
	if errors.Is(err, llm.ErrSecretNotFound) {
		resp.Code = "LLM_PROVIDER_SECRET_MISSING"
		resp.Remediation = "Provide the required provider credential through the documented environment variable; secrets are not read from .gofly/config.json."
	}
	if errors.Is(err, llm.ErrProviderEndpointRejected) {
		resp.Code = "LLM_PROVIDER_ENDPOINT_REJECTED"
		resp.Remediation = "Use an HTTPS endpoint whose hostname is included in the provider allowlist."
	}
	if errors.Is(err, llm.ErrProviderConfigInvalid) {
		resp.Code = "LLM_PROVIDER_CONFIG_INVALID"
		resp.Remediation = "Check provider configuration environment variables documented by `gofly ai manifest --format json`."
	}
	if errors.Is(err, llm.ErrProviderCapabilityUnsupported) {
		resp.Code = "LLM_PROVIDER_CAPABILITY_UNSUPPORTED"
		resp.Remediation = "Use a provider operation listed in llmGovernance.providers capabilities."
	}
	if errors.Is(err, llm.ErrProviderRequestFailed) {
		resp.Code = "LLM_PROVIDER_REQUEST_FAILED"
		resp.Retryable = true
		resp.Remediation = "Check provider availability and endpoint configuration; raw provider response bodies are intentionally omitted."
		addProviderFailoverNextActions(resp)
	}
	var httpErr *llm.ProviderHTTPError
	if errors.As(err, &httpErr) {
		resp.Retryable = httpErr.Retryable()
		resp.Details = map[string]any{
			"provider":    httpErr.Provider,
			"statusCode":  httpErr.StatusCode,
			"statusClass": httpErr.StatusClass(),
		}
		switch httpErr.StatusClass() {
		case "auth":
			resp.Remediation = "Check provider credentials and authorization scopes; authentication failures are not retried automatically."
		case "rate_limit":
			resp.Remediation = "Retry after provider throttling clears or lower request concurrency."
		case "server":
			resp.Remediation = "Retry later or fail over to another provider endpoint; raw provider response bodies are intentionally omitted."
		}
		if resp.Retryable {
			addProviderFailoverNextActions(resp)
		}
	}
	if errors.Is(err, llm.ErrProviderResponseTooLarge) {
		resp.Code = "LLM_PROVIDER_RESPONSE_TOO_LARGE"
		resp.Remediation = "Increase the provider max-response-bytes limit or reduce the generation length."
	}
	if errors.Is(err, llm.ErrProviderAlreadyRegistered) {
		resp.Code = "LLM_PROVIDER_ALREADY_REGISTERED"
		resp.Remediation = "Avoid duplicate provider registration in plugin or extension code."
	}
	if strings.Contains(message, "feature ") && strings.Contains(message, " is not registered") {
		resp.Code = "FEATURE_NOT_REGISTERED"
		resp.Remediation = "Use one of: http-compat, rpc-compat, ecosystem-compat."
	}
	return resp
}

func addProviderFailoverNextActions(resp *jsonError) {
	if resp == nil {
		return
	}
	resp.NextActions = appendMissingStrings(resp.NextActions,
		"retry the request after the provider or network condition clears",
		"set GOFLY_LLM_FAILOVER_PROVIDERS and rerun with --allow-failover to manually retry retryable provider failures",
		"inspect `gofly ai manifest --format json` for provider capabilities and secret environment variables",
	)
}

func appendMissingStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	out := append([]string(nil), values...)
	for _, value := range out {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func commandName(args []string) string {
	if len(args) == 0 {
		return "root"
	}
	if len(args) == 1 {
		return args[0]
	}
	return args[0] + "." + args[1]
}
