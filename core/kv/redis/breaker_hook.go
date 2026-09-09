package redis

import (
	"context"
	"errors"

	redisv9 "github.com/redis/go-redis/v9"
)

// redisCommandBreaker is the subset of the gofly breakers the hook depends on.
// Both *breaker.Breaker (consecutive-failure) and *breaker.GoogleBreaker
// (Google SRE adaptive throttling) satisfy it, so the Redis adapter can pick
// either strategy without changing the hook.
type redisCommandBreaker interface {
	DoWithAcceptable(ctx context.Context, fn func() error, acceptable func(error) bool) error
}

// redisBreakerHook applies gofly's circuit breaker to v9 command execution.
// HELLO is a connection handshake and BLPOP intentionally blocks, so neither
// participates in availability accounting, matching go-zero's hook policy.
type redisBreakerHook struct {
	breaker redisCommandBreaker
}

func (h redisBreakerHook) DialHook(next redisv9.DialHook) redisv9.DialHook {
	return next
}

func (h redisBreakerHook) ProcessHook(next redisv9.ProcessHook) redisv9.ProcessHook {
	return func(ctx context.Context, cmd redisv9.Cmder) error {
		if h.breaker == nil || ignoredRedisBreakerCommand(cmd.Name()) {
			return next(ctx, cmd)
		}
		return h.breaker.DoWithAcceptable(ctx, func() error {
			return next(ctx, cmd)
		}, redisBreakerAcceptable)
	}
}

func (h redisBreakerHook) ProcessPipelineHook(next redisv9.ProcessPipelineHook) redisv9.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redisv9.Cmder) error {
		if h.breaker == nil {
			return next(ctx, cmds)
		}
		return h.breaker.DoWithAcceptable(ctx, func() error {
			return next(ctx, cmds)
		}, redisBreakerAcceptable)
	}
}

func ignoredRedisBreakerCommand(name string) bool {
	switch name {
	case "blpop", "hello":
		return true
	default:
		return false
	}
}

func redisBreakerAcceptable(err error) bool {
	return err == nil || errors.Is(err, redisv9.Nil) || errors.Is(err, context.Canceled)
}
