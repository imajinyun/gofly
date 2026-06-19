// Package app provides the gofly application runtime lifecycle management.
// It coordinates server startup, graceful shutdown, hooks, and production
// configuration defaults.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/gofly/gofly/core/observability/metrics"
	metricsotel "github.com/gofly/gofly/core/observability/metrics/otel"
	coretrace "github.com/gofly/gofly/core/observability/trace"
	coreproc "github.com/gofly/gofly/core/proc"
)

// Config describes the wiring Bootstrap performs. All fields are optional;
// leaving a struct at its zero value disables that subsystem.
type Config struct {
	ServiceName string
	Log         LogConfig
	Trace       coretrace.AgentConfig
	Profile     ProfileConfig
	Metrics     MetricsConfig
	Health      HealthConfig
	Environment *RuntimeEnvironment
	BuildInfo   coreproc.BuildInfo // set automatically by Bootstrap; callers may override
}

// RuntimeEnvironment lets applications inject runtime dependencies explicitly
// instead of letting Bootstrap mutate process globals. Passing nil preserves the
// historical Bootstrap behavior for compatibility.
type RuntimeEnvironment struct {
	Logger              *slog.Logger
	LogLevel            *slog.LevelVar
	MetricsRegistry     *metrics.Registry
	InstallGlobalLogger bool
}

// MetricsConfig selects which metrics registry to use.
type MetricsConfig struct {
	Enabled  bool
	Registry *metrics.Registry // if nil and Enabled is true, metrics.Default is used
	OTLP     metricsotel.Config
}

// HealthCheck is a named bootstrap-level health probe. It is deliberately
// small so applications can reuse existing dependency checks, for example
// SQLStore.Ping or Gateway.HealthCheck.
type HealthCheck func(context.Context) error

// HealthConfig registers bootstrap-level liveness, readiness and startup
// checks. Checks are exposed on the profile server when profiling is enabled
// and are also available through BootstrapRuntime for tests and custom admin
// endpoints.
type HealthConfig struct {
	Timeout         time.Duration          `json:"timeout,omitempty"`
	LivenessChecks  map[string]HealthCheck `json:"-"`
	ReadinessChecks map[string]HealthCheck `json:"-"`
	StartupChecks   map[string]HealthCheck `json:"-"`
}

