// Package otel bridges the core/metrics Registry to OpenTelemetry, exporting
// gateway/service metrics over OTLP (gRPC or HTTP) without forcing the core
// metrics package to depend on the OTel SDK directly.
package otel

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"

	"github.com/gofly/gofly/core/metrics"
)

// Protocol selects the OTLP transport.
type Protocol string

const (
	// ProtocolGRPC uses OTLP/gRPC (default OTLP collector port 4317).
	ProtocolGRPC Protocol = "grpc"
	// ProtocolHTTP uses OTLP/HTTP (default OTLP collector port 4318).
	ProtocolHTTP Protocol = "http"
)

// Config configures the OTLP metric exporter and its meter provider.
type Config struct {
	// ServiceName is reported as the resource service.name. Defaults to "gofly".
	ServiceName string
	// Protocol selects gRPC or HTTP. Defaults to gRPC.
	Protocol Protocol
	// Endpoint is the collector address. Required.
	Endpoint string
	// Insecure disables TLS (plaintext). Use only for local collectors.
	Insecure bool
	// Headers are sent with each export request (e.g. auth tokens).
	Headers map[string]string
	// Timeout bounds each export request.
	Timeout time.Duration
	// Interval is the periodic collection/export interval. Defaults to 15s.
	Interval time.Duration
	// Registry is the source of metrics. Defaults to metrics.Default.
	Registry *metrics.Registry
}

func (c Config) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 10 * time.Second
	}
	return c.Timeout
}

func (c Config) interval() time.Duration {
	if c.Interval <= 0 {
		return 15 * time.Second
	}
	return c.Interval
}

// Exporter owns the OTel meter provider streaming Registry snapshots to a
// collector. Close it to flush and release resources.
type Exporter struct {
	provider *sdkmetric.MeterProvider
}

// Start builds an OTLP metric exporter, wires a periodic reader, and registers
// observable callbacks that read from the Registry on each collection cycle.
func Start(ctx context.Context, cfg Config) (*Exporter, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("metrics/otel: OTLP endpoint is required")
	}
	registry := cfg.Registry
	if registry == nil {
		registry = metrics.Default
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "gofly"
	}
	exp, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(semconv.ServiceName(cfg.ServiceName)))
	if err != nil {
		return nil, fmt.Errorf("metrics/otel: create resource: %w", err)
	}
	reader := sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(cfg.interval()))
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)
	if err := registerCallbacks(provider, registry); err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}
	return &Exporter{provider: provider}, nil
}

// MeterProvider exposes the underlying provider so callers can create custom
// instruments alongside the bridged Registry metrics.
func (e *Exporter) MeterProvider() otelmetric.MeterProvider {
	if e == nil {
		return nil
	}
	return e.provider
}

// Close flushes pending metrics and shuts down the provider.
func (e *Exporter) Close(ctx context.Context) error {
	if e == nil || e.provider == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	return e.provider.Shutdown(ctx)
}

func newExporter(ctx context.Context, cfg Config) (sdkmetric.Exporter, error) {
	switch cfg.Protocol {
	case ProtocolHTTP:
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.Endpoint),
			otlpmetrichttp.WithTimeout(cfg.timeout()),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
		}
		exp, err := otlpmetrichttp.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("metrics/otel: create OTLP/HTTP exporter: %w", err)
		}
		return exp, nil
	case ProtocolGRPC, "":
		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
			otlpmetricgrpc.WithTimeout(cfg.timeout()),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlpmetricgrpc.WithHeaders(cfg.Headers))
		}
		exp, err := otlpmetricgrpc.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("metrics/otel: create OTLP/gRPC exporter: %w", err)
		}
		return exp, nil
	default:
		return nil, fmt.Errorf("metrics/otel: unknown OTLP protocol %q", cfg.Protocol)
	}
}

