package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- RFC 6455 requires SHA-1 for Sec-WebSocket-Accept in tests.
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/discovery"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/core/observability/trace"
	controladmin "github.com/imajinyun/gofly/ops/admin"
	"github.com/imajinyun/gofly/rest"
	"github.com/imajinyun/gofly/rpc"
)

type failAfterWriter struct {
	failOn int
	writes int
	err    error
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failOn {
		return 0, w.err
	}
	return len(p), nil
}

type gatewayRoundTripFunc func(*http.Request) (*http.Response, error)

func (f gatewayRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type errorReadCloser struct{ err error }

func (r errorReadCloser) Read([]byte) (int, error) { return 0, r.err }

func (r errorReadCloser) Close() error { return nil }

type fakeBalancer struct{}

func (fakeBalancer) Pick(context.Context, []string) (string, error) { return "picked", nil }

type fakeDiscoveryResolver struct{}

func (fakeDiscoveryResolver) Resolve(context.Context, string, ...discovery.ResolveOption) ([]discovery.Instance, error) {
	return []discovery.Instance{{Endpoint: "http://127.0.0.1:1", Status: discovery.StatusHealthy}}, nil
}

func (fakeDiscoveryResolver) Watch(context.Context, string, ...discovery.ResolveOption) (<-chan discovery.Event, error) {
	return make(chan discovery.Event), nil
}

func TestGatewayOptionBoundaryBranches(t *testing.T) {
	routes := []Route{{PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}}}
	g, err := New(routes,
		WithBalancer(nil),
		WithResolvers(nil),
		WithResolvers(map[string]rpc.Resolver{"": rpc.ResolverFunc(func(context.Context) ([]string, error) { return nil, nil }), "nil": nil}),
		WithDiscoveryResolvers(nil),
		WithDiscoveryResolvers(map[string]discovery.Resolver{"": fakeDiscoveryResolver{}, "nil": nil}),
		WithShadowPool(0, -1),
	)
	if err != nil {
		t.Fatalf("New gateway with empty options returned error: %v", err)
	}
	firstGateway := g
	t.Cleanup(func() { _ = firstGateway.Close() })
	if g.balancer == nil {
		t.Fatal("gateway balancer is nil after default initialization")
	}
	if len(g.resolvers) != 0 {
		t.Fatalf("invalid resolvers were registered: %#v", g.resolvers)
	}
	if g.shadowPool == nil {
		t.Fatal("WithShadowPool default branch did not allocate shadow pool")
	}

	resolver := rpc.ResolverFunc(func(context.Context) ([]string, error) { return []string{"http://127.0.0.1:2"}, nil })
	g, err = New(routes,
		WithBalancer(fakeBalancer{}),
		WithResolvers(map[string]rpc.Resolver{"orders": resolver}),
		WithDiscoveryResolvers(map[string]discovery.Resolver{"catalog": fakeDiscoveryResolver{}}),
	)
	if err != nil {
		t.Fatalf("New gateway with resolvers returned error: %v", err)
	}
	secondGateway := g
	t.Cleanup(func() { _ = secondGateway.Close() })
	if _, ok := g.balancer.(fakeBalancer); !ok {
		t.Fatalf("gateway balancer = %T, want fakeBalancer", g.balancer)
	}
	if len(g.resolvers) != 2 || g.resolvers["orders"] == nil || g.resolvers["catalog"] == nil {
		t.Fatalf("registered resolvers = %#v, want orders and catalog", g.resolvers)
	}
	endpoints, err := g.resolvers["catalog"].Resolve(context.Background())
	if err != nil {
		t.Fatalf("catalog discovery resolver returned error: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0] != "http://127.0.0.1:1" {
		t.Fatalf("catalog endpoints = %#v, want discovery endpoint", endpoints)
	}
}

func TestGatewayReverseProxyRewritesPathAndHeaders(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotService string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotService = r.Header.Get(HeaderGatewayService)
		if r.Header.Get(HeaderForwardedHost) == "" {
			t.Fatalf("missing %s header", HeaderForwardedHost)
		}
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	g, err := New([]Route{{
		Name:           "users",
		Method:         http.MethodGet,
		PathPrefix:     "/api",
		UpstreamPrefix: "/v1",
		Service:        "user",
		Targets:        []string{upstream.URL},
		Headers:        map[string]string{"X-Gateway-Test": "true"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/users?id=1", nil)
	req.Host = "gateway.local"
	g.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if gotPath != "/v1/users" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotQuery != "id=1" {
		t.Fatalf("upstream query = %q", gotQuery)
	}
	if gotService != "user" {
		t.Fatalf("gateway service header = %q", gotService)
	}
	snapshot := g.Snapshot()
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].Requests != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestGatewayReverseProxyStreamsServerSentEvents(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "data: ready\n\n"); err != nil {
			t.Errorf("write sse event: %v", err)
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-release
	}))

	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/events", Targets: []string{upstream.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(g)
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		server.Close()
		upstream.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("sse response status=%d content-type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lineCh := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		line, _ := reader.ReadString('\n')
		lineCh <- line
	}()
	select {
	case line := <-lineCh:
		if line != "data: ready\n" {
			t.Fatalf("first sse line = %q, want data: ready", line)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for streamed sse line before upstream close")
	}
	releaseOnce.Do(func() { close(release) })
}

func TestGatewayReverseProxyTunnelsWebSocket(t *testing.T) {
	var gotService string
	var gotForwardedHost string
	var gotPolicyHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotService = r.Header.Get(HeaderGatewayService)
		gotForwardedHost = r.Header.Get(HeaderForwardedHost)
		gotPolicyHeader = r.Header.Get("X-Gateway-Policy")
		ctx := &rest.Context{Response: w, Request: r}
		_ = ctx.WebSocket(func(_ context.Context, conn *rest.WebSocketConn) {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("upstream read websocket: %v", err)
				return
			}
			if err := conn.WriteMessage(messageType, append([]byte("echo:"), payload...)); err != nil {
				t.Errorf("upstream write websocket: %v", err)
			}
		})
	}))
	t.Cleanup(upstream.Close)

	g, err := New([]Route{{
		Method:     http.MethodGet,
		PathPrefix: "/ws",
		Service:    "chat",
		Targets:    []string{upstream.URL},
		Header:     HeaderPolicy{SetRequest: map[string]string{"X-Gateway-Policy": "on"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)

	conn, rw := dialGatewayWebSocket(t, server.URL, "/ws")
	defer conn.Close()
	writeGatewayClientFrame(t, rw, 1, []byte("hello"))
	messageType, payload := readGatewayServerFrame(t, rw)
	if messageType != 1 || string(payload) != "echo:hello" {
		t.Fatalf("websocket frame type=%d payload=%q, want echo", messageType, payload)
	}
	if gotService != "chat" || gotForwardedHost == "" || gotPolicyHeader != "on" {
		t.Fatalf("upstream websocket headers service=%q forwarded-host=%q policy=%q", gotService, gotForwardedHost, gotPolicyHeader)
	}
}

func TestGatewayAggregatesBFFRoute(t *testing.T) {
	var gotProfileHeader string
	var gotOrdersHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/profile":
			gotProfileHeader = r.Header.Get(HeaderGatewayService)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"u1"}`)
		case "/orders":
			gotOrdersHeader = r.Header.Get("X-Step")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":"o1"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	g, err := New([]Route{{
		Name:       "bff",
		Method:     http.MethodGet,
		PathPrefix: "/bff",
		Service:    "bff",
		Targets:    []string{upstream.URL},
		Aggregation: AggregationConfig{
			Enabled: true,
			Steps: []AggregationStep{
				{Name: "profile", Path: "/profile", Required: true},
				{Name: "orders", Path: "/orders", Headers: map[string]string{"X-Step": "orders"}},
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/bff/home", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	if !strings.HasPrefix(rr.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content-type = %q, want json", rr.Header().Get("Content-Type"))
	}
	var envelope struct {
		Data struct {
			Profile json.RawMessage `json:"profile"`
			Orders  json.RawMessage `json:"orders"`
		} `json:"data"`
		Errors map[string]string `json:"errors,omitempty"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode aggregation envelope: %v\n%s", err, rr.Body.String())
	}
	if string(envelope.Data.Profile) != `{"id":"u1"}` || string(envelope.Data.Orders) != `[{"id":"o1"}]` {
		t.Fatalf("aggregation data profile=%s orders=%s", envelope.Data.Profile, envelope.Data.Orders)
	}
	if len(envelope.Errors) != 0 {
		t.Fatalf("aggregation errors = %#v, want none", envelope.Errors)
	}
	if gotProfileHeader != "bff" || gotOrdersHeader != "orders" {
		t.Fatalf("aggregation headers service=%q step=%q", gotProfileHeader, gotOrdersHeader)
	}

	routes := g.RouteConfigs()
	if len(routes) != 1 || !routes[0].Aggregation.Enabled || len(routes[0].Aggregation.Steps) != 2 {
		t.Fatalf("route configs aggregation = %#v", routes)
	}
	runtime := g.RuntimeSnapshot()
	if len(runtime.Routes) != 1 || !runtime.Routes[0].Aggregation.Enabled {
		t.Fatalf("runtime aggregation = %#v", runtime.Routes)
	}
}

func TestGatewayAggregationRequiredStepFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/required":
			http.Error(w, "required down", http.StatusServiceUnavailable)
		case "/optional":
			http.Error(w, "optional down", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	g, err := New([]Route{{
		Method:     http.MethodGet,
		PathPrefix: "/bff",
		Targets:    []string{upstream.URL},
		Aggregation: AggregationConfig{
			Enabled: true,
			Steps: []AggregationStep{
				{Name: "ok", Path: "/ok"},
				{Name: "required", Path: "/required", Required: true},
				{Name: "optional", Path: "/optional"},
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/bff/home", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors map[string]string          `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode aggregation envelope: %v\n%s", err, rr.Body.String())
	}
	if string(envelope.Data["ok"]) != `{"ok":true}` {
		t.Fatalf("ok data = %s", envelope.Data["ok"])
	}
	if !strings.Contains(envelope.Errors["required"], "503") || !strings.Contains(envelope.Errors["optional"], "503") {
		t.Fatalf("aggregation errors = %#v, want required and optional 503", envelope.Errors)
	}
}

func TestGatewayAggregationUsesStepFallbacksForPartialResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/profile":
			http.Error(w, "profile down", http.StatusServiceUnavailable)
		case "/orders":
			http.Error(w, "orders down", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	g, err := New([]Route{{
		Method:     http.MethodGet,
		PathPrefix: "/bff",
		Targets:    []string{upstream.URL},
		Aggregation: AggregationConfig{
			Enabled: true,
			Steps: []AggregationStep{
				{Name: "profile", Path: "/profile", Required: true, Fallback: json.RawMessage(`{"id":"anonymous"}`)},
				{Name: "orders", Path: "/orders", Fallback: json.RawMessage(`[]`)},
			},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/bff/home", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want fallback partial success", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors map[string]string          `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode aggregation fallback envelope: %v\n%s", err, rr.Body.String())
	}
	if string(envelope.Data["profile"]) != `{"id":"anonymous"}` || string(envelope.Data["orders"]) != `[]` {
		t.Fatalf("fallback data = %#v", envelope.Data)
	}
	if !strings.Contains(envelope.Errors["profile"], "503") || !strings.Contains(envelope.Errors["orders"], "502") {
		t.Fatalf("fallback errors = %#v, want retained upstream failures", envelope.Errors)
	}

	routes := g.RouteConfigs()
	if string(routes[0].Aggregation.Steps[0].Fallback) != `{"id":"anonymous"}` || string(routes[0].Aggregation.Steps[1].Fallback) != `[]` {
		t.Fatalf("route config fallback = %#v", routes[0].Aggregation.Steps)
	}
	routes[0].Aggregation.Steps[0].Fallback[1] = 'x'
	runtime := g.RuntimeSnapshot()
	if string(runtime.Routes[0].Aggregation.Steps[0].Fallback) != `{"id":"anonymous"}` {
		t.Fatalf("runtime fallback mutated through route config alias: %s", runtime.Routes[0].Aggregation.Steps[0].Fallback)
	}
}

func TestGatewayGovernanceManagerOverridesExplicitRuleSet(t *testing.T) {
	stale := governance.NewRuleSet(governance.Rule{Name: "stale", Transport: governance.TransportGateway, Path: "/api/*"})
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{
		Name:      "live",
		Transport: governance.TransportGateway,
		Path:      "/api/*",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "live"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}}}, WithGovernanceRuleSet(stale), WithGovernanceManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	route := gw.governedRoute(httptest.NewRequest(http.MethodGet, "/api/orders", nil), gw.routes[0])
	if got := route.Headers["X-Version"]; got != "live" {
		t.Fatalf("governed route header = %q, want manager rule", got)
	}
}

func TestGatewayGovernanceSuiteProvidesRules(t *testing.T) {
	suite := governance.MustNewSuite(governance.NewPlugin("gateway-default", governance.Rule{
		Name:      "suite",
		Transport: governance.TransportGateway,
		Path:      "/api/*",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "suite"}},
	}))
	gw, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}}}, WithGovernanceSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	route := gw.governedRoute(httptest.NewRequest(http.MethodGet, "/api/orders", nil), gw.routes[0])
	if got := route.Headers["X-Version"]; got != "suite" {
		t.Fatalf("governed route header = %q, want suite rule", got)
	}
}

func TestApplyGovernancePolicyBoundaries(t *testing.T) {
	route := Route{
		Name:      "orders",
		Headers:   map[string]string{"X-Original": "true"},
		Timeout:   time.Second,
		Canary:    []CanaryRoute{{Target: "http://stable"}},
		RateLimit: RateLimitConfig{Rate: 1, Burst: 1},
	}
	policy := governance.Policy{
		Timeout:      2 * time.Second,
		MaxBodyBytes: 1024,
		Retry:        governance.RetryPolicy{Attempts: 3, Backoff: time.Millisecond, Statuses: []int{http.StatusBadGateway}, Methods: []string{http.MethodPost}},
		Breaker:      governance.BreakerPolicy{Enabled: true, OpenTimeout: time.Second, Window: time.Minute, Buckets: 4, MinRequests: 5, FailureRatio: 0.5},
		RateLimit:    governance.RateLimitPolicy{Rate: 9, Burst: 2},
		Concurrency:  governance.ConcurrencyPolicy{Limit: 7},
		Headers:      map[string]string{"X-Policy": "on"},
		Canary:       governance.CanaryPolicy{Target: "http://canary", Ratio: 0.25, Headers: map[string]string{"X-Canary": "true"}, MatchHeaders: map[string]string{"X-Bucket": "beta"}},
	}

	governed := applyGovernancePolicy(route, policy)
	if governed.Timeout != 2*time.Second || governed.MaxBodyBytes != 1024 || governed.Retry.Attempts != 3 || governed.Retry.Statuses[0] != http.StatusBadGateway {
		t.Fatalf("governed retry/timeout = %#v, want policy applied", governed)
	}
	if !governed.Breaker.Enabled || governed.RateLimit.Rate != 9 || governed.Concurrency.Limit != 7 {
		t.Fatalf("governed limits = %#v, want breaker/rate/concurrency applied", governed)
	}
	if governed.Headers["X-Original"] != "true" || governed.Headers["X-Policy"] != "on" {
		t.Fatalf("governed headers = %#v, want original and policy headers", governed.Headers)
	}
	governed.Headers["X-Original"] = "mutated"
	if route.Headers["X-Original"] != "true" {
		t.Fatalf("source headers mutated to %#v, want defensive copy", route.Headers)
	}
	if len(governed.Canary) != 2 || governed.Canary[1].Target != "http://canary" || governed.Canary[1].Headers["X-Canary"] != "true" || governed.Canary[1].MatchHeaders["X-Bucket"] != "beta" {
		t.Fatalf("governed canary = %#v, want existing plus policy canary", governed.Canary)
	}
	policy.Canary.Headers["X-Canary"] = "mutated"
	if governed.Canary[1].Headers["X-Canary"] != "true" {
		t.Fatalf("canary headers mutated through source policy: %#v", governed.Canary[1].Headers)
	}
}

func TestGatewaySnapshotAndPrometheusBoundaries(t *testing.T) {
	if err := (*Stats)(nil).WritePrometheus(&bytes.Buffer{}); err != nil {
		t.Fatalf("nil Stats WritePrometheus error = %v, want nil", err)
	}
	stats := NewStats()
	stats.Observe("GET /route\n\"quoted\"\\slash", http.StatusServiceUnavailable, 3*time.Millisecond)
	stats.Observe("GET /route\n\"quoted\"\\slash", http.StatusOK, time.Millisecond)
	stats.IncRetry("GET /route\n\"quoted\"\\slash", 1)
	stats.IncShadow("GET /route\n\"quoted\"\\slash")
	stats.IncShadowDropped("GET /route\n\"quoted\"\\slash")
	stats.IncEjection("GET /route\n\"quoted\"\\slash")

	var buf bytes.Buffer
	if err := stats.WritePrometheus(&buf); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `gofly_gateway_route_requests_total{route="GET /route\n\"quoted\"\\slash"} 2`) {
		t.Fatalf("escaped request metric missing:\n%s", out)
	}
	idx200 := strings.Index(out, `status="200"`)
	idx503 := strings.Index(out, `status="503"`)
	if idx200 < 0 || idx503 < 0 || idx200 > idx503 {
		t.Fatalf("status metrics ordering invalid: idx200=%d idx503=%d\n%s", idx200, idx503, out)
	}
	if routeLabel(RouteSnapshot{Name: "named", Method: http.MethodGet, PathPrefix: "/ignored"}) != "named" {
		t.Fatal("routeLabel with name did not prefer name")
	}
	if routeLabel(RouteSnapshot{Method: http.MethodPost, PathPrefix: "/orders"}) != "POST /orders" {
		t.Fatal("routeLabel with method/path did not include method")
	}
	if routeLabel(RouteSnapshot{PathPrefix: "/orders"}) != "/orders" {
		t.Fatal("routeLabel path fallback mismatch")
	}
	if got := prometheusLabel("line\n\"quoted\"\\slash"); got != `line\n\"quoted\"\\slash` {
		t.Fatalf("prometheusLabel = %q, want escaped label", got)
	}
	if got := ((*Gateway)(nil)).snapshotResolveTimeout(); got != 500*time.Millisecond {
		t.Fatalf("nil snapshot timeout = %v, want 500ms", got)
	}
	if got := (&Gateway{timeout: 100 * time.Millisecond}).snapshotResolveTimeout(); got != 100*time.Millisecond {
		t.Fatalf("short snapshot timeout = %v, want 100ms", got)
	}
	if got := (&Gateway{timeout: 2 * time.Second}).snapshotResolveTimeout(); got != 500*time.Millisecond {
		t.Fatalf("long snapshot timeout = %v, want capped 500ms", got)
	}
}

func TestGatewayWritePrometheusErrorBoundaries(t *testing.T) {
	stats := NewStats()
	stats.Observe("GET /alpha", http.StatusOK, time.Millisecond)
	stats.Observe("POST /zeta", http.StatusBadGateway, 3*time.Millisecond)
	stats.IncRetry("POST /zeta", 2)
	stats.IncShadow("POST /zeta")
	stats.IncShadowDropped("POST /zeta")
	stats.IncEjection("POST /zeta")

	var ok bytes.Buffer
	if err := stats.WritePrometheus(&ok); err != nil {
		t.Fatalf("WritePrometheus: %v", err)
	}
	out := ok.String()
	for _, needle := range []string{
		`gofly_gateway_route_requests_total{route="GET /alpha"} 1`,
		`gofly_gateway_route_requests_total{route="POST /zeta"} 1`,
		`gofly_gateway_route_errors_total{route="POST /zeta"} 1`,
		`gofly_gateway_route_retries_total{route="POST /zeta"} 2`,
		`gofly_gateway_route_shadow_total{route="POST /zeta"} 1`,
		`gofly_gateway_route_shadow_dropped_total{route="POST /zeta"} 1`,
		`gofly_gateway_route_ejections_total{route="POST /zeta"} 1`,
		`gofly_gateway_route_status_total{route="POST /zeta",status="502"} 1`,
		`gofly_gateway_route_duration_seconds_count{route="POST /zeta"} 1`,
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("prometheus output missing %q:\n%s", needle, out)
		}
	}
	if strings.Index(out, `route="GET /alpha"`) > strings.Index(out, `route="POST /zeta"`) {
		t.Fatalf("route metrics are not sorted:\n%s", out)
	}

	wantErr := errors.New("gateway prometheus write failed")
	for failOn := 1; failOn <= 30; failOn++ {
		t.Run(strconv.Itoa(failOn), func(t *testing.T) {
			writer := &failAfterWriter{failOn: failOn, err: wantErr}
			if err := stats.WritePrometheus(writer); !errors.Is(err, wantErr) {
				t.Fatalf("WritePrometheus failOn=%d error = %v, want write error", failOn, err)
			}
		})
	}
}

func TestGatewayGovernanceManagerOverridesLaterSuite(t *testing.T) {
	manager, err := governance.NewManager(governance.Config{Rules: []governance.Rule{{
		Name:      "live",
		Transport: governance.TransportGateway,
		Path:      "/api/*",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "live"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	suite := governance.MustNewSuite(governance.NewPlugin("stale", governance.Rule{
		Name:      "stale",
		Transport: governance.TransportGateway,
		Path:      "/api/*",
		Policy:    governance.Policy{Headers: map[string]string{"X-Version": "stale"}},
	}))
	gw, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}}}, WithGovernanceManager(manager), WithGovernanceSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	route := gw.governedRoute(httptest.NewRequest(http.MethodGet, "/api/orders", nil), gw.routes[0])
	if got := route.Headers["X-Version"]; got != "live" {
		t.Fatalf("governed route header = %q, want manager rule", got)
	}
}

func TestGatewayRecordsCoreMetricsRegistry(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	registry := metrics.NewRegistry()
	g, err := New([]Route{{Name: "users", Method: http.MethodGet, PathPrefix: "/api", Targets: []string{upstream.URL}}}, WithMetricsRegistry(registry))
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/users", nil))

	snapshot := registry.Snapshot()
	if snapshot.Requests != 1 || snapshot.InFlight != 0 {
		t.Fatalf("metrics snapshot = %+v, want one completed gateway request", snapshot)
	}
	if _, ok := snapshot.Routes["gateway:users"]; !ok {
		t.Fatalf("routes = %+v, want gateway route metric", snapshot.Routes)
	}
}

func TestGatewayUsesRegistryResolverAndBalancer(t *testing.T) {
	var firstHits atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits.Add(1)
		_, _ = fmt.Fprint(w, "first")
	}))
	t.Cleanup(first.Close)
	var secondHits atomic.Int64
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		_, _ = fmt.Fprint(w, "second")
	}))
	t.Cleanup(second.Close)

	registry := rpc.NewRegistry()
	if err := registry.RegisterService(context.Background(), "users", first.URL); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterService(context.Background(), "users", second.URL); err != nil {
		t.Fatal(err)
	}
	g, err := New([]Route{{PathPrefix: "/api", Service: "users", Resolver: registry.Resolver("users")}})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		rr := httptest.NewRecorder()
		g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, body = %s", i, rr.Code, rr.Body.String())
		}
	}
	if firstHits.Load() == 0 || secondHits.Load() == 0 {
		t.Fatalf("first hits = %d, second hits = %d", firstHits.Load(), secondHits.Load())
	}
}

