package app

import (
	"context"
	"testing"
	"time"

	"github.com/imajinyun/gofly/rest"
)

func TestServiceConfWithDefaultsWiresRuntimePrimitives(t *testing.T) {
	conf := DefaultServiceConf("orders")
	if conf.Name != "orders" || conf.Environment != "development" {
		t.Fatalf("service defaults identity = %+v", conf)
	}
	if conf.Log.Level != "info" || conf.Log.Format != "json" || conf.Log.Trace {
		t.Fatalf("service log defaults = %+v", conf.Log)
	}
	if conf.Metrics.Enabled || conf.Trace.Enabled || conf.Trace.ServiceName != "orders" || conf.Trace.SampleRatio != 1 {
		t.Fatalf("service observability defaults metrics=%+v trace=%+v", conf.Metrics, conf.Trace)
	}
	if conf.Profile.Addr != "127.0.0.1:6060" || conf.Profile.PathPrefix != "/debug/pprof" || conf.Profile.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("service profile defaults = %+v", conf.Profile)
	}
	if conf.Health.Timeout != time.Second || conf.StartupTimeout != 5*time.Second || conf.ShutdownTimeout != 10*time.Second {
		t.Fatalf("service lifecycle defaults = %+v", conf)
	}
}

func TestServiceConfEnvironmentConstructors(t *testing.T) {
	dev := DevelopmentServiceConf("orders")
	if dev.Environment != "development" || dev.Mode != "development" || dev.Log.Level != "debug" || dev.Log.Format != "console" {
		t.Fatalf("development config identity/logging = %+v", dev)
	}
	if !dev.Metrics.Enabled || !dev.Trace.Enabled || dev.Trace.SampleRatio != 1 || !dev.Profile.Enabled || dev.Governance.Disabled {
		t.Fatalf("development observability/governance = %+v", dev)
	}

	prod := ProductionServiceConf("orders")
	if prod.Environment != "production" || prod.Mode != "production" || prod.Log.Level != "info" || prod.Log.Format != "json" || !prod.Log.Trace {
		t.Fatalf("production config identity/logging = %+v", prod)
	}
	if !prod.Metrics.Enabled || !prod.Trace.Enabled || prod.Trace.SampleRatio != 0.1 || prod.Profile.Enabled || prod.Governance.Disabled {
		t.Fatalf("production observability/governance = %+v", prod)
	}
	if prod.ProductionGovernancePlugin() == nil {
		t.Fatal("production config should create governance plugin")
	}

	testConf := TestServiceConf("orders")
	if testConf.Environment != "test" || testConf.Mode != "test" || testConf.Log.Level != "warn" || testConf.Log.Format != "console" {
		t.Fatalf("test config identity/logging = %+v", testConf)
	}
	if testConf.Metrics.Enabled || testConf.Trace.Enabled || testConf.Profile.Enabled || !testConf.Governance.Disabled {
		t.Fatalf("test observability/governance = %+v", testConf)
	}
	if len(testConf.RPCServerOptions()) != 0 || testConf.ProductionGovernancePlugin() != nil {
		t.Fatal("test config should disable rpc and production governance side effects")
	}
}

func TestServiceConfBuildsBootstrapConfigAndRunOptions(t *testing.T) {
	conf := ServiceConf{
		Name:            "billing",
		StartupTimeout:  2 * time.Second,
		ShutdownTimeout: 3 * time.Second,
	}
	bootstrap := conf.BootstrapConfig("")
	if bootstrap.ServiceName != "billing" || bootstrap.Trace.Enabled || bootstrap.Metrics.Enabled {
		t.Fatalf("bootstrap config = %+v", bootstrap)
	}
	if report := (&BootstrapRuntime{health: normalizeHealthConfig(bootstrap.Health)}).Check(context.Background(), "healthz"); report.Status != "ok" || len(report.Checks) != 1 {
		t.Fatalf("bootstrap self health report = %+v", report)
	}
	var opts Options
	for _, opt := range conf.RunOptions() {
		opt(&opts)
	}
	if opts.StartupTimeout != 2*time.Second || opts.ShutdownTimeout != 3*time.Second {
		t.Fatalf("run options = %+v", opts)
	}
}

func TestServiceConfBootstrapConfigClonesHealthChecks(t *testing.T) {
	checks := map[string]HealthCheck{"custom": func(context.Context) error { return nil }}
	conf := ServiceConf{Name: "billing", Health: HealthConfig{LivenessChecks: checks}}
	bootstrap := conf.BootstrapConfig("")
	delete(bootstrap.Health.LivenessChecks, "custom")
	if _, ok := checks["custom"]; !ok {
		t.Fatal("BootstrapConfig exposed mutable health check map")
	}
}

func TestServiceConfNestedShutdownNormalizesLifecycle(t *testing.T) {
	conf := ServiceConf{
		Name: "billing",
		Shutdown: ShutdownConfig{
			StartupTimeout: 4 * time.Second,
			Timeout:        6 * time.Second,
		},
	}.WithDefaults("")
	if conf.StartupTimeout != 4*time.Second || conf.ShutdownTimeout != 6*time.Second {
		t.Fatalf("normalized top-level lifecycle = startup %s shutdown %s", conf.StartupTimeout, conf.ShutdownTimeout)
	}
	if conf.Shutdown.StartupTimeout != 4*time.Second || conf.Shutdown.Timeout != 6*time.Second {
		t.Fatalf("normalized nested shutdown = %+v", conf.Shutdown)
	}
	var opts Options
	for _, opt := range conf.RunOptions() {
		opt(&opts)
	}
	if opts.StartupTimeout != 4*time.Second || opts.ShutdownTimeout != 6*time.Second {
		t.Fatalf("run options from nested shutdown = %+v", opts)
	}
}

