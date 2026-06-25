package rest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/governance"
	coreretry "github.com/imajinyun/gofly/core/retry"
	"github.com/imajinyun/gofly/core/security"
)

func TestRESTClientGovernanceRuleRuntimeRateLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-rate-limit",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/orders",
		Policy:    governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}},
	})
	client := MustNewClient(server.URL, WithClientService("orders"), WithClientGovernanceRuleSet(rules))
	resp, err := client.Get(context.Background(), "/orders")
	if err != nil {
		t.Fatalf("first request error: %v", err)
	}
	closeResponseBody(resp)
	_, err = client.Get(context.Background(), "/orders")
	if coreerrors.CodeOf(err) != coreerrors.CodeResourceExhausted {
		t.Fatalf("second request code = %s, want %s (err=%v)", coreerrors.CodeOf(err), coreerrors.CodeResourceExhausted, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("server calls = %d, want 1", got)
	}
}

func TestRESTClientGovernanceManagerOverridesExplicitRuleSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get("X-Version")))
	}))
	t.Cleanup(server.Close)
	stale := governance.NewRuleSet(governance.Rule{
		Name:      "orders-stale",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "stale"}},
	})
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{
		Name:      "orders-live",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "live"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	client := MustNewClient(server.URL, WithClientService("orders"), WithClientGovernanceRuleSet(stale), WithClientGovernanceManager(manager))
	resp, err := client.Get(context.Background(), "/orders")
	if err != nil {
		t.Fatal(err)
	}
	defer closeResponseBody(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "live" {
		t.Fatalf("response body = %q, want manager rule", body)
	}
}

func TestRESTClientGovernanceManagerOverridesLaterSuite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get("X-Version")))
	}))
	t.Cleanup(server.Close)
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{
		Name:      "orders-live",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "live"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	suite := governance.MustNewSuite(governance.NewPlugin("stale", governance.Rule{
		Name:      "orders-stale",
		Transport: governance.TransportREST,
		Service:   "orders",
		Path:      "/orders",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "stale"}},
	}))
	client := MustNewClient(server.URL, WithClientService("orders"), WithClientGovernanceManager(manager), WithClientGovernanceSuite(suite))
	resp, err := client.Get(context.Background(), "/orders")
	if err != nil {
		t.Fatal(err)
	}
	defer closeResponseBody(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "live" {
		t.Fatalf("response body = %q, want manager rule", body)
	}
}

func TestRESTClientRetryAndStatusBoundaries_BitsUT(t *testing.T) {
	statusErr := restStatusError{status: http.StatusBadGateway, method: http.MethodPost, url: "http://example.test/orders"}
	if got := statusErr.Error(); !strings.Contains(got, "POST") || !strings.Contains(got, "502") || !strings.Contains(got, "http://example.test/orders") {
		t.Fatalf("restStatusError.Error() = %q, want method/url/status", got)
	}
	policy := coreretry.Policy{Attempts: 3, Backoff: time.Second}
	client := MustNewClient("http://example.test", WithClientRetry(policy))
	if client.opts.retryPolicy.Attempts != 3 || client.opts.retryPolicy.Backoff != time.Second {
		t.Fatalf("retry policy = %#v, want explicit policy preserved", client.opts.retryPolicy)
	}

	tests := []struct {
		name   string
		status int
		method string
		policy governance.RetryPolicy
		want   bool
	}{
		{name: "default 408", status: http.StatusRequestTimeout, method: http.MethodGet, want: true},
		{name: "default 429", status: http.StatusTooManyRequests, method: http.MethodGet, want: true},
		{name: "default 503", status: http.StatusServiceUnavailable, method: http.MethodGet, want: true},
		{name: "default 404", status: http.StatusNotFound, method: http.MethodGet, want: false},
		{name: "explicit status match", status: http.StatusTeapot, method: http.MethodPost, policy: governance.RetryPolicy{Statuses: []int{http.StatusTeapot}}, want: true},
		{name: "explicit status miss", status: http.StatusBadGateway, method: http.MethodPost, policy: governance.RetryPolicy{Statuses: []int{http.StatusTeapot}}, want: false},
		{name: "method rejected", status: http.StatusServiceUnavailable, method: http.MethodPost, policy: governance.RetryPolicy{Methods: []string{http.MethodGet}}, want: false},
		{name: "method case insensitive", status: http.StatusServiceUnavailable, method: http.MethodPost, policy: governance.RetryPolicy{Methods: []string{"post"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRetryStatus(tt.status, tt.method, tt.policy); got != tt.want {
				t.Fatalf("shouldRetryStatus(%d, %q, %#v) = %v, want %v", tt.status, tt.method, tt.policy, got, tt.want)
			}
		})
	}
}

func TestRESTClientGovernanceRuleRuntimeConcurrencyLimit(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-concurrency",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/orders",
		Policy:    governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}},
	})
	client := MustNewClient(server.URL, WithClientService("orders"), WithClientGovernanceRuleSet(rules), WithClientTimeout(time.Second))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := client.Get(context.Background(), "/orders")
		if err != nil {
			t.Errorf("first request error: %v", err)
			return
		}
		closeResponseBody(resp)
	}()
	<-entered
	_, err := client.Get(context.Background(), "/orders")
	if coreerrors.CodeOf(err) != coreerrors.CodeUnavailable {
		t.Fatalf("second request code = %s, want %s (err=%v)", coreerrors.CodeOf(err), coreerrors.CodeUnavailable, err)
	}
	close(release)
	wg.Wait()
}