func TestGatewayFiltersInstancesByTags(t *testing.T) {
	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("wrong tagged upstream was selected")
	}))
	t.Cleanup(wrong.Close)
	right := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "right")
	}))
	t.Cleanup(right.Close)

	registry := rpc.NewRegistry()
	if err := registry.RegisterInstance(context.Background(), "users", rpc.ServiceInstance{Endpoint: wrong.URL, Tags: map[string]string{"zone": "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterInstance(context.Background(), "users", rpc.ServiceInstance{Endpoint: right.URL, Weight: 2, Tags: map[string]string{"zone": "a"}}); err != nil {
		t.Fatal(err)
	}
	g, err := New([]Route{{PathPrefix: "/api", Service: "users", Resolver: registry.Resolver("users"), Tags: map[string]string{"zone": "a"}}})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "right" {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestGatewayUsesDiscoveryResolverByService(t *testing.T) {
	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("wrong discovery upstream was selected")
	}))
	t.Cleanup(wrong.Close)
	right := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "discovery")
	}))
	t.Cleanup(right.Close)

	registry := discovery.NewMemoryRegistry()
	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: wrong.URL, Zone: "b", Tags: map[string]string{"zone": "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: right.URL, Weight: 2, Version: "v1", Zone: "a", Tags: map[string]string{"zone": "a"}}); err != nil {
		t.Fatal(err)
	}
	g, err := New([]Route{{PathPrefix: "/api", Service: "users", Tags: map[string]string{"zone": "a"}}}, WithDiscoveryResolvers(map[string]discovery.Resolver{"users": registry}))
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "discovery" {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	snapshot := g.Snapshot()
	if len(snapshot.Discovery) != 1 || snapshot.Discovery[0].Service != "users" || len(snapshot.Discovery[0].Instances) != 1 || snapshot.Discovery[0].Instances[0].Endpoint != right.URL {
		t.Fatalf("discovery snapshot = %+v, want filtered users instance", snapshot.Discovery)
	}
	if len(snapshot.Discovery[0].Endpoints) != 2 {
		t.Fatalf("weighted endpoints = %v, want weight-expanded endpoints", snapshot.Discovery[0].Endpoints)
	}
}

func TestGatewayDiscoveryFailoverKeepsLastKnownGoodEndpoint(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options func(*discovery.MemoryRegistry) []Option
	}{
		{
			name: "failover before discovery resolvers",
			options: func(registry *discovery.MemoryRegistry) []Option {
				return []Option{
					WithDiscoveryFailover(),
					WithDiscoveryResolvers(map[string]discovery.Resolver{"users": registry}),
				}
			},
		},
		{
			name: "failover after discovery resolvers",
			options: func(registry *discovery.MemoryRegistry) []Option {
				return []Option{
					WithDiscoveryResolvers(map[string]discovery.Resolver{"users": registry}),
					WithDiscoveryFailover(),
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = fmt.Fprint(w, "stable")
			}))
			t.Cleanup(upstream.Close)

			registry := discovery.NewMemoryRegistry()
			lease, err := registry.Register(context.Background(), discovery.Instance{
				Service:  "users",
				Endpoint: upstream.URL,
				Weight:   2,
				Tags:     map[string]string{"zone": "a"},
			})
			if err != nil {
				t.Fatal(err)
			}
			g, err := New([]Route{{PathPrefix: "/api", Service: "users", Tags: map[string]string{"zone": "a"}}}, tt.options(registry)...)
			if err != nil {
				t.Fatal(err)
			}

			first := httptest.NewRecorder()
			g.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
			if first.Code != http.StatusOK || strings.TrimSpace(first.Body.String()) != "stable" {
				t.Fatalf("first response = %d/%q, want stable", first.Code, first.Body.String())
			}
			if err := lease.Close(context.Background()); err != nil {
				t.Fatal(err)
			}

			second := httptest.NewRecorder()
			g.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
			if second.Code != http.StatusOK || strings.TrimSpace(second.Body.String()) != "stable" {
				t.Fatalf("stale response = %d/%q, want last known endpoint", second.Code, second.Body.String())
			}
			if hits.Load() != 2 {
				t.Fatalf("upstream hits = %d, want initial and stale fallback requests", hits.Load())
			}
			snapshot := g.Snapshot()
			if len(snapshot.Discovery) != 1 {
				t.Fatalf("discovery snapshot = %+v, want one route snapshot", snapshot.Discovery)
			}
			routeSnapshot := snapshot.Discovery[0]
			if !routeSnapshot.Stale || routeSnapshot.Fallbacks == 0 || routeSnapshot.Error == "" {
				t.Fatalf("discovery failover snapshot = %+v, want stale fallback evidence", routeSnapshot)
			}
			if len(routeSnapshot.Instances) != 1 || routeSnapshot.Instances[0].Endpoint != upstream.URL || routeSnapshot.Instances[0].Tags["zone"] != "a" {
				t.Fatalf("stale instances = %+v, want last known tagged instance", routeSnapshot.Instances)
			}
			if len(routeSnapshot.Endpoints) != 2 {
				t.Fatalf("weighted stale endpoints = %+v, want weight-expanded last known endpoints", routeSnapshot.Endpoints)
			}
		})
	}
}

func TestGatewayDiscoveryAdapterMatrix(t *testing.T) {
	t.Run("memory stale fallback update empty and cancel", func(t *testing.T) {
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "memory-first")
		}))
		t.Cleanup(first.Close)
		second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "memory-second")
		}))
		t.Cleanup(second.Close)

		registry := discovery.NewMemoryRegistry()
		lease, err := registry.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: first.URL})
		if err != nil {
			t.Fatal(err)
		}
		g, err := New(
			[]Route{{PathPrefix: "/api", Service: "users"}},
			WithDiscoveryFailover(),
			WithDiscoveryResolvers(map[string]discovery.Resolver{"users": registry}),
		)
		if err != nil {
			t.Fatal(err)
		}

		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "memory-first")
		if err := lease.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "memory-first")
		assertGatewayDiscoverySnapshot(t, g, "users", true, first.URL)

		if _, err := registry.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: second.URL}); err != nil {
			t.Fatal(err)
		}
		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "memory-second")
		assertGatewayDiscoverySnapshot(t, g, "users", false, second.URL)

		empty := discovery.NewMemoryRegistry()
		emptyGateway, err := New([]Route{{PathPrefix: "/api", Service: "users"}}, WithDiscoveryResolvers(map[string]discovery.Resolver{"users": empty}))
		if err != nil {
			t.Fatal(err)
		}
		if err := emptyGateway.HealthCheck(context.Background()); err == nil || !strings.Contains(err.Error(), "no service instances resolved") {
			t.Fatalf("memory empty health error = %v, want no service instances resolved", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := g.HealthCheck(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("memory canceled health error = %v, want context.Canceled", err)
		}
	})

	t.Run("dns stale fallback update empty and cancel", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "dns-first")
		}))
		t.Cleanup(upstream.Close)
		host, portText, err := net.SplitHostPort(upstream.Listener.Addr().String())
		if err != nil {
			t.Fatalf("split upstream address: %v", err)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			t.Fatalf("upstream host %q is not an IP address", host)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			t.Fatalf("parse upstream port %q: %v", portText, err)
		}

		var lookupMu sync.Mutex
		lookupIPs := []net.IP{ip}
		lookupErr := error(nil)
		dnsResolver, err := rpc.NewDNSResolver(rpc.DNSResolverConfig{
			Host: "users.service.local",
			Port: port,
			LookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				lookupMu.Lock()
				defer lookupMu.Unlock()
				if lookupErr != nil {
					return nil, lookupErr
				}
				return append([]net.IP(nil), lookupIPs...), nil
			},
			WatchInterval: time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		failover, err := rpc.NewFailoverResolver(dnsResolver)
		if err != nil {
			t.Fatal(err)
		}
		g, err := NewFromConfig(
			Config{Routes: []RouteConfig{{PathPrefix: "/api", Service: "users"}}},
			map[string]rpc.Resolver{"users": failover},
		)
		if err != nil {
			t.Fatal(err)
		}

		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "dns-first")
		lookupMu.Lock()
		lookupErr = errors.New("dns unavailable")
		lookupMu.Unlock()
		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "dns-first")
		assertGatewayDiscoverySnapshot(t, g, "users", true, upstream.URL)

		lookupMu.Lock()
		lookupErr = nil
		lookupIPs = []net.IP{net.ParseIP("127.0.0.2")}
		lookupMu.Unlock()
		snapshot := g.Snapshot()
		if len(snapshot.Discovery) != 1 || !slices.Contains(snapshot.Discovery[0].Endpoints, "http://127.0.0.2:"+portText) {
			t.Fatalf("dns update snapshot = %+v, want updated DNS endpoint", snapshot.Discovery)
		}

		emptyResolver, err := rpc.NewDNSResolver(rpc.DNSResolverConfig{
			Host: "empty.service.local",
			Port: port,
			LookupIP: func(context.Context, string) ([]net.IP, error) {
				return nil, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		emptyGateway, err := NewFromConfig(
			Config{Routes: []RouteConfig{{PathPrefix: "/api", Service: "users"}}},
			map[string]rpc.Resolver{"users": emptyResolver},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := emptyGateway.HealthCheck(context.Background()); err == nil || !strings.Contains(err.Error(), "no rpc endpoints resolved") {
			t.Fatalf("dns empty health error = %v, want no rpc endpoints resolved", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := g.HealthCheck(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("dns canceled health error = %v, want context.Canceled", err)
		}
	})

	t.Run("static update empty and cancel", func(t *testing.T) {
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "static-first")
		}))
		t.Cleanup(first.Close)
		second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "static-second")
		}))
		t.Cleanup(second.Close)

		g, err := New([]Route{{PathPrefix: "/api", Targets: []string{first.URL}}})
		if err != nil {
			t.Fatal(err)
		}
		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "static-first")
		if err := g.SetRoutes([]Route{{PathPrefix: "/api", Targets: []string{second.URL}}}); err != nil {
			t.Fatalf("static SetRoutes update: %v", err)
		}
		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "static-second")

		emptyGateway, err := New([]Route{{PathPrefix: "/api", Resolver: rpc.NewStaticResolver()}})
		if err != nil {
			t.Fatal(err)
		}
		if err := emptyGateway.HealthCheck(context.Background()); err == nil || !strings.Contains(err.Error(), "no rpc endpoints resolved") {
			t.Fatalf("static empty health error = %v, want no rpc endpoints resolved", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := g.HealthCheck(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("static canceled health error = %v, want context.Canceled", err)
		}
	})
}

func assertGatewayBody(t *testing.T, g *Gateway, path string, wantCode int, wantBody string) {
	t.Helper()
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	if rr.Code != wantCode || strings.TrimSpace(rr.Body.String()) != wantBody {
		t.Fatalf("gateway response = %d/%q, want %d/%q", rr.Code, rr.Body.String(), wantCode, wantBody)
	}
}

func assertGatewayDiscoverySnapshot(t *testing.T, g *Gateway, service string, stale bool, endpoint string) {
	t.Helper()
	snapshot := g.Snapshot()
	if len(snapshot.Discovery) != 1 {
		t.Fatalf("discovery snapshot = %+v, want one route snapshot", snapshot.Discovery)
	}
	routeSnapshot := snapshot.Discovery[0]
	if routeSnapshot.Service != service || routeSnapshot.Stale != stale || !slices.Contains(routeSnapshot.Endpoints, endpoint) {
		t.Fatalf("discovery snapshot = %+v, want service=%q stale=%v endpoint=%q", routeSnapshot, service, stale, endpoint)
	}
}

