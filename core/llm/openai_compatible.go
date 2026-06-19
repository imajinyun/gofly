package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// ProviderOpenAICompatible is the registry name for the OpenAI-compatible HTTP adapter.
	ProviderOpenAICompatible = "openai-compatible"
	// EnvOpenAICompatibleAPIKey is the environment variable used for bearer credentials.
	// #nosec G101 -- this is an environment variable name, not a credential value.
	EnvOpenAICompatibleAPIKey = "GOFLY_LLM_OPENAI_API_KEY"
	// EnvOpenAICompatibleBaseURL overrides the default OpenAI-compatible API base URL.
	EnvOpenAICompatibleBaseURL = "GOFLY_LLM_OPENAI_BASE_URL"
	// EnvOpenAICompatibleAllowedHosts is a comma-separated host allowlist for custom endpoints.
	EnvOpenAICompatibleAllowedHosts = "GOFLY_LLM_OPENAI_ALLOWED_HOSTS"
	// EnvOpenAICompatibleMaxResponseBytes caps one provider response read.
	EnvOpenAICompatibleMaxResponseBytes = "GOFLY_LLM_OPENAI_MAX_RESPONSE_BYTES"
)

const (
	defaultOpenAICompatibleBaseURL          = "https://api.openai.com/v1"
	defaultOpenAICompatibleModel            = "gpt-4o-mini"
	defaultOpenAICompatibleTimeout          = 30 * time.Second
	defaultOpenAICompatibleMaxResponseBytes = int64(1 << 20)
)

var (
	// ErrProviderEndpointRejected reports that a provider endpoint failed scheme or allowlist validation.
	ErrProviderEndpointRejected = errors.New("llm provider endpoint rejected")
	// ErrProviderResponseTooLarge reports that a provider response exceeded the configured read limit.
	ErrProviderResponseTooLarge = errors.New("llm provider response too large")
	// ErrProviderRequestFailed reports that a provider returned a non-success status or malformed response.
	ErrProviderRequestFailed = errors.New("llm provider request failed")
	// ErrProviderConfigInvalid reports that provider configuration failed validation.
	ErrProviderConfigInvalid = errors.New("llm provider config invalid")
	// ErrProviderCapabilityUnsupported reports that the selected provider does not implement an operation.
	ErrProviderCapabilityUnsupported = errors.New("llm provider capability unsupported")
)

// OpenAICompatibleConfig configures the standard-library HTTP adapter. APIKey
// must come from a secret resolver or another external secret manager, not from
// .gofly/config.json.
type OpenAICompatibleConfig struct {
	BaseURL           string
	APIKey            string
	DefaultModel      string
	AllowedHosts      []string
	AllowInsecureHTTP bool
	HTTPClient        *http.Client
	Timeout           time.Duration
	MaxResponseBytes  int64
}

// OpenAICompatibleProvider is a minimal OpenAI-compatible chat completions
// adapter. It intentionally uses only the standard library and enforces bounded
// response reads, context timeouts, HTTPS by default and host allowlisting.
type OpenAICompatibleProvider struct {
	baseURL          *url.URL
	apiKey           string
	defaultModel     string
	httpClient       *http.Client
	timeout          time.Duration
	maxResponseBytes int64
}

// OpenAICompatibleProviderSpec returns the safe-to-publish registry metadata
// for the OpenAI-compatible adapter.
func OpenAICompatibleProviderSpec() ProviderSpec {
	return ProviderSpec{
		Name:            ProviderOpenAICompatible,
		DisplayName:     "OpenAI-compatible chat completions provider",
		DefaultModel:    defaultOpenAICompatibleModel,
		BuiltIn:         true,
		NetworkAccess:   true,
		RequiresSecrets: true,
		SecretEnvVars:   []string{EnvOpenAICompatibleAPIKey},
		ConfigEnvVars:   []string{EnvOpenAICompatibleAllowedHosts, EnvOpenAICompatibleBaseURL, EnvOpenAICompatibleMaxResponseBytes},
		Capabilities:    []string{"complete", "stream", "chat-completions", "http", "openai-compatible", "sse"},
		Models: []ProviderModelSpec{
			{Name: defaultOpenAICompatibleModel, Default: true, Capabilities: []string{"complete", "stream", "chat-completions", "json-mode", "tool-call", "sse"}, ContextWindow: 128000, MaxOutputTokens: 16384},
		},
	}
}

