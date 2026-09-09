package redis

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	redisv9 "github.com/redis/go-redis/v9"

	"github.com/imajinyun/gofly/core/breaker"
	"github.com/imajinyun/gofly/core/observability/metrics"
)

func TestRedisErrorClass(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "success", want: "ok"},
		{name: "miss", err: redisv9.Nil, want: "not_found"},
		{name: "canceled", err: context.Canceled, want: "canceled"},
		{name: "deadline", err: context.DeadlineExceeded, want: "deadline"},
		{name: "breaker", err: breaker.ErrOpen, want: "breaker_open"},
		{name: "backend", err: errors.New("backend"), want: "backend"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redisErrorClass(tt.err); got != tt.want {
				t.Fatalf("redisErrorClass(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestRedisObservabilityHookAvoidsCommandArguments(t *testing.T) {
	hook := redisObservabilityHook{}
	process := hook.ProcessHook(func(context.Context, redisv9.Cmder) error { return errors.New("backend") })
	ctx := context.Background()
	if err := process(ctx, redisv9.NewCmd(ctx, "set", "customer-secret-key", "customer-secret-value")); err == nil {
		t.Fatal("ProcessHook: want backend error")
	}
	if redisMetrics.errors == nil || redisMetrics.duration == nil {
		t.Fatal("Redis metrics must be initialized")
	}
	snapshot, ok := metrics.Default.Snapshot().Customs["gofly_redis_command_errors_total"]
	if !ok {
		t.Fatal("Redis error metric is missing")
	}
	found := false
	for _, series := range snapshot.Series {
		if series.Labels["operation"] == "set" && series.Labels["class"] == "backend" {
			found = true
			for _, value := range series.Labels {
				if value == "customer-secret-key" || value == "customer-secret-value" {
					t.Fatalf("Redis metric leaked command argument: %#v", series.Labels)
				}
			}
		}
	}
	if !found {
		t.Fatalf("Redis metric series = %#v, want set/backend", snapshot.Series)
	}
}

func TestRedisObservabilityHookRecordsSafeSlowSignal(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	hook := redisObservabilityHook{slowThreshold: time.Nanosecond}
	process := hook.ProcessHook(func(context.Context, redisv9.Cmder) error { return nil })
	ctx := context.Background()
	if err := process(ctx, redisv9.NewCmd(ctx, "set", "secret-key", "secret-value")); err != nil {
		t.Fatalf("ProcessHook: %v", err)
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, "slow Redis command") || !strings.Contains(logOutput, "operation=set") {
		t.Fatalf("slow log = %q, want operation-only warning", logOutput)
	}
	for _, secret := range []string{"secret-key", "secret-value"} {
		if strings.Contains(logOutput, secret) {
			t.Fatalf("slow log leaked command argument %q: %s", secret, logOutput)
		}
	}

	snapshot := metrics.Default.Snapshot().Customs["gofly_redis_slow_commands_total"]
	if !customMetricHasSeries(snapshot, map[string]string{"operation": "set"}) {
		t.Fatalf("slow metric series = %#v, want set operation", snapshot.Series)
	}
}

func TestRedisPoolMetricsAreExportedWithoutEndpointLabels(t *testing.T) {
	client, _ := newTestClient(t)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	client.refreshPoolMetrics()

	snapshot := metrics.Default.Snapshot()
	connections := snapshot.Customs["gofly_redis_pool_connections"]
	if !customMetricHasSeries(connections, map[string]string{"topology": "node", "state": "max"}) {
		t.Fatalf("pool connection series = %#v, want node/max", connections.Series)
	}
	for name, metric := range snapshot.Customs {
		if !strings.HasPrefix(name, "gofly_redis_pool_") {
			continue
		}
		for _, series := range metric.Series {
			for key, value := range series.Labels {
				if key != "topology" && key != "state" {
					t.Fatalf("pool metric %s has unbounded label %q=%q", name, key, value)
				}
				if strings.Contains(value, "127.0.0.1") {
					t.Fatalf("pool metric %s leaked endpoint: %#v", name, series.Labels)
				}
			}
		}
	}
}

func customMetricHasSeries(snapshot metrics.CustomMetricSnapshot, labels map[string]string) bool {
	for _, series := range snapshot.Series {
		match := true
		for key, value := range labels {
			if series.Labels[key] != value {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
