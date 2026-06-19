// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	core "github.com/gofly/gofly/core"
	"github.com/gofly/gofly/core/breaker"
	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/limit"
	coreretry "github.com/gofly/gofly/core/retry"
	"github.com/gofly/gofly/core/security"
)

// ClientOption customises the REST client.
type ClientOption func(*clientOptions)

type clientOptions struct {
	service     string
	timeout     time.Duration
	httpClient  *http.Client
	manager     *governance.Manager
	rules       *governance.RuleSet
	retryPolicy coreretry.Policy
	tls         *security.TLSConfig
}

// Client is a governance-aware HTTP client with retries and circuit breaking.
type Client struct {
	baseURL string
	hc      *http.Client
	opts    clientOptions
	runtime *ruleRuntime
}

type restStatusError struct {
	status int
	method string
	url    string
}

func (e restStatusError) Error() string {
	return fmt.Sprintf("rest %s %s returned status %d", e.method, e.url, e.status)
}

func NewClient(baseURL string, opts ...ClientOption) (*Client, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil, errors.New("base url is required")
	}
	o := clientOptions{timeout: 3 * time.Second, retryPolicy: coreretry.Policy{Attempts: 1}}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	hc := o.httpClient
	if hc == nil {
		hc = core.DefaultHTTPClient()
	}
	if o.tls != nil {
		var err error
		hc, err = clientWithTLS(hc, *o.tls)
		if err != nil {
			return nil, err
		}
	}
	return &Client{baseURL: baseURL, hc: hc, opts: o, runtime: newRuleRuntime()}, nil
}

func MustNewClient(baseURL string, opts ...ClientOption) *Client {
	c, err := NewClient(baseURL, opts...)
	if err != nil {
		panic(err)
	}
	return c
}

func WithClientService(service string) ClientOption {
	return func(o *clientOptions) {
		o.service = service
	}
}

func WithClientTimeout(timeout time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.timeout = timeout
	}
}

func WithClientHTTPClient(hc *http.Client) ClientOption {
	return func(o *clientOptions) {
		o.httpClient = hc
	}
}

func WithClientGovernanceRuleSet(rules *governance.RuleSet) ClientOption {
	return func(o *clientOptions) {
		o.rules = rules
	}
}

func WithClientGovernanceManager(manager *governance.Manager) ClientOption {
	return func(o *clientOptions) {
		o.manager = manager
		if manager != nil {
			o.rules = manager.RuleSet()
		}
	}
}

func WithClientGovernanceSuite(suite *governance.Suite) ClientOption {
	return func(o *clientOptions) {
		if o.manager != nil {
			return
		}
		if suite != nil {
			o.rules = governance.MergeRuleSets(o.rules, suite.RuleSet())
		}
	}
}

func WithClientRetry(policy coreretry.Policy) ClientOption {
	return func(o *clientOptions) {
		o.retryPolicy = policy
	}
}

// WithClientTLS configures TLS or mutual TLS for the REST client. Provide
// CAFile to verify the server and CertFile/KeyFile to present a client identity
// (mTLS). The base URL must use the https:// scheme for TLS to take effect.
func WithClientTLS(cfg security.TLSConfig) ClientOption {
	return func(o *clientOptions) {
		c := cfg
		o.tls = &c
	}
}

func clientWithTLS(hc *http.Client, cfg security.TLSConfig) (*http.Client, error) {
	tlsCfg, err := cfg.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("configure rest tls: %w", err)
	}
	if tlsCfg == nil {
		return hc, nil
	}
	if hc == nil {
		hc = core.DefaultHTTPClient()
	}
	out := *hc
	if transport, ok := hc.Transport.(*http.Transport); ok {
		clone := transport.Clone()
		clone.TLSClientConfig = tlsCfg
		out.Transport = clone
	} else {
		out.Transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	return &out, nil
}