// NewOpenAICompatibleProviderFromConfig builds the adapter from registry config
// and environment-backed resolver values.
func NewOpenAICompatibleProviderFromConfig(config ProviderConfig) (Provider, error) {
	resolver := config.Secrets
	if resolver == nil {
		resolver = EnvSecretResolver{}
	}
	apiKey, ok := resolver.LookupSecret(EnvOpenAICompatibleAPIKey)
	if !ok {
		return nil, fmt.Errorf("%w: %s requires %s", ErrSecretNotFound, ProviderOpenAICompatible, EnvOpenAICompatibleAPIKey)
	}
	baseURL := defaultOpenAICompatibleBaseURL
	if value, ok := resolver.LookupSecret(EnvOpenAICompatibleBaseURL); ok {
		baseURL = value
	}
	allowedHosts := []string{"api.openai.com"}
	if value, ok := resolver.LookupSecret(EnvOpenAICompatibleAllowedHosts); ok {
		allowedHosts = splitCommaList(value)
	}
	maxResponseBytes := int64(0)
	if value, ok := resolver.LookupSecret(EnvOpenAICompatibleMaxResponseBytes); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("%w: %s must be a positive integer", ErrProviderConfigInvalid, EnvOpenAICompatibleMaxResponseBytes)
		}
		maxResponseBytes = parsed
	}
	return NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:          baseURL,
		APIKey:           apiKey,
		DefaultModel:     config.Model,
		AllowedHosts:     allowedHosts,
		MaxResponseBytes: maxResponseBytes,
	})
}

// NewOpenAICompatibleProvider validates config and creates an adapter instance.
func NewOpenAICompatibleProvider(config OpenAICompatibleConfig) (*OpenAICompatibleProvider, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = defaultOpenAICompatibleBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: parse base url: %v", ErrProviderEndpointRejected, err)
	}
	if err := validateProviderEndpoint(parsed, config.AllowedHosts, config.AllowInsecureHTTP); err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: %s requires %s", ErrSecretNotFound, ProviderOpenAICompatible, EnvOpenAICompatibleAPIKey)
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultOpenAICompatibleTimeout
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultOpenAICompatibleMaxResponseBytes
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	defaultModel := strings.TrimSpace(config.DefaultModel)
	if defaultModel == "" {
		defaultModel = defaultOpenAICompatibleModel
	}
	return &OpenAICompatibleProvider{
		baseURL:          parsed,
		apiKey:           apiKey,
		defaultModel:     defaultModel,
		httpClient:       client,
		timeout:          timeout,
		maxResponseBytes: maxResponseBytes,
	}, nil
}

// Complete calls an OpenAI-compatible /chat/completions endpoint.
func (p *OpenAICompatibleProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if p == nil {
		return Response{}, errors.New("llm openai-compatible provider is nil")
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	body, err := json.Marshal(p.chatRequest(req))
	if err != nil {
		return Response{}, fmt.Errorf("openai-compatible marshal request: %w", err)
	}
	endpoint := p.endpoint("chat/completions")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("openai-compatible create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("%w: complete request: %v", ErrProviderRequestFailed, err)
	}
	defer httpResp.Body.Close()
	data, err := readLimited(httpResp.Body, p.maxResponseBytes)
	if err != nil {
		return Response{}, err
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return Response{}, &ProviderHTTPError{Provider: ProviderOpenAICompatible, StatusCode: httpResp.StatusCode}
	}
	var decoded openAICompatibleChatResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Response{}, fmt.Errorf("%w: decode response: %v", ErrProviderRequestFailed, err)
	}
	if len(decoded.Choices) == 0 {
		return Response{}, fmt.Errorf("%w: response has no choices", ErrProviderRequestFailed)
	}
	return Response{
		Text: decoded.Choices[0].Message.Content,
		Usage: Usage{
			InputTokens:  decoded.Usage.PromptTokens,
			OutputTokens: decoded.Usage.CompletionTokens,
			TotalTokens:  decoded.Usage.TotalTokens,
		}.normalized(),
	}, nil
}