func registerCallbacks(provider *sdkmetric.MeterProvider, registry *metrics.Registry) error {
	meter := provider.Meter("github.com/gofly/gofly/core/metrics")
	requests, err := meter.Int64ObservableCounter("gofly_requests_total",
		otelmetric.WithDescription("Total number of handled requests."))
	if err != nil {
		return fmt.Errorf("metrics/otel: create requests counter: %w", err)
	}
	errorsTotal, err := meter.Int64ObservableCounter("gofly_errors_total",
		otelmetric.WithDescription("Total number of failed requests."))
	if err != nil {
		return fmt.Errorf("metrics/otel: create errors counter: %w", err)
	}
	inFlight, err := meter.Int64ObservableGauge("gofly_inflight_requests",
		otelmetric.WithDescription("Current number of in-flight requests."))
	if err != nil {
		return fmt.Errorf("metrics/otel: create inflight gauge: %w", err)
	}
	goroutines, err := meter.Int64ObservableGauge("gofly_runtime_goroutines",
		otelmetric.WithDescription("Current number of goroutines."))
	if err != nil {
		return fmt.Errorf("metrics/otel: create goroutines gauge: %w", err)
	}
	heapAlloc, err := meter.Int64ObservableGauge("gofly_runtime_heap_alloc_bytes",
		otelmetric.WithDescription("Current heap allocation in bytes."))
	if err != nil {
		return fmt.Errorf("metrics/otel: create heap gauge: %w", err)
	}
	routeRequests, err := meter.Int64ObservableCounter("gofly_route_requests_total",
		otelmetric.WithDescription("Total number of requests by route."))
	if err != nil {
		return fmt.Errorf("metrics/otel: create route requests counter: %w", err)
	}
	routeErrors, err := meter.Int64ObservableCounter("gofly_route_errors_total",
		otelmetric.WithDescription("Total number of failed requests by route."))
	if err != nil {
		return fmt.Errorf("metrics/otel: create route errors counter: %w", err)
	}
	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		snap := registry.Snapshot()
		observer.ObserveInt64(requests, snap.Requests)
		observer.ObserveInt64(errorsTotal, snap.Errors)
		observer.ObserveInt64(inFlight, snap.InFlight)
		observer.ObserveInt64(goroutines, int64(snap.Runtime.Goroutines))
		observer.ObserveInt64(heapAlloc, uint64ToInt64(snap.Runtime.HeapAlloc))
		for route, stats := range snap.Routes {
			attrs := otelmetric.WithAttributes(attribute.String("route", route))
			observer.ObserveInt64(routeRequests, stats.Requests, attrs)
			observer.ObserveInt64(routeErrors, stats.Errors, attrs)
		}
		return nil
	}, requests, errorsTotal, inFlight, goroutines, heapAlloc, routeRequests, routeErrors)
	if err != nil {
		return fmt.Errorf("metrics/otel: register callback: %w", err)
	}
	if err := registerCustomCallbacks(meter, registry); err != nil {
		return err
	}
	return nil
}

func registerCustomCallbacks(meter otelmetric.Meter, registry *metrics.Registry) error {
	snap := registry.Snapshot()
	if len(snap.Customs) == 0 {
		return nil
	}
	names := make([]string, 0, len(snap.Customs))
	for name := range snap.Customs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		metric := snap.Customs[name]
		switch metric.Type {
		case metrics.MetricCounter:
			if err := registerCustomCounterCallback(meter, registry, metric); err != nil {
				return err
			}
		case metrics.MetricGauge:
			if err := registerCustomGaugeCallback(meter, registry, metric); err != nil {
				return err
			}
		case metrics.MetricHistogram:
			if err := registerCustomHistogramCallback(meter, registry, metric); err != nil {
				return err
			}
		default:
			return fmt.Errorf("metrics/otel: unknown custom metric type %q for %s", metric.Type, metric.Name)
		}
	}
	return nil
}

func registerCustomCounterCallback(meter otelmetric.Meter, registry *metrics.Registry, metric metrics.CustomMetricSnapshot) error {
	instrument, err := meter.Float64ObservableCounter(metric.Name, float64ObservableCounterOptions(metric.Help)...)
	if err != nil {
		return fmt.Errorf("metrics/otel: create custom counter %s: %w", metric.Name, err)
	}
	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		current, ok := registry.Snapshot().Customs[metric.Name]
		if !ok {
			return nil
		}
		for _, series := range current.Series {
			observer.ObserveFloat64(instrument, series.Value, observeOption(series.Labels))
		}
		return nil
	}, instrument)
	if err != nil {
		return fmt.Errorf("metrics/otel: register custom counter %s: %w", metric.Name, err)
	}
	return nil
}

