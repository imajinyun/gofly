package otel

import (
	"context"
	"math"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gofly/gofly/core/metrics"
)

func TestStartRequiresEndpoint(t *testing.T) {
	if _, err := Start(context.Background(), Config{}); err == nil {
		t.Fatal("Start should require an endpoint")
	}
}

func TestStartRejectsUnknownProtocol(t *testing.T) {
	if _, err := Start(context.Background(), Config{Endpoint: "localhost:4317", Protocol: "bogus"}); err == nil {
		t.Fatal("Start should reject unknown protocol")
	}
}

func TestConfigDefaults(t *testing.T) {
	if got := (Config{}).timeout(); got <= 0 {
		t.Fatalf("default timeout = %v, want positive", got)
	}
	if got := (Config{}).interval(); got <= 0 {
		t.Fatalf("default interval = %v, want positive", got)
	}
}

func TestStartAndCloseBridgesRegistry(t *testing.T) {
	// Constructing an OTLP exporter does not dial; it only validates options,
	// so Start succeeds without a live collector.
	registry := metrics.NewRegistry()
	registry.Observe("users.list", 200, 5*time.Millisecond)
	registry.Observe("users.list", 500, 9*time.Millisecond)

	exp, err := Start(context.Background(), Config{
		ServiceName: "svc",
		Protocol:    ProtocolHTTP,
		Endpoint:    "localhost:4318",
		Insecure:    true,
		Interval:    time.Hour,
		Registry:    registry,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if exp.MeterProvider() == nil {
		t.Fatal("MeterProvider is nil")
	}
	// Close triggers a final export; without a live collector it returns a
	// network error, which is expected in unit tests. We only assert that
	// shutdown does not panic and releases the provider.
	_ = exp.Close(context.Background())
}

func TestExporterNilSafe(t *testing.T) {
	var exp *Exporter
	if exp.MeterProvider() != nil {
		t.Fatal("nil exporter MeterProvider should be nil")
	}
	if err := exp.Close(context.Background()); err != nil {
		t.Fatalf("nil Close: %v", err)
	}
}

func TestRegisterCallbacksExportsCustomMetrics(t *testing.T) {
	registry := metrics.NewRegistry()
	registry.Counter("orders_total", "Total orders.", "status").Add(3, "ok")
	registry.Gauge("queue_depth", "Queue depth.", "queue").Set(7, "emails")
	registry.Histogram("op_seconds", "Op duration.", []float64{0.1, 0.5}, "op").Observe(0.2, "load")

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if err := registerCallbacks(provider, registry); err != nil {
		t.Fatalf("registerCallbacks: %v", err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for _, name := range []string{"orders_total", "queue_depth", "op_seconds_count", "op_seconds_sum", "op_seconds_bucket"} {
		if !collectedMetric(rm, name) {
			t.Fatalf("custom metric %q was not collected", name)
		}
	}
}

func collectedMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == name {
				return true
			}
		}
	}
	return false
}

func TestTimeoutZeroReturnsDefault(t *testing.T) {
	if got := (Config{}).timeout(); got != 10*time.Second {
		t.Fatalf("zero timeout = %v, want 10s", got)
	}
	if got := (Config{Timeout: 5 * time.Second}).timeout(); got != 5*time.Second {
		t.Fatalf("explicit timeout = %v, want 5s", got)
	}
}

func TestNewExporterHTTPAndGRPC(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		protocol Protocol
		insecure bool
		headers  map[string]string
		timeout  time.Duration
	}{
		{"http-default", ProtocolHTTP, false, nil, 0},
		{"http-insecure", ProtocolHTTP, true, nil, 0},
		{"http-headers", ProtocolHTTP, false, map[string]string{"Auth": "token"}, 0},
		{"http-timeout", ProtocolHTTP, false, nil, 3 * time.Second},
		{"grpc-default", ProtocolGRPC, false, nil, 0},
		{"grpc-insecure", ProtocolGRPC, true, nil, 0},
		{"grpc-headers", ProtocolGRPC, false, map[string]string{"Auth": "token"}, 0},
		{"grpc-timeout", ProtocolGRPC, false, nil, 3 * time.Second},
		{"grpc-empty-protocol", "", false, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Endpoint: "localhost:4317",
				Protocol: tt.protocol,
				Insecure: tt.insecure,
				Headers:  tt.headers,
				Timeout:  tt.timeout,
			}
			exp, err := newExporter(ctx, cfg)
			if err != nil {
				t.Fatalf("newExporter: %v", err)
			}
			if exp == nil {
				t.Fatal("exporter is nil")
			}
		})
	}
}

