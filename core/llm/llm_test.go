package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTokenBudgetReserveAndSnapshot(t *testing.T) {
	budget := NewTokenBudget(10, 5, 12)
	usage, err := budget.Reserve(4, 3)
	if err != nil {
		t.Fatalf("Reserve() error = %v, want nil", err)
	}
	if usage.InputTokens != 4 || usage.OutputTokens != 3 || usage.TotalTokens != 7 {
		t.Fatalf("Reserve() usage = %+v, want input=4 output=3 total=7", usage)
	}

	if _, err := budget.Reserve(7, 0); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Reserve() error = %v, want ErrBudgetExceeded", err)
	}
	snapshot := budget.Snapshot()
	if snapshot.UsedInput != 4 || snapshot.UsedOutput != 3 || snapshot.RemainInput != 6 || snapshot.RemainOutput != 2 || snapshot.RemainTotal != 5 {
		t.Fatalf("Snapshot() = %+v", snapshot)
	}
}

func TestRateLimiterAllow(t *testing.T) {
	limiter := NewRateLimiter(1, 1)
	if !limiter.Allow() {
		t.Fatal("first Allow() = false, want true")
	}
	if limiter.Allow() {
		t.Fatal("second Allow() = true, want false after burst is consumed")
	}
}

func TestRedactorRedactsSecretsAndPII(t *testing.T) {
	redactor := DefaultRedactor()
	input := "email alice@example.com Authorization: Bearer abc.def token=secret password=hunter2"
	got := redactor.Redact(input)
	for _, leaked := range []string{"alice@example.com", "abc.def", "secret", "hunter2"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("Redact() leaked %q in %q", leaked, got)
		}
	}
	for _, want := range []string{"[REDACTED_EMAIL]", "[REDACTED]", "token=[REDACTED]", "password=[REDACTED]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Redact() = %q, want substring %q", got, want)
		}
	}
}