// CheckResult is the result of a single bootstrap health check.
type CheckResult struct {
	Status   string        `json:"status"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
}

// CheckReport is the aggregate report returned by bootstrap health endpoints.
type CheckReport struct {
	Status string                 `json:"status"`
	Checks map[string]CheckResult `json:"checks"`
}

// RuntimeSnapshot is a testable startup/runtime summary emitted by Bootstrap.
type RuntimeSnapshot struct {
	ServiceName        string                  `json:"serviceName"`
	BuildInfo          coreproc.BuildInfo      `json:"buildInfo"`
	MaxProcs           coreproc.MaxProcsResult `json:"maxProcs"`
	StartedAt          time.Time               `json:"startedAt"`
	Uptime             time.Duration           `json:"uptime"`
	GoVersion          string                  `json:"goVersion"`
	Goroutines         int                     `json:"goroutines"`
	TraceEnabled       bool                    `json:"traceEnabled"`
	TraceOTLPEnabled   bool                    `json:"traceOtlpEnabled"`
	ProfileEnabled     bool                    `json:"profileEnabled"`
	MetricsEnabled     bool                    `json:"metricsEnabled"`
	MetricsOTLPEnabled bool                    `json:"metricsOtlpEnabled"`
	Health             map[string]CheckReport  `json:"health,omitempty"`
	Metrics            *metrics.Snapshot       `json:"metrics,omitempty"`
}

// BootstrapRuntime holds the runtime state wired by Bootstrap. It is returned
// by BootstrapWithRuntime for tests and for applications that want to mount
// their own diagnostics endpoint instead of using the profile server.
type BootstrapRuntime struct {
	serviceName        string
	buildInfo          coreproc.BuildInfo
	maxProcs           coreproc.MaxProcsResult
	startedAt          time.Time
	logger             *slog.Logger
	logLevel           *slog.LevelVar
	traceEnabled       bool
	traceOTLPEnabled   bool
	profileEnabled     bool
	metricsEnabled     bool
	metricsOTLPEnabled bool
	registry           *metrics.Registry
	health             HealthConfig
}

// Bootstrap wires up the standard gofly runtime primitives in a single call:
//
//  1. Container-aware GOMAXPROCS tuning via core/proc.SetMaxProcs
//  2. Structured slog logger (app/log)
//  3. OpenTelemetry tracer (core/trace.Agent)
//  4. Optional pprof HTTP server (app/profile)
//  5. Optional metrics registry (core/metrics)
//
// The returned Shutdown must be invoked at exit to release resources.
// Bootstrap never returns a nil Shutdown even on partial success — callers
// should always invoke Shutdown.
func Bootstrap(ctx context.Context, conf Config) (*coreproc.Shutdown, error) {
	shutdown, _, err := BootstrapWithRuntime(ctx, conf)
	return shutdown, err
}

// BootstrapWithRuntime is Bootstrap plus a testable runtime snapshot. Most
// applications should call Bootstrap; tests and admin servers can use the
// returned runtime to inspect health checks and startup state without parsing
// logs.
func BootstrapWithRuntime(ctx context.Context, conf Config) (*coreproc.Shutdown, *BootstrapRuntime, error) {
	if conf.ServiceName == "" {
		conf.ServiceName = "gofly-app"
	}
	if conf.BuildInfo.Version == "" {
		conf.BuildInfo = coreproc.ReadBuildInfo()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	startedAt := time.Now()

	// 1. CPU quota
	maxProcs := coreproc.SetMaxProcs()

	// 2. Logger (required — default settings produce a json stdout logger).
	// Without an explicit environment we preserve the legacy process-wide default
	// logger installation; with an environment the logger stays explicit unless
	// InstallGlobalLogger is requested.
	logger, levelVar, err := bootstrapLogger(conf)
	if err != nil {
		return coreproc.NewShutdown(), nil, fmt.Errorf("bootstrap: logger: %w", err)
	}

	shutdown := coreproc.NewShutdown()
	runtimeState := &BootstrapRuntime{
		serviceName:      conf.ServiceName,
		buildInfo:        conf.BuildInfo,
		maxProcs:         maxProcs,
		startedAt:        startedAt,
		logger:           logger,
		logLevel:         levelVar,
		traceEnabled:     conf.Trace.Enabled,
		traceOTLPEnabled: conf.Trace.Enabled && conf.Trace.OTLP.Endpoint != "",
		profileEnabled:   conf.Profile.Enabled,
		metricsEnabled:   conf.Metrics.Enabled,
		health:           normalizeHealthConfig(conf.Health),
	}

	// 3. Trace agent
	traceConf := conf.Trace
	if traceConf.Enabled && traceConf.ServiceName == "" {
		traceConf.ServiceName = conf.ServiceName
	}
	agent, err := coretrace.StartAgent(ctx, traceConf)
	if err != nil {
		return shutdown, runtimeState, fmt.Errorf("bootstrap: trace: %w", err)
	}
	shutdown.Add(func(c context.Context) error { return agent.Shutdown(c) })

	// 4. pprof profile server (best-effort)
	if conf.Profile.Enabled {
		ps := NewProfileServer(conf.Profile,
			WithProfileHandler("/debug/loglevel", LevelHandler(levelVar)),
			WithProfileHandler("/debug/runtime.json", runtimeState.runtimeHandler()),
			WithProfileHandler("/debug/healthz", runtimeState.checkHandler("healthz")),
			WithProfileHandler("/debug/readyz", runtimeState.checkHandler("readyz")),
			WithProfileHandler("/debug/startupz", runtimeState.checkHandler("startupz")),
		)
		go func() {
			if err := ps.Start(); err != nil {
				slog.Error("profile server start failed", "error", err)
			}
		}()
		shutdown.Add(func(c context.Context) error { return ps.Shutdown(c) })
	}

	// 5. Metrics registry and optional OTLP exporter.
	registry := bootstrapMetricsRegistry(conf)
	runtimeState.registry = registry
	runtimeState.metricsOTLPEnabled = conf.Metrics.Enabled && conf.Metrics.OTLP.Endpoint != ""
	if conf.Metrics.Enabled && conf.Metrics.OTLP.Endpoint != "" {
		metricConf := conf.Metrics.OTLP
		metricConf.Registry = registry
		if metricConf.ServiceName == "" {
			metricConf.ServiceName = conf.ServiceName
		}
		metricExporter, err := metricsotel.Start(ctx, metricConf)
		if err != nil {
			return shutdown, runtimeState, fmt.Errorf("bootstrap: metrics otlp: %w", err)
		}
		shutdown.Add(func(c context.Context) error { return metricExporter.Close(c) })
	}

	startupReport := runtimeState.Check(ctx, "startupz")
	if startupReport.Status != "ok" {
		return shutdown, runtimeState, fmt.Errorf("bootstrap: startup checks failed")
	}

	// Log the bootstrap summary — a single line that remains searchable
	snapshot := runtimeState.Snapshot(ctx)
	logger.Info(
		"gofly bootstrap complete",
		"service", snapshot.ServiceName,
		"version", snapshot.BuildInfo.Version,
		"commit", snapshot.BuildInfo.Commit,
		"built_at", snapshot.BuildInfo.BuiltAt,
		"goos", snapshot.BuildInfo.GoOS,
		"goarch", snapshot.BuildInfo.GoArch,
		"go_version", snapshot.GoVersion,
		"gomaxprocs", snapshot.MaxProcs.Applied,
		"trace_enabled", snapshot.TraceEnabled,
		"trace_otlp_enabled", snapshot.TraceOTLPEnabled,
		"profile_enabled", snapshot.ProfileEnabled,
		"metrics_enabled", snapshot.MetricsEnabled,
		"metrics_otlp_enabled", snapshot.MetricsOTLPEnabled,
		"startup_status", startupReport.Status,
	)

	return shutdown, runtimeState, nil
}

func bootstrapLogger(conf Config) (*slog.Logger, *slog.LevelVar, error) {
	if conf.Environment != nil && conf.Environment.Logger != nil {
		if conf.Environment.InstallGlobalLogger {
			slog.SetDefault(conf.Environment.Logger)
		}
		return conf.Environment.Logger, conf.Environment.LogLevel, nil
	}
	logger, levelVar, err := NewLeveledLogger(nil, conf.Log)
	if err != nil {
		return nil, nil, err
	}
	if conf.Environment == nil || conf.Environment.InstallGlobalLogger {
		slog.SetDefault(logger)
	}
	if conf.Environment != nil && conf.Environment.LogLevel == nil {
		conf.Environment.LogLevel = levelVar
	}
	return logger, levelVar, nil
}

func bootstrapMetricsRegistry(conf Config) *metrics.Registry {
	if !conf.Metrics.Enabled {
		return nil
	}
	if conf.Metrics.Registry != nil {
		return conf.Metrics.Registry
	}
	if conf.Environment != nil && conf.Environment.MetricsRegistry != nil {
		return conf.Environment.MetricsRegistry
	}
	return metrics.Default
}

func normalizeHealthConfig(conf HealthConfig) HealthConfig {
	if conf.Timeout <= 0 {
		conf.Timeout = time.Second
	}
	conf.LivenessChecks = cloneChecks(conf.LivenessChecks)
	conf.ReadinessChecks = cloneChecks(conf.ReadinessChecks)
	conf.StartupChecks = cloneChecks(conf.StartupChecks)
	return conf
}

func cloneChecks(in map[string]HealthCheck) map[string]HealthCheck {
	out := make(map[string]HealthCheck, len(in))
	for name, check := range in {
		if name != "" && check != nil {
			out[name] = check
		}
	}
	return out
}

func (r *BootstrapRuntime) Snapshot(ctx context.Context) RuntimeSnapshot {
	if r == nil {
		return RuntimeSnapshot{}
	}
	snapshot := RuntimeSnapshot{
		ServiceName:        r.serviceName,
		BuildInfo:          r.buildInfo,
		MaxProcs:           r.maxProcs,
		StartedAt:          r.startedAt,
		Uptime:             time.Since(r.startedAt),
		GoVersion:          runtime.Version(),
		Goroutines:         runtime.NumGoroutine(),
		TraceEnabled:       r.traceEnabled,
		TraceOTLPEnabled:   r.traceOTLPEnabled,
		ProfileEnabled:     r.profileEnabled,
		MetricsEnabled:     r.metricsEnabled,
		MetricsOTLPEnabled: r.metricsOTLPEnabled,
		Health: map[string]CheckReport{
			"healthz":  r.Check(ctx, "healthz"),
			"readyz":   r.Check(ctx, "readyz"),
			"startupz": r.Check(ctx, "startupz"),
		},
	}
	if r.metricsEnabled && r.registry != nil {
		metricsSnapshot := r.registry.Snapshot()
		snapshot.Metrics = &metricsSnapshot
	}
	return snapshot
}

func (r *BootstrapRuntime) Logger() *slog.Logger {
	if r == nil {
		return nil
	}
	return r.logger
}

func (r *BootstrapRuntime) LogLevel() *slog.LevelVar {
	if r == nil {
		return nil
	}
	return r.logLevel
}

func (r *BootstrapRuntime) MetricsRegistry() *metrics.Registry {
	if r == nil {
		return nil
	}
	return r.registry
}

func (r *BootstrapRuntime) Check(ctx context.Context, probe string) CheckReport {
	checks := r.checks(probe)
	report := CheckReport{Status: "ok", Checks: make(map[string]CheckResult, len(checks))}
	for name, check := range checks {
		start := time.Now()
		checkCtx := ctx
		if checkCtx == nil {
			checkCtx = context.Background()
		}
		var cancel context.CancelFunc
		if r != nil && r.health.Timeout > 0 {
			checkCtx, cancel = context.WithTimeout(checkCtx, r.health.Timeout)
		}
		err := check(checkCtx)
		if cancel != nil {
			cancel()
		}
		result := CheckResult{Status: "ok", Duration: time.Since(start)}
		if err != nil {
			result.Status = "error"
			result.Error = err.Error()
			report.Status = "error"
		}
		report.Checks[name] = result
	}
	return report
}

func (r *BootstrapRuntime) checks(probe string) map[string]HealthCheck {
	if r == nil {
		return nil
	}
	switch probe {
	case "readyz", "ready":
		return r.health.ReadinessChecks
	case "startupz", "startup":
		return r.health.StartupChecks
	default:
		return r.health.LivenessChecks
	}
}

func (r *BootstrapRuntime) runtimeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(r.Snapshot(req.Context()))
	})
}

func (r *BootstrapRuntime) checkHandler(probe string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		report := r.Check(req.Context(), probe)
		w.Header().Set("Content-Type", "application/json")
		if report.Status != "ok" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(report)
	})
}

// RunBootstrap combines Bootstrap with app.Run so callers get a
// single-call "set up primitives + run servers + shut down" helper.
//
// Example:
//
//	err := app.RunBootstrap(ctx, app.Config{...}, servers...)
//
// It returns the first non-nil error encountered.
func RunBootstrap(ctx context.Context, conf Config, servers ...Server) (err error) {
	if len(servers) == 0 {
		return errors.New("bootstrap: no servers provided")
	}
	shutdown, err := Bootstrap(ctx, conf)
	if err != nil {
		return err
	}
	defer func() {
		if shutdownErr := shutdown.Shutdown(shutdownContext(ctx)); err == nil {
			err = shutdownErr
		}
	}()
	return Run(ctx, servers)
}

func shutdownContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