func TestGatewayDiscoveryResolverReflectsRegistryChanges(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "first")
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "second")
	}))
	t.Cleanup(second.Close)

	registry := discovery.NewMemoryRegistry()
	lease, err := registry.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: first.URL})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New([]Route{{PathPrefix: "/api", Service: "users"}}, WithDiscoveryResolvers(map[string]discovery.Resolver{"users": registry}))
	if err != nil {
		t.Fatal(err)
	}

	firstRR := httptest.NewRecorder()
	g.ServeHTTP(firstRR, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if firstRR.Code != http.StatusOK || strings.TrimSpace(firstRR.Body.String()) != "first" {
		t.Fatalf("first status = %d, body = %q", firstRR.Code, firstRR.Body.String())
	}
	if err := lease.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: second.URL}); err != nil {
		t.Fatal(err)
	}
	secondRR := httptest.NewRecorder()
	g.ServeHTTP(secondRR, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if secondRR.Code != http.StatusOK || strings.TrimSpace(secondRR.Body.String()) != "second" {
		t.Fatalf("second status = %d, body = %q", secondRR.Code, secondRR.Body.String())
	}
}

func TestGatewayDiscoveryResolverUpdateRefreshesBalancerEndpointSet(t *testing.T) {
	var firstHits atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits.Add(1)
		_, _ = fmt.Fprint(w, "first")
	}))
	t.Cleanup(first.Close)
	var secondHits atomic.Int64
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		_, _ = fmt.Fprint(w, "second")
	}))
	t.Cleanup(second.Close)

	registry := discovery.NewMemoryRegistry()
	lease, err := registry.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: first.URL})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New([]Route{{PathPrefix: "/api", Service: "users"}}, WithDiscoveryResolvers(map[string]discovery.Resolver{"users": registry}))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "first")
	}
	if firstHits.Load() != 3 || secondHits.Load() != 0 {
		t.Fatalf("before update hits first=%d second=%d, want only first", firstHits.Load(), secondHits.Load())
	}

	if err := lease.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "users", Endpoint: second.URL}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		assertGatewayBody(t, g, "/api/ping", http.StatusOK, "second")
	}
	if firstHits.Load() != 3 || secondHits.Load() != 5 {
		t.Fatalf("after update hits first=%d second=%d, want removed endpoint excluded from balancer set", firstHits.Load(), secondHits.Load())
	}
	snapshot := g.Snapshot()
	if len(snapshot.Discovery) != 1 || !slices.Equal(snapshot.Discovery[0].Endpoints, []string{second.URL}) {
		t.Fatalf("discovery snapshot = %+v, want only new endpoint", snapshot.Discovery)
	}
}

func TestGatewayCanaryUsesDiscoveryResolver(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))
	t.Cleanup(primary.Close)
	canary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Canary-Upstream") != "true" {
			t.Fatalf("missing canary header")
		}
		_, _ = fmt.Fprint(w, "canary")
	}))
	t.Cleanup(canary.Close)

	registry := discovery.NewMemoryRegistry()
	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "users-gray", Endpoint: canary.URL, Tags: map[string]string{"version": "gray"}}); err != nil {
		t.Fatal(err)
	}
	g, err := New([]Route{{
		PathPrefix: "/api",
		Service:    "users",
		Targets:    []string{primary.URL},
		Canary: []CanaryRoute{{
			Service:      "users-gray",
			Discovery:    registry,
			Ratio:        1,
			Headers:      map[string]string{"X-Canary-Upstream": "true"},
			MatchHeaders: map[string]string{"X-Canary": "true"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	req.Header.Set("X-Canary", "true")
	g.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "canary" {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	snapshot := g.Snapshot()
	if len(snapshot.Discovery) != 2 || snapshot.Discovery[1].Kind != "canary" || snapshot.Discovery[1].Service != "users-gray" || len(snapshot.Discovery[1].Instances) != 1 {
		t.Fatalf("discovery snapshot = %+v, want canary discovery instance", snapshot.Discovery)
	}
}

func TestGatewayUnavailableWhenResolverHasNoEndpoints(t *testing.T) {
	g, err := New([]Route{{PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}, Resolver: rpc.NewStaticResolver()}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
}

func TestGatewayRegisterRESTAppliesRestMiddleware(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.URL.Path)
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/gw", Targets: []string{upstream.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := rest.NewServer(rest.Config{})
	if err != nil {
		t.Fatal(err)
	}
	g.RegisterREST(s, rest.WithRequestID())

	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/gw/hello", nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "/hello" {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get(rest.RequestIDHeader) == "" {
		t.Fatalf("missing rest request id header")
	}
}

func TestGatewayRESTAdapterPatternsAndMethods(t *testing.T) {
	methods := routeMethods("")
	if len(methods) != 7 || methods[0] != http.MethodGet || methods[len(methods)-1] != http.MethodOptions {
		t.Fatalf("routeMethods empty = %#v, want default REST methods", methods)
	}
	if got := routeMethods(http.MethodPatch); len(got) != 1 || got[0] != http.MethodPatch {
		t.Fatalf("routeMethods PATCH = %#v, want PATCH only", got)
	}

	tests := []struct {
		name   string
		prefix string
		want   []string
	}{
		{name: "root", prefix: "/", want: []string{"/{path...}"}},
		{name: "prefix", prefix: "/api", want: []string{"/api", "/api/{path...}"}},
		{name: "trailing slash", prefix: "/api/", want: []string{"/api/", "/api/{path...}"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := restPatterns(tt.prefix)
			if len(got) != len(tt.want) {
				t.Fatalf("restPatterns(%q) = %#v, want %#v", tt.prefix, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("restPatterns(%q) = %#v, want %#v", tt.prefix, got, tt.want)
				}
			}
		})
	}
}

func TestGatewayNewFromConfigUsesNamedResolver(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.URL.Path)
	}))
	t.Cleanup(upstream.Close)

	g, err := NewFromConfig(Config{Routes: []RouteConfig{{
		Name:           "users",
		Method:         http.MethodPost,
		PathPrefix:     "/api",
		UpstreamPrefix: "/backend",
		Service:        "users",
	}}}, map[string]rpc.Resolver{"users": rpc.NewStaticResolver(upstream.URL)})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/create", nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "/backend/create" {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
}

func TestGatewayNewFromConfigCoverageBuffer(t *testing.T) {
	resolver := rpc.NewStaticResolver("http://127.0.0.1:1")
	g, err := NewFromConfig(Config{
		Timeout:              50 * time.Millisecond,
		MaxBodyBytes:         128,
		MaxExpandedEndpoints: 2,
		PassiveHealth:        PassiveHealthConfig{Enabled: true, FailureThreshold: 2, EjectionDuration: time.Second},
		ActiveHealth:         ActiveHealthConfig{Enabled: true, Path: "/healthz", Timeout: time.Millisecond, Interval: time.Hour},
		Shadow:               ShadowConfig{Workers: 1, Queue: 2},
		Routes: []RouteConfig{{
			Name:       "orders",
			Method:     http.MethodGet,
			PathPrefix: "/orders",
			Service:    "orders-main",
			Tags:       map[string]string{"zone": "primary"},
			Canary: []CanaryRoute{{
				Name:    "orders-canary",
				Service: "orders-canary",
				Ratio:   0.25,
			}},
			Shadow: []ShadowRoute{{
				Service:     "orders-shadow",
				SampleRatio: 1,
			}},
		}},
	}, map[string]rpc.Resolver{
		"orders-main":   resolver,
		"orders-canary": resolver,
		"orders-shadow": resolver,
	})
	if err != nil {
		t.Fatalf("NewFromConfig: %v", err)
	}
	defer func() { _ = g.Close() }()
	if g.timeout != 50*time.Millisecond || g.maxBodyBytes != 128 || g.maxExpandedEndpoint != 2 || g.passive == nil || g.shadowPool == nil {
		t.Fatalf("gateway config not applied: timeout=%s maxBody=%d expanded=%d passive=%v shadowPool=%v", g.timeout, g.maxBodyBytes, g.maxExpandedEndpoint, g.passive, g.shadowPool)
	}
	routes := g.Routes()
	if len(routes) != 1 || routes[0].Resolver == nil || routes[0].Canary[0].Resolver == nil || routes[0].Shadow[0].Resolver == nil {
		t.Fatalf("resolved routes = %+v, want main/canary/shadow resolvers", routes)
	}
	snapshot := g.Snapshot()
	if len(snapshot.Discovery) != 3 {
		t.Fatalf("snapshot discovery = %+v, want route/canary/shadow entries", snapshot.Discovery)
	}
	for _, item := range snapshot.Discovery {
		if item.Error != "" || len(item.Endpoints) == 0 {
			t.Fatalf("discovery snapshot item = %+v, want resolved endpoints", item)
		}
	}
}

func TestGatewayRejectsOversizedBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("oversized body reached upstream")
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{PathPrefix: "/api", Targets: []string{upstream.URL}}}, WithMaxBodyBytes(4))
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader("too large")))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
	snapshot := g.Snapshot()
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].Statuses[http.StatusRequestEntityTooLarge] != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestGatewayHealthCheck(t *testing.T) {
	healthy, err := New([]Route{{PathPrefix: "/api", Targets: []string{"http://127.0.0.1:8080"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := healthy.HealthCheck(context.Background()); err != nil {
		t.Fatalf("healthy gateway check error = %v", err)
	}
	unhealthy, err := New([]Route{{PathPrefix: "/api", Resolver: rpc.NewStaticResolver()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := unhealthy.HealthCheck(context.Background()); err == nil || !strings.Contains(err.Error(), "route") {
		t.Fatalf("unhealthy gateway check error = %v", err)
	}
}

func TestGatewayStatsWritePrometheus(t *testing.T) {
	stats := NewStats()
	stats.Observe("GET /api", http.StatusOK, 10)
	stats.Observe("GET /api", http.StatusBadGateway, 20)
	var buf bytes.Buffer
	if err := stats.WritePrometheus(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"# HELP gofly_gateway_route_duration_seconds Gateway request duration summary by route.",
		"# TYPE gofly_gateway_route_duration_seconds summary",
		"gofly_gateway_route_requests_total{route=\"GET /api\"} 2",
		"gofly_gateway_route_errors_total{route=\"GET /api\"} 1",
		"gofly_gateway_route_status_total{route=\"GET /api\",status=\"502\"} 1",
		"gofly_gateway_route_duration_seconds_count{route=\"GET /api\"} 2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prometheus output missing %q:\n%s", want, out)
		}
	}
}

func TestGatewayRegisterAdminExposesSnapshotMetricsAndHealth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	rules := governance.NewRuleSet(governance.Rule{Name: "gateway-api", Transport: governance.TransportGateway, Path: "/api/*", Policy: governance.Policy{Headers: map[string]string{"X-Governance": "on"}}})
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{upstream.URL}, Headers: map[string]string{"Authorization": "Bearer upstream"}}}, WithGovernanceRuleSet(rules), WithDescriptors(rpc.Descriptor{Name: "gateway.greeter", Methods: []rpc.MethodDescriptor{{Name: "SayHello"}}}))
	if err != nil {
		t.Fatal(err)
	}
	s := rest.MustNewServer(rest.Config{})
	g.RegisterREST(s)
	g.RegisterAdmin(s, "/admin/gateway", "secret")

	req := httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d", rec.Code)
	}

	unauthorized := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/admin/gateway/snapshot", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	if !strings.Contains(unauthorized.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("unauthorized body = %s", unauthorized.Body.String())
	}

	for _, tt := range []struct {
		name        string
		path        string
		contentType string
		want        string
	}{
		{name: "snapshot", path: "/admin/gateway/snapshot", contentType: "application/json", want: `"requests":1`},
		{name: "discovery", path: "/admin/gateway/discovery", contentType: "application/json", want: upstream.URL},
		{name: "metrics", path: "/admin/gateway/metrics", contentType: "text/plain", want: "gofly_gateway_route_requests_total"},
		{name: "health", path: "/admin/gateway/health", contentType: "application/json", want: `"status":"ok"`},
		{name: "routes", path: "/admin/gateway/routes", contentType: "application/json", want: `"Authorization":"***"`},
		{name: "descriptors", path: "/admin/gateway/descriptors", contentType: "application/json", want: `"gateway.greeter"`},
		{name: "governance", path: "/admin/gateway/governance/rules", contentType: "application/json", want: `"gateway-api"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set(auth.AuthorizationHeader, "Bearer secret")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Header().Get("Content-Type"), tt.contentType) {
				t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
			}
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body missing %q: %s", tt.want, rec.Body.String())
			}
		})
	}
}

func TestGatewayRegisterAdminExposesFullGovernanceAdminParity(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{Name: "gateway-api", Transport: governance.TransportGateway, Path: "/api/*", Policy: governance.Policy{Headers: map[string]string{"X-Governance": "on"}}})
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{"http://127.0.0.1:65535"}}}, WithGovernanceRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}
	s := rest.MustNewServer(rest.Config{})
	g.RegisterAdmin(s, "/admin/gateway", "secret")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   string
	}{
		{name: "events", method: http.MethodGet, path: "/admin/gateway/governance/events", want: `"action":"replace"`},
		{name: "versions", method: http.MethodGet, path: "/admin/gateway/governance/versions", want: "gateway-api"},
		{name: "diff", method: http.MethodGet, path: "/admin/gateway/governance/diff", want: "gateway-api"},
		{name: "validate", method: http.MethodPost, path: "/admin/gateway/governance/validate", body: `{"rules":[{"name":"valid","transport":"gateway","path":"/v1/*","policy":{"headers":{"X-Test":"ok"}}}]}`, want: `"ok":true`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set(auth.AuthorizationHeader, "Bearer secret")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body missing %q: %s", tt.want, rec.Body.String())
			}
		})
	}

	missingManager := httptest.NewRequest(http.MethodPost, "/admin/gateway/governance/reload", nil)
	missingManager.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, missingManager)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "governance manager is nil") {
		t.Fatalf("reload status = %d body = %s, want manager error", rec.Code, rec.Body.String())
	}
}

func TestGatewayAdminRouteMutationAndAuditCoverageBuffer(t *testing.T) {
	var nilGateway *Gateway
	nilGateway.RegisterAdmin(nil, "", "")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{upstream.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	s := rest.MustNewServer(rest.Config{Preset: rest.PresetCustom, DisableDefaultMiddlewares: true})
	var events []controladmin.AuditEvent
	g.RegisterAdminWithAudit(s, "gateway-admin/", "secret", func(_ context.Context, event controladmin.AuditEvent) {
		events = append(events, event)
	})
	handler := s.Handler()

	adminRequest := func(method, path, body string) *http.Request {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(auth.AuthorizationHeader, "Bearer secret")
		return req
	}
	assertStatus := func(name string, req *http.Request, want int, wantBody string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s status = %d body = %s, want %d", name, rec.Code, rec.Body.String(), want)
		}
		if wantBody != "" && !strings.Contains(rec.Body.String(), wantBody) {
			t.Fatalf("%s body missing %q: %s", name, wantBody, rec.Body.String())
		}
		return rec.Body.String()
	}

	assertStatus("unauthorized snapshot", httptest.NewRequest(http.MethodGet, "/gateway-admin/snapshot", nil), http.StatusUnauthorized, "unauthorized")
	assertStatus("malformed route", adminRequest(http.MethodPost, "/gateway-admin/routes", "{"), http.StatusBadRequest, "unexpected EOF")

	existingRoute := `{"method":"GET","pathPrefix":"/api","targets":["` + upstream.URL + `"]}`
	assertStatus("duplicate route", adminRequest(http.MethodPost, "/gateway-admin/routes", existingRoute), http.StatusConflict, ErrRouteExists.Error())

	newRoute := RouteConfig{
		Name:       "hot",
		Method:     http.MethodPost,
		PathPrefix: "/hot",
		Targets:    []string{upstream.URL},
		Headers:    map[string]string{"Authorization": "Bearer upstream-secret"},
		Header: HeaderPolicy{
			SetRequest:  map[string]string{"X-Token": "request-secret"},
			SetResponse: map[string]string{"Set-Cookie": "response-secret"},
		},
		Canary: []CanaryRoute{{Name: "canary", Target: upstream.URL, Ratio: 0.5, Headers: map[string]string{"Cookie": "canary-secret"}, MatchHeaders: map[string]string{"Authorization": "Bearer canary"}}},
		Shadow: []ShadowRoute{{Target: upstream.URL, SampleRatio: 1, Headers: map[string]string{"X-Shadow-Token": "shadow-secret"}}},
	}
	body, err := json.Marshal(newRoute)
	if err != nil {
		t.Fatal(err)
	}
	createdBody := assertStatus("create route", adminRequest(http.MethodPost, "/gateway-admin/routes", string(body)), http.StatusCreated, `"pathPrefix":"/hot"`)
	for _, leaked := range []string{"upstream-secret", "request-secret", "response-secret", "canary-secret", "Bearer canary", "shadow-secret"} {
		if strings.Contains(createdBody, leaked) {
			t.Fatalf("created route leaked sensitive value %q: %s", leaked, createdBody)
		}
	}
	if !strings.Contains(createdBody, `"Authorization":"***"`) || !strings.Contains(createdBody, `"X-Shadow-Token":"***"`) {
		t.Fatalf("created route body did not mask sensitive values: %s", createdBody)
	}

	newRoute.Targets = []string{"http://127.0.0.1:65535"}
	body, err = json.Marshal(newRoute)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus("upsert route", adminRequest(http.MethodPut, "/gateway-admin/routes", string(body)), http.StatusOK, `"targets":["http://127.0.0.1:65535"]`)
	routesBody := assertStatus("list routes", adminRequest(http.MethodGet, "/gateway-admin/routes", ""), http.StatusOK, `"X-Token":"***"`)
	if strings.Contains(routesBody, "request-secret") || strings.Contains(routesBody, "shadow-secret") {
		t.Fatalf("routes response leaked sensitive values: %s", routesBody)
	}

	assertStatus("delete missing route", adminRequest(http.MethodDelete, "/gateway-admin/routes?method=GET&pathPrefix=/missing", ""), http.StatusNotFound, ErrNoRoute.Error())
	assertStatus("delete hot route", adminRequest(http.MethodDelete, "/gateway-admin/routes?method=POST&pathPrefix="+url.QueryEscape("/hot"), ""), http.StatusOK, `"status":"ok"`)
	assertStatus("delete api route", adminRequest(http.MethodDelete, "/gateway-admin/routes?method=GET&pathPrefix="+url.QueryEscape("/api"), ""), http.StatusOK, `"status":"ok"`)
	assertStatus("health unavailable", adminRequest(http.MethodGet, "/gateway-admin/health", ""), http.StatusServiceUnavailable, `"status":"unavailable"`)
	assertStatus("governance root", adminRequest(http.MethodGet, "/gateway-admin/governance", ""), http.StatusOK, `"version"`)

	seenStatus := map[int]bool{}
	for _, event := range events {
		if event.Component != "gateway" || event.Path == "" || event.Method == "" {
			t.Fatalf("invalid audit event: %+v", event)
		}
		seenStatus[event.Status] = true
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusCreated, http.StatusServiceUnavailable} {
		if !seenStatus[status] {
			t.Fatalf("audit statuses = %#v, missing %d", seenStatus, status)
		}
	}
}

func TestGatewayHTTPProxyErrorBranchesCoverageBuffer(t *testing.T) {
	g, err := New([]Route{{PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}}}, WithHTTPClient(&http.Client{Transport: gatewayRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orders?debug=1", strings.NewReader("body"))
	req.Host = "gateway.local"
	req.RemoteAddr = "198.51.100.10:12345"

	if result, err := g.proxyHTTPOnce(req, Route{PathPrefix: "/api"}, "://bad-endpoint", []byte("body"), nil); err == nil || result.Endpoint != "://bad-endpoint" {
		t.Fatalf("invalid endpoint result = %+v err = %v, want parse error with endpoint", result, err)
	}
	if result, err := g.proxyHTTPOnce(req, Route{PathPrefix: "/api"}, "http://127.0.0.1:1", []byte("body"), nil); err == nil || result.Err == nil {
		t.Fatalf("client error result = %+v err = %v, want transport error", result, err)
	}

	g, err = New([]Route{{PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}}}, WithHTTPClient(&http.Client{Transport: gatewayRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Upstream": []string{"ok"}}, Body: errorReadCloser{err: errors.New("read failed")}}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := g.proxyHTTPOnce(req, Route{PathPrefix: "/api"}, "http://127.0.0.1:1", []byte("body"), nil); err == nil || result.Status != http.StatusOK || result.Header.Get("X-Upstream") != "ok" {
		t.Fatalf("body read result = %+v err = %v, want response metadata with read error", result, err)
	}

	req.TLS = &tls.ConnectionState{}
	req.Header.Set(HeaderForwardedFor, "203.0.113.7")
	out, err := cloneProxyRequest(req, mustParseGatewayURL(t, "http://upstream.local/base"), Route{Name: "orders", Service: "orders-svc", PreserveHost: true, Headers: map[string]string{"X-Route": "hot"}}, []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Host != "gateway.local" || out.Header.Get(HeaderForwardedProto) != "https" || out.Header.Get(HeaderForwardedFor) != "203.0.113.7, 198.51.100.10" {
		t.Fatalf("forwarded headers host=%q proto=%q for=%q", out.Host, out.Header.Get(HeaderForwardedProto), out.Header.Get(HeaderForwardedFor))
	}
	if out.Header.Get(HeaderGatewayService) != "orders-svc" || out.Header.Get(HeaderGatewayRoute) != "orders" || out.Header.Get("X-Route") != "hot" {
		t.Fatalf("gateway headers = %#v", out.Header)
	}
	if body, err := out.GetBody(); err != nil {
		t.Fatalf("GetBody returned error: %v", err)
	} else {
		defer body.Close()
		data, readErr := io.ReadAll(body)
		if readErr != nil || string(data) != "body" {
			t.Fatalf("GetBody data = %q err = %v, want body", data, readErr)
		}
	}
}

func mustParseGatewayURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestGatewayRouteHotUpdate(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "first")
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "second")
	}))
	t.Cleanup(second.Close)

	g, err := New([]Route{{Name: "api", Method: http.MethodGet, PathPrefix: "/api", Targets: []string{first.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.AddRoute(Route{Name: "hot", Method: http.MethodPost, PathPrefix: "/hot", Targets: []string{second.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddRoute(Route{Method: http.MethodPost, PathPrefix: "/hot", Targets: []string{second.URL}}); err != ErrRouteExists {
		t.Fatalf("duplicate add error = %v, want ErrRouteExists", err)
	}

	hot := httptest.NewRecorder()
	g.ServeHTTP(hot, httptest.NewRequest(http.MethodPost, "/hot/ping", nil))
	if hot.Code != http.StatusOK || strings.TrimSpace(hot.Body.String()) != "second" {
		t.Fatalf("hot route status = %d body = %q", hot.Code, hot.Body.String())
	}

	if err := g.UpdateRoute(Route{Name: "api-v2", Method: http.MethodGet, PathPrefix: "/api", Targets: []string{second.URL}}); err != nil {
		t.Fatal(err)
	}
	updated := httptest.NewRecorder()
	g.ServeHTTP(updated, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if updated.Code != http.StatusOK || strings.TrimSpace(updated.Body.String()) != "second" {
		t.Fatalf("updated route status = %d body = %q", updated.Code, updated.Body.String())
	}

	if !g.RemoveRoute(http.MethodGet, "/api") {
		t.Fatalf("RemoveRoute returned false, want true")
	}
	removed := httptest.NewRecorder()
	g.ServeHTTP(removed, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if removed.Code != http.StatusNotFound {
		t.Fatalf("removed route status = %d, want 404", removed.Code)
	}
	configs := g.RouteConfigs()
	if len(configs) != 1 || configs[0].PathPrefix != "/hot" || configs[0].Method != http.MethodPost {
		t.Fatalf("route configs = %#v, want only POST /hot", configs)
	}
}

func TestGatewayAdminHotUpdatesRoutes(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "first")
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "second")
	}))
	t.Cleanup(second.Close)

	g, err := New([]Route{{Name: "api", Method: http.MethodGet, PathPrefix: "/api", Targets: []string{first.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	s := rest.MustNewServer(rest.Config{})
	g.RegisterAdmin(s, "/admin/gateway", "secret")

	post := httptest.NewRequest(http.MethodPost, "/admin/gateway/routes", strings.NewReader(fmt.Sprintf(`{"name":"hot","method":"POST","pathPrefix":"/hot","targets":[%q]}`, second.URL)))
	post.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	postRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(postRec, post)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("post route status = %d body = %s", postRec.Code, postRec.Body.String())
	}
	hot := httptest.NewRecorder()
	g.ServeHTTP(hot, httptest.NewRequest(http.MethodPost, "/hot/ping", nil))
	if hot.Code != http.StatusOK || strings.TrimSpace(hot.Body.String()) != "second" {
		t.Fatalf("hot route status = %d body = %q", hot.Code, hot.Body.String())
	}

	put := httptest.NewRequest(http.MethodPut, "/admin/gateway/routes", strings.NewReader(fmt.Sprintf(`{"name":"api-v2","method":"GET","pathPrefix":"/api","targets":[%q]}`, second.URL)))
	put.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	putRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put route status = %d body = %s", putRec.Code, putRec.Body.String())
	}
	updated := httptest.NewRecorder()
	g.ServeHTTP(updated, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if updated.Code != http.StatusOK || strings.TrimSpace(updated.Body.String()) != "second" {
		t.Fatalf("updated route status = %d body = %q", updated.Code, updated.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/admin/gateway/routes", nil)
	list.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	listRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"pathPrefix":"/hot"`) {
		t.Fatalf("routes status = %d body = %s", listRec.Code, listRec.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/admin/gateway/routes?method=POST&pathPrefix=/hot", nil)
	deleteRequest.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	deleteRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(deleteRec, deleteRequest)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete route status = %d body = %s", deleteRec.Code, deleteRec.Body.String())
	}
	removed := httptest.NewRecorder()
	g.ServeHTTP(removed, httptest.NewRequest(http.MethodPost, "/hot/ping", nil))
	if removed.Code != http.StatusNotFound {
		t.Fatalf("removed route status = %d, want 404", removed.Code)
	}
}

func TestGatewayAdminWithoutTokenAllowsOnlyLocal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{upstream.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	s := rest.MustNewServer(rest.Config{})
	g.RegisterAdmin(s, "/admin/gateway", "")

	local := httptest.NewRequest(http.MethodGet, "/admin/gateway/snapshot", nil)
	local.RemoteAddr = "127.0.0.1:12345"
	localRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(localRec, local)
	if localRec.Code != http.StatusOK {
		t.Fatalf("local admin status = %d, want 200", localRec.Code)
	}
	remote := httptest.NewRequest(http.MethodGet, "/admin/gateway/snapshot", nil)
	remote.RemoteAddr = "203.0.113.10:12345"
	remoteRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote admin status = %d, want 403", remoteRec.Code)
	}
}