func TestGovernedProviderRedactEmbedRequestAndErrorClass_BitsUT(t *testing.T) {
	req := EmbedRequest{
		Inputs:   []string{"email alice@example.com token=secret", "password=hunter2"},
		Metadata: map[string]string{"credential": "api_key=secret-key", "safe": "value"},
	}
	redacted := NewGovernedProvider(NoOpProvider{}).redactEmbedRequest(req)
	for _, leaked := range []string{"alice@example.com", "secret", "hunter2", "secret-key"} {
		if strings.Contains(strings.Join(redacted.Inputs, " "), leaked) || strings.Contains(redacted.Metadata["credential"], leaked) {
			t.Fatalf("redactEmbedRequest leaked %q in %#v", leaked, redacted)
		}
	}
	if redacted.Metadata["safe"] != "value" {
		t.Fatalf("safe metadata = %q, want preserved", redacted.Metadata["safe"])
	}
	if !strings.Contains(req.Inputs[0], "[REDACTED_EMAIL]") || req.Metadata["credential"] != "api_key=secret-key" {
		t.Fatalf("source request = %#v, want input slice redacted in place and metadata source preserved", req)
	}
	if got := ((*GovernedProvider)(nil)).redactEmbedRequest(req); got.Inputs[0] != req.Inputs[0] || got.Metadata["credential"] != req.Metadata["credential"] {
		t.Fatalf("nil provider redaction = %#v, want unchanged request", got)
	}
	if got := NewGovernedProvider(NoOpProvider{}, WithRequestRedaction(false)).redactEmbedRequest(req); got.Inputs[0] != req.Inputs[0] || got.Metadata["credential"] != req.Metadata["credential"] {
		t.Fatalf("disabled redaction = %#v, want unchanged request", got)
	}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "http auth", err: &ProviderHTTPError{Provider: "test", StatusCode: http.StatusUnauthorized}, want: "auth"},
		{name: "http rate limit", err: &ProviderHTTPError{Provider: "test", StatusCode: http.StatusTooManyRequests}, want: "rate_limit"},
		{name: "http server", err: &ProviderHTTPError{Provider: "test", StatusCode: http.StatusBadGateway}, want: "server"},
		{name: "budget", err: ErrBudgetExceeded, want: "budget"},
		{name: "response too large", err: ErrProviderResponseTooLarge, want: "response_too_large"},
		{name: "endpoint rejected", err: ErrProviderEndpointRejected, want: "endpoint_rejected"},
		{name: "provider config", err: ErrProviderConfigInvalid, want: "provider_config"},
		{name: "capability", err: ErrProviderCapabilityUnsupported, want: "capability"},
		{name: "provider request", err: ErrProviderRequestFailed, want: "provider_request"},
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "deadline"},
		{name: "unknown", err: errors.New("boom"), want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorClass(tt.err); got != tt.want {
				t.Fatalf("errorClass(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestNoOpProviderIsDeterministic(t *testing.T) {
	provider := NoOpProvider{}
	resp, err := provider.Complete(context.Background(), Request{Prompt: "hello world"})
	if err != nil {
		t.Fatalf("Complete() error = %v, want nil", err)
	}
	if resp.Text != "" || resp.Usage.InputTokens == 0 || resp.Usage.TotalTokens != resp.Usage.InputTokens {
		t.Fatalf("Complete() response = %+v", resp)
	}

	stream, err := provider.Stream(context.Background(), Request{Prompt: "hello world"})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	event, ok := <-stream
	if !ok || !event.Done || event.Usage.InputTokens == 0 {
		t.Fatalf("Stream() event = %+v ok=%t", event, ok)
	}
	if _, ok := <-stream; ok {
		t.Fatal("Stream() channel still open, want closed")
	}

	embed, err := provider.Embed(context.Background(), EmbedRequest{Inputs: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Embed() error = %v, want nil", err)
	}
	if len(embed.Vectors) != 2 || embed.Usage.InputTokens == 0 {
		t.Fatalf("Embed() response = %+v", embed)
	}
}

func TestGovernedProviderRedactsBudgetsAndAudits(t *testing.T) {
	var logs bytes.Buffer
	provider := &captureProvider{complete: Response{Text: "ok", Usage: Usage{InputTokens: 2, OutputTokens: 1}}}
	governed := NewGovernedProvider(provider,
		WithTokenBudget(NewTokenBudget(10, 10, 20)),
		WithAuditLogger(NewAuditLogger(slog.New(slog.NewJSONHandler(&logs, nil)), DefaultRedactor())),
	)

	resp, err := governed.Complete(context.Background(), Request{
		Provider:        "test-provider",
		Model:           "test-model",
		Prompt:          "email alice@example.com password=hunter2",
		MaxOutputTokens: 1,
		Metadata:        map[string]string{"authorization": "Bearer abc.def"},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v, want nil", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("Complete() text = %q, want ok", resp.Text)
	}
	if strings.Contains(provider.seen.Prompt, "alice@example.com") || strings.Contains(provider.seen.Prompt, "hunter2") {
		t.Fatalf("provider saw unredacted prompt %q", provider.seen.Prompt)
	}
	logOutput := logs.String()
	for _, leaked := range []string{"alice@example.com", "hunter2", "abc.def"} {
		if strings.Contains(logOutput, leaked) {
			t.Fatalf("audit log leaked %q in %s", leaked, logOutput)
		}
	}
	for _, want := range []string{"llm call audited", "test-provider", "test-model", "[REDACTED]"} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("audit log = %s, want substring %q", logOutput, want)
		}
	}
}

func TestGovernedProviderAuditTelemetryClassification(t *testing.T) {
	t.Run("http provider errors include low cardinality telemetry", func(t *testing.T) {
		var logs bytes.Buffer
		provider := &captureProvider{completeErr: &ProviderHTTPError{Provider: ProviderOpenAICompatible, StatusCode: http.StatusTooManyRequests}}
		governed := NewGovernedProvider(provider, WithAuditLogger(NewAuditLogger(slog.New(slog.NewJSONHandler(&logs, nil)), DefaultRedactor())))

		_, err := governed.Complete(context.Background(), Request{Provider: ProviderOpenAICompatible, Model: "model", Prompt: "hello"})
		if !errors.Is(err, ErrProviderRequestFailed) {
			t.Fatalf("Complete() error = %v, want ErrProviderRequestFailed", err)
		}
		logOutput := logs.String()
		for _, want := range []string{`"error_class":"rate_limit"`, `"retryable":true`, `"provider_status_code":429`} {
			if !strings.Contains(logOutput, want) {
				t.Fatalf("audit log = %s, want substring %q", logOutput, want)
			}
		}
	})

	t.Run("stream audit records terminal event count", func(t *testing.T) {
		var logs bytes.Buffer
		provider := &captureProvider{streamEvents: []StreamEvent{{Delta: "hello"}, {Done: true, Usage: Usage{InputTokens: 1, OutputTokens: 1}}}}
		governed := NewGovernedProvider(provider, WithAuditLogger(NewAuditLogger(slog.New(slog.NewJSONHandler(&logs, nil)), DefaultRedactor())))

		stream, err := governed.Stream(context.Background(), Request{Provider: "noop", Model: "model", Prompt: "hello"})
		if err != nil {
			t.Fatalf("Stream() error = %v, want nil", err)
		}
		for range stream {
		}
		if logOutput := logs.String(); !strings.Contains(logOutput, `"stream_events":2`) {
			t.Fatalf("stream audit log = %s, want stream_events", logOutput)
		}
	})
}

func TestGovernedProviderRejectsBudgetAndRateLimit(t *testing.T) {
	t.Run("budget", func(t *testing.T) {
		governed := NewGovernedProvider(NoOpProvider{}, WithTokenBudget(NewTokenBudget(1, 0, 0)))
		_, err := governed.Complete(context.Background(), Request{Prompt: "this prompt is too large"})
		if !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("Complete() error = %v, want ErrBudgetExceeded", err)
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		limiter := NewRateLimiter(1, 1)
		if !limiter.Allow() {
			t.Fatal("precondition failed: first Allow() = false")
		}
		governed := NewGovernedProvider(NoOpProvider{}, WithRateLimiter(limiter))
		_, err := governed.Complete(context.Background(), Request{Prompt: "ok"})
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("Complete() error = %v, want ErrRateLimited", err)
		}
	})
}

func TestGovernedProviderStreamEmbedAndOptions(t *testing.T) {
	provider := &captureProvider{
		streamEvents: []StreamEvent{{Delta: "hello"}, {Done: true, Usage: Usage{InputTokens: 2, OutputTokens: 1}}},
		embed:        EmbedResponse{Vectors: [][]float64{{1, 2}}, Usage: Usage{InputTokens: 3}},
	}
	governed := NewGovernedProvider(provider,
		WithRedactor(nil),
		WithRequestRedaction(false),
		WithObserver(nil),
	)

	stream, err := governed.Stream(context.Background(), Request{Prompt: "email alice@example.com", MaxOutputTokens: 1})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	var events []StreamEvent
	for event := range stream {
		events = append(events, event)
	}
	if len(events) != 2 || !events[1].Done {
		t.Fatalf("Stream() events = %+v", events)
	}
	if !strings.Contains(provider.seen.Prompt, "alice@example.com") {
		t.Fatalf("provider prompt = %q, want unredacted because request redaction is disabled", provider.seen.Prompt)
	}

	embed, err := governed.Embed(context.Background(), EmbedRequest{Inputs: []string{"secret=abc"}})
	if err != nil {
		t.Fatalf("Embed() error = %v, want nil", err)
	}
	if len(embed.Vectors) != 1 || embed.Usage.InputTokens != 3 {
		t.Fatalf("Embed() response = %+v", embed)
	}
}

func TestGovernedProviderStreamGovernance(t *testing.T) {
	t.Run("limits oversized stream events", func(t *testing.T) {
		provider := &captureProvider{streamEvents: []StreamEvent{{Delta: strings.Repeat("x", 9)}}}
		governed := NewGovernedProvider(provider, WithMaxStreamEventBytes(8))
		stream, err := governed.Stream(context.Background(), Request{Prompt: "hello"})
		if err != nil {
			t.Fatalf("Stream() error = %v, want nil", err)
		}
		event, ok := <-stream
		if !ok || !event.Done || !errors.Is(event.Err, ErrProviderResponseTooLarge) {
			t.Fatalf("stream event = %+v ok=%t, want bounded error", event, ok)
		}
		if _, ok := <-stream; ok {
			t.Fatal("stream channel still open, want closed")
		}
	})

	t.Run("redacts provider stream errors", func(t *testing.T) {
		provider := &captureProvider{streamEvents: []StreamEvent{{Err: errors.New("provider echoed token=secret and alice@example.com")}}}
		governed := NewGovernedProvider(provider)
		stream, err := governed.Stream(context.Background(), Request{Prompt: "hello"})
		if err != nil {
			t.Fatalf("Stream() error = %v, want nil", err)
		}
		event, ok := <-stream
		if !ok || !event.Done || !errors.Is(event.Err, ErrProviderRequestFailed) {
			t.Fatalf("stream event = %+v ok=%t, want sanitized provider error", event, ok)
		}
		if strings.Contains(event.Err.Error(), "secret") || strings.Contains(event.Err.Error(), "alice@example.com") {
			t.Fatalf("stream error leaked sensitive content: %v", event.Err)
		}
	})

	t.Run("reports context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan StreamEvent)
		governed := NewGovernedProvider(streamProviderFunc(func(context.Context, Request) (<-chan StreamEvent, error) {
			return ch, nil
		}))
		stream, err := governed.Stream(ctx, Request{Prompt: "hello"})
		if err != nil {
			t.Fatalf("Stream() error = %v, want nil", err)
		}
		cancel()
		event, ok := <-stream
		if !ok || !event.Done || !errors.Is(event.Err, ErrProviderRequestFailed) {
			t.Fatalf("stream cancel event = %+v ok=%t", event, ok)
		}
	})
}

