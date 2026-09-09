package redis

import (
	"context"
	"errors"
	"testing"

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
