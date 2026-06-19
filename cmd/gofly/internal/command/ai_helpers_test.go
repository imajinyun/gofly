package command

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gofly/gofly/core/llm"
)

func TestIsAIHelpSubcommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "manifest", command: "manifest", want: true},
		{name: "complete", command: "complete", want: true},
		{name: "doctor", command: "doctor", want: true},
		{name: "ask is not supported", command: "ask", want: false},
		{name: "empty", command: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAIHelpSubcommand(tt.command); got != tt.want {
				t.Fatalf("isAIHelpSubcommand(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestIsRetryableLLMError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "rate limited", err: llm.ErrRateLimited, want: true},
		{name: "wrapped provider request failed", err: errors.Join(errors.New("call failed"), llm.ErrProviderRequestFailed), want: true},
		{name: "http throttled", err: &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusTooManyRequests}, want: true},
		{name: "http server error", err: &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusBadGateway}, want: true},
		{name: "http unauthorized", err: &llm.ProviderHTTPError{Provider: llm.ProviderOpenAICompatible, StatusCode: http.StatusUnauthorized}, want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableLLMError(tt.err); got != tt.want {
				t.Fatalf("isRetryableLLMError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
