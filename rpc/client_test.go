package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofly/gofly/core/auth"
	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/discovery"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/observability/trace"
	"github.com/gofly/gofly/rpc/endpoint"
)

func TestHTTPClientCall(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
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
	var resp helloResp
	if err := c.Call(context.Background(), "greeter/SayHello", helloReq{Name: "client"}, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Message != "hello client" {
		t.Fatalf("message = %q, want hello client", resp.Message)
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
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
	var resp helloResp
	md, err := c.CallWithMetadata(context.Background(), "greeter/SayHello", helloReq{Name: "client"}, &resp)
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
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
	payload, md, err := c.CallRaw(context.Background(), "greeter/SayHello", helloReq{Name: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	var resp helloResp
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
		NewRequest: func() any { return new(helloReq) },
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
	payload, md, err := c.CallRaw(context.Background(), "greeter/Missing", helloReq{})
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			<-gate
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL,
		WithClientSingleflightKey(func(ctx context.Context, method string, request any) (string, error) {
			return method + ":" + request.(helloReq).Name, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	const workers = 12
	var wg sync.WaitGroup
	responses := make(chan helloResp, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var resp helloResp
			if err := c.Call(context.Background(), "greeter/SayHello", helloReq{Name: "shared"}, &resp); err != nil {
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
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
		var resp helloResp
		firstErr <- c.Call(context.Background(), "greeter/Slow", helloReq{Name: "first"}, &resp)
	}()
	<-entered

	var resp helloResp
	err = c.Call(context.Background(), "greeter/Slow", helloReq{Name: "second"}, &resp)
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
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
		var resp helloResp
		firstErr <- c.Call(context.Background(), "greeter/Slow", helloReq{Name: "first"}, &resp)
	}()
	<-entered

	var resp helloResp
	err = c.Call(context.Background(), "greeter/Slow", helloReq{Name: "second"}, &resp)
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
		NewRequest: func() any { return new(helloReq) },
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
	var resp helloResp
	err = c.Call(context.Background(), "greeter/Missing", helloReq{Name: "client"}, &resp)
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
		_ = json.NewEncoder(w).Encode(responseEnvelope{Payload: helloResp{Message: "ok"}, Code: CodeOK})
	}))
	defer ts.Close()

	c, err := NewClient(ts.URL, WithClientMiddleware(TraceMiddleware("greeter.client")))
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.Append(context.Background(), trace.TraceParentHeader, parent)
	var resp helloResp
	if err := c.Call(ctx, "greeter/SayHello", helloReq{Name: "client"}, &resp); err != nil {
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
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
	var resp helloResp
	if err := client.Call(context.Background(), "greeter/SayHello", helloReq{Name: "gofly"}, &resp); err != nil {
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			if calls.Add(1) == 1 {
				return nil, NewError(CodeUnavailable, "try again")
			}
			md, _ := metadata.FromContext(ctx)
			return helloResp{Message: md.Get("x-governance")}, nil
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
	var resp helloResp
	if err := c.Call(context.Background(), "greeter/SayHello", helloReq{Name: "gofly"}, &resp); err != nil {
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
	resp = helloResp{}
	if err := c.Call(context.Background(), "greeter/SayHello", helloReq{Name: "gofly"}, &resp); err != nil {
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromContext(ctx)
			return helloResp{Message: md.Get("x-client-rule")}, nil
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

	var resp helloResp
	if err := c.Call(context.Background(), "greeter/SayHello", helloReq{Name: "gofly"}, &resp); err != nil {
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromContext(ctx)
			return helloResp{Message: md.Get(governance.HeaderCanary) + ":" + md.Get("x-lane")}, nil
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
	var resp helloResp
	if err := c.Call(ctx, "greeter/SayHello", helloReq{Name: "gofly"}, &resp); err != nil {
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			return helloResp{Message: "hello"}, nil
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

	var resp helloResp
	if err := c.Call(context.Background(), "greeter/SayHello", helloReq{Name: "first"}, &resp); err != nil {
		t.Fatalf("first call: %v", err)
	}
	err = c.Call(context.Background(), "greeter/SayHello", helloReq{Name: "second"}, &resp)
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return helloResp{Message: "hello"}, nil
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
		var resp helloResp
		firstErr <- c.Call(context.Background(), "greeter/Slow", helloReq{Name: "first"}, &resp)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("timed out waiting for first request to enter handler")
	}

	var resp helloResp
	err = c.Call(context.Background(), "greeter/Slow", helloReq{Name: "second"}, &resp)
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
		NewRequest: func() any { return new(helloReq) },
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

	var resp helloResp
	for i := 1; i <= 2; i++ {
		err = c.Call(context.Background(), "greeter/Unstable", helloReq{Name: "gofly"}, &resp)
		var rpcErr *Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInternal {
			t.Fatalf("call %d error = %v, want internal rpc error", i, err)
		}
	}
	err = c.Call(context.Background(), "greeter/Unstable", helloReq{Name: "gofly"}, &resp)
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
		NewRequest: func() any { return new(helloReq) },
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

	var resp helloResp
	err = c.Call(context.Background(), "greeter/Slow", helloReq{Name: "gofly"}, &resp)
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: auth.SubjectFromContext(ctx)}, nil
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
	var resp helloResp
	err = unauthorized.Call(context.Background(), "greeter/WhoAmI", helloReq{}, &resp)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnauthenticated {
		t.Fatalf("unauthorized error = %v, want unauthenticated", err)
	}
	authorized, err := NewClient(ts.URL, WithClientMiddleware(ClientBearerTokenMiddleware("secret")))
	if err != nil {
		t.Fatal(err)
	}
	if err := authorized.Call(context.Background(), "greeter/WhoAmI", helloReq{}, &resp); err != nil {
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			if metadata.RequestIDFromContext(ctx) == "" {
				t.Fatal("request id should be set")
			}
			if _, ok := trace.FromContext(ctx); !ok {
				t.Fatal("trace context should be set")
			}
			return helloResp{Message: auth.SubjectFromContext(ctx)}, nil
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
	var resp helloResp
	md, err := c.CallWithMetadata(context.Background(), "greeter/WhoAmI", helloReq{}, &resp)
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
		NewRequest: func() any { return new(helloReq) },
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
	var resp helloResp
	if err := c.Call(context.Background(), "greeter/Unstable", helloReq{}, &resp); err == nil || CodeOf(err) != CodeInternal {
		t.Fatalf("first error = %v, want internal", err)
	}
	if err := c.Call(context.Background(), "greeter/Unstable", helloReq{}, &resp); err == nil || CodeOf(err) != CodeUnavailable {
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
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: metadata.RequestIDFromContext(ctx)}, nil
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
	var resp helloResp
	if err := c.Call(ctx, "greeter/SayHello", helloReq{}, &resp); err != nil {
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
		NewRequest: func() any { return new(helloReq) },
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
	var resp helloResp
	if err := c.Call(context.Background(), "greeter/Fail", helloReq{}, &resp); err == nil {
		t.Fatal("first call error is nil, want failure")
	}
	err = c.Call(context.Background(), "greeter/Fail", helloReq{}, &resp)
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
