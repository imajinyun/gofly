// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/governance"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/retry"
	"github.com/gofly/gofly/core/security"
	"github.com/gofly/gofly/core/syncx"
	controladmin "github.com/gofly/gofly/ops/admin"
	"github.com/gofly/gofly/rpc/endpoint"
)

type ServerOption func(*serverOptions)
type ClientOption func(*clientOptions)

type SingleflightKeyFunc func(ctx context.Context, method string, request any) (string, error)

type Suite interface {
	ServerOptions() []ServerOption
	ClientOptions() []ClientOption
}

type serverOptions struct {
	addr              string
	codec             Codec
	middlewares       []endpoint.Middleware
	streamMiddlewares []StreamMiddleware
	registrar         Registrar
	serviceName       string
	advertiseEndpoint string
	registryTTL       time.Duration
	registryRefresh   time.Duration
	adminToken        string
	governance        *governance.Registry
	manager           *governance.Manager
	rules             *governance.RuleSet
	readHeaderTimeout time.Duration
	adminAudit        controladmin.AuditSink
	tls               security.TLSConfig
}

type clientOptions struct {
	codec             Codec
	httpClient        *http.Client
	timeout           time.Duration
	streamTimeout     time.Duration
	retry             int
	retryPolicy       retry.Policy
	breaker           *breaker.Breaker
	adaptive          *breaker.AdaptiveBreaker
	resolver          Resolver
	balancer          Balancer
	middlewares       []endpoint.Middleware
	streamMiddlewares []ClientStreamMiddleware
	singleflight      *syncx.Group[*callResult]
	singleflightKey   SingleflightKeyFunc
	manager           *governance.Manager
	rules             *governance.RuleSet
	governanceTags    map[string]string
	tls               *security.TLSConfig
}

type TransportConfig struct {
	Timeout               time.Duration                         `json:"timeout"`
	MaxIdleConns          int                                   `json:"maxIdleConns"`
	MaxIdleConnsPerHost   int                                   `json:"maxIdleConnsPerHost"`
	MaxConnsPerHost       int                                   `json:"maxConnsPerHost"`
	DialTimeout           time.Duration                         `json:"dialTimeout"`
	KeepAlive             time.Duration                         `json:"keepAlive"`
	IdleConnTimeout       time.Duration                         `json:"idleConnTimeout"`
	TLSHandshakeTimeout   time.Duration                         `json:"tlsHandshakeTimeout"`
	ResponseHeaderTimeout time.Duration                         `json:"responseHeaderTimeout"`
	ExpectContinueTimeout time.Duration                         `json:"expectContinueTimeout"`
	Proxy                 func(*http.Request) (*url.URL, error) `json:"-"`
	TLSClientConfig       *tls.Config                           `json:"-"`
}

func DefaultTransportConfig() TransportConfig {
	return TransportConfig{
		Timeout:               30 * time.Second,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       0,
		DialTimeout:           30 * time.Second,
		KeepAlive:             30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0,
		ExpectContinueTimeout: time.Second,
	}
}

func IsZeroTransportConfig(conf TransportConfig) bool {
	return conf.Timeout == 0 &&
		conf.MaxIdleConns == 0 &&
		conf.MaxIdleConnsPerHost == 0 &&
		conf.MaxConnsPerHost == 0 &&
		conf.DialTimeout == 0 &&
		conf.KeepAlive == 0 &&
		conf.IdleConnTimeout == 0 &&
		conf.TLSHandshakeTimeout == 0 &&
		conf.ResponseHeaderTimeout == 0 &&
		conf.ExpectContinueTimeout == 0 &&
		conf.Proxy == nil &&
		conf.TLSClientConfig == nil
}

func NewHTTPClient(conf TransportConfig) *http.Client {
	if conf.Timeout <= 0 {
		conf.Timeout = 30 * time.Second
	}
	if conf.MaxIdleConns <= 0 {
		conf.MaxIdleConns = 200
	}
	if conf.MaxIdleConnsPerHost <= 0 {
		conf.MaxIdleConnsPerHost = 100
	}
	if conf.DialTimeout <= 0 {
		conf.DialTimeout = 30 * time.Second
	}
	if conf.KeepAlive <= 0 {
		conf.KeepAlive = 30 * time.Second
	}
	if conf.IdleConnTimeout <= 0 {
		conf.IdleConnTimeout = 90 * time.Second
	}
	if conf.TLSHandshakeTimeout <= 0 {
		conf.TLSHandshakeTimeout = 10 * time.Second
	}
	if conf.ExpectContinueTimeout <= 0 {
		conf.ExpectContinueTimeout = time.Second
	}
	proxy := conf.Proxy
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Timeout: conf.Timeout, Transport: &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: conf.DialTimeout, KeepAlive: conf.KeepAlive}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          conf.MaxIdleConns,
		MaxIdleConnsPerHost:   conf.MaxIdleConnsPerHost,
		MaxConnsPerHost:       conf.MaxConnsPerHost,
		IdleConnTimeout:       conf.IdleConnTimeout,
		TLSHandshakeTimeout:   conf.TLSHandshakeTimeout,
		ResponseHeaderTimeout: conf.ResponseHeaderTimeout,
		ExpectContinueTimeout: conf.ExpectContinueTimeout,
		TLSClientConfig:       conf.TLSClientConfig,
	}}
}

