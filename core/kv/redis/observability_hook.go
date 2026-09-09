package redis

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
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
	slow     *metrics.Counter
	pool     *metrics.Gauge
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
	slow: metrics.RegisterCounter(
		"gofly_redis_slow_commands_total",
		"Redis commands exceeding the configured slow threshold.",
		"operation",
	),
	pool: metrics.RegisterGauge(
		"gofly_redis_pool_connections",
		"Redis connection-pool statistics by bounded state.",
		"topology",
		"state",
	),
}

var redisPoolCounters = struct {
	hits     *metrics.Counter
	misses   *metrics.Counter
	timeouts *metrics.Counter
	stale    *metrics.Counter
	waits    *metrics.Counter
}{
	hits: metrics.RegisterCounter(
		"gofly_redis_pool_hits_total",
		"Redis pool hits reported by go-redis.",
		"topology",
	),
	misses: metrics.RegisterCounter(
		"gofly_redis_pool_misses_total",
		"Redis pool misses reported by go-redis.",
		"topology",
	),
	timeouts: metrics.RegisterCounter(
		"gofly_redis_pool_timeouts_total",
		"Redis pool acquisition timeouts reported by go-redis.",
		"topology",
	),
	stale: metrics.RegisterCounter(
		"gofly_redis_pool_stale_connections_total",
		"Redis stale connections removed from the pool.",
		"topology",
	),
	waits: metrics.RegisterCounter(
		"gofly_redis_pool_waits_total",
		"Redis pool waits reported by go-redis.",
		"topology",
	),
}

var redisPoolRegistry = struct {
	sync.Mutex
	clients map[*Client]struct{}
}{clients: make(map[*Client]struct{})}

// redisObservabilityHook records low-cardinality command metrics and OTel
// spans. It deliberately excludes Redis command arguments to avoid keys,
// values, scripts, and credentials leaking into telemetry.
type redisObservabilityHook struct {
	slowThreshold time.Duration
	after         func()
}

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
	if h.after != nil {
		h.after()
	}
	if h.slowThreshold > 0 && duration > h.slowThreshold {
		redisMetrics.slow.Inc(operation)
		slog.WarnContext(ctx, "slow Redis command",
			"operation", operation,
			"duration", duration,
			"threshold", h.slowThreshold,
		)
	}

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

func (c *Client) registerPoolMetrics() {
	if c == nil {
		return
	}
	redisPoolRegistry.Lock()
	redisPoolRegistry.clients[c] = struct{}{}
	redisPoolRegistry.Unlock()
	c.refreshPoolMetrics()
}

func (c *Client) refreshPoolMetrics() {
	if c == nil || c.client == nil {
		return
	}
	stats := c.Snapshot()
	topology := redisTopology(c.cfg)
	c.poolMetricsMu.Lock()
	previous := c.poolMetrics
	c.poolMetrics = stats
	c.poolMetricsMu.Unlock()
	redisPoolCounters.hits.Add(redisPoolDelta(stats.PoolHits, previous.PoolHits), topology)
	redisPoolCounters.misses.Add(redisPoolDelta(stats.PoolMisses, previous.PoolMisses), topology)
	redisPoolCounters.timeouts.Add(redisPoolDelta(stats.PoolTimeouts, previous.PoolTimeouts), topology)
	redisPoolCounters.stale.Add(redisPoolDelta(stats.StaleConns, previous.StaleConns), topology)
	redisPoolCounters.waits.Add(redisPoolDelta(stats.WaitCount, previous.WaitCount), topology)
	refreshRedisPoolConnectionMetrics(topology)
}

func (c *Client) unregisterPoolMetrics() {
	if c == nil {
		return
	}
	topology := redisTopology(c.cfg)
	redisPoolRegistry.Lock()
	delete(redisPoolRegistry.clients, c)
	redisPoolRegistry.Unlock()
	refreshRedisPoolConnectionMetrics(topology)
}

func redisPoolDelta(current, previous uint32) float64 {
	if current >= previous {
		return float64(current - previous)
	}
	// Pool counters can reset when an underlying client is replaced. Count the
	// new value from zero rather than emitting a negative counter delta.
	return float64(current)
}

func refreshRedisPoolConnectionMetrics(topology string) {
	redisPoolRegistry.Lock()
	defer redisPoolRegistry.Unlock()
	var active, idle, maxConns int
	for client := range redisPoolRegistry.clients {
		if redisTopology(client.cfg) != topology {
			continue
		}
		stats := client.Snapshot()
		active += stats.ActiveConns
		idle += stats.IdleConns
		maxConns += client.cfg.MaxConns
	}
	if maxConns == 0 {
		for _, state := range []string{"active", "idle", "max"} {
			redisMetrics.pool.Delete(topology, state)
		}
		return
	}
	redisMetrics.pool.Set(float64(active), topology, "active")
	redisMetrics.pool.Set(float64(idle), topology, "idle")
	redisMetrics.pool.Set(float64(maxConns), topology, "max")
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