func TestRESTClientGovernanceRuleRuntimeBreaker(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-breaker",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/orders",
		Policy: governance.Policy{Breaker: governance.BreakerPolicy{
			Enabled:      true,
			MinRequests:  1,
			FailureRatio: 0.5,
			OpenTimeout:  time.Second,
		}},
	})
	client := MustNewClient(server.URL, WithClientService("orders"), WithClientGovernanceRuleSet(rules))
	for i := 0; i < 2; i++ {
		if _, err := client.Get(context.Background(), "/orders"); err == nil {
			t.Fatalf("request %d succeeded, want failure", i+1)
		}
	}
	_, err := client.Get(context.Background(), "/orders")
	if coreerrors.CodeOf(err) != coreerrors.CodeUnavailable {
		t.Fatalf("open breaker code = %s, want %s (err=%v)", coreerrors.CodeOf(err), coreerrors.CodeUnavailable, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("server calls = %d, want 2", got)
	}
}

func TestRESTClientGovernanceRuleRuntimeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	t.Cleanup(server.Close)
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-timeout",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/orders",
		Policy:    governance.Policy{Timeout: 20 * time.Millisecond},
	})
	client := MustNewClient(server.URL, WithClientService("orders"), WithClientGovernanceRuleSet(rules), WithClientTimeout(time.Second))
	_, err := client.Get(context.Background(), "/orders")
	if coreerrors.CodeOf(err) != coreerrors.CodeDeadlineExceeded {
		t.Fatalf("timeout code = %s, want %s (err=%v)", coreerrors.CodeOf(err), coreerrors.CodeDeadlineExceeded, err)
	}
}

func TestRESTClientGovernanceRuleRuntimeHeadersAndCanary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Policy"); got != "v1" {
			t.Fatalf("X-Policy = %q, want v1", got)
		}
		if got := r.Header.Get("X-Meta"); got != "m1" {
			t.Fatalf("X-Meta = %q, want m1", got)
		}
		if got := r.Header.Get(governance.HeaderCanary); got != "true" {
			t.Fatalf("canary header = %q, want true", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-headers",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/orders",
		Policy: governance.Policy{
			Headers:  map[string]string{"X-Policy": "v1"},
			Metadata: map[string]string{"X-Meta": "m1"},
			Canary:   governance.CanaryPolicy{Ratio: 1},
		},
	})
	client := MustNewClient(server.URL, WithClientService("orders"), WithClientGovernanceRuleSet(rules))
	resp, err := client.Get(context.Background(), "/orders")
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	closeResponseBody(resp)
}

func TestRESTClientClosesRetryResponseBody(t *testing.T) {
	firstBody := &trackingReadCloser{}
	transport := &sequenceRoundTripper{responses: []*http.Response{
		{StatusCode: http.StatusServiceUnavailable, Body: firstBody},
		{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody)},
	}}
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "retry",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodGet,
		Path:      "/orders",
		Policy:    governance.Policy{Retry: governance.RetryPolicy{Attempts: 2}},
	})
	client := MustNewClient(
		"http://example.test",
		WithClientService("orders"),
		WithClientHTTPClient(&http.Client{Transport: transport}),
		WithClientGovernanceRuleSet(rules),
	)
	resp, err := client.Get(context.Background(), "/orders")
	if err != nil {
		t.Fatal(err)
	}
	closeResponseBody(resp)
	if !firstBody.closed.Load() {
		t.Fatal("retry response body was not closed")
	}
	if got := transport.calls.Load(); got != 2 {
		t.Fatalf("round trip calls = %d, want 2", got)
	}
}

