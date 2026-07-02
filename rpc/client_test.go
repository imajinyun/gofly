package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/auth"
	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/callstats"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/observability/trace"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

func TestHTTPClientCall(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/SayHello", helloRequest{Name: "client"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "hello client" {
		t.Fatalf("message = %q, want hello client", resp.Message)
	}
}

func TestHTTPClientRuntimeSnapshotIncludesCallPhaseStats(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Stats",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "stats"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/Stats", helloRequest{Name: "gofly"}, &resp); err != nil {
		t.Fatal(err)
	}
	snapshot := c.RuntimeSnapshot()
	for _, phase := range []string{callstats.PhaseGovernance, callstats.PhaseResolve, callstats.PhaseLoadBal, callstats.PhaseSend, callstats.PhaseRecv} {
		stats, ok := rpcPhaseStats(snapshot.Stats, phase)
		if !ok || stats.Calls == 0 {
			t.Fatalf("runtime stats = %#v, want phase %s calls", snapshot.Stats, phase)
		}
	}
}

func TestHTTPClientCallWithMetadata(t *testing.T) {
	s := NewServer(WithServerMiddleware(func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			ctx = metadata.Append(ctx, "server", "greeter")
			return next(ctx, req)
		}
	}))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	md, err := c.CallWithMetadata(context.Background(), "greeter/SayHello", helloRequest{Name: "client"}, &resp)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message != "hello client" {
		t.Fatalf("message = %q, want hello client", resp.Message)
	}
	if md.Get("server") != "greeter" {
		t.Fatalf("metadata server = %q, want greeter", md.Get("server"))
	}
}