func WithAddress(addr string) ServerOption {
	return func(o *serverOptions) { o.addr = addr }
}

func WithServerCodec(codec Codec) ServerOption {
	return func(o *serverOptions) { o.codec = codec }
}

func WithServerMiddleware(mw endpoint.Middleware) ServerOption {
	return func(o *serverOptions) { o.middlewares = append(o.middlewares, mw) }
}

func WithServerStreamMiddleware(mw StreamMiddleware) ServerOption {
	return func(o *serverOptions) { o.streamMiddlewares = append(o.streamMiddlewares, mw) }
}

func WithServerMaxConcurrency(max int) ServerOption {
	return func(o *serverOptions) {
		o.middlewares = append(o.middlewares, MaxConcurrencyMiddleware(max))
	}
}

func WithServerAdaptiveLimiter(limiter *limit.AdaptiveLimiter) ServerOption {
	return func(o *serverOptions) {
		if limiter == nil {
			limiter = limit.NewAdaptiveLimiter()
		}
		ensureGovernance(o).Register("adaptive-rate-limit", "adaptive_limiter", "server", func() any { return limiter.Snapshot() })
		o.middlewares = append(o.middlewares, AdaptiveLimitMiddleware(limiter))
	}
}

func WithServerAdaptiveBreaker(brk *breaker.AdaptiveBreaker) ServerOption {
	return func(o *serverOptions) {
		if brk == nil {
			brk = breaker.NewAdaptive()
		}
		ensureGovernance(o).Register("adaptive-breaker", "adaptive_breaker", "server", func() any { return brk.Snapshot() })
		o.middlewares = append(o.middlewares, AdaptiveBreakerMiddleware(brk))
	}
}

func ensureGovernance(o *serverOptions) *governance.Registry {
	if o.governance == nil {
		o.governance = governance.NewRegistry()
	}
	return o.governance
}

func WithServerSuite(suite Suite) ServerOption {
	return func(o *serverOptions) {
		if suite == nil {
			return
		}
		for _, opt := range suite.ServerOptions() {
			opt(o)
		}
	}
}

func WithRegistry(registrar Registrar, serviceName string, advertiseEndpoint string) ServerOption {
	return func(o *serverOptions) {
		o.registrar = registrar
		o.serviceName = serviceName
		o.advertiseEndpoint = advertiseEndpoint
	}
}

func WithRegistryTTL(ttl time.Duration) ServerOption {
	return func(o *serverOptions) {
		if ttl > 0 {
			o.registryTTL = ttl
		}
	}
}

func WithRegistryRefreshInterval(interval time.Duration) ServerOption {
	return func(o *serverOptions) {
		if interval > 0 {
			o.registryRefresh = interval
		}
	}
}

func WithServerAdminToken(token string) ServerOption {
	return func(o *serverOptions) {
		o.adminToken = token
	}
}

func WithServerAdminAuditSink(sink controladmin.AuditSink) ServerOption {
	return func(o *serverOptions) {
		o.adminAudit = sink
	}
}

func WithServerRuleSet(rules *governance.RuleSet) ServerOption {
	return func(o *serverOptions) {
		o.rules = rules
	}
}

func WithServerGovernanceRuleSet(rules *governance.RuleSet) ServerOption {
	return WithServerRuleSet(rules)
}

func WithServerGovernanceManager(manager *governance.Manager) ServerOption {
	return func(o *serverOptions) {
		o.manager = manager
		if manager != nil {
			o.rules = manager.RuleSet()
		}
	}
}

func WithServerGovernanceSuite(suite *governance.Suite) ServerOption {
	return func(o *serverOptions) {
		if o.manager != nil {
			return
		}
		if suite != nil {
			o.rules = governance.MergeRuleSets(o.rules, suite.RuleSet())
		}
	}
}

func WithServerReadHeaderTimeout(timeout time.Duration) ServerOption {
	return func(o *serverOptions) {
		if timeout > 0 {
			o.readHeaderTimeout = timeout
		}
	}
}

func WithServerTLS(certFile string, keyFile string) ServerOption {
	return func(o *serverOptions) {
		o.tls = security.TLSConfig{CertFile: certFile, KeyFile: keyFile}
	}
}

// WithServerTLSConfig configures TLS or mutual TLS for the self-developed RPC
// server. Set CertFile/KeyFile for TLS and ClientCAFile to additionally require
// and verify client certificates (mTLS).
func WithServerTLSConfig(cfg security.TLSConfig) ServerOption {
	return func(o *serverOptions) {
		o.tls = cfg
	}
}

func WithCodec(codec Codec) ClientOption {
	return func(o *clientOptions) { o.codec = codec }
}

func WithTimeout(timeout time.Duration) ClientOption {
	return func(o *clientOptions) { o.timeout = timeout }
}

