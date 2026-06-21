package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/retry"
	"github.com/gofly/gofly/core/security"
	controladmin "github.com/gofly/gofly/ops/admin"
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