func TestGatewayShadowPoolDropsWhenQueueFull(t *testing.T) {
	release := make(chan struct{})
	shadow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = fmt.Fprint(w, "shadow")
	}))
	t.Cleanup(shadow.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{
		Method:     http.MethodGet,
		PathPrefix: "/api",
		Targets:    []string{upstream.URL},
		Shadow:     []ShadowRoute{{Target: shadow.URL, SampleRatio: 1}},
	}}, WithShadowPool(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	t.Cleanup(func() { close(release) })
	for i := 0; i < 20; i++ {
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i, rec.Code)
		}
	}
	if got := g.stats.Snapshot().Routes[0].ShadowDropped; got == 0 {
		t.Fatalf("shadow dropped = %d, want drops when worker queue is full", got)
	}
}

func TestGatewayActiveHealthProbesHTTPUpstream(t *testing.T) {
	var healthy atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" && !healthy.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{upstream.URL}}}, WithActiveHealth(ActiveHealthConfig{Enabled: true, Timeout: time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.HealthCheck(context.Background()); err == nil {
		t.Fatal("HealthCheck succeeded, want active probe failure")
	}
	healthy.Store(true)
	if err := g.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck healthy upstream: %v", err)
	}
}

func TestGatewayRetriesOnRetryableStatus(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "bad", http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{PathPrefix: "/api", Targets: []string{upstream.URL}, Retry: RetryPolicy{Attempts: 2, Statuses: []int{http.StatusBadGateway}, Methods: []string{http.MethodGet}}}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "ok" || calls.Load() != 2 {
		t.Fatalf("status = %d body = %q calls = %d", rr.Code, rr.Body.String(), calls.Load())
	}
	if got := g.Snapshot().Routes[0].Retries; got != 1 {
		t.Fatalf("retries = %d, want 1", got)
	}
}

func TestGatewayRetryBudgetLimitsRetries(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "retry", http.StatusServiceUnavailable)
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{PathPrefix: "/api", Targets: []string{upstream.URL}, Retry: RetryPolicy{Attempts: 5, Statuses: []int{http.StatusServiceUnavailable}, Methods: []string{http.MethodGet}, BudgetRate: 1, BudgetBurst: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want initial call plus one budgeted retry", got)
	}
}

func TestGatewayRuntimeSnapshotExposesOutboundResilience(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "retry", http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	g, err := NewFromConfig(Config{
		Timeout:      3 * time.Second,
		MaxBodyBytes: 4096,
		Routes: []RouteConfig{{
			Name:       "orders",
			Method:     http.MethodGet,
			PathPrefix: "/api",
			Service:    "orders",
			Targets:    []string{upstream.URL},
			Timeout:    5 * time.Second,
			Retry: RetryPolicy{
				Attempts:     2,
				Backoff:      time.Millisecond,
				Statuses:     []int{http.StatusBadGateway},
				Methods:      []string{http.MethodGet},
				BudgetRate:   1,
				BudgetBurst:  1,
				MaxBodyBytes: 1024,
			},
			Breaker:     BreakerConfig{Enabled: true, OpenTimeout: time.Second, Window: time.Minute, Buckets: 2, MinRequests: 1, FailureRatio: 0.5},
			RateLimit:   RateLimitConfig{Rate: 100, Burst: 100},
			Concurrency: ConcurrencyConfig{Limit: 64},
		}},
	}, nil, WithGovernanceRuleSet(governance.NewRuleSet(governance.Rule{Name: "gateway-default", Transport: governance.TransportGateway, Path: "/api/*"})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })

	before := g.RuntimeSnapshot()
	if !before.GovernanceBacked || before.DefaultTimeout != 3*time.Second || before.MaxBodyBytes != 4096 || before.RouteCount != 1 || len(before.Routes) != 1 {
		t.Fatalf("runtime snapshot = %+v, want governed gateway defaults and one route", before)
	}
	route := before.Routes[0]
	if route.Name != "orders" || route.Method != http.MethodGet || route.PathPrefix != "/api" || route.Service != "orders" || route.TargetCount != 1 {
		t.Fatalf("route runtime identity = %+v", route)
	}
	if route.Timeout != 5*time.Second || route.EffectiveTimeout != 5*time.Second {
		t.Fatalf("route timeout = %+v, want explicit 5s", route)
	}
	if route.Retry.Attempts != 2 || route.Retry.Backoff != time.Millisecond || route.Retry.BudgetRate != 1 || route.Retry.BudgetBurst != 1 || route.Retry.MaxBodyBytes != 1024 || !route.Retry.shouldRetryStatus(http.StatusBadGateway) || !route.Retry.matchesMethod(http.MethodGet) {
		t.Fatalf("route retry = %+v, want generated outbound retry policy", route.Retry)
	}
	if !route.Breaker.Enabled || route.RateLimit.Rate != 100 || route.Concurrency.Limit != 64 {
		t.Fatalf("route resilience = breaker %+v rate %+v concurrency %+v", route.Breaker, route.RateLimit, route.Concurrency)
	}

	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orders", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" || calls.Load() != 2 {
		t.Fatalf("gateway status = %d body = %q calls = %d, want retry success", rec.Code, rec.Body.String(), calls.Load())
	}
	after := g.RuntimeSnapshot()
	if after.Cache.RateLimiters != 1 || after.Cache.ConcurrencyLimiters != 1 || after.Cache.Breakers != 1 || after.Cache.RetryRouteBudgets != 1 || after.Cache.RetryUpstreamBudget != 1 {
		t.Fatalf("runtime cache = %+v, want materialized outbound resilience primitives", after.Cache)
	}
}

func TestRouteConfigsFromOpenAPIImportsDeterministicGatewayRoutes(t *testing.T) {
	doc := rest.OpenAPIDocument{
		OpenAPI: "3.0.3",
		Info:    rest.OpenAPIInfo{Title: "orders API", Version: "1.0.0"},
		Paths: map[string]map[string]rest.Operation{
			"/orders": {
				"post": {OperationID: "createOrder", Tags: []string{"orders"}, Responses: map[string]rest.Response{"201": {Description: "created"}}},
			},
			"/orders/{id}": {
				"get": {OperationID: "getOrder", Tags: []string{"orders"}, Responses: map[string]rest.Response{"200": {Description: "ok"}}},
			},
		},
	}
	routes, err := RouteConfigsFromOpenAPI(doc, OpenAPIRouteOptions{
		NamePrefix:     "edge-",
		GatewayPrefix:  "/edge",
		UpstreamPrefix: "/internal",
		Targets:        []string{"http://127.0.0.1:1"},
		Timeout:        time.Second,
		Retry:          RetryPolicy{Attempts: 2, Methods: []string{http.MethodGet}},
		Headers:        map[string]string{"X-Gateway-Contract": "openapi"},
	})
	if err != nil {
		t.Fatalf("RouteConfigsFromOpenAPI: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes length = %d, want 2", len(routes))
	}
	first, second := routes[0], routes[1]
	if first.Name != "edge-createOrder" || first.Method != http.MethodPost || first.PathPrefix != "/edge/orders" || first.UpstreamPrefix != "/internal/orders" {
		t.Fatalf("first route = %+v", first)
	}
	if second.Name != "edge-getOrder" || second.Method != http.MethodGet || second.PathPrefix != "/edge/orders" || second.UpstreamPrefix != "/internal/orders" {
		t.Fatalf("second route = %+v", second)
	}
	if second.Retry.Attempts != 2 || !second.Retry.matchesMethod(http.MethodGet) || second.Headers["X-Gateway-Contract"] != "openapi" {
		t.Fatalf("second route policies = retry %+v headers %+v", second.Retry, second.Headers)
	}
}

