// Package rest provides a configurable HTTP server with middleware support for
// gofly services. It includes built-in governance (rate limiting, circuit breaking,
// adaptive limiting), health checks, admin endpoints, OpenAPI generation, and
// Swagger UI serving.
package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	controladmin "github.com/gofly/gofly/ops/admin"
	"github.com/gofly/gofly/core/auth"
	"github.com/gofly/gofly/core/breaker"
	coreerrors "github.com/gofly/gofly/core/errors"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/metrics"
	"github.com/gofly/gofly/core/security"
	"github.com/gofly/gofly/core/trace"
)

// Option configures a Server.
type Option func(*Server)

// Server is a configurable HTTP server with middleware, governance, and admin support.
type Server struct {
	conf        Config
	mux         *http.ServeMux
	middlewares []Middleware
	routes      []RouteSpec
	httpServer  *http.Server
	health      map[string]CheckFunc
	ready       map[string]CheckFunc
	state       atomic.Int32
	stateSince  atomic.Int64
	governance  *governance.Registry
	rules       *governance.RuleSet
	manager     *governance.Manager
	ruleRuntime *ruleRuntime
	cpuReader   func() int
	adminAudit  controladmin.AuditSink
}

type ruleRuntime struct {
	mu          sync.Mutex
	rateLimits  map[string]*cachedRateLimiter
	concurrency map[string]*cachedConcurrencyLimiter
	breakers    map[string]*cachedBreaker
}

type cachedRateLimiter struct {
	rate    int
	burst   int
	limiter *limit.Limiter
}

type cachedConcurrencyLimiter struct {
	limit   int
	limiter *limit.ConcurrencyLimiter
}

type cachedBreaker struct {
	policy  governance.BreakerPolicy
	breaker *breaker.AdaptiveBreaker
}

// NewServer creates a Server from Config and options.
func NewServer(c Config, opts ...Option) (*Server, error) {
	c = applyConfigDefaults(c)
	if c.Timeout == 0 {
		c.Timeout = 3 * time.Second
	}
	s := &Server{conf: c, mux: http.NewServeMux(), health: make(map[string]CheckFunc), ready: make(map[string]CheckFunc), governance: governance.NewRegistry(), ruleRuntime: newRuleRuntime()}
	s.setState(serverStateInitialized)
	for _, opt := range opts {
		opt(s)
	}
	if s.conf.Middlewares.Health {
		s.AddHealthRoutes()
	}
	if s.conf.Admin.Enabled {
		s.AddAdminRoutes(s.conf.Admin)
	}
	return s, nil
}

func applyConfigDefaults(c Config) Config {
	if c.DisableDefaultMiddlewares {
		return c
	}
	c.Middlewares.Recover = true
	c.Middlewares.Trace = true
	c.Middlewares.Log = true
	c.Middlewares.Timeout = true
	c.Middlewares.Metrics = true
	c.Middlewares.Health = true
	c.Middlewares.RequestID = true
	if c.MaxBodyBytes == 0 && c.Middlewares.MaxBodyBytesConfig.Limit == 0 {
		c.MaxBodyBytes = defaultMaxBodyBytes
	}
	if c.Middlewares.SecurityHeaders == nil {
		c.Middlewares.SecurityHeaders = &SecurityHeadersConfig{}
	}
	return c
}

// MustNewServer is like NewServer but panics on error.
func MustNewServer(c Config, opts ...Option) *Server {
	s, err := NewServer(c, opts...)
	if err != nil {
		panic(err)
	}
	return s
}

// WithGovernanceRuleSet attaches a static governance rule set.
func WithGovernanceRuleSet(rules *governance.RuleSet) Option {
	return func(s *Server) {
		s.rules = rules
	}
}

// WithAdminAuditSink sets the audit sink for admin endpoints.
func WithAdminAuditSink(sink controladmin.AuditSink) Option {
	return func(s *Server) {
		s.adminAudit = sink
	}
}

// WithGovernanceManager attaches a governance manager and derives its rule set.
func WithGovernanceManager(manager *governance.Manager) Option {
	return func(s *Server) {
		s.manager = manager
		if manager != nil {
			s.rules = manager.RuleSet()
		}
	}
}