func TestRateLimiterWaitHonorsContext(t *testing.T) {
	limiter := NewRateLimiter(1, 1)
	if !limiter.Allow() {
		t.Fatal("precondition failed: first Allow() = false")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.Wait(ctx); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Wait() error = %v, want ErrRateLimited", err)
	}
}

func TestProviderRegistryBuildsNoOpProvider(t *testing.T) {
	registry := NewDefaultProviderRegistry()
	provider, spec, err := registry.Build(" NOOP ", ProviderConfig{})
	if err != nil {
		t.Fatalf("Build() error = %v, want nil", err)
	}
	if spec.Name != "noop" || spec.DefaultModel != "noop" || spec.NetworkAccess || spec.RequiresSecrets {
		t.Fatalf("Build() spec = %+v", spec)
	}
	resp, err := provider.Complete(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Complete() error = %v, want nil", err)
	}
	if resp.Usage.InputTokens == 0 {
		t.Fatalf("Complete() response = %+v, want input usage", resp)
	}

	if names := registry.ProviderNames(); !reflect.DeepEqual(names, []string{"noop", "openai-compatible"}) {
		t.Fatalf("ProviderNames() = %#v", names)
	}
	if specs := registry.Specs(); len(specs) != 2 || specs[0].Name != "noop" || specs[1].Name != "openai-compatible" || !specs[1].NetworkAccess || !specs[1].RequiresSecrets {
		t.Fatalf("Specs() = %+v", specs)
	}
}