func TestHTTPClientCallRaw(t *testing.T) {
	s := NewServer(WithServerMiddleware(func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			ctx = metadata.Append(ctx, "raw", "true")
			return next(ctx, req)
		}
	}))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	payload, md, err := c.CallRaw(context.Background(), "greeter/SayHello", helloRequest{Name: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "hello raw" {
		t.Fatalf("message = %q, want hello raw", resp.Message)
	}
	if md.Get("raw") != "true" {
		t.Fatalf("metadata raw = %q, want true", md.Get("raw"))
	}
}

func TestHTTPClientCallRawReturnsRPCError(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Missing",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return nil, NewError(CodeNotFound, "missing")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	payload, md, err := c.CallRaw(context.Background(), "greeter/Missing", helloRequest{})
	if payload != nil || md != nil {
		t.Fatalf("payload=%s metadata=%v, want nil results on error", string(payload), md)
	}
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeNotFound {
		t.Fatalf("error = %v, want not_found rpc error", err)
	}
}

func TestHTTPClientSingleflightDeduplicatesConcurrentCalls(t *testing.T) {
	var calls atomic.Int64
	gate := make(chan struct{})
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			<-gate
			return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL,
		WithClientSingleflightKey(func(ctx context.Context, method string, request any) (string, error) {
			return method + ":" + request.(helloRequest).Name, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 12
	var wg sync.WaitGroup
	responses := make(chan helloResponse, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var resp helloResponse
			if err := c.Call(context.Background(), "greeter/SayHello", helloRequest{Name: "shared"}, &resp); err != nil {
				t.Errorf("Call: %v", err)
				return
			}
			responses <- resp
		}()
	}
	time.Sleep(10 * time.Millisecond)
	close(gate)
	wg.Wait()
	close(responses)
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	for resp := range responses {
		if resp.Message != "hello shared" {
			t.Fatalf("message = %q, want hello shared", resp.Message)
		}
	}
}

func TestHTTPClientMaxConcurrencyRejectsLocalOverload(t *testing.T) {
	var calls atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Slow",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithRetry(1), WithClientMaxConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}

	firstErr := make(chan error, 1)
	go func() {
		var resp helloResponse
		firstErr <- c.Call(context.Background(), "greeter/Slow", helloRequest{Name: "first"}, &resp)
	}()
	<-entered

	var resp helloResponse
	err = c.Call(context.Background(), "greeter/Slow", helloRequest{Name: "second"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnavailable {
		t.Fatalf("second error = %v, want unavailable rpc error", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls = %d, want 1", calls.Load())
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first call: %v", err)
	}
}

func TestHTTPServerMaxConcurrencyRejectsOverload(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := NewServer(WithServerMaxConcurrency(1))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Slow",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithRetry(1))
	if err != nil {
		t.Fatal(err)
	}

	firstErr := make(chan error, 1)
	go func() {
		var resp helloResponse
		firstErr <- c.Call(context.Background(), "greeter/Slow", helloRequest{Name: "first"}, &resp)
	}()
	<-entered

	var resp helloResponse
	err = c.Call(context.Background(), "greeter/Slow", helloRequest{Name: "second"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnavailable {
		t.Fatalf("second error = %v, want unavailable rpc error", err)
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first call: %v", err)
	}
}

func TestHTTPClientReceivesRPCErrorCode(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Missing",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return nil, NewError(CodeNotFound, "missing")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithRetry(3))
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	err = c.Call(context.Background(), "greeter/Missing", helloRequest{Name: "client"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error = %T, want *Error", err)
	}
	if rpcErr.Code != CodeNotFound {
		t.Fatalf("code = %s, want %s", rpcErr.Code, CodeNotFound)
	}
}

func TestHTTPClientTraceMiddlewareSendsTraceMetadata(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	var got metadata.MD
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env requestEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Fatal(err)
		}
		got = env.Metadata
		_ = json.NewEncoder(w).Encode(responseEnvelope{Payload: helloResponse{Message: "ok"}, Code: CodeOK})
	}))
	defer ts.Close()

	c, err := NewClient(ts.URL, WithClientMiddleware(TraceMiddleware("greeter.client")))
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.Append(context.Background(), trace.TraceParentHeader, parent)
	var resp helloResponse
	if err := c.Call(ctx, "greeter/SayHello", helloRequest{Name: "client"}, &resp); err != nil {
		t.Fatal(err)
	}
	traceParent := got.Get(trace.TraceParentHeader)
	sc, ok := trace.ParseTraceParent(traceParent)
	if !ok {
		t.Fatalf("traceparent = %q should parse", traceParent)
	}
	if sc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || sc.SpanID == "00f067aa0ba902b7" {
		t.Fatalf("span = %#v, want child span", sc)
	}
}

func TestRoundRobinResolver(t *testing.T) {
	balancer := &RoundRobinBalancer{}
	endpoints := []string{"http://a", "http://b"}
	first, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	second, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("expected round-robin to pick different endpoints, got %q and %q", first, second)
	}
}

func TestHealthBalancerSkipsEjectedEndpoint(t *testing.T) {
	balancer := NewHealthBalancer(
		WithHealthFailureThreshold(1),
		WithHealthEjectionDuration(time.Hour),
	)
	endpoints := []string{"http://bad", "http://good"}
	first, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if first != "http://bad" {
		t.Fatalf("first endpoint = %q, want http://bad", first)
	}
	balancer.Report(context.Background(), "http://bad", NewError(CodeUnavailable, "down"))
	second, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if second != "http://good" {
		t.Fatalf("second endpoint = %q, want http://good", second)
	}
}

func TestWeightedRoundRobinBalancer(t *testing.T) {
	balancer := NewWeightedRoundRobinBalancer(map[string]int{"http://a": 2, "http://b": 1})
	endpoints := []string{"http://a", "http://b"}
	counts := map[string]int{}
	for i := 0; i < 6; i++ {
		endpoint, err := balancer.Pick(context.Background(), endpoints)
		if err != nil {
			t.Fatal(err)
		}
		counts[endpoint]++
	}
	if counts["http://a"] != 4 || counts["http://b"] != 2 {
		t.Fatalf("counts = %v, want a=4 b=2", counts)
	}
}

func TestP2CBalancerPicksLessLoadedEndpoint(t *testing.T) {
	balancer := NewP2CBalancer()
	endpoints := []string{"http://a", "http://b"}
	first, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	second, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("second endpoint = %q, want different endpoint from %q", second, first)
	}
	balancer.Report(context.Background(), first, nil)
	third, err := balancer.Pick(context.Background(), endpoints)
	if err != nil {
		t.Fatal(err)
	}
	if third != first {
		t.Fatalf("third endpoint = %q, want released endpoint %q", third, first)
	}
}

func TestHTTPClientHealthBalancerFailover(t *testing.T) {
	failCalls := 0
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failCalls++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"unavailable","error":"down"}`))
	}))
	defer failing.Close()

	success := NewServer()
	if err := success.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	successServer := httptest.NewServer(success)
	defer successServer.Close()

	client, err := NewClient(
		failing.URL,
		WithRetry(2),
		WithResolver(NewStaticResolver(failing.URL, successServer.URL)),
		WithBalancer(NewHealthBalancer(WithHealthFailureThreshold(1))),
	)
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	if err := client.Call(context.Background(), "greeter/SayHello", helloRequest{Name: "gofly"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "hello gofly" {
		t.Fatalf("message = %q, want hello gofly", resp.Message)
	}
	if failCalls != 1 {
		t.Fatalf("failCalls = %d, want 1", failCalls)
	}
}

func TestHTTPClientGovernanceRuleSetAppliesMetadataAndRetry(t *testing.T) {
	var calls atomic.Int64
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			if calls.Add(1) == 1 {
				return nil, NewError(CodeUnavailable, "try again")
			}
			md, _ := metadata.FromContext(ctx)
			return helloResponse{Message: md.Get("x-governance")}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	rules := governance.NewRuleSet(governance.Rule{
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: governance.Policy{
			Retry:    governance.RetryPolicy{Attempts: 2},
			Metadata: map[string]string{"x-governance": "on"},
		},
	})
	c, err := NewClient(ts.URL, WithRetry(1), WithClientRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/SayHello", helloRequest{Name: "gofly"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "on" || calls.Load() != 2 {
		t.Fatalf("message = %q calls = %d, want governance metadata and retry", resp.Message, calls.Load())
	}
	if stats := rules.Stats(); len(stats) != 1 || stats[0].Hits != 1 {
		t.Fatalf("stats = %#v, want one client rule hit", stats)
	}

	rules.Replace(governance.Rule{
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: governance.Policy{
			Metadata: map[string]string{"x-governance": "hot"},
		},
	})
	resp = helloResponse{}
	if err := c.Call(context.Background(), "greeter/SayHello", helloRequest{Name: "gofly"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "hot" {
		t.Fatalf("message = %q, want hot metadata after rule reload", resp.Message)
	}
}

func TestHTTPClientGovernanceRuleSetMatchesClientTags(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromContext(ctx)
			return helloResponse{Message: md.Get("x-client-rule")}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client-tagged",
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Tags:      map[string]string{"env": "prod", "rpc.service": "greeter", "rpc.method": "SayHello"},
		Policy: governance.Policy{
			Metadata: map[string]string{"x-client-rule": "tagged"},
		},
	})
	tags := map[string]string{"env": "prod"}
	c, err := NewClient(ts.URL, WithClientRuleSet(rules), WithClientGovernanceTags(tags))
	if err != nil {
		t.Fatal(err)
	}
	tags["env"] = "staging"

	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/SayHello", helloRequest{Name: "gofly"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "tagged" {
		t.Fatalf("message = %q, want client tag matched governance metadata", resp.Message)
	}
}

func TestHTTPClientGovernanceCanaryMatchesOutgoingMetadata(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromContext(ctx)
			return helloResponse{Message: md.Get(governance.HeaderCanary) + ":" + md.Get("x-lane")}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client-canary",
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: governance.Policy{
			Canary: governance.CanaryPolicy{
				MatchHeaders: map[string]string{"X-Tenant": "beta"},
				Headers:      map[string]string{"x-lane": "beta"},
			},
		},
	})
	c, err := NewClient(ts.URL, WithClientRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}

	ctx := metadata.Append(context.Background(), "x-tenant", "beta")
	var resp helloResponse
	if err := c.Call(ctx, "greeter/SayHello", helloRequest{Name: "gofly"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "true:beta" {
		t.Fatalf("message = %q, want canary metadata from outgoing RPC metadata", resp.Message)
	}
}

func TestHTTPClientGovernanceRuleRuntimeRateLimit(t *testing.T) {
	var calls atomic.Int64
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			return helloResponse{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client-rate",
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: governance.Policy{
			RateLimit: governance.RateLimitPolicy{Rate: 1, Burst: 1},
		},
	})
	c, err := NewClient(ts.URL, WithClientRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/SayHello", helloRequest{Name: "first"}, &resp); err != nil {
		t.Fatalf("first call: %v", err)
	}
	err = c.Call(context.Background(), "greeter/SayHello", helloRequest{Name: "second"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeResourceExhausted {
		t.Fatalf("second error = %v, want resource exhausted rpc error", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls = %d, want 1", calls.Load())
	}
}

func TestHTTPClientGovernanceRuleRuntimeConcurrencyLimit(t *testing.T) {
	var calls atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Slow",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return helloResponse{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client-concurrency",
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "Slow",
		Policy: governance.Policy{
			Concurrency: governance.ConcurrencyPolicy{Limit: 1},
		},
	})
	c, err := NewClient(ts.URL, WithClientRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}

	firstErr := make(chan error, 1)
	go func() {
		var resp helloResponse
		firstErr <- c.Call(context.Background(), "greeter/Slow", helloRequest{Name: "first"}, &resp)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("timed out waiting for first request to enter handler")
	}

	var resp helloResponse
	err = c.Call(context.Background(), "greeter/Slow", helloRequest{Name: "second"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnavailable {
		close(release)
		t.Fatalf("second error = %v, want unavailable rpc error", err)
	}
	if calls.Load() != 1 {
		close(release)
		t.Fatalf("server calls = %d, want 1", calls.Load())
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first call: %v", err)
	}
}

func TestHTTPClientGovernanceRuleRuntimeBreaker(t *testing.T) {
	var calls atomic.Int64
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Unstable",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			return nil, NewError(CodeInternal, "boom")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client-breaker",
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "Unstable",
		Policy: governance.Policy{
			Breaker: governance.BreakerPolicy{Enabled: true, MinRequests: 1, FailureRatio: 0.1, OpenTimeout: time.Second},
		},
	})
	c, err := NewClient(ts.URL, WithRetry(1), WithClientRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	for i := 1; i <= 2; i++ {
		err = c.Call(context.Background(), "greeter/Unstable", helloRequest{Name: "gofly"}, &resp)
		var rpcErr *Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInternal {
			t.Fatalf("call %d error = %v, want internal rpc error", i, err)
		}
	}
	err = c.Call(context.Background(), "greeter/Unstable", helloRequest{Name: "gofly"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnavailable {
		t.Fatalf("third error = %v, want unavailable rpc error", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls = %d, want 2 before breaker opens", calls.Load())
	}
}

func TestHTTPClientGovernanceRuleRuntimeTimeout(t *testing.T) {
	started := make(chan struct{})
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Slow",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "client-timeout",
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "Slow",
		Policy: governance.Policy{
			Timeout: 20 * time.Millisecond,
		},
	})
	c, err := NewClient(ts.URL, WithTimeout(time.Second), WithClientRuleSet(rules))
	if err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	err = c.Call(context.Background(), "greeter/Slow", helloRequest{Name: "gofly"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeDeadlineExceeded {
		t.Fatalf("error = %v, want deadline exceeded rpc error", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler to start")
	}
}

func TestHTTPClientRPCPolicyRuntimeTimeout(t *testing.T) {
	started := make(chan struct{})
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SlowPolicy",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL,
		WithTimeout(time.Second),
		WithRPCPolicy(RPCPolicy{Timeout: 20 * time.Millisecond}),
	)
	if err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	err = c.Call(context.Background(), "greeter/SlowPolicy", helloRequest{Name: "gofly"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeDeadlineExceeded {
		t.Fatalf("error = %v, want deadline exceeded rpc error", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler to start")
	}
}

func TestHTTPClientRPCPolicyRuntimeRetryAndCancel(t *testing.T) {
	t.Run("policy retry overrides client default", func(t *testing.T) {
		var calls atomic.Int64
		s := NewServer()
		if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
			Name:       "RetryPolicy",
			NewRequest: func() any { return new(helloRequest) },
			Handler: func(ctx context.Context, req any) (any, error) {
				if calls.Add(1) == 1 {
					return nil, NewError(CodeUnavailable, "try again")
				}
				return helloResponse{Message: "ok"}, nil
			},
		}}}, nil); err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(s)
		defer ts.Close()
		c, err := NewClient(ts.URL,
			WithRetry(1),
			WithRPCPolicy(RPCPolicy{Retry: governance.RetryPolicy{Attempts: 2, Backoff: time.Millisecond}}),
		)
		if err != nil {
			t.Fatal(err)
		}

		var resp helloResponse
		if err := c.Call(context.Background(), "greeter/RetryPolicy", helloRequest{Name: "gofly"}, &resp); err != nil {
			t.Fatalf("Call: %v", err)
		}
		if resp.Message != "ok" || calls.Load() != 2 {
			t.Fatalf("response/calls = %#v/%d, want retry success after two attempts", resp, calls.Load())
		}
	})

	t.Run("method policy retry overrides global policy", func(t *testing.T) {
		var calls atomic.Int64
		s := NewServer()
		if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
			Name:       "MethodRetryPolicy",
			NewRequest: func() any { return new(helloRequest) },
			Handler: func(ctx context.Context, req any) (any, error) {
				if calls.Add(1) == 1 {
					return nil, NewError(CodeUnavailable, "try again")
				}
				return helloResponse{Message: "method ok"}, nil
			},
		}}}, nil); err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(s)
		defer ts.Close()
		c, err := NewClient(ts.URL,
			WithRPCPolicy(RPCPolicy{
				Retry: governance.RetryPolicy{Attempts: 1, Backoff: time.Millisecond},
				Methods: map[string]RPCPolicy{
					"greeter/MethodRetryPolicy": {
						Retry: governance.RetryPolicy{Attempts: 2, Backoff: time.Millisecond},
					},
				},
			}),
		)
		if err != nil {
			t.Fatal(err)
		}

		var resp helloResponse
		if err := c.Call(context.Background(), "/greeter/MethodRetryPolicy", helloRequest{Name: "gofly"}, &resp); err != nil {
			t.Fatalf("Call: %v", err)
		}
		if resp.Message != "method ok" || calls.Load() != 2 {
			t.Fatalf("response/calls = %#v/%d, want method policy retry success after two attempts", resp, calls.Load())
		}
	})

	t.Run("retry backoff observes context cancellation", func(t *testing.T) {
		var calls atomic.Int64
		s := NewServer()
		if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
			Name:       "CancelRetryPolicy",
			NewRequest: func() any { return new(helloRequest) },
			Handler: func(ctx context.Context, req any) (any, error) {
				calls.Add(1)
				return nil, NewError(CodeUnavailable, "try again")
			},
		}}}, nil); err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(s)
		defer ts.Close()
		c, err := NewClient(ts.URL,
			WithRPCPolicy(RPCPolicy{Retry: governance.RetryPolicy{Attempts: 3, Backoff: 100 * time.Millisecond}}),
		)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		time.AfterFunc(10*time.Millisecond, cancel)
		var resp helloResponse
		err = c.Call(ctx, "greeter/CancelRetryPolicy", helloRequest{Name: "gofly"}, &resp)
		var rpcErr *Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != CodeCanceled {
			t.Fatalf("error = %v, want canceled rpc error", err)
		}
		if calls.Load() != 1 {
			t.Fatalf("server calls = %d, want no retry after context cancellation", calls.Load())
		}
	})
}

func TestHTTPClientRPCPolicyRuntimeBreaker(t *testing.T) {
	var calls atomic.Int64
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "BreakerPolicy",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			return nil, NewError(CodeInternal, "boom")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL,
		WithRetry(1),
		WithRPCPolicy(RPCPolicy{Breaker: governance.BreakerPolicy{Enabled: true, MinRequests: 1, FailureRatio: 0.1, OpenTimeout: time.Second}}),
	)
	if err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	for i := 1; i <= 2; i++ {
		err = c.Call(context.Background(), "greeter/BreakerPolicy", helloRequest{Name: "gofly"}, &resp)
		var rpcErr *Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInternal {
			t.Fatalf("call %d error = %v, want internal rpc error", i, err)
		}
	}
	err = c.Call(context.Background(), "greeter/BreakerPolicy", helloRequest{Name: "gofly"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnavailable {
		t.Fatalf("third error = %v, want unavailable rpc error", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("server calls = %d, want 2 before breaker opens", calls.Load())
	}
}

func TestHTTPClientRPCPolicyRuntimeWeightedBalancer(t *testing.T) {
	serverA := NewServer()
	if err := serverA.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "BalancePolicy",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "a"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	tsA := httptest.NewServer(serverA)
	defer tsA.Close()

	serverB := NewServer()
	if err := serverB.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "BalancePolicy",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: "b"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	tsB := httptest.NewServer(serverB)
	defer tsB.Close()

	c, err := NewClient(tsA.URL,
		WithResolver(NewStaticResolver(tsA.URL, tsB.URL)),
		WithRPCPolicy(RPCPolicy{Balancer: RPCBalancerPolicy{
			Name:    RPCBalancerWeightedRoundRobin,
			Weights: map[string]int{tsA.URL: 1, tsB.URL: 3},
		}}),
	)
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	for i := 0; i < 4; i++ {
		var resp helloResponse
		if err := c.Call(context.Background(), "greeter/BalancePolicy", helloRequest{Name: "gofly"}, &resp); err != nil {
			t.Fatalf("Call %d: %v", i, err)
		}
		counts[resp.Message]++
	}
	if counts["a"] != 1 || counts["b"] != 3 {
		t.Fatalf("counts = %v, want weighted policy a=1 b=3", counts)
	}
}

func TestHTTPClientRPCPolicyRuntimeLoadShedder(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "LoadShedPolicy",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			select {
			case <-release:
				return helloResponse{Message: "ok"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL,
		WithRPCPolicy(RPCPolicy{LoadShedder: RPCLoadShedderPolicy{Enabled: true, MaxConcurrency: 1}}),
	)
	if err != nil {
		t.Fatal(err)
	}

	firstErr := make(chan error, 1)
	go func() {
		var resp helloResponse
		firstErr <- c.Call(context.Background(), "greeter/LoadShedPolicy", helloRequest{Name: "first"}, &resp)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first call")
	}
	var resp helloResponse
	err = c.Call(context.Background(), "greeter/LoadShedPolicy", helloRequest{Name: "second"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeResourceExhausted {
		t.Fatalf("second error = %v, want resource exhausted load shedding error", err)
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first call: %v", err)
	}
	state := c.PolicyRuntimeSnapshot().State
	if !state.LoadShedderEnabled || state.LoadShedderLimit != 1 || state.LoadShedderMode != "static_concurrency" {
		t.Fatalf("load shedder state = %+v, want static concurrency limit", state)
	}
}

func TestHTTPClientRPCPolicyRuntimeLoadShedderReportsMinWindow(t *testing.T) {
	c, err := NewClient("http://a", WithRPCPolicy(RPCPolicy{LoadShedder: RPCLoadShedderPolicy{
		Enabled:        true,
		MaxConcurrency: 3,
		MinWindow:      250 * time.Millisecond,
	}}))
	if err != nil {
		t.Fatal(err)
	}
	state := c.PolicyRuntimeSnapshot().State
	if state.LoadShedderMode != "static_concurrency" || state.LoadShedderWindow != 250*time.Millisecond {
		t.Fatalf("load shedder state = %+v, want mode and min window", state)
	}
}

func TestHTTPClientRPCPolicyRuntimeFallback(t *testing.T) {
	var fallbackCalls atomic.Int64
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"unavailable","error":"down"}`))
	}))
	defer failing.Close()

	fallback := NewServer()
	if err := fallback.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "FallbackPolicy",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			fallbackCalls.Add(1)
			return helloResponse{Message: "fallback"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	fallbackServer := httptest.NewServer(fallback)
	defer fallbackServer.Close()
	c, err := NewClient(failing.URL,
		WithRetry(1),
		WithRPCPolicy(RPCPolicy{Fallback: RPCFallbackPolicy{Enabled: true, Target: fallbackServer.URL}}),
	)
	if err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/FallbackPolicy", helloRequest{Name: "gofly"}, &resp); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Message != "fallback" || fallbackCalls.Load() != 1 {
		t.Fatalf("fallback response/calls = %#v/%d, want fallback success", resp, fallbackCalls.Load())
	}
	stats, ok := rpcPhaseStats(c.RuntimeSnapshot().Stats, callstats.PhaseFallback)
	if !ok || stats.Calls != 1 || stats.Errors != 0 {
		t.Fatalf("fallback stats = %#v, want one successful fallback", c.RuntimeSnapshot().Stats)
	}
}