// WithGovernanceSuite merges a governance suite into the server rules.
func WithGovernanceSuite(suite *governance.Suite) Option {
	return func(s *Server) {
		if s.manager != nil {
			return
		}
		if suite != nil {
			s.rules = governance.MergeRuleSets(s.rules, suite.RuleSet())
		}
	}
}

// WithAdaptiveCPUReader injects a CPU load reader for adaptive REST limiters.
// The reader should return millicpu notation, so 900 means 90% utilization.
func WithAdaptiveCPUReader(reader func() int) Option {
	return func(s *Server) {
		s.cpuReader = reader
	}
}

// AddRoute registers a single route with the server.
func (s *Server) AddRoute(r Route, opts ...RouteOption) {
	ro := s.routeOptions(opts...)
	r.Path = joinPath(ro.prefix, r.Path)
	s.routes = append(s.routes, routeSpecFromRoute(r))
	pattern := r.Method + " " + r.Path
	handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.Handler(&Context{Response: w, Request: req})
	}))
	handler = s.dynamicGovernanceHandler(r, handler)
	middlewares := s.routeMiddlewares(r, ro)
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	s.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		handler.ServeHTTP(w, req)
	}))
}

func (s *Server) dynamicGovernanceHandler(route Route, next http.Handler) http.Handler {
	if s == nil || s.rules == nil || next == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if s.isAdminRequest(req) {
			next.ServeHTTP(w, req)
			return
		}
		governanceReq := governance.HTTPRequest(governance.TransportREST, s.conf.Name, req, routeTags(route.Tags))
		var decision governance.Decision
		if s.manager != nil {
			decision = s.manager.MatchContext(req.Context(), governanceReq)
		} else {
			decision = s.rules.Match(governanceReq)
		}
		if !decision.Matched {
			next.ServeHTTP(w, req)
			return
		}
		policy := decision.Policy
		key := governanceRuntimeKey(decision, route.Method+" "+route.Path)
		for key, value := range policy.Headers {
			req.Header.Set(key, value)
			w.Header().Set(key, value)
		}
		if canary := governance.SelectCanary(policy.Canary, governance.HTTPRequest(governance.TransportREST, s.conf.Name, req, routeTags(route.Tags))); canary.Selected {
			req.Header.Set(governance.HeaderCanary, "true")
			w.Header().Set(governance.HeaderCanary, "true")
			if canary.Service != "" {
				req.Header.Set(governance.HeaderCanaryService, canary.Service)
				w.Header().Set(governance.HeaderCanaryService, canary.Service)
			}
			for key, value := range canary.Headers {
				req.Header.Set(key, value)
				w.Header().Set(key, value)
			}
		}
		if limiter := s.ruleRateLimiter(key, policy.RateLimit); limiter != nil && !limiter.Allow() {
			writeError(w, http.StatusTooManyRequests, coreerrors.CodeResourceExhausted, "too many requests")
			return
		}
		if limiter := s.ruleConcurrencyLimiter(key, policy.Concurrency); limiter != nil {
			if !limiter.TryAcquire() {
				writeError(w, http.StatusServiceUnavailable, coreerrors.CodeUnavailable, "too many concurrent requests")
				return
			}
			defer limiter.Release()
		}
		if policy.MaxBodyBytes > 0 && req.Body != nil {
			if req.ContentLength > policy.MaxBodyBytes {
				http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
				return
			}
			req.Body = http.MaxBytesReader(w, req.Body, policy.MaxBodyBytes)
		}
		serve := func(rw http.ResponseWriter) {
			if policy.Timeout > 0 {
				TimeoutMiddleware(policy.Timeout)(next).ServeHTTP(rw, req)
				return
			}
			next.ServeHTTP(rw, req)
		}
		brk := s.ruleBreaker(key, policy.Breaker)
		if brk == nil {
			serve(w)
			return
		}
		if err := brk.Allow(); err != nil {
			writeError(w, http.StatusServiceUnavailable, coreerrors.CodeUnavailable, err.Error())
			return
		}
		sw := newStatusResponseWriter(w)
		serve(sw)
		if sw.status >= http.StatusInternalServerError {
			brk.MarkFailure()
			return
		}
		brk.MarkSuccess()
	})
}

