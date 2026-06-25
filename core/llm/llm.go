// Package llm provides provider-neutral governance primitives for LLM calls:
// provider abstraction, token budgeting, rate limiting, redaction and audit
// logging. It intentionally avoids vendor SDK dependencies so services can
// wrap any model provider behind the same production controls.
package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/observability"
)

var (
	// ErrBudgetExceeded reports that an LLM request would exceed its token budget.
	ErrBudgetExceeded = errors.New("llm token budget exceeded")
	// ErrRateLimited reports that an LLM request was rejected by rate limiting.
	ErrRateLimited = errors.New("llm rate limited")
)

const defaultMaxStreamEventBytes = 64 << 10

// Provider is the provider-neutral LLM contract used by gofly services.
// Implementations may wrap OpenAI-compatible APIs, local models, in-house
// gateways or test fakes without changing callers.
type Provider interface {
	Complete(context.Context, Request) (Response, error)
	Stream(context.Context, Request) (<-chan StreamEvent, error)
	Embed(context.Context, EmbedRequest) (EmbedResponse, error)
}

// Message is one chat-style input message.
type Message struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content"`
}

// Request describes a text generation request.
type Request struct {
	Provider        string            `json:"provider,omitempty"`
	Model           string            `json:"model,omitempty"`
	Prompt          string            `json:"prompt,omitempty"`
	Messages        []Message         `json:"messages,omitempty"`
	MaxInputTokens  int               `json:"maxInputTokens,omitempty"`
	MaxOutputTokens int               `json:"maxOutputTokens,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Response describes a text generation response.
type Response struct {
	Text  string `json:"text,omitempty"`
	Usage Usage  `json:"usage,omitempty"`
}

// StreamEvent is one streaming generation event.
type StreamEvent struct {
	Delta string `json:"delta,omitempty"`
	Usage Usage  `json:"usage,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Err   error  `json:"-"`
}

