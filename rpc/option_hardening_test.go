package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/retry"
	"github.com/imajinyun/gofly/core/security"
	controladmin "github.com/imajinyun/gofly/ops/admin"
)

func TestRPCServerOptionHardeningContracts(t *testing.T) {
	sinkCalled := false
	sink := controladmin.AuditSink(func(context.Context, controladmin.AuditEvent) {
		sinkCalled = true
	})

	var opts serverOptions
	WithServerAdminAuditSink(sink)(&opts)
	WithServerTLS("server.crt", "server.key")(&opts)
	if opts.adminAudit == nil {
		t.Fatal("admin audit sink was not configured")
	}
	opts.adminAudit(context.Background(), controladmin.AuditEvent{})
	if !sinkCalled {
		t.Fatal("admin audit sink was not invoked")
	}
	if opts.tls.CertFile != "server.crt" || opts.tls.KeyFile != "server.key" {
		t.Fatalf("server tls = %#v, want cert/key files", opts.tls)
	}

	cfg := security.TLSConfig{CertFile: "custom.crt", KeyFile: "custom.key", ClientCAFile: "ca.crt"}
	WithServerTLSConfig(cfg)(&opts)
	if opts.tls != cfg {
		t.Fatalf("server tls config = %#v, want %#v", opts.tls, cfg)
	}
}

func TestRPCClientOptionHardeningContracts(t *testing.T) {
	policy := retry.Policy{Attempts: 3, Backoff: time.Millisecond, ShouldRetry: func(error) bool { return false }}
	brk := breaker.New(breaker.WithFailureThreshold(1), breaker.WithOpenTimeout(time.Second))
	limiter := limit.NewAdaptiveLimiter()
	managerRules := governance.NewRuleSet(governance.Rule{Name: "manager", Transport: governance.TransportRPC, Service: "svc", Method: "Unary"})
	manager := governance.MustNewManager(governance.Config{}, governance.WithRuleSet(managerRules))
	suite := governance.MustNewSuite(governance.NewPlugin("suite", governance.Rule{Name: "suite", Transport: governance.TransportRPC, Service: "svc", Method: "Unary"}))

	var opts clientOptions
	WithRetryPolicy(policy)(&opts)
	WithBreaker(brk)(&opts)
	WithClientAdaptiveLimiter(limiter)(&opts)
	WithClientGovernanceSuite(suite)(&opts)
	if opts.retryPolicy.Attempts != 3 || opts.retryPolicy.Backoff != time.Millisecond || opts.retryPolicy.ShouldRetry == nil {
		t.Fatalf("retry policy = %#v, want configured policy", opts.retryPolicy)
	}
	if opts.breaker != brk {
		t.Fatalf("breaker = %#v, want configured breaker", opts.breaker)
	}
	if len(opts.middlewares) != 1 || len(opts.streamMiddlewares) != 2 {
		t.Fatalf("middleware counts = %d/%d, want unary limiter and breaker+limiter stream middleware", len(opts.middlewares), len(opts.streamMiddlewares))
	}
	if opts.rules == nil || len(opts.rules.Snapshot()) != 1 || opts.rules.Snapshot()[0].Name != "suite" {
		t.Fatalf("suite rules = %#v, want suite rule merged", opts.rules.Snapshot())
	}

	WithClientGovernanceManager(manager)(&opts)
	if opts.manager != manager || opts.rules != managerRules {
		t.Fatalf("manager/rules = %#v/%#v, want manager rule set", opts.manager, opts.rules)
	}
	WithClientGovernanceSuite(governance.MustNewSuite(governance.NewPlugin("ignored", governance.Rule{Name: "ignored", Transport: governance.TransportRPC, Service: "svc", Method: "Unary"})))(&opts)
	if got := opts.rules.Snapshot(); len(got) != 1 || got[0].Name != "manager" {
		t.Fatalf("rules after suite with manager = %#v, want manager rules unchanged", got)
	}
}