func TestHTTPClientRPCPolicyRuntimeHedge(t *testing.T) {
	var calls atomic.Int64
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "HedgePolicy",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			if calls.Add(1) == 1 {
				select {
				case <-time.After(100 * time.Millisecond):
					return helloResponse{Message: "primary"}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return helloResponse{Message: "hedge"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL,
		WithRPCPolicy(RPCPolicy{Hedge: RPCHedgePolicy{Enabled: true, Delay: 5 * time.Millisecond, Attempts: 2}}),
	)
	if err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/HedgePolicy", helloRequest{Name: "gofly"}, &resp); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Message != "hedge" || calls.Load() < 2 {
		t.Fatalf("hedge response/calls = %#v/%d, want hedged response", resp, calls.Load())
	}
}

func TestHTTPClientRPCPolicyRuntimeSnapshotContributor(t *testing.T) {
	policy := RPCPolicy{
		Timeout: 50 * time.Millisecond,
		Retry:   governance.RetryPolicy{Attempts: 3, Backoff: time.Millisecond},
		Breaker: governance.BreakerPolicy{Enabled: true, MinRequests: 1, FailureRatio: 0.1, OpenTimeout: time.Second},
		Balancer: RPCBalancerPolicy{
			Name:    RPCBalancerWeightedRoundRobin,
			Weights: map[string]int{"http://a": 1, "http://b": 2},
		},
		LoadShedder: RPCLoadShedderPolicy{Enabled: true, MaxConcurrency: 2},
		Fallback:    RPCFallbackPolicy{Enabled: true, Target: "http://fallback"},
		Hedge:       RPCHedgePolicy{Enabled: true, Delay: time.Millisecond, Attempts: 2},
		Methods: map[string]RPCPolicy{
			"greeter/Fast": {Timeout: 10 * time.Millisecond},
			"Slow":         {Retry: governance.RetryPolicy{Attempts: 2}},
		},
	}
	c, err := NewClient("http://a", WithRPCPolicy(policy), WithResolver(NewStaticResolver("http://a", "http://b")))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.ruleBalancer("unit", policy.Balancer)
	_ = c.ruleLoadShedderLimiter("unit", policy.LoadShedder)
	_ = c.ruleBreaker("unit", policy.Breaker)

	runtimeSnapshot := c.PolicyRuntimeSnapshot()
	if !runtimeSnapshot.State.TimeoutEnforced || runtimeSnapshot.State.EffectiveTimeout != 50*time.Millisecond {
		t.Fatalf("timeout state = %+v, want enforced 50ms", runtimeSnapshot.State)
	}
	if runtimeSnapshot.State.RetryAttempts != 3 || runtimeSnapshot.State.RetryBackoff != time.Millisecond {
		t.Fatalf("retry state = %+v, want attempts/backoff from policy", runtimeSnapshot.State)
	}
	if !runtimeSnapshot.State.BreakerEnabled || runtimeSnapshot.State.Balancer != RPCBalancerWeightedRoundRobin || !runtimeSnapshot.State.LoadShedderEnabled || runtimeSnapshot.State.LoadShedderLimit != 2 || runtimeSnapshot.State.LoadShedderMode != "static_concurrency" {
		t.Fatalf("runtime state = %+v, want breaker, balancer and load shedder", runtimeSnapshot.State)
	}
	if !runtimeSnapshot.State.FallbackEnabled || runtimeSnapshot.State.FallbackTarget != "http://fallback" || !runtimeSnapshot.State.HedgeEnabled || runtimeSnapshot.State.HedgeAttempts != 2 {
		t.Fatalf("fallback/hedge state = %+v, want enabled", runtimeSnapshot.State)
	}
	if runtimeSnapshot.Cache.Balancers != 1 || runtimeSnapshot.Cache.Breakers != 1 || runtimeSnapshot.Cache.ConcurrencyLimiters != 1 {
		t.Fatalf("runtime cache = %+v, want balancer, breaker and load shedder limiter", runtimeSnapshot.Cache)
	}
	if runtimeSnapshot.MethodPolicyCount != 2 || !slices.Equal(runtimeSnapshot.MethodPolicyKeys, []string{"Slow", "greeter/Fast"}) {
		t.Fatalf("method policy snapshot = count %d keys %#v, want sorted method policy keys", runtimeSnapshot.MethodPolicyCount, runtimeSnapshot.MethodPolicyKeys)
	}
	if !slices.Equal(runtimeSnapshot.Priority, []string{"client_default", "governance_rule", "dynamic_policy", "method_policy"}) {
		t.Fatalf("priority = %#v, want documented policy precedence", runtimeSnapshot.Priority)
	}

	provider := controlplane.CompositeProvider{Contributors: []controlplane.SnapshotContributor{
		RPCPolicyRuntimeContributor{Name: "primary", Client: c},
	}}
	snapshot, err := provider.Load(context.Background())
	if err != nil {
		t.Fatalf("Load controlplane snapshot: %v", err)
	}
	if snapshot.Metadata["rpc.policy.runtime"] != "available" || snapshot.Metadata["rpc.policy.runtime.enforcement"] == "" {
		t.Fatalf("snapshot metadata = %+v, want rpc policy runtime metadata", snapshot.Metadata)
	}
	raw := snapshot.Configs["rpc.policy.runtime.primary"]
	if !json.Valid(raw) {
		t.Fatalf("rpc runtime config is not valid JSON: %s", raw)
	}
	var decoded RPCPolicyRuntimeSnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode rpc runtime config: %v", err)
	}
	if decoded.State.Balancer != RPCBalancerWeightedRoundRobin || !decoded.State.ExplicitPolicyBound || !slices.Contains(decoded.Capabilities, "dynamic_policy") || !slices.Contains(decoded.Capabilities, "method_policy") || !slices.Contains(decoded.Capabilities, "kitex_interceptor") {
		t.Fatalf("decoded runtime snapshot = %+v, want policy runtime capability state", decoded)
	}
}