// EmbedRequest describes an embedding request.
type EmbedRequest struct {
	Provider string            `json:"provider,omitempty"`
	Model    string            `json:"model,omitempty"`
	Inputs   []string          `json:"inputs"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// EmbedResponse describes an embedding response.
type EmbedResponse struct {
	Vectors [][]float64 `json:"vectors,omitempty"`
	Usage   Usage       `json:"usage,omitempty"`
}

// Usage captures token usage for budgeting and audit records.
type Usage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
	TotalTokens  int `json:"totalTokens,omitempty"`
}

func (u Usage) normalized() Usage {
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u
}

// EstimateTokens returns a deterministic, dependency-free token estimate. It
// intentionally over-approximates short text by counting roughly four UTF-8
// runes per token and returning one token for any non-empty string.
func EstimateTokens(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	runes := utf8.RuneCountInString(s)
	return max(1, (runes+3)/4)
}

func requestInputTokens(req Request) int {
	tokens := EstimateTokens(req.Prompt)
	for _, msg := range req.Messages {
		tokens += EstimateTokens(msg.Role)
		tokens += EstimateTokens(msg.Content)
	}
	if req.MaxInputTokens > 0 && req.MaxInputTokens < tokens {
		return req.MaxInputTokens
	}
	return tokens
}

func embedInputTokens(req EmbedRequest) int {
	tokens := 0
	for _, input := range req.Inputs {
		tokens += EstimateTokens(input)
	}
	return tokens
}

// TokenBudget limits cumulative input, output and total tokens.
type TokenBudget struct {
	mu        sync.Mutex
	maxInput  int
	maxOutput int
	maxTotal  int
	usedInput int
	usedOut   int
}

// NewTokenBudget creates a cumulative token budget. A non-positive limit means
// that dimension is unlimited.
func NewTokenBudget(maxInput, maxOutput, maxTotal int) *TokenBudget {
	return &TokenBudget{maxInput: maxInput, maxOutput: maxOutput, maxTotal: maxTotal}
}

// BudgetSnapshot is a point-in-time view of token budget usage.
type BudgetSnapshot struct {
	MaxInput     int `json:"maxInput,omitempty"`
	MaxOutput    int `json:"maxOutput,omitempty"`
	MaxTotal     int `json:"maxTotal,omitempty"`
	UsedInput    int `json:"usedInput,omitempty"`
	UsedOutput   int `json:"usedOutput,omitempty"`
	UsedTotal    int `json:"usedTotal,omitempty"`
	RemainInput  int `json:"remainInput,omitempty"`
	RemainOutput int `json:"remainOutput,omitempty"`
	RemainTotal  int `json:"remainTotal,omitempty"`
}

// Reserve records intended usage if it fits within all configured limits.
func (b *TokenBudget) Reserve(inputTokens, outputTokens int) (Usage, error) {
	if b == nil {
		return Usage{InputTokens: max(0, inputTokens), OutputTokens: max(0, outputTokens)}.normalized(), nil
	}
	inputTokens = max(0, inputTokens)
	outputTokens = max(0, outputTokens)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxInput > 0 && b.usedInput+inputTokens > b.maxInput {
		return Usage{}, fmt.Errorf("%w: input tokens %d exceed remaining %d", ErrBudgetExceeded, inputTokens, b.maxInput-b.usedInput)
	}
	if b.maxOutput > 0 && b.usedOut+outputTokens > b.maxOutput {
		return Usage{}, fmt.Errorf("%w: output tokens %d exceed remaining %d", ErrBudgetExceeded, outputTokens, b.maxOutput-b.usedOut)
	}
	if b.maxTotal > 0 && b.usedInput+b.usedOut+inputTokens+outputTokens > b.maxTotal {
		return Usage{}, fmt.Errorf("%w: total tokens %d exceed remaining %d", ErrBudgetExceeded, inputTokens+outputTokens, b.maxTotal-b.usedInput-b.usedOut)
	}
	b.usedInput += inputTokens
	b.usedOut += outputTokens
	return Usage{InputTokens: inputTokens, OutputTokens: outputTokens}.normalized(), nil
}

// Snapshot returns the current budget state.
func (b *TokenBudget) Snapshot() BudgetSnapshot {
	if b == nil {
		return BudgetSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return BudgetSnapshot{
		MaxInput:     b.maxInput,
		MaxOutput:    b.maxOutput,
		MaxTotal:     b.maxTotal,
		UsedInput:    b.usedInput,
		UsedOutput:   b.usedOut,
		UsedTotal:    b.usedInput + b.usedOut,
		RemainInput:  remaining(b.maxInput, b.usedInput),
		RemainOutput: remaining(b.maxOutput, b.usedOut),
		RemainTotal:  remaining(b.maxTotal, b.usedInput+b.usedOut),
	}
}

func remaining(limitValue, used int) int {
	if limitValue <= 0 {
		return 0
	}
	return max(0, limitValue-used)
}

// RateLimiter wraps gofly's token bucket limiter for provider calls.
type RateLimiter struct {
	limiter *limit.Limiter
	check   time.Duration
}

// NewRateLimiter creates a token-bucket limiter with calls-per-second rate and
// burst capacity.
func NewRateLimiter(rate, burst int) *RateLimiter {
	return &RateLimiter{limiter: limit.New(rate, burst), check: 10 * time.Millisecond}
}

// Allow reports whether one call may proceed immediately.
func (r *RateLimiter) Allow() bool {
	return r == nil || r.limiter == nil || r.limiter.Allow()
}

// Wait blocks until a call may proceed or ctx is canceled.
func (r *RateLimiter) Wait(ctx context.Context) error {
	if r.Allow() {
		return nil
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	ticker := time.NewTicker(r.check)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %v", ErrRateLimited, ctx.Err())
		case <-ticker.C:
			if r.Allow() {
				return nil
			}
		}
	}
}

// Redactor removes secrets and common PII from prompts, metadata and audit
// records before they leave the process or enter logs.
type Redactor struct {
	rules []redactionRule
}

type redactionRule struct {
	re *regexp.Regexp
	to string
}

var defaultRedactionRules = []redactionRule{
	{regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), "[REDACTED_PRIVATE_KEY]"},
	{regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|token|secret|password|authorization)(\s*[:=]\s*)([^\s,;"']+)`), "$1$2[REDACTED]"},
	{regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`), "[REDACTED_EMAIL]"},
}

// DefaultRedactor returns a redactor with built-in secret and email patterns.
func DefaultRedactor() *Redactor {
	return &Redactor{rules: append([]redactionRule(nil), defaultRedactionRules...)}
}

// Redact returns s with sensitive substrings replaced.
func (r *Redactor) Redact(s string) string {
	if r == nil {
		r = DefaultRedactor()
	}
	for _, rule := range r.rules {
		s = rule.re.ReplaceAllString(s, rule.to)
	}
	return s
}

// RedactMetadata returns a sanitized copy of metadata.
func (r *Redactor) RedactMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for k, v := range metadata {
		out[k] = r.Redact(v)
	}
	return out
}

// AuditRecord is the structured event emitted for a governed provider call.
type AuditRecord struct {
	Time               time.Time         `json:"time"`
	Operation          string            `json:"operation"`
	Provider           string            `json:"provider,omitempty"`
	Model              string            `json:"model,omitempty"`
	Status             string            `json:"status"`
	Duration           time.Duration     `json:"duration"`
	Usage              Usage             `json:"usage,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Error              string            `json:"error,omitempty"`
	ErrorClass         string            `json:"errorClass,omitempty"`
	Retryable          bool              `json:"retryable,omitempty"`
	ProviderStatusCode int               `json:"providerStatusCode,omitempty"`
	StreamEvents       int               `json:"streamEvents,omitempty"`
	Redacted           bool              `json:"redacted"`
	RateLimited        bool              `json:"rateLimited,omitempty"`
	BudgetDenied       bool              `json:"budgetDenied,omitempty"`
}

