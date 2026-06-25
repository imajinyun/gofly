package rpc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/imajinyun/gofly/core/observability"
	"github.com/imajinyun/gofly/core/observability/metrics"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

type KitexEndpoint = endpoint.Endpoint

type KitexMiddleware = endpoint.Middleware

type KitexInterceptor func(context.Context, any, KitexEndpoint) (any, error)

func KitexEndpointChain(middlewares ...KitexMiddleware) KitexMiddleware {
	return endpoint.Chain(middlewares...)
}

func KitexInterceptorMiddleware(interceptors ...KitexInterceptor) KitexMiddleware {
	return func(next KitexEndpoint) KitexEndpoint {
		chain := next
		for i := len(interceptors) - 1; i >= 0; i-- {
			interceptor := interceptors[i]
			if interceptor == nil {
				continue
			}
			nextEndpoint := chain
			chain = func(ctx context.Context, req any) (any, error) {
				return interceptor(ctx, req, nextEndpoint)
			}
		}
		return chain
	}
}

func WithKitexServerInterceptors(interceptors ...KitexInterceptor) ServerOption {
	return WithServerMiddleware(KitexInterceptorMiddleware(interceptors...))
}

func WithKitexClientInterceptors(interceptors ...KitexInterceptor) ClientOption {
	return WithClientMiddleware(KitexInterceptorMiddleware(interceptors...))
}

func KitexObservabilityInterceptor(name string, reg *metrics.Registry, logger *slog.Logger) KitexInterceptor {
	if name == "" {
		name = "rpc.kitex"
	}
	if reg == nil {
		reg = metrics.Default
	}
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, req any, next KitexEndpoint) (any, error) {
		start := time.Now()
		reg.IncInFlight()
		defer reg.DecInFlight()
		resp, err := next(ctx, req)
		status := httpStatusFromCode(CodeOf(err))
		if err == nil {
			status = 200
		}
		observability.Record(reg, name, status, time.Since(start))
		attrs := []any{"name", name, "code", CodeOf(err), "duration", time.Since(start)}
		attrs = append(attrs, observability.TraceAttrs(ctx)...)
		if err != nil {
			attrs = append(attrs, "error", fmt.Sprint(err))
			logger.WarnContext(ctx, "rpc kitex call", attrs...)
			return resp, err
		}
		logger.InfoContext(ctx, "rpc kitex call", attrs...)
		return resp, nil
	}
}