func TestRoutesFromOpenAPIProxyRuntime(t *testing.T) {
	var seen atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/internal/orders/42" || r.URL.Query().Get("expand") != "items" {
			t.Errorf("upstream request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("X-Gateway-Contract") != "openapi" {
			t.Errorf("X-Gateway-Contract = %q, want openapi", r.Header.Get("X-Gateway-Contract"))
		}
		_, _ = fmt.Fprint(w, `{"id":"42"}`)
	}))
	t.Cleanup(upstream.Close)

	doc := rest.OpenAPIDocument{
		OpenAPI: "3.0.3",
		Info:    rest.OpenAPIInfo{Title: "orders API", Version: "1.0.0"},
		Paths: map[string]map[string]rest.Operation{
			"/orders/{id}": {
				"get": {OperationID: "getOrder", Responses: map[string]rest.Response{"200": {Description: "ok"}}},
			},
		},
	}
	routes, err := RoutesFromOpenAPI(doc, OpenAPIRouteOptions{
		GatewayPrefix:  "/edge",
		UpstreamPrefix: "/internal",
		Service:        "orders",
		Targets:        []string{upstream.URL},
		Headers:        map[string]string{"X-Gateway-Contract": "openapi"},
	})
	if err != nil {
		t.Fatalf("RoutesFromOpenAPI: %v", err)
	}
	g, err := New(routes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })

	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/edge/orders/42?expand=items", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"id":"42"}` || seen.Load() != 1 {
		t.Fatalf("gateway response code = %d body = %q seen = %d", rec.Code, rec.Body.String(), seen.Load())
	}
	snapshot := g.RuntimeSnapshot()
	if snapshot.RouteCount != 1 || snapshot.Routes[0].Name != "getOrder" || snapshot.Routes[0].Service != "orders" {
		t.Fatalf("runtime snapshot = %+v", snapshot)
	}
}

func TestRoutesFromOpenAPIURLGroupsOperationsByTag(t *testing.T) {
	var ordersSeen atomic.Int64
	ordersUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ordersSeen.Add(1)
		if r.URL.Path != "/orders-api/orders/42" || r.Header.Get("X-Backend") != "orders" || r.Header.Get("X-Base") != "edge" {
			t.Errorf("orders upstream request path=%q headers=%q/%q", r.URL.Path, r.Header.Get("X-Backend"), r.Header.Get("X-Base"))
		}
		_, _ = fmt.Fprint(w, `{"backend":"orders"}`)
	}))
	t.Cleanup(ordersUpstream.Close)

	var inventorySeen atomic.Int64
	inventoryUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inventorySeen.Add(1)
		if r.URL.Path != "/inventory-api/inventory/sku-1" || r.Header.Get("X-Backend") != "inventory" || r.Header.Get("X-Base") != "edge" {
			t.Errorf("inventory upstream request path=%q headers=%q/%q", r.URL.Path, r.Header.Get("X-Backend"), r.Header.Get("X-Base"))
		}
		_, _ = fmt.Fprint(w, `{"backend":"inventory"}`)
	}))
	t.Cleanup(inventoryUpstream.Close)

	doc := rest.OpenAPIDocument{
		OpenAPI: "3.0.3",
		Info:    rest.OpenAPIInfo{Title: "edge contract", Version: "1.0.0"},
		Paths: map[string]map[string]rest.Operation{
			"/inventory/{sku}": {
				"get": {OperationID: "getInventory", Tags: []string{"inventory"}, Responses: map[string]rest.Response{"200": {Description: "ok"}}},
			},
			"/orders/{id}": {
				"get": {OperationID: "getOrder", Tags: []string{"orders"}, Responses: map[string]rest.Response{"200": {Description: "ok"}}},
			},
		},
	}
	openAPIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi.json" || r.Header.Get("X-Contract-Token") != "test-token" {
			t.Errorf("openapi request path=%q token=%q", r.URL.Path, r.Header.Get("X-Contract-Token"))
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(doc); err != nil {
			t.Errorf("encode openapi doc: %v", err)
		}
	}))
	t.Cleanup(openAPIServer.Close)

	routes, err := RoutesFromOpenAPIURL(context.Background(), OpenAPIURLSource{
		URL:     openAPIServer.URL + "/openapi.json",
		Headers: map[string]string{"X-Contract-Token": "test-token"},
	}, OpenAPIRouteOptions{
		GatewayPrefix: "/edge",
		Headers:       map[string]string{"X-Base": "edge"},
		Groups: []OpenAPIRouteGroup{
			{
				Name:           "orders",
				MatchTags:      []string{"orders"},
				UpstreamPrefix: "/orders-api",
				Targets:        []string{ordersUpstream.URL},
				Headers:        map[string]string{"X-Backend": "orders"},
			},
			{
				Name:           "inventory",
				MatchTags:      []string{"inventory"},
				UpstreamPrefix: "/inventory-api",
				Targets:        []string{inventoryUpstream.URL},
				Headers:        map[string]string{"X-Backend": "inventory"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RoutesFromOpenAPIURL: %v", err)
	}
	g, err := New(routes)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })

	orders := httptest.NewRecorder()
	g.ServeHTTP(orders, httptest.NewRequest(http.MethodGet, "/edge/orders/42", nil))
	if orders.Code != http.StatusOK || strings.TrimSpace(orders.Body.String()) != `{"backend":"orders"}` {
		t.Fatalf("orders response = %d %q", orders.Code, orders.Body.String())
	}
	inventory := httptest.NewRecorder()
	g.ServeHTTP(inventory, httptest.NewRequest(http.MethodGet, "/edge/inventory/sku-1", nil))
	if inventory.Code != http.StatusOK || strings.TrimSpace(inventory.Body.String()) != `{"backend":"inventory"}` {
		t.Fatalf("inventory response = %d %q", inventory.Code, inventory.Body.String())
	}
	if ordersSeen.Load() != 1 || inventorySeen.Load() != 1 {
		t.Fatalf("upstream calls orders=%d inventory=%d", ordersSeen.Load(), inventorySeen.Load())
	}
}

func TestRoutesFromOpenAPIMapsDescriptorDrivenTranscode(t *testing.T) {
	doc := rest.OpenAPIDocument{
		OpenAPI: "3.0.3",
		Info:    rest.OpenAPIInfo{Title: "orders RPC contract", Version: "1.0.0"},
		Paths: map[string]map[string]rest.Operation{
			"/orders/{id}/items/{item_id}": {
				"post": {
					OperationID: "getOrder",
					Tags:        []string{"orders"},
					Parameters: []rest.Parameter{
						{Name: "id", In: "path", Required: true, Schema: rest.IntegerSchema()},
						{Name: "item_id", In: "path", Required: true, Schema: rest.StringSchema()},
						{Name: "include_history", In: "query", Schema: rest.BooleanSchema()},
						{Name: "tags", In: "query", Schema: rest.ArraySchema(rest.Schema{Type: "integer"})},
						{Name: "score", In: "query", Schema: rest.NumberSchema()},
					},
					RequestBody: rest.JSONBodySchema(rest.Schema{
						Type:     "object",
						Required: []string{"trace", "items"},
						Properties: map[string]rest.Schema{
							"id":    {Type: "string"},
							"trace": {Type: "string"},
							"items": {Type: "array", Items: &rest.Schema{Type: "object", Required: []string{"sku"}, Properties: map[string]rest.Schema{
								"sku":      {Type: "string"},
								"quantity": {Type: "integer"},
							}}},
							"metadata": {Type: "object", Required: []string{"source"}, Properties: map[string]rest.Schema{
								"source": {Type: "string"},
								"urgent": {Type: "boolean"},
							}},
						},
					}, false),
					Responses: map[string]rest.Response{"200": {Description: "ok"}},
					Extensions: map[string]any{
						"x-gofly-transcode": map[string]any{
							"payloadMappings": []any{
								map[string]any{"source": "path.id", "target": "order.id"},
								map[string]any{"source": "path.item_id", "target": "order.itemID"},
								map[string]any{"source": "query.include_history", "target": "options.includeHistory"},
								map[string]any{"source": "query.tags", "target": "options.tags"},
								map[string]any{"source": "query.score", "target": "options.score"},
								map[string]any{"source": "body.trace", "target": "meta.trace"},
								map[string]any{"source": "body.items", "target": "order.lines"},
								map[string]any{"source": "body.metadata.source", "target": "meta.source"},
								map[string]any{"target": "meta.region", "default": "cn"},
							},
						},
					},
				},
			},
		},
	}
	routes, err := RoutesFromOpenAPI(doc, OpenAPIRouteOptions{
		GatewayPrefix: "/contract",
		Groups: []OpenAPIRouteGroup{{
			Name:      "orders",
			MatchTags: []string{"orders"},
			Service:   "orders-rpc",
			Targets:   []string{"http://orders-rpc"},
			Transcode: OpenAPITranscodeOptions{
				Enabled:               true,
				Descriptor:            "orders.OrderService",
				MethodFromOperationID: true,
			},
		}},
	})
	if err != nil {
		t.Fatalf("RoutesFromOpenAPI: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes length = %d, want 1", len(routes))
	}
	route := routes[0]
	if route.Name != "orders-getOrder" || route.Method != http.MethodPost || route.PathPrefix != "/contract/orders" {
		t.Fatalf("imported transcode route = %+v", route)
	}
	if !route.Transcode.Enabled || route.Transcode.Descriptor != "orders.OrderService" || route.Transcode.DescriptorMethod != "GetOrder" {
		t.Fatalf("imported transcode config = %+v", route.Transcode)
	}
	if route.Transcode.Payload.Mode != "openapi" || route.Transcode.Payload.PathTemplate != "/orders/{id}/items/{item_id}" || strings.Join(route.Transcode.Payload.PathParams, ",") != "id,item_id" || strings.Join(route.Transcode.Payload.QueryParams, ",") != "include_history,tags,score" {
		t.Fatalf("imported transcode payload = %+v", route.Transcode.Payload)
	}
	if len(route.Transcode.Payload.PathParameters) != 2 || route.Transcode.Payload.PathParameters[0].Type != "integer" || route.Transcode.Payload.PathParameters[1].Type != "string" {
		t.Fatalf("imported path parameter schemas = %+v", route.Transcode.Payload.PathParameters)
	}
	if len(route.Transcode.Payload.QueryParameters) != 3 || route.Transcode.Payload.QueryParameters[0].Type != "boolean" || route.Transcode.Payload.QueryParameters[1].Type != "array" || route.Transcode.Payload.QueryParameters[1].Items == nil || route.Transcode.Payload.QueryParameters[1].Items.Type != "integer" || route.Transcode.Payload.QueryParameters[2].Type != "number" {
		t.Fatalf("imported query parameter schemas = %+v", route.Transcode.Payload.QueryParameters)
	}
	if route.Transcode.Payload.BodySchema == nil || route.Transcode.Payload.BodySchema.Type != "object" || strings.Join(route.Transcode.Payload.BodySchema.Required, ",") != "trace,items" || len(route.Transcode.Payload.BodySchema.Properties) != 4 {
		t.Fatalf("imported body schema = %+v", route.Transcode.Payload.BodySchema)
	}
	if len(route.Transcode.Payload.Mappings) != 9 || route.Transcode.Payload.Mappings[0].Source != "path.id" || route.Transcode.Payload.Mappings[0].Target != "order.id" {
		t.Fatalf("imported payload mappings = %+v", route.Transcode.Payload.Mappings)
	}

	fake := &fakeGenericClient{payload: json.RawMessage(`{"id":"o42","source":"openapi"}`)}
	g, err := New(routes,
		WithDescriptors(rpc.Descriptor{Name: "orders.OrderService", Methods: []rpc.MethodDescriptor{{Name: "GetOrder"}}}),
		WithTranscoderFactory(func(endpoint string, route Route) (rpc.GenericClient, error) {
			if endpoint != "http://orders-rpc" || route.Service != "orders-rpc" {
				t.Fatalf("transcoder endpoint=%q route service=%q", endpoint, route.Service)
			}
			return fake, nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = g.Close() })

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/contract/orders/42/items/sku-1?include_history=true&tags=1,2&tags=3&score=98.5", strings.NewReader(`{"id":"body-id","trace":"t1","items":[{"sku":"sku-1","quantity":2}],"metadata":{"source":"web","urgent":true}}`)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"source":"openapi"`) {
		t.Fatalf("openapi transcode response = %d %q", rr.Code, rr.Body.String())
	}
	var request map[string]any
	if err := json.Unmarshal(fake.request, &request); err != nil {
		t.Fatalf("decode generic request %s: %v", fake.request, err)
	}
	order, ok := request["order"].(map[string]any)
	if !ok {
		t.Fatalf("mapped order = %#v from request=%s", request["order"], fake.request)
	}
	options, ok := request["options"].(map[string]any)
	if !ok {
		t.Fatalf("mapped options = %#v from request=%s", request["options"], fake.request)
	}
	meta, ok := request["meta"].(map[string]any)
	if !ok {
		t.Fatalf("mapped meta = %#v from request=%s", request["meta"], fake.request)
	}
	tags, ok := options["tags"].([]any)
	if !ok || len(tags) != 3 || tags[0] != float64(1) || tags[1] != float64(2) || tags[2] != float64(3) {
		t.Fatalf("generic typed tags = %#v from request=%s", options["tags"], fake.request)
	}
	items, ok := order["lines"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("generic typed body items = %#v from request=%s", order["lines"], fake.request)
	}
	firstItem, ok := items[0].(map[string]any)
	if !ok || firstItem["sku"] != "sku-1" || firstItem["quantity"] != float64(2) {
		t.Fatalf("generic typed body first item = %#v from request=%s", items[0], fake.request)
	}
	if fake.method != "orders.OrderService/GetOrder" ||
		order["id"] != float64(42) ||
		order["itemID"] != "sku-1" ||
		options["includeHistory"] != true ||
		options["score"] != 98.5 ||
		meta["trace"] != "t1" ||
		meta["source"] != "web" ||
		meta["region"] != "cn" {
		t.Fatalf("generic call method=%q request=%s", fake.method, fake.request)
	}

	fake.request = nil
	invalid := httptest.NewRecorder()
	g.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/contract/orders/not-int/items/sku-1?include_history=true", strings.NewReader(`{"trace":"t1","items":[{"sku":"sku-1","quantity":2}]}`)))
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_argument"`) || !strings.Contains(invalid.Body.String(), "id must be integer") {
		t.Fatalf("invalid typed transcode response = %d %q", invalid.Code, invalid.Body.String())
	}
	if len(fake.request) != 0 {
		t.Fatalf("invalid typed transcode called backend with request=%s", fake.request)
	}

	invalidBody := httptest.NewRecorder()
	g.ServeHTTP(invalidBody, httptest.NewRequest(http.MethodPost, "/contract/orders/42/items/sku-1?include_history=true", strings.NewReader(`{"items":[{"sku":"sku-1","quantity":"two"}],"metadata":{"source":"web"}}`)))
	if invalidBody.Code != http.StatusBadRequest || !strings.Contains(invalidBody.Body.String(), `"code":"invalid_argument"`) || !strings.Contains(invalidBody.Body.String(), "body.trace is required") {
		t.Fatalf("invalid body required response = %d %q", invalidBody.Code, invalidBody.Body.String())
	}

	invalidNested := httptest.NewRecorder()
	g.ServeHTTP(invalidNested, httptest.NewRequest(http.MethodPost, "/contract/orders/42/items/sku-1?include_history=true", strings.NewReader(`{"trace":"t1","items":[{"sku":"sku-1","quantity":"two"}],"metadata":{"source":"web"}}`)))
	if invalidNested.Code != http.StatusBadRequest || !strings.Contains(invalidNested.Body.String(), `"code":"invalid_argument"`) || !strings.Contains(invalidNested.Body.String(), "body.items[0].quantity must be integer") {
		t.Fatalf("invalid nested body response = %d %q", invalidNested.Code, invalidNested.Body.String())
	}
}

