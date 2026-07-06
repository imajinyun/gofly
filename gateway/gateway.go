// Package gateway provides an HTTP reverse proxy and protocol transcoding gateway
// for gofly services. It supports route-based load balancing, circuit breaking,
// rate limiting, canary/shadow traffic, passive health checks, and REST-to-RPC
// transcoding via registered descriptors.
package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/discovery"
	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/limit"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/rpc"
)

const (
	HeaderForwardedFor   = "X-Forwarded-For"
	HeaderForwardedHost  = "X-Forwarded-Host"
	HeaderForwardedProto = "X-Forwarded-Proto"
	HeaderGatewayService = "X-Gofly-Gateway-Service"
	HeaderGatewayRoute   = "X-Gofly-Gateway-Route"
)

var (
	ErrRouteRequired = errors.New("gateway route is required")
	ErrNoRoute       = errors.New("gateway route not found")
	ErrRouteExists   = errors.New("gateway route already exists")
)

// Option configures a Gateway.
type Option func(*Gateway)

// Config is the JSON-friendly configuration for a Gateway.
type Config struct {
	Routes               []RouteConfig       `json:"routes"`
	Timeout              time.Duration       `json:"timeout,omitempty"`
	MaxBodyBytes         int64               `json:"maxBodyBytes,omitempty"`
	MaxExpandedEndpoints int                 `json:"maxExpandedEndpoints,omitempty"`
	PassiveHealth        PassiveHealthConfig `json:"passiveHealth,omitempty"`
	ActiveHealth         ActiveHealthConfig  `json:"activeHealth,omitempty"`
	Shadow               ShadowConfig        `json:"shadow,omitempty"`
}

