package gateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofly/gofly/core/auth"
	"github.com/gofly/gofly/core/discovery"
	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/core/observability/trace"
	"github.com/gofly/gofly/rest"
	"github.com/gofly/gofly/rpc"
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

type bitsUTBalancer struct{}

func (bitsUTBalancer) Pick(context.Context, []string) (string, error) { return "picked", nil }

type bitsUTDiscoveryResolver struct{}

func (bitsUTDiscoveryResolver) Resolve(context.Context, string, ...discovery.ResolveOption) ([]discovery.Instance, error) {
	return []discovery.Instance{{Endpoint: "http://127.0.0.1:1", Status: discovery.StatusHealthy}}, nil
}

func (bitsUTDiscoveryResolver) Watch(context.Context, string, ...discovery.ResolveOption) (<-chan discovery.Event, error) {
	return make(chan discovery.Event), nil
}

func TestGatewayOptionBoundaryBranches_BitsUT(t *testing.T) {
	routes := []Route{{PathPrefix: "/api", Targets: []string{"http://127.0.0.1:1"}}}
	g, err := New(routes,
		WithBalancer(nil),
		WithResolvers(nil),
		WithResolvers(map[string]rpc.Resolver{"": rpc.ResolverFunc(func(context.Context) ([]string, error) { return nil, nil }), "nil": nil}),
		WithDiscoveryResolvers(nil),
		WithDiscoveryResolvers(map[string]discovery.Resolver{"": bitsUTDiscoveryResolver{}, "nil": nil}),
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
		WithBalancer(bitsUTBalancer{}),
		WithResolvers(map[string]rpc.Resolver{"orders": resolver}),
		WithDiscoveryResolvers(map[string]discovery.Resolver{"catalog": bitsUTDiscoveryResolver{}}),
	)
	if err != nil {
		t.Fatalf("New gateway with resolvers returned error: %v", err)
	}
	secondGateway := g
	t.Cleanup(func() { _ = secondGateway.Close() })
	if _, ok := g.balancer.(bitsUTBalancer); !ok {
		t.Fatalf("gateway balancer = %T, want bitsUTBalancer", g.balancer)
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

func TestApplyGovernancePolicyBoundaries_BitsUT(t *testing.T) {
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

func TestGatewaySnapshotAndPrometheusBoundaries_BitsUT(t *testing.T) {
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

func TestGatewayWritePrometheusErrorBoundaries_BitsUT(t *testing.T) {
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

	deleteReq := httptest.NewRequest(http.MethodDelete, "/admin/gateway/routes?method=POST&pathPrefix=/hot", nil)
	deleteReq.Header.Set(auth.AuthorizationHeader, "Bearer secret")
	deleteRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(deleteRec, deleteReq)
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

func TestGatewayPureProxyAndTranscodeBranches_BitsUT(t *testing.T) {
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