func newRuleRuntime() *ruleRuntime {
	return &ruleRuntime{
		rateLimits:  make(map[string]*cachedRateLimiter),
		concurrency: make(map[string]*cachedConcurrencyLimiter),
		breakers:    make(map[string]*cachedBreaker),
	}
}

func governanceRuntimeKey(decision governance.Decision, fallback string) string {
	if decision.RuleKey != "" {
		return decision.RuleKey
	}
	if decision.RuleName != "" {
		return "name:" + decision.RuleName
	}
	return fallback
}

func (s *Server) ruleRateLimiter(key string, policy governance.RateLimitPolicy) *limit.Limiter {
	if s == nil || s.ruleRuntime == nil || policy.Rate <= 0 && policy.Burst <= 0 {
		return nil
	}
	rate := policy.Rate
	burst := policy.Burst
	if burst <= 0 {
		burst = rate
	}
	s.ruleRuntime.mu.Lock()
	defer s.ruleRuntime.mu.Unlock()
	cached := s.ruleRuntime.rateLimits[key]
	if cached != nil && cached.rate == rate && cached.burst == burst {
		return cached.limiter
	}
	limiter := limit.New(rate, burst)
	s.ruleRuntime.rateLimits[key] = &cachedRateLimiter{rate: rate, burst: burst, limiter: limiter}
	return limiter
}

func (s *Server) ruleConcurrencyLimiter(key string, policy governance.ConcurrencyPolicy) *limit.ConcurrencyLimiter {
	if s == nil || s.ruleRuntime == nil || policy.Limit <= 0 {
		return nil
	}
	s.ruleRuntime.mu.Lock()
	defer s.ruleRuntime.mu.Unlock()
	cached := s.ruleRuntime.concurrency[key]
	if cached != nil && cached.limit == policy.Limit {
		return cached.limiter
	}
	limiter := limit.NewConcurrency(policy.Limit)
	s.ruleRuntime.concurrency[key] = &cachedConcurrencyLimiter{limit: policy.Limit, limiter: limiter}
	return limiter
}

func (s *Server) ruleBreaker(key string, policy governance.BreakerPolicy) *breaker.AdaptiveBreaker {
	if s == nil || s.ruleRuntime == nil || !policy.Enabled {
		return nil
	}
	s.ruleRuntime.mu.Lock()
	defer s.ruleRuntime.mu.Unlock()
	cached := s.ruleRuntime.breakers[key]
	if cached != nil && cached.policy == policy {
		return cached.breaker
	}
	brk := adaptiveBreakerFromPolicy(policy)
	s.ruleRuntime.breakers[key] = &cachedBreaker{policy: policy, breaker: brk}
	return brk
}

func adaptiveBreakerFromPolicy(policy governance.BreakerPolicy) *breaker.AdaptiveBreaker {
	opts := make([]breaker.AdaptiveOption, 0, 5)
	if policy.OpenTimeout > 0 {
		opts = append(opts, breaker.WithAdaptiveOpenTimeout(policy.OpenTimeout))
	}
	if policy.Window > 0 {
		opts = append(opts, breaker.WithAdaptiveWindow(policy.Window))
	}
	if policy.Buckets > 0 {
		opts = append(opts, breaker.WithAdaptiveBuckets(policy.Buckets))
	}
	if policy.MinRequests > 0 {
		opts = append(opts, breaker.WithAdaptiveMinRequests(policy.MinRequests))
	}
	if policy.FailureRatio > 0 {
		opts = append(opts, breaker.WithAdaptiveFailureRatio(policy.FailureRatio))
	}
	return breaker.NewAdaptive(opts...)
}