// RouteConfig is the JSON-friendly configuration for a single gateway route.
type RouteConfig struct {
	Name           string            `json:"name,omitempty"`
	Method         string            `json:"method,omitempty"`
	PathPrefix     string            `json:"pathPrefix"`
	UpstreamPrefix string            `json:"upstreamPrefix,omitempty"`
	Service        string            `json:"service,omitempty"`
	Targets        []string          `json:"targets,omitempty"`
	Timeout        time.Duration     `json:"timeout,omitempty"`
	MaxBodyBytes   int64             `json:"maxBodyBytes,omitempty"`
	PreserveHost   bool              `json:"preserveHost,omitempty"`
	Retry          RetryPolicy       `json:"retry,omitempty"`
	Header         HeaderPolicy      `json:"header,omitempty"`
	Breaker        BreakerConfig     `json:"breaker,omitempty"`
	RateLimit      RateLimitConfig   `json:"rateLimit,omitempty"`
	Concurrency    ConcurrencyConfig `json:"concurrency,omitempty"`
	Canary         []CanaryRoute     `json:"canary,omitempty"`
	Shadow         []ShadowRoute     `json:"shadow,omitempty"`
	AllowedHosts   []string          `json:"allowedHosts,omitempty"`
	Transcode      TranscodeConfig   `json:"transcode,omitempty"`
	Aggregation    AggregationConfig `json:"aggregation,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
}

// TranscodeConfig enables REST-to-RPC protocol transcoding for a route. When
// enabled the gateway converts an inbound HTTP/JSON request into a generic RPC
// call against the resolved upstream instead of plain HTTP reverse proxying.
type TranscodeConfig struct {
	Enabled          bool                   `json:"enabled,omitempty"`
	Protocol         string                 `json:"protocol,omitempty"`
	Service          string                 `json:"service,omitempty"`
	Method           string                 `json:"method,omitempty"`
	Descriptor       string                 `json:"descriptor,omitempty"`
	DescriptorMethod string                 `json:"descriptorMethod,omitempty"`
	Payload          TranscodePayloadConfig `json:"payload,omitempty"`
}

// TranscodePayloadConfig controls how HTTP request parts are assembled into
// the generic RPC request payload.
type TranscodePayloadConfig struct {
	Mode            string                     `json:"mode,omitempty"`
	PathTemplate    string                     `json:"pathTemplate,omitempty"`
	PathParams      []string                   `json:"pathParams,omitempty"`
	QueryParams     []string                   `json:"queryParams,omitempty"`
	PathParameters  []TranscodeParameterConfig `json:"pathParameters,omitempty"`
	QueryParameters []TranscodeParameterConfig `json:"queryParameters,omitempty"`
	BodyField       string                     `json:"bodyField,omitempty"`
	BodyRequired    bool                       `json:"bodyRequired,omitempty"`
	BodySchema      *TranscodeSchemaConfig     `json:"bodySchema,omitempty"`
	Mappings        []TranscodePayloadMapping  `json:"mappings,omitempty"`
	MergeBodyObject bool                       `json:"mergeBodyObject,omitempty"`
}

// TranscodeParameterConfig describes a schema-aware HTTP parameter mapping for
// REST-to-RPC transcoding.
type TranscodeParameterConfig struct {
	Name     string                    `json:"name"`
	Type     string                    `json:"type,omitempty"`
	Format   string                    `json:"format,omitempty"`
	Required bool                      `json:"required,omitempty"`
	Items    *TranscodeParameterConfig `json:"items,omitempty"`
}

// TranscodeSchemaConfig is the schema subset used to validate and map request
// bodies before forwarding descriptor-driven RPC calls.
type TranscodeSchemaConfig struct {
	Type       string                           `json:"type,omitempty"`
	Format     string                           `json:"format,omitempty"`
	Items      *TranscodeSchemaConfig           `json:"items,omitempty"`
	Properties map[string]TranscodeSchemaConfig `json:"properties,omitempty"`
	Required   []string                         `json:"required,omitempty"`
}

// TranscodePayloadMapping projects a validated HTTP request value into the RPC
// payload. Source paths are rooted at body, path, or query, and target paths are
// object paths in the generated RPC JSON payload.
type TranscodePayloadMapping struct {
	Source  string `json:"source,omitempty"`
	Target  string `json:"target,omitempty"`
	Default any    `json:"default,omitempty"`
}

// TranscodeProfile binds descriptor methods to request and response mapping
// contracts that can be reused by multiple gateway routes.
type TranscodeProfile struct {
	Descriptor       string                    `json:"descriptor"`
	DescriptorMethod string                    `json:"descriptorMethod"`
	RequestMappings  []TranscodePayloadMapping `json:"requestMappings,omitempty"`
	ResponseMappings []TranscodePayloadMapping `json:"responseMappings,omitempty"`
	ErrorMappings    []TranscodePayloadMapping `json:"errorMappings,omitempty"`
}

// TranscodeProfileValidationReport describes a read-only comparison between a
// candidate profile and the currently registered profile for the same method.
type TranscodeProfileValidationReport struct {
	OK         bool                     `json:"ok"`
	Compatible bool                     `json:"compatible"`
	Errors     []string                 `json:"errors,omitempty"`
	Changes    []TranscodeProfileChange `json:"changes,omitempty"`
	Current    *TranscodeProfile        `json:"current,omitempty"`
	Candidate  TranscodeProfile         `json:"candidate"`
}

// TranscodeProfileChange describes one request/response/error mapping contract
// difference.
type TranscodeProfileChange struct {
	Kind     string `json:"kind"`
	Scope    string `json:"scope"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// AggregationConfig enables BFF-style fan-out aggregation for a route. When
// enabled, the gateway issues each step as an upstream HTTP request and returns
// a JSON envelope keyed by step name.
type AggregationConfig struct {
	Enabled bool              `json:"enabled,omitempty"`
	Steps   []AggregationStep `json:"steps,omitempty"`
}

// AggregationStep describes one upstream HTTP request in an aggregation route.
type AggregationStep struct {
	Name     string            `json:"name"`
	Method   string            `json:"method,omitempty"`
	Path     string            `json:"path"`
	Target   string            `json:"target,omitempty"`
	Required bool              `json:"required,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     json.RawMessage   `json:"body,omitempty"`
	Fallback json.RawMessage   `json:"fallback,omitempty"`
}

// RetryPolicy configures per-route retry behavior.
type RetryPolicy struct {
	Attempts          int           `json:"attempts,omitempty"`
	Backoff           time.Duration `json:"backoff,omitempty"`
	Statuses          []int         `json:"statuses,omitempty"`
	Methods           []string      `json:"methods,omitempty"`
	BudgetRate        int           `json:"budgetRate,omitempty"`
	BudgetBurst       int           `json:"budgetBurst,omitempty"`
	MaxBodyBytes      int64         `json:"maxBodyBytes,omitempty"`
	RespectRetryAfter bool          `json:"respectRetryAfter,omitempty"`
}

// HeaderPolicy controls which headers are allowed, dropped, or set on requests
// and responses.
type HeaderPolicy struct {
	AllowRequest  []string          `json:"allowRequest,omitempty"`
	DropRequest   []string          `json:"dropRequest,omitempty"`
	SetRequest    map[string]string `json:"setRequest,omitempty"`
	SetResponse   map[string]string `json:"setResponse,omitempty"`
	ExposeHeaders bool              `json:"exposeHeaders,omitempty"`
}

// BreakerConfig configures per-route circuit breaker settings.
type BreakerConfig struct {
	Enabled      bool          `json:"enabled,omitempty"`
	OpenTimeout  time.Duration `json:"openTimeout,omitempty"`
	Window       time.Duration `json:"window,omitempty"`
	Buckets      int           `json:"buckets,omitempty"`
	MinRequests  int64         `json:"minRequests,omitempty"`
	FailureRatio float64       `json:"failureRatio,omitempty"`
}

// RateLimitConfig configures per-route token bucket rate limiting.
type RateLimitConfig struct {
	Rate  int `json:"rate,omitempty"`
	Burst int `json:"burst,omitempty"`
}

// ConcurrencyConfig configures per-route concurrency limiting.
type ConcurrencyConfig struct {
	Limit int `json:"limit,omitempty"`
}

// ShadowRoute defines a shadow (async mirror) target for a route.
type ShadowRoute struct {
	Target         string             `json:"target,omitempty"`
	Resolver       rpc.Resolver       `json:"-"`
	Discovery      discovery.Resolver `json:"-"`
	Service        string             `json:"service,omitempty"`
	SampleRatio    float64            `json:"sampleRatio,omitempty"`
	Headers        map[string]string  `json:"headers,omitempty"`
	Timeout        time.Duration      `json:"timeout,omitempty"`
	UpstreamPrefix string             `json:"upstreamPrefix,omitempty"`
}

// CanaryRoute defines a canary (traffic split) target for a route.
type CanaryRoute struct {
	Name           string             `json:"name,omitempty"`
	Target         string             `json:"target,omitempty"`
	Targets        []string           `json:"targets,omitempty"`
	Resolver       rpc.Resolver       `json:"-"`
	Discovery      discovery.Resolver `json:"-"`
	Service        string             `json:"service,omitempty"`
	Ratio          float64            `json:"ratio,omitempty"`
	Headers        map[string]string  `json:"headers,omitempty"`
	MatchHeaders   map[string]string  `json:"matchHeaders,omitempty"`
	MatchCookies   map[string]string  `json:"matchCookies,omitempty"`
	UpstreamPrefix string             `json:"upstreamPrefix,omitempty"`
}

// PassiveHealthConfig configures passive health check (failure ejection) settings.
type PassiveHealthConfig struct {
	Enabled          bool          `json:"enabled,omitempty"`
	FailureThreshold int           `json:"failureThreshold,omitempty"`
	EjectionDuration time.Duration `json:"ejectionDuration,omitempty"`
}

// ActiveHealthConfig configures active health check (probing) settings.
type ActiveHealthConfig struct {
	Enabled  bool          `json:"enabled,omitempty"`
	Path     string        `json:"path,omitempty"`
	Timeout  time.Duration `json:"timeout,omitempty"`
	Interval time.Duration `json:"interval,omitempty"`
}

// ShadowConfig configures the shadow traffic worker pool.
type ShadowConfig struct {
	Workers int `json:"workers,omitempty"`
	Queue   int `json:"queue,omitempty"`
}

// Route is a runtime gateway route with resolved resolvers and balancers.
type Route struct {
	Name           string
	Method         string
	PathPrefix     string
	UpstreamPrefix string
	Service        string
	Targets        []string
	Resolver       rpc.Resolver
	Discovery      discovery.Resolver
	Balancer       rpc.Balancer
	Timeout        time.Duration
	MaxBodyBytes   int64
	PreserveHost   bool
	Retry          RetryPolicy
	Header         HeaderPolicy
	Breaker        BreakerConfig
	RateLimit      RateLimitConfig
	Concurrency    ConcurrencyConfig
	Canary         []CanaryRoute
	Shadow         []ShadowRoute
	AllowedHosts   []string
	Transcode      TranscodeConfig
	Aggregation    AggregationConfig
	Tags           map[string]string
	Headers        map[string]string
	governanceKey  string
}

// Gateway is an HTTP reverse proxy with governance and observability support.
type Gateway struct {
	mu                  sync.RWMutex
	routes              []Route
	client              *http.Client
	balancer            rpc.Balancer
	timeout             time.Duration
	maxBodyBytes        int64
	maxExpandedEndpoint int
	stats               *Stats
	registry            *metrics.Registry
	passive             *passiveHealth
	breakers            map[string]*gatewayCachedBreaker
	ruleRuntime         *gatewayRuleRuntime
	manager             *governance.Manager
	rules               *governance.RuleSet
	resolvers           map[string]rpc.Resolver
	discoveryServices   map[string]struct{}
	transcoders         map[string]rpc.GenericClient
	transcoderMu        sync.Mutex
	transcoderFactory   TranscoderFactory
	descriptors         map[string]rpc.Descriptor
	transcodeProfiles   map[string]TranscodeProfile
	activeHealth        ActiveHealthConfig
	shadowPool          *shadowPool
	logger              *slog.Logger
	retryRuntime        *retryRuntime
	discoveryFailover   bool
	initErr             error
	transcodeRuntime    map[string]TranscodeRuntimeSnapshot
	aggregationRuntime  map[string]AggregationRuntimeSnapshot
}

type gatewayRuleRuntime struct {
	mu          sync.Mutex
	rateLimits  map[string]*gatewayCachedRateLimiter
	concurrency map[string]*gatewayCachedConcurrencyLimiter
}

type gatewayCachedRateLimiter struct {
	rate    int
	burst   int
	limiter *limit.Limiter
}

type gatewayCachedConcurrencyLimiter struct {
	limit   int
	limiter *limit.ConcurrencyLimiter
}

type gatewayCachedBreaker struct {
	config  BreakerConfig
	breaker *breaker.AdaptiveBreaker
}

// Snapshot is a point-in-time view of gateway state for observability.
type Snapshot struct {
	Routes     []RouteSnapshot           `json:"routes"`
	Discovery  []DiscoveryRouteSnapshot  `json:"discovery,omitempty"`
	Upstreams  []EndpointHealthSnapshot  `json:"upstreams,omitempty"`
	Rules      []governance.Rule         `json:"rules,omitempty"`
	RuleStats  []governance.RuleStats    `json:"ruleStats,omitempty"`
	RuleStatus governance.RuleSetStatus  `json:"ruleStatus,omitempty"`
	RuleEvents []governance.RuleSetEvent `json:"ruleEvents,omitempty"`
}

// RuntimeSnapshot exposes the gateway outbound policy wiring that affects
// upstream requests.
type RuntimeSnapshot struct {
	DefaultTimeout      time.Duration           `json:"defaultTimeout,omitempty"`
	HTTPClientTimeout   time.Duration           `json:"httpClientTimeout,omitempty"`
	MaxBodyBytes        int64                   `json:"maxBodyBytes,omitempty"`
	MaxExpandedEndpoint int                     `json:"maxExpandedEndpoint,omitempty"`
	RouteCount          int                     `json:"routeCount"`
	TranscodeProfiles   []TranscodeProfile      `json:"transcodeProfiles,omitempty"`
	Routes              []RouteRuntimeSnapshot  `json:"routes,omitempty"`
	Cache               RuntimeCacheSnapshot    `json:"cache,omitempty"`
	PassiveHealth       *PassiveRuntimeSnapshot `json:"passiveHealth,omitempty"`
	ActiveHealth        ActiveHealthConfig      `json:"activeHealth,omitempty"`
	ShadowPool          *ShadowRuntimeSnapshot  `json:"shadowPool,omitempty"`
	GovernanceBacked    bool                    `json:"governanceBacked"`
}

// RouteRuntimeSnapshot holds the normalized outbound policy for one gateway route.
type RouteRuntimeSnapshot struct {
	Name               string                     `json:"name,omitempty"`
	Method             string                     `json:"method,omitempty"`
	PathPrefix         string                     `json:"pathPrefix"`
	Service            string                     `json:"service,omitempty"`
	TargetCount        int                        `json:"targetCount,omitempty"`
	Timeout            time.Duration              `json:"timeout,omitempty"`
	EffectiveTimeout   time.Duration              `json:"effectiveTimeout,omitempty"`
	Retry              RetryPolicy                `json:"retry"`
	Breaker            BreakerConfig              `json:"breaker,omitempty"`
	RateLimit          RateLimitConfig            `json:"rateLimit,omitempty"`
	Concurrency        ConcurrencyConfig          `json:"concurrency,omitempty"`
	CanaryCount        int                        `json:"canaryCount,omitempty"`
	ShadowCount        int                        `json:"shadowCount,omitempty"`
	Transcode          TranscodeConfig            `json:"transcode,omitempty"`
	TranscodeRuntime   TranscodeRuntimeSnapshot   `json:"transcodeRuntime,omitempty"`
	Aggregation        AggregationConfig          `json:"aggregation,omitempty"`
	AggregationRuntime AggregationRuntimeSnapshot `json:"aggregationRuntime,omitempty"`
}

// TranscodeRuntimeSnapshot reports the effective descriptor transcode profile
// and the last mapping error for a route.
type TranscodeRuntimeSnapshot struct {
	Descriptor       string `json:"descriptor,omitempty"`
	DescriptorMethod string `json:"descriptorMethod,omitempty"`
	ProfileKey       string `json:"profileKey,omitempty"`
	RequestMappings  int    `json:"requestMappings,omitempty"`
	ResponseMappings int    `json:"responseMappings,omitempty"`
	ErrorMappings    int    `json:"errorMappings,omitempty"`
	LastError        string `json:"lastError,omitempty"`
	LastErrorStage   string `json:"lastErrorStage,omitempty"`
}

// AggregationRuntimeSnapshot reports the latest BFF aggregation degradation
// state for a route.
type AggregationRuntimeSnapshot struct {
	Steps         int `json:"steps,omitempty"`
	RequiredSteps int `json:"requiredSteps,omitempty"`
	FallbackSteps int `json:"fallbackSteps,omitempty"`
	LastFailures  int `json:"lastFailures,omitempty"`
	LastFallbacks int `json:"lastFallbacks,omitempty"`
	LastStatus    int `json:"lastStatus,omitempty"`
}

// RuntimeCacheSnapshot reports lazily materialized gateway policy primitives.
type RuntimeCacheSnapshot struct {
	RateLimiters        int `json:"rateLimiters,omitempty"`
	ConcurrencyLimiters int `json:"concurrencyLimiters,omitempty"`
	Breakers            int `json:"breakers,omitempty"`
	RetryRouteBudgets   int `json:"retryRouteBudgets,omitempty"`
	RetryUpstreamBudget int `json:"retryUpstreamBudgets,omitempty"`
	Transcoders         int `json:"transcoders,omitempty"`
	Descriptors         int `json:"descriptors,omitempty"`
	TranscodeProfiles   int `json:"transcodeProfiles,omitempty"`
}

// PassiveRuntimeSnapshot reports passive health runtime state.
type PassiveRuntimeSnapshot struct {
	FailureThreshold int `json:"failureThreshold"`
	EndpointCount    int `json:"endpointCount"`
}

// ShadowRuntimeSnapshot reports shadow worker runtime capacity.
type ShadowRuntimeSnapshot struct {
	QueueCapacity int `json:"queueCapacity"`
	Queued        int `json:"queued"`
}

// RouteSnapshot holds request metrics for a single route.
type RouteSnapshot struct {
	Name          string        `json:"name,omitempty"`
	Method        string        `json:"method,omitempty"`
	PathPrefix    string        `json:"pathPrefix"`
	Service       string        `json:"service,omitempty"`
	Requests      int64         `json:"requests"`
	Errors        int64         `json:"errors"`
	Retries       int64         `json:"retries"`
	Shadowed      int64         `json:"shadowed"`
	ShadowDropped int64         `json:"shadowDropped"`
	Ejections     int64         `json:"ejections"`
	Statuses      map[int]int64 `json:"statuses,omitempty"`
	TotalDuration time.Duration `json:"totalDuration"`
	MaxDuration   time.Duration `json:"maxDuration"`
	AvgDuration   time.Duration `json:"avgDuration"`
}

// EndpointHealthSnapshot represents the health state of a single upstream endpoint.
type EndpointHealthSnapshot struct {
	Endpoint  string    `json:"endpoint"`
	Failures  int       `json:"failures"`
	Ejected   bool      `json:"ejected"`
	EjectedAt time.Time `json:"ejectedAt,omitempty"`
}

// DiscoveryRouteSnapshot represents resolved endpoints for a route or canary/shadow.
type DiscoveryRouteSnapshot struct {
	Name      string                `json:"name,omitempty"`
	Route     string                `json:"route"`
	Service   string                `json:"service,omitempty"`
	Kind      string                `json:"kind"`
	Tags      map[string]string     `json:"tags,omitempty"`
	Endpoints []string              `json:"endpoints,omitempty"`
	Removed   []string              `json:"removed,omitempty"`
	Instances []rpc.ServiceInstance `json:"instances,omitempty"`
	Error     string                `json:"error,omitempty"`
	Stale     bool                  `json:"stale,omitempty"`
	Fallbacks int64                 `json:"fallbacks,omitempty"`
}

// Stats collects request metrics per route.
type Stats struct {
	mu     sync.RWMutex
	routes map[string]*routeStats
}

type routeStats struct {
	Requests      int64
	Errors        int64
	Retries       int64
	Shadowed      int64
	ShadowDropped int64
	Ejections     int64
	Statuses      map[int]int64
	TotalDuration time.Duration
	MaxDuration   time.Duration
}

type passiveHealth struct {
	mu               sync.Mutex
	failureThreshold int
	ejectionDuration time.Duration
	endpoints        map[string]*endpointHealth
}

type endpointHealth struct {
	failures  int
	ejectedAt time.Time
}

type proxyResult struct {
	Endpoint   string
	Status     int
	Header     http.Header
	Body       []byte
	BodyStream io.ReadCloser
	Hijacked   bool
	Retries    int
	Err        error
}

type routeMatch struct {
	index int
	route Route
}

// New creates a Gateway from runtime routes. It applies defaults for balancer,
// timeout, and shadow pool. Routes are normalized and sorted by path prefix length.
func New(routes []Route, opts ...Option) (*Gateway, error) {
	if len(routes) == 0 {
		return nil, ErrRouteRequired
	}
	g := &Gateway{
		balancer:            &rpc.RoundRobinBalancer{},
		timeout:             3 * time.Second,
		maxExpandedEndpoint: 1000,
		stats:               NewStats(),
		breakers:            make(map[string]*gatewayCachedBreaker),
		ruleRuntime:         newGatewayRuleRuntime(),
		transcoders:         make(map[string]rpc.GenericClient),
		descriptors:         make(map[string]rpc.Descriptor),
		transcodeRuntime:    make(map[string]TranscodeRuntimeSnapshot),
		aggregationRuntime:  make(map[string]AggregationRuntimeSnapshot),
		logger:              slog.Default(),
		retryRuntime:        newRetryRuntime(),
	}
	for _, opt := range opts {
		opt(g)
	}
	if g.initErr != nil {
		return nil, g.initErr
	}
	if g.client == nil {
		g.client = rpc.NewHTTPClient(rpc.DefaultTransportConfig())
	}
	normalized, err := g.normalizeRoutes(routes)
	if err != nil {
		return nil, err
	}
	g.routes = normalized
	if g.shadowPool == nil && hasShadowRoutes(g.routes) {
		g.shadowPool = newShadowPool(4, 1024)
	}
	return g, nil
}

func hasShadowRoutes(routes []Route) bool {
	for _, route := range routes {
		if len(route.Shadow) > 0 {
			return true
		}
	}
	return false
}

// NewFromConfig creates a Gateway from JSON-friendly Config and optional service resolvers.
func NewFromConfig(conf Config, resolvers map[string]rpc.Resolver, opts ...Option) (*Gateway, error) {
	routes := make([]Route, 0, len(conf.Routes))
	for _, route := range conf.Routes {
		r := routeFromConfig(route)
		if r.Service != "" {
			r.Resolver = resolvers[r.Service]
		}
		for i := range r.Shadow {
			if r.Shadow[i].Service != "" {
				r.Shadow[i].Resolver = resolvers[r.Shadow[i].Service]
			}
		}
		for i := range r.Canary {
			if r.Canary[i].Service != "" {
				r.Canary[i].Resolver = resolvers[r.Canary[i].Service]
			}
		}
		routes = append(routes, r)
	}
	configOpts := make([]Option, 0, len(opts)+4)
	if conf.Timeout > 0 {
		configOpts = append(configOpts, WithTimeout(conf.Timeout))
	}
	if conf.MaxBodyBytes > 0 {
		configOpts = append(configOpts, WithMaxBodyBytes(conf.MaxBodyBytes))
	}
	if conf.MaxExpandedEndpoints > 0 {
		configOpts = append(configOpts, WithMaxExpandedEndpoints(conf.MaxExpandedEndpoints))
	}
	if conf.PassiveHealth.Enabled {
		configOpts = append(configOpts, WithPassiveHealth(conf.PassiveHealth))
	}
	if conf.ActiveHealth.Enabled {
		configOpts = append(configOpts, WithActiveHealth(conf.ActiveHealth))
	}
	if conf.Shadow.Workers > 0 || conf.Shadow.Queue > 0 {
		configOpts = append(configOpts, WithShadowPool(conf.Shadow.Workers, conf.Shadow.Queue))
	}
	if len(resolvers) > 0 {
		configOpts = append(configOpts, WithResolvers(resolvers))
	}
	configOpts = append(configOpts, opts...)
	return New(routes, configOpts...)
}

// MustNew is like New but panics on error.
func MustNew(routes []Route, opts ...Option) *Gateway {
	g, err := New(routes, opts...)
	if err != nil {
		panic(err)
	}
	return g
}

// WithHTTPClient sets the HTTP client used for proxying requests.
func WithHTTPClient(client *http.Client) Option {
	return func(g *Gateway) {
		if client != nil {
			g.client = client
		}
	}
}

// WithBalancer sets the load balancer for selecting upstream endpoints.
func WithBalancer(balancer rpc.Balancer) Option {
	return func(g *Gateway) {
		if balancer != nil {
			g.balancer = balancer
		}
	}
}

// WithTranscoderFactory overrides how the gateway builds generic RPC clients
// for transcoded routes. It is primarily useful for tests or to customize the
// codec, TLS settings, or transport of the transcoding backend.
func WithTranscoderFactory(factory TranscoderFactory) Option {
	return func(g *Gateway) {
		if factory != nil {
			g.transcoderFactory = factory
		}
	}
}

// WithDescriptors registers RPC service descriptors that transcoded routes can
// reference through TranscodeConfig.Descriptor. Descriptor-backed routes derive
// the RPC service name from the descriptor and validate the selected method
// before forwarding the request.
func WithDescriptors(descriptors ...rpc.Descriptor) Option {
	return func(g *Gateway) {
		for _, desc := range descriptors {
			_ = g.RegisterDescriptor(desc)
		}
	}
}

// WithTranscodeProfiles registers descriptor/method transcode mapping profiles.
func WithTranscodeProfiles(profiles ...TranscodeProfile) Option {
	return func(g *Gateway) {
		for _, profile := range profiles {
			if err := g.RegisterTranscodeProfile(profile); err != nil {
				g.initErr = errors.Join(g.initErr, err)
			}
		}
	}
}

// WithTimeout sets the default request timeout for all routes.
func WithTimeout(timeout time.Duration) Option {
	return func(g *Gateway) {
		if timeout > 0 {
			g.timeout = timeout
		}
	}
}

// WithMaxBodyBytes sets the maximum request body size for all routes.
func WithMaxBodyBytes(maxBodyBytes int64) Option {
	return func(g *Gateway) {
		if maxBodyBytes > 0 {
			g.maxBodyBytes = maxBodyBytes
		}
	}
}

// WithMaxExpandedEndpoints limits the number of endpoints expanded from service instances.
func WithMaxExpandedEndpoints(limit int) Option {
	return func(g *Gateway) {
		if limit > 0 {
			g.maxExpandedEndpoint = limit
		}
	}
}

// WithStats injects a custom Stats collector.
func WithStats(stats *Stats) Option {
	return func(g *Gateway) {
		if stats != nil {
			g.stats = stats
		}
	}
}

// WithMetricsRegistry injects a metrics registry for route-level observability.
func WithMetricsRegistry(registry *metrics.Registry) Option {
	return func(g *Gateway) {
		g.registry = registry
	}
}

// WithLogger sets the logger for gateway request logging.
func WithLogger(logger *slog.Logger) Option {
	return func(g *Gateway) {
		if logger != nil {
			g.logger = logger
		}
	}
}

// WithGovernanceRuleSet attaches a static rule set for request governance.
func WithGovernanceRuleSet(rules *governance.RuleSet) Option {
	return func(g *Gateway) {
		g.rules = rules
	}
}

// WithGovernanceManager attaches a governance manager and derives its rule set.
func WithGovernanceManager(manager *governance.Manager) Option {
	return func(g *Gateway) {
		g.manager = manager
		if manager != nil {
			g.rules = manager.RuleSet()
		}
	}
}

// WithGovernanceSuite merges a governance suite into the gateway rules.
func WithGovernanceSuite(suite *governance.Suite) Option {
	return func(g *Gateway) {
		if g.manager != nil {
			return
		}
		if suite != nil {
			g.rules = governance.MergeRuleSets(g.rules, suite.RuleSet())
		}
	}
}

// WithResolvers registers RPC resolvers by service name.
func WithResolvers(resolvers map[string]rpc.Resolver) Option {
	return func(g *Gateway) {
		if len(resolvers) == 0 {
			return
		}
		if g.resolvers == nil {
			g.resolvers = make(map[string]rpc.Resolver, len(resolvers))
		}
		for service, resolver := range resolvers {
			if service != "" && resolver != nil {
				g.resolvers[service] = resolver
			}
		}
	}
}

// WithDiscoveryResolvers wraps discovery resolvers as RPC resolvers by service name.
func WithDiscoveryResolvers(resolvers map[string]discovery.Resolver) Option {
	return func(g *Gateway) {
		if len(resolvers) == 0 {
			return
		}
		if g.resolvers == nil {
			g.resolvers = make(map[string]rpc.Resolver, len(resolvers))
		}
		if g.discoveryServices == nil {
			g.discoveryServices = make(map[string]struct{}, len(resolvers))
		}
		for service, resolver := range resolvers {
			service = strings.TrimSpace(service)
			if service != "" && resolver != nil {
				g.discoveryServices[service] = struct{}{}
				g.resolvers[service] = g.discoveryResolver(resolver, service)
			}
		}
	}
}

// WithDiscoveryFailover enables last-known-good endpoint fallback for discovery
// backed routes. It is opt-in so route owners can keep strict resolver failure
// semantics where stale endpoints are not acceptable.
func WithDiscoveryFailover() Option {
	return func(g *Gateway) {
		g.discoveryFailover = true
		for service := range g.discoveryServices {
			if resolver := g.resolvers[service]; resolver != nil {
				g.resolvers[service] = wrapGatewayFailoverResolver(resolver)
			}
		}
	}
}

// WithPassiveHealth enables passive health checking with the given configuration.
func WithPassiveHealth(conf PassiveHealthConfig) Option {
	return func(g *Gateway) {
		g.passive = newPassiveHealth(conf)
	}
}

// WithActiveHealth enables active health probing with the given configuration.
func WithActiveHealth(conf ActiveHealthConfig) Option {
	return func(g *Gateway) {
		g.activeHealth = normalizeActiveHealthConfig(conf)
	}
}

// WithShadowPool configures the shadow traffic worker pool size.
func WithShadowPool(workers int, queue int) Option {
	return func(g *Gateway) {
		if workers <= 0 {
			workers = 4
		}
		if queue <= 0 {
			queue = 1024
		}
		g.shadowPool = newShadowPool(workers, queue)
	}
}

func newGatewayRuleRuntime() *gatewayRuleRuntime {
	return &gatewayRuleRuntime{
		rateLimits:  make(map[string]*gatewayCachedRateLimiter),
		concurrency: make(map[string]*gatewayCachedConcurrencyLimiter),
	}
}

// Handler returns the Gateway as an http.Handler.
func (g *Gateway) Handler() http.Handler { return g }

// Shutdown gracefully stops the gateway, waiting for shadow traffic to drain.
func (g *Gateway) Shutdown(ctx context.Context) error {
	if g == nil || g.shadowPool == nil {
		return nil
	}
	return g.shadowPool.Shutdown(ctx)
}

// Close shuts down the gateway immediately.
func (g *Gateway) Close() error {
	return g.Shutdown(context.Background())
}

// HealthCheck verifies that all routes have resolvable upstream endpoints.
func (g *Gateway) HealthCheck(ctx context.Context) error {
	if g == nil {
		return errors.New("gateway is nil")
	}
	routes := g.Routes()
	if len(routes) == 0 {
		return ErrNoRoute
	}
	var errs []error
	for _, route := range routes {
		endpoints, err := g.resolveEndpoints(ctx, route)
		if err != nil {
			errs = append(errs, fmt.Errorf("route %s: %w", routeKey(route), err))
			continue
		}
		if len(endpoints) == 0 {
			errs = append(errs, fmt.Errorf("route %s: no upstream endpoints", routeKey(route)))
			continue
		}
		if g.activeHealth.Enabled {
			if err := g.probeActiveHealth(ctx, route, endpoints); err != nil {
				errs = append(errs, fmt.Errorf("route %s: %w", routeKey(route), err))
			}
		}
	}
	return errors.Join(errs...)
}

// Routes returns a snapshot of the current route table.
func (g *Gateway) Routes() []Route {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Route, len(g.routes))
	for i, route := range g.routes {
		out[i] = cloneRoute(route)
	}
	return out
}

// RouteConfigs returns a snapshot of the current routes as JSON-friendly configs.
func (g *Gateway) RouteConfigs() []RouteConfig {
	routes := g.Routes()
	out := make([]RouteConfig, 0, len(routes))
	for _, route := range routes {
		out = append(out, routeConfigFromRoute(route))
	}
	return out
}

// RegisterDescriptor registers an RPC descriptor for descriptor-driven gateway
// transcoding. The descriptor is keyed by its service name.
func (g *Gateway) RegisterDescriptor(desc rpc.Descriptor) error {
	if g == nil {
		return errors.New("gateway is nil")
	}
	if err := desc.Validate(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.descriptors == nil {
		g.descriptors = make(map[string]rpc.Descriptor)
	}
	g.descriptors[strings.TrimSpace(desc.Name)] = cloneDescriptor(desc)
	return nil
}

// RegisterTranscodeProfile registers a descriptor/method-level mapping profile.
func (g *Gateway) RegisterTranscodeProfile(profile TranscodeProfile) error {
	if g == nil {
		return errors.New("gateway is nil")
	}
	profile.Descriptor = strings.TrimSpace(profile.Descriptor)
	profile.DescriptorMethod = strings.Trim(strings.TrimSpace(profile.DescriptorMethod), "/")
	if err := validateTranscodeProfile(profile); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.transcodeProfiles == nil {
		g.transcodeProfiles = make(map[string]TranscodeProfile)
	}
	g.transcodeProfiles[transcodeProfileKey(profile.Descriptor, profile.DescriptorMethod)] = cloneTranscodeProfile(profile)
	return nil
}

// Descriptors returns the registered RPC descriptors keyed by service name.
func (g *Gateway) Descriptors() map[string]rpc.Descriptor {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make(map[string]rpc.Descriptor, len(g.descriptors))
	for name, desc := range g.descriptors {
		out[name] = cloneDescriptor(desc)
	}
	return out
}

// TranscodeProfiles returns registered descriptor/method mapping profiles.
func (g *Gateway) TranscodeProfiles() []TranscodeProfile {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]TranscodeProfile, 0, len(g.transcodeProfiles))
	for _, profile := range g.transcodeProfiles {
		out = append(out, cloneTranscodeProfile(profile))
	}
	sort.Slice(out, func(i, j int) bool {
		return transcodeProfileKey(out[i].Descriptor, out[i].DescriptorMethod) < transcodeProfileKey(out[j].Descriptor, out[j].DescriptorMethod)
	})
	return out
}

// ValidateTranscodeProfile compares a candidate profile with the registered
// profile for the same descriptor method without mutating gateway state.
func (g *Gateway) ValidateTranscodeProfile(candidate TranscodeProfile) TranscodeProfileValidationReport {
	candidate.Descriptor = strings.TrimSpace(candidate.Descriptor)
	candidate.DescriptorMethod = strings.Trim(strings.TrimSpace(candidate.DescriptorMethod), "/")
	report := TranscodeProfileValidationReport{OK: true, Compatible: true, Candidate: cloneTranscodeProfile(candidate)}
	if err := validateTranscodeProfile(candidate); err != nil {
		report.OK = false
		report.Compatible = false
		report.Errors = append(report.Errors, err.Error())
		return report
	}
	if g == nil {
		return report
	}
	g.mu.RLock()
	current, ok := g.transcodeProfiles[transcodeProfileKey(candidate.Descriptor, candidate.DescriptorMethod)]
	g.mu.RUnlock()
	if !ok {
		report.Changes = append(report.Changes, TranscodeProfileChange{
			Kind:     "add_profile",
			Severity: "info",
			Message:  "candidate profile is new",
		})
		return report
	}
	current = cloneTranscodeProfile(current)
	report.Current = &current
	report.Changes = append(report.Changes, compareTranscodeProfileMappings("request", current.RequestMappings, candidate.RequestMappings)...)
	report.Changes = append(report.Changes, compareTranscodeProfileMappings("response", current.ResponseMappings, candidate.ResponseMappings)...)
	report.Changes = append(report.Changes, compareTranscodeProfileMappings("error", current.ErrorMappings, candidate.ErrorMappings)...)
	for _, change := range report.Changes {
		if change.Severity == "breaking" {
			report.Compatible = false
			break
		}
	}
	return report
}

// AddRoute appends a new route to the gateway.
func (g *Gateway) AddRoute(route Route) error {
	if g == nil {
		return ErrRouteRequired
	}
	normalized, err := g.normalizeRoute(route)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if routeIndex(g.routes, normalized.Method, normalized.PathPrefix) >= 0 {
		return ErrRouteExists
	}
	g.routes = append(g.routes, normalized)
	sortRoutes(g.routes)
	return nil
}

// UpdateRoute replaces an existing route matched by method and path prefix.
func (g *Gateway) UpdateRoute(route Route) error {
	if g == nil {
		return ErrRouteRequired
	}
	normalized, err := g.normalizeRoute(route)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	idx := routeIndex(g.routes, normalized.Method, normalized.PathPrefix)
	if idx < 0 {
		return ErrNoRoute
	}
	g.routes[idx] = normalized
	sortRoutes(g.routes)
	return nil
}

// UpsertRoute inserts or replaces a route matched by method and path prefix.
func (g *Gateway) UpsertRoute(route Route) error {
	if g == nil {
		return ErrRouteRequired
	}
	normalized, err := g.normalizeRoute(route)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	idx := routeIndex(g.routes, normalized.Method, normalized.PathPrefix)
	if idx >= 0 {
		g.routes[idx] = normalized
	} else {
		g.routes = append(g.routes, normalized)
	}
	sortRoutes(g.routes)
	return nil
}

// RemoveRoute deletes a route by method and path prefix.
func (g *Gateway) RemoveRoute(method, pathPrefix string) bool {
	if g == nil {
		return false
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	pathPrefix = cleanPrefix(pathPrefix)
	if pathPrefix == "" {
		pathPrefix = "/"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	idx := routeIndex(g.routes, method, pathPrefix)
	if idx < 0 {
		return false
	}
	copy(g.routes[idx:], g.routes[idx+1:])
	g.routes[len(g.routes)-1] = Route{}
	g.routes = g.routes[:len(g.routes)-1]
	return true
}

// SetRoutes replaces the entire route table.
func (g *Gateway) SetRoutes(routes []Route) error {
	if g == nil {
		return ErrRouteRequired
	}
	if len(routes) == 0 {
		return ErrRouteRequired
	}
	normalized, err := g.normalizeRoutes(routes)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.routes = normalized
	g.mu.Unlock()
	return nil
}

// Snapshot returns a point-in-time view of routes, discovery, and governance state.
func (g *Gateway) Snapshot() Snapshot {
	if g == nil || g.stats == nil {
		return Snapshot{}
	}
	routes := g.Routes()
	ctx, cancel := context.WithTimeout(context.Background(), g.snapshotResolveTimeout())
	defer cancel()
	byKey := g.stats.snapshotByKey()
	snapshot := Snapshot{Routes: make([]RouteSnapshot, 0, len(routes))}
	for _, route := range routes {
		key := routeKey(route)
		item := byKey[key]
		item.Name = route.Name
		item.Method = route.Method
		item.PathPrefix = route.PathPrefix
		item.Service = route.Service
		snapshot.Routes = append(snapshot.Routes, item)
		snapshot.Discovery = append(snapshot.Discovery, g.discoverySnapshot(ctx, "route", key, route)...)
	}
	if g.passive != nil {
		snapshot.Upstreams = g.passive.Snapshot()
	}
	if g.rules != nil {
		snapshot.Rules = g.rules.Snapshot()
		snapshot.RuleStats = g.rules.Stats()
		snapshot.RuleStatus = g.rules.Status()
		snapshot.RuleEvents = g.rules.History()
	}
	return snapshot
}

// RuntimeSnapshot returns the normalized gateway outbound runtime state.
func (g *Gateway) RuntimeSnapshot() RuntimeSnapshot {
	if g == nil {
		return RuntimeSnapshot{}
	}
	routes := g.Routes()
	snapshot := RuntimeSnapshot{
		DefaultTimeout:      g.timeout,
		HTTPClientTimeout:   gatewayHTTPClientTimeout(g.client),
		MaxBodyBytes:        g.maxBodyBytes,
		MaxExpandedEndpoint: g.maxExpandedEndpoint,
		RouteCount:          len(routes),
		TranscodeProfiles:   g.TranscodeProfiles(),
		Routes:              make([]RouteRuntimeSnapshot, 0, len(routes)),
		Cache:               g.runtimeCacheSnapshot(),
		ActiveHealth:        g.activeHealth,
		GovernanceBacked:    g.manager != nil || g.rules != nil,
	}
	if g.passive != nil {
		snapshot.PassiveHealth = g.passive.runtimeSnapshot()
	}
	if g.shadowPool != nil {
		snapshot.ShadowPool = g.shadowPool.runtimeSnapshot()
	}
	for _, route := range routes {
		snapshot.Routes = append(snapshot.Routes, g.routeRuntimeSnapshot(route))
	}
	return snapshot
}

func (g *Gateway) routeRuntimeSnapshot(route Route) RouteRuntimeSnapshot {
	effectiveTimeout := route.Timeout
	if effectiveTimeout <= 0 && g != nil {
		effectiveTimeout = g.timeout
	}
	return RouteRuntimeSnapshot{
		Name:               route.Name,
		Method:             route.Method,
		PathPrefix:         route.PathPrefix,
		Service:            route.Service,
		TargetCount:        len(route.Targets),
		Timeout:            route.Timeout,
		EffectiveTimeout:   effectiveTimeout,
		Retry:              normalizeRetryPolicy(route.Retry),
		Breaker:            route.Breaker,
		RateLimit:          route.RateLimit,
		Concurrency:        route.Concurrency,
		CanaryCount:        len(route.Canary),
		ShadowCount:        len(route.Shadow),
		Transcode:          cloneTranscodeConfig(route.Transcode),
		TranscodeRuntime:   g.routeTranscodeRuntimeSnapshot(route),
		Aggregation:        cloneAggregationConfig(route.Aggregation),
		AggregationRuntime: g.routeAggregationRuntimeSnapshot(route),
	}
}

func (g *Gateway) routeAggregationRuntimeSnapshot(route Route) AggregationRuntimeSnapshot {
	if g == nil || !route.Aggregation.Enabled {
		return AggregationRuntimeSnapshot{}
	}
	snapshot := AggregationRuntimeSnapshot{Steps: len(route.Aggregation.Steps)}
	for _, step := range route.Aggregation.Steps {
		if step.Required {
			snapshot.RequiredSteps++
		}
		if len(step.Fallback) > 0 {
			snapshot.FallbackSteps++
		}
	}
	g.mu.RLock()
	runtime := g.aggregationRuntime[routeKey(route)]
	g.mu.RUnlock()
	snapshot.LastFailures = runtime.LastFailures
	snapshot.LastFallbacks = runtime.LastFallbacks
	snapshot.LastStatus = runtime.LastStatus
	return snapshot
}

func (g *Gateway) routeTranscodeRuntimeSnapshot(route Route) TranscodeRuntimeSnapshot {
	if g == nil || !route.Transcode.Enabled {
		return TranscodeRuntimeSnapshot{}
	}
	snapshot := TranscodeRuntimeSnapshot{
		Descriptor:       strings.TrimSpace(route.Transcode.Descriptor),
		DescriptorMethod: strings.Trim(strings.TrimSpace(route.Transcode.DescriptorMethod), "/"),
	}
	if snapshot.DescriptorMethod == "" {
		snapshot.DescriptorMethod = strings.Trim(strings.TrimSpace(route.Transcode.Method), "/")
	}
	if snapshot.Descriptor != "" && snapshot.DescriptorMethod != "" {
		if profile := g.transcodeProfile(snapshot.Descriptor, snapshot.DescriptorMethod); profile != nil {
			snapshot.ProfileKey = transcodeProfileKey(profile.Descriptor, profile.DescriptorMethod)
			snapshot.RequestMappings = len(profile.RequestMappings)
			snapshot.ResponseMappings = len(profile.ResponseMappings)
			snapshot.ErrorMappings = len(profile.ErrorMappings)
		}
	}
	g.mu.RLock()
	runtime := g.transcodeRuntime[routeKey(route)]
	g.mu.RUnlock()
	snapshot.LastError = runtime.LastError
	snapshot.LastErrorStage = runtime.LastErrorStage
	return snapshot
}

func (g *Gateway) runtimeCacheSnapshot() RuntimeCacheSnapshot {
	if g == nil {
		return RuntimeCacheSnapshot{}
	}
	snapshot := RuntimeCacheSnapshot{}
	if g.ruleRuntime != nil {
		g.ruleRuntime.mu.Lock()
		snapshot.RateLimiters = len(g.ruleRuntime.rateLimits)
		snapshot.ConcurrencyLimiters = len(g.ruleRuntime.concurrency)
		g.ruleRuntime.mu.Unlock()
	}
	g.mu.RLock()
	snapshot.Breakers = len(g.breakers)
	g.mu.RUnlock()
	if g.retryRuntime != nil {
		g.retryRuntime.mu.Lock()
		snapshot.RetryRouteBudgets = len(g.retryRuntime.route)
		snapshot.RetryUpstreamBudget = len(g.retryRuntime.upstream)
		g.retryRuntime.mu.Unlock()
	}
	g.transcoderMu.Lock()
	snapshot.Transcoders = len(g.transcoders)
	g.transcoderMu.Unlock()
	g.mu.RLock()
	snapshot.Descriptors = len(g.descriptors)
	snapshot.TranscodeProfiles = len(g.transcodeProfiles)
	g.mu.RUnlock()
	return snapshot
}

func gatewayHTTPClientTimeout(client *http.Client) time.Duration {
	if client == nil {
		return 0
	}
	return client.Timeout
}

func (p *passiveHealth) runtimeSnapshot() *PassiveRuntimeSnapshot {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return &PassiveRuntimeSnapshot{FailureThreshold: p.failureThreshold, EndpointCount: len(p.endpoints)}
}

func (p *shadowPool) runtimeSnapshot() *ShadowRuntimeSnapshot {
	if p == nil {
		return nil
	}
	return &ShadowRuntimeSnapshot{QueueCapacity: cap(p.tasks), Queued: len(p.tasks)}
}

func (g *Gateway) snapshotResolveTimeout() time.Duration {
	if g == nil || g.timeout <= 0 {
		return 500 * time.Millisecond
	}
	if g.timeout < 500*time.Millisecond {
		return g.timeout
	}
	return 500 * time.Millisecond
}

func (g *Gateway) discoverySnapshot(ctx context.Context, kind string, key string, route Route) []DiscoveryRouteSnapshot {
	out := make([]DiscoveryRouteSnapshot, 0, 1+len(route.Canary)+len(route.Shadow))
	out = append(out, g.resolverSnapshot(ctx, kind, key, route.Name, route.Service, route.Tags, route.Resolver))
	for i, canary := range route.Canary {
		canaryRoute := canary.apply(route)
		canaryKey := key + ":canary:" + strconv.Itoa(i)
		out = append(out, g.resolverSnapshot(ctx, "canary", canaryKey, canaryRoute.Name, canaryRoute.Service, canaryRoute.Tags, canaryRoute.Resolver))
	}
	for i, shadow := range route.Shadow {
		shadowKey := key + ":shadow:" + strconv.Itoa(i)
		out = append(out, g.resolverSnapshot(ctx, "shadow", shadowKey, route.Name, shadow.Service, nil, shadow.Resolver))
	}
	return out
}

func (g *Gateway) resolverSnapshot(ctx context.Context, kind string, key string, name string, service string, tags map[string]string, resolver rpc.Resolver) DiscoveryRouteSnapshot {
	snapshot := DiscoveryRouteSnapshot{Kind: kind, Route: key, Name: name, Service: service, Tags: cloneMap(tags)}
	if resolver == nil {
		snapshot.Error = "resolver is nil"
		return snapshot
	}
	if snapshoter, ok := resolver.(resolverSnapshotter); ok {
		mergeResolverSnapshot(&snapshot, snapshoter.Snapshot())
	}
	if instanceResolver, ok := resolver.(rpc.InstanceResolver); ok {
		instances, err := instanceResolver.ResolveInstances(ctx)
		if err != nil {
			if snapshot.Error == "" {
				snapshot.Error = err.Error()
			}
			return snapshot
		}
		matched := filterInstances(instances, tags)
		snapshot.Instances = cloneServiceInstances(matched)
		snapshot.Endpoints = g.filterHealthy(expandInstances(matched, nil, g.maxExpandedEndpoint))
		if snapshoter, ok := resolver.(resolverSnapshotter); ok {
			mergeResolverSnapshot(&snapshot, snapshoter.Snapshot())
		}
		return snapshot
	}
	endpoints, err := resolver.Resolve(ctx)
	if err != nil {
		if snapshot.Error == "" {
			snapshot.Error = err.Error()
		}
		return snapshot
	}
	snapshot.Endpoints = g.filterHealthy(normalizeTargets(endpoints))
	if snapshoter, ok := resolver.(resolverSnapshotter); ok {
		mergeResolverSnapshot(&snapshot, snapshoter.Snapshot())
	}
	return snapshot
}

type resolverSnapshotter interface {
	Snapshot() rpc.ResolverSnapshot
}

func mergeResolverSnapshot(snapshot *DiscoveryRouteSnapshot, resolverSnapshot rpc.ResolverSnapshot) {
	if snapshot == nil {
		return
	}
	snapshot.Removed = append([]string(nil), resolverSnapshot.Removed...)
	snapshot.Stale = resolverSnapshot.Stale
	snapshot.Fallbacks = resolverSnapshot.Fallbacks
	if resolverSnapshot.Error != "" {
		snapshot.Error = resolverSnapshot.Error
	}
}

// NewStats creates a new Stats collector.
func NewStats() *Stats {
	return &Stats{routes: make(map[string]*routeStats)}
}

// Observe records a request outcome for the given route.
func (s *Stats) Observe(route string, status int, duration time.Duration) {
	if s == nil {
		return
	}
	if route == "" {
		route = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routes == nil {
		s.routes = make(map[string]*routeStats)
	}
	stats := s.routes[route]
	if stats == nil {
		stats = &routeStats{Statuses: make(map[int]int64)}
		s.routes[route] = stats
	}
	stats.Requests++
	if stats.Statuses == nil {
		stats.Statuses = make(map[int]int64)
	}
	stats.Statuses[status]++
	if status >= http.StatusInternalServerError {
		stats.Errors++
	}
	stats.TotalDuration += duration
	if duration > stats.MaxDuration {
		stats.MaxDuration = duration
	}
}

// IncRetry increments the retry counter for the given route.
func (s *Stats) IncRetry(route string, retries int) {
	if s == nil || retries <= 0 {
		return
	}
	s.withRoute(route, func(stats *routeStats) { stats.Retries += int64(retries) })
}

// IncShadow increments the shadow request counter for the given route.
func (s *Stats) IncShadow(route string) {
	if s == nil {
		return
	}
	s.withRoute(route, func(stats *routeStats) { stats.Shadowed++ })
}

// IncShadowDropped increments the shadow dropped counter for the given route.
func (s *Stats) IncShadowDropped(route string) {
	if s == nil {
		return
	}
	s.withRoute(route, func(stats *routeStats) { stats.ShadowDropped++ })
}

// IncEjection increments the passive health ejection counter for the given route.
func (s *Stats) IncEjection(route string) {
	if s == nil {
		return
	}
	s.withRoute(route, func(stats *routeStats) { stats.Ejections++ })
}

func (s *Stats) withRoute(route string, fn func(*routeStats)) {
	if route == "" {
		route = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routes == nil {
		s.routes = make(map[string]*routeStats)
	}
	stats := s.routes[route]
	if stats == nil {
		stats = &routeStats{Statuses: make(map[int]int64)}
		s.routes[route] = stats
	}
	fn(stats)
}

// Snapshot returns a sorted snapshot of all observed route metrics.
func (s *Stats) Snapshot() Snapshot {
	byKey := s.snapshotByKey()
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := Snapshot{Routes: make([]RouteSnapshot, 0, len(keys))}
	for _, key := range keys {
		out.Routes = append(out.Routes, byKey[key])
	}
	return out
}

func (s *Stats) snapshotByKey() map[string]RouteSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]RouteSnapshot, len(s.routes))
	for key, stats := range s.routes {
		statuses := make(map[int]int64, len(stats.Statuses))
		for status, count := range stats.Statuses {
			statuses[status] = count
		}
		item := RouteSnapshot{PathPrefix: key, Requests: stats.Requests, Errors: stats.Errors, Retries: stats.Retries, Shadowed: stats.Shadowed, ShadowDropped: stats.ShadowDropped, Ejections: stats.Ejections, Statuses: statuses, TotalDuration: stats.TotalDuration, MaxDuration: stats.MaxDuration}
		if stats.Requests > 0 {
			item.AvgDuration = stats.TotalDuration / time.Duration(stats.Requests)
		}
		out[key] = item
	}
	return out
}

// WritePrometheus writes route metrics in Prometheus text exposition format.
func (s *Stats) WritePrometheus(w io.Writer) error {
	if s == nil {
		return nil
	}
	snapshot := s.Snapshot()
	if _, err := fmt.Fprintln(w, "# HELP gofly_gateway_route_requests_total Total number of gateway requests by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_gateway_route_requests_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_gateway_route_errors_total Total number of failed gateway requests by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_gateway_route_errors_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_gateway_route_retries_total Total number of gateway retries by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_gateway_route_retries_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_gateway_route_shadow_total Total number of gateway shadow requests by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_gateway_route_shadow_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_gateway_route_shadow_dropped_total Total number of gateway shadow requests dropped by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_gateway_route_shadow_dropped_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_gateway_route_ejections_total Total number of passive health ejections by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_gateway_route_ejections_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_gateway_route_status_total Total number of gateway requests by route and status code."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_gateway_route_status_total counter"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# HELP gofly_gateway_route_duration_seconds Gateway request duration summary by route."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "# TYPE gofly_gateway_route_duration_seconds summary"); err != nil {
		return err
	}
	for _, route := range snapshot.Routes {
		label := prometheusLabel(routeLabel(route))
		if _, err := fmt.Fprintf(w, "gofly_gateway_route_requests_total{route=\"%s\"} %d\n", label, route.Requests); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_gateway_route_errors_total{route=\"%s\"} %d\n", label, route.Errors); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_gateway_route_retries_total{route=\"%s\"} %d\n", label, route.Retries); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_gateway_route_shadow_total{route=\"%s\"} %d\n", label, route.Shadowed); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_gateway_route_shadow_dropped_total{route=\"%s\"} %d\n", label, route.ShadowDropped); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_gateway_route_ejections_total{route=\"%s\"} %d\n", label, route.Ejections); err != nil {
			return err
		}
		statuses := make([]int, 0, len(route.Statuses))
		for status := range route.Statuses {
			statuses = append(statuses, status)
		}
		sort.Ints(statuses)
		for _, status := range statuses {
			if _, err := fmt.Fprintf(w, "gofly_gateway_route_status_total{route=\"%s\",status=%q} %d\n", label, strconv.Itoa(status), route.Statuses[status]); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "gofly_gateway_route_duration_seconds_sum{route=\"%s\"} %.9f\n", label, route.TotalDuration.Seconds()); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "gofly_gateway_route_duration_seconds_count{route=\"%s\"} %d\n", label, route.Requests); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gateway) match(r *http.Request) (routeMatch, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for i, route := range g.routes {
		if route.Method != "" && route.Method != r.Method {
			continue
		}
		if matchPathPrefix(r.URL.Path, route.PathPrefix) {
			return routeMatch{index: i, route: cloneRoute(route)}, true
		}
	}
	return routeMatch{}, false
}

func (g *Gateway) pickEndpoint(ctx context.Context, route Route) (string, error) {
	endpoints, err := g.resolveEndpoints(ctx, route)
	if err != nil {
		return "", fmt.Errorf("resolve gateway upstream: %w", err)
	}
	balancer := route.Balancer
	if balancer == nil {
		balancer = g.balancer
	}
	if balancer == nil {
		balancer = &rpc.RoundRobinBalancer{}
	}
	endpoint, err := balancer.Pick(ctx, endpoints)
	if err != nil {
		return "", fmt.Errorf("pick gateway upstream: %w", err)
	}
	return strings.TrimRight(endpoint, "/"), nil
}

func (g *Gateway) resolveEndpoints(ctx context.Context, route Route) ([]string, error) {
	if resolver, ok := route.Resolver.(rpc.InstanceResolver); ok {
		instances, err := resolver.ResolveInstances(ctx)
		if err != nil {
			return nil, err
		}
		endpoints := expandInstances(instances, route.Tags, g.maxExpandedEndpoint)
		return g.filterHealthy(endpoints), nil
	}
	endpoints, err := route.Resolver.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	return g.filterHealthy(endpoints), nil
}

func (g *Gateway) filterHealthy(endpoints []string) []string {
	if g == nil || g.passive == nil {
		return endpoints
	}
	filtered := g.passive.Filter(endpoints)
	if len(filtered) == 0 {
		return endpoints
	}
	return filtered
}

func (g *Gateway) reportEndpoint(route Route, endpoint string, success bool) {
	if g == nil || g.passive == nil {
		return
	}
	if g.passive.Report(endpoint, success) && g.stats != nil {
		g.stats.IncEjection(routeKey(route))
	}
}

func (g *Gateway) ruleRateLimiter(route Route) *limit.Limiter {
	policy := route.RateLimit
	if g == nil || g.ruleRuntime == nil || policy.Rate <= 0 && policy.Burst <= 0 {
		return nil
	}
	rate := policy.Rate
	burst := policy.Burst
	if burst <= 0 {
		burst = rate
	}
	key := route.governanceKey
	if key == "" {
		key = routeKey(route)
	}
	g.ruleRuntime.mu.Lock()
	defer g.ruleRuntime.mu.Unlock()
	cached := g.ruleRuntime.rateLimits[key]
	if cached != nil && cached.rate == rate && cached.burst == burst {
		return cached.limiter
	}
	limiter := limit.New(rate, burst)
	g.ruleRuntime.rateLimits[key] = &gatewayCachedRateLimiter{rate: rate, burst: burst, limiter: limiter}
	return limiter
}

func (g *Gateway) ruleConcurrencyLimiter(route Route) *limit.ConcurrencyLimiter {
	policy := route.Concurrency
	if g == nil || g.ruleRuntime == nil || policy.Limit <= 0 {
		return nil
	}
	key := route.governanceKey
	if key == "" {
		key = routeKey(route)
	}
	g.ruleRuntime.mu.Lock()
	defer g.ruleRuntime.mu.Unlock()
	cached := g.ruleRuntime.concurrency[key]
	if cached != nil && cached.limit == policy.Limit {
		return cached.limiter
	}
	limiter := limit.NewConcurrency(policy.Limit)
	g.ruleRuntime.concurrency[key] = &gatewayCachedConcurrencyLimiter{limit: policy.Limit, limiter: limiter}
	return limiter
}

func (g *Gateway) breakerFor(route Route) *breaker.AdaptiveBreaker {
	if g == nil || !route.Breaker.Enabled {
		return nil
	}
	key := route.governanceKey
	if key == "" {
		key = routeKey(route)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.breakers == nil {
		g.breakers = make(map[string]*gatewayCachedBreaker)
	}
	if cached := g.breakers[key]; cached != nil && cached.config == route.Breaker {
		return cached.breaker
	}
	opts := make([]breaker.AdaptiveOption, 0, 5)
	if route.Breaker.OpenTimeout > 0 {
		opts = append(opts, breaker.WithAdaptiveOpenTimeout(route.Breaker.OpenTimeout))
	}
	if route.Breaker.Window > 0 {
		opts = append(opts, breaker.WithAdaptiveWindow(route.Breaker.Window))
	}
	if route.Breaker.Buckets > 0 {
		opts = append(opts, breaker.WithAdaptiveBuckets(route.Breaker.Buckets))
	}
	if route.Breaker.MinRequests > 0 {
		opts = append(opts, breaker.WithAdaptiveMinRequests(route.Breaker.MinRequests))
	}
	if route.Breaker.FailureRatio > 0 {
		opts = append(opts, breaker.WithAdaptiveFailureRatio(route.Breaker.FailureRatio))
	}
	brk := breaker.NewAdaptive(opts...)
	g.breakers[key] = &gatewayCachedBreaker{config: route.Breaker, breaker: brk}
	return brk
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

func normalizeRoute(route Route) (Route, error) {
	route.PathPrefix = cleanPrefix(route.PathPrefix)
	if route.PathPrefix == "" {
		route.PathPrefix = "/"
	}
	route.UpstreamPrefix = cleanPrefix(route.UpstreamPrefix)
	route.Method = strings.ToUpper(strings.TrimSpace(route.Method))
	if route.Resolver == nil && route.Discovery != nil && strings.TrimSpace(route.Service) != "" {
		route.Resolver = rpc.NewDiscoveryResolver(route.Discovery, route.Service)
	}
	if route.Resolver == nil {
		if len(route.Targets) == 0 {
			return Route{}, ErrRouteRequired
		}
		route.Resolver = rpc.NewStaticResolver(route.Targets...)
	}
	route.Targets = normalizeTargets(route.Targets)
	route.Retry = normalizeRetryPolicy(route.Retry)
	route.Header = cloneHeaderPolicy(route.Header)
	route.Canary = cloneCanaryRoutes(route.Canary)
	route.Shadow = cloneShadowRoutes(route.Shadow)
	route.Aggregation = normalizeAggregationConfig(route.Aggregation)
	route.AllowedHosts = normalizeHosts(route.AllowedHosts)
	route.Tags = cloneMap(route.Tags)
	route.Headers = cloneMap(route.Headers)
	return route, nil
}

func (g *Gateway) normalizeRoute(route Route) (Route, error) {
	if g != nil {
		route = g.attachRouteResolvers(route)
	}
	return normalizeRoute(route)
}

func (g *Gateway) normalizeRoutes(routes []Route) ([]Route, error) {
	out := make([]Route, 0, len(routes))
	for _, route := range routes {
		normalized, err := g.normalizeRoute(route)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	sortRoutes(out)
	return out, nil
}

func sortRoutes(routes []Route) {
	sort.SliceStable(routes, func(i, j int) bool {
		return len(routes[i].PathPrefix) > len(routes[j].PathPrefix)
	})
}

func routeIndex(routes []Route, method, pathPrefix string) int {
	for i, route := range routes {
		if route.Method == method && route.PathPrefix == pathPrefix {
			return i
		}
	}
	return -1
}

func routeFromConfig(route RouteConfig) Route {
	return Route{
		Name:           route.Name,
		Method:         route.Method,
		PathPrefix:     route.PathPrefix,
		UpstreamPrefix: route.UpstreamPrefix,
		Service:        route.Service,
		Targets:        append([]string(nil), route.Targets...),
		Timeout:        route.Timeout,
		MaxBodyBytes:   route.MaxBodyBytes,
		PreserveHost:   route.PreserveHost,
		Retry:          normalizeRetryPolicy(route.Retry),
		Header:         cloneHeaderPolicy(route.Header),
		Breaker:        route.Breaker,
		RateLimit:      route.RateLimit,
		Concurrency:    route.Concurrency,
		Canary:         cloneCanaryRoutes(route.Canary),
		Shadow:         cloneShadowRoutes(route.Shadow),
		AllowedHosts:   append([]string(nil), route.AllowedHosts...),
		Transcode:      cloneTranscodeConfig(route.Transcode),
		Aggregation:    cloneAggregationConfig(route.Aggregation),
		Tags:           cloneMap(route.Tags),
		Headers:        cloneMap(route.Headers),
	}
}

func routeConfigFromRoute(route Route) RouteConfig {
	return RouteConfig{
		Name:           route.Name,
		Method:         route.Method,
		PathPrefix:     route.PathPrefix,
		UpstreamPrefix: route.UpstreamPrefix,
		Service:        route.Service,
		Targets:        append([]string(nil), route.Targets...),
		Timeout:        route.Timeout,
		MaxBodyBytes:   route.MaxBodyBytes,
		PreserveHost:   route.PreserveHost,
		Retry:          normalizeRetryPolicy(route.Retry),
		Header:         cloneHeaderPolicy(route.Header),
		Breaker:        route.Breaker,
		RateLimit:      route.RateLimit,
		Concurrency:    route.Concurrency,
		Canary:         cloneCanaryRoutes(route.Canary),
		Shadow:         cloneShadowRoutes(route.Shadow),
		AllowedHosts:   append([]string(nil), route.AllowedHosts...),
		Transcode:      cloneTranscodeConfig(route.Transcode),
		Aggregation:    cloneAggregationConfig(route.Aggregation),
		Tags:           cloneMap(route.Tags),
		Headers:        cloneMap(route.Headers),
	}
}

func cloneRoute(route Route) Route {
	route.Targets = append([]string(nil), route.Targets...)
	route.Header = cloneHeaderPolicy(route.Header)
	route.Canary = cloneCanaryRoutes(route.Canary)
	route.Shadow = cloneShadowRoutes(route.Shadow)
	route.Transcode = cloneTranscodeConfig(route.Transcode)
	route.Aggregation = cloneAggregationConfig(route.Aggregation)
	route.AllowedHosts = append([]string(nil), route.AllowedHosts...)
	route.Tags = cloneMap(route.Tags)
	route.Headers = cloneMap(route.Headers)
	return route
}

func cloneTranscodeConfig(config TranscodeConfig) TranscodeConfig {
	config.Payload.PathParams = append([]string(nil), config.Payload.PathParams...)
	config.Payload.QueryParams = append([]string(nil), config.Payload.QueryParams...)
	config.Payload.PathParameters = cloneTranscodeParameters(config.Payload.PathParameters)
	config.Payload.QueryParameters = cloneTranscodeParameters(config.Payload.QueryParameters)
	config.Payload.BodySchema = cloneTranscodeSchema(config.Payload.BodySchema)
	config.Payload.Mappings = cloneTranscodePayloadMappings(config.Payload.Mappings)
	return config
}

func cloneTranscodeParameters(parameters []TranscodeParameterConfig) []TranscodeParameterConfig {
	if len(parameters) == 0 {
		return nil
	}
	out := make([]TranscodeParameterConfig, len(parameters))
	for i, parameter := range parameters {
		out[i] = parameter
		if parameter.Items != nil {
			item := cloneTranscodeParameter(*parameter.Items)
			out[i].Items = &item
		}
	}
	return out
}

func cloneTranscodeParameter(parameter TranscodeParameterConfig) TranscodeParameterConfig {
	if parameter.Items != nil {
		item := cloneTranscodeParameter(*parameter.Items)
		parameter.Items = &item
	}
	return parameter
}

func cloneTranscodeSchema(schema *TranscodeSchemaConfig) *TranscodeSchemaConfig {
	if schema == nil {
		return nil
	}
	out := *schema
	out.Required = append([]string(nil), schema.Required...)
	if schema.Items != nil {
		out.Items = cloneTranscodeSchema(schema.Items)
	}
	if len(schema.Properties) > 0 {
		out.Properties = make(map[string]TranscodeSchemaConfig, len(schema.Properties))
		for name, property := range schema.Properties {
			out.Properties[name] = *cloneTranscodeSchema(&property)
		}
	}
	return &out
}

func cloneTranscodePayloadMappings(mappings []TranscodePayloadMapping) []TranscodePayloadMapping {
	if len(mappings) == 0 {
		return nil
	}
	out := make([]TranscodePayloadMapping, len(mappings))
	for i, mapping := range mappings {
		out[i] = mapping
		out[i].Default = cloneTranscodeMappingDefault(mapping.Default)
	}
	return out
}

func cloneTranscodeProfile(profile TranscodeProfile) TranscodeProfile {
	profile.RequestMappings = cloneTranscodePayloadMappings(profile.RequestMappings)
	profile.ResponseMappings = cloneTranscodePayloadMappings(profile.ResponseMappings)
	profile.ErrorMappings = cloneTranscodePayloadMappings(profile.ErrorMappings)
	return profile
}

func transcodeProfileKey(descriptor, method string) string {
	return strings.TrimSpace(descriptor) + "/" + strings.Trim(strings.TrimSpace(method), "/")
}

func validateTranscodeProfile(profile TranscodeProfile) error {
	if strings.TrimSpace(profile.Descriptor) == "" {
		return errors.New("transcode profile descriptor is required")
	}
	if strings.Trim(strings.TrimSpace(profile.DescriptorMethod), "/") == "" {
		return errors.New("transcode profile descriptor method is required")
	}
	if err := validateTranscodePayloadMappings("request", profile.RequestMappings); err != nil {
		return err
	}
	if err := validateTranscodePayloadMappings("response", profile.ResponseMappings); err != nil {
		return err
	}
	if err := validateTranscodePayloadMappings("error", profile.ErrorMappings); err != nil {
		return err
	}
	return nil
}

func validateTranscodePayloadMappings(kind string, mappings []TranscodePayloadMapping) error {
	for _, mapping := range mappings {
		if err := validateTranscodeMappingPath(kind+" source", mapping.Source, true, true); err != nil {
			return err
		}
		if err := validateTranscodeMappingPath(kind+" target", mapping.Target, false, false); err != nil {
			return err
		}
	}
	return nil
}

func compareTranscodeProfileMappings(scope string, current, candidate []TranscodePayloadMapping) []TranscodeProfileChange {
	currentBySource := transcodeMappingBySource(current)
	candidateBySource := transcodeMappingBySource(candidate)
	var changes []TranscodeProfileChange
	for source, currentMapping := range currentBySource {
		candidateMapping, ok := candidateBySource[source]
		if !ok {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "remove_mapping",
				Scope:    scope,
				Source:   source,
				Target:   currentMapping.Target,
				Severity: "breaking",
				Message:  "candidate removes an existing mapping",
			})
			continue
		}
		if strings.TrimSpace(currentMapping.Target) != strings.TrimSpace(candidateMapping.Target) {
			changes = append(changes, TranscodeProfileChange{
				Kind:     "change_target",
				Scope:    scope,
				Source:   source,
				Target:   candidateMapping.Target,
				Severity: "breaking",
				Message:  "candidate changes an existing mapping target",
			})
		}
	}
	for source, candidateMapping := range candidateBySource {
		if _, ok := currentBySource[source]; ok {
			continue
		}
		changes = append(changes, TranscodeProfileChange{
			Kind:     "add_mapping",
			Scope:    scope,
			Source:   source,
			Target:   candidateMapping.Target,
			Severity: "info",
			Message:  "candidate adds a new mapping",
		})
	}
	return changes
}

func transcodeMappingBySource(mappings []TranscodePayloadMapping) map[string]TranscodePayloadMapping {
	out := make(map[string]TranscodePayloadMapping, len(mappings))
	for _, mapping := range mappings {
		source := strings.TrimSpace(mapping.Source)
		if source == "" && mapping.Default != nil {
			source = "default:" + strings.TrimSpace(mapping.Target)
		}
		out[source] = mapping
	}
	return out
}

func validateTranscodeMappingPath(label, path string, allowEmptySource bool, source bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		if allowEmptySource {
			return nil
		}
		return fmt.Errorf("transcode mapping %s is required", label)
	}
	parts := strings.Split(path, ".")
	if source {
		root := strings.TrimSuffix(strings.TrimSpace(parts[0]), "[]")
		if root != "body" && root != "path" && root != "query" {
			return fmt.Errorf("transcode mapping %s %q must start with body, path, or query", label, path)
		}
	}
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimSuffix(part, "[]"))
		if part == "" {
			return fmt.Errorf("transcode mapping %s %q has empty segment", label, path)
		}
	}
	return nil
}

func cloneTranscodeMappingDefault(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneTranscodeMappingDefault(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneTranscodeMappingDefault(item)
		}
		return out
	default:
		return typed
	}
}

func cloneDescriptor(desc rpc.Descriptor) rpc.Descriptor {
	desc.Metadata = cloneMap(desc.Metadata)
	if len(desc.Methods) > 0 {
		methods := make([]rpc.MethodDescriptor, len(desc.Methods))
		for i, method := range desc.Methods {
			method.Metadata = cloneMap(method.Metadata)
			methods[i] = method
		}
		desc.Methods = methods
	}
	if len(desc.Streams) > 0 {
		streams := make([]rpc.StreamDescriptor, len(desc.Streams))
		for i, stream := range desc.Streams {
			stream.Metadata = cloneMap(stream.Metadata)
			streams[i] = stream
		}
		desc.Streams = streams
	}
	return desc
}

func (g *Gateway) attachRouteResolvers(route Route) Route {
	if route.Resolver == nil && route.Discovery != nil && strings.TrimSpace(route.Service) != "" {
		route.Resolver = g.discoveryResolver(route.Discovery, route.Service)
	}
	if route.Resolver == nil && strings.TrimSpace(route.Service) != "" {
		route.Resolver = g.resolver(route.Service)
	}
	for i := range route.Shadow {
		if route.Shadow[i].Resolver == nil && route.Shadow[i].Discovery != nil && strings.TrimSpace(route.Shadow[i].Service) != "" {
			route.Shadow[i].Resolver = g.discoveryResolver(route.Shadow[i].Discovery, route.Shadow[i].Service)
		}
		if route.Shadow[i].Resolver == nil && strings.TrimSpace(route.Shadow[i].Service) != "" {
			route.Shadow[i].Resolver = g.resolver(route.Shadow[i].Service)
		}
	}
	for i := range route.Canary {
		if route.Canary[i].Resolver == nil && route.Canary[i].Discovery != nil && strings.TrimSpace(route.Canary[i].Service) != "" {
			route.Canary[i].Resolver = g.discoveryResolver(route.Canary[i].Discovery, route.Canary[i].Service)
		}
		if route.Canary[i].Resolver == nil && strings.TrimSpace(route.Canary[i].Service) != "" {
			route.Canary[i].Resolver = g.resolver(route.Canary[i].Service)
		}
	}
	return route
}

func (g *Gateway) discoveryResolver(resolver discovery.Resolver, service string) rpc.Resolver {
	base := rpc.NewDiscoveryResolver(resolver, service)
	if g == nil || !g.discoveryFailover {
		return base
	}
	return wrapGatewayFailoverResolver(base)
}

func wrapGatewayFailoverResolver(resolver rpc.Resolver) rpc.Resolver {
	if resolver == nil {
		return nil
	}
	if _, ok := resolver.(*gatewayFailoverResolver); ok {
		return resolver
	}
	return newGatewayFailoverResolver(resolver)
}

func normalizeTargets(targets []string) []string {
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimRight(strings.TrimSpace(target), "/")
		if target != "" {
			out = append(out, target)
		}
	}
	return out
}

func expandInstances(instances []rpc.ServiceInstance, tags map[string]string, limit int) []string {
	if limit <= 0 {
		limit = 1000
	}
	out := make([]string, 0, len(instances))
	for _, instance := range instances {
		endpoint := strings.TrimRight(strings.TrimSpace(instance.Endpoint), "/")
		if endpoint == "" || !matchTags(instance.Tags, tags) {
			continue
		}
		weight := instance.Weight
		if weight <= 0 {
			weight = 1
		}
		for i := 0; i < weight && len(out) < limit; i++ {
			out = append(out, endpoint)
		}
	}
	return out
}

func filterInstances(instances []rpc.ServiceInstance, tags map[string]string) []rpc.ServiceInstance {
	if len(instances) == 0 {
		return nil
	}
	out := make([]rpc.ServiceInstance, 0, len(instances))
	for _, instance := range instances {
		endpoint := strings.TrimRight(strings.TrimSpace(instance.Endpoint), "/")
		if endpoint == "" || !matchTags(instance.Tags, tags) {
			continue
		}
		instance.Endpoint = endpoint
		instance.Tags = cloneMap(instance.Tags)
		instance.Metadata = cloneMap(instance.Metadata)
		out = append(out, instance)
	}
	return out
}

func cloneServiceInstances(instances []rpc.ServiceInstance) []rpc.ServiceInstance {
	if len(instances) == 0 {
		return nil
	}
	out := make([]rpc.ServiceInstance, len(instances))
	for i, instance := range instances {
		instance.Tags = cloneMap(instance.Tags)
		instance.Metadata = cloneMap(instance.Metadata)
		out[i] = instance
	}
	return out
}

func matchTags(have, want map[string]string) bool {
	for key, value := range want {
		if have[key] != value {
			return false
		}
	}
	return true
}

func normalizeRetryPolicy(policy RetryPolicy) RetryPolicy {
	if policy.Attempts <= 0 {
		policy.Attempts = 1
	}
	if policy.Backoff < 0 {
		policy.Backoff = 0
	}
	if policy.BudgetBurst <= 0 && policy.BudgetRate > 0 {
		policy.BudgetBurst = policy.BudgetRate
	}
	if policy.MaxBodyBytes < 0 {
		policy.MaxBodyBytes = 0
	}
	if len(policy.Statuses) == 0 {
		policy.Statuses = []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}
	} else {
		policy.Statuses = append([]int(nil), policy.Statuses...)
	}
	if len(policy.Methods) == 0 {
		policy.Methods = []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	} else {
		methods := make([]string, 0, len(policy.Methods))
		for _, method := range policy.Methods {
			method = strings.ToUpper(strings.TrimSpace(method))
			if method != "" {
				methods = append(methods, method)
			}
		}
		policy.Methods = methods
	}
	return policy
}

func (p RetryPolicy) matchesMethod(method string) bool {
	method = strings.ToUpper(method)
	for _, candidate := range p.Methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func (p RetryPolicy) shouldRetryStatus(status int) bool {
	for _, candidate := range p.Statuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func cloneHeaderPolicy(policy HeaderPolicy) HeaderPolicy {
	return HeaderPolicy{
		AllowRequest:  normalizeHeaderNames(policy.AllowRequest),
		DropRequest:   normalizeHeaderNames(policy.DropRequest),
		SetRequest:    cloneMap(policy.SetRequest),
		SetResponse:   cloneMap(policy.SetResponse),
		ExposeHeaders: policy.ExposeHeaders,
	}
}

func cloneShadowRoutes(routes []ShadowRoute) []ShadowRoute {
	if len(routes) == 0 {
		return nil
	}
	out := make([]ShadowRoute, len(routes))
	for i, route := range routes {
		if route.Resolver == nil && route.Discovery != nil && strings.TrimSpace(route.Service) != "" {
			route.Resolver = rpc.NewDiscoveryResolver(route.Discovery, route.Service)
		}
		route.Headers = cloneMap(route.Headers)
		route.UpstreamPrefix = cleanPrefix(route.UpstreamPrefix)
		out[i] = route
	}
	return out
}

func cloneCanaryRoutes(routes []CanaryRoute) []CanaryRoute {
	if len(routes) == 0 {
		return nil
	}
	out := make([]CanaryRoute, 0, len(routes))
	for _, route := range routes {
		route.Target = strings.TrimRight(strings.TrimSpace(route.Target), "/")
		targets := normalizeTargets(route.Targets)
		if route.Target != "" {
			targets = append([]string{route.Target}, targets...)
		}
		route.Targets = targets
		if route.Resolver == nil && route.Discovery != nil && strings.TrimSpace(route.Service) != "" {
			route.Resolver = rpc.NewDiscoveryResolver(route.Discovery, route.Service)
		}
		if route.Resolver == nil && len(route.Targets) > 0 {
			route.Resolver = rpc.NewStaticResolver(route.Targets...)
		}
		route.Headers = cloneMap(route.Headers)
		route.MatchHeaders = cloneMap(route.MatchHeaders)
		route.MatchCookies = cloneMap(route.MatchCookies)
		route.UpstreamPrefix = cleanPrefix(route.UpstreamPrefix)
		out = append(out, route)
	}
	return out
}

func normalizeHeaderNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = http.CanonicalHeaderKey(strings.TrimSpace(name))
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func normalizeHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			out = append(out, host)
		}
	}
	return out
}

func cloneHeader(header http.Header) http.Header {
	out := make(http.Header, len(header))
	for key, values := range header {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func applyHeaderPolicy(header http.Header, policy HeaderPolicy) {
	if len(policy.AllowRequest) > 0 {
		allowed := make(map[string]struct{}, len(policy.AllowRequest))
		for _, name := range policy.AllowRequest {
			allowed[http.CanonicalHeaderKey(name)] = struct{}{}
		}
		for name := range header {
			if _, ok := allowed[http.CanonicalHeaderKey(name)]; !ok {
				header.Del(name)
			}
		}
	}
	for _, name := range policy.DropRequest {
		header.Del(name)
	}
	for key, value := range policy.SetRequest {
		header.Set(key, value)
	}
}

func copyResponseHeaders(dst, src http.Header, policy HeaderPolicy) {
	for key, values := range src {
		dst[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
	for key, value := range policy.SetResponse {
		dst.Set(key, value)
	}
	if policy.ExposeHeaders && len(policy.SetResponse) > 0 {
		names := make([]string, 0, len(policy.SetResponse))
		for key := range policy.SetResponse {
			names = append(names, http.CanonicalHeaderKey(key))
		}
		sort.Strings(names)
		dst.Set("Access-Control-Expose-Headers", strings.Join(names, ", "))
	}
}

func (route Route) hostAllowed(host string) bool {
	if len(route.AllowedHosts) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	for _, allowed := range route.AllowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == host {
			return true
		}
	}
	return false
}

func newPassiveHealth(conf PassiveHealthConfig) *passiveHealth {
	failureThreshold := conf.FailureThreshold
	if failureThreshold <= 0 {
		failureThreshold = 2
	}
	ejectionDuration := conf.EjectionDuration
	if ejectionDuration <= 0 {
		ejectionDuration = 5 * time.Second
	}
	return &passiveHealth{failureThreshold: failureThreshold, ejectionDuration: ejectionDuration, endpoints: make(map[string]*endpointHealth)}
}

func (p *passiveHealth) Filter(endpoints []string) []string {
	if p == nil {
		return endpoints
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		health := p.endpoints[endpoint]
		if health == nil || health.ejectedAt.IsZero() || now.Sub(health.ejectedAt) >= p.ejectionDuration {
			out = append(out, endpoint)
		}
	}
	return out
}

func (p *passiveHealth) Report(endpoint string, success bool) bool {
	if p == nil || endpoint == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.endpoints == nil {
		p.endpoints = make(map[string]*endpointHealth)
	}
	health := p.endpoints[endpoint]
	if health == nil {
		health = &endpointHealth{}
		p.endpoints[endpoint] = health
	}
	if success {
		health.failures = 0
		health.ejectedAt = time.Time{}
		return false
	}
	health.failures++
	if health.failures >= p.failureThreshold {
		health.ejectedAt = time.Now()
		return true
	}
	return false
}

func (p *passiveHealth) Snapshot() []EndpointHealthSnapshot {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]EndpointHealthSnapshot, 0, len(p.endpoints))
	for endpoint, health := range p.endpoints {
		if health == nil {
			continue
		}
		out = append(out, EndpointHealthSnapshot{
			Endpoint:  endpoint,
			Failures:  health.failures,
			Ejected:   !health.ejectedAt.IsZero(),
			EjectedAt: health.ejectedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Endpoint < out[j].Endpoint })
	return out
}

func matchPathPrefix(path, prefix string) bool {
	if prefix == "/" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, strings.TrimRight(prefix, "/")+"/")
}

func cleanPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimRight(prefix, "/")
}

func joinURLPath(parts ...string) string {
	var out []string
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return "/"
	}
	return "/" + strings.Join(out, "/")
}

func routeKey(route Route) string {
	if route.Name != "" {
		return route.Name
	}
	if route.Method != "" {
		return route.Method + " " + route.PathPrefix
	}
	return route.PathPrefix
}

func routeLabel(route RouteSnapshot) string {
	if route.Name != "" {
		return route.Name
	}
	if route.Method != "" {
		return route.Method + " " + route.PathPrefix
	}
	return route.PathPrefix
}

func prometheusLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return strings.ReplaceAll(s, "\"", "\\\"")
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

type statusRecorder struct {
	http.ResponseWriter
	status   int
	wrote    bool
	hijacked bool
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	conn, rw, err := hijacker.Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, rw, err
}

func (w *statusRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