// WithClientStreamTimeout configures per-operation read/write deadlines for
// streams created by HTTPClient.Stream. It is intentionally separate from the
// unary request timeout so long-lived streams are not capped by default.
func WithClientStreamTimeout(timeout time.Duration) ClientOption {
	return func(o *clientOptions) {
		if timeout > 0 {
			o.streamTimeout = timeout
		}
	}
}

func WithHTTPClient(client *http.Client) ClientOption {
	return func(o *clientOptions) {
		if client != nil {
			o.httpClient = client
		}
	}
}

func WithTransportConfig(conf TransportConfig) ClientOption {
	return func(o *clientOptions) {
		o.httpClient = NewHTTPClient(conf)
	}
}

// WithClientTLS configures TLS or mutual TLS for the self-developed RPC client.
// Provide CAFile to verify the server and CertFile/KeyFile to present a client
// identity (mTLS). The target must use the https:// scheme for TLS to take
// effect.
func WithClientTLS(cfg security.TLSConfig) ClientOption {
	return func(o *clientOptions) {
		c := cfg
		o.tls = &c
	}
}

func WithRetry(attempts int) ClientOption {
	return func(o *clientOptions) { o.retry = attempts }
}

func WithRetryPolicy(policy retry.Policy) ClientOption {
	return func(o *clientOptions) { o.retryPolicy = policy }
}

func WithBreaker(brk *breaker.Breaker) ClientOption {
	return func(o *clientOptions) {
		o.breaker = brk
		o.streamMiddlewares = append(o.streamMiddlewares, ClientStreamBreakerMiddleware(brk))
	}
}

func WithAdaptiveBreaker(brk *breaker.AdaptiveBreaker) ClientOption {
	return func(o *clientOptions) {
		o.adaptive = brk
		if brk != nil {
			o.streamMiddlewares = append(o.streamMiddlewares, ClientStreamAdaptiveBreakerMiddleware(brk))
		}
	}
}

func WithResolver(resolver Resolver) ClientOption {
	return func(o *clientOptions) { o.resolver = resolver }
}

func WithBalancer(balancer Balancer) ClientOption {
	return func(o *clientOptions) { o.balancer = balancer }
}

func WithClientMiddleware(mw endpoint.Middleware) ClientOption {
	return func(o *clientOptions) { o.middlewares = append(o.middlewares, mw) }
}

func WithClientStreamMiddleware(mw ClientStreamMiddleware) ClientOption {
	return func(o *clientOptions) { o.streamMiddlewares = append(o.streamMiddlewares, mw) }
}

func WithClientMaxConcurrency(max int) ClientOption {
	return func(o *clientOptions) {
		o.middlewares = append(o.middlewares, MaxConcurrencyMiddleware(max))
		o.streamMiddlewares = append(o.streamMiddlewares, ClientStreamMaxConcurrencyMiddleware(max))
	}
}

func WithClientAdaptiveLimiter(limiter *limit.AdaptiveLimiter) ClientOption {
	return func(o *clientOptions) {
		o.middlewares = append(o.middlewares, AdaptiveLimitMiddleware(limiter))
		o.streamMiddlewares = append(o.streamMiddlewares, ClientStreamAdaptiveLimitMiddleware(limiter))
	}
}

func WithClientSingleflight() ClientOption {
	return func(o *clientOptions) {
		o.singleflight = &syncx.Group[*callResult]{}
	}
}

func WithClientSingleflightKey(fn SingleflightKeyFunc) ClientOption {
	return func(o *clientOptions) {
		o.singleflight = &syncx.Group[*callResult]{}
		o.singleflightKey = fn
	}
}

func WithClientRuleSet(rules *governance.RuleSet) ClientOption {
	return func(o *clientOptions) {
		o.rules = rules
	}
}

func WithClientGovernanceRuleSet(rules *governance.RuleSet) ClientOption {
	return WithClientRuleSet(rules)
}

func WithClientGovernanceManager(manager *governance.Manager) ClientOption {
	return func(o *clientOptions) {
		o.manager = manager
		if manager != nil {
			o.rules = manager.RuleSet()
		}
	}
}

func WithClientGovernanceSuite(suite *governance.Suite) ClientOption {
	return func(o *clientOptions) {
		if o.manager != nil {
			return
		}
		if suite != nil {
			o.rules = governance.MergeRuleSets(o.rules, suite.RuleSet())
		}
	}
}

func WithClientGovernanceTags(tags map[string]string) ClientOption {
	return func(o *clientOptions) {
		o.governanceTags = cloneStringMap(tags)
	}
}

func WithClientSuite(suite Suite) ClientOption {
	return func(o *clientOptions) {
		if suite == nil {
			return
		}
		for _, opt := range suite.ClientOptions() {
			opt(o)
		}
	}
}

type BasicSuite struct {
	Server []ServerOption
	Client []ClientOption
}

func (s BasicSuite) ServerOptions() []ServerOption { return append([]ServerOption(nil), s.Server...) }

func (s BasicSuite) ClientOptions() []ClientOption { return append([]ClientOption(nil), s.Client...) }
