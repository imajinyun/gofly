// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"context"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/rpc"
)

// governedRoute returns a copy of route with governance policies applied.
func (g *Gateway) governedRoute(r *http.Request, route Route) Route {
	if g == nil || g.rules == nil || r == nil {
		return route
	}
	decision := g.rules.Match(governance.HTTPRequest(governance.TransportGateway, route.Service, r, route.Tags))
	if !decision.Matched {
		return route
	}
	governed := g.applyGovernancePolicy(route, decision.Policy)
	governed.governanceKey = governanceRuntimeKey(decision, routeKey(route))
	return governed
}

func applyGovernancePolicy(route Route, policy governance.Policy) Route {
	return (*Gateway)(nil).applyGovernancePolicy(route, policy)
}

func (g *Gateway) applyGovernancePolicy(route Route, policy governance.Policy) Route {
	if policy.Timeout > 0 {
		route.Timeout = policy.Timeout
	}
	if policy.MaxBodyBytes > 0 {
		route.MaxBodyBytes = policy.MaxBodyBytes
	}
	if policy.Retry.Attempts > 0 {
		route.Retry.Attempts = policy.Retry.Attempts
	}
	if policy.Retry.Backoff > 0 {
		route.Retry.Backoff = policy.Retry.Backoff
	}
	if len(policy.Retry.Statuses) > 0 {
		route.Retry.Statuses = append([]int(nil), policy.Retry.Statuses...)
	}
	if len(policy.Retry.Methods) > 0 {
		route.Retry.Methods = append([]string(nil), policy.Retry.Methods...)
	}
	if policy.Breaker.Enabled {
		route.Breaker = BreakerConfig{
			Enabled:      true,
			OpenTimeout:  policy.Breaker.OpenTimeout,
			Window:       policy.Breaker.Window,
			Buckets:      policy.Breaker.Buckets,
			MinRequests:  policy.Breaker.MinRequests,
			FailureRatio: policy.Breaker.FailureRatio,
		}
	}
	if policy.RateLimit.Rate > 0 || policy.RateLimit.Burst > 0 {
		route.RateLimit = RateLimitConfig{Rate: policy.RateLimit.Rate, Burst: policy.RateLimit.Burst}
	}
	if policy.Concurrency.Limit > 0 {
		route.Concurrency = ConcurrencyConfig{Limit: policy.Concurrency.Limit}
	}
	if len(policy.Headers) > 0 {
		route.Headers = cloneMap(route.Headers)
		if route.Headers == nil {
			route.Headers = make(map[string]string, len(policy.Headers))
		}
		for key, value := range policy.Headers {
			route.Headers[key] = value
		}
	}
	if policy.Canary.Ratio > 0 || policy.Canary.Service != "" || policy.Canary.Target != "" || len(policy.Canary.Targets) > 0 || len(policy.Canary.MatchHeaders) > 0 || len(policy.Canary.MatchCookies) > 0 {
		canary := CanaryRoute{
			Target:         policy.Canary.Target,
			Targets:        append([]string(nil), policy.Canary.Targets...),
			Service:        policy.Canary.Service,
			Resolver:       g.resolver(policy.Canary.Service),
			Ratio:          policy.Canary.Ratio,
			Headers:        cloneMap(policy.Canary.Headers),
			MatchHeaders:   cloneMap(policy.Canary.MatchHeaders),
			MatchCookies:   cloneMap(policy.Canary.MatchCookies),
			UpstreamPrefix: policy.Canary.UpstreamPrefix,
		}
		route.Canary = append(route.Canary, cloneCanaryRoutes([]CanaryRoute{canary})...)
	}
	return route
}

func (g *Gateway) resolver(service string) rpc.Resolver {
	if g == nil || service == "" || len(g.resolvers) == 0 {
		return nil
	}
	return g.resolvers[service]
}

func (g *Gateway) canaryRoute(r *http.Request, route Route) Route {
	for _, canary := range route.Canary {
		if !canary.matches(r) {
			continue
		}
		if canary.Resolver == nil && len(canary.Targets) == 0 {
			continue
		}
		return canary.apply(route)
	}
	return route
}