func TestHTTPClientEffectivePolicySnapshotExplainsMethodPriority(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{
		Name:      "orders-governance",
		Transport: governance.TransportRPC,
		Service:   "greeter",
		Method:    "Priority",
		Policy: governance.Policy{
			Retry:   governance.RetryPolicy{Attempts: 2, Backoff: time.Millisecond},
			Headers: map[string]string{"X-Source": "governance"},
		},
	})
	c, err := NewClient("http://a",
		WithClientGovernanceRuleSet(rules),
		WithDynamicRPCPolicy(RPCPolicyProviderFunc(func(context.Context, governance.Request) (RPCPolicy, error) {
			return RPCPolicy{
				Retry:   governance.RetryPolicy{Attempts: 3, Backoff: 2 * time.Millisecond},
				Headers: map[string]string{"X-Dynamic": "true"},
			}, nil
		})),
		WithRPCPolicy(RPCPolicy{
			Retry: governance.RetryPolicy{Attempts: 1, Backoff: time.Second},
			Methods: map[string]RPCPolicy{
				"greeter/Priority": {
					Retry:    governance.RetryPolicy{Attempts: 4, Backoff: 3 * time.Millisecond},
					Fallback: RPCFallbackPolicy{Enabled: true, Target: "http://fallback"},
				},
			},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := c.EffectivePolicySnapshot(context.Background(), "/greeter/Priority")
	if snapshot.Method != "greeter/Priority" || snapshot.MethodKey != "greeter/Priority" || snapshot.GovernanceRule != "orders-governance" {
		t.Fatalf("effective policy identity = %+v, want method key and governance rule", snapshot)
	}
	if snapshot.State.RetryAttempts != 4 || snapshot.State.RetryBackoff != 3*time.Millisecond {
		t.Fatalf("effective retry = %+v, want method override over dynamic/governance/default", snapshot.State)
	}
	if !snapshot.State.FallbackEnabled || snapshot.State.FallbackTarget != "http://fallback" {
		t.Fatalf("effective fallback = %+v, want method fallback", snapshot.State)
	}
	if snapshot.Policy.Headers["X-Source"] != "governance" || snapshot.Policy.Headers["X-Dynamic"] != "true" {
		t.Fatalf("effective headers = %#v, want merged governance and dynamic headers", snapshot.Policy.Headers)
	}
	if !slices.Equal(snapshot.Priority, []string{"client_default", "governance_rule", "dynamic_policy", "method_policy"}) {
		t.Fatalf("priority = %#v, want documented policy precedence", snapshot.Priority)
	}
}

func TestHTTPClientDynamicRPCPolicyProviderOverridesRuntime(t *testing.T) {
	started := make(chan struct{})
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "DynamicPolicy",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()

	var providerCalls atomic.Int64
	c, err := NewClient(ts.URL,
		WithTimeout(time.Second),
		WithRPCPolicy(RPCPolicy{Timeout: 500 * time.Millisecond}),
		WithDynamicRPCPolicy(RPCPolicyProviderFunc(func(ctx context.Context, req governance.Request) (RPCPolicy, error) {
			providerCalls.Add(1)
			if req.Service != "greeter" || req.Method != "DynamicPolicy" {
				t.Fatalf("governance request = %+v, want greeter/DynamicPolicy", req)
			}
			return RPCPolicy{Timeout: 20 * time.Millisecond}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	var resp helloResponse
	err = c.Call(context.Background(), "greeter/DynamicPolicy", helloRequest{Name: "gofly"}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeDeadlineExceeded {
		t.Fatalf("error = %v, want deadline exceeded from dynamic policy", err)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("dynamic provider calls = %d, want 1", providerCalls.Load())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler to start")
	}
	snapshot := c.PolicyRuntimeSnapshot()
	if !snapshot.State.DynamicPolicyBound || !snapshot.State.ExplicitPolicyBound {
		t.Fatalf("runtime state = %+v, want dynamic and explicit policy bound", snapshot.State)
	}
}

func TestHTTPClientRuntimeSnapshotAndResolverWatchCloseIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := make(chan []string, 2)
	resolver := hardeningWatchResolver{
		endpoints: []string{"http://a", "http://b"},
		updates:   updates,
	}
	c, err := NewClient("http://a",
		WithResolver(resolver),
		WithClientMiddleware(func(next endpoint.Endpoint) endpoint.Endpoint { return next }),
		WithClientStreamMiddleware(func(next ClientStreamHandler) ClientStreamHandler { return next }),
		WithRPCPolicy(RPCPolicy{Balancer: RPCBalancerPolicy{Name: RPCBalancerP2C}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	initial := c.RuntimeSnapshot()
	if initial.Target != "http://a" || initial.Codec != "json" || !initial.Resolver.Watch || !initial.Discovery.WatchEnabled {
		t.Fatalf("initial runtime snapshot = %+v, want target, codec and watch enabled", initial)
	}
	if initial.Middlewares.Unary != 1 || initial.Middlewares.Stream != 1 || initial.Policy.State.Balancer != RPCBalancerP2C {
		t.Fatalf("initial middleware/policy snapshot = %+v, want middleware counts and p2c policy", initial)
	}

	updates <- []string{"http://b", "http://c"}
	if !waitForRPCSnapshot(t, time.Second, func() bool {
		snapshot := c.RuntimeSnapshot()
		return snapshot.Discovery.CloseIdleCalls == 1 &&
			len(snapshot.Discovery.Removed) == 1 &&
			snapshot.Discovery.Removed[0] == "http://a"
	}) {
		t.Fatalf("runtime snapshot after resolver update = %+v, want removed http://a and idle close", c.RuntimeSnapshot())
	}

	provider := controlplane.CompositeProvider{Contributors: []controlplane.SnapshotContributor{
		RPCRuntimeContributor{Name: "primary", Client: c},
	}}
	snapshot, err := provider.Load(ctx)
	if err != nil {
		t.Fatalf("Load controlplane snapshot: %v", err)
	}
	if snapshot.Metadata["rpc.runtime"] != "available" {
		t.Fatalf("snapshot metadata = %+v, want rpc runtime metadata", snapshot.Metadata)
	}
	if raw := snapshot.Configs["rpc.runtime.primary"]; !json.Valid(raw) {
		t.Fatalf("rpc runtime config is not valid JSON: %s", raw)
	}
}

func TestHTTPClientRuntimeSnapshotRecordsDiscoveryEvents(t *testing.T) {
	registry := discovery.NewMemoryRegistry()
	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "orders", ID: "a", Endpoint: "http://a", Weight: 1}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	var removed []string
	var mu sync.Mutex
	manager := NewConnPoolManager(func(context.Context, string) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return client, nil
	}, ConnPoolConfig{MaxIdle: 1, MaxActive: 1, OnClose: func(endpoint string, reason string, _ ConnPoolStats) {
		mu.Lock()
		defer mu.Unlock()
		removed = append(removed, endpoint+":"+reason)
	}})
	conn, err := manager.Get(context.Background(), "http://a")
	if err != nil {
		t.Fatalf("pool Get: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("pool Close: %v", err)
	}
	c, err := NewClient("http://a", WithResolver(NewDiscoveryResolver(registry, "orders")), WithConnPoolManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "orders", ID: "b", Endpoint: "http://b", Weight: 1}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if !waitForRPCSnapshot(t, time.Second, func() bool {
		snapshot := c.RuntimeSnapshot()
		return slices.Equal(snapshot.Discovery.Added, []string{"http://b"}) &&
			slices.Contains(snapshot.Discovery.Endpoints, "http://a") &&
			slices.Contains(snapshot.Discovery.Endpoints, "http://b")
	}) {
		t.Fatalf("runtime snapshot after add = %+v, want added http://b", c.RuntimeSnapshot())
	}

	if _, err := registry.Register(context.Background(), discovery.Instance{Service: "orders", ID: "b", Endpoint: "http://b", Weight: 2}); err != nil {
		t.Fatalf("update b: %v", err)
	}
	if !waitForRPCSnapshot(t, time.Second, func() bool {
		return slices.Equal(c.RuntimeSnapshot().Discovery.Updated, []string{"http://b"})
	}) {
		t.Fatalf("runtime snapshot after update = %+v, want updated http://b", c.RuntimeSnapshot())
	}

	if err := registry.Deregister(context.Background(), discovery.Instance{Service: "orders", ID: "a", Endpoint: "http://a"}); err != nil {
		t.Fatalf("deregister a: %v", err)
	}
	if !waitForRPCSnapshot(t, time.Second, func() bool {
		snapshot := c.RuntimeSnapshot()
		return slices.Equal(snapshot.Discovery.Removed, []string{"http://a"}) && snapshot.Discovery.CloseIdleCalls > 0
	}) {
		t.Fatalf("runtime snapshot after remove = %+v, want removed http://a and close idle", c.RuntimeSnapshot())
	}
	mu.Lock()
	gotRemoved := append([]string(nil), removed...)
	mu.Unlock()
	if len(gotRemoved) != 1 || gotRemoved[0] != "http://a:endpoint_removed" {
		t.Fatalf("connpool removed callbacks = %#v, want endpoint_removed", gotRemoved)
	}
	if snapshot := c.RuntimeSnapshot(); slices.ContainsFunc(snapshot.ConnPool.Endpoints, func(stats ConnPoolStats) bool {
		return stats.Endpoint == "http://a"
	}) {
		t.Fatalf("connpool snapshot = %+v, want http://a removed", snapshot.ConnPool)
	}
}

func TestHTTPClientWarmupResolvesBalancerAndConnPool(t *testing.T) {
	var warmed []string
	var mu sync.Mutex
	manager := NewConnPoolManager(func(_ context.Context, endpoint string) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		mu.Lock()
		warmed = append(warmed, endpoint)
		mu.Unlock()
		return client, nil
	}, ConnPoolConfig{MaxIdle: 1, MaxActive: 1})

	c, err := NewClient("http://a",
		WithResolver(NewStaticResolver("http://a", "http://b")),
		WithConnPoolManager(manager),
		WithClientWarmup(RPCWarmupConfig{ConnPool: true, MaxEndpoints: 2, Timeout: time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	snapshot := c.RuntimeSnapshot()
	if !snapshot.Warmup.Enabled || !snapshot.Warmup.Attempted || !snapshot.Warmup.Completed || snapshot.Warmup.ConnPoolWarmed != 2 {
		t.Fatalf("warmup snapshot = %#v, want completed connpool warmup for two endpoints", snapshot.Warmup)
	}
	if !slices.Contains(snapshot.Warmup.Endpoints, "http://a") || !slices.Contains(snapshot.Warmup.Endpoints, "http://b") || snapshot.Warmup.Selected == "" {
		t.Fatalf("warmup endpoints = %#v selected=%q, want resolved endpoints and selected target", snapshot.Warmup.Endpoints, snapshot.Warmup.Selected)
	}
	if len(snapshot.ConnPool.Endpoints) != 2 {
		t.Fatalf("connpool snapshot = %#v, want two warmed endpoint pools", snapshot.ConnPool)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(warmed, "http://a") || !slices.Contains(warmed, "http://b") {
		t.Fatalf("warmed endpoints = %#v, want both endpoints dialed", warmed)
	}
}

func TestHTTPClientWarmupFailsFastOnResolverError(t *testing.T) {
	_, err := NewClient("http://a",
		WithResolver(ResolverFunc(func(context.Context) ([]string, error) {
			return nil, errors.New("resolver down")
		})),
		WithClientWarmup(RPCWarmupConfig{Timeout: time.Second}),
	)
	if err == nil || !strings.Contains(err.Error(), "warm up rpc resolver") {
		t.Fatalf("NewClient warmup error = %v, want resolver warmup error", err)
	}
}

func waitForRPCSnapshot(t *testing.T, timeout time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return fn()
}

func TestRegistryResolver(t *testing.T) {
	registry := NewRegistry()
	registry.Register("greeter", "http://127.0.0.1:8081/")
	registry.Register("greeter", "http://127.0.0.1:8082")
	endpoints, err := registry.Resolver("greeter").Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("endpoints len = %d, want 2", len(endpoints))
	}
	registry.Deregister("greeter", "http://127.0.0.1:8081")
	endpoints, err = registry.ResolveService(context.Background(), "greeter")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0] != "http://127.0.0.1:8082" {
		t.Fatalf("endpoints = %v, want only http://127.0.0.1:8082", endpoints)
	}
}

func TestRegistryResolveInstances(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterInstance(context.Background(), "greeter", ServiceInstance{
		Endpoint: "http://127.0.0.1:8081/",
		Weight:   10,
		Version:  "v1",
		Zone:     "a",
		Status:   "healthy",
		Tags:     map[string]string{"role": "primary"},
		Metadata: map[string]string{"owner": "platform"},
	}); err != nil {
		t.Fatal(err)
	}
	instances, err := registry.ResolveInstances(context.Background(), "greeter")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Endpoint != "http://127.0.0.1:8081" || instances[0].Weight != 10 || instances[0].Version != "v1" || instances[0].Zone != "a" || instances[0].Status != "healthy" || instances[0].Tags["role"] != "primary" || instances[0].Metadata["owner"] != "platform" {
		t.Fatalf("instances = %#v, want registered instance metadata", instances)
	}
	instances[0].Tags["role"] = "mutated"
	instances[0].Metadata["owner"] = "mutated"
	again, err := registry.ResolveInstances(context.Background(), "greeter")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Tags["role"] != "primary" || again[0].Metadata["owner"] != "platform" {
		t.Fatalf("registry returned mutable metadata: %#v", again[0])
	}
	discoverySnapshot := registry.Discovery().Snapshot()
	if len(discoverySnapshot["greeter"]) != 1 || discoverySnapshot["greeter"][0].Version != "v1" {
		t.Fatalf("discovery snapshot = %#v, want bridged greeter v1", discoverySnapshot)
	}
}

func TestKubernetesResolver(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/endpoints/greeter" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"subsets": []any{map[string]any{
				"addresses": []any{map[string]any{"ip": "10.0.0.1", "targetRef": map[string]any{"labels": map[string]string{"version": "v1"}}}},
				"ports":     []any{map[string]any{"port": 8081}},
			}},
		})
	}))
	defer ts.Close()
	resolver, err := NewKubernetesResolver(KubernetesResolverConfig{BaseURL: ts.URL, Namespace: "default", Service: "greeter"})
	if err != nil {
		t.Fatal(err)
	}
	instances, err := resolver.ResolveInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Endpoint != "http://10.0.0.1:8081" || instances[0].Tags["version"] != "v1" {
		t.Fatalf("instances = %#v, want kubernetes endpoint", instances)
	}
}

func TestEtcdRegistry(t *testing.T) {
	store := map[string]string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/v3/kv/put":
			store[body["key"]] = body["value"]
			_, _ = w.Write([]byte(`{}`))
		case "/v3/kv/range":
			var kvs []map[string]string
			for _, value := range store {
				kvs = append(kvs, map[string]string{"value": value})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"kvs": kvs})
		case "/v3/kv/deleterange":
			delete(store, body["key"])
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected etcd path %s", r.URL.Path)
		}
	}))
	defer ts.Close()
	registry, err := NewEtcdRegistry(ts.URL, "/gofly/services", ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterInstance(context.Background(), "greeter", ServiceInstance{Endpoint: "http://127.0.0.1:8081", Weight: 3, Tags: map[string]string{"zone": "a"}}); err != nil {
		t.Fatal(err)
	}
	instances, err := registry.ResolveInstances(context.Background(), "greeter")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Weight != 3 || instances[0].Tags["zone"] != "a" {
		t.Fatalf("instances = %#v, want etcd instance metadata", instances)
	}
	encodedKey := base64.StdEncoding.EncodeToString([]byte("/gofly/services/greeter/http://127.0.0.1:8081"))
	if _, ok := store[encodedKey]; !ok {
		t.Fatalf("store missing encoded service key %s", encodedKey)
	}
}

