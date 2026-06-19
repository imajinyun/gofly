package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/gofly/gofly/core/breaker"
	coregovernance "github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/observability/trace"
	"github.com/gofly/gofly/rpc/endpoint"
)

type helloReq struct {
	Name string `json:"name"`
}
type helloResp struct {
	Message string `json:"message"`
}

func TestHTTPServerServeHTTP(t *testing.T) {
	s := NewServer()
	err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
		},
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPServerServeHTTPRejectsNilBody(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{}`))
	req.Body = nil
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusBadRequest)
	}
}

func TestHTTPServerServiceNamesAreDeterministic(t *testing.T) {
	s := NewServer()
	for _, service := range []string{"zeta", "alpha", "middle"} {
		if err := s.RegisterService(ServiceDesc{Name: service, Methods: []MethodDesc{{
			Name:       "Ping",
			NewRequest: func() any { return new(helloReq) },
			Handler: func(ctx context.Context, req any) (any, error) {
				return helloResp{Message: "pong"}, nil
			},
		}}}, nil); err != nil {
			t.Fatal(err)
		}
	}

	names := s.serviceNames()
	want := []string{"alpha", "middle", "zeta"}
	if len(names) != len(want) {
		t.Fatalf("serviceNames = %#v, want %#v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("serviceNames = %#v, want %#v", names, want)
		}
	}
}

func TestServerGovernanceManagerOverridesExplicitRuleSet(t *testing.T) {
	stale := coregovernance.NewRuleSet(coregovernance.Rule{Name: "stale", Transport: coregovernance.TransportRPC, Service: "greeter"})
	manager, err := coregovernance.NewManager(coregovernance.Config{Rules: []coregovernance.Rule{{Name: "live", Transport: coregovernance.TransportRPC, Service: "greeter"}}})
	if err != nil {
		t.Fatal(err)
	}

	s := NewServer(WithServerRuleSet(stale), WithServerGovernanceManager(manager))
	decision := s.governanceDecision(coregovernance.Request{Transport: coregovernance.TransportRPC, Service: "greeter"})
	if !decision.Matched || decision.RuleName != "live" {
		t.Fatalf("decision = %#v, want manager rule", decision)
	}
}

func TestServerGovernanceSuiteProvidesRules(t *testing.T) {
	suite := coregovernance.MustNewSuite(coregovernance.NewPlugin("rpc-default", coregovernance.Rule{Name: "suite", Transport: coregovernance.TransportRPC, Service: "greeter"}))
	s := NewServer(WithServerGovernanceSuite(suite))
	decision := s.governanceDecision(coregovernance.Request{Transport: coregovernance.TransportRPC, Service: "greeter"})
	if !decision.Matched || decision.RuleName != "suite" {
		t.Fatalf("decision = %#v, want suite rule", decision)
	}
}

func TestServerGovernanceManagerOverridesLaterSuite(t *testing.T) {
	manager, err := coregovernance.NewManager(coregovernance.Config{Rules: []coregovernance.Rule{{Name: "live", Transport: coregovernance.TransportRPC, Service: "greeter"}}})
	if err != nil {
		t.Fatal(err)
	}
	suite := coregovernance.MustNewSuite(coregovernance.NewPlugin("stale", coregovernance.Rule{Name: "stale", Transport: coregovernance.TransportRPC, Service: "greeter"}))
	s := NewServer(WithServerGovernanceManager(manager), WithServerGovernanceSuite(suite))
	decision := s.governanceDecision(coregovernance.Request{Transport: coregovernance.TransportRPC, Service: "greeter"})
	if !decision.Matched || decision.RuleName != "live" {
		t.Fatalf("decision = %#v, want manager rule", decision)
	}
}

func TestHTTPServerProtoCodec(t *testing.T) {
	s := NewServer(WithServerCodec(ProtoCodec{}))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(wrapperspb.StringValue) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return wrapperspb.String("hello " + req.(*wrapperspb.StringValue).Value), nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	defer ts.Close()
	c, err := NewClient(ts.URL, WithCodec(ProtoCodec{}))
	if err != nil {
		t.Fatal(err)
	}
	var resp wrapperspb.StringValue
	if err := c.Call(context.Background(), "greeter/SayHello", wrapperspb.String("gofly"), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Value != "hello gofly" {
		t.Fatalf("response = %q, want hello gofly", resp.Value)
	}
}

func TestHTTPServerAdaptiveLimiterRejectsWhenSaturated(t *testing.T) {
	limiter := limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1), limit.WithAdaptiveInitialLimit(1))
	first, err := limiter.Allow()
	if err != nil {
		t.Fatalf("pre-acquire limiter: %v", err)
	}
	defer first.Done(true)

	s := NewServer(WithServerAdaptiveLimiter(limiter))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Version: "v1", Metadata: map[string]string{"owner": "platform"}, Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Metadata:   map[string]string{"request": "helloReq", "response": "helloResp"},
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

func TestHTTPServerAdaptiveBreakerRejectsAfterFailure(t *testing.T) {
	s := NewServer(WithServerAdaptiveBreaker(breaker.NewAdaptive(
		breaker.WithAdaptiveMinRequests(1),
		breaker.WithAdaptiveFailureRatio(0.1),
		breaker.WithAdaptiveK(1),
		breaker.WithAdaptiveOpenTimeout(time.Second),
	)))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Unstable",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return nil, NewError(CodeInternal, "boom")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	s.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/rpc/greeter/Unstable", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", first.Code)
	}

	second := httptest.NewRecorder()
	s.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/rpc/greeter/Unstable", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if second.Code != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503", second.Code)
	}
}

func TestHTTPServerServiceInfosAndErrorCode(t *testing.T) {
	s := NewServer()
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Missing",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return nil, NewError(CodeNotFound, "hello not found")
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	if len(s.GetServiceInfos()) != 1 {
		t.Fatalf("service infos len = %d, want 1", len(s.GetServiceInfos()))
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/Missing", strings.NewReader(`{"payload":{"name":"gofly"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHTTPServerMethodMetadataAndMiddleware(t *testing.T) {
	s := NewServer()
	methodMiddleware := func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromContext(ctx)
			if md.Get("service-owner") != "platform" || md.Get("method-kind") != "read" || md.Get("shared") != "method" {
				return nil, NewError(CodeInternal, "metadata not applied")
			}
			ctx = metadata.Append(ctx, "method-middleware", "seen")
			return next(ctx, req)
		}
	}
	if err := s.RegisterService(ServiceDesc{
		Name:     "greeter",
		Version:  "v1",
		Metadata: map[string]string{"service-owner": "platform", "shared": "service"},
		Methods: []MethodDesc{{
			Name:        "SayHello",
			NewRequest:  func() any { return new(helloReq) },
			Metadata:    map[string]string{"method-kind": "read", "shared": "method"},
			Middlewares: []endpoint.Middleware{methodMiddleware},
			Handler: func(ctx context.Context, req any) (any, error) {
				md, _ := metadata.FromContext(ctx)
				if md.Get("method-middleware") != "seen" || md.Get("rpc.service.version") != "v1" {
					return nil, NewError(CodeInternal, "method middleware not applied")
				}
				return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
			},
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestHTTPServerMethodTimeoutAndServiceSnapshot(t *testing.T) {
	s := NewServer()
	serviceMetadata := map[string]string{"owner": "platform"}
	methodMetadata := map[string]string{"kind": "slow"}
	streamMetadata := map[string]string{"mode": "duplex"}
	if err := s.RegisterService(ServiceDesc{
		Name:     "greeter",
		Version:  "v2",
		Metadata: serviceMetadata,
		Methods: []MethodDesc{{
			Name:       "Slow",
			NewRequest: func() any { return new(helloReq) },
			Timeout:    time.Millisecond,
			Metadata:   methodMetadata,
			Handler: func(ctx context.Context, req any) (any, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}},
		Streams: []StreamDesc{{
			Name:        "Chat",
			NewMessage:  func() any { return new(helloReq) },
			Timeout:     2 * time.Second,
			Metadata:    streamMetadata,
			Middlewares: []StreamMiddleware{StreamRequestIDMiddleware()},
			Handler:     func(context.Context, *Stream) error { return nil },
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	serviceMetadata["owner"] = "mutated"
	methodMetadata["kind"] = "mutated"
	streamMetadata["mode"] = "mutated"

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/Slow", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout status = %d, body = %s, want %d", rec.Code, rec.Body.String(), http.StatusGatewayTimeout)
	}

	snapshots := s.ServiceSnapshots()
	if len(snapshots) != 1 || snapshots[0].Version != "v2" || snapshots[0].Metadata["owner"] != "platform" {
		t.Fatalf("service snapshots = %#v, want version and defensive service metadata", snapshots)
	}
	if len(snapshots[0].MethodDetails) != 1 || snapshots[0].MethodDetails[0].Timeout != "1ms" || snapshots[0].MethodDetails[0].Metadata["kind"] != "slow" {
		t.Fatalf("method details = %#v, want timeout and defensive metadata", snapshots[0].MethodDetails)
	}
	if len(snapshots[0].Streams) != 1 || snapshots[0].Streams[0].Timeout != "2s" || snapshots[0].Streams[0].Metadata["mode"] != "duplex" || snapshots[0].Streams[0].Middlewares != 1 {
		t.Fatalf("stream details = %#v, want timeout, middleware count and defensive metadata", snapshots[0].Streams)
	}
}

func TestHTTPServerGovernanceRuleRuntimeRateLimit(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-rate",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			RateLimit: coregovernance.RateLimitPolicy{Rate: 1, Burst: 1},
		},
	})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		return helloResp{Message: "hello"}, nil
	})

	first := httptest.NewRecorder()
	s.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, body = %s, want 200", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	s.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, body = %s, want 429", second.Code, second.Body.String())
	}
	var env responseEnvelope
	if err := json.NewDecoder(second.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != CodeResourceExhausted {
		t.Fatalf("code = %q, want %q", env.Code, CodeResourceExhausted)
	}
}

func TestHTTPServerGovernanceRuleRuntimeConcurrencyLimit(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-concurrency",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			Concurrency: coregovernance.ConcurrencyPolicy{Limit: 1},
		},
	})
	entered := make(chan struct{})
	release := make(chan struct{})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return helloResp{Message: "hello"}, nil
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
		done <- rec
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first request to enter handler")
	}

	second := httptest.NewRecorder()
	s.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if second.Code != http.StatusServiceUnavailable {
		close(release)
		t.Fatalf("second status = %d, body = %s, want 503", second.Code, second.Body.String())
	}
	close(release)
	select {
	case first := <-done:
		if first.Code != http.StatusOK {
			t.Fatalf("first status = %d, body = %s, want 200", first.Code, first.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first request to finish")
	}
}

func TestHTTPServerGovernanceRuleRuntimeBreaker(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-breaker",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			Breaker: coregovernance.BreakerPolicy{Enabled: true, MinRequests: 1, FailureRatio: 0.1, OpenTimeout: time.Second},
		},
	})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		return nil, NewError(CodeInternal, "boom")
	})

	first := httptest.NewRecorder()
	s.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, body = %s, want 500", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	s.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if second.Code != http.StatusInternalServerError {
		t.Fatalf("second status = %d, body = %s, want 500", second.Code, second.Body.String())
	}

	third := httptest.NewRecorder()
	s.ServeHTTP(third, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if third.Code != http.StatusServiceUnavailable {
		t.Fatalf("third status = %d, body = %s, want 503", third.Code, third.Body.String())
	}
}