// AuditLogger emits structured LLM audit logs with trace/request attributes.
type AuditLogger struct {
	logger   *slog.Logger
	redactor *Redactor
}

// NewAuditLogger creates an audit logger. Nil logger uses slog.Default().
func NewAuditLogger(logger *slog.Logger, redactor *Redactor) *AuditLogger {
	if logger == nil {
		logger = slog.Default()
	}
	if redactor == nil {
		redactor = DefaultRedactor()
	}
	return &AuditLogger{logger: logger, redactor: redactor}
}

// Record writes one audit event. It never logs prompt text or model output.
func (a *AuditLogger) Record(ctx context.Context, record AuditRecord) {
	if a == nil {
		a = NewAuditLogger(nil, nil)
	}
	if record.Time.IsZero() {
		record.Time = time.Now()
	}
	if record.Error != "" {
		record.Error = a.redactor.Redact(record.Error)
	}
	attrs := []any{
		"operation", record.Operation,
		"provider", record.Provider,
		"model", record.Model,
		"status", record.Status,
		"duration", record.Duration,
		"input_tokens", record.Usage.InputTokens,
		"output_tokens", record.Usage.OutputTokens,
		"total_tokens", record.Usage.normalized().TotalTokens,
		"redacted", record.Redacted,
	}
	if record.ErrorClass != "" {
		attrs = append(attrs, "error_class", record.ErrorClass, "retryable", record.Retryable)
	}
	if record.ProviderStatusCode != 0 {
		attrs = append(attrs, "provider_status_code", record.ProviderStatusCode)
	}
	if record.StreamEvents != 0 {
		attrs = append(attrs, "stream_events", record.StreamEvents)
	}
	if len(record.Metadata) > 0 {
		attrs = append(attrs, "metadata", a.redactor.RedactMetadata(record.Metadata))
	}
	attrs = append(attrs, observability.TraceAttrs(ctx)...)
	if record.Error != "" {
		attrs = append(attrs, "error", record.Error, "rate_limited", record.RateLimited, "budget_denied", record.BudgetDenied)
		a.logger.WarnContext(ctx, "llm call audited", attrs...)
		return
	}
	a.logger.InfoContext(ctx, "llm call audited", attrs...)
}

// GovernedProvider applies budget, rate-limit, redaction and audit controls
// before delegating to an underlying Provider.
type GovernedProvider struct {
	provider       Provider
	budget         *TokenBudget
	rateLimiter    *RateLimiter
	redactor       *Redactor
	auditor        *AuditLogger
	observer       *observability.Observer
	redactRequests bool
	maxStreamBytes int
}

// Option configures a GovernedProvider.
type Option func(*GovernedProvider)

// WithTokenBudget enables cumulative token budgeting.
func WithTokenBudget(budget *TokenBudget) Option {
	return func(p *GovernedProvider) { p.budget = budget }
}

// WithRateLimiter enables call rate limiting.
func WithRateLimiter(rateLimiter *RateLimiter) Option {
	return func(p *GovernedProvider) { p.rateLimiter = rateLimiter }
}

// WithRedactor replaces the default redactor.
func WithRedactor(redactor *Redactor) Option {
	return func(p *GovernedProvider) { p.redactor = redactor }
}

// WithAuditLogger replaces the default audit logger.
func WithAuditLogger(auditor *AuditLogger) Option {
	return func(p *GovernedProvider) { p.auditor = auditor }
}

// WithObserver records governed provider calls through gofly observability.
func WithObserver(observer *observability.Observer) Option {
	return func(p *GovernedProvider) { p.observer = observer }
}