func TestEtcdRegistryDiscoveryResolveFilters(t *testing.T) {
	var mu sync.Mutex
	store := map[string]string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v3/kv/put":
			store[body["key"]] = body["value"]
			_, _ = w.Write([]byte(`{}`))
		case "/v3/kv/range":
			var kvs []map[string]string
			for _, value := range store {
				kvs = append(kvs, map[string]string{"value": value})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"kvs": kvs})
		case "/v3/kv/deleterange":
			delete(store, body["key"])
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected etcd path %s", r.URL.Path)
		}
	}))
	defer ts.Close()
	registry, err := NewEtcdRegistry(ts.URL, "/gofly/services", ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := registry.Register(context.Background(), discovery.Instance{
		Service:  "greeter",
		Endpoint: "http://127.0.0.1:8081/",
		Weight:   7,
		Version:  "v1",
		Zone:     "az1",
		Status:   discovery.StatusHealthy,
		Tags:     map[string]string{"env": "prod"},
		Metadata: map[string]string{"owner": "platform"},
	})
	if err != nil {
		t.Fatal(err)
	}
	instances, err := registry.Resolve(context.Background(), "greeter", discovery.WithVersion("v1"), discovery.WithZone("az1"), discovery.WithTag("env", "prod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Endpoint != "http://127.0.0.1:8081" || instances[0].Weight != 7 || instances[0].Metadata["owner"] != "platform" {
		t.Fatalf("instances = %#v, want structured discovery instance", instances)
	}
	instances[0].Tags["env"] = "mutated"
	instances[0].Metadata["owner"] = "mutated"
	again, err := registry.Resolve(context.Background(), "greeter", discovery.WithVersion("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Tags["env"] != "prod" || again[0].Metadata["owner"] != "platform" {
		t.Fatalf("registry returned mutable instance maps: %#v", again[0])
	}
	if _, err := registry.Resolve(context.Background(), "greeter", discovery.WithVersion("v2")); !errors.Is(err, discovery.ErrNoInstances) {
		t.Fatalf("resolve v2 err = %v, want ErrNoInstances", err)
	}
	if err := lease.KeepAlive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(context.Background(), "greeter"); !errors.Is(err, discovery.ErrNoInstances) {
		t.Fatalf("resolve after close err = %v, want ErrNoInstances", err)
	}
}

func TestEtcdRegistryWatchPollsChanges(t *testing.T) {
	var mu sync.Mutex
	store := map[string]string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v3/kv/put":
			store[body["key"]] = body["value"]
			_, _ = w.Write([]byte(`{}`))
		case "/v3/kv/range":
			var kvs []map[string]string
			for _, value := range store {
				kvs = append(kvs, map[string]string{"value": value})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"kvs": kvs})
		case "/v3/kv/deleterange":
			delete(store, body["key"])
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected etcd path %s", r.URL.Path)
		}
	}))
	defer ts.Close()
	registry, err := NewEtcdRegistry(ts.URL, "/gofly/services", ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	registry.watchInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := registry.Watch(ctx, "greeter")
	if err != nil {
		t.Fatal(err)
	}
	initial := <-events
	if initial.Type != discovery.EventSnapshot || len(initial.Instances) != 0 {
		t.Fatalf("initial event = %#v, want empty snapshot", initial)
	}
	if err := registry.RegisterInstance(context.Background(), "greeter", ServiceInstance{Endpoint: "http://127.0.0.1:8081", Weight: 3}); err != nil {
		t.Fatal(err)
	}
	registered := waitEtcdDiscoveryEvent(t, events, discovery.EventRegistered)
	if registered.Instance.Endpoint != "http://127.0.0.1:8081" || len(registered.Instances) != 1 {
		t.Fatalf("registered event = %#v, want registered 8081", registered)
	}
	if err := registry.DeregisterService(context.Background(), "greeter", "http://127.0.0.1:8081"); err != nil {
		t.Fatal(err)
	}
	deregistered := waitEtcdDiscoveryEvent(t, events, discovery.EventDeregister)
	if deregistered.Instance.Endpoint != "http://127.0.0.1:8081" || len(deregistered.Instances) != 0 {
		t.Fatalf("deregistered event = %#v, want no instances", deregistered)
	}
	cancel()
}

