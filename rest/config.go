// Package rest provides an HTTP server with middleware chaining, route groups,
// request binding, governance integration and OpenAPI generation.
package rest

import (
	"strconv"
	"time"

	"github.com/gofly/gofly/core/security"
)

const defaultMaxBodyBytes int64 = 1 << 20 // 1 MiB

const (
	PresetDevelopment = "development"
	PresetProduction  = "production"
	PresetCustom      = "custom"
)

// Config configures a REST server.
//
// Zero values produce sensible defaults: the server binds to 0.0.0.0:8080
// with a 3-second per-request timeout and all built-in middlewares enabled
// (recover, trace, log, timeout, metrics, health, request-id).
type Config struct {
	// Preset selects a built-in middleware profile. Empty defaults to
	// production for backwards-compatible safe defaults.
	Preset string `json:"preset,omitempty"`

	// Name is the logical service name used for metrics, traces, admin
	// dashboard labels, and as a default route prefix. Required.
	Name string `json:"name"`

	// Host is the bind address. Default: "0.0.0.0".
	Host string `json:"host"`

	// Port is the listen port. Default: 8080.
	Port int `json:"port"`

	// Timeout is the per-request timeout applied on every route when
	// Middlewares.Timeout is true. Default: 3s.
	Timeout time.Duration `json:"timeout"`

	// MaxBodyBytes limits the request body read size. Default: 1 MiB.
	MaxBodyBytes int64 `json:"maxBodyBytes"`

	// TLS configures server-side TLS. Leave zero-valued to serve plain HTTP.
	TLS security.TLSConfig `json:"tls,omitempty"`

	// Middlewares controls which built-in middleware layers are installed.
	// When DisableDefaultMiddlewares is false (default) the server enables
	// Recover, Trace, Log, Timeout, Metrics, Health, and RequestID.
	Middlewares MiddlewaresConfig `json:"middlewares"`

	// Validator validates bound request structs. Leave nil to use gofly's
	// built-in validate tag support; set it to adapt project-specific validators
	// such as go-playground/validator without adding them to gofly's dependency graph.
	Validator Validator `json:"-"`

	// Admin configures the optional /admin/* governance endpoints.
	Admin AdminConfig `json:"admin,omitempty"`

	// DisableDefaultMiddlewares, when true, skips the automatic wiring of
	// all built-in middlewares. Callers must then wire every layer they
	// need via Options or middleware registration.
	DisableDefaultMiddlewares bool `json:"disableDefaultMiddlewares,omitempty"`
}

// SamplingConfig controls probability-based sampling for trace or log events.
type SamplingConfig struct {
	// Ratio is the fraction of events to sample (0.0 – 1.0). Default: 1.0
	// (sample everything).
	Ratio float64 `json:"ratio"`

	// ParentBased, when true, respects an upstream sampling decision and
	// only samples if the parent span was already sampled.
	ParentBased bool `json:"parentBased,omitempty"`
}

// AdminConfig controls the /admin/* governance endpoints.
type AdminConfig struct {
	// Enabled controls whether the admin HTTP handler is mounted.
	Enabled bool `json:"enabled"`

	// PathPrefix sets the URL prefix for admin routes. Default: "/admin".
	PathPrefix string `json:"pathPrefix,omitempty"`

	// Pprof, when true, registers runtime profiling endpoints under
	// PathPrefix/debug/pprof/.
	Pprof bool `json:"pprof,omitempty"`

	// Token, when set, requires this bearer token on all admin requests.
	Token string `json:"token,omitempty"`

	// Audit, when true, logs every admin request for compliance tracing.
	Audit bool `json:"audit,omitempty"`
}