func (s *Server) isAdminRequest(req *http.Request) bool {
	if s == nil || req == nil || !s.conf.Admin.Enabled {
		return false
	}
	prefix := cleanPrefix(s.conf.Admin.PathPrefix)
	if prefix == "" {
		prefix = "/debug"
	}
	path := req.URL.Path
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func routeTags(tags []string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, tag := range tags {
		if tag != "" {
			out[tag] = "true"
		}
	}
	return out
}

// AddRoutes registers multiple routes with the server.
func (s *Server) AddRoutes(routes []Route, opts ...RouteOption) {
	for _, r := range routes {
		s.AddRoute(r, opts...)
	}
}

// AddHealthRoutes registers /startupz, /healthz, /readyz, /metrics, and /metrics.json.
func (s *Server) AddHealthRoutes() {
	s.AddRoute(Route{Method: http.MethodGet, Path: "/startupz", Handler: func(ctx *Context) {
		writeCheckReport(ctx, s.runChecks(ctx.Request.Context(), s.health))
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/healthz", Handler: func(ctx *Context) {
		writeCheckReport(ctx, s.runChecks(ctx.Request.Context(), s.health))
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/readyz", Handler: func(ctx *Context) {
		writeCheckReport(ctx, s.runChecks(ctx.Request.Context(), s.ready))
	}})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/metrics", Handler: PrometheusMetricsHandler(nil)})
	s.AddRoute(Route{Method: http.MethodGet, Path: "/metrics.json", Handler: MetricsHandler(nil)})
}

// AddAdminRoutes registers admin endpoints such as /config, /runtime, /state, /health,
// /governance, /routes, /openapi.json, /diagnostics, and optional pprof handlers.
func (s *Server) AddAdminRoutes(c AdminConfig) {
	prefix := cleanPrefix(c.PathPrefix)
	if prefix == "" {
		prefix = "/debug"
	}
	opts := []RouteOption{WithPrefix(prefix)}
	if c.Audit && s.adminAudit == nil {
		s.adminAudit = controladmin.SlogAuditSink(nil)
	}
	if s.adminAudit != nil {
		opts = append(opts, WithMiddlewares(controladmin.AuditMiddleware("rest", s.adminAudit)))
	}
	if c.Token != "" {
		opts = append(opts, WithAuth(auth.StaticTokenValidator(c.Token, "admin")))
	} else {
		opts = append(opts, WithMiddlewares(localAdminOnlyMiddleware()))
	}
	s.AddRoute(Route{Method: http.MethodGet, Path: "/config", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, s.conf)
	}}, opts...)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/runtime", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, metrics.Default.Snapshot())
	}}, opts...)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/state", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, s.State())
	}}, opts...)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/health", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, map[string]CheckReport{
			"health": s.runChecks(ctx.Request.Context(), s.health),
			"ready":  s.runChecks(ctx.Request.Context(), s.ready),
		})
	}}, opts...)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/governance", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, s.Governance())
	}}, opts...)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/governance/explain", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, s.ExplainGovernance(ctx.Request))
	}}, opts...)
	governanceAdmin := governance.NewAdmin(s.rules, s.governance,
		governance.WithAdminPathPrefix(prefix+"/governance"),
		governance.WithAdminDefaultRequest(governance.Request{Transport: governance.TransportREST, Service: s.conf.Name}),
		governance.WithAdminManager(s.manager),
	)
	for _, route := range controladmin.GovernanceEndpoints() {
		route := route
		s.AddRoute(Route{Method: route.Method, Path: route.Path, Handler: func(ctx *Context) {
			governanceAdmin.ServeHTTP(ctx.Response, ctx.Request)
		}}, opts...)
	}
	s.AddRoute(Route{Method: http.MethodGet, Path: "/routes", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, s.Routes())
	}}, opts...)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/openapi.json", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, s.OpenAPI(OpenAPIInfo{}))
	}}, opts...)
	s.AddRoute(Route{Method: http.MethodGet, Path: "/diagnostics", Handler: func(ctx *Context) {
		ctx.JSON(http.StatusOK, s.Diagnostics(ctx.Request.Context()))
	}}, opts...)
	if c.Pprof {
		s.AddRoute(Route{Method: http.MethodGet, Path: "/pprof/", Handler: func(ctx *Context) {
			pprof.Index(ctx.Response, ctx.Request)
		}}, opts...)
		s.AddRoute(Route{Method: http.MethodGet, Path: "/pprof/{name}", Handler: func(ctx *Context) {
			pprof.Index(ctx.Response, ctx.Request)
		}}, opts...)
		s.AddRoute(Route{Method: http.MethodGet, Path: "/pprof/cmdline", Handler: func(ctx *Context) {
			pprof.Cmdline(ctx.Response, ctx.Request)
		}}, opts...)
		s.AddRoute(Route{Method: http.MethodGet, Path: "/pprof/profile", Handler: func(ctx *Context) {
			pprof.Profile(ctx.Response, ctx.Request)
		}}, opts...)
		s.AddRoute(Route{Method: http.MethodGet, Path: "/pprof/symbol", Handler: func(ctx *Context) {
			pprof.Symbol(ctx.Response, ctx.Request)
		}}, opts...)
		s.AddRoute(Route{Method: http.MethodPost, Path: "/pprof/symbol", Handler: func(ctx *Context) {
			pprof.Symbol(ctx.Response, ctx.Request)
		}}, opts...)
		s.AddRoute(Route{Method: http.MethodGet, Path: "/pprof/trace", Handler: func(ctx *Context) {
			pprof.Trace(ctx.Response, ctx.Request)
		}}, opts...)
	}
}

func localAdminOnlyMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !security.IsLocalRemote(r.RemoteAddr) {
				writeError(w, http.StatusForbidden, coreerrors.CodePermissionDenied, "admin is only available from localhost without token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Use appends global middleware to the server.
func (s *Server) Use(mw Middleware) { s.middlewares = append(s.middlewares, mw) }

func (s *Server) routeOptions(opts ...RouteOption) routeOptions {
	ro := routeOptions{
		maxBodyBytes: s.maxBodyBytes(),
		recover:      boolPtr(s.conf.Middlewares.Recover),
		trace:        boolPtr(s.conf.Middlewares.Trace),
		log:          boolPtr(s.conf.Middlewares.Log),
		metrics:      boolPtr(s.conf.Middlewares.Metrics),
		breaker:      boolPtr(s.conf.Middlewares.Breaker),
		requestID:    boolPtr(s.conf.Middlewares.RequestID),
		trimStrings:  s.conf.Middlewares.TrimStrings,
		trimConfig:   s.conf.Middlewares.TrimStringsConfig,
	}
	if s.conf.Middlewares.Timeout {
		ro.timeout = s.timeoutDuration()
	}
	if s.conf.Middlewares.RateLimit {
		rate, burst := defaultRateLimit(s.conf.Middlewares.RateLimitConfig)
		ro.rateLimit = &routeRateLimit{
			enabled: true,
			rate:    rate,
			burst:   burst,
		}
	}
	if s.conf.Middlewares.AdaptiveRateLimit {
		ro.adaptive = &routeAdaptiveLimit{enabled: true, config: s.conf.Middlewares.AdaptiveLimitConfig}
	}
	if s.conf.Middlewares.MaxConcurrency {
		ro.concurrency = &routeConcurrencyLimit{
			enabled: true,
			limit:   defaultMaxConcurrency(s.conf.Middlewares.MaxConcurrencyConfig),
		}
	}
	for _, opt := range opts {
		opt(&ro)
	}
	return ro
}

func (s *Server) routeMiddlewares(r Route, ro routeOptions) []Middleware {
	middlewares := make([]Middleware, 0, 12+len(ro.middlewares)+len(r.Middlewares))
	if s.conf.Middlewares.SecurityHeaders != nil {
		middlewares = append(middlewares, SecurityHeadersMiddleware(*s.conf.Middlewares.SecurityHeaders))
	}
	if boolEnabled(ro.trace) {
		middlewares = append(middlewares, TraceMiddlewareWithSampler(s.conf.Name, traceSamplerFromConfig(s.conf.Middlewares.TraceSampling)))
	}
	if boolEnabled(ro.requestID) {
		middlewares = append(middlewares, RequestIDMiddleware())
	}
	middlewares = append(middlewares, RouteInfoMiddleware(r.Method, r.Path))
	if boolEnabled(ro.recover) {
		middlewares = append(middlewares, RecoverMiddleware())
	}
	if boolEnabled(ro.log) {
		middlewares = append(middlewares, LogMiddlewareWithConfig(samplerFromConfig(s.conf.Middlewares.LogSampling), s.conf.Middlewares.LogRedaction))
	}
	if boolEnabled(ro.metrics) {
		middlewares = append(middlewares, MetricsMiddleware(nil))
	}
	if ro.rateLimit != nil && ro.rateLimit.enabled {
		middlewares = append(middlewares, RateLimitMiddleware(ro.rateLimit.rate, ro.rateLimit.burst))
	}
	if ro.adaptive != nil && ro.adaptive.enabled {
		limiter := s.adaptiveLimiterFromConfig(ro.adaptive.config)
		target := r.Method + " " + r.Path
		s.governance.Register("adaptive-rate-limit", "adaptive_limiter", target, func() any { return limiter.Snapshot() })
		middlewares = append(middlewares, AdaptiveRateLimitMiddleware(limiter))
	}
	if ro.concurrency != nil && ro.concurrency.enabled {
		middlewares = append(middlewares, MaxConcurrencyMiddleware(ro.concurrency.limit))
	}
	if boolEnabled(ro.breaker) {
		brk := adaptiveBreakerFromConfig(s.conf.Middlewares.BreakerConfig)
		target := r.Method + " " + r.Path
		s.governance.Register("adaptive-breaker", "adaptive_breaker", target, func() any { return brk.Snapshot() })
		middlewares = append(middlewares, AdaptiveBreakerMiddleware(brk))
	}
	if ro.timeout > 0 {
		middlewares = append(middlewares, TimeoutMiddleware(ro.timeout))
	}
	if ro.maxBodyBytes > 0 {
		middlewares = append(middlewares, MaxBodyBytesMiddleware(ro.maxBodyBytes))
	}
	if trimStringsEnabled(ro.trimStrings) {
		middlewares = append(middlewares, TrimStringsMiddlewareWithConfig(ro.trimConfig))
	}
	if ro.auth != nil {
		middlewares = append(middlewares, BearerAuthMiddleware(ro.auth))
	}
	if s.conf.Middlewares.CSRF != nil {
		middlewares = append(middlewares, CSRFMiddleware(*s.conf.Middlewares.CSRF))
	}
	middlewares = append(middlewares, ro.middlewares...)
	middlewares = append(middlewares, r.Middlewares...)
	return middlewares
}

func boolEnabled(v *bool) bool { return v != nil && *v }

func (s *Server) timeoutDuration() time.Duration {
	if s.conf.Middlewares.TimeoutConfig.Duration > 0 {
		return s.conf.Middlewares.TimeoutConfig.Duration
	}
	return s.conf.Timeout
}

func (s *Server) healthTimeout() time.Duration {
	if s.conf.Middlewares.TimeoutConfig.HealthTimeout > 0 {
		return s.conf.Middlewares.TimeoutConfig.HealthTimeout
	}
	return s.conf.Timeout
}

func (s *Server) readHeaderTimeout() time.Duration {
	if s.conf.Middlewares.TimeoutConfig.ReadHeaderTimeout > 0 {
		return s.conf.Middlewares.TimeoutConfig.ReadHeaderTimeout
	}
	return s.conf.Timeout
}

func (s *Server) maxBodyBytes() int64 {
	if s.conf.Middlewares.MaxBodyBytesConfig.Limit > 0 {
		return s.conf.Middlewares.MaxBodyBytesConfig.Limit
	}
	return s.conf.MaxBodyBytes
}

type GovernanceSnapshot struct {
	Config     MiddlewaresConfig              `json:"config"`
	Components []governance.ComponentSnapshot `json:"components"`
	Rules      []governance.Rule              `json:"rules,omitempty"`
	RuleStats  []governance.RuleStats         `json:"ruleStats,omitempty"`
	RuleStatus governance.RuleSetStatus       `json:"ruleStatus,omitempty"`
	RuleEvents []governance.RuleSetEvent      `json:"ruleEvents,omitempty"`
}

// Governance returns a snapshot of middleware config and governance state.
func (s *Server) Governance() GovernanceSnapshot {
	snapshot := GovernanceSnapshot{Config: s.conf.Middlewares, Components: s.governance.Snapshots()}
	if s.rules != nil {
		snapshot.Rules = s.rules.Snapshot()
		snapshot.RuleStats = s.rules.Stats()
		snapshot.RuleStatus = s.rules.Status()
		snapshot.RuleEvents = s.rules.History()
	}
	return snapshot
}

// ExplainGovernance returns a human-readable explanation of which rules matched the request.
func (s *Server) ExplainGovernance(req *http.Request) governance.RuleExplain {
	request := governance.Request{Transport: governance.TransportREST}
	if s != nil {
		request.Service = s.conf.Name
	}
	return governance.NewAdmin(s.rules, nil, governance.WithAdminDefaultRequest(request)).Explain(req)
}

type DiagnosticsSnapshot struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Config      Config             `json:"config"`
	State       StateSnapshot      `json:"state"`
	Health      CheckReport        `json:"health"`
	Ready       CheckReport        `json:"ready"`
	Metrics     metrics.Snapshot   `json:"metrics"`
	Governance  GovernanceSnapshot `json:"governance"`
	Routes      []RouteSpec        `json:"routes"`
}

// Diagnostics returns a comprehensive snapshot of server health and configuration.
func (s *Server) Diagnostics(ctx context.Context) DiagnosticsSnapshot {
	return DiagnosticsSnapshot{
		GeneratedAt: time.Now(),
		Config:      s.conf,
		State:       s.State(),
		Health:      s.runChecks(ctx, s.health),
		Ready:       s.runChecks(ctx, s.ready),
		Metrics:     metrics.Default.Snapshot(),
		Governance:  s.Governance(),
		Routes:      s.Routes(),
	}
}

func traceSamplerFromConfig(conf *SamplingConfig) trace.Sampler {
	if conf == nil {
		return nil
	}
	root := samplerFromConfig(conf)
	if conf.ParentBased {
		return trace.ParentBasedSampler(root)
	}
	return root
}

func samplerFromConfig(conf *SamplingConfig) trace.Sampler {
	if conf == nil {
		return nil
	}
	return trace.RatioSampler(conf.Ratio)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func adaptiveBreakerFromConfig(conf BreakerConfig) *breaker.AdaptiveBreaker {
	opts := make([]breaker.AdaptiveOption, 0, 6)
	if conf.OpenTimeout > 0 {
		opts = append(opts, breaker.WithAdaptiveOpenTimeout(conf.OpenTimeout))
	}
	if conf.Window > 0 {
		opts = append(opts, breaker.WithAdaptiveWindow(conf.Window))
	}
	if conf.Buckets > 0 {
		opts = append(opts, breaker.WithAdaptiveBuckets(conf.Buckets))
	}
	if conf.MinRequests > 0 {
		opts = append(opts, breaker.WithAdaptiveMinRequests(conf.MinRequests))
	}
	if conf.FailureRatio > 0 {
		opts = append(opts, breaker.WithAdaptiveFailureRatio(conf.FailureRatio))
	}
	if conf.K > 0 {
		opts = append(opts, breaker.WithAdaptiveK(conf.K))
	}
	return breaker.NewAdaptive(opts...)
}

func (s *Server) adaptiveLimiterFromConfig(conf AdaptiveLimitConfig) *limit.AdaptiveLimiter {
	opts := make([]limit.AdaptiveLimiterOption, 0, 8)
	if conf.MinLimit > 0 || conf.MaxLimit > 0 {
		opts = append(opts, limit.WithAdaptiveLimits(conf.MinLimit, conf.MaxLimit))
	}
	if conf.InitialLimit > 0 {
		opts = append(opts, limit.WithAdaptiveInitialLimit(conf.InitialLimit))
	}
	if conf.CPUThreshold > 0 {
		opts = append(opts, limit.WithAdaptiveCPUThreshold(conf.CPUThreshold))
		if s != nil && s.cpuReader != nil {
			opts = append(opts, limit.WithAdaptiveCPUReader(s.cpuReader))
		}
	}
	if conf.Window > 0 {
		opts = append(opts, limit.WithAdaptiveLimitWindow(conf.Window))
	}
	if conf.TargetLatency > 0 {
		opts = append(opts, limit.WithAdaptiveTargetLatency(conf.TargetLatency))
	}
	if conf.TargetErrorRatio > 0 {
		opts = append(opts, limit.WithAdaptiveTargetErrorRatio(conf.TargetErrorRatio))
	}
	if conf.MinSamples > 0 {
		opts = append(opts, limit.WithAdaptiveMinSamples(conf.MinSamples))
	}
	return limit.NewAdaptiveLimiter(opts...)
}

func defaultRateLimit(c RateLimitConfig) (int, int) {
	rate := c.Rate
	if rate <= 0 {
		rate = 100
	}
	burst := c.Burst
	if burst <= 0 {
		burst = rate
	}
	return rate, burst
}

func defaultMaxConcurrency(c MaxConcurrencyConfig) int {
	if c.Limit <= 0 {
		return 1000
	}
	return c.Limit
}

// Handler returns the root HTTP handler with all middleware applied.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		h = s.middlewares[i](h)
	}
	if s.conf.Middlewares.CORS != nil {
		h = CORSMiddleware(*s.conf.Middlewares.CORS)(h)
	}
	return h
}