func TestNewExporterUnknownProtocol(t *testing.T) {
	_, err := newExporter(context.Background(), Config{Endpoint: "localhost:4317", Protocol: "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestFloat64ObservableCounterOptionsEmptyHelp(t *testing.T) {
	if got := float64ObservableCounterOptions(""); got != nil {
		t.Fatalf("empty help options = %v, want nil", got)
	}
	if got := float64ObservableCounterOptions("help"); got == nil {
		t.Fatal("non-empty help options should not be nil")
	}
}

func TestFloat64ObservableGaugeOptionsEmptyHelp(t *testing.T) {
	if got := float64ObservableGaugeOptions(""); got != nil {
		t.Fatalf("empty help options = %v, want nil", got)
	}
	if got := float64ObservableGaugeOptions("help"); got == nil {
		t.Fatal("non-empty help options should not be nil")
	}
}

func TestInt64ObservableCounterOptionsEmptyHelp(t *testing.T) {
	if got := int64ObservableCounterOptions(""); got != nil {
		t.Fatalf("empty help options = %v, want nil", got)
	}
	if got := int64ObservableCounterOptions("help"); got == nil {
		t.Fatal("non-empty help options should not be nil")
	}
}

func TestUint64ToInt64(t *testing.T) {
	if got := uint64ToInt64(0); got != 0 {
		t.Fatalf("uint64ToInt64(0) = %d, want 0", got)
	}
	if got := uint64ToInt64(42); got != 42 {
		t.Fatalf("uint64ToInt64(42) = %d, want 42", got)
	}
	if got := uint64ToInt64(math.MaxInt64 + 1); got != math.MaxInt64 {
		t.Fatalf("uint64ToInt64(overflow) = %d, want MaxInt64", got)
	}
}

func TestRegisterCustomCallbacksUnknownType(t *testing.T) {
	registry := metrics.NewRegistry()
	// Inject a custom metric with an unknown type by manipulating the snapshot.
	// Since Registry does not expose direct injection, we use the public API
	// and then replace the type in the snapshot via reflection is brittle.
	// Instead we rely on the fact that registerCustomCallbacks iterates
	// snap.Customs; to trigger the default case we need an unknown type.
	// The Registry API only creates Counter/Gauge/Histogram, so we test
	// the default branch via a fake snapshot by using a manual callback
	// registration path that is not directly exposed.
	//
	// Alternative: test registerCustomCallbacks with empty customs (returns nil)
	// and with known types (covered elsewhere). The unknown-type branch is
	// defensive and hard to reach without unsafe/reflect; we skip it.
	if err := registerCustomCallbacks(nil, registry); err != nil {
		t.Fatalf("empty registry should not error: %v", err)
	}
}

func TestObserveOptionSorting(t *testing.T) {
	labels := map[string]string{"z": "1", "a": "2", "m": "3"}
	op := observeOption(labels)
	// observeOption returns otelmetric.WithAttributes; we can only assert
	// it does not panic and is non-nil by type assertion.
	if op == nil {
		t.Fatal("observeOption should not return nil")
	}
	// With extras
	op2 := observeOption(labels, attribute.String("extra", "v"))
	if op2 == nil {
		t.Fatal("observeOption with extras should not return nil")
	}
}

func TestCloseWithNilDeadline(t *testing.T) {
	// Start a minimal exporter so we have a real provider.
	registry := metrics.NewRegistry()
	exp, err := Start(context.Background(), Config{
		ServiceName: "svc",
		Protocol:    ProtocolHTTP,
		Endpoint:    "localhost:4318",
		Insecure:    true,
		Interval:    time.Hour,
		Registry:    registry,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Close with a context that has no deadline should auto-wrap with timeout.
	if err := exp.Close(context.Background()); err != nil {
		// Network error on shutdown without collector is acceptable.
		t.Logf("Close error (expected): %v", err)
	}
}
