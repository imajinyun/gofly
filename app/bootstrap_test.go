package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofly/gofly/core/observability/metrics"
	metricsotel "github.com/gofly/gofly/core/observability/metrics/otel"
	coretrace "github.com/gofly/gofly/core/observability/trace"
	traceexporter "github.com/gofly/gofly/core/observability/trace/exporter"
)

func TestBootstrapReturnsNonNilShutdown(t *testing.T) {
	ctx := context.Background()
	shutdown, err := Bootstrap(ctx, Config{ServiceName: "test-app"})
	if err != nil {
		t.Fatalf("bootstrap err=%v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil Shutdown")
	}
	// Shutdown is safe to call with everything disabled.
	if err := shutdown.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown err=%v", err)
	}
}

func TestBootstrapDefaultsServiceName(t *testing.T) {
	shutdown, err := Bootstrap(context.Background(), Config{})
	if err != nil {
		t.Fatalf("bootstrap err=%v", err)
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
	if shutdown == nil {
		t.Fatal("expected non-nil Shutdown even with empty config")
	}
}

func TestBootstrapWiresTraceAgent(t *testing.T) {
	shutdown, err := Bootstrap(context.Background(), Config{
		ServiceName: "test-trace",
		Trace:       coretrace.AgentConfig{}, // zero value = disabled by default
	})
	if err != nil {
		t.Fatalf("bootstrap err=%v", err)
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
}

func TestBootstrapTraceOTLPValidation(t *testing.T) {
	_, err := Bootstrap(context.Background(), Config{
		ServiceName: "trace-otlp",
		Trace: coretrace.AgentConfig{
			Enabled: true,
			OTLP: traceexporter.OTLPConfig{
				Endpoint: "localhost:4317",
				Protocol: "bogus",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap: trace") || !strings.Contains(err.Error(), "trace OTLP") {
		t.Fatalf("Bootstrap trace otlp err = %v, want wrapped trace OTLP error", err)
	}
}

func TestBootstrapTraceOTLPDisabledWhenTraceDisabled(t *testing.T) {
	shutdown, err := Bootstrap(context.Background(), Config{
		ServiceName: "trace-disabled",
		Trace: coretrace.AgentConfig{
			Enabled: false,
			OTLP: traceexporter.OTLPConfig{
				Endpoint: "localhost:4317",
				Protocol: "bogus",
			},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap disabled trace otlp err = %v", err)
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
}

func TestBootstrapMetricsOTLPValidation(t *testing.T) {
	_, err := Bootstrap(context.Background(), Config{
		ServiceName: "metrics-otlp",
		Metrics: MetricsConfig{
			Enabled: true,
			OTLP: metricsotel.Config{
				Endpoint: "localhost:4317",
				Protocol: "bogus",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap: metrics otlp") {
		t.Fatalf("Bootstrap metrics otlp err = %v, want wrapped metrics error", err)
	}
}

func TestBootstrapMetricsOTLPDisabledWhenMetricsDisabled(t *testing.T) {
	shutdown, err := Bootstrap(context.Background(), Config{
		ServiceName: "metrics-disabled",
		Metrics: MetricsConfig{
			Enabled: false,
			OTLP: metricsotel.Config{
				Endpoint: "localhost:4317",
				Protocol: "bogus",
			},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap disabled metrics otlp err = %v", err)
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
}

func TestBootstrapWithRuntimeExposesSnapshotAndChecks(t *testing.T) {
	shutdown, runtimeState, err := BootstrapWithRuntime(context.Background(), Config{
		ServiceName: "runtime-test",
		Health: HealthConfig{
			LivenessChecks: map[string]HealthCheck{
				"self": func(context.Context) error { return nil },
			},
			ReadinessChecks: map[string]HealthCheck{
				"dependency": func(context.Context) error { return errors.New("downstream unavailable") },
			},
		},
	})
	if err != nil {
		t.Fatalf("BootstrapWithRuntime err = %v", err)
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()

	snapshot := runtimeState.Snapshot(context.Background())
	if snapshot.ServiceName != "runtime-test" || snapshot.MaxProcs.Applied == 0 || snapshot.Health["healthz"].Status != "ok" {
		t.Fatalf("runtime snapshot = %#v, want service/maxprocs/health", snapshot)
	}
	if ready := runtimeState.Check(context.Background(), "readyz"); ready.Status != "error" || ready.Checks["dependency"].Error == "" {
		t.Fatalf("ready check = %#v, want dependency error", ready)
	}
}

func TestBootstrapUsesExplicitRuntimeEnvironmentWithoutGlobalLoggerMutation(t *testing.T) {
	previous := slog.Default()
	var buf bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelWarn)
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level}))
	registry := metrics.NewRegistry()

	shutdown, runtimeState, err := BootstrapWithRuntime(context.Background(), Config{
		ServiceName: "explicit-runtime",
		Metrics:     MetricsConfig{Enabled: true},
		Environment: &RuntimeEnvironment{
			Logger:          logger,
			LogLevel:        level,
			MetricsRegistry: registry,
		},
	})
	if err != nil {
		t.Fatalf("BootstrapWithRuntime explicit environment err = %v", err)
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()

	if slog.Default() != previous {
		t.Fatal("BootstrapWithRuntime with explicit environment mutated global slog default")
	}
	if runtimeState.Logger() != logger || runtimeState.LogLevel() != level || runtimeState.MetricsRegistry() != registry {
		t.Fatalf("runtime dependencies = (%p, %p, %p), want injected (%p, %p, %p)", runtimeState.Logger(), runtimeState.LogLevel(), runtimeState.MetricsRegistry(), logger, level, registry)
	}
}

func TestBootstrapRuntimeEnvironmentControlsGlobalLoggerInstallation(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })

	var buf bytes.Buffer
	env := &RuntimeEnvironment{}
	shutdown, runtimeState, err := BootstrapWithRuntime(context.Background(), Config{
		ServiceName: "generated-explicit-runtime",
		Environment: env,
		Log:         LogConfig{Format: "json", Level: "debug"},
	})
	if err != nil {
		t.Fatalf("BootstrapWithRuntime no global err = %v", err)
	}
	if err := shutdown.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown err = %v", err)
	}
	if slog.Default() != previous {
		t.Fatal("BootstrapWithRuntime with non-nil environment and InstallGlobalLogger=false mutated global logger")
	}
	if runtimeState.Logger() == nil || runtimeState.LogLevel() == nil || env.LogLevel == nil {
		t.Fatalf("runtime/env log dependencies were not populated: runtime=%p level=%p envLevel=%p", runtimeState.Logger(), runtimeState.LogLevel(), env.LogLevel)
	}

	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	shutdown, _, err = BootstrapWithRuntime(context.Background(), Config{
		ServiceName: "global-runtime",
		Environment: &RuntimeEnvironment{
			Logger:              logger,
			InstallGlobalLogger: true,
		},
	})
	if err != nil {
		t.Fatalf("BootstrapWithRuntime global err = %v", err)
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()
	if slog.Default() != logger {
		t.Fatal("BootstrapWithRuntime with InstallGlobalLogger=true did not install injected logger")
	}
}

func TestBootstrapProfileMountsRuntimeHealthHandlers(t *testing.T) {
	_, runtimeState, err := BootstrapWithRuntime(context.Background(), Config{ServiceName: "profile-runtime"})
	if err != nil {
		t.Fatalf("BootstrapWithRuntime err = %v", err)
	}

	runtimeRec := httptest.NewRecorder()
	runtimeState.runtimeHandler().ServeHTTP(runtimeRec, httptest.NewRequest(http.MethodGet, "/debug/runtime.json", nil))
	if runtimeRec.Code != http.StatusOK || !strings.Contains(runtimeRec.Body.String(), "profile-runtime") {
		t.Fatalf("runtime handler status = %d body %q, want service snapshot", runtimeRec.Code, runtimeRec.Body.String())
	}

	healthRec := httptest.NewRecorder()
	runtimeState.checkHandler("healthz").ServeHTTP(healthRec, httptest.NewRequest(http.MethodGet, "/debug/healthz", nil))
	if healthRec.Code != http.StatusOK || !strings.Contains(healthRec.Body.String(), `"status":"ok"`) {
		t.Fatalf("health handler status = %d body %q, want ok report", healthRec.Code, healthRec.Body.String())
	}
}

func TestBootstrapRejectsFailingStartupCheck(t *testing.T) {
	shutdown, _, err := BootstrapWithRuntime(context.Background(), Config{
		ServiceName: "startup-fail",
		Health: HealthConfig{StartupChecks: map[string]HealthCheck{
			"migrate": func(context.Context) error { return errors.New("not ready") },
		}},
	})
	if shutdown != nil {
		defer func() { _ = shutdown.Shutdown(context.Background()) }()
	}
	if err == nil || !strings.Contains(err.Error(), "startup checks failed") {
		t.Fatalf("BootstrapWithRuntime startup err = %v, want startup checks failed", err)
	}
}

type testServer struct {
	started     bool
	shutdown    bool
	block       chan struct{}
	startSignal chan struct{}
	shutdownErr error
}

func (s *testServer) Start() error {
	s.started = true
	if s.startSignal != nil {
		close(s.startSignal)
	}
	if s.block != nil {
		<-s.block
	}
	return nil
}

func (s *testServer) Shutdown(ctx context.Context) error {
	s.shutdown = true
	if s.block != nil {
		close(s.block)
	}
	return s.shutdownErr
}

func TestRunBootstrapRequiresServers(t *testing.T) {
	if err := RunBootstrap(context.Background(), Config{ServiceName: "empty"}); err == nil {
		t.Fatal("expected error when no servers provided")
	}
}

func TestRunBootstrapStartsAndStopsServers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	ts := &testServer{startSignal: started}
	done := make(chan error, 1)
	go func() {
		done <- RunBootstrap(ctx, Config{ServiceName: "run-test"}, ts)
	}()

	<-started
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("runbootstrap err=%v", err)
	}
	if !ts.shutdown {
		t.Fatal("server was not shut down")
	}
}

func TestRunBootstrapReturnsShutdownError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	shutdownErr := errors.New("shutdown failed")
	err := RunBootstrap(ctx, Config{ServiceName: "run-shutdown-error"}, &testServer{shutdownErr: shutdownErr})
	if !errors.Is(err, shutdownErr) {
		t.Fatalf("RunBootstrap error = %v, want shutdown error", err)
	}
}
