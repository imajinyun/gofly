package redis

import (
	"context"
	"errors"
	"testing"

	redisv9 "github.com/redis/go-redis/v9"

	"github.com/imajinyun/gofly/core/breaker"
)

func TestRedisBreakerHookCommandPolicy(t *testing.T) {
	backendErr := errors.New("redis backend unavailable")

	tests := []struct {
		name        string
		command     string
		err         error
		wantState   breaker.State
		wantResult  error
		secondError error
	}{
		{
			name:       "redis nil is an acceptable cache miss",
			command:    "get",
			err:        redisv9.Nil,
			wantState:  breaker.Closed,
			wantResult: redisv9.Nil,
		},
		{
			name:       "canceled context is acceptable",
			command:    "get",
			err:        context.Canceled,
			wantState:  breaker.Closed,
			wantResult: context.Canceled,
		},
		{
			name:       "hello handshake is ignored",
			command:    "hello",
			err:        backendErr,
			wantState:  breaker.Closed,
			wantResult: backendErr,
		},
		{
			name:       "blocking pop is ignored",
			command:    "blpop",
			err:        backendErr,
			wantState:  breaker.Closed,
			wantResult: backendErr,
		},
		{
			name:        "ordinary command opens breaker",
			command:     "get",
			err:         backendErr,
			wantState:   breaker.Open,
			wantResult:  backendErr,
			secondError: breaker.ErrOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			brk := breaker.New(breaker.WithFailureThreshold(1))
			hook := redisBreakerHook{breaker: brk}
			process := hook.ProcessHook(func(context.Context, redisv9.Cmder) error { return tt.err })
			ctx := context.Background()

			err := process(ctx, redisv9.NewCmd(ctx, tt.command))
			if !errors.Is(err, tt.wantResult) {
				t.Fatalf("first %s error = %v, want %v", tt.command, err, tt.wantResult)
			}
			if got := brk.State(); got != tt.wantState {
				t.Fatalf("state after %s = %s, want %s", tt.command, got, tt.wantState)
			}
			if tt.secondError != nil {
				err = process(ctx, redisv9.NewCmd(ctx, tt.command))
				if !errors.Is(err, tt.secondError) {
					t.Fatalf("second %s error = %v, want %v", tt.command, err, tt.secondError)
				}
			}
		})
	}
}

func TestRedisBreakerHookPipelinePolicy(t *testing.T) {
	backendErr := errors.New("pipeline failed")
	brk := breaker.New(breaker.WithFailureThreshold(1))
	hook := redisBreakerHook{breaker: brk}
	process := hook.ProcessPipelineHook(func(context.Context, []redisv9.Cmder) error { return backendErr })
	ctx := context.Background()
	cmds := []redisv9.Cmder{redisv9.NewCmd(ctx, "get", "key")}

	if err := process(ctx, cmds); !errors.Is(err, backendErr) {
		t.Fatalf("pipeline error = %v, want backend error", err)
	}
	if err := process(ctx, cmds); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("pipeline after open error = %v, want ErrOpen", err)
	}
}