func (c *Client) NewRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if c == nil {
		return nil, errors.New("rest client is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	url := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		url = c.baseURL + "/" + strings.TrimLeft(path, "/")
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create rest request: %w", err)
	}
	return req, nil
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c == nil {
		return nil, errors.New("rest client is nil")
	}
	if req == nil {
		return nil, errors.New("rest request is nil")
	}
	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	governanceReq := governance.HTTPRequest(governance.TransportREST, c.opts.service, req, nil)
	decision := c.governanceDecision(governanceReq)
	policy := decision.Policy
	runtimeKey := governanceRuntimeKey(decision, req.Method+" "+req.URL.Path)
	if limiter := c.ruleRateLimiter(runtimeKey, policy.RateLimit); limiter != nil && !limiter.Allow() {
		return nil, coreerrors.New(coreerrors.CodeResourceExhausted, "too many requests")
	}
	if limiter := c.ruleConcurrencyLimiter(runtimeKey, policy.Concurrency); limiter != nil {
		if !limiter.TryAcquire() {
			return nil, coreerrors.New(coreerrors.CodeUnavailable, "too many concurrent requests")
		}
		defer limiter.Release()
	}
	if policy.MaxBodyBytes > 0 && req.ContentLength > policy.MaxBodyBytes {
		return nil, coreerrors.New(coreerrors.CodeResourceExhausted, "request body too large")
	}
	timeout := c.opts.timeout
	if policy.Timeout > 0 {
		timeout = policy.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	retryPolicy := c.retryPolicy(policy.Retry)
	if retryPolicy.Attempts > 1 && req.Body != nil && req.Body != http.NoBody && req.GetBody == nil && requestMayRetry(req.Method, policy.Retry) {
		return nil, errors.New("rest request body is not replayable for retry")
	}
	var resp *http.Response
	call := func() error {
		attemptReq, err := c.prepareRequest(ctx, req, policy, governanceReq)
		if err != nil {
			return err
		}
		resp, err = c.hc.Do(attemptReq) //nolint:bodyclose // successful response body is returned to the caller for ownership and closing
		if err != nil {
			return normalizeClientContextError(ctx, coreerrors.Wrap(coreerrors.CodeUnavailable, "send request", err))
		}
		if shouldRetryStatus(resp.StatusCode, req.Method, policy.Retry) {
			closeResponseBody(resp)
			return restStatusError{status: resp.StatusCode, method: req.Method, url: req.URL.String()}
		}
		return nil
	}
	fn := func() error {
		if brk := c.ruleBreaker(runtimeKey, policy.Breaker); brk != nil {
			if err := brk.Allow(); err != nil {
				return err
			}
			err := call()
			if err != nil {
				brk.MarkFailure()
			} else {
				brk.MarkSuccess()
			}
			return err
		}
		return call()
	}
	err := coreretry.Do(ctx, retryPolicy, fn)
	err = normalizeClientContextError(ctx, err)
	if errors.Is(err, breaker.ErrOpen) {
		return nil, coreerrors.New(coreerrors.CodeUnavailable, err.Error())
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := c.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func (c *Client) Post(ctx context.Context, path string, contentType string, body io.Reader) (*http.Response, error) {
	req, err := c.NewRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.Do(req)
}

func (c *Client) governanceDecision(req governance.Request) governance.Decision {
	if c == nil || c.opts.rules == nil {
		return governance.Decision{}
	}
	return c.opts.rules.Match(req)
}

func (c *Client) prepareRequest(ctx context.Context, req *http.Request, policy governance.Policy, governanceReq governance.Request) (*http.Request, error) {
	attemptReq := req.Clone(ctx)
	if req.Body != nil && req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("reset request body: %w", err)
		}
		attemptReq.Body = body
	}
	for key, value := range policy.Headers {
		attemptReq.Header.Set(key, value)
	}
	for key, value := range policy.Metadata {
		attemptReq.Header.Set(key, value)
	}
	for key, value := range canaryHeaders(policy.Canary, governanceReq) {
		attemptReq.Header.Set(key, value)
	}
	return attemptReq, nil
}

func (c *Client) retryPolicy(policy governance.RetryPolicy) coreretry.Policy {
	retryPolicy := c.opts.retryPolicy
	if retryPolicy.Attempts <= 0 {
		retryPolicy.Attempts = 1
	}
	if policy.Attempts > 0 {
		retryPolicy.Attempts = policy.Attempts
	}
	if policy.Backoff > 0 {
		retryPolicy.Backoff = policy.Backoff
	}
	if retryPolicy.Backoff <= 0 && retryPolicy.BackoffFunc == nil {
		retryPolicy.Backoff = 10 * time.Millisecond
	}
	if retryPolicy.ShouldRetry == nil {
		retryPolicy.ShouldRetry = defaultRESTRetryable
	}
	return retryPolicy
}

func (c *Client) ruleRateLimiter(key string, policy governance.RateLimitPolicy) *limit.Limiter {
	if c == nil || c.runtime == nil || policy.Rate <= 0 && policy.Burst <= 0 {
		return nil
	}
	rate := policy.Rate
	burst := policy.Burst
	if burst <= 0 {
		burst = rate
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	cached := c.runtime.rateLimits[key]
	if cached != nil && cached.rate == rate && cached.burst == burst {
		return cached.limiter
	}
	limiter := limit.New(rate, burst)
	c.runtime.rateLimits[key] = &cachedRateLimiter{rate: rate, burst: burst, limiter: limiter}
	return limiter
}

func (c *Client) ruleConcurrencyLimiter(key string, policy governance.ConcurrencyPolicy) *limit.ConcurrencyLimiter {
	if c == nil || c.runtime == nil || policy.Limit <= 0 {
		return nil
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	cached := c.runtime.concurrency[key]
	if cached != nil && cached.limit == policy.Limit {
		return cached.limiter
	}
	limiter := limit.NewConcurrency(policy.Limit)
	c.runtime.concurrency[key] = &cachedConcurrencyLimiter{limit: policy.Limit, limiter: limiter}
	return limiter
}

func (c *Client) ruleBreaker(key string, policy governance.BreakerPolicy) *breaker.AdaptiveBreaker {
	if c == nil || c.runtime == nil || !policy.Enabled {
		return nil
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	cached := c.runtime.breakers[key]
	if cached != nil && cached.policy == policy {
		return cached.breaker
	}
	brk := adaptiveBreakerFromPolicy(policy)
	c.runtime.breakers[key] = &cachedBreaker{policy: policy, breaker: brk}
	return brk
}

func defaultRESTRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var statusErr restStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status == http.StatusRequestTimeout || statusErr.status == http.StatusTooManyRequests || statusErr.status >= http.StatusInternalServerError
	}
	return coreerrors.Retryable(err)
}

func shouldRetryStatus(status int, method string, policy governance.RetryPolicy) bool {
	if len(policy.Methods) > 0 && !containsMethod(policy.Methods, method) {
		return false
	}
	if len(policy.Statuses) > 0 {
		for _, candidate := range policy.Statuses {
			if candidate == status {
				return true
			}
		}
		return false
	}
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func containsMethod(methods []string, method string) bool {
	method = strings.ToUpper(method)
	for _, candidate := range methods {
		if strings.ToUpper(candidate) == method {
			return true
		}
	}
	return false
}

func requestMayRetry(method string, policy governance.RetryPolicy) bool {
	if len(policy.Methods) > 0 {
		return containsMethod(policy.Methods, method)
	}
	return true
}

func normalizeClientContextError(ctx context.Context, err error) error {
	if ctx == nil || ctx.Err() == nil {
		return err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return coreerrors.New(coreerrors.CodeDeadlineExceeded, ctx.Err().Error())
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return coreerrors.New(coreerrors.CodeCanceled, ctx.Err().Error())
	}
	return err
}

func canaryHeaders(policy governance.CanaryPolicy, req governance.Request) map[string]string {
	decision := governance.SelectCanary(policy, req)
	if !decision.Selected {
		return nil
	}
	headers := map[string]string{governance.HeaderCanary: "true"}
	if decision.Service != "" {
		headers[governance.HeaderCanaryService] = decision.Service
	}
	for key, value := range decision.Headers {
		headers[key] = value
	}
	return headers
}

func closeResponseBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body) // drain before close
		_ = resp.Body.Close()                 // best-effort cleanup
	}
}