func TestFetchOpenAPIDocumentValidation(t *testing.T) {
	if _, err := FetchOpenAPIDocument(context.TODO(), OpenAPIURLSource{URL: "://bad"}); err == nil || !strings.Contains(err.Error(), "parse openapi url") {
		t.Fatalf("bad url error = %v", err)
	}
	if _, err := FetchOpenAPIDocument(context.Background(), OpenAPIURLSource{URL: "file:///tmp/openapi.json"}); err == nil || !strings.Contains(err.Error(), "scheme must be http or https") {
		t.Fatalf("bad scheme error = %v", err)
	}
	largeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"openapi":"3.0.3","paths":{}}`)
	}))
	t.Cleanup(largeServer.Close)
	if _, err := FetchOpenAPIDocument(context.Background(), OpenAPIURLSource{URL: largeServer.URL, MaxBytes: 8}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("max bytes error = %v", err)
	}
	failingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	t.Cleanup(failingServer.Close)
	if _, err := FetchOpenAPIDocument(context.Background(), OpenAPIURLSource{URL: failingServer.URL}); err == nil || !strings.Contains(err.Error(), "status = 404") {
		t.Fatalf("status error = %v", err)
	}
}

func TestRouteConfigsFromOpenAPIValidation(t *testing.T) {
	_, err := RouteConfigsFromOpenAPI(rest.OpenAPIDocument{}, OpenAPIRouteOptions{Targets: []string{"http://127.0.0.1:1"}})
	if !errors.Is(err, ErrOpenAPIPathsRequired) {
		t.Fatalf("empty paths error = %v, want ErrOpenAPIPathsRequired", err)
	}
	doc := rest.OpenAPIDocument{Paths: map[string]map[string]rest.Operation{"/orders": {"get": {}}}}
	_, err = RouteConfigsFromOpenAPI(doc, OpenAPIRouteOptions{})
	if !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("missing upstream error = %v, want ErrRouteRequired", err)
	}
	doc = rest.OpenAPIDocument{Paths: map[string]map[string]rest.Operation{"/orders": {"trace": {}}}}
	_, err = RouteConfigsFromOpenAPI(doc, OpenAPIRouteOptions{Targets: []string{"http://127.0.0.1:1"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported openapi method") {
		t.Fatalf("unsupported method error = %v", err)
	}
	doc = rest.OpenAPIDocument{Paths: map[string]map[string]rest.Operation{"orders": {"get": {}}}}
	_, err = RouteConfigsFromOpenAPI(doc, OpenAPIRouteOptions{Targets: []string{"http://127.0.0.1:1"}})
	if err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Fatalf("unsafe path error = %v", err)
	}
}

func TestRouteConfigsFromOpenAPIEdgeCases(t *testing.T) {
	doc := rest.OpenAPIDocument{Paths: map[string]map[string]rest.Operation{
		"/": {
			"HEAD":    {Tags: []string{"root"}},
			"options": {OperationID: "rootOptions", Tags: []string{"root"}},
		},
		"/files/{path}": {
			"patch": {Tags: []string{"files"}},
		},
	}}
	routes, err := RouteConfigsFromOpenAPI(doc, OpenAPIRouteOptions{
		NamePrefix:    "edge",
		GatewayPrefix: "/gw",
		Service:       "default",
		Headers:       map[string]string{"X-Base": "edge"},
		Groups: []OpenAPIRouteGroup{{
			Name:      "files",
			MatchTags: []string{"FILES"},
			Targets:   []string{"http://127.0.0.1:1"},
			Headers:   map[string]string{"X-Group": "files"},
		}},
	})
	if err != nil {
		t.Fatalf("RouteConfigsFromOpenAPI edge cases: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("routes length = %d, want 3", len(routes))
	}
	byMethod := make(map[string]RouteConfig, len(routes))
	for _, route := range routes {
		byMethod[route.Method] = route
	}
	if head := byMethod[http.MethodHead]; head.Name != "edge-head-root" || head.PathPrefix != "/gw" || head.UpstreamPrefix != "" || head.Service != "default" {
		t.Fatalf("head route = %+v, want root defaults", head)
	}
	if options := byMethod[http.MethodOptions]; options.Name != "edgerootOptions" || options.PathPrefix != "/gw" {
		t.Fatalf("options route = %+v, want operation id name and root path", options)
	}
	if patch := byMethod[http.MethodPatch]; patch.Name != "files--patch-files-wildcard" || patch.PathPrefix != "/gw/files" || patch.UpstreamPrefix != "/files" || patch.Headers["X-Base"] != "edge" || patch.Headers["X-Group"] != "files" || len(patch.Targets) != 1 {
		t.Fatalf("patch route = %+v, want group override and wildcard name", patch)
	}

	_, err = RouteConfigsFromOpenAPI(
		rest.OpenAPIDocument{Paths: map[string]map[string]rest.Operation{"/orders": {"get": {Tags: []string{"orders"}}}}},
		OpenAPIRouteOptions{Groups: []OpenAPIRouteGroup{{MatchTags: []string{"orders"}}}},
	)
	if !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("matched group without upstream error = %v, want ErrRouteRequired", err)
	}

	_, err = RouteConfigsFromOpenAPI(
		rest.OpenAPIDocument{Paths: map[string]map[string]rest.Operation{"/orders//items": {"get": {}}}},
		OpenAPIRouteOptions{Targets: []string{"http://127.0.0.1:1"}},
	)
	if err == nil || !strings.Contains(err.Error(), "empty segment") {
		t.Fatalf("empty segment error = %v", err)
	}
}

func TestFetchOpenAPIDocumentDecodeAndContextValidation(t *testing.T) {
	var nilContext context.Context
	if _, err := FetchOpenAPIDocument(nilContext, OpenAPIURLSource{URL: "http://127.0.0.1/openapi.json"}); err == nil || !strings.Contains(err.Error(), "context is required") {
		t.Fatalf("nil context error = %v", err)
	}
	if _, err := FetchOpenAPIDocument(context.Background(), OpenAPIURLSource{URL: "http:///openapi.json"}); err == nil || !strings.Contains(err.Error(), "host is required") {
		t.Fatalf("missing host error = %v", err)
	}
	invalidJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{`)
	}))
	t.Cleanup(invalidJSONServer.Close)
	if _, err := FetchOpenAPIDocument(context.Background(), OpenAPIURLSource{URL: invalidJSONServer.URL}); err == nil || !strings.Contains(err.Error(), "decode openapi document") {
		t.Fatalf("decode error = %v", err)
	}
}

func TestGatewayProxyRetryBackoffCancellation(t *testing.T) {
	var cancel context.CancelFunc
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "retry later", http.StatusServiceUnavailable)
		time.AfterFunc(time.Millisecond, cancel)
	}))
	t.Cleanup(upstream.Close)

	g, err := New([]Route{{
		PathPrefix: "/api",
		Targets:    []string{upstream.URL},
		Retry: RetryPolicy{
			Attempts:          2,
			Backoff:           time.Hour,
			Statuses:          []int{http.StatusServiceUnavailable},
			Methods:           []string{http.MethodPost},
			RespectRetryAfter: true,
		},
	}})
	if err != nil {
		t.Fatalf("New gateway: %v", err)
	}
	defer func() { _ = g.Close() }()

	ctx, stop := context.WithCancel(context.Background())
	cancel = stop
	req := httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader("{}"))
	req = req.WithContext(ctx)
	result, err := g.proxyWithRetry(req, g.Routes()[0], []byte("{}"))
	if !errors.Is(err, context.Canceled) || result.Status != http.StatusServiceUnavailable {
		t.Fatalf("proxyWithRetry result=%+v err=%v, want canceled after retryable response", result, err)
	}
}

func TestGatewayHeaderPolicyAndAllowedHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Keep") != "yes" || r.Header.Get("X-Drop") != "" || r.Header.Get("X-Set") != "set" {
			t.Fatalf("headers = %#v", r.Header)
		}
		w.Header().Set("X-Upstream", "ok")
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{
		PathPrefix:   "/api",
		Targets:      []string{upstream.URL},
		AllowedHosts: []string{"gateway.local"},
		Header: HeaderPolicy{
			AllowRequest:  []string{"X-Keep"},
			SetRequest:    map[string]string{"X-Set": "set"},
			SetResponse:   map[string]string{"X-Gateway": "gofly"},
			ExposeHeaders: true,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	blocked := httptest.NewRecorder()
	g.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "http://evil.local/api/ping", nil))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("blocked status = %d, want forbidden", blocked.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "http://gateway.local/api/ping", nil)
	req.Header.Set("X-Keep", "yes")
	req.Header.Set("X-Drop", "no")
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("X-Gateway") != "gofly" || rr.Header().Get("Access-Control-Expose-Headers") != "X-Gateway" {
		t.Fatalf("status = %d headers = %#v", rr.Code, rr.Header())
	}
}

func TestGatewayPassiveHealthEjectsFailingEndpoint(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	var successHits atomic.Int64
	success := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		successHits.Add(1)
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(success.Close)
	g, err := New([]Route{{PathPrefix: "/api", Targets: []string{failing.URL, success.URL}}}, WithPassiveHealth(PassiveHealthConfig{Enabled: true, FailureThreshold: 1}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		rr := httptest.NewRecorder()
		g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api", nil))
	}
	snapshot := g.Snapshot()
	if successHits.Load() == 0 || snapshot.Routes[0].Ejections == 0 {
		t.Fatalf("success hits = %d snapshot = %+v", successHits.Load(), g.Snapshot())
	}
	if len(snapshot.Upstreams) == 0 || !hasEjectedEndpoint(snapshot.Upstreams, failing.URL) {
		t.Fatalf("upstreams = %+v, want ejected failing endpoint", snapshot.Upstreams)
	}
}

func hasEjectedEndpoint(upstreams []EndpointHealthSnapshot, endpoint string) bool {
	for _, upstream := range upstreams {
		if upstream.Endpoint == endpoint && upstream.Ejected && upstream.Failures > 0 {
			return true
		}
	}
	return false
}

func TestGatewayShadowTraffic(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))
	t.Cleanup(primary.Close)
	shadowCh := make(chan string, 1)
	shadow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		shadowCh <- string(data) + ":" + r.Header.Get("X-Shadow")
	}))
	t.Cleanup(shadow.Close)
	g, err := New([]Route{{PathPrefix: "/api", Targets: []string{primary.URL}, Shadow: []ShadowRoute{{Target: shadow.URL, SampleRatio: 1, Headers: map[string]string{"X-Shadow": "true"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api", strings.NewReader("body")))
	select {
	case got := <-shadowCh:
		if got != "body:true" {
			t.Fatalf("shadow got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("shadow request not received")
	}
}

func TestGatewayCanaryMatchesHeaderAndCookie(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "primary:", r.URL.Path)
	}))
	t.Cleanup(primary.Close)
	canary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Canary-Upstream") != "true" {
			t.Fatalf("missing canary header: %#v", r.Header)
		}
		_, _ = fmt.Fprint(w, "canary:", r.URL.Path)
	}))
	t.Cleanup(canary.Close)
	g, err := New([]Route{{
		PathPrefix: "/api",
		Targets:    []string{primary.URL},
		Canary: []CanaryRoute{{
			Target:         canary.URL,
			MatchHeaders:   map[string]string{"X-Gray": "1"},
			MatchCookies:   map[string]string{"bucket": "gray"},
			Headers:        map[string]string{"X-Canary-Upstream": "true"},
			UpstreamPrefix: "/v2",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	miss := httptest.NewRecorder()
	g.ServeHTTP(miss, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if miss.Code != http.StatusOK || strings.TrimSpace(miss.Body.String()) != "primary:/users" {
		t.Fatalf("miss status = %d body = %q", miss.Code, miss.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	req.Header.Set("X-Gray", "1")
	req.AddCookie(&http.Cookie{Name: "bucket", Value: "gray"})
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "canary:/v2/users" {
		t.Fatalf("match status = %d body = %q", rr.Code, rr.Body.String())
	}
}

func TestGatewayCanaryRatioAndConfigResolver(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))
	t.Cleanup(primary.Close)
	canary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.Header.Get(HeaderGatewayService), ":", r.URL.Path)
	}))
	t.Cleanup(canary.Close)
	g, err := NewFromConfig(Config{Routes: []RouteConfig{{
		PathPrefix: "/api",
		Targets:    []string{primary.URL},
		Canary: []CanaryRoute{{
			Service:        "users-gray",
			Ratio:          1,
			UpstreamPrefix: "/gray",
		}},
	}}}, map[string]rpc.Resolver{"users-gray": rpc.NewStaticResolver(canary.URL)})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/ping", nil))
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "users-gray:/gray/ping" {
		t.Fatalf("status = %d body = %q", rr.Code, rr.Body.String())
	}
}

func TestGatewayGovernanceRuleSetAppliesRoutePolicy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.Header.Get("X-Governance"), ":", r.URL.Path)
	}))
	t.Cleanup(upstream.Close)
	rules := governance.NewRuleSet(governance.Rule{
		Transport: governance.TransportGateway,
		Service:   "orders",
		Method:    http.MethodPost,
		Path:      "/api/*",
		Policy: governance.Policy{
			MaxBodyBytes: 4,
			Retry:        governance.RetryPolicy{Attempts: 2},
			Headers:      map[string]string{"X-Governance": "on"},
		},
	})
	g, err := New([]Route{{
		Method:     http.MethodPost,
		PathPrefix: "/api",
		Service:    "orders",
		Targets:    []string{upstream.URL},
	}}, WithGovernanceRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}

	blocked := httptest.NewRecorder()
	g.ServeHTTP(blocked, httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader("too large")))
	if blocked.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("blocked status = %d, want %d", blocked.Code, http.StatusRequestEntityTooLarge)
	}

	allowed := httptest.NewRecorder()
	g.ServeHTTP(allowed, httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader("ok")))
	if allowed.Code != http.StatusOK || strings.TrimSpace(allowed.Body.String()) != "on:/upload" {
		t.Fatalf("allowed status = %d body = %q", allowed.Code, allowed.Body.String())
	}
	snapshot := g.Snapshot()
	if len(snapshot.Rules) != 1 || snapshot.RuleStatus.Rules != 1 || len(snapshot.RuleStats) != 1 || snapshot.RuleStats[0].Hits != 2 {
		t.Fatalf("snapshot = %+v, want rule status and two hits", snapshot)
	}
	if len(snapshot.RuleEvents) == 0 || snapshot.RuleStatus.Events != len(snapshot.RuleEvents) {
		t.Fatalf("snapshot = %+v, want rule events in gateway diagnostics", snapshot)
	}

	rules.Replace(governance.Rule{
		Transport: governance.TransportGateway,
		Service:   "orders",
		Method:    http.MethodPost,
		Path:      "/api/*",
		Policy: governance.Policy{
			MaxBodyBytes: 16,
			Headers:      map[string]string{"X-Governance": "hot"},
		},
	})
	reloaded := httptest.NewRecorder()
	g.ServeHTTP(reloaded, httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader("larger-ok")))
	if reloaded.Code != http.StatusOK || strings.TrimSpace(reloaded.Body.String()) != "hot:/upload" {
		t.Fatalf("reloaded status = %d body = %q", reloaded.Code, reloaded.Body.String())
	}
}

func TestGatewayGovernanceRuleSetEnforcesResiliencePolicy(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		started := make(chan struct{})
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		}))
		t.Cleanup(upstream.Close)
		rules := governance.NewRuleSet(governance.Rule{
			Name:      "gateway-timeout",
			Transport: governance.TransportGateway,
			Service:   "orders",
			Method:    http.MethodGet,
			Path:      "/api/*",
			Policy:    governance.Policy{Timeout: 25 * time.Millisecond},
		})
		g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Service: "orders", Targets: []string{upstream.URL}, Timeout: time.Second}}, WithGovernanceRuleSet(rules))
		if err != nil {
			t.Fatal(err)
		}

		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/list", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("timeout status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for upstream request")
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, "ok")
		}))
		t.Cleanup(upstream.Close)
		rules := governance.NewRuleSet(governance.Rule{
			Name:      "gateway-rate",
			Transport: governance.TransportGateway,
			Service:   "orders",
			Method:    http.MethodGet,
			Path:      "/api/*",
			Policy:    governance.Policy{RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1}},
		})
		g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Service: "orders", Targets: []string{upstream.URL}}}, WithGovernanceRuleSet(rules))
		if err != nil {
			t.Fatal(err)
		}

		first := httptest.NewRecorder()
		g.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/list", nil))
		if first.Code != http.StatusOK {
			t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
		}
		second := httptest.NewRecorder()
		g.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/list", nil))
		if second.Code != http.StatusTooManyRequests {
			t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
		}
		snapshot := g.Snapshot()
		if len(snapshot.RuleStats) != 1 || snapshot.RuleStats[0].Hits != 2 {
			t.Fatalf("snapshot rule stats = %+v, want two rate-limit hits", snapshot.RuleStats)
		}
	})

	t.Run("concurrency", func(t *testing.T) {
		entered := make(chan struct{})
		release := make(chan struct{})
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			_, _ = fmt.Fprint(w, "ok")
		}))
		t.Cleanup(upstream.Close)
		rules := governance.NewRuleSet(governance.Rule{
			Name:      "gateway-concurrency",
			Transport: governance.TransportGateway,
			Service:   "orders",
			Method:    http.MethodGet,
			Path:      "/api/*",
			Policy:    governance.Policy{Concurrency: governance.ConcurrencyPolicy{Limit: 1}},
		})
		g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Service: "orders", Targets: []string{upstream.URL}}}, WithGovernanceRuleSet(rules))
		if err != nil {
			t.Fatal(err)
		}

		first := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			g.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/list", nil))
		}()
		<-entered
		second := httptest.NewRecorder()
		g.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/list", nil))
		if second.Code != http.StatusServiceUnavailable {
			t.Fatalf("second status = %d, want %d", second.Code, http.StatusServiceUnavailable)
		}
		close(release)
		<-done
		if first.Code != http.StatusOK {
			t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
		}
	})

	t.Run("retry", func(t *testing.T) {
		var calls atomic.Int64
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				http.Error(w, "bad", http.StatusBadGateway)
				return
			}
			_, _ = fmt.Fprint(w, "ok")
		}))
		t.Cleanup(upstream.Close)
		rules := governance.NewRuleSet(governance.Rule{
			Name:      "gateway-retry",
			Transport: governance.TransportGateway,
			Service:   "orders",
			Method:    http.MethodGet,
			Path:      "/api/*",
			Policy: governance.Policy{Retry: governance.RetryPolicy{
				Attempts: 2,
				Statuses: []int{http.StatusBadGateway},
				Methods:  []string{http.MethodGet},
			}},
		})
		g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Service: "orders", Targets: []string{upstream.URL}}}, WithGovernanceRuleSet(rules))
		if err != nil {
			t.Fatal(err)
		}

		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/list", nil))
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" || calls.Load() != 2 {
			t.Fatalf("retry status = %d body = %q calls = %d, want retry success", rec.Code, rec.Body.String(), calls.Load())
		}
		if got := g.Snapshot().Routes[0].Retries; got != 1 {
			t.Fatalf("retry snapshot = %d, want one retry", got)
		}
	})

	t.Run("breaker", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad", http.StatusInternalServerError)
		}))
		t.Cleanup(upstream.Close)
		rules := governance.NewRuleSet(governance.Rule{
			Name:      "gateway-breaker",
			Transport: governance.TransportGateway,
			Service:   "orders",
			Method:    http.MethodGet,
			Path:      "/api/*",
			Policy: governance.Policy{Breaker: governance.BreakerPolicy{
				Enabled:      true,
				MinRequests:  1,
				FailureRatio: 0.1,
				OpenTimeout:  time.Second,
			}},
		})
		g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Service: "orders", Targets: []string{upstream.URL}}}, WithGovernanceRuleSet(rules))
		if err != nil {
			t.Fatal(err)
		}

		for i := 0; i < 2; i++ {
			rec := httptest.NewRecorder()
			g.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/list", nil))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("failure %d status = %d, want %d", i, rec.Code, http.StatusInternalServerError)
			}
		}
		blocked := httptest.NewRecorder()
		g.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/api/list", nil))
		if blocked.Code != http.StatusServiceUnavailable {
			t.Fatalf("blocked status = %d, want %d", blocked.Code, http.StatusServiceUnavailable)
		}
	})
}