func TestRPCClientInternalHelpersHardeningContracts(t *testing.T) {
	c, err := NewClient("http://127.0.0.1:1", WithClientSingleflight())
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadataContext("tenant", "gofly")
	first, err := c.singleflightKey(ctx, "svc/Unary", helloReq{Name: "one"})
	if err != nil {
		t.Fatalf("singleflightKey first: %v", err)
	}
	second, err := c.singleflightKey(ctx, "svc/Unary", helloReq{Name: "one"})
	if err != nil {
		t.Fatalf("singleflightKey second: %v", err)
	}
	if first == "" || first != second {
		t.Fatalf("singleflight keys = %q/%q, want stable non-empty key", first, second)
	}

	result := &callResult{Payload: []byte(`{"message":"hello"}`), Metadata: metadataContextMap("trace", "abc")}
	clone := cloneCallResult(result)
	clone.Payload[0] = '['
	clone.Metadata["trace"] = "mutated"
	if string(result.Payload) != `{"message":"hello"}` || result.Metadata.Get("trace") != "abc" {
		t.Fatalf("clone mutation leaked into original: payload=%s metadata=%v", string(result.Payload), result.Metadata)
	}
	if cloneCallResult(nil) != nil {
		t.Fatal("cloneCallResult(nil) should return nil")
	}

	var resp helloResp
	if err := c.unmarshalResponsePayload(nil, &resp); err != nil {
		t.Fatalf("unmarshal nil payload: %v", err)
	}
	if err := c.unmarshalResponsePayload([]byte(`{`), &resp); err == nil {
		t.Fatal("unmarshal invalid payload succeeded, want error")
	}

	reporter := &recordingEndpointReporter{}
	c.opts.balancer = reporter
	reportErr := errors.New("boom")
	c.reportEndpoint(context.Background(), "http://endpoint", &reportErr)
	c.reportEndpoint(context.Background(), "http://endpoint", nil)
	if len(reporter.reports) != 2 || reporter.reports[0] != reportErr || reporter.reports[1] != nil {
		t.Fatalf("reports = %#v, want error then nil", reporter.reports)
	}

	c.opts.resolver = NewStaticResolver(" http://endpoint/ ")
	c.opts.balancer = nil
	target, _, err := c.pickTarget(context.Background(), RPCBalancerPolicy{}, "")
	if err != nil {
		t.Fatalf("pickTarget default balancer: %v", err)
	}
	if target != "http://endpoint" {
		t.Fatalf("target = %q, want trimmed endpoint", target)
	}
}