func TestHTTPServerGovernanceRuleRuntimeTimeoutAndMetadata(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-timeout-metadata",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			Timeout: 5 * time.Millisecond,
			Metadata: map[string]string{
				"x-policy": "enabled",
			},
			Canary: coregovernance.CanaryPolicy{
				Ratio:   1,
				Service: "greeter-canary",
				Headers: map[string]string{"x-canary-group": "blue"},
			},
		},
	})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		md, ok := metadata.FromContext(ctx)
		if !ok || md.Get("x-policy") != "enabled" || md.Get(coregovernance.HeaderCanary) != "true" || md.Get(coregovernance.HeaderCanaryService) != "greeter-canary" || md.Get("x-canary-group") != "blue" {
			t.Fatalf("metadata = %#v, want governance policy and canary metadata", md)
		}
		<-ctx.Done()
		return nil, nil
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, body = %s, want 504", rec.Code, rec.Body.String())
	}
	var env responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Code != CodeDeadlineExceeded {
		t.Fatalf("code = %q, want %q", env.Code, CodeDeadlineExceeded)
	}
}

func TestHTTPServerGovernanceRuleRuntimeMaxBodyBytes(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{
		Name:      "rpc-body-limit",
		Transport: coregovernance.TransportRPC,
		Service:   "greeter",
		Method:    "SayHello",
		Policy: coregovernance.Policy{
			MaxBodyBytes: 8,
		},
	})
	s := newGovernedGreeterServer(t, rules, func(ctx context.Context, req any) (any, error) {
		return helloResp{Message: "hello"}, nil
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/rpc/greeter/SayHello", strings.NewReader(`{"payload":{"name":"gofly"}}`)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s, want 413", rec.Code, rec.Body.String())
	}
}

