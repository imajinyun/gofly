// Package app provides the gofly application runtime lifecycle management.
// It coordinates server startup, graceful shutdown, hooks, and production
// configuration defaults.
package app

import (
	"context"
	"time"

	coregovernance "github.com/gofly/gofly/core/governance"
	coreproc "github.com/gofly/gofly/core/proc"
	coretrace "github.com/gofly/gofly/core/observability/trace"
	"github.com/gofly/gofly/rest"
	"github.com/gofly/gofly/rpc"
)

const (
	defaultServiceEnvironment = "development"
	developmentEnvironment    = "development"
	productionEnvironment     = "production"
	testEnvironment           = "test"
)

// ServiceConf is the high-level service configuration used by generated
// projects. It groups runtime primitives that are usually wired together:
// logging, metrics, tracing, profile endpoints, health checks and lifecycle
// timeouts.
type ServiceConf struct {
	Name            string                `json:"name"`
	Mode            string                `json:"mode,omitempty"`
	Environment     string                `json:"environment,omitempty"`
	StartupTimeout  time.Duration         `json:"startupTimeout,omitempty"`
	ShutdownTimeout time.Duration         `json:"shutdownTimeout,omitempty"`
	Shutdown        ShutdownConfig        `json:"shutdown,omitempty"`
	Log             LogConfig             `json:"log"`
	Metrics         MetricsConfig         `json:"metrics"`
	Trace           coretrace.AgentConfig `json:"trace"`
	Profile         ProfileConfig         `json:"profile"`
	Health          HealthConfig          `json:"health"`
	Governance      ServiceGovernance     `json:"governance"`
	BuildInfo       coreproc.BuildInfo    `json:"-"`
}

// ShutdownConfig mirrors app.Run lifecycle timeouts while keeping a dedicated
// nested section for production service configuration files.
type ShutdownConfig struct {
	StartupTimeout time.Duration `json:"startupTimeout,omitempty"`
	Timeout        time.Duration `json:"timeout,omitempty"`
}

// ServiceGovernance contains the default runtime policies shared by generated
// REST and RPC services.
type ServiceGovernance struct {
	Disabled          bool                 `json:"disabled,omitempty"`
	Timeout           time.Duration        `json:"timeout,omitempty"`
	ReadHeaderTimeout time.Duration        `json:"readHeaderTimeout,omitempty"`
	Breaker           bool                 `json:"breaker"`
	RateLimit         ServiceRateLimit     `json:"rateLimit,omitempty"`
	MaxConcurrency    int                  `json:"maxConcurrency,omitempty"`
	AdaptiveLimit     bool                 `json:"adaptiveLimit,omitempty"`
	RestBreaker       rest.BreakerConfig   `json:"restBreaker,omitempty"`
	RPCTimeout        rpc.RPCTimeoutConfig `json:"rpcTimeout,omitempty"`
	RPCTransport      rpc.TransportConfig  `json:"rpcTransport,omitempty"`
}

// ServiceRateLimit configures per-service rate limiting.
//
// Both fields must be positive for rate limiting to take effect.
type ServiceRateLimit struct {
	// Rate is the maximum request rate (requests per second).
	Rate int `json:"rate,omitempty"`
	// Burst is the maximum burst size.
	Burst int `json:"burst,omitempty"`
}

// DefaultServiceConf returns a production-ready baseline that callers can
// override field-by-field.
func DefaultServiceConf(name string) ServiceConf {
	return ServiceConf{Name: name}.WithDefaults(name)
}

// DevelopmentServiceConf returns defaults optimized for local development:
// verbose logs, full tracing, metrics and local profile endpoints enabled.
func DevelopmentServiceConf(name string) ServiceConf {
	conf := ServiceConf{
		Name:        name,
		Mode:        developmentEnvironment,
		Environment: developmentEnvironment,
		Log: LogConfig{
			Level:  "debug",
			Format: "console",
			Trace:  true,
		},
		Metrics: MetricsConfig{Enabled: true},
		Trace: coretrace.AgentConfig{
			Enabled:     true,
			ServiceName: name,
			SampleRatio: 1,
		},
		Profile: ProfileConfig{Enabled: true},
	}
	return conf.WithDefaults(name)
}