func TestProviderRegistryRegistrationValidationAndCopies(t *testing.T) {
	registry := NewProviderRegistry()
	spec := ProviderSpec{
		Name:            " Remote ",
		DisplayName:     "remote",
		DefaultModel:    "model-a",
		NetworkAccess:   true,
		RequiresSecrets: true,
		SecretEnvVars:   []string{"REMOTE_API_KEY", "REMOTE_API_KEY", ""},
		Capabilities:    []string{"complete", "embed", "complete"},
	}
	if err := registry.Register(spec, func(ProviderConfig) (Provider, error) { return NoOpProvider{}, nil }); err != nil {
		t.Fatalf("Register() error = %v, want nil", err)
	}
	if err := registry.Register(spec, func(ProviderConfig) (Provider, error) { return NoOpProvider{}, nil }); !errors.Is(err, ErrProviderAlreadyRegistered) {
		t.Fatalf("duplicate Register() error = %v, want ErrProviderAlreadyRegistered", err)
	}

	got, ok := registry.Spec("remote")
	if !ok {
		t.Fatal("Spec(remote) ok = false, want true")
	}
	if !reflect.DeepEqual(got.SecretEnvVars, []string{"REMOTE_API_KEY"}) || !reflect.DeepEqual(got.Capabilities, []string{"complete", "embed"}) {
		t.Fatalf("Spec(remote) = %+v", got)
	}
	got.SecretEnvVars[0] = "LEAKED_MUTATION"
	again, _ := registry.Spec("remote")
	if again.SecretEnvVars[0] != "REMOTE_API_KEY" {
		t.Fatalf("Spec() returned mutable registry state: %+v", again)
	}
}

func TestProviderRegistryCapabilityNegotiation(t *testing.T) {
	registry := NewProviderRegistry()
	if err := registry.Register(ProviderSpec{
		Name:         "complete-only",
		DefaultModel: "text-a",
		Capabilities: []string{"complete"},
		Models: []ProviderModelSpec{
			{Name: "text-a", Default: true, Capabilities: []string{"complete", "json-mode"}},
		},
	}, func(ProviderConfig) (Provider, error) { return NoOpProvider{}, nil }); err != nil {
		t.Fatalf("Register(complete-only) error = %v", err)
	}
	if err := registry.Register(ProviderSpec{
		Name:         "streaming",
		DefaultModel: "chat-a",
		Capabilities: []string{"complete", "stream"},
		Models: []ProviderModelSpec{
			{Name: "chat-a", Default: true, Capabilities: []string{"complete", "stream", "tool-call"}},
		},
	}, func(ProviderConfig) (Provider, error) { return NoOpProvider{}, nil }); err != nil {
		t.Fatalf("Register(streaming) error = %v", err)
	}

	if !registry.ProviderSupportsCapability(" streaming ", "stream") || registry.ProviderSupportsCapability("complete-only", "stream") || registry.ProviderSupportsCapability("missing", "stream") {
		t.Fatalf("ProviderSupportsCapability() returned unexpected results")
	}
	streamSpecs := registry.SpecsWithCapability("stream")
	if len(streamSpecs) != 1 || streamSpecs[0].Name != "streaming" {
		t.Fatalf("SpecsWithCapability(stream) = %+v", streamSpecs)
	}
	streamSpecs[0].Capabilities[0] = "mutated"
	again := registry.SpecsWithCapability("stream")
	if again[0].Capabilities[0] != "complete" {
		t.Fatalf("SpecsWithCapability() returned mutable registry state: %+v", again)
	}
	if got := registry.SpecsWithCapability(""); got != nil {
		t.Fatalf("SpecsWithCapability(empty) = %+v, want nil", got)
	}
	if !registry.ProviderModelSupportsCapability(" complete-only ", "", "json-mode") || !registry.ProviderModelSupportsCapability("streaming", "chat-a", "tool-call") || registry.ProviderModelSupportsCapability("complete-only", "text-a", "tool-call") {
		t.Fatalf("ProviderModelSupportsCapability() returned unexpected results")
	}
	toolSpecs := registry.SpecsWithModelCapability("tool-call")
	if len(toolSpecs) != 1 || toolSpecs[0].Name != "streaming" {
		t.Fatalf("SpecsWithModelCapability(tool-call) = %+v", toolSpecs)
	}
	toolSpecs[0].Models[0].Capabilities[0] = "mutated"
	againModels := registry.SpecsWithModelCapability("tool-call")
	if againModels[0].Models[0].Capabilities[0] != "complete" {
		t.Fatalf("SpecsWithModelCapability() returned mutable registry state: %+v", againModels)
	}
}