func TestRESTClientRejectsRetryWithNonReplayableBody(t *testing.T) {
	transport := &sequenceRoundTripper{responses: []*http.Response{
		{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(http.NoBody)},
	}}
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "retry-body",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodPost,
		Path:      "/orders",
		Policy:    governance.Policy{Retry: governance.RetryPolicy{Attempts: 2}},
	})
	client := MustNewClient(
		"http://example.test",
		WithClientService("orders"),
		WithClientHTTPClient(&http.Client{Transport: transport}),
		WithClientGovernanceRuleSet(rules),
	)
	_, err := client.Post(context.Background(), "/orders", "text/plain", oneShotReader{})
	if err == nil || !strings.Contains(err.Error(), "not replayable") {
		t.Fatalf("Post error = %v, want non-replayable retry error", err)
	}
	if got := transport.calls.Load(); got != 0 {
		t.Fatalf("round trip calls = %d, want fail before sending request", got)
	}
}

func TestRESTClientAllowsNonReplayableBodyWhenRetryDoesNotApplyToMethod(t *testing.T) {
	transport := &sequenceRoundTripper{responses: []*http.Response{
		{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody)},
	}}
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "retry-get-only",
		Transport: governance.TransportREST,
		Service:   "orders",
		Method:    http.MethodPost,
		Path:      "/orders",
		Policy: governance.Policy{Retry: governance.RetryPolicy{
			Attempts: 2,
			Methods:  []string{http.MethodGet},
		}},
	})
	client := MustNewClient(
		"http://example.test",
		WithClientService("orders"),
		WithClientHTTPClient(&http.Client{Transport: transport}),
		WithClientGovernanceRuleSet(rules),
	)
	resp, err := client.Post(context.Background(), "/orders", "text/plain", oneShotReader{})
	if err != nil {
		t.Fatalf("Post error = %v, want request allowed because retry excludes POST", err)
	}
	closeResponseBody(resp)
	if got := transport.calls.Load(); got != 1 {
		t.Fatalf("round trip calls = %d, want one request", got)
	}
}

func TestRESTClientNormalizeContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := normalizeClientContextError(ctx, errors.New("transport canceled"))
	if coreerrors.CodeOf(err) != coreerrors.CodeCanceled {
		t.Fatalf("code = %s, want %s", coreerrors.CodeOf(err), coreerrors.CodeCanceled)
	}
}

func TestRESTClientConstructionAndRequestBoundaries_BitsUT(t *testing.T) {
	if _, err := NewClient(""); err == nil || !strings.Contains(err.Error(), "base url is required") {
		t.Fatalf("NewClient blank base error = %v, want base url required", err)
	}
	if _, err := NewClient("http://example.test", nil, WithClientTimeout(0)); err != nil {
		t.Fatalf("NewClient with nil option returned error: %v", err)
	}
	if _, err := NewClient("http://example.test", WithClientTLS(security.TLSConfig{CAFile: "/definitely/missing/ca.pem"})); err == nil || !strings.Contains(err.Error(), "configure rest tls") {
		t.Fatalf("NewClient TLS error = %v, want configure rest tls", err)
	}

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("MustNewClient blank base did not panic")
			}
		}()
		_ = MustNewClient("")
	}()

	var nilClient *Client
	var nilCtx context.Context
	if _, err := nilClient.NewRequest(nilCtx, http.MethodGet, "/orders", nil); err == nil || !strings.Contains(err.Error(), "rest client is nil") {
		t.Fatalf("nil client NewRequest error = %v, want nil client", err)
	}
	if _, err := nilClient.Do(httptest.NewRequest(http.MethodGet, "/", nil)); err == nil || !strings.Contains(err.Error(), "rest client is nil") {
		t.Fatalf("nil client Do error = %v, want nil client", err)
	}
	client := MustNewClient("http://api.example.test/base/")
	if _, err := client.Do(nil); err == nil || !strings.Contains(err.Error(), "rest request is nil") {
		t.Fatalf("nil request Do error = %v, want nil request", err)
	}
	req, err := client.NewRequest(nilCtx, http.MethodGet, "/orders", nil)
	if err != nil {
		t.Fatalf("NewRequest relative path returned error: %v", err)
	}
	if got := req.URL.String(); got != "http://api.example.test/base/orders" {
		t.Fatalf("relative NewRequest URL = %q, want base joined path", got)
	}
	absReq, err := client.NewRequest(context.Background(), http.MethodGet, "https://other.example.test/ping", nil)
	if err != nil {
		t.Fatalf("NewRequest absolute path returned error: %v", err)
	}
	if got := absReq.URL.String(); got != "https://other.example.test/ping" {
		t.Fatalf("absolute NewRequest URL = %q, want unchanged absolute URL", got)
	}
	if _, err := client.NewRequest(context.Background(), http.MethodGet, "http://%zz", nil); err == nil || !strings.Contains(err.Error(), "create rest request") {
		t.Fatalf("NewRequest bad URL error = %v, want create rest request", err)
	}
	if _, err := client.Get(context.Background(), "http://%zz"); err == nil || !strings.Contains(err.Error(), "create rest request") {
		t.Fatalf("Get bad URL error = %v, want create rest request", err)
	}
}