func waitEtcdDiscoveryEvent(t *testing.T, events <-chan discovery.Event, eventType discovery.EventType) discovery.Event {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before %s", eventType)
			}
			if event.Type == eventType {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}

func TestCachedResolverWatchesRegistryUpdates(t *testing.T) {
	registry := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver, err := NewCachedResolver(ctx, registry.Resolver("greeter").(WatchResolver))
	if err != nil {
		t.Fatal(err)
	}
	if endpoints, err := resolver.Resolve(context.Background()); err == nil || endpoints != nil {
		t.Fatalf("initial resolve endpoints=%v error=%v, want empty cache error", endpoints, err)
	}
	updates, err := resolver.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial := <-updates; len(initial) != 0 {
		t.Fatalf("initial cached update = %v, want empty", initial)
	}

	registry.Register("greeter", "http://127.0.0.1:8081/")
	deadline := time.After(time.Second)
	waitingForRegister := true
	for waitingForRegister {
		select {
		case got := <-updates:
			if len(got) == 1 && got[0] == "http://127.0.0.1:8081" {
				waitingForRegister = false
			}
		case <-deadline:
			t.Fatal("timed out waiting for cached resolver update")
		}
	}
	endpoints, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0] != "http://127.0.0.1:8081" {
		t.Fatalf("cached endpoints = %v, want registered endpoint", endpoints)
	}
	snapshot := resolver.Snapshot()
	if len(snapshot.Endpoints) != 1 || snapshot.Endpoints[0] != "http://127.0.0.1:8081" {
		t.Fatalf("snapshot endpoints = %v, want registered endpoint", snapshot.Endpoints)
	}
	if snapshot.Watchers != 1 {
		t.Fatalf("snapshot watchers = %d, want 1", snapshot.Watchers)
	}
	if snapshot.Updates == 0 || snapshot.LastUpdated.IsZero() {
		t.Fatalf("snapshot = %#v, want update metadata", snapshot)
	}

	registry.Deregister("greeter", "http://127.0.0.1:8081")
	deadline = time.After(time.Second)
	waitingForDeregister := true
	for waitingForDeregister {
		select {
		case got := <-updates:
			if len(got) == 0 {
				waitingForDeregister = false
			}
		case <-deadline:
			t.Fatal("timed out waiting for cached resolver deregister update")
		}
	}
}