func TestProviderRegistryRegisterManifestContract(t *testing.T) {
	registry := NewProviderRegistry()
	manifest := ProviderPluginManifest{
		SchemaVersion: ProviderPluginManifestSchemaVersion,
		Provider: ProviderSpec{
			Name:            " Plugin ",
			DefaultModel:    "model-b",
			NetworkAccess:   true,
			RequiresSecrets: true,
			SecretEnvVars:   []string{"PLUGIN_API_KEY", "PLUGIN_API_KEY"},
			ConfigEnvVars:   []string{"PLUGIN_BASE_URL"},
			Capabilities:    []string{"complete", "stream"},
		},
		Models: []ProviderModelSpec{
			{Name: "model-b", Default: true, Capabilities: []string{"complete", "stream", "json-mode", "tool-call"}, ContextWindow: 128000},
		},
	}
	if err := registry.RegisterManifest(manifest, func(ProviderConfig) (Provider, error) { return NoOpProvider{}, nil }); err != nil {
		t.Fatalf("RegisterManifest() error = %v", err)
	}
	spec, ok := registry.Spec("plugin")
	if !ok {
		t.Fatal("Spec(plugin) ok = false, want true")
	}
	if spec.Name != "plugin" || !reflect.DeepEqual(spec.SecretEnvVars, []string{"PLUGIN_API_KEY"}) || len(spec.Models) != 1 || !registry.ProviderModelSupportsCapability("plugin", "model-b", "tool-call") {
		t.Fatalf("registered manifest spec = %+v", spec)
	}
	spec.Models[0].Capabilities[0] = "mutated"
	again, _ := registry.Spec("plugin")
	if again.Models[0].Capabilities[0] != "complete" {
		t.Fatalf("Spec() returned mutable model state: %+v", again)
	}
	if err := NewProviderRegistry().RegisterManifest(ProviderPluginManifest{SchemaVersion: "unsupported", Provider: ProviderSpec{Name: "bad"}}, func(ProviderConfig) (Provider, error) { return NoOpProvider{}, nil }); err == nil {
		t.Fatal("RegisterManifest(unsupported schema) error = nil, want error")
	}
}

func TestProviderRegistrySecretBoundary(t *testing.T) {
	const secretValue = "super-secret-provider-token"
	registry := NewProviderRegistry()
	if err := registry.Register(ProviderSpec{
		Name:            "remote",
		DefaultModel:    "remote-default",
		NetworkAccess:   true,
		RequiresSecrets: true,
		SecretEnvVars:   []string{"REMOTE_API_KEY"},
		Capabilities:    []string{"complete"},
	}, func(config ProviderConfig) (Provider, error) {
		secret, ok := config.Secrets.LookupSecret("REMOTE_API_KEY")
		if !ok || secret != secretValue {
			return nil, errors.New("secret resolver did not provide expected value")
		}
		if config.Model != "remote-default" || config.Provider != "remote" {
			return nil, errors.New("provider config was not normalized")
		}
		return NoOpProvider{}, nil
	}); err != nil {
		t.Fatalf("Register() error = %v, want nil", err)
	}

	_, _, err := registry.Build("remote", ProviderConfig{Secrets: staticSecretResolver{}})
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Build() error = %v, want ErrSecretNotFound", err)
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Fatalf("Build() error leaked secret: %v", err)
	}

	provider, spec, err := registry.Build("remote", ProviderConfig{Secrets: staticSecretResolver{"REMOTE_API_KEY": secretValue}})
	if err != nil {
		t.Fatalf("Build() with secret error = %v, want nil", err)
	}
	if spec.RequiresSecrets != true || !spec.NetworkAccess {
		t.Fatalf("Build() spec = %+v", spec)
	}
	if _, err := provider.Complete(context.Background(), Request{Prompt: "hello"}); err != nil {
		t.Fatalf("Complete() error = %v, want nil", err)
	}
}

func TestDefaultRegistryOpenAICompatibleSecretBoundary(t *testing.T) {
	const secretValue = "super-secret-openai-token"
	registry := NewDefaultProviderRegistry()
	_, _, err := registry.Build(ProviderOpenAICompatible, ProviderConfig{Secrets: staticSecretResolver{}})
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Build(openai-compatible) error = %v, want ErrSecretNotFound", err)
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Fatalf("Build(openai-compatible) leaked secret: %v", err)
	}
	_, _, err = registry.Build(ProviderOpenAICompatible, ProviderConfig{Secrets: staticSecretResolver{EnvOpenAICompatibleAPIKey: secretValue}})
	if err != nil {
		t.Fatalf("Build(openai-compatible) with default https endpoint error = %v, want nil", err)
	}
}