// ProductionServiceConf returns secure production defaults with structured
// logging, metrics, sampled tracing and shared governance enabled.
func ProductionServiceConf(name string) ServiceConf {
	conf := ServiceConf{
		Name:        name,
		Mode:        productionEnvironment,
		Environment: productionEnvironment,
		Log: LogConfig{
			Level:  "info",
			Format: "json",
			Trace:  true,
		},
		Metrics: MetricsConfig{Enabled: true},
		Trace: coretrace.AgentConfig{
			Enabled:     true,
			ServiceName: name,
			SampleRatio: 0.1,
		},
		Profile: ProfileConfig{
			Enabled: false,
			Addr:    "127.0.0.1:6060",
		},
	}
	return conf.WithDefaults(name)
}

// TestServiceConf returns deterministic, low-noise defaults for unit and
// integration tests. Governance and observability side effects are disabled by
// default while lifecycle timeouts remain bounded.
func TestServiceConf(name string) ServiceConf {
	conf := ServiceConf{
		Name:            name,
		Mode:            testEnvironment,
		Environment:     testEnvironment,
		StartupTimeout:  time.Second,
		ShutdownTimeout: time.Second,
		Log: LogConfig{
			Level:  "warn",
			Format: "console",
		},
		Governance: ServiceGovernance{Disabled: true},
	}
	return conf.WithDefaults(name)
}

