// Package observability provides request tracing, metrics recording, and
// structured logging helpers for gofly services.
package observability

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/imajinyun/gofly/core/metadata"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/core/observability/trace"
)

// Config configures the Observer.
type Config struct {
	Service    string
	Registry   *metrics.Registry
	Logger     *slog.Logger
	LogSampler trace.Sampler
}

// Observer records traces, metrics, and logs for operations.
type Observer struct {
	service    string
	registry   *metrics.Registry
	logger     *slog.Logger
	logSampler trace.Sampler
}

// Operation represents a single observable operation.
type Operation struct {
	observer *Observer
	name     string
	start    time.Time
	attrs    []any
	ended    bool
}

// New creates an Observer from Config.
func New(conf Config) *Observer {
	registry := conf.Registry
	if registry == nil {
		registry = metrics.Default
	}
	logger := conf.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Observer{service: conf.Service, registry: registry, logger: logger, logSampler: conf.LogSampler}
}

func StartTrace(ctx context.Context, parent string, service string, sampler trace.Sampler) (context.Context, trace.SpanContext) {
	ctx, sc := trace.StartWithSampler(ctx, parent, sampler)
	ctx = metadata.Append(ctx,
		trace.TraceParentHeader, trace.TraceParent(sc),
		trace.TraceIDKey, sc.TraceID,
		trace.SpanIDKey, sc.SpanID,
		trace.SampledKey, strconv.FormatBool(sc.Sampled),
	)
	if service != "" {
		ctx = metadata.Append(ctx, "service", service)
	}
	return ctx, sc
}

func TraceAttrs(ctx context.Context) []any {
	attrs := make([]any, 0, 6)
	if sc, ok := trace.FromContext(ctx); ok {
		attrs = append(attrs, trace.TraceIDKey, sc.TraceID, trace.SpanIDKey, sc.SpanID, trace.SampledKey, sc.Sampled)
	}
	if requestID := metadata.RequestIDFromContext(ctx); requestID != "" {
		attrs = append(attrs, metadata.RequestIDKey, requestID)
	}
	return attrs
}

func ShouldLog(ctx context.Context, sampler trace.Sampler, status int) bool {
	if status >= http.StatusInternalServerError {
		return true
	}
	traceID := ""
	if sc, ok := trace.FromContext(ctx); ok {
		traceID = sc.TraceID
	}
	return trace.ShouldSample(sampler, traceID)
}

func Record(registry *metrics.Registry, name string, status int, duration time.Duration) {
	if registry == nil {
		registry = metrics.Default
	}
	registry.Observe(name, status, duration)
}

func (o *Observer) Start(name string, attrs ...any) *Operation {
	if o == nil {
		o = New(Config{})
	}
	o.registry.IncInFlight()
	return &Operation{observer: o, name: name, start: time.Now(), attrs: append([]any(nil), attrs...)}
}

func (op *Operation) End(ctx context.Context, status int, err error, message string, attrs ...any) {
	if op == nil || op.ended {
		return
	}
	op.ended = true
	duration := time.Since(op.start)
	op.observer.registry.DecInFlight()
	op.observer.registry.Observe(op.name, status, duration)

	logAttrs := make([]any, 0, len(op.attrs)+len(attrs)+10)
	if op.observer.service != "" {
		logAttrs = append(logAttrs, "service", op.observer.service)
	}
	logAttrs = append(logAttrs, "name", op.name, "status", status, "duration", duration)
	logAttrs = append(logAttrs, op.attrs...)
	logAttrs = append(logAttrs, attrs...)
	logAttrs = append(logAttrs, TraceAttrs(ctx)...)
	if err != nil {
		logAttrs = append(logAttrs, "error", err)
		op.observer.logger.WarnContext(ctx, message, logAttrs...)
		return
	}
	if ShouldLog(ctx, op.observer.logSampler, status) {
		op.observer.logger.InfoContext(ctx, message, logAttrs...)
	}
}