func newGovernedGreeterServer(t *testing.T, rules *coregovernance.RuleSet, handler func(context.Context, any) (any, error)) *HTTPServer {
	t.Helper()
	s := NewServer(WithServerRuleSet(rules))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Handler:    handler,
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHTTPServerAdminEndpoints(t *testing.T) {
	rules := coregovernance.NewRuleSet(coregovernance.Rule{Name: "rpc-default", Transport: coregovernance.TransportRPC, Service: "greeter"})
	s := NewServer(WithServerAdminToken("secret"), WithServerAdaptiveLimiter(limit.NewAdaptiveLimiter(limit.WithAdaptiveLimits(1, 1))), WithServerRuleSet(rules))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Version: "v1", Metadata: map[string]string{"owner": "platform"}, Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Metadata:   map[string]string{"request": "helloReq", "response": "helloResp"},
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRecorder()
	s.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	stateReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil)
	stateReq.Header.Set("Authorization", "Bearer secret")
	stateRec := httptest.NewRecorder()
	s.ServeHTTP(stateRec, stateReq)
	if stateRec.Code != http.StatusOK {
		t.Fatalf("state status = %d, want %d", stateRec.Code, http.StatusOK)
	}
	var state StateSnapshot
	if err := json.NewDecoder(stateRec.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.State != "initialized" {
		t.Fatalf("state = %#v, want initialized", state)
	}

	servicesReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/services", nil)
	servicesReq.Header.Set("Authorization", "Bearer secret")
	servicesRec := httptest.NewRecorder()
	s.ServeHTTP(servicesRec, servicesReq)
	if servicesRec.Code != http.StatusOK {
		t.Fatalf("services status = %d, want %d", servicesRec.Code, http.StatusOK)
	}
	var services []ServiceSnapshot
	if err := json.NewDecoder(servicesRec.Body).Decode(&services); err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Name != "greeter" || len(services[0].Methods) != 1 || services[0].Methods[0] != "SayHello" {
		t.Fatalf("services = %#v, want greeter/SayHello", services)
	}

	descriptorsReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors", nil)
	descriptorsReq.Header.Set("Authorization", "Bearer secret")
	descriptorsRec := httptest.NewRecorder()
	s.ServeHTTP(descriptorsRec, descriptorsReq)
	if descriptorsRec.Code != http.StatusOK {
		t.Fatalf("descriptors status = %d, want %d", descriptorsRec.Code, http.StatusOK)
	}
	var descriptors map[string]Descriptor
	if err := json.NewDecoder(descriptorsRec.Body).Decode(&descriptors); err != nil {
		t.Fatal(err)
	}
	desc, ok := descriptors["greeter"]
	if !ok || desc.Name != "greeter" || desc.Version != "v1" || desc.Metadata["owner"] != "platform" {
		t.Fatalf("descriptors = %#v, want greeter v1 descriptor", descriptors)
	}
	if len(desc.Methods) != 1 || desc.Methods[0].Name != "SayHello" || desc.Methods[0].Request != "helloReq" || desc.Methods[0].Response != "helloResp" {
		t.Fatalf("descriptor methods = %#v, want SayHello request/response contract", desc.Methods)
	}

	descriptorReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors/greeter", nil)
	descriptorReq.Header.Set("Authorization", "Bearer secret")
	descriptorRec := httptest.NewRecorder()
	s.ServeHTTP(descriptorRec, descriptorReq)
	if descriptorRec.Code != http.StatusOK {
		t.Fatalf("descriptor status = %d, want %d", descriptorRec.Code, http.StatusOK)
	}
	var single Descriptor
	if err := json.NewDecoder(descriptorRec.Body).Decode(&single); err != nil {
		t.Fatal(err)
	}
	if single.Name != "greeter" || len(single.Methods) != 1 || single.Methods[0].Request != "helloReq" {
		t.Fatalf("single descriptor = %#v, want greeter contract", single)
	}

	missingDescriptorReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors/missing", nil)
	missingDescriptorReq.Header.Set("Authorization", "Bearer secret")
	missingDescriptorRec := httptest.NewRecorder()
	s.ServeHTTP(missingDescriptorRec, missingDescriptorReq)
	if missingDescriptorRec.Code != http.StatusNotFound {
		t.Fatalf("missing descriptor status = %d, want %d", missingDescriptorRec.Code, http.StatusNotFound)
	}

	invalidDescriptorReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors/greeter%2Fv1", nil)
	invalidDescriptorReq.Header.Set("Authorization", "Bearer secret")
	invalidDescriptorRec := httptest.NewRecorder()
	s.ServeHTTP(invalidDescriptorRec, invalidDescriptorReq)
	if invalidDescriptorRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid descriptor status = %d, want %d", invalidDescriptorRec.Code, http.StatusBadRequest)
	}

	compatibilityAsDescriptorReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/descriptors/greeter/compatibility", nil)
	compatibilityAsDescriptorReq.Header.Set("Authorization", "Bearer secret")
	compatibilityAsDescriptorRec := httptest.NewRecorder()
	s.ServeHTTP(compatibilityAsDescriptorRec, compatibilityAsDescriptorReq)
	if compatibilityAsDescriptorRec.Code != http.StatusBadRequest {
		t.Fatalf("compatibility descriptor status = %d, want %d", compatibilityAsDescriptorRec.Code, http.StatusBadRequest)
	}

	candidate := single
	candidate.Methods[0].Response = "ChangedResp"
	candidateData, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	compatibilityReq := httptest.NewRequest(http.MethodPost, "/rpc/admin/descriptors/greeter/compatibility", strings.NewReader(string(candidateData)))
	compatibilityReq.Header.Set("Authorization", "Bearer secret")
	compatibilityRec := httptest.NewRecorder()
	s.ServeHTTP(compatibilityRec, compatibilityReq)
	if compatibilityRec.Code != http.StatusConflict {
		t.Fatalf("compatibility status = %d, want %d", compatibilityRec.Code, http.StatusConflict)
	}
	var compatibility DescriptorCompatibilityReport
	if err := json.NewDecoder(compatibilityRec.Body).Decode(&compatibility); err != nil {
		t.Fatal(err)
	}
	if !compatibility.HasBreaking() {
		t.Fatalf("compatibility = %#v, want breaking response change", compatibility)
	}

	invalidTargetReq := httptest.NewRequest(
		http.MethodPost,
		"/rpc/admin/descriptors/greeter/compatibility",
		strings.NewReader(`{"methods":[{"name":"SayHello","request":"helloReq","response":"helloResp"}]}`),
	)
	invalidTargetReq.Header.Set("Authorization", "Bearer secret")
	invalidTargetRec := httptest.NewRecorder()
	s.ServeHTTP(invalidTargetRec, invalidTargetReq)
	if invalidTargetRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid target compatibility status = %d, want %d", invalidTargetRec.Code, http.StatusBadRequest)
	}

	oversizedCompatibilityReq := httptest.NewRequest(
		http.MethodPost,
		"/rpc/admin/descriptors/greeter/compatibility",
		strings.NewReader(`{"name":"`+strings.Repeat("x", maxDescriptorCompatibilityBytes)+`"}`),
	)
	oversizedCompatibilityReq.Header.Set("Authorization", "Bearer secret")
	oversizedCompatibilityRec := httptest.NewRecorder()
	s.ServeHTTP(oversizedCompatibilityRec, oversizedCompatibilityReq)
	if oversizedCompatibilityRec.Code != http.StatusBadRequest {
		t.Fatalf("oversized compatibility status = %d, want %d", oversizedCompatibilityRec.Code, http.StatusBadRequest)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/health", nil)
	healthReq.Header.Set("Authorization", "Bearer secret")
	healthRec := httptest.NewRecorder()
	s.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", healthRec.Code, http.StatusOK)
	}
	var health HealthSnapshot
	if err := json.NewDecoder(healthRec.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "ok" || health.State.State != "initialized" || len(health.Services) != 1 {
		t.Fatalf("health = %#v, want ok initialized with one service", health)
	}

	governanceReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/governance", nil)
	governanceReq.Header.Set("Authorization", "Bearer secret")
	governanceRec := httptest.NewRecorder()
	s.ServeHTTP(governanceRec, governanceReq)
	if governanceRec.Code != http.StatusOK {
		t.Fatalf("governance status = %d, want 200", governanceRec.Code)
	}
	var governance GovernanceSnapshot
	if err := json.NewDecoder(governanceRec.Body).Decode(&governance); err != nil {
		t.Fatal(err)
	}
	if len(governance.Components) == 0 || governance.Components[0].Kind != "adaptive_limiter" {
		t.Fatalf("governance = %#v, want adaptive limiter component", governance)
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/rpc/admin/governance/rules", nil)
	rulesReq.Header.Set("Authorization", "Bearer secret")
	rulesRec := httptest.NewRecorder()
	s.ServeHTTP(rulesRec, rulesReq)
	if rulesRec.Code != http.StatusOK {
		t.Fatalf("governance rules status = %d, want 200", rulesRec.Code)
	}
	var gotRules []coregovernance.Rule
	if err := json.NewDecoder(rulesRec.Body).Decode(&gotRules); err != nil {
		t.Fatal(err)
	}
	if len(gotRules) != 1 || gotRules[0].Name != "rpc-default" {
		t.Fatalf("rules = %#v, want rpc-default", gotRules)
	}
}

func TestHTTPServerAdminWithoutTokenAllowsOnlyLocal(t *testing.T) {
	s := NewServer()
	local := httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil)
	local.RemoteAddr = "127.0.0.1:12345"
	localRec := httptest.NewRecorder()
	s.ServeHTTP(localRec, local)
	if localRec.Code != http.StatusOK {
		t.Fatalf("local admin status = %d, want 200", localRec.Code)
	}
	remote := httptest.NewRequest(http.MethodGet, "/rpc/admin/state", nil)
	remote.RemoteAddr = "203.0.113.10:12345"
	remoteRec := httptest.NewRecorder()
	s.ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote admin status = %d, want 403", remoteRec.Code)
	}
}

