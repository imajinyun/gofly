package command

import (
	"errors"
	"strings"

	"github.com/imajinyun/gofly/core/llm"
)

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
