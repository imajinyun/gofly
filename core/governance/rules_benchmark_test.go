package governance

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

var benchmarkDecisionSink Decision

func BenchmarkRuleSetDecide(b *testing.B) {
	rules := benchmarkRuleSet(256)
	requests := map[string]Request{
		"early":  {Transport: TransportREST, Service: "svc-000", Method: http.MethodGet, Path: "/v1/svc-000/items/42", Tags: map[string]string{"zone": "cn"}},
		"middle": {Transport: TransportREST, Service: "svc-128", Method: http.MethodGet, Path: "/v1/svc-128/items/42", Tags: map[string]string{"zone": "cn"}},
		"late":   {Transport: TransportREST, Service: "svc-255", Method: http.MethodGet, Path: "/v1/svc-255/items/42", Tags: map[string]string{"zone": "cn"}},
		"miss":   {Transport: TransportREST, Service: "missing", Method: http.MethodGet, Path: "/v1/missing/items/42", Tags: map[string]string{"zone": "cn"}},
	}

	for name, req := range requests {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchmarkDecisionSink = rules.Decide(req)
			}
		})
	}
}

func BenchmarkRuleSetMatch(b *testing.B) {
	rules := benchmarkRuleSet(256)
	req := Request{Transport: TransportREST, Service: "svc-128", Method: http.MethodGet, Path: "/v1/svc-128/items/42", Tags: map[string]string{"zone": "cn"}}

	b.ReportAllocs()
	for b.Loop() {
		benchmarkDecisionSink = rules.Match(req)
	}
}

func BenchmarkManagerDecide(b *testing.B) {
	manager := MustNewManager(Config{Rules: benchmarkRules(256)})
	req := Request{Transport: TransportREST, Service: "svc-128", Method: http.MethodGet, Path: "/v1/svc-128/items/42", Tags: map[string]string{"zone": "cn"}}

	b.ReportAllocs()
	for b.Loop() {
		benchmarkDecisionSink = manager.Decide(req)
	}
}

func benchmarkRuleSet(count int) *RuleSet {
	return NewRuleSet(benchmarkRules(count)...)
}

func benchmarkRules(count int) []Rule {
	rules := make([]Rule, 0, count)
	for i := range count {
		service := fmt.Sprintf("svc-%03d", i)
		rules = append(rules, Rule{
			Name:      service,
			Transport: TransportREST,
			Service:   service,
			Method:    http.MethodGet,
			Path:      fmt.Sprintf("/v1/%s/items/*", service),
			Tags:      map[string]string{"zone": "cn"},
			Policy: Policy{
				Timeout:   time.Second,
				Retry:     RetryPolicy{Attempts: 2, Backoff: time.Millisecond, Statuses: []int{500, 503}, Methods: []string{http.MethodGet}},
				Headers:   map[string]string{"X-Gofly-Service": service},
				Metadata:  map[string]string{"owner": "platform"},
				RateLimit: RateLimitPolicy{Rate: 100, Burst: 200},
			},
		})
	}
	return rules
}