func TestHTTPServerTraceMiddlewarePropagatesMetadata(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	s := NewServer(WithServerMiddleware(TraceMiddleware("greeter.server")))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Trace",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			sc, ok := trace.FromContext(ctx)
			if !ok {
				t.Fatal("trace context missing")
			}
			if sc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || sc.SpanID == "00f067aa0ba902b7" {
				t.Fatalf("span = %#v, want child span", sc)
			}
			md, ok := metadata.FromContext(ctx)
			if !ok || md.Get(trace.TraceParentHeader) == "" {
				t.Fatalf("metadata = %#v, want traceparent", md)
			}
			return helloResp{Message: sc.TraceID}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	body := `{"payload":{"name":"gofly"},"metadata":{"traceparent":"` + parent + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/Trace", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var env responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Metadata.Get(trace.TraceParentHeader) == "" {
		t.Fatalf("response metadata = %#v, want traceparent", env.Metadata)
	}
}

func TestHTTPServerTraceMiddlewareCanSampleOut(t *testing.T) {
	s := NewServer(WithServerMiddleware(TraceMiddlewareWithSampler("greeter.server", trace.NeverSampler())))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "Trace",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			sc, ok := trace.FromContext(ctx)
			if !ok {
				t.Fatal("trace context missing")
			}
			if sc.Sampled {
				t.Fatal("span should be sampled out")
			}
			md, ok := metadata.FromContext(ctx)
			if !ok || md.Get(trace.SampledKey) != "false" {
				t.Fatalf("metadata = %#v, want sampled=false", md)
			}
			return helloResp{Message: md.Get(trace.TraceParentHeader)}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc/greeter/Trace", strings.NewReader(`{"payload":{"name":"gofly"}}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var env responseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	var resp helloResp
	data, _ := json.Marshal(env.Payload)
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	sc, ok := trace.ParseTraceParent(resp.Message)
	if !ok {
		t.Fatalf("traceparent = %q should parse", resp.Message)
	}
	if sc.Sampled {
		t.Fatalf("span = %#v, want sampled=false", sc)
	}
}