func TestServiceConfWithDefaultsPreservesDisabledObservability(t *testing.T) {
	conf := ServiceConf{Name: "orders"}.WithDefaults("")
	if conf.Log.Trace || conf.Metrics.Enabled || conf.Trace.Enabled || conf.Profile.Enabled {
		t.Fatalf("disabled observability should be preserved: %+v", conf)
	}
}

func TestServiceConfRESTConfigWiresDefaultGovernance(t *testing.T) {
	conf := ServiceConf{
		Name: "orders",
		Governance: ServiceGovernance{
			RateLimit:      ServiceRateLimit{Rate: 100, Burst: 200},
			MaxConcurrency: 32,
			AdaptiveLimit:  true,
		},
	}
	restConf := conf.RESTConfig(rest.Config{})
	if restConf.Name != "orders" || restConf.Timeout != 3*time.Second {
		t.Fatalf("rest identity/timeouts = %+v", restConf)
	}
	mw := restConf.Middlewares
	if !mw.Recover || !mw.Trace || !mw.Log || !mw.Timeout || !mw.Metrics || !mw.Health || !mw.RequestID || !mw.Breaker {
		t.Fatalf("rest default middleware flags = %+v", mw)
	}
	if !mw.RateLimit || mw.RateLimitConfig.Rate != 100 || mw.RateLimitConfig.Burst != 200 {
		t.Fatalf("rest rate limit = %+v", mw.RateLimitConfig)
	}
	if !mw.MaxConcurrency || mw.MaxConcurrencyConfig.Limit != 32 || !mw.AdaptiveRateLimit {
		t.Fatalf("rest concurrency/adaptive = %+v", mw)
	}
}

func TestServiceConfRPCOptionsWireDefaultGovernance(t *testing.T) {
	conf := ServiceConf{Name: "orders", Governance: ServiceGovernance{MaxConcurrency: 16}}.WithDefaults("")
	gov := conf.RPCGovernanceConfig()
	if !gov.Recover || !gov.RequestID || !gov.Trace || !gov.Log || !gov.Metrics || !gov.Breaker {
		t.Fatalf("rpc governance flags = %+v", gov)
	}
	if gov.Timeout != 3*time.Second || gov.MaxConcurrency != 16 {
		t.Fatalf("rpc governance timeout/concurrency = %+v", gov)
	}
	if len(conf.RPCServerOptions()) == 0 || len(conf.RPCClientOptions()) == 0 || conf.RPCSuite() == nil {
		t.Fatal("rpc options/suite should be populated by default governance")
	}
}

func TestServiceConfProductionGovernanceConfigUsesSharedDefaults(t *testing.T) {
	conf := ServiceConf{
		Name: "orders",
		Governance: ServiceGovernance{
			Timeout:        4 * time.Second,
			RateLimit:      ServiceRateLimit{Rate: 90, Burst: 120},
			MaxConcurrency: 32,
		},
	}.WithDefaults("")
	gov := conf.ProductionGovernanceConfig()
	if gov.Service != "orders" || gov.RESTTimeout != 4*time.Second || gov.RPCTimeout != 4*time.Second || gov.MQTimeout != 4*time.Second {
		t.Fatalf("production governance timeouts = %+v", gov)
	}
	if gov.GatewayTimeout != 5*time.Second || gov.RetryAttempts != 2 || gov.RetryBackoff != 100*time.Millisecond {
		t.Fatalf("production governance shared defaults = %+v", gov)
	}
	if gov.RateLimit != 90 || gov.RateBurst != 120 || gov.ConcurrencyLimit != 32 || !gov.Breaker.Enabled {
		t.Fatalf("production governance policies = %+v", gov)
	}
	if conf.ProductionGovernancePlugin() == nil {
		t.Fatal("production governance plugin should be created from service config")
	}
}

func TestServiceConfGovernanceCanBeDisabled(t *testing.T) {
	conf := ServiceConf{Name: "orders", Governance: ServiceGovernance{Disabled: true}}.WithDefaults("")
	restConf := conf.RESTConfig(rest.Config{})
	if restConf.Middlewares.Breaker || restConf.Middlewares.Timeout {
		t.Fatalf("disabled governance should not force rest middleware: %+v", restConf.Middlewares)
	}
	if len(conf.RPCServerOptions()) != 0 || len(conf.RPCClientOptions()) != 0 {
		t.Fatal("disabled governance should not produce rpc governance options")
	}
	if conf.ProductionGovernancePlugin() != nil {
		t.Fatal("disabled governance should not produce production defaults plugin")
	}
}

func TestRunServiceStartsAndStopsServers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	ts := &testServer{startSignal: started}
	done := make(chan error, 1)
	go func() {
		done <- RunService(ctx, ServiceConf{Name: "run-service", Metrics: MetricsConfig{Enabled: false}}, ts)
	}()

	<-started
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("RunService err = %v", err)
	}
	if !ts.shutdown {
		t.Fatal("server was not shut down")
	}
}