// WithRequestRedaction controls whether prompts are sanitized before provider
// calls. Audit records are always sanitized.
func WithRequestRedaction(enabled bool) Option {
	return func(p *GovernedProvider) { p.redactRequests = enabled }
}

// WithMaxStreamEventBytes limits one provider stream delta. Non-positive values
// restore the safe default limit.
func WithMaxStreamEventBytes(maxBytes int) Option {
	return func(p *GovernedProvider) { p.maxStreamBytes = maxBytes }
}

// NewGovernedProvider creates a Provider wrapper. A nil provider is replaced by
// NoOpProvider so tests and disabled integrations remain safe.
func NewGovernedProvider(provider Provider, opts ...Option) *GovernedProvider {
	if provider == nil {
		provider = NoOpProvider{}
	}
	p := &GovernedProvider{provider: provider, redactor: DefaultRedactor(), redactRequests: true, maxStreamBytes: defaultMaxStreamEventBytes}
	for _, opt := range opts {
		opt(p)
	}
	if p.redactor == nil {
		p.redactor = DefaultRedactor()
	}
	if p.auditor == nil {
		p.auditor = NewAuditLogger(nil, p.redactor)
	}
	if p.maxStreamBytes <= 0 {
		p.maxStreamBytes = defaultMaxStreamEventBytes
	}
	return p
}

// Complete applies governance and executes a non-streaming completion.
func (p *GovernedProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if ctx == nil {
		ctx = context.TODO()
	}
	inputTokens := requestInputTokens(req)
	outputTokens := max(0, req.MaxOutputTokens)
	usage, err := p.reserve(ctx, "complete", req.Provider, req.Model, req.Metadata, inputTokens, outputTokens)
	if err != nil {
		return Response{}, err
	}
	req = p.redactRequest(req)
	op := p.start("llm.complete", req.Provider, req.Model)
	started := time.Now()
	resp, err := p.provider.Complete(ctx, req)
	duration := time.Since(started)
	status := auditStatus(err)
	if resp.Usage.TotalTokens != 0 || resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != 0 {
		usage = resp.Usage.normalized()
	}
	p.end(ctx, op, statusCode(err), err, telemetryAttrs(err, 0)...)
	p.audit(ctx, newAuditRecord("complete", req.Provider, req.Model, status, duration, usage, req.Metadata, err, p.redactRequests, 0))
	if err != nil {
		return Response{}, fmt.Errorf("llm complete: %w", err)
	}
	return resp, nil
}

// Stream applies governance and starts a streaming completion.
func (p *GovernedProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if ctx == nil {
		ctx = context.TODO()
	}
	inputTokens := requestInputTokens(req)
	outputTokens := max(0, req.MaxOutputTokens)
	usage, err := p.reserve(ctx, "stream", req.Provider, req.Model, req.Metadata, inputTokens, outputTokens)
	if err != nil {
		return nil, err
	}
	req = p.redactRequest(req)
	op := p.start("llm.stream", req.Provider, req.Model)
	started := time.Now()
	stream, err := p.provider.Stream(ctx, req)
	if err != nil {
		duration := time.Since(started)
		p.end(ctx, op, statusCode(err), err, telemetryAttrs(err, 0)...)
		p.audit(ctx, newAuditRecord("stream", req.Provider, req.Model, auditStatus(err), duration, usage, req.Metadata, err, p.redactRequests, 0))
		return nil, fmt.Errorf("llm stream: %w", err)
	}
	if stream == nil {
		err := fmt.Errorf("%w: provider returned nil stream", ErrProviderRequestFailed)
		duration := time.Since(started)
		p.end(ctx, op, statusCode(err), err, telemetryAttrs(err, 0)...)
		p.audit(ctx, newAuditRecord("stream", req.Provider, req.Model, auditStatus(err), duration, usage, req.Metadata, err, p.redactRequests, 0))
		return nil, fmt.Errorf("llm stream: %w", err)
	}
	return p.governStream(ctx, stream, req, op, started, usage), nil
}