func TestRESTClientPostSetsContentType_BitsUT(t *testing.T) {
	transport := &captureHeaderRoundTripper{}
	client := MustNewClient("http://example.test", WithClientHTTPClient(&http.Client{Transport: transport}))
	resp, err := client.Post(context.Background(), "/orders", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post returned error: %v", err)
	}
	closeResponseBody(resp)
	if got := transport.contentType.Load(); got != "application/json" {
		t.Fatalf("captured content type = %q, want application/json", got)
	}
}

func TestRESTClientPrepareRequestCanaryAndRetryHelpers_BitsUT(t *testing.T) {
	client := MustNewClient("http://example.test")
	req, err := http.NewRequest(http.MethodPost, "http://example.test/orders", strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Original", "keep")
	req.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("reset boom") }
	_, err = client.prepareRequest(context.Background(), req, governance.Policy{}, governance.Request{})
	if err == nil || !strings.Contains(err.Error(), "reset request body") {
		t.Fatalf("prepareRequest GetBody error = %v, want reset request body", err)
	}

	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("replayed")), nil }
	policy := governance.Policy{
		Headers:  map[string]string{"X-Policy": "v1"},
		Metadata: map[string]string{"X-Meta": "m1"},
		Canary: governance.CanaryPolicy{
			Ratio:   1,
			Service: "orders-canary",
			Headers: map[string]string{"X-Canary-Extra": "yes"},
		},
	}
	attemptReq, err := client.prepareRequest(context.Background(), req, policy, governance.Request{Transport: governance.TransportREST, Service: "orders", Method: http.MethodPost, Path: "/orders"})
	if err != nil {
		t.Fatalf("prepareRequest returned error: %v", err)
	}
	if attemptReq == req {
		t.Fatal("prepareRequest returned original request, want cloned request")
	}
	for key, want := range map[string]string{
		"X-Original":                   "keep",
		"X-Policy":                     "v1",
		"X-Meta":                       "m1",
		governance.HeaderCanary:        "true",
		governance.HeaderCanaryService: "orders-canary",
		"X-Canary-Extra":               "yes",
	} {
		if got := attemptReq.Header.Get(key); got != want {
			t.Fatalf("prepared header %s = %q, want %q", key, got, want)
		}
	}
	data, err := io.ReadAll(attemptReq.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "replayed" {
		t.Fatalf("prepared body = %q, want replayed", data)
	}

	if got := canaryHeaders(governance.CanaryPolicy{}, governance.Request{}); got != nil {
		t.Fatalf("canaryHeaders without match = %#v, want nil", got)
	}
	if !requestMayRetry(http.MethodPut, governance.RetryPolicy{}) || requestMayRetry(http.MethodPut, governance.RetryPolicy{Methods: []string{http.MethodGet}}) {
		t.Fatal("requestMayRetry method filtering returned unexpected result")
	}
	if defaultRESTRetryable(nil) || defaultRESTRetryable(context.Canceled) || defaultRESTRetryable(context.DeadlineExceeded) {
		t.Fatal("defaultRESTRetryable should reject nil/canceled/deadline errors")
	}
	var nilCtx context.Context
	if normalizeClientContextError(nilCtx, errors.New("plain")).Error() != "plain" {
		t.Fatal("normalizeClientContextError nil ctx should preserve original error")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if err := normalizeClientContextError(ctx, errors.New("deadline")); coreerrors.CodeOf(err) != coreerrors.CodeDeadlineExceeded {
		t.Fatalf("deadline normalize code = %s, want %s", coreerrors.CodeOf(err), coreerrors.CodeDeadlineExceeded)
	}
}

type trackingReadCloser struct {
	closed atomic.Bool
}

func (b *trackingReadCloser) Read([]byte) (int, error) { return 0, io.EOF }

func (b *trackingReadCloser) Close() error {
	b.closed.Store(true)
	return nil
}

type oneShotReader struct{}

func (oneShotReader) Read([]byte) (int, error) { return 0, io.EOF }

type sequenceRoundTripper struct {
	calls     atomic.Int32
	responses []*http.Response
}

func (t *sequenceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	call := int(t.calls.Add(1)) - 1
	if call >= len(t.responses) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody), Request: req}, nil
	}
	resp := t.responses[call]
	resp.Request = req
	return resp, nil
}

type captureHeaderRoundTripper struct {
	contentType atomic.Value
}

func (t *captureHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	t.contentType.Store(req.Header.Get("Content-Type"))
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(http.NoBody), Request: req}, nil
}