func TestRPCPolicyValidateAndGovernanceMapping(t *testing.T) {
	policy := RPCPolicyFromGovernance(governance.Policy{
		Timeout:  2 * time.Second,
		Retry:    governance.RetryPolicy{Attempts: 3, Backoff: time.Millisecond},
		Breaker:  governance.BreakerPolicy{Enabled: true, OpenTimeout: time.Second},
		Metadata: map[string]string{"tenant": "alpha"},
		Headers:  map[string]string{"X-Gofly": "on"},
	})
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate mapped policy: %v", err)
	}
	policy.Metadata["tenant"] = "mutated"
	policy.Headers["X-Gofly"] = "off"
	mappedAgain := RPCPolicyFromGovernance(governance.Policy{
		Metadata: map[string]string{"tenant": "alpha"},
		Headers:  map[string]string{"X-Gofly": "on"},
	})
	if mappedAgain.Metadata["tenant"] != "alpha" || mappedAgain.Headers["X-Gofly"] != "on" {
		t.Fatalf("mapped policy was not defensively copied: %#v", mappedAgain)
	}

	tests := []struct {
		name   string
		policy RPCPolicy
		want   string
	}{
		{name: "negative timeout", policy: RPCPolicy{Timeout: -time.Second}, want: "timeout"},
		{name: "negative retry", policy: RPCPolicy{Retry: governance.RetryPolicy{Attempts: -1}}, want: "retry attempts"},
		{name: "single hedge attempt", policy: RPCPolicy{Hedge: RPCHedgePolicy{Enabled: true, Attempts: 1}}, want: "hedge attempts"},
		{name: "fallback missing target", policy: RPCPolicy{Fallback: RPCFallbackPolicy{Enabled: true}}, want: "fallback target"},
		{name: "unknown balancer", policy: RPCPolicy{Balancer: RPCBalancerPolicy{Name: "least_request"}}, want: "unsupported"},
		{name: "negative balancer weight", policy: RPCPolicy{Balancer: RPCBalancerPolicy{Name: RPCBalancerWeightedRoundRobin, Weights: map[string]int{"a": -1}}}, want: "non-negative"},
		{name: "empty method policy key", policy: RPCPolicy{Methods: map[string]RPCPolicy{" ": {Timeout: time.Millisecond}}}, want: "method key"},
		{name: "invalid method policy", policy: RPCPolicy{Methods: map[string]RPCPolicy{"svc/Foo": {Timeout: -time.Millisecond}}}, want: "method \"svc/Foo\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRPCPolicyValidateAdditionalBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		policy RPCPolicy
		want   string
	}{
		{name: "negative retry backoff", policy: RPCPolicy{Retry: governance.RetryPolicy{Backoff: -time.Millisecond}}, want: "retry backoff"},
		{name: "negative hedge delay", policy: RPCPolicy{Hedge: RPCHedgePolicy{Delay: -time.Millisecond}}, want: "hedge delay"},
		{name: "negative hedge attempts", policy: RPCPolicy{Hedge: RPCHedgePolicy{Attempts: -1}}, want: "hedge attempts"},
		{name: "negative breaker open timeout", policy: RPCPolicy{Breaker: governance.BreakerPolicy{OpenTimeout: -time.Second}}, want: "breaker open timeout"},
		{name: "negative breaker window", policy: RPCPolicy{Breaker: governance.BreakerPolicy{Window: -time.Second}}, want: "breaker window"},
		{name: "negative load shedder max concurrency", policy: RPCPolicy{LoadShedder: RPCLoadShedderPolicy{MaxConcurrency: -1}}, want: "max concurrency"},
		{name: "negative load shedder max inflight", policy: RPCPolicy{LoadShedder: RPCLoadShedderPolicy{MaxInflight: -1}}, want: "max inflight"},
		{name: "negative load shedder min window", policy: RPCPolicy{LoadShedder: RPCLoadShedderPolicy{MinWindow: -time.Second}}, want: "min window"},
		{name: "empty weighted endpoint", policy: RPCPolicy{Balancer: RPCBalancerPolicy{Name: RPCBalancerWeightedRoundRobin, Weights: map[string]int{" ": 1}}}, want: "endpoint is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestRPCPolicyMergeAndBalancerHelpers(t *testing.T) {
	base := RPCPolicy{
		Timeout:  time.Second,
		Retry:    governance.RetryPolicy{Attempts: 1, Backoff: time.Millisecond, Statuses: []int{500}, Methods: []string{"Base"}},
		Metadata: map[string]string{"base": "kept", "shared": "base"},
		Headers:  map[string]string{"X-Base": "1"},
		Methods: map[string]RPCPolicy{
			"svc/Foo": {Timeout: time.Millisecond, Metadata: map[string]string{"base": "method"}},
		},
	}
	override := RPCPolicy{
		Timeout:  2 * time.Second,
		Retry:    governance.RetryPolicy{Attempts: 3, Backoff: 2 * time.Millisecond, Statuses: []int{503}, Methods: []string{"Override"}},
		Breaker:  governance.BreakerPolicy{Enabled: true, OpenTimeout: time.Second},
		Hedge:    RPCHedgePolicy{Delay: time.Millisecond, Attempts: 3},
		Fallback: RPCFallbackPolicy{Target: "http://fallback", Method: "svc/Fallback"},
		LoadShedder: RPCLoadShedderPolicy{
			MaxInflight: 2,
			MinWindow:   time.Second,
		},
		Balancer: RPCBalancerPolicy{Name: RPCBalancerWeightedRoundRobin, Weights: map[string]int{"http://a": 2}},
		Metadata: map[string]string{"shared": "override", "extra": "yes"},
		Headers:  map[string]string{"X-Override": "1"},
		Methods: map[string]RPCPolicy{
			"svc/Foo": {Retry: governance.RetryPolicy{Attempts: 4}, Metadata: map[string]string{"override": "method"}},
			"Bar":     {Timeout: 3 * time.Second},
		},
	}
	merged := mergeRPCPolicy(base, override)
	if merged.Timeout != 2*time.Second || merged.Retry.Attempts != 3 || merged.Retry.Backoff != 2*time.Millisecond || !merged.Breaker.Enabled {
		t.Fatalf("merged scalar policy = %#v, want override values", merged)
	}
	if merged.Hedge.Attempts != 3 || merged.Fallback.Method != "svc/Fallback" || merged.LoadShedder.MaxInflight != 2 || merged.Balancer.Name != RPCBalancerWeightedRoundRobin {
		t.Fatalf("merged advanced policy = %#v, want override advanced fields", merged)
	}
	if merged.Metadata["base"] != "kept" || merged.Metadata["shared"] != "override" || merged.Headers["X-Base"] != "1" || merged.Headers["X-Override"] != "1" {
		t.Fatalf("merged maps = metadata=%#v headers=%#v, want base plus override", merged.Metadata, merged.Headers)
	}
	if merged.Methods["svc/Foo"].Timeout != time.Millisecond || merged.Methods["svc/Foo"].Retry.Attempts != 4 || merged.Methods["svc/Foo"].Metadata["base"] != "method" || merged.Methods["svc/Foo"].Metadata["override"] != "method" || merged.Methods["Bar"].Timeout != 3*time.Second {
		t.Fatalf("merged method policies = %#v, want base plus method override", merged.Methods)
	}
	override.Balancer.Weights["http://a"] = 99
	if merged.Balancer.Weights["http://a"] != 2 {
		t.Fatalf("merged balancer weights alias override: %#v", merged.Balancer.Weights)
	}
	override.Methods["svc/Foo"] = RPCPolicy{Timeout: 99 * time.Second}
	if merged.Methods["svc/Foo"].Timeout != time.Millisecond {
		t.Fatalf("merged method policies alias override: %#v", merged.Methods["svc/Foo"])
	}
	nested := cloneRPCPolicy(RPCPolicy{Methods: map[string]RPCPolicy{"svc/Nested": {Methods: map[string]RPCPolicy{"ignored": {Timeout: time.Second}}}}})
	if nested.Methods["svc/Nested"].Methods != nil {
		t.Fatalf("cloned nested method policy = %#v, want leaf method policy", nested.Methods["svc/Nested"])
	}
	if got := mergeRPCPolicyStringMap(nil, nil); got != nil {
		t.Fatalf("mergeRPCPolicyStringMap(nil, nil) = %#v, want nil", got)
	}
	copyOnly := mergeRPCPolicyStringMap(nil, map[string]string{"k": "v"})
	if copyOnly["k"] != "v" {
		t.Fatalf("copy-only merge = %#v, want k=v", copyOnly)
	}

	ctx := metadata.NewContext(context.Background(), metadata.MD{"tenant": "alpha"})
	if got := rpcPolicyHashKey(ctx, " tenant "); got != "alpha" {
		t.Fatalf("rpcPolicyHashKey metadata = %q, want alpha", got)
	}
	if got := rpcPolicyHashKey(context.Background(), " fallback "); got != "fallback" {
		t.Fatalf("rpcPolicyHashKey fallback = %q, want fallback", got)
	}
	if got := rpcPolicyHashKey(context.Background(), " "); got != "" {
		t.Fatalf("rpcPolicyHashKey blank = %q, want empty", got)
	}

	client, err := NewClient("http://127.0.0.1:1", WithBalancer(NewConsistentHashBalancer()))
	if err != nil {
		t.Fatal(err)
	}
	if got := client.effectiveBalancerName(RPCBalancerPolicy{Name: " " + RPCBalancerHealth + " "}); got != RPCBalancerHealth {
		t.Fatalf("explicit balancer name = %q, want health", got)
	}
	if got := client.effectiveBalancerName(RPCBalancerPolicy{}); got != RPCBalancerConsistentHash {
		t.Fatalf("inferred balancer name = %q, want consistent_hash", got)
	}
	weightedClient, err := NewClient("http://127.0.0.1:1", WithBalancer(NewWeightedRoundRobinBalancer(map[string]int{"http://a": 1})))
	if err != nil {
		t.Fatal(err)
	}
	if got := weightedClient.effectiveBalancerName(RPCBalancerPolicy{}); got != RPCBalancerWeightedRoundRobin {
		t.Fatalf("weighted inferred balancer name = %q, want weighted_round_robin", got)
	}
	p2cClient, err := NewClient("http://127.0.0.1:1", WithBalancer(NewP2CBalancer()))
	if err != nil {
		t.Fatal(err)
	}
	if got := p2cClient.effectiveBalancerName(RPCBalancerPolicy{}); got != RPCBalancerP2C {
		t.Fatalf("p2c inferred balancer name = %q, want p2c", got)
	}
	healthClient, err := NewClient("http://127.0.0.1:1", WithBalancer(NewHealthBalancer()))
	if err != nil {
		t.Fatal(err)
	}
	if got := healthClient.effectiveBalancerName(RPCBalancerPolicy{}); got != RPCBalancerHealth {
		t.Fatalf("health inferred balancer name = %q, want health", got)
	}
	for _, tt := range []struct {
		name   string
		policy RPCBalancerPolicy
	}{
		{name: "weighted", policy: RPCBalancerPolicy{Name: RPCBalancerWeightedRoundRobin, Weights: map[string]int{"http://a": 1}}},
		{name: "p2c", policy: RPCBalancerPolicy{Name: RPCBalancerP2C}},
		{name: "consistent", policy: RPCBalancerPolicy{Name: RPCBalancerConsistentHash, Key: "tenant"}},
		{name: "health", policy: RPCBalancerPolicy{Name: RPCBalancerHealth}},
		{name: "default", policy: RPCBalancerPolicy{Name: "unknown"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if balancer := newRPCPolicyBalancer(tt.policy); balancer == nil {
				t.Fatalf("newRPCPolicyBalancer(%#v) = nil", tt.policy)
			}
		})
	}
}

func metadataContext(key, value string) context.Context {
	return metadata.NewContext(context.Background(), metadata.MD{key: value})
}

func metadataContextMap(key, value string) metadata.MD {
	return metadata.MD{key: value}
}

type recordingEndpointReporter struct {
	reports []error
}

func (r *recordingEndpointReporter) Pick(ctx context.Context, endpoints []string) (string, error) {
	return endpoints[0], nil
}

func (r *recordingEndpointReporter) Report(ctx context.Context, endpoint string, err error) {
	r.reports = append(r.reports, err)
}