func registerCustomGaugeCallback(meter otelmetric.Meter, registry *metrics.Registry, metric metrics.CustomMetricSnapshot) error {
	instrument, err := meter.Float64ObservableGauge(metric.Name, float64ObservableGaugeOptions(metric.Help)...)
	if err != nil {
		return fmt.Errorf("metrics/otel: create custom gauge %s: %w", metric.Name, err)
	}
	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		current, ok := registry.Snapshot().Customs[metric.Name]
		if !ok {
			return nil
		}
		for _, series := range current.Series {
			observer.ObserveFloat64(instrument, series.Value, observeOption(series.Labels))
		}
		return nil
	}, instrument)
	if err != nil {
		return fmt.Errorf("metrics/otel: register custom gauge %s: %w", metric.Name, err)
	}
	return nil
}

func registerCustomHistogramCallback(meter otelmetric.Meter, registry *metrics.Registry, metric metrics.CustomMetricSnapshot) error {
	count, err := meter.Int64ObservableCounter(metric.Name+"_count", int64ObservableCounterOptions(metric.Help+" count")...)
	if err != nil {
		return fmt.Errorf("metrics/otel: create custom histogram count %s: %w", metric.Name, err)
	}
	sum, err := meter.Float64ObservableCounter(metric.Name+"_sum", float64ObservableCounterOptions(metric.Help+" sum")...)
	if err != nil {
		return fmt.Errorf("metrics/otel: create custom histogram sum %s: %w", metric.Name, err)
	}
	bucket, err := meter.Int64ObservableCounter(metric.Name+"_bucket", int64ObservableCounterOptions(metric.Help+" buckets")...)
	if err != nil {
		return fmt.Errorf("metrics/otel: create custom histogram buckets %s: %w", metric.Name, err)
	}
	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		current, ok := registry.Snapshot().Customs[metric.Name]
		if !ok {
			return nil
		}
		for _, series := range current.Series {
			observer.ObserveInt64(count, uint64ToInt64(series.Count), observeOption(series.Labels))
			observer.ObserveFloat64(sum, series.Sum, observeOption(series.Labels))
			bounds := make([]string, 0, len(series.Counts))
			for bound := range series.Counts {
				bounds = append(bounds, bound)
			}
			sort.Strings(bounds)
			for _, bound := range bounds {
				observer.ObserveInt64(bucket, uint64ToInt64(series.Counts[bound]), observeOption(series.Labels, attribute.String("le", bound)))
			}
		}
		return nil
	}, count, sum, bucket)
	if err != nil {
		return fmt.Errorf("metrics/otel: register custom histogram %s: %w", metric.Name, err)
	}
	return nil
}

func float64ObservableCounterOptions(help string) []otelmetric.Float64ObservableCounterOption {
	if help == "" {
		return nil
	}
	return []otelmetric.Float64ObservableCounterOption{otelmetric.WithDescription(help)}
}

func float64ObservableGaugeOptions(help string) []otelmetric.Float64ObservableGaugeOption {
	if help == "" {
		return nil
	}
	return []otelmetric.Float64ObservableGaugeOption{otelmetric.WithDescription(help)}
}

func int64ObservableCounterOptions(help string) []otelmetric.Int64ObservableCounterOption {
	if help == "" {
		return nil
	}
	return []otelmetric.Int64ObservableCounterOption{otelmetric.WithDescription(help)}
}

func observeOption(labels map[string]string, extras ...attribute.KeyValue) otelmetric.ObserveOption {
	attrs := make([]attribute.KeyValue, 0, len(labels)+len(extras))
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		attrs = append(attrs, attribute.String(key, labels[key]))
	}
	attrs = append(attrs, extras...)
	return otelmetric.WithAttributes(attrs...)
}

func uint64ToInt64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(value)
}