func TestHTTPServerRegistersAndDeregisters(t *testing.T) {
	registry := NewRegistry()
	s := NewServer(WithAddress("127.0.0.1:0"), WithRegistry(registry, "", ""))
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Version: "v1", Metadata: map[string]string{"owner": "platform"}, Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Metadata:   map[string]string{"request": "helloReq", "response": "helloResp"},
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRegistry(ctx, registry, "greeter", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rpc server to stop")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRegistry(ctx, registry, "greeter", 0); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPServerRegistryTTLKeepalive(t *testing.T) {
	registry := NewRegistry()
	s := NewServer(
		WithAddress("127.0.0.1:0"),
		WithRegistry(registry, "", ""),
		WithRegistryTTL(50*time.Millisecond),
		WithRegistryRefreshInterval(10*time.Millisecond),
	)
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run() }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRegistry(ctx, registry, "greeter", 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err := registry.ResolveService(context.Background(), "greeter"); err != nil {
		t.Fatalf("resolve after ttl = %v, want keepalive to refresh registration", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rpc server to stop")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForRegistry(ctx, registry, "greeter", 0); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPServerStateTransitions(t *testing.T) {
	s := NewServer(WithAddress("127.0.0.1:0"))
	if got := s.State().State; got != "initialized" {
		t.Fatalf("initial state = %q, want initialized", got)
	}
	if err := s.RegisterService(ServiceDesc{Name: "greeter", Methods: []MethodDesc{{
		Name:       "SayHello",
		NewRequest: func() any { return new(helloReq) },
		Handler: func(ctx context.Context, req any) (any, error) {
			return helloResp{Message: "hello"}, nil
		},
	}}}, nil); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForState(ctx, s, "running"); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rpc server to stop")
	}
	if got := s.State().State; got != "stopped" {
		t.Fatalf("stopped state = %q, want stopped", got)
	}
}

func waitForRegistry(ctx context.Context, registry *Registry, service string, want int) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		endpoints, err := registry.ResolveService(ctx, service)
		if want == 0 && err != nil {
			return nil
		}
		if err == nil && len(endpoints) == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForState(ctx context.Context, server *HTTPServer, want string) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if server.State().State == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