func TestGovernanceSuiteUsesSeparateTimeoutConfig(t *testing.T) {
	conf := GovernanceConfig{
		Timeout: time.Second,
		TimeoutConfig: RPCTimeoutConfig{
			Server: time.Millisecond,
			Client: 2 * time.Millisecond,
		},
	}
	serverTimeout, clientTimeout := rpcTimeouts(conf)
	if serverTimeout != time.Millisecond {
		t.Fatalf("server timeout = %v, want %v", serverTimeout, time.Millisecond)
	}
	if clientTimeout != 2*time.Millisecond {
		t.Fatalf("client timeout = %v, want %v", clientTimeout, 2*time.Millisecond)
	}
}

func TestRPCDefaultGovernanceAndObservabilitySuite(t *testing.T) {
	conf := DefaultGovernanceConfig(25 * time.Millisecond)
	if !conf.Recover || !conf.RequestID || !conf.Trace || !conf.Log || !conf.Metrics || conf.Timeout != 25*time.Millisecond {
		t.Fatalf("DefaultGovernanceConfig = %#v, want recover/request-id/trace/log/metrics and timeout", conf)
	}

	suite := ObservabilitySuite("orders", 10*time.Millisecond)
	server := suite.ServerOptions()
	client := suite.ClientOptions()
	if len(server) != 9 {
		t.Fatalf("observability server options = %d, want unary and stream recover/trace/timeout/metrics/log", len(server))
	}
	if len(client) != 8 {
		t.Fatalf("observability client options = %d, want unary and stream trace/timeout/metrics/log", len(client))
	}
}