// Start begins listening and serving HTTP requests.
func (s *Server) Start() error {
	s.setState(serverStateStarting)
	s.httpServer = &http.Server{Addr: s.addr(), Handler: s.Handler(), ReadHeaderTimeout: s.readHeaderTimeout()}
	s.setState(serverStateRunning)
	var err error
	if s.conf.TLS.Enabled() {
		tlsCfg, tlsErr := s.conf.TLS.ServerTLSConfig()
		if tlsErr != nil {
			s.setState(serverStateStopped)
			return fmt.Errorf("configure rest tls: %w", tlsErr)
		}
		s.httpServer.TLSConfig = tlsCfg
		err = s.httpServer.ListenAndServeTLS("", "")
	} else {
		err = s.httpServer.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	s.setState(serverStateStopped)
	return err
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.setState(serverStateStopping)
	if s.httpServer == nil {
		s.setState(serverStateStopped)
		return nil
	}
	err := s.httpServer.Shutdown(ctx)
	s.setState(serverStateStopped)
	return err
}

func (s *Server) addr() string {
	host := s.conf.Host
	if host == "" {
		host = "0.0.0.0"
	}
	port := s.conf.Port
	if port == 0 {
		port = 8080
	}
	return host + ":" + strconv.Itoa(port)
}

// AddOpenAPIRoutes 挂载 `/openapi.json`（当前 server 的路由契约）与
// `/docs`（Swagger UI HTML，静态资源走 unpkg CDN，零额外依赖）。
//
// 调用时机应在所有业务路由注册完毕后，以便 `/openapi.json` 反映完整 API。
//
//	srv.AddRoute(...)
//	srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "demo", Version: "1.0.0"})
func (s *Server) AddOpenAPIRoutes(info OpenAPIInfo) {
	s.AddRoute(Route{
		Method:  http.MethodGet,
		Path:    "/openapi.json",
		Summary: "OpenAPI 3.0 契约（JSON）",
		Tags:    []string{"meta"},
		Handler: func(ctx *Context) {
			doc := s.OpenAPI(info)
			ctx.JSON(http.StatusOK, doc)
		},
	})
	s.AddRoute(Route{
		Method:  http.MethodGet,
		Path:    "/docs",
		Summary: "Swagger UI（基于 unpkg CDN）",
		Tags:    []string{"meta"},
		Handler: func(ctx *Context) {
			ctx.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
			ctx.Response.WriteHeader(http.StatusOK)
			_, _ = ctx.Response.Write([]byte(swaggerUIHTML))
		},
	})
}

// swaggerUIHTML 是一个无额外依赖的 Swagger UI 页面模板；
// 通过 unpkg.com 拉取官方 CSS/JS 运行时资源，运行时从 /openapi.json 拉取契约。
const swaggerUIHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width,initial-scale=1" />
    <title>gofly Swagger UI</title>
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist/swagger-ui.css" />
    <style>html,body{margin:0;padding:0}#swagger-ui{box-sizing:border-box}</style>
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist/swagger-ui-bundle.js" defer></script>
    <script src="https://unpkg.com/swagger-ui-dist/swagger-ui-standalone-preset.js" defer></script>
    <script>
      window.addEventListener('load', function () {
        window.SwaggerUIBundle({
          url: '/openapi.json',
          dom_id: '#swagger-ui',
          deepLinking: true,
          presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
          layout: 'StandaloneLayout',
        });
      });
    </script>
  </body>
</html>
`