// Stream calls an OpenAI-compatible streaming /chat/completions endpoint and
// converts server-sent event data frames into provider-neutral stream events.
func (p *OpenAICompatibleProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if p == nil {
		return nil, errors.New("llm openai-compatible provider is nil")
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)

	requestPayload := p.chatRequest(req)
	requestPayload.Stream = true
	requestPayload.StreamOptions = &openAICompatibleStreamOptions{IncludeUsage: true}
	body, err := json.Marshal(requestPayload)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("openai-compatible marshal stream request: %w", err)
	}
	endpoint := p.endpoint("chat/completions")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("openai-compatible create stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: stream request: %v", ErrProviderRequestFailed, err)
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		_ = httpResp.Body.Close()
		cancel()
		return nil, &ProviderHTTPError{Provider: ProviderOpenAICompatible, StatusCode: httpResp.StatusCode}
	}

	out := make(chan StreamEvent, 1)
	go p.readSSEStream(ctx, cancel, httpResp.Body, out)
	return out, nil
}

// Embed is intentionally not implemented in this first HTTP adapter skeleton.
func (*OpenAICompatibleProvider) Embed(context.Context, EmbedRequest) (EmbedResponse, error) {
	return EmbedResponse{}, fmt.Errorf("%w: %s embed", ErrProviderCapabilityUnsupported, ProviderOpenAICompatible)
}

// ProviderHTTPError is a sanitized HTTP provider error. It deliberately omits
// response bodies because provider errors may echo prompts or credentials.
type ProviderHTTPError struct {
	Provider   string
	StatusCode int
}

func (e *ProviderHTTPError) Error() string {
	if e == nil {
		return ErrProviderRequestFailed.Error()
	}
	return fmt.Sprintf("%s: %s returned status %d", ErrProviderRequestFailed, e.Provider, e.StatusCode)
}

func (e *ProviderHTTPError) Unwrap() error { return ErrProviderRequestFailed }

// Retryable reports whether retrying the same provider request may succeed.
// Authentication and most client-side validation failures are intentionally
// non-retryable, while provider throttling and server-side failures are safe to
// surface as retryable to machine callers.
func (e *ProviderHTTPError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	case http.StatusUnauthorized, http.StatusForbidden:
		return false
	}
	return e.StatusCode >= http.StatusInternalServerError && e.StatusCode <= 599
}

// StatusClass returns a stable, low-cardinality classification for manifests,
// JSON errors and tests without exposing provider response bodies.
func (e *ProviderHTTPError) StatusClass() string {
	if e == nil || e.StatusCode <= 0 {
		return "unknown"
	}
	switch {
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden:
		return "auth"
	case e.StatusCode == http.StatusTooManyRequests:
		return "rate_limit"
	case e.StatusCode >= http.StatusInternalServerError && e.StatusCode <= 599:
		return "server"
	case e.StatusCode >= http.StatusBadRequest && e.StatusCode < http.StatusInternalServerError:
		return "client"
	default:
		return "http"
	}
}

type openAICompatibleChatRequest struct {
	Model         string                         `json:"model"`
	Messages      []openAICompatibleChatMessage  `json:"messages"`
	MaxTokens     int                            `json:"max_tokens,omitempty"`
	Stream        bool                           `json:"stream,omitempty"`
	StreamOptions *openAICompatibleStreamOptions `json:"stream_options,omitempty"`
}

type openAICompatibleStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAICompatibleChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAICompatibleChatResponse struct {
	Choices []struct {
		Message openAICompatibleChatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAICompatibleStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (p *OpenAICompatibleProvider) chatRequest(req Request) openAICompatibleChatRequest {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.defaultModel
	}
	messages := make([]openAICompatibleChatMessage, 0, max(1, len(req.Messages)))
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		messages = append(messages, openAICompatibleChatMessage{Role: role, Content: msg.Content})
	}
	if len(messages) == 0 {
		messages = append(messages, openAICompatibleChatMessage{Role: "user", Content: req.Prompt})
	}
	return openAICompatibleChatRequest{Model: model, Messages: messages, MaxTokens: max(0, req.MaxOutputTokens)}
}

func (p *OpenAICompatibleProvider) endpoint(path string) string {
	base := *p.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	base.RawQuery = ""
	base.Fragment = ""
	return base.String()
}