// MiddlewaresConfig declares which built-in middleware layers are wired
// into the server. Most boolean fields default to true when
// Config.DisableDefaultMiddlewares is false.
type MiddlewaresConfig struct {
	Recover              bool                   `json:"recover"`
	TrimStrings          *bool                  `json:"trimStrings,omitempty"`
	TrimStringsConfig    TrimStringsConfig      `json:"trimStringsConfig,omitempty"`
	Trace                bool                   `json:"trace"`
	TraceSampling        *SamplingConfig        `json:"traceSampling,omitempty"`
	Log                  bool                   `json:"log"`
	LogSampling          *SamplingConfig        `json:"logSampling,omitempty"`
	Timeout              bool                   `json:"timeout"`
	TimeoutConfig        TimeoutConfig          `json:"timeoutConfig,omitempty"`
	MaxBodyBytesConfig   MaxBodyBytesConfig     `json:"maxBodyBytesConfig,omitempty"`
	Breaker              bool                   `json:"breaker"`
	BreakerConfig        BreakerConfig          `json:"breakerConfig,omitempty"`
	RateLimit            bool                   `json:"rateLimit"`
	RateLimitConfig      RateLimitConfig        `json:"rateLimitConfig,omitempty"`
	AdaptiveRateLimit    bool                   `json:"adaptiveRateLimit"`
	AdaptiveLimitConfig  AdaptiveLimitConfig    `json:"adaptiveLimitConfig,omitempty"`
	MaxConcurrency       bool                   `json:"maxConcurrency"`
	MaxConcurrencyConfig MaxConcurrencyConfig   `json:"maxConcurrencyConfig,omitempty"`
	CORS                 *CORSConfig            `json:"cors,omitempty"`
	CSRF                 *CSRFConfig            `json:"csrf,omitempty"`
	SecurityHeaders      *SecurityHeadersConfig `json:"securityHeaders,omitempty"`
	LogRedaction         LogRedactionConfig     `json:"logRedaction,omitempty"`
	Metrics              bool                   `json:"metrics"`
	Health               bool                   `json:"health"`
	RequestID            bool                   `json:"requestId"`
}

// TimeoutConfig controls per-route timeout behaviour.
type TimeoutConfig struct {
	// Duration is the per-request timeout. Default: Config.Timeout (3s).
	Duration time.Duration `json:"duration,omitempty"`

	// ReadHeaderTimeout limits how long the server waits to read the
	// request headers. Default: 0 (no limit).
	ReadHeaderTimeout time.Duration `json:"readHeaderTimeout,omitempty"`

	// HealthTimeout is a shorter timeout applied specifically to health-
	// check endpoints so they do not block under load.
	HealthTimeout time.Duration `json:"healthTimeout,omitempty"`
}

// MaxBodyBytesConfig overrides the global Config.MaxBodyBytes for selective
// routes or groups.
type MaxBodyBytesConfig struct {
	// Limit is the per-request body size limit in bytes. Default: 1 MiB.
	Limit int64 `json:"limit,omitempty"`
}

// TrimStringsConfig controls automatic whitespace trimming on request data.
// Each field defaults to nil (the caller's TrimStrings boolean governs
// behaviour); set an explicit pointer to override per-source.
type TrimStringsConfig struct {
	// Query, when true, trims whitespace from query string values.
	// Default: nil (use MiddlewaresConfig.TrimStrings).
	Query *bool `json:"query,omitempty"`
	// Body, when true, trims whitespace from form or JSON body values.
	// Default: nil (use MiddlewaresConfig.TrimStrings).
	Body *bool `json:"body,omitempty"`
	// JSON, when true, trims whitespace during JSON binding.
	// Default: nil (use MiddlewaresConfig.TrimStrings).
	JSON *bool `json:"json,omitempty"`
}

// BreakerConfig configures the circuit breaker middleware behaviour.
type BreakerConfig struct {
	// OpenTimeout is how long the breaker stays open before half-opening.
	OpenTimeout time.Duration `json:"openTimeout,omitempty"`

	// Window is the sliding time window for failure ratio calculation.
	Window time.Duration `json:"window,omitempty"`

	// Buckets is the number of time buckets in the sliding window.
	Buckets int `json:"buckets,omitempty"`

	// MinRequests is the minimum number of requests that must be recorded
	// in the window before the breaker evaluates the failure ratio.
	MinRequests int64 `json:"minRequests,omitempty"`

	// FailureRatio above which the breaker trips open (0.0 – 1.0).
	FailureRatio float64 `json:"failureRatio,omitempty"`

	// K is the sensitivity multiplier for the Google SRE adaptive breaker.
	K float64 `json:"k,omitempty"`
}