func TestGatewayGovernanceRuleSetCanaryUsesResolver(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))
	t.Cleanup(primary.Close)
	gray := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.Header.Get(HeaderGatewayService), ":", r.Header.Get("X-Gray"))
	}))
	t.Cleanup(gray.Close)
	rules := governance.NewRuleSet(governance.Rule{
		Transport: governance.TransportGateway,
		Service:   "orders",
		Path:      "/api/*",
		Policy: governance.Policy{Canary: governance.CanaryPolicy{
			Ratio:        1,
			Service:      "orders-gray",
			Headers:      map[string]string{"X-Gray": "true"},
			MatchHeaders: map[string]string{"X-Use-Gray": "1"},
		}},
	})
	g, err := New([]Route{{PathPrefix: "/api", Service: "orders", Targets: []string{primary.URL}}},
		WithGovernanceRuleSet(rules),
		WithResolvers(map[string]rpc.Resolver{"orders-gray": rpc.NewStaticResolver(gray.URL)}),
	)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	req.Header.Set("X-Use-Gray", "1")
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "orders-gray:true" {
		t.Fatalf("status = %d body = %q", rr.Code, rr.Body.String())
	}
}

func TestGatewayGovernanceRuleSetCanaryUsesTargetAndPrefix(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))
	t.Cleanup(primary.Close)
	gray := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.Header.Get("X-Gray"), ":", r.URL.Path)
	}))
	t.Cleanup(gray.Close)
	rules := governance.NewRuleSet(governance.Rule{
		Transport: governance.TransportGateway,
		Service:   "orders",
		Path:      "/api/*",
		Policy: governance.Policy{Canary: governance.CanaryPolicy{
			Ratio:          1,
			Target:         gray.URL,
			UpstreamPrefix: "/v2",
			Headers:        map[string]string{"X-Gray": "target"},
			MatchCookies:   map[string]string{"gray": "1"},
		}},
	})
	g, err := New([]Route{{PathPrefix: "/api", Service: "orders", Targets: []string{primary.URL}}}, WithGovernanceRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	req.AddCookie(&http.Cookie{Name: "gray", Value: "1"})
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "target:/v2/list" {
		t.Fatalf("status = %d body = %q", rr.Code, rr.Body.String())
	}
}

func TestGatewayBreakerOpensAfterFailures(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	g, err := New([]Route{{PathPrefix: "/api", Targets: []string{upstream.URL}, Breaker: BreakerConfig{Enabled: true, MinRequests: 1, FailureRatio: 0.1, OpenTimeout: time.Second}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		rr := httptest.NewRecorder()
		g.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api", nil))
	}
	if g.Snapshot().Routes[0].Errors == 0 {
		t.Fatalf("snapshot = %+v", g.Snapshot())
	}
}

type fakeGenericClient struct {
	method  string
	request json.RawMessage
	md      metadata.MD
	payload json.RawMessage
	err     error
}

func (f *fakeGenericClient) CallRaw(ctx context.Context, method string, request any) (json.RawMessage, metadata.MD, error) {
	f.method = method
	if raw, ok := request.(json.RawMessage); ok {
		f.request = append(json.RawMessage(nil), raw...)
	}
	if md, ok := metadata.FromContext(ctx); ok {
		f.md = md.Clone()
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	return append(json.RawMessage(nil), f.payload...), metadata.MD{"trace": "abc"}, nil
}

func TestGatewayPureProxyAndTranscodeBranches(t *testing.T) {
	if (*Gateway)(nil).Routes() != nil || (*Gateway)(nil).Descriptors() != nil {
		t.Fatal("nil gateway snapshots should return nil")
	}
	if err := (*Gateway)(nil).RegisterDescriptor(rpc.Descriptor{Name: "svc"}); err == nil || !strings.Contains(err.Error(), "gateway is nil") {
		t.Fatalf("nil RegisterDescriptor error = %v, want gateway is nil", err)
	}
	if err := (*Gateway)(nil).AddRoute(Route{}); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("nil AddRoute error = %v, want ErrRouteRequired", err)
	}
	if err := (*Gateway)(nil).UpdateRoute(Route{}); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("nil UpdateRoute error = %v, want ErrRouteRequired", err)
	}
	if err := (*Gateway)(nil).UpsertRoute(Route{}); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("nil UpsertRoute error = %v, want ErrRouteRequired", err)
	}
	if (*Gateway)(nil).RemoveRoute(http.MethodGet, "/") {
		t.Fatal("nil RemoveRoute returned true, want false")
	}

	if _, err := buildTargetURL("::::", Route{}, &url.URL{Path: "/api"}); err == nil || !strings.Contains(err.Error(), "parse endpoint") {
		t.Fatalf("buildTargetURL parse error = %v, want parse endpoint", err)
	}
	if _, err := buildTargetURL("upstream", Route{}, &url.URL{Path: "/api"}); err == nil || !strings.Contains(err.Error(), "scheme and host") {
		t.Fatalf("buildTargetURL missing scheme error = %v, want scheme and host", err)
	}
	if got := rewritePath(Route{PathPrefix: "/", UpstreamPrefix: "/v1"}, "/users"); got != "/v1/users" {
		t.Fatalf("rewritePath root upstream = %q, want /v1/users", got)
	}
	if got := rewritePath(Route{PathPrefix: "/api"}, "/api"); got != "/" {
		t.Fatalf("rewritePath empty suffix = %q, want /", got)
	}
	if got := rewritePath(Route{PathPrefix: "/api", UpstreamPrefix: "/v2"}, "/other"); got != "/other" {
		t.Fatalf("rewritePath no match = %q, want /other", got)
	}

	target, err := buildTargetURL("https://upstream/base", Route{PathPrefix: "/api", UpstreamPrefix: "/v2"}, &url.URL{Path: "/api/users", RawQuery: "q=1"})
	if err != nil {
		t.Fatalf("buildTargetURL valid: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://gateway/api/users", strings.NewReader("body"))
	req.Host = "gateway.example"
	req.RemoteAddr = "192.0.2.10:1234"
	req.TLS = &tls.ConnectionState{}
	req.Header.Set(HeaderForwardedFor, "198.51.100.1")
	cloned, err := cloneProxyRequest(req, target, Route{Name: "route", Service: "svc", PreserveHost: true, Headers: map[string]string{"X-Set": "yes"}}, []byte("payload"))
	if err != nil {
		t.Fatalf("cloneProxyRequest: %v", err)
	}
	if cloned.Host != "gateway.example" || cloned.Header.Get(HeaderForwardedProto) != "https" || cloned.Header.Get(HeaderGatewayRoute) != "route" || cloned.Header.Get("X-Set") != "yes" {
		t.Fatalf("cloned request host/header = host=%q headers=%v", cloned.Host, cloned.Header)
	}
	if got := cloned.Header.Get(HeaderForwardedFor); got != "198.51.100.1, 192.0.2.10" {
		t.Fatalf("forwarded-for = %q, want appended client IP", got)
	}

	transportErr := errors.New("transport down")
	g := &Gateway{client: &http.Client{Transport: gatewayRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, transportErr })}}
	if _, err := g.proxyHTTPOnce(httptest.NewRequest(http.MethodGet, "/api", nil), Route{PathPrefix: "/api"}, "http://upstream", nil, nil); !errors.Is(err, transportErr) {
		t.Fatalf("proxyHTTPOnce transport error = %v, want transport down", err)
	}
	readErr := errors.New("read body")
	g.client = &http.Client{Transport: gatewayRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: errorReadCloser{err: readErr}}, nil
	})}
	result, err := g.proxyHTTPOnce(httptest.NewRequest(http.MethodGet, "/api", nil), Route{PathPrefix: "/api"}, "http://upstream", nil, nil)
	if !errors.Is(err, readErr) || !errors.Is(result.Err, readErr) {
		t.Fatalf("proxyHTTPOnce read error result=%+v err=%v, want read body", result, err)
	}

	retryable := rpc.NewError(rpc.CodeUnavailable, "unavailable")
	fake := &fakeGenericClient{err: retryable}
	g = &Gateway{transcoders: map[string]rpc.GenericClient{"http://upstream": fake}}
	result, err = g.transcodeOnce(httptest.NewRequest(http.MethodPost, "/api/Get", nil), Route{PathPrefix: "/api", Transcode: TranscodeConfig{Enabled: true, Service: "svc"}}, "http://upstream", nil, nil)
	if !errors.Is(err, retryable) || !errors.Is(result.Err, retryable) || result.Status != http.StatusServiceUnavailable {
		t.Fatalf("transcode retryable result=%+v err=%v, want propagated unavailable", result, err)
	}

	g = &Gateway{transcoderFactory: func(string, Route) (rpc.GenericClient, error) { return nil, errors.New("factory failed") }}
	if _, err := g.transcodeOnce(httptest.NewRequest(http.MethodPost, "/api/Get", nil), Route{PathPrefix: "/api", Transcode: TranscodeConfig{Enabled: true, Service: "svc"}}, "http://new", nil, nil); err == nil || !strings.Contains(err.Error(), "factory failed") {
		t.Fatalf("transcode factory error = %v, want factory failed", err)
	}
	if _, err := g.transcodeOnce(httptest.NewRequest(http.MethodPost, "/api/Get", nil), Route{PathPrefix: "/api", Transcode: TranscodeConfig{Enabled: true, DescriptorMethod: "Get"}}, "http://new", nil, nil); err == nil || !strings.Contains(err.Error(), "descriptor is required") {
		t.Fatalf("transcode descriptor config error = %v, want descriptor required", err)
	}
}