func TestOpenAICompatibleProviderComplete(t *testing.T) {
	const apiKey = "super-secret-openai-token"
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request path = %s, want /v1/chat/completions", r.URL.Path)
		}
		capturedAuth = r.Header.Get("Authorization")
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.Model != "test-model" || payload.MaxTokens != 7 || len(payload.Messages) != 1 || payload.Messages[0].Content != "hello" {
			t.Fatalf("request payload = %+v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:           server.URL + "/v1",
		APIKey:            apiKey,
		DefaultModel:      "test-model",
		AllowedHosts:      []string{"127.0.0.1"},
		AllowInsecureHTTP: true,
		Timeout:           time.Second,
		MaxResponseBytes:  1024,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v, want nil", err)
	}
	resp, err := provider.Complete(context.Background(), Request{Prompt: "hello", MaxOutputTokens: 7})
	if err != nil {
		t.Fatalf("Complete() error = %v, want nil", err)
	}
	if resp.Text != "hi" || resp.Usage.InputTokens != 2 || resp.Usage.OutputTokens != 3 || resp.Usage.TotalTokens != 5 {
		t.Fatalf("Complete() response = %+v", resp)
	}
	if capturedAuth != "Bearer "+apiKey {
		t.Fatalf("Authorization header = %q", capturedAuth)
	}

	request := provider.chatRequest(Request{Model: "", Messages: []Message{{Role: "", Content: "msg"}, {Role: "system", Content: "sys"}}})
	if request.Model != "test-model" || len(request.Messages) != 2 || request.Messages[0].Role != "user" || request.Messages[1].Role != "system" {
		t.Fatalf("chatRequest() = %+v", request)
	}
}

func TestOpenAICompatibleProviderFromConfigUsesEnvResolver(t *testing.T) {
	const apiKey = "super-secret-openai-token"
	provider, err := NewOpenAICompatibleProviderFromConfig(ProviderConfig{
		Model: "env-model",
		Secrets: staticSecretResolver{
			EnvOpenAICompatibleAPIKey:           apiKey,
			EnvOpenAICompatibleBaseURL:          "https://api.openai.com/v1",
			EnvOpenAICompatibleAllowedHosts:     "127.0.0.1, api.openai.com",
			EnvOpenAICompatibleMaxResponseBytes: "2048",
		},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProviderFromConfig() error = %v, want nil", err)
	}
	openAIProvider, ok := provider.(*OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *OpenAICompatibleProvider", provider)
	}
	request := openAIProvider.chatRequest(Request{Prompt: "hello"})
	if request.Model != "env-model" || len(request.Messages) != 1 || request.Messages[0].Content != "hello" {
		t.Fatalf("chatRequest() = %+v", request)
	}
	if openAIProvider.maxResponseBytes != 2048 {
		t.Fatalf("maxResponseBytes = %d, want 2048", openAIProvider.maxResponseBytes)
	}

	_, err = NewOpenAICompatibleProviderFromConfig(ProviderConfig{Secrets: staticSecretResolver{
		EnvOpenAICompatibleAPIKey:           apiKey,
		EnvOpenAICompatibleMaxResponseBytes: "bad",
	}})
	if !errors.Is(err, ErrProviderConfigInvalid) {
		t.Fatalf("NewOpenAICompatibleProviderFromConfig() error = %v, want ErrProviderConfigInvalid", err)
	}
}

func TestOpenAICompatibleProviderSecurityValidation(t *testing.T) {
	tests := []struct {
		name   string
		config OpenAICompatibleConfig
		want   error
	}{
		{
			name:   "rejects http by default",
			config: OpenAICompatibleConfig{BaseURL: "http://api.openai.com/v1", APIKey: "secret", AllowedHosts: []string{"api.openai.com"}},
			want:   ErrProviderEndpointRejected,
		},
		{
			name:   "requires allowlisted host",
			config: OpenAICompatibleConfig{BaseURL: "https://evil.example/v1", APIKey: "secret", AllowedHosts: []string{"api.openai.com"}},
			want:   ErrProviderEndpointRejected,
		},
		{
			name:   "requires api key",
			config: OpenAICompatibleConfig{BaseURL: "https://api.openai.com/v1", AllowedHosts: []string{"api.openai.com"}},
			want:   ErrSecretNotFound,
		},
		{
			name:   "rejects malformed base url",
			config: OpenAICompatibleConfig{BaseURL: "://bad", APIKey: "secret", AllowedHosts: []string{"api.openai.com"}},
			want:   ErrProviderEndpointRejected,
		},
		{
			name:   "requires host",
			config: OpenAICompatibleConfig{BaseURL: "https:///v1", APIKey: "secret", AllowedHosts: []string{"api.openai.com"}},
			want:   ErrProviderEndpointRejected,
		},
		{
			name:   "requires allowlist",
			config: OpenAICompatibleConfig{BaseURL: "https://api.openai.com/v1", APIKey: "secret"},
			want:   ErrProviderEndpointRejected,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewOpenAICompatibleProvider(tt.config)
			if !errors.Is(err, tt.want) {
				t.Fatalf("NewOpenAICompatibleProvider() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestOpenAICompatibleProviderErrorsAreSanitizedAndBounded(t *testing.T) {
	const secretValue = "super-secret-openai-token"
	t.Run("nil provider", func(t *testing.T) {
		var provider *OpenAICompatibleProvider
		_, err := provider.Complete(context.Background(), Request{})
		if err == nil || !strings.Contains(err.Error(), "provider is nil") {
			t.Fatalf("Complete() error = %v, want nil provider error", err)
		}
	})

	t.Run("http error omits response body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "prompt echo and "+secretValue, http.StatusUnauthorized)
		}))
		defer server.Close()
		provider := newTestOpenAICompatibleProvider(t, server.URL, secretValue, 1024)
		_, err := provider.Complete(context.Background(), Request{Prompt: "hello"})
		if !errors.Is(err, ErrProviderRequestFailed) {
			t.Fatalf("Complete() error = %v, want ErrProviderRequestFailed", err)
		}
		if strings.Contains(err.Error(), secretValue) || strings.Contains(err.Error(), "prompt echo") {
			t.Fatalf("Complete() error leaked provider body: %v", err)
		}
	})

	t.Run("response body limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", 32)))
		}))
		defer server.Close()
		provider := newTestOpenAICompatibleProvider(t, server.URL, secretValue, 8)
		_, err := provider.Complete(context.Background(), Request{Prompt: "hello"})
		if !errors.Is(err, ErrProviderResponseTooLarge) {
			t.Fatalf("Complete() error = %v, want ErrProviderResponseTooLarge", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{bad`))
		}))
		defer server.Close()
		provider := newTestOpenAICompatibleProvider(t, server.URL, secretValue, 1024)
		_, err := provider.Complete(context.Background(), Request{Prompt: "hello"})
		if !errors.Is(err, ErrProviderRequestFailed) {
			t.Fatalf("Complete() error = %v, want ErrProviderRequestFailed", err)
		}
	})

	t.Run("no choices", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[],"usage":{"prompt_tokens":1}}`))
		}))
		defer server.Close()
		provider := newTestOpenAICompatibleProvider(t, server.URL, secretValue, 1024)
		_, err := provider.Complete(context.Background(), Request{Prompt: "hello"})
		if !errors.Is(err, ErrProviderRequestFailed) {
			t.Fatalf("Complete() error = %v, want ErrProviderRequestFailed", err)
		}
	})

	if errText := (*ProviderHTTPError)(nil).Error(); errText != ErrProviderRequestFailed.Error() {
		t.Fatalf("nil ProviderHTTPError Error() = %q", errText)
	}
	for _, tt := range []struct {
		name       string
		statusCode int
		wantClass  string
		wantRetry  bool
	}{
		{name: "401 auth", statusCode: http.StatusUnauthorized, wantClass: "auth", wantRetry: false},
		{name: "429 rate limit", statusCode: http.StatusTooManyRequests, wantClass: "rate_limit", wantRetry: true},
		{name: "502 server", statusCode: http.StatusBadGateway, wantClass: "server", wantRetry: true},
		{name: "400 client", statusCode: http.StatusBadRequest, wantClass: "client", wantRetry: false},
	} {
		t.Run("http retryability "+tt.name, func(t *testing.T) {
			err := &ProviderHTTPError{Provider: ProviderOpenAICompatible, StatusCode: tt.statusCode}
			if got := err.StatusClass(); got != tt.wantClass {
				t.Fatalf("StatusClass() = %q, want %q", got, tt.wantClass)
			}
			if got := err.Retryable(); got != tt.wantRetry {
				t.Fatalf("Retryable() = %v, want %v", got, tt.wantRetry)
			}
		})
	}
}