// RateLimitConfig configures token-bucket rate limiting per route.
type RateLimitConfig struct {
	// Rate is the number of tokens added per second.
	Rate int `json:"rate"`

	// Burst is the maximum accumulated tokens (allows short bursts).
	Burst int `json:"burst"`
}

// AdaptiveLimitConfig configures the adaptive (heuristic) rate limiter that
// adjusts the concurrency limit based on latency and error rate.
type AdaptiveLimitConfig struct {
	// MinLimit is the floor for the adaptive concurrency limit. Default: 16.
	MinLimit int `json:"minLimit,omitempty"`
	// MaxLimit is the ceiling for the adaptive concurrency limit. Default: 512.
	MaxLimit int `json:"maxLimit,omitempty"`
	// InitialLimit is the starting concurrency limit on server boot. Default: 64.
	InitialLimit int `json:"initialLimit,omitempty"`
	// CPUThreshold is the CPU utilisation percentage that triggers
	// aggressive limit reduction. Default: 80.
	CPUThreshold int `json:"cpuThreshold,omitempty"`
	// Window is the sliding time window over which latency and error
	// samples are evaluated. Default: 10s.
	Window time.Duration `json:"window,omitempty"`
	// TargetLatency is the ideal per-request latency. The limiter
	// increases concurrency below this target and decreases above.
	// Default: 100ms.
	TargetLatency time.Duration `json:"targetLatency,omitempty"`
	// TargetErrorRatio is the acceptable error rate above which the
	// limiter reduces concurrency (0.0 – 1.0). Default: 0.05.
	TargetErrorRatio float64 `json:"targetErrorRatio,omitempty"`
	// MinSamples is the minimum request samples before the limiter
	// adjusts the concurrency limit. Default: 100.
	MinSamples int64 `json:"minSamples,omitempty"`
}

// MaxConcurrencyConfig limits the number of concurrent in-flight requests.
type MaxConcurrencyConfig struct {
	// Limit is the maximum number of concurrent requests allowed. 0 means
	// unlimited.
	Limit int `json:"limit"`
}

// SecurityHeadersConfig configures HTTP security response headers per route
// or globally. All fields default to empty (header is not set).
type SecurityHeadersConfig struct {
	// ContentSecurityPolicy sets Content-Security-Policy header value.
	ContentSecurityPolicy string `json:"contentSecurityPolicy,omitempty"`
	// FrameOptions sets X-Frame-Options (e.g. "DENY", "SAMEORIGIN").
	FrameOptions string `json:"frameOptions,omitempty"`
	// ContentTypeOptions sets X-Content-Type-Options (usually "nosniff").
	ContentTypeOptions string `json:"contentTypeOptions,omitempty"`
	// ReferrerPolicy sets Referrer-Policy header value.
	ReferrerPolicy string `json:"referrerPolicy,omitempty"`
	// PermissionsPolicy sets Permissions-Policy header value.
	PermissionsPolicy string `json:"permissionsPolicy,omitempty"`
	// HSTS sets Strict-Transport-Security header value
	// (e.g. "max-age=31536000; includeSubDomains").
	HSTS string `json:"hsts,omitempty"`
	// Custom is a key-value map of additional security headers.
	Custom map[string]string `json:"custom,omitempty"`
}

// LogRedactionConfig controls which request headers and query parameters are
// redacted before being written to the access log. Sensitive fields such as
// Authorization, Cookie, and Set-Cookie are always redacted regardless of
// this configuration.
type LogRedactionConfig struct {
	// Headers lists request header names whose values are replaced with
	// "***" in access logs. Default: empty (only built-in sensitive
	// headers are redacted).
	Headers []string `json:"headers,omitempty"`
	// Queries lists query parameter names whose values are replaced with
	// "***" in access logs. Default: empty.
	Queries []string `json:"queries,omitempty"`
}

func (c Config) addr() string {
	host := c.Host
	if host == "" {
		host = "0.0.0.0"
	}
	if c.Port == 0 {
		c.Port = 8080
	}
	return host + ":" + fmtInt(c.Port)
}

func fmtInt(v int) string { return strconv.Itoa(v) }