func TestGatewayTranscodeRESTToRPC(t *testing.T) {
	fake := &fakeGenericClient{payload: json.RawMessage(`{"ok":true}`)}
	g, err := New([]Route{{
		Name:       "echo",
		Method:     http.MethodPost,
		PathPrefix: "/api",
		Targets:    []string{"http://upstream"},
		Header:     HeaderPolicy{AllowRequest: []string{"X-Tenant"}},
		Transcode:  TranscodeConfig{Enabled: true, Service: "echo", Method: "Say"},
	}}, WithTranscoderFactory(func(endpoint string, route Route) (rpc.GenericClient, error) {
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/say", bytes.NewReader([]byte(`{"name":"gofly"}`)))
	req.Header.Set("X-Tenant", "t1")
	g.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != `{"ok":true}` {
		t.Fatalf("body = %q", rr.Body.String())
	}
	if fake.method != "echo/Say" {
		t.Fatalf("rpc method = %q", fake.method)
	}
	if string(fake.request) != `{"name":"gofly"}` {
		t.Fatalf("rpc request = %s", fake.request)
	}
	if fake.md["x-tenant"] != "t1" {
		t.Fatalf("metadata = %+v", fake.md)
	}
	if rr.Header().Get("X-Gofly-Md-trace") != "abc" {
		t.Fatalf("response metadata header = %q", rr.Header().Get("X-Gofly-Md-trace"))
	}
}

func TestGatewayTranscodeMethodFromPath(t *testing.T) {
	fake := &fakeGenericClient{payload: json.RawMessage(`{}`)}
	g, err := New([]Route{{
		PathPrefix: "/rpc",
		Targets:    []string{"http://upstream"},
		Transcode:  TranscodeConfig{Enabled: true, Service: "users"},
	}}, WithTranscoderFactory(func(endpoint string, route Route) (rpc.GenericClient, error) {
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/rpc/GetUser", bytes.NewReader([]byte(`{"id":1}`))))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	if fake.method != "users/GetUser" {
		t.Fatalf("rpc method = %q", fake.method)
	}
}

func TestGatewayDescriptorDrivenTranscode(t *testing.T) {
	fake := &fakeGenericClient{payload: json.RawMessage(`{"message":"hello ada"}`)}
	desc := rpc.Descriptor{
		Name:    "examples.greeter.Greeter",
		Version: "v1",
		Methods: []rpc.MethodDescriptor{{
			Name:     "SayHello",
			Request:  "examples.greeter.HelloRequest",
			Response: "examples.greeter.HelloResponse",
		}},
	}
	g, err := New([]Route{{
		PathPrefix: "/gw/greeter",
		Targets:    []string{"http://upstream"},
		Transcode: TranscodeConfig{
			Enabled:          true,
			Descriptor:       "examples.greeter.Greeter",
			DescriptorMethod: "SayHello",
		},
	}}, WithDescriptors(desc), WithTranscoderFactory(func(endpoint string, route Route) (rpc.GenericClient, error) {
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/gw/greeter", bytes.NewReader([]byte(`{"name":"ada"}`))))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	if fake.method != "examples.greeter.Greeter/SayHello" {
		t.Fatalf("rpc method = %q", fake.method)
	}
	if string(fake.request) != `{"name":"ada"}` {
		t.Fatalf("rpc request = %s", fake.request)
	}
	descriptors := g.Descriptors()
	descriptors[desc.Name].Methods[0].Name = "Mutated"
	if !descriptorHasMethod(g.Descriptors()[desc.Name], "SayHello") {
		t.Fatal("descriptor registry returned mutable internal state")
	}
}

func TestGatewayDescriptorTranscodeMethodFromPath(t *testing.T) {
	fake := &fakeGenericClient{payload: json.RawMessage(`{}`)}
	desc := rpc.Descriptor{Name: "users.UserService", Methods: []rpc.MethodDescriptor{{Name: "GetUser"}}}
	g, err := New([]Route{{
		PathPrefix: "/rpc/users",
		Targets:    []string{"http://upstream"},
		Transcode:  TranscodeConfig{Enabled: true, Descriptor: "users.UserService"},
	}}, WithTranscoderFactory(func(endpoint string, route Route) (rpc.GenericClient, error) {
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterDescriptor(desc); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/rpc/users/GetUser", bytes.NewReader([]byte(`{"id":1}`))))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	if fake.method != "users.UserService/GetUser" {
		t.Fatalf("rpc method = %q", fake.method)
	}
}

func TestGatewayDescriptorTranscodeRejectsUnknownMethod(t *testing.T) {
	desc := rpc.Descriptor{Name: "users.UserService", Methods: []rpc.MethodDescriptor{{Name: "GetUser"}}}
	g, err := New([]Route{{
		PathPrefix: "/rpc/users",
		Targets:    []string{"http://upstream"},
		Transcode: TranscodeConfig{
			Enabled:          true,
			Descriptor:       "users.UserService",
			DescriptorMethod: "DeleteUser",
		},
	}}, WithDescriptors(desc), WithTranscoderFactory(func(endpoint string, route Route) (rpc.GenericClient, error) {
		return &fakeGenericClient{payload: json.RawMessage(`{}`)}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/rpc/users", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
}

func TestGatewayTranscodeMapsRPCError(t *testing.T) {
	fake := &fakeGenericClient{err: rpc.NewError(rpc.CodeNotFound, "missing")}
	g, err := New([]Route{{
		PathPrefix: "/api",
		Targets:    []string{"http://upstream"},
		Transcode:  TranscodeConfig{Enabled: true, Service: "users", Method: "Get"},
	}}, WithTranscoderFactory(func(endpoint string, route Route) (rpc.GenericClient, error) {
		return fake, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	g.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/x", nil))
	if rr.Code != coreerrors.HTTPStatus(rpc.CodeNotFound) {
		t.Fatalf("status = %d want %d", rr.Code, coreerrors.HTTPStatus(rpc.CodeNotFound))
	}
	if !strings.Contains(rr.Body.String(), "missing") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func TestMustNewPanicsOnError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustNew did not panic on invalid route")
		}
	}()
	MustNew([]Route{{}})
}

func TestMustNewSucceeds(t *testing.T) {
	g := MustNew([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if g == nil {
		t.Fatal("MustNew returned nil")
	}
}

func TestWithOptionsNilGuards(t *testing.T) {
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}

	// WithHTTPClient nil should not panic and not change client
	oldClient := g.client
	WithHTTPClient(nil)(g)
	if g.client != oldClient {
		t.Fatal("WithHTTPClient(nil) changed client")
	}

	// WithBalancer nil should not panic
	WithBalancer(nil)(g)

	// WithTimeout 0 should not change timeout
	oldTimeout := g.timeout
	WithTimeout(0)(g)
	if g.timeout != oldTimeout {
		t.Fatal("WithTimeout(0) changed timeout")
	}

	// WithMaxExpandedEndpoints 0 should not change
	oldLimit := g.maxExpandedEndpoint
	WithMaxExpandedEndpoints(0)(g)
	if g.maxExpandedEndpoint != oldLimit {
		t.Fatal("WithMaxExpandedEndpoints(0) changed limit")
	}

	// WithStats nil should not panic
	WithStats(nil)(g)

	// WithLogger nil should not panic
	WithLogger(nil)(g)
}

func TestWithOptionsApplyValues(t *testing.T) {
	customClient := &http.Client{Timeout: time.Second}
	stats := &Stats{}
	logger := slog.Default()

	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}},
		WithHTTPClient(customClient),
		WithTimeout(5*time.Second),
		WithMaxExpandedEndpoints(100),
		WithStats(stats),
		WithLogger(logger),
	)
	if err != nil {
		t.Fatal(err)
	}
	if g.client != customClient {
		t.Fatal("WithHTTPClient did not apply")
	}
	if g.timeout != 5*time.Second {
		t.Fatalf("timeout = %v, want 5s", g.timeout)
	}
	if g.maxExpandedEndpoint != 100 {
		t.Fatalf("maxExpandedEndpoint = %d, want 100", g.maxExpandedEndpoint)
	}
	if g.stats != stats {
		t.Fatal("WithStats did not apply")
	}
	if g.logger != logger {
		t.Fatal("WithLogger did not apply")
	}
}

func TestGatewayHandlerReturnsSelf(t *testing.T) {
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if g.Handler() != g {
		t.Fatal("Handler did not return self")
	}
}

func TestGatewayCloseAndShutdown(t *testing.T) {
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := g.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestGatewayCloseNilShadowPool(t *testing.T) {
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	g.shadowPool = nil
	if err := g.Close(); err != nil {
		t.Fatalf("Close nil shadowPool: %v", err)
	}
}

func TestGatewayNoShadowPoolWithoutShadowRoutes(t *testing.T) {
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if g.shadowPool != nil {
		t.Fatal("shadowPool = non-nil, want nil when no route defines shadow traffic")
	}
}

func TestSetRoutesBoundaries(t *testing.T) {
	var nilG *Gateway
	if err := nilG.SetRoutes([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}}); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("nil SetRoutes = %v, want ErrRouteRequired", err)
	}

	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.SetRoutes(nil); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("SetRoutes empty = %v, want ErrRouteRequired", err)
	}
	if err := g.SetRoutes([]Route{{Method: http.MethodGet, PathPrefix: "/new", Targets: []string{"http://127.0.0.1:1"}}}); err != nil {
		t.Fatalf("SetRoutes: %v", err)
	}
	routes := g.Routes()
	if len(routes) != 1 || routes[0].PathPrefix != "/new" {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestStatusRecorderFlushAndUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec}

	// Flush should not panic even if underlying does not implement Flusher
	sr.Flush()

	// Unwrap should return underlying writer
	if sr.Unwrap() != rec {
		t.Fatal("Unwrap did not return underlying ResponseWriter")
	}

	// WriteHeader twice should ignore second
	sr.WriteHeader(http.StatusOK)
	sr.WriteHeader(http.StatusNotFound)
	if sr.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", sr.status)
	}
}

func TestRetryAfterDelayBoundaries(t *testing.T) {
	if got := retryAfterDelay("", time.Minute); got != time.Minute {
		t.Fatalf("retryAfterDelay empty = %v, want 1m", got)
	}
	if got := retryAfterDelay("  ", time.Minute); got != time.Minute {
		t.Fatalf("retryAfterDelay whitespace = %v, want 1m", got)
	}
	if got := retryAfterDelay("invalid", time.Minute); got != time.Minute {
		t.Fatalf("retryAfterDelay invalid = %v, want 1m", got)
	}
	if got := retryAfterDelay("2", time.Minute); got != 2*time.Second {
		t.Fatalf("retryAfterDelay seconds = %v, want 2s", got)
	}
	if got := retryAfterDelay("-1", time.Minute); got != time.Minute {
		t.Fatalf("retryAfterDelay negative = %v, want 1m", got)
	}
	future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	if got := retryAfterDelay(future, time.Minute); got <= 0 || got > time.Hour {
		t.Fatalf("retryAfterDelay future = %v, want positive < 1h", got)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := retryAfterDelay(past, time.Minute); got != time.Minute {
		t.Fatalf("retryAfterDelay past = %v, want 1m", got)
	}
}

func TestWriteGatewayRouteErrorMapping(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{ErrRouteRequired, http.StatusBadRequest},
		{ErrRouteExists, http.StatusConflict},
		{ErrNoRoute, http.StatusNotFound},
		{errors.New("other"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		rec := httptest.NewRecorder()
		ctx := &rest.Context{Response: rec, Request: httptest.NewRequest(http.MethodGet, "/", nil)}
		writeGatewayRouteError(ctx, tc.err)
		if rec.Code != tc.status {
			t.Fatalf("writeGatewayRouteError(%v) status = %d, want %d", tc.err, rec.Code, tc.status)
		}
	}
}

func TestSanitizedGatewayRouteConfig(t *testing.T) {
	route := RouteConfig{
		Headers: map[string]string{"Authorization": "secret", "X-Trace": "abc"},
		Header: HeaderPolicy{
			SetRequest:  map[string]string{"Cookie": "session"},
			SetResponse: map[string]string{"X-Key": "val"},
		},
		Canary: []CanaryRoute{{Headers: map[string]string{"token": "t"}, MatchHeaders: map[string]string{"env": "prod"}}},
		Shadow: []ShadowRoute{{Headers: map[string]string{"Authorization": "s"}}},
	}
	sanitized := sanitizedGatewayRouteConfig(route)
	if sanitized.Headers["Authorization"] == "secret" {
		t.Fatal("sensitive header not masked")
	}
	if sanitized.Headers["X-Trace"] != "abc" {
		t.Fatal("non-sensitive header mutated")
	}
	if sanitized.Header.SetRequest["Cookie"] == "session" {
		t.Fatal("sensitive set-request header not masked")
	}
	if len(sanitized.Canary) != 1 || sanitized.Canary[0].Headers["token"] == "t" {
		t.Fatal("canary sensitive header not masked")
	}
	if len(sanitized.Shadow) != 1 || sanitized.Shadow[0].Headers["Authorization"] == "s" {
		t.Fatal("shadow sensitive header not masked")
	}
}

func TestSanitizedGatewayRouteConfigs(t *testing.T) {
	routes := []RouteConfig{{Headers: map[string]string{"Authorization": "secret"}}}
	out := sanitizedGatewayRouteConfigs(routes)
	if len(out) != 1 || out[0].Headers["Authorization"] == "secret" {
		t.Fatal("sensitive header not masked in batch")
	}
}

func TestDefaultTranscoderFactory(t *testing.T) {
	_, err := defaultTranscoderFactory("127.0.0.1:8080", Route{})
	if err != nil {
		t.Fatalf("defaultTranscoderFactory: %v", err)
	}
	_, err = defaultTranscoderFactory("http://127.0.0.1:8080", Route{})
	if err != nil {
		t.Fatalf("defaultTranscoderFactory with scheme: %v", err)
	}
}

func TestShadowPoolShutdownNil(t *testing.T) {
	var p *shadowPool
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown nil: %v", err)
	}
}

func TestGatewayHealthCheckNil(t *testing.T) {
	var g *Gateway
	if err := g.HealthCheck(context.Background()); err == nil {
		t.Fatal("HealthCheck nil: want error, got nil")
	}
}

func TestGatewayHealthCheckNoRoutes(t *testing.T) {
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	g.routes = nil
	if err := g.HealthCheck(context.Background()); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("HealthCheck no routes = %v, want ErrNoRoute", err)
	}
}

func TestReusableBodyNilBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Body = nil
	b, err := reusableBody(req)
	if err != nil {
		t.Fatalf("reusableBody nil: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("reusableBody nil = %q, want empty", b)
	}
}

func TestReusableBodyNoBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Body = http.NoBody
	b, err := reusableBody(req)
	if err != nil {
		t.Fatalf("reusableBody NoBody: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("reusableBody NoBody = %q, want empty", b)
	}
}

func TestDecodeGatewayRouteConfigBadJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{bad`))
	ctx := &rest.Context{Response: rec, Request: req}
	_, ok := decodeGatewayRouteConfig(ctx)
	if ok {
		t.Fatal("decodeGatewayRouteConfig bad json: want false")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDecodeGatewayRouteConfigSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"pathPrefix":"/api"}`))
	ctx := &rest.Context{Response: rec, Request: req}
	conf, ok := decodeGatewayRouteConfig(ctx)
	if !ok {
		t.Fatal("decodeGatewayRouteConfig valid: want true")
	}
	if conf.PathPrefix != "/api" {
		t.Fatalf("pathPrefix = %q, want /api", conf.PathPrefix)
	}
}

func TestGovernanceRuntimeKey(t *testing.T) {
	if got := governanceRuntimeKey(governance.Decision{RuleKey: "custom"}, "r1"); got != "custom" {
		t.Fatalf("governanceRuntimeKey ruleKey = %q, want custom", got)
	}
	if got := governanceRuntimeKey(governance.Decision{RuleName: "n"}, "r1"); got != "name:n" {
		t.Fatalf("governanceRuntimeKey ruleName = %q, want name:n", got)
	}
	if got := governanceRuntimeKey(governance.Decision{}, "r1"); got != "r1" {
		t.Fatalf("governanceRuntimeKey fallback = %q, want r1", got)
	}
}

func TestCloneDescriptorEmpty(t *testing.T) {
	got := cloneDescriptor(rpc.Descriptor{})
	if got.Metadata != nil {
		t.Fatalf("cloneDescriptor empty metadata = %v, want nil", got.Metadata)
	}
}

func TestGatewayMutationAndSnapshotCoverageBuffer(t *testing.T) {
	if got := (*Gateway)(nil).Snapshot(); len(got.Routes) != 0 || len(got.Discovery) != 0 {
		t.Fatalf("nil gateway snapshot = %+v, want empty", got)
	}
	if got := (*Gateway)(nil).Descriptors(); got != nil {
		t.Fatalf("nil gateway descriptors = %+v, want nil", got)
	}
	if err := (*Gateway)(nil).RegisterDescriptor(rpc.Descriptor{}); err == nil || !strings.Contains(err.Error(), "gateway is nil") {
		t.Fatalf("nil RegisterDescriptor error = %v", err)
	}
	if err := (*Gateway)(nil).AddRoute(Route{}); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("nil AddRoute error = %v, want ErrRouteRequired", err)
	}
	if err := (*Gateway)(nil).UpdateRoute(Route{}); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("nil UpdateRoute error = %v, want ErrRouteRequired", err)
	}
	if err := (*Gateway)(nil).UpsertRoute(Route{}); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("nil UpsertRoute error = %v, want ErrRouteRequired", err)
	}
	if (*Gateway)(nil).RemoveRoute(http.MethodGet, "/missing") {
		t.Fatal("nil RemoveRoute = true, want false")
	}
	if err := (*Gateway)(nil).SetRoutes([]Route{{PathPrefix: "/"}}); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("nil SetRoutes error = %v, want ErrRouteRequired", err)
	}

	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}}}, WithTimeout(25*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = g.Close() }()
	if got := g.snapshotResolveTimeout(); got != 25*time.Millisecond {
		t.Fatalf("snapshotResolveTimeout = %s, want route timeout", got)
	}
	if err := g.AddRoute(Route{Method: http.MethodGet, PathPrefix: "/api", Targets: []string{"http://127.0.0.1:2"}}); !errors.Is(err, ErrRouteExists) {
		t.Fatalf("duplicate AddRoute error = %v, want ErrRouteExists", err)
	}
	if err := g.UpdateRoute(Route{Method: http.MethodPost, PathPrefix: "/missing", Targets: []string{"http://127.0.0.1:2"}}); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("missing UpdateRoute error = %v, want ErrNoRoute", err)
	}
	if err := g.UpsertRoute(Route{Method: http.MethodPost, PathPrefix: "/api", Targets: []string{"http://127.0.0.1:2"}}); err != nil {
		t.Fatalf("UpsertRoute insert: %v", err)
	}
	if err := g.UpsertRoute(Route{Method: http.MethodPost, PathPrefix: "/api", Targets: []string{"http://127.0.0.1:3"}}); err != nil {
		t.Fatalf("UpsertRoute replace: %v", err)
	}
	if g.RemoveRoute(http.MethodDelete, "/missing") {
		t.Fatal("RemoveRoute missing = true, want false")
	}
	if err := g.SetRoutes(nil); !errors.Is(err, ErrRouteRequired) {
		t.Fatalf("SetRoutes empty error = %v, want ErrRouteRequired", err)
	}

	badResolver := rpc.ResolverFunc(func(context.Context) ([]string, error) { return nil, errors.New("resolve failed") })
	badSnapshot := g.resolverSnapshot(context.Background(), "route", "bad", "bad", "svc", map[string]string{"env": "test"}, badResolver)
	if badSnapshot.Error != "resolve failed" || badSnapshot.Tags["env"] != "test" {
		t.Fatalf("bad resolver snapshot = %+v, want error and cloned tags", badSnapshot)
	}
	nilSnapshot := g.resolverSnapshot(context.Background(), "route", "nil", "nil", "svc", nil, nil)
	if nilSnapshot.Error != "resolver is nil" {
		t.Fatalf("nil resolver snapshot = %+v, want resolver is nil", nilSnapshot)
	}
}

func TestAttachRouteResolversEmpty(t *testing.T) {
	g, err := New([]Route{{Method: http.MethodGet, PathPrefix: "/", Targets: []string{"http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	// attachRouteResolvers with empty targets should not panic
	g.attachRouteResolvers(Route{Targets: []string{}})
}

func TestSetForwardHeadersPropagatesTraceContext(t *testing.T) {
	sc := trace.SpanContext{TraceID: "abc12300000000000000000000000000", SpanID: "def4560000000000", Sampled: true}
	ctx := trace.NewContext(context.Background(), sc)

	original := httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)
	original = original.WithContext(ctx)
	original.RemoteAddr = "192.168.1.1:12345"

	out, err := cloneProxyRequest(original, &url.URL{Scheme: "http", Host: "upstream:8080"}, Route{}, nil)
	if err != nil {
		t.Fatalf("cloneProxyRequest: %v", err)
	}

	want := trace.TraceParent(sc)
	got := out.Header.Get(trace.TraceParentHeader)
	if got != want {
		t.Fatalf("traceparent header = %q, want %q", got, want)
	}
}

func TestSetForwardHeadersNoTraceContext(t *testing.T) {
	original := httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)
	original.RemoteAddr = "192.168.1.1:12345"

	out, err := cloneProxyRequest(original, &url.URL{Scheme: "http", Host: "upstream:8080"}, Route{}, nil)
	if err != nil {
		t.Fatalf("cloneProxyRequest: %v", err)
	}

	if out.Header.Get(trace.TraceParentHeader) != "" {
		t.Fatalf("traceparent header should be empty when no trace context")
	}
}

func dialGatewayWebSocket(t *testing.T, serverURL, path string) (net.Conn, *bufio.ReadWriter) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	key := base64.StdEncoding.EncodeToString([]byte("gofly-gateway-ws"))
	for _, line := range []string{
		"GET " + path + " HTTP/1.1\r\n",
		"Host: " + u.Host + "\r\n",
		"Upgrade: websocket\r\n",
		"Connection: Upgrade\r\n",
		"Sec-WebSocket-Version: 13\r\n",
		"Sec-WebSocket-Key: " + key + "\r\n\r\n",
	} {
		if _, err := rw.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}
	status, err := rw.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake status = %q, want switching protocols", status)
	}
	wantAccept := gatewayWebSocketAccept(key)
	foundAccept := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
		if strings.EqualFold(strings.TrimSpace(line), "Sec-WebSocket-Accept: "+wantAccept) {
			foundAccept = true
		}
	}
	if !foundAccept {
		t.Fatal("missing websocket accept header")
	}
	return conn, rw
}

func writeGatewayClientFrame(t *testing.T, rw *bufio.ReadWriter, messageType int, payload []byte) {
	t.Helper()
	if err := rw.WriteByte(0x80 | byte(messageType)); err != nil {
		t.Fatal(err)
	}
	mask := [4]byte{1, 2, 3, 4}
	length := len(payload)
	switch {
	case length < 126:
		if err := rw.WriteByte(0x80 | byte(length)); err != nil {
			t.Fatal(err)
		}
	case length <= 65535:
		if err := rw.WriteByte(0x80 | 126); err != nil {
			t.Fatal(err)
		}
		var buf [2]byte
		binary.BigEndian.PutUint16(buf[:], uint16(length))
		if _, err := rw.Write(buf[:]); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("test frame too large")
	}
	if _, err := rw.Write(mask[:]); err != nil {
		t.Fatal(err)
	}
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= mask[i%4]
	}
	if _, err := rw.Write(masked); err != nil {
		t.Fatal(err)
	}
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}
}

func readGatewayServerFrame(t *testing.T, rw *bufio.ReadWriter) (int, []byte) {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(rw, header); err != nil {
		t.Fatal(err)
	}
	messageType := int(header[0] & 0x0f)
	length := int(header[1] & 0x7f)
	if length == 126 {
		var buf [2]byte
		if _, err := io.ReadFull(rw, buf[:]); err != nil {
			t.Fatal(err)
		}
		length = int(binary.BigEndian.Uint16(buf[:]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(rw, payload); err != nil {
		t.Fatal(err)
	}
	return messageType, payload
}

func gatewayWebSocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}