func (p *GovernedProvider) governStream(ctx context.Context, stream <-chan StreamEvent, req Request, op *observability.Operation, started time.Time, usage Usage) <-chan StreamEvent {
	out := make(chan StreamEvent, 1)
	go func() {
		defer close(out)
		var finalErr error
		var streamEvents int
		finalUsage := usage
		defer func() {
			p.end(ctx, op, statusCode(finalErr), finalErr, telemetryAttrs(finalErr, streamEvents)...)
			p.audit(ctx, newAuditRecord("stream", req.Provider, req.Model, auditStatus(finalErr), time.Since(started), finalUsage, req.Metadata, finalErr, p.redactRequests, streamEvents))
		}()
		for {
			select {
			case <-ctx.Done():
				finalErr = ctx.Err()
				out <- StreamEvent{Done: true, Err: fmt.Errorf("%w: stream canceled: %v", ErrProviderRequestFailed, ctx.Err())}
				return
			case event, ok := <-stream:
				if !ok {
					return
				}
				streamEvents++
				if event.Usage.TotalTokens != 0 || event.Usage.InputTokens != 0 || event.Usage.OutputTokens != 0 {
					finalUsage = event.Usage.normalized()
				}
				if event.Err != nil {
					finalErr = event.Err
					event.Err = fmt.Errorf("%w: stream event: %s", ErrProviderRequestFailed, p.redactor.Redact(event.Err.Error()))
					event.Done = true
					p.sendStreamEvent(ctx, out, event)
					return
				}
				if len([]byte(event.Delta)) > p.maxStreamBytes {
					finalErr = ErrProviderResponseTooLarge
					p.sendStreamEvent(ctx, out, StreamEvent{Done: true, Err: fmt.Errorf("%w: stream event exceeds %d bytes", ErrProviderResponseTooLarge, p.maxStreamBytes)})
					return
				}
				if !p.sendStreamEvent(ctx, out, event) {
					finalErr = ctx.Err()
					return
				}
			}
		}
	}()
	return out
}

func (p *GovernedProvider) sendStreamEvent(ctx context.Context, out chan<- StreamEvent, event StreamEvent) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// Embed applies governance and executes an embedding request.
func (p *GovernedProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if ctx == nil {
		ctx = context.TODO()
	}
	usage, err := p.reserve(ctx, "embed", req.Provider, req.Model, req.Metadata, embedInputTokens(req), 0)
	if err != nil {
		return EmbedResponse{}, err
	}
	req = p.redactEmbedRequest(req)
	op := p.start("llm.embed", req.Provider, req.Model)
	started := time.Now()
	resp, err := p.provider.Embed(ctx, req)
	duration := time.Since(started)
	if resp.Usage.TotalTokens != 0 || resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != 0 {
		usage = resp.Usage.normalized()
	}
	p.end(ctx, op, statusCode(err), err, telemetryAttrs(err, 0)...)
	p.audit(ctx, newAuditRecord("embed", req.Provider, req.Model, auditStatus(err), duration, usage, req.Metadata, err, p.redactRequests, 0))
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("llm embed: %w", err)
	}
	return resp, nil
}

func (p *GovernedProvider) reserve(ctx context.Context, operation, provider, model string, metadata map[string]string, inputTokens, outputTokens int) (Usage, error) {
	if p == nil {
		return Usage{}, errors.New("llm governed provider is nil")
	}
	if p.rateLimiter != nil && !p.rateLimiter.Allow() {
		err := ErrRateLimited
		record := newAuditRecord(operation, provider, model, "rejected", 0, Usage{}, metadata, err, p.redactRequests, 0)
		record.RateLimited = true
		p.audit(ctx, record)
		return Usage{}, err
	}
	usage, err := p.budget.Reserve(inputTokens, outputTokens)
	if err != nil {
		record := newAuditRecord(operation, provider, model, "rejected", 0, Usage{}, metadata, err, p.redactRequests, 0)
		record.BudgetDenied = true
		p.audit(ctx, record)
		return Usage{}, err
	}
	return usage, nil
}

func (p *GovernedProvider) redactRequest(req Request) Request {
	if p == nil || !p.redactRequests {
		return req
	}
	req.Prompt = p.redactor.Redact(req.Prompt)
	for i := range req.Messages {
		req.Messages[i].Content = p.redactor.Redact(req.Messages[i].Content)
	}
	req.Metadata = p.redactor.RedactMetadata(req.Metadata)
	return req
}

func (p *GovernedProvider) redactEmbedRequest(req EmbedRequest) EmbedRequest {
	if p == nil || !p.redactRequests {
		return req
	}
	for i := range req.Inputs {
		req.Inputs[i] = p.redactor.Redact(req.Inputs[i])
	}
	req.Metadata = p.redactor.RedactMetadata(req.Metadata)
	return req
}

func (p *GovernedProvider) audit(ctx context.Context, record AuditRecord) {
	if p != nil && p.auditor != nil {
		p.auditor.Record(ctx, record)
	}
}