func (p *OpenAICompatibleProvider) readSSEStream(ctx context.Context, cancel context.CancelFunc, body io.ReadCloser, out chan<- StreamEvent) {
	defer cancel()
	defer close(out)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	maxToken := scannerMaxTokenBytes(p.maxResponseBytes)
	scanner.Buffer(make([]byte, 0, min(maxToken, 4096)), maxToken)
	var readBytes int64
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := scanner.Text()
		readBytes += int64(len(line)) + 1
		if readBytes > p.maxResponseBytes {
			sendOpenAIStreamEvent(ctx, out, StreamEvent{Err: fmt.Errorf("%w: stream exceeds %d bytes", ErrProviderResponseTooLarge, p.maxResponseBytes)})
			return
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			sendOpenAIStreamEvent(ctx, out, StreamEvent{Done: true})
			return
		}
		var decoded openAICompatibleStreamResponse
		if err := json.Unmarshal([]byte(data), &decoded); err != nil {
			sendOpenAIStreamEvent(ctx, out, StreamEvent{Err: fmt.Errorf("%w: decode stream event: %v", ErrProviderRequestFailed, err)})
			return
		}
		if decoded.Usage.PromptTokens != 0 || decoded.Usage.CompletionTokens != 0 || decoded.Usage.TotalTokens != 0 {
			if !sendOpenAIStreamEvent(ctx, out, StreamEvent{Usage: Usage{InputTokens: decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens, TotalTokens: decoded.Usage.TotalTokens}.normalized()}) {
				return
			}
		}
		for _, choice := range decoded.Choices {
			if choice.Delta.Content != "" {
				if !sendOpenAIStreamEvent(ctx, out, StreamEvent{Delta: choice.Delta.Content}) {
					return
				}
			}
			if choice.FinishReason != nil {
				if !sendOpenAIStreamEvent(ctx, out, StreamEvent{Done: true}) {
					return
				}
				return
			}
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		streamErr := fmt.Errorf("%w: read stream: %v", ErrProviderRequestFailed, err)
		if len(err.Error()) > 0 && strings.Contains(strings.ToLower(err.Error()), "token too long") {
			streamErr = fmt.Errorf("%w: stream event exceeds %d bytes", ErrProviderResponseTooLarge, p.maxResponseBytes)
		}
		sendOpenAIStreamEvent(ctx, out, StreamEvent{Err: streamErr})
	}
}

func sendOpenAIStreamEvent(ctx context.Context, out chan<- StreamEvent, event StreamEvent) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func scannerMaxTokenBytes(maxBytes int64) int {
	if maxBytes <= 0 {
		maxBytes = defaultOpenAICompatibleMaxResponseBytes
	}
	if maxBytes > int64(math.MaxInt-1) {
		return math.MaxInt
	}
	return int(maxBytes) + 1
}

func validateProviderEndpoint(endpoint *url.URL, allowedHosts []string, allowInsecureHTTP bool) error {
	if endpoint == nil || endpoint.Host == "" {
		return fmt.Errorf("%w: endpoint host is required", ErrProviderEndpointRejected)
	}
	scheme := strings.ToLower(endpoint.Scheme)
	if scheme != "https" && (!allowInsecureHTTP || scheme != "http") {
		return fmt.Errorf("%w: endpoint must use https", ErrProviderEndpointRejected)
	}
	allowed := cleanStringList(allowedHosts)
	if len(allowed) == 0 {
		return fmt.Errorf("%w: endpoint allowlist is required", ErrProviderEndpointRejected)
	}
	host := strings.ToLower(endpoint.Hostname())
	for _, allowedHost := range allowed {
		if host == strings.ToLower(allowedHost) {
			return nil
		}
	}
	return fmt.Errorf("%w: host %s is not allowlisted", ErrProviderEndpointRejected, host)
}

func readLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultOpenAICompatibleMaxResponseBytes
	}
	limited := io.LimitReader(reader, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrProviderRequestFailed, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: limit %d bytes", ErrProviderResponseTooLarge, maxBytes)
	}
	return data, nil
}

func splitCommaList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

var _ Provider = (*OpenAICompatibleProvider)(nil)