// WithDefaults fills unset fields without mutating the receiver.
func (c ServiceConf) WithDefaults(fallbackName string) ServiceConf {
	if c.Name == "" {
		c.Name = fallbackName
	}
	if c.Name == "" {
		c.Name = "gofly-app"
	}
	if c.Environment == "" {
		c.Environment = defaultServiceEnvironment
	}
	if c.StartupTimeout <= 0 {
		c.StartupTimeout = c.Shutdown.StartupTimeout
	}
	if c.StartupTimeout <= 0 {
		c.StartupTimeout = 5 * time.Second
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = c.Shutdown.Timeout
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = 10 * time.Second
	}
	if c.Shutdown.StartupTimeout <= 0 {
		c.Shutdown.StartupTimeout = c.StartupTimeout
	}
	if c.Shutdown.Timeout <= 0 {
		c.Shutdown.Timeout = c.ShutdownTimeout
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
	if c.Trace.ServiceName == "" {
		c.Trace.ServiceName = c.Name
	}
	if c.Trace.SampleRatio <= 0 || c.Trace.SampleRatio > 1 {
		c.Trace.SampleRatio = 1
	}
	if c.Profile.Addr == "" {
		c.Profile.Addr = "127.0.0.1:6060"
	}
	if c.Profile.PathPrefix == "" {
		c.Profile.PathPrefix = "/debug/pprof"
	}
	if c.Profile.ReadHeaderTimeout <= 0 {
		c.Profile.ReadHeaderTimeout = 5 * time.Second
	}
	if c.Health.Timeout <= 0 {
		c.Health.Timeout = time.Second
	}
	if !c.Governance.Disabled {
		defaults := c.Governance.ProductionDefaultsConfig(c.Name)
		if c.Governance.Timeout <= 0 {
			c.Governance.Timeout = defaults.RESTTimeout
		}
		if c.Governance.ReadHeaderTimeout <= 0 {
			c.Governance.ReadHeaderTimeout = 3 * time.Second
		}
		c.Governance.Breaker = defaults.Breaker.Enabled
	}
	return c
}

// ProductionGovernanceConfig extracts the ProductionDefaultsConfig from
// ServiceConf. It applies WithDefaults before returning the config so that
// callers can use it without separate normalization.
func (c ServiceConf) ProductionGovernanceConfig() coregovernance.ProductionDefaultsConfig {
	c = c.WithDefaults(c.Name)
	return c.Governance.ProductionDefaultsConfig(c.Name)
}

// ProductionGovernancePlugin returns a governance Plugin wired from
// ServiceConf. Returns nil when governance is disabled.
func (c ServiceConf) ProductionGovernancePlugin() coregovernance.Plugin {
	c = c.WithDefaults(c.Name)
	if c.Governance.Disabled {
		return nil
	}
	return coregovernance.ProductionDefaultsWithConfig(c.ProductionGovernanceConfig())
}

// ProductionDefaultsConfig converts ServiceGovernance into the low-level
// coregovernance.ProductionDefaultsConfig used by REST, RPC and MQ transports.
func (g ServiceGovernance) ProductionDefaultsConfig(service string) coregovernance.ProductionDefaultsConfig {
	conf := coregovernance.DefaultProductionDefaultsConfig(service)
	if g.Timeout > 0 {
		conf.RESTTimeout = g.Timeout
		conf.RPCTimeout = g.Timeout
		conf.MQTimeout = g.Timeout
	}
	if g.RPCTimeout.Server > 0 {
		conf.RPCTimeout = g.RPCTimeout.Server
	}
	if g.RPCTimeout.Client > conf.RPCTimeout {
		conf.RPCTimeout = g.RPCTimeout.Client
	}
	if g.RateLimit.Rate > 0 {
		conf.RateLimit = g.RateLimit.Rate
		conf.RateBurst = g.RateLimit.Burst
	}
	if g.MaxConcurrency > 0 {
		conf.ConcurrencyLimit = g.MaxConcurrency
	}
	if g.RestBreaker != (rest.BreakerConfig{}) {
		conf.Breaker = coregovernance.BreakerPolicy{
			Enabled:      true,
			OpenTimeout:  g.RestBreaker.OpenTimeout,
			Window:       g.RestBreaker.Window,
			Buckets:      g.RestBreaker.Buckets,
			MinRequests:  g.RestBreaker.MinRequests,
			FailureRatio: g.RestBreaker.FailureRatio,
		}
	}
	return coregovernance.NormalizeProductionDefaultsConfig(conf)
}

// BootstrapConfig converts ServiceConf into the lower-level Bootstrap config.
func (c ServiceConf) BootstrapConfig(fallbackName string) Config {
	c = c.WithDefaults(fallbackName)
	health := c.Health
	if len(health.LivenessChecks) == 0 {
		health.LivenessChecks = map[string]HealthCheck{
			"self": func(context.Context) error { return nil },
		}
	}
	health = normalizeHealthConfig(health)
	return Config{
		ServiceName: c.Name,
		Log:         c.Log,
		Trace:       c.Trace,
		Profile:     c.Profile,
		Metrics:     c.Metrics,
		Health:      health,
		BuildInfo:   c.BuildInfo,
	}
}

// RunOptions converts ServiceConf lifecycle timeouts into app.Run options.
func (c ServiceConf) RunOptions() []Option {
	c = c.WithDefaults(c.Name)
	return []Option{
		WithStartupTimeout(c.StartupTimeout),
		WithShutdownTimeout(c.ShutdownTimeout),
	}
}

// ServiceConfig returns the legacy lightweight lifecycle config for existing
// helpers that still accept ServiceConfig.
func (c ServiceConf) ServiceConfig() ServiceConfig {
	c = c.WithDefaults(c.Name)
	return ServiceConfig{
		Name:            c.Name,
		Mode:            c.Mode,
		StartupTimeout:  c.StartupTimeout,
		ShutdownTimeout: c.ShutdownTimeout,
	}
}

// RESTConfig merges ServiceConf defaults into a REST server config.
func (c ServiceConf) RESTConfig(base rest.Config) rest.Config {
	c = c.WithDefaults(base.Name)
	if base.Name == "" {
		base.Name = c.Name
	}
	if !c.Governance.Disabled {
		if base.Timeout <= 0 {
			base.Timeout = c.Governance.Timeout
		}
		if base.Middlewares.TimeoutConfig.Duration <= 0 {
			base.Middlewares.TimeoutConfig.Duration = c.Governance.Timeout
		}
		if base.Middlewares.TimeoutConfig.ReadHeaderTimeout <= 0 {
			base.Middlewares.TimeoutConfig.ReadHeaderTimeout = c.Governance.ReadHeaderTimeout
		}
		base.Middlewares.Recover = true
		base.Middlewares.Trace = true
		base.Middlewares.Log = true
		base.Middlewares.Timeout = true
		base.Middlewares.Metrics = true
		base.Middlewares.Health = true
		base.Middlewares.RequestID = true
		if c.Governance.Breaker {
			base.Middlewares.Breaker = true
			if base.Middlewares.BreakerConfig == (rest.BreakerConfig{}) {
				base.Middlewares.BreakerConfig = c.Governance.RestBreaker
			}
		}
		if c.Governance.RateLimit.Rate > 0 {
			base.Middlewares.RateLimit = true
			if base.Middlewares.RateLimitConfig.Rate <= 0 {
				base.Middlewares.RateLimitConfig = rest.RateLimitConfig{Rate: c.Governance.RateLimit.Rate, Burst: c.Governance.RateLimit.Burst}
			}
		}
		if c.Governance.MaxConcurrency > 0 {
			base.Middlewares.MaxConcurrency = true
			if base.Middlewares.MaxConcurrencyConfig.Limit <= 0 {
				base.Middlewares.MaxConcurrencyConfig.Limit = c.Governance.MaxConcurrency
			}
		}
		if c.Governance.AdaptiveLimit {
			base.Middlewares.AdaptiveRateLimit = true
		}
	}
	return base
}

// RPCGovernanceConfig converts ServiceConf into the shared RPC governance
// suite config used by both RPC clients and servers.
func (c ServiceConf) RPCGovernanceConfig() rpc.GovernanceConfig {
	c = c.WithDefaults(c.Name)
	if c.Governance.Disabled {
		return rpc.GovernanceConfig{}
	}
	conf := rpc.DefaultGovernanceConfig(c.Governance.Timeout)
	conf.TimeoutConfig = c.Governance.RPCTimeout
	conf.Breaker = c.Governance.Breaker
	conf.MaxConcurrency = c.Governance.MaxConcurrency
	conf.AdaptiveLimit = c.Governance.AdaptiveLimit
	return conf
}

// RPCSuite returns the RPC governance suite for this config.
// When governance is disabled, returns BasicSuite.
func (c ServiceConf) RPCSuite() rpc.Suite {
	c = c.WithDefaults(c.Name)
	if c.Governance.Disabled {
		return rpc.BasicSuite{}
	}
	return rpc.GovernanceSuite(c.Name, c.RPCGovernanceConfig())
}

// RPCServerOptions converts ServiceConf governance into a slice of
// rpc.ServerOption. When governance is disabled, returns an empty slice.
func (c ServiceConf) RPCServerOptions() []rpc.ServerOption {
	c = c.WithDefaults(c.Name)
	opts := make([]rpc.ServerOption, 0, 2)
	if !c.Governance.Disabled {
		opts = append(opts,
			rpc.WithServerReadHeaderTimeout(c.Governance.ReadHeaderTimeout),
			rpc.WithServerSuite(c.RPCSuite()),
		)
	}
	return opts
}

// RPCClientOptions converts ServiceConf governance into a slice of
// rpc.ClientOption. When governance is disabled, returns an empty slice.
func (c ServiceConf) RPCClientOptions() []rpc.ClientOption {
	c = c.WithDefaults(c.Name)
	opts := make([]rpc.ClientOption, 0, 4)
	if !c.Governance.Disabled {
		opts = append(opts,
			rpc.WithTimeout(c.Governance.Timeout),
			rpc.WithTransportConfig(c.Governance.rpcTransportConfig()),
			rpc.WithClientSuite(c.RPCSuite()),
		)
	}
	return opts
}

func (g ServiceGovernance) rpcTransportConfig() rpc.TransportConfig {
	if rpc.IsZeroTransportConfig(g.RPCTransport) {
		return rpc.DefaultTransportConfig()
	}
	return g.RPCTransport
}

// BootstrapServiceWithRuntime wires a ServiceConf directly.
func BootstrapServiceWithRuntime(ctx context.Context, conf ServiceConf) (*coreproc.Shutdown, *BootstrapRuntime, error) {
	return BootstrapWithRuntime(ctx, conf.BootstrapConfig(conf.Name))
}

// BootstrapService is the ServiceConf variant of Bootstrap.
func BootstrapService(ctx context.Context, conf ServiceConf) (*coreproc.Shutdown, error) {
	shutdown, _, err := BootstrapServiceWithRuntime(ctx, conf)
	return shutdown, err
}

// RunService combines ServiceConf bootstrap, app.Run and shutdown.
func RunService(ctx context.Context, conf ServiceConf, servers ...Server) error {
	if len(servers) == 0 {
		return RunBootstrap(ctx, conf.BootstrapConfig(conf.Name))
	}
	shutdown, err := BootstrapService(ctx, conf)
	if err != nil {
		return err
	}
	defer func() { _ = shutdown.Shutdown(shutdownContext(ctx)) }()
	return Run(ctx, servers, conf.RunOptions()...)
}