func (p *GovernedProvider) start(name, provider, model string) *observability.Operation {
	if p == nil || p.observer == nil {
		return nil
	}
	return p.observer.Start(name, "provider", provider, "model", model, "operation", strings.TrimPrefix(name, "llm."))
}

func (p *GovernedProvider) end(ctx context.Context, op *observability.Operation, status int, err error, attrs ...any) {
	if op != nil {
		op.End(ctx, status, err, "llm provider call", attrs...)
	}
}

func newAuditRecord(operation, provider, model, status string, duration time.Duration, usage Usage, metadata map[string]string, err error, redacted bool, streamEvents int) AuditRecord {
	record := AuditRecord{
		Operation:    operation,
		Provider:     provider,
		Model:        model,
		Status:       status,
		Duration:     duration,
		Usage:        usage,
		Metadata:     metadata,
		Error:        errorString(err),
		Redacted:     redacted,
		StreamEvents: streamEvents,
	}
	if err != nil {
		record.ErrorClass = errorClass(err)
		record.Retryable = errorRetryable(err)
		record.ProviderStatusCode = providerStatusCode(err)
	}
	return record
}

func telemetryAttrs(err error, streamEvents int) []any {
	attrs := make([]any, 0, 8)
	if err != nil {
		attrs = append(attrs, "error_class", errorClass(err), "retryable", errorRetryable(err))
		if code := providerStatusCode(err); code != 0 {
			attrs = append(attrs, "provider_status_code", code)
		}
	}
	if streamEvents != 0 {
		attrs = append(attrs, "stream_events", streamEvents)
	}
	return attrs
}

type retryableError interface {
	Retryable() bool
}

func errorRetryable(err error) bool {
	if err == nil {
		return false
	}
	var retryable retryableError
	if errors.As(err, &retryable) {
		return retryable.Retryable()
	}
	return errors.Is(err, ErrRateLimited) || errors.Is(err, context.DeadlineExceeded)
}

func errorClass(err error) string {
	if err == nil {
		return ""
	}
	var httpErr *ProviderHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusClass()
	}
	switch {
	case errors.Is(err, ErrBudgetExceeded):
		return "budget"
	case errors.Is(err, ErrRateLimited):
		return "rate_limit"
	case errors.Is(err, ErrProviderResponseTooLarge):
		return "response_too_large"
	case errors.Is(err, ErrProviderEndpointRejected):
		return "endpoint_rejected"
	case errors.Is(err, ErrProviderConfigInvalid):
		return "provider_config"
	case errors.Is(err, ErrProviderCapabilityUnsupported):
		return "capability"
	case errors.Is(err, ErrProviderRequestFailed):
		return "provider_request"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	default:
		return "unknown"
	}
}

func providerStatusCode(err error) int {
	var httpErr *ProviderHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	return 0
}

func auditStatus(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func statusCode(err error) int {
	if err == nil {
		return 200
	}
	if code := providerStatusCode(err); code != 0 {
		return code
	}
	switch {
	case errors.Is(err, ErrRateLimited):
		return 429
	case errors.Is(err, ErrBudgetExceeded), errors.Is(err, ErrProviderConfigInvalid), errors.Is(err, ErrProviderEndpointRejected), errors.Is(err, ErrProviderCapabilityUnsupported):
		return 400
	case errors.Is(err, context.Canceled):
		return 499
	case errors.Is(err, context.DeadlineExceeded):
		return 504
	default:
		return 500
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// NoOpProvider is a deterministic provider for tests and disabled LLM wiring.
type NoOpProvider struct{}

// Complete returns an empty response with estimated input usage.
func (NoOpProvider) Complete(_ context.Context, req Request) (Response, error) {
	return Response{Usage: Usage{InputTokens: requestInputTokens(req)}.normalized()}, nil
}

// Stream returns a closed stream with one terminal event.
func (NoOpProvider) Stream(_ context.Context, req Request) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Usage: Usage{InputTokens: requestInputTokens(req)}.normalized(), Done: true}
	close(ch)
	return ch, nil
}

// Embed returns zero vectors sized to the request inputs with estimated usage.
func (NoOpProvider) Embed(_ context.Context, req EmbedRequest) (EmbedResponse, error) {
	return EmbedResponse{Vectors: make([][]float64, len(req.Inputs)), Usage: Usage{InputTokens: embedInputTokens(req)}.normalized()}, nil
}

var _ Provider = (*GovernedProvider)(nil)
var _ Provider = NoOpProvider{}
