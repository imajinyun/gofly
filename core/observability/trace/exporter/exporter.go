// Package exporter provides convenience constructors for OpenTelemetry trace
// exporters (OTLP over gRPC or HTTP) so callers can wire core/trace.StartAgent
// without importing the OTLP SDK packages directly.
package exporter

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Protocol selects the OTLP transport.
type Protocol string

const (
	// ProtocolGRPC uses OTLP/gRPC (default OTLP collector port 4317).
	ProtocolGRPC Protocol = "grpc"
	// ProtocolHTTP uses OTLP/HTTP (default OTLP collector port 4318).
	ProtocolHTTP Protocol = "http"
)

// OTLPConfig configures an OTLP exporter.
type OTLPConfig struct {
	// Protocol selects gRPC or HTTP. Defaults to gRPC.
	Protocol Protocol
	// Endpoint is the collector address (host:port for gRPC, host:port or URL
	// host for HTTP). Required.
	Endpoint string
	// Insecure disables TLS (plaintext). Use only for local collectors.
	Insecure bool
	// Headers are sent with each export request (e.g. auth tokens).
	Headers map[string]string
	// Timeout bounds each export request.
	Timeout time.Duration
}

func (c OTLPConfig) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 10 * time.Second
	}
	return c.Timeout
}

// NewOTLP builds an OTLP span exporter from cfg, choosing gRPC or HTTP. The
// returned exporter is ready to pass to core/trace.StartAgent.
func NewOTLP(ctx context.Context, cfg OTLPConfig) (sdktrace.SpanExporter, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("exporter: OTLP endpoint is required")
	}
	switch cfg.Protocol {
	case ProtocolHTTP:
		return newHTTP(ctx, cfg)
	case ProtocolGRPC, "":
		return newGRPC(ctx, cfg)
	default:
		return nil, fmt.Errorf("exporter: unknown OTLP protocol %q", cfg.Protocol)
	}
}

func newGRPC(ctx context.Context, cfg OTLPConfig) (sdktrace.SpanExporter, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
		otlptracegrpc.WithTimeout(cfg.timeout()),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
	}
	exp, err := otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
	if err != nil {
		return nil, fmt.Errorf("exporter: create OTLP/gRPC exporter: %w", err)
	}
	return exp, nil
}

func newHTTP(ctx context.Context, cfg OTLPConfig) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(cfg.Endpoint),
		otlptracehttp.WithTimeout(cfg.timeout()),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
	}
	exp, err := otlptrace.New(ctx, otlptracehttp.NewClient(opts...))
	if err != nil {
		return nil, fmt.Errorf("exporter: create OTLP/HTTP exporter: %w", err)
	}
	return exp, nil
}