func TestRPCBasicSuiteReturnsDefensiveCopies(t *testing.T) {
	suite := BasicSuite{
		Server: []ServerOption{WithServerAdminToken("secret")},
		Client: []ClientOption{WithRetry(2)},
	}
	serverOptions := suite.ServerOptions()
	client := suite.ClientOptions()
	serverOptions[0] = WithServerAdminToken("mutated")
	client[0] = WithRetry(9)

	server := NewServer(WithServerSuite(suite))
	if server.opts.adminToken != "secret" {
		t.Fatalf("server admin token = %q, want secret", server.opts.adminToken)
	}

	rpcClient, err := NewClient("http://127.0.0.1:1", WithClientSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	if rpcClient.opts.retry != 2 {
		t.Fatalf("client retry = %d, want 2", rpcClient.opts.retry)
	}
}

func TestRPCGovernanceOptionAliasesAndOverrides(t *testing.T) {
	rules := governance.NewRuleSet(governance.Rule{Name: "alias"})
	server := NewServer(WithServerGovernanceRuleSet(rules), WithServerReadHeaderTimeout(time.Second), WithServerReadHeaderTimeout(0))
	if server.opts.rules != rules {
		t.Fatal("WithServerGovernanceRuleSet should set server rules")
	}
	if server.opts.readHeaderTimeout != time.Second {
		t.Fatalf("read header timeout = %v, want %v", server.opts.readHeaderTimeout, time.Second)
	}

	rpcClient, err := NewClient("http://127.0.0.1:1", WithClientGovernanceRuleSet(rules), WithClientSingleflight())
	if err != nil {
		t.Fatal(err)
	}
	if rpcClient.opts.rules != rules {
		t.Fatal("WithClientGovernanceRuleSet should set client rules")
	}
	if rpcClient.opts.singleflight == nil {
		t.Fatal("WithClientSingleflight should initialize singleflight group")
	}
}

func TestRegistryTTLAndWatch(t *testing.T) {
	registry := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, err := registry.WatchService(ctx, "greeter")
	if err != nil {
		t.Fatal(err)
	}
	if got := <-updates; len(got) != 0 {
		t.Fatalf("initial update = %v, want empty", got)
	}
	if err := registry.RegisterServiceWithOptions(context.Background(), "greeter", "http://127.0.0.1:8081", WithRegisterTTL(10*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-updates:
		if len(got) != 1 || got[0] != "http://127.0.0.1:8081" {
			t.Fatalf("watch update = %v, want registered endpoint", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for registry watch update")
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := registry.ResolveService(context.Background(), "greeter"); err == nil {
		t.Fatal("resolve error is nil, want expired endpoint to be removed")
	}
}

func TestRPCAuthMiddleware(t *testing.T) {
	s := NewServer(WithServerMiddleware(ServerAuthMiddleware(auth.StaticTokenValidator("secret", "rpc-user"))))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "WhoAmI",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: auth.SubjectFromContext(ctx)}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	unauthorized, err := NewClient(ts.URL, WithRetry(1))
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	err = unauthorized.Call(context.Background(), "greeter/WhoAmI", helloRequest{}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnauthenticated {
		t.Fatalf("unauthorized error = %v, want unauthenticated", err)
	}
	authorized, err := NewClient(ts.URL, WithClientMiddleware(ClientBearerTokenMiddleware("secret")))
	if err != nil {
		t.Fatal(err)
	}
	if err := authorized.Call(context.Background(), "greeter/WhoAmI", helloRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "rpc-user" {
		t.Fatalf("message = %q, want rpc-user", resp.Message)
	}
}

func TestGovernanceSuiteAddsRequestIDTraceAndAuth(t *testing.T) {
	suite := GovernanceSuite("greeter", GovernanceConfig{
		Recover:     true,
		RequestID:   true,
		Trace:       true,
		Metrics:     true,
		ServerAuth:  auth.StaticTokenValidator("secret", "rpc-user"),
		ClientToken: "secret",
	})
	s := NewServer(WithServerSuite(suite))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "WhoAmI",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			if metadata.RequestIDFromContext(ctx) == "" {
				t.Fatal("request id should be set")
			}
			if _, ok := trace.FromContext(ctx); !ok {
				t.Fatal("trace context should be set")
			}
			return helloResponse{Message: auth.SubjectFromContext(ctx)}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithClientSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	md, err := c.CallWithMetadata(context.Background(), "greeter/WhoAmI", helloRequest{}, &resp)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message != "rpc-user" {
		t.Fatalf("message = %q, want rpc-user", resp.Message)
	}
	if md.Get(metadata.RequestIDKey) == "" || md.Get(trace.TraceParentHeader) == "" {
		t.Fatalf("metadata = %#v, want request id and traceparent", md)
	}
}

func TestGovernanceSuiteCanWireAdaptiveBreaker(t *testing.T) {
	suite := GovernanceSuite("greeter", GovernanceConfig{
		Breaker: true,
		ServerBreaker: breaker.NewAdaptive(
			breaker.WithAdaptiveMinRequests(1),
			breaker.WithAdaptiveFailureRatio(0.1),
			breaker.WithAdaptiveK(1),
			breaker.WithAdaptiveOpenTimeout(time.Second),
		),
	})
	s := NewServer(WithServerSuite(suite))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Unstable",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return nil, NewError(CodeInternal, "boom")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/Unstable", helloRequest{}, &resp); err == nil || CodeOf(err) != CodeInternal {
		t.Fatalf("first error = %v, want internal", err)
	}
	if err := c.Call(context.Background(), "greeter/Unstable", helloRequest{}, &resp); err == nil || CodeOf(err) != CodeUnavailable {
		t.Fatalf("second error = %v, want unavailable", err)
	}
}

func TestHTTPClientTransportOptions(t *testing.T) {
	custom := &http.Client{Timeout: time.Second}
	c, err := NewClient("http://127.0.0.1:1", WithHTTPClient(custom))
	if err != nil {
		t.Fatal(err)
	}
	if c.hc != custom {
		t.Fatal("client should use provided http client")
	}
	configured, err := NewClient("http://127.0.0.1:1", WithTransportConfig(TransportConfig{MaxIdleConnsPerHost: 7}))
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := configured.hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", configured.hc.Transport)
	}
	if transport.MaxIdleConnsPerHost != 7 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want 7", transport.MaxIdleConnsPerHost)
	}
}

func TestRPCMetadataPropagationAndSuite(t *testing.T) {
	serverMWCalled := false
	clientMWCalled := false
	suite := BasicSuite{
		Server: []ServerOption{WithServerMiddleware(func(next endpoint.Endpoint) endpoint.Endpoint {
			return func(ctx context.Context, req any) (any, error) {
				serverMWCalled = true
				if got := metadata.RequestIDFromContext(ctx); got != "rid-rpc" {
					t.Fatalf("server request id = %q, want rid-rpc", got)
				}
				return next(ctx, req)
			}
		})},
		Client: []ClientOption{WithClientMiddleware(func(next endpoint.Endpoint) endpoint.Endpoint {
			return func(ctx context.Context, req any) (any, error) {
				clientMWCalled = true
				return next(ctx, req)
			}
		})},
	}
	s := NewServer(WithServerSuite(suite))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResponse{Message: metadata.RequestIDFromContext(ctx)}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithClientSuite(suite))
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.Append(context.Background(), metadata.RequestIDKey, "rid-rpc")
	var resp helloResponse
	if err := c.Call(ctx, "greeter/SayHello", helloRequest{}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "rid-rpc" {
		t.Fatalf("message = %q, want rid-rpc", resp.Message)
	}
	if !serverMWCalled || !clientMWCalled {
		t.Fatalf("serverMWCalled=%v clientMWCalled=%v, want both true", serverMWCalled, clientMWCalled)
	}
}

func TestHTTPClientAdaptiveBreaker(t *testing.T) {
	calls := 0
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Fail",
		NewRequest: func() any { return new(helloRequest) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls++
			return nil, NewError(CodeInternal, "boom")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL,
		WithRetry(1),
		WithAdaptiveBreaker(breaker.NewAdaptive(
			breaker.WithAdaptiveMinRequests(1),
			breaker.WithAdaptiveFailureRatio(0.1),
			breaker.WithAdaptiveK(1),
		)),
	)
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResponse
	if err := c.Call(context.Background(), "greeter/Fail", helloRequest{}, &resp); err == nil {
		t.Fatal("first call error is nil, want failure")
	}
	err = c.Call(context.Background(), "greeter/Fail", helloRequest{}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnavailable {
		t.Fatalf("second error = %v, want unavailable rpc error", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestErrorfAndHelpers(t *testing.T) {
	err := Errorf(CodeNotFound, "resource %s", "x")
	if err.Code != CodeNotFound || err.Text != "resource x" {
		t.Fatalf("Errorf = %+v", err)
	}
	if CodeOf(err) != CodeNotFound {
		t.Fatalf("CodeOf = %v, want NotFound", CodeOf(err))
	}
	if httpStatusFromCode(CodeNotFound) != http.StatusNotFound {
		t.Fatalf("httpStatusFromCode = %d, want 404", httpStatusFromCode(CodeNotFound))
	}
	if !isRetryable(NewError(CodeUnavailable, "retry me")) {
		t.Fatal("isRetryable unavailable = false, want true")
	}
	if isRetryable(NewError(CodeNotFound, "no")) {
		t.Fatal("isRetryable notfound = true, want false")
	}
}

func TestIsZeroTransportConfig(t *testing.T) {
	if !IsZeroTransportConfig(TransportConfig{}) {
		t.Fatal("IsZeroTransportConfig empty = false, want true")
	}
	if IsZeroTransportConfig(TransportConfig{Timeout: time.Second}) {
		t.Fatal("IsZeroTransportConfig with timeout = true, want false")
	}
}

func rpcPhaseStats(snapshot callstats.Snapshot, phase string) (callstats.PhaseSnapshot, bool) {
	for _, stats := range snapshot.Phases {
		if stats.Phase == phase {
			return stats, true
		}
	}
	return callstats.PhaseSnapshot{}, false
}