func TestOpenAICompatibleProviderStream(t *testing.T) {
	const apiKey = "super-secret-openai-token"
	t.Run("parses SSE deltas usage and done", func(t *testing.T) {
		var capturedAccept string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedAccept = r.Header.Get("Accept")
			var payload struct {
				Stream        bool `json:"stream"`
				StreamOptions struct {
					IncludeUsage bool `json:"include_usage"`
				} `json:"stream_options"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if !payload.Stream || !payload.StreamOptions.IncludeUsage {
				t.Fatalf("stream payload = %+v", payload)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}))
		defer server.Close()

		provider := newTestOpenAICompatibleProvider(t, server.URL, apiKey, 4096)
		stream, err := provider.Stream(context.Background(), Request{Prompt: "hello", MaxOutputTokens: 8})
		if err != nil {
			t.Fatalf("Stream() error = %v, want nil", err)
		}
		events := collectStreamEvents(t, stream)
		if capturedAccept != "text/event-stream" {
			t.Fatalf("Accept header = %q", capturedAccept)
		}
		if len(events) != 4 || events[0].Delta != "hel" || events[1].Delta != "lo" || events[2].Usage.TotalTokens != 5 || !events[3].Done {
			t.Fatalf("Stream() events = %+v", events)
		}
	})

	t.Run("http error omits response body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "prompt echo and "+apiKey, http.StatusUnauthorized)
		}))
		defer server.Close()
		provider := newTestOpenAICompatibleProvider(t, server.URL, apiKey, 1024)
		_, err := provider.Stream(context.Background(), Request{Prompt: "hello"})
		if !errors.Is(err, ErrProviderRequestFailed) {
			t.Fatalf("Stream() error = %v, want ErrProviderRequestFailed", err)
		}
		if strings.Contains(err.Error(), apiKey) || strings.Contains(err.Error(), "prompt echo") {
			t.Fatalf("Stream() error leaked provider body: %v", err)
		}
	})

	t.Run("malformed SSE event reports request failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {bad\n\n"))
		}))
		defer server.Close()
		provider := newTestOpenAICompatibleProvider(t, server.URL, apiKey, 1024)
		stream, err := provider.Stream(context.Background(), Request{Prompt: "hello"})
		if err != nil {
			t.Fatalf("Stream() error = %v, want nil", err)
		}
		events := collectStreamEvents(t, stream)
		if len(events) != 1 || !errors.Is(events[0].Err, ErrProviderRequestFailed) {
			t.Fatalf("Stream() malformed events = %+v", events)
		}
	})

	t.Run("oversized stream reports response too large", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: " + strings.Repeat("x", 64) + "\n\n"))
		}))
		defer server.Close()
		provider := newTestOpenAICompatibleProvider(t, server.URL, apiKey, 16)
		stream, err := provider.Stream(context.Background(), Request{Prompt: "hello"})
		if err != nil {
			t.Fatalf("Stream() error = %v, want nil", err)
		}
		events := collectStreamEvents(t, stream)
		if len(events) != 1 || !errors.Is(events[0].Err, ErrProviderResponseTooLarge) {
			t.Fatalf("Stream() oversized events = %+v", events)
		}
	})

	t.Run("context cancellation stops stream", func(t *testing.T) {
		started := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			if flusher, ok := w.(http.Flusher); ok {
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
				flusher.Flush()
			}
			close(started)
			<-r.Context().Done()
		}))
		defer server.Close()
		provider := newTestOpenAICompatibleProvider(t, server.URL, apiKey, 1024)
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := provider.Stream(ctx, Request{Prompt: "hello"})
		if err != nil {
			t.Fatalf("Stream() error = %v, want nil", err)
		}
		<-started
		cancel()
		events := collectStreamEvents(t, stream)
		for _, event := range events {
			if event.Err != nil {
				t.Fatalf("Stream() canceled events = %+v", events)
			}
		}
	})
}

func TestOpenAICompatibleProviderUnsupportedCapabilities(t *testing.T) {
	provider := newTestOpenAICompatibleProvider(t, "http://127.0.0.1", "secret", 1024)
	if _, err := provider.Embed(context.Background(), EmbedRequest{}); !errors.Is(err, ErrProviderCapabilityUnsupported) {
		t.Fatalf("Embed() error = %v, want ErrProviderCapabilityUnsupported", err)
	}
}

func collectStreamEvents(t *testing.T, stream <-chan StreamEvent) []StreamEvent {
	t.Helper()
	events := []StreamEvent{}
	for event := range stream {
		events = append(events, event)
	}
	return events
}

func newTestOpenAICompatibleProvider(t *testing.T, baseURL, apiKey string, maxResponseBytes int64) *OpenAICompatibleProvider {
	t.Helper()
	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:           baseURL,
		APIKey:            apiKey,
		AllowedHosts:      []string{"127.0.0.1"},
		AllowInsecureHTTP: true,
		Timeout:           time.Second,
		MaxResponseBytes:  maxResponseBytes,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	return provider
}

func TestEnvSecretResolver(t *testing.T) {
	t.Setenv("GOFLY_TEST_SECRET", "value")
	resolver := EnvSecretResolver{}
	if got, ok := resolver.LookupSecret("GOFLY_TEST_SECRET"); !ok || got != "value" {
		t.Fatalf("LookupSecret() = %q, %t; want value,true", got, ok)
	}
	if got, ok := resolver.LookupSecret(" "); ok || got != "" {
		t.Fatalf("LookupSecret(empty) = %q, %t; want empty,false", got, ok)
	}
}

type staticSecretResolver map[string]string

func (r staticSecretResolver) LookupSecret(name string) (string, bool) {
	value, ok := r[name]
	return value, ok && value != ""
}

type captureProvider struct {
	seen         Request
	complete     Response
	completeErr  error
	streamEvents []StreamEvent
	streamErr    error
	embed        EmbedResponse
	embedErr     error
}

func (p *captureProvider) Complete(_ context.Context, req Request) (Response, error) {
	p.seen = req
	return p.complete, p.completeErr
}

func (p *captureProvider) Stream(_ context.Context, req Request) (<-chan StreamEvent, error) {
	p.seen = req
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	ch := make(chan StreamEvent, len(p.streamEvents))
	for _, event := range p.streamEvents {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func (p *captureProvider) Embed(_ context.Context, _ EmbedRequest) (EmbedResponse, error) {
	return p.embed, p.embedErr
}

type streamProviderFunc func(context.Context, Request) (<-chan StreamEvent, error)

func (f streamProviderFunc) Complete(context.Context, Request) (Response, error) {
	return Response{}, nil
}

func (f streamProviderFunc) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	return f(ctx, req)
}

func (f streamProviderFunc) Embed(context.Context, EmbedRequest) (EmbedResponse, error) {
	return EmbedResponse{}, nil
}
