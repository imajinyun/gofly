// Package trace provides W3C traceparent propagation and OpenTelemetry tracer
// lifecycle management for gofly services.
package trace

import (
	"context"
	"fmt"
	"time"

	"github.com/imajinyun/gofly/core/observability/trace/exporter"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
)

// AgentConfig controls the OpenTelemetry trace agent.
type AgentConfig struct {
	Enabled     bool                `json:"enabled"`
	ServiceName string              `json:"serviceName,omitempty"`
	SampleRatio float64             `json:"sampleRatio,omitempty"`
	OTLP        exporter.OTLPConfig `json:"otlp,omitempty"`
}

// Agent wraps an OpenTelemetry TracerProvider.
type Agent struct {
	provider *sdktrace.TracerProvider
}

// StartAgent initialises and starts the trace agent with the given config and
// optional additional exporters.
func StartAgent(ctx context.Context, conf AgentConfig, exporters ...sdktrace.SpanExporter) (*Agent, error) {
	if !conf.Enabled {
		return &Agent{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if conf.ServiceName == "" {
		conf.ServiceName = "gofly"
	}
	if conf.OTLP.Endpoint != "" {
		exp, err := exporter.NewOTLP(ctx, conf.OTLP)
		if err != nil {
			return nil, fmt.Errorf("create trace OTLP exporter: %w", err)
		}
		exporters = append(exporters, exp)
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(semconv.ServiceName(conf.ServiceName)))
	if err != nil {
		return nil, fmt.Errorf("create otel resource: %w", err)
	}
	if conf.SampleRatio <= 0 || conf.SampleRatio > 1 {
		conf.SampleRatio = 1
	}
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(conf.SampleRatio))),
	}
	for _, exporter := range exporters {
		if exporter != nil {
			options = append(options, sdktrace.WithBatcher(exporter))
		}
	}
	provider := sdktrace.NewTracerProvider(options...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return &Agent{provider: provider}, nil
}

func (a *Agent) Shutdown(ctx context.Context) error {
	if a == nil || a.provider == nil {
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
	return a.provider.Shutdown(ctx)
}
