// Package gateway provides an HTTP reverse proxy, request router and protocol
// gateway for gofly services with governance, discovery and load balancing.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/limit"
)

// retryRuntime tracks per-route and per-upstream retry budgets.
type retryRuntime struct {
	mu       sync.Mutex
	route    map[string]*limit.Limiter
	upstream map[string]*limit.Limiter
}

func newRetryRuntime() *retryRuntime {
	return &retryRuntime{
		route:    make(map[string]*limit.Limiter),
		upstream: make(map[string]*limit.Limiter),
	}
}

// allowRetry reports whether a retry is permitted by the budget.
func (g *Gateway) allowRetry(route Route, endpoint string, policy RetryPolicy) bool {
	if g == nil || g.retryRuntime == nil || policy.BudgetRate <= 0 {
		return true
	}
	key := routeKey(route)
	if !g.retryRuntime.allow(g.retryRuntime.route, key, policy.BudgetRate, policy.BudgetBurst) {
		return false
	}
	if endpoint != "" && !g.retryRuntime.allow(g.retryRuntime.upstream, key+"|"+endpoint, policy.BudgetRate, policy.BudgetBurst) {
		return false
	}
	return true
}

func (r *retryRuntime) allow(bucket map[string]*limit.Limiter, key string, rate int, burst int) bool {
	r.mu.Lock()
	limiter := bucket[key]
	if limiter == nil {
		limiter = limit.New(rate, burst)
		bucket[key] = limiter
	}
	r.mu.Unlock()
	return limiter.Allow()
}

type shadowTask struct {
	gateway *Gateway
	ctx     context.Context
	req     *http.Request
	route   Route
	shadow  ShadowRoute
	body    []byte
}

type shadowPool struct {
	tasks chan shadowTask
	wg    sync.WaitGroup
	once  sync.Once
}

func newShadowPool(workers int, queue int) *shadowPool {
	p := &shadowPool{tasks: make(chan shadowTask, queue)}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for task := range p.tasks {
				if task.gateway != nil {
					task.gateway.sendShadow(task.ctx, task.req, task.route, task.shadow, task.body)
				}
			}
		}()
	}
	return p
}

func (g *Gateway) enqueueShadow(ctx context.Context, r *http.Request, route Route, shadow ShadowRoute, body []byte) bool {
	if g == nil || g.shadowPool == nil {
		return false
	}
	task := shadowTask{gateway: g, ctx: ctx, req: r, route: route, shadow: shadow, body: append([]byte(nil), body...)}
	select {
	case g.shadowPool.tasks <- task:
		return true
	default:
		if g.logger != nil {
			g.logger.WarnContext(ctx, "gateway shadow queue full", "route", routeKey(route))
		}
		return false
	}
}

func (p *shadowPool) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	done := make(chan struct{})
	p.once.Do(func() { close(p.tasks) })
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func normalizeActiveHealthConfig(conf ActiveHealthConfig) ActiveHealthConfig {
	if conf.Path == "" {
		conf.Path = "/healthz"
	}
	if conf.Timeout <= 0 {
		conf.Timeout = time.Second
	}
	return conf
}

func (g *Gateway) probeActiveHealth(ctx context.Context, route Route, endpoints []string) error {
	conf := normalizeActiveHealthConfig(g.activeHealth)
	var errs []error
	for _, endpoint := range endpoints {
		if err := g.probeEndpoint(ctx, endpoint, conf); err != nil {
			g.reportEndpoint(route, endpoint, false)
			errs = append(errs, err)
			continue
		}
		g.reportEndpoint(route, endpoint, true)
	}
	if len(errs) == len(endpoints) {
		return fmt.Errorf("all active health probes failed: %v", errs)
	}
	return nil
}

func (g *Gateway) probeEndpoint(ctx context.Context, endpoint string, conf ActiveHealthConfig) error {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return fmt.Errorf("empty endpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, conf.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+conf.Path, nil)
	if err != nil {
		return err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("endpoint %s unhealthy: %d", endpoint, resp.StatusCode)
	}
	return nil
}
