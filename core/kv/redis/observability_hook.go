package redis

import (
	"context"
	"errors"
	"net"
	"time"

	redisv9 "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/observability/metrics"
)

var redisMetrics = struct {
	duration *metrics.Histogram
	errors   *metrics.Counter
}{
	duration: metrics.RegisterHistogram(
		"gofly_redis_command_duration_seconds",
		"Redis command latency without key or value labels.",
		nil,
		"operation",
	),
	errors: metrics.RegisterCounter(
		"gofly_redis_command_errors_total",
		"Redis command errors by low-cardinality operation and class.",
		"operation",
		"class",
	),
}

// redisObservabilityHook records low-cardinality command metrics and OTel
// spans. It deliberately excludes Redis command arguments to avoid keys,
// values, scripts, and credentials leaking into telemetry.
type redisObservabilityHook struct{}

func (h redisObservabilityHook) DialHook(next redisv9.DialHook) redisv9.DialHook {
	return next
}

func (h redisObservabilityHook) ProcessHook(next redisv9.ProcessHook) redisv9.ProcessHook {
	return func(ctx context.Context, cmd redisv9.Cmder) error {
		return h.observe(ctx, cmd.Name(), func(ctx context.Context) error {
			return next(ctx, cmd)
		})
	}
}

func (h redisObservabilityHook) ProcessPipelineHook(next redisv9.ProcessPipelineHook) redisv9.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redisv9.Cmder) error {
		return h.observe(ctx, "pipeline", func(ctx context.Context) error {
			return next(ctx, cmds)
		})
	}
}

func (h redisObservabilityHook) observe(ctx context.Context, operation string, next func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, span := otel.Tracer("github.com/imajinyun/gofly/core/kv/redis").Start(ctx, "redis",
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(attribute.String("db.system", "redis"), attribute.String("db.operation.name", operation)),
	)
	start := time.Now()
	err := next(ctx)
	duration := time.Since(start)
	redisMetrics.duration.Observe(duration.Seconds(), operation)

	class := redisErrorClass(err)
	if class == "ok" || class == "not_found" || class == "canceled" {
		span.SetStatus(codes.Ok, "")
	} else {
		span.SetStatus(codes.Error, class)
		span.RecordError(err)
		redisMetrics.errors.Inc(operation, class)
	}
	span.End()
	return err
}

func redisErrorClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, redisv9.Nil):
		return "not_found"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, breaker.ErrOpen):
		return "breaker_open"
	}
	var operationErr *net.OpError
	if errors.As(err, &operationErr) && operationErr.Timeout() {
		return "timeout"
	}
	return "backend"
}