func (c CanaryRoute) matches(r *http.Request) bool {
	if r == nil {
		return false
	}
	matchedPredicate := false
	for key, value := range c.MatchHeaders {
		matchedPredicate = true
		if r.Header.Get(key) != value {
			return false
		}
	}
	for key, value := range c.MatchCookies {
		matchedPredicate = true
		cookie, err := r.Cookie(key)
		if err != nil || cookie.Value != value {
			return false
		}
	}
	if c.Ratio <= 0 {
		return matchedPredicate
	}
	ratio := c.Ratio
	if ratio > 1 {
		ratio = 1
	}
	return canaryBucket(r) < uint32(ratio*1_000_000)
}

func (c CanaryRoute) apply(route Route) Route {
	out := route
	if c.Name != "" {
		out.Name = c.Name
	}
	if c.Service != "" {
		out.Service = c.Service
	}
	if c.UpstreamPrefix != "" {
		out.UpstreamPrefix = c.UpstreamPrefix
	}
	if c.Resolver != nil {
		out.Resolver = c.Resolver
	}
	if len(c.Targets) > 0 {
		out.Targets = append([]string(nil), c.Targets...)
		if c.Resolver == nil {
			out.Resolver = rpc.NewStaticResolver(c.Targets...)
		}
	}
	out.Headers = cloneMap(route.Headers)
	if len(c.Headers) > 0 {
		if out.Headers == nil {
			out.Headers = make(map[string]string, len(c.Headers))
		}
		for key, value := range c.Headers {
			out.Headers[key] = value
		}
	}
	return out
}

func canaryBucket(r *http.Request) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(r.Method))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(r.Host))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(r.URL.Path))
	_, _ = h.Write([]byte{0})
	if value := r.Header.Get("X-Request-Id"); value != "" {
		_, _ = h.Write([]byte(value))
	} else if value = r.Header.Get(HeaderForwardedFor); value != "" {
		_, _ = h.Write([]byte(value))
	} else {
		_, _ = h.Write([]byte(r.RemoteAddr))
	}
	return h.Sum32() % 1_000_000
}

func (g *Gateway) shadow(ctx context.Context, r *http.Request, route Route, body []byte) {
	for _, shadow := range route.Shadow {
		if shadow.SampleRatio <= 0 {
			continue
		}
		if shadow.SampleRatio < 1 && time.Now().UnixNano()%1_000_000 >= int64(shadow.SampleRatio*1_000_000) {
			continue
		}
		if g.stats != nil {
			g.stats.IncShadow(routeKey(route))
		}
		if !g.enqueueShadow(context.WithoutCancel(ctx), r, route, shadow, body) && g.stats != nil {
			g.stats.IncShadowDropped(routeKey(route))
		}
	}
}

func (g *Gateway) sendShadow(ctx context.Context, r *http.Request, route Route, shadow ShadowRoute, body []byte) {
	timeout := shadow.Timeout
	if timeout <= 0 {
		timeout = route.Timeout
	}
	if timeout <= 0 {
		timeout = g.timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	endpoint := strings.TrimRight(strings.TrimSpace(shadow.Target), "/")
	if endpoint == "" && shadow.Resolver != nil {
		endpoints, err := shadow.Resolver.Resolve(ctx)
		if err != nil || len(endpoints) == 0 {
			return
		}
		endpoint = endpoints[0]
	}
	if endpoint == "" {
		return
	}
	shadowRoute := route
	shadowRoute.UpstreamPrefix = shadow.UpstreamPrefix
	target, err := buildTargetURL(endpoint, shadowRoute, r.URL)
	if err != nil {
		return
	}
	out, err := cloneProxyRequest(r.WithContext(ctx), target, route, body)
	if err != nil {
		return
	}
	for key, value := range shadow.Headers {
		out.Header.Set(key, value)
	}
	resp, err := g.client.Do(out)
	if err != nil {
		slog.DebugContext(ctx, "gateway shadow request failed", "route", routeKey(route), "error", err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain shadow response
	_ = resp.Body.Close()                 // best-effort cleanup
}
