// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/observability"
	"github.com/imajinyun/gofly/core/observability/metrics"
	coretrace "github.com/imajinyun/gofly/core/observability/trace"
	coreretry "github.com/imajinyun/gofly/core/retry"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type FallbackFunc func(ctx context.Context, method string, req any, reply any, err error) error

func RecoveryUnaryServerInterceptor(logger *slog.Logger) stdgrpc.UnaryServerInterceptor {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, req any, info *stdgrpc.UnaryServerInfo, handler stdgrpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if v := recover(); v != nil {
				logger.ErrorContext(ctx, "grpc panic recovered", "method", info.FullMethod, "panic", v, "stack", string(debug.Stack()))
				err = coreerrors.GRPCError(coreerrors.New(coreerrors.CodeInternal, "internal server error"))
			}
		}()
		return handler(ctx, req)
	}
}

func ObservabilityUnaryServerInterceptor(service string, registry *metrics.Registry, logger *slog.Logger) stdgrpc.UnaryServerInterceptor {
	if registry == nil {
		registry = metrics.Default
	}
	if logger == nil {
		logger = slog.Default()
	}
	observer := observability.New(observability.Config{Service: service, Registry: registry, Logger: logger})
	return func(ctx context.Context, req any, info *stdgrpc.UnaryServerInfo, handler stdgrpc.UnaryHandler) (any, error) {
		ctx = contextWithIncomingTrace(ctx)
		op := observer.Start("grpc:"+info.FullMethod, "method", info.FullMethod)
		resp, err := handler(ctx, req)
		err = normalizeGRPCError(err)
		code := status.Code(err)
		op.End(ctx, grpcStatusCode(code), err, "grpc server call", "code", code.String())
		return resp, err
	}
}

func ObservabilityStreamServerInterceptor(service string, registry *metrics.Registry, logger *slog.Logger) stdgrpc.StreamServerInterceptor {
	if registry == nil {
		registry = metrics.Default
	}
	if logger == nil {
		logger = slog.Default()
	}
	observer := observability.New(observability.Config{Service: service, Registry: registry, Logger: logger})
	return func(srv any, stream stdgrpc.ServerStream, info *stdgrpc.StreamServerInfo, handler stdgrpc.StreamHandler) error {
		op := observer.Start("grpc_stream:"+info.FullMethod, "method", info.FullMethod)
		err := handler(srv, stream)
		err = normalizeGRPCError(err)
		code := status.Code(err)
		op.End(stream.Context(), grpcStatusCode(code), err, "grpc server stream", "code", code.String())
		return err
	}
}

func RetryUnaryClientInterceptor(policy coreretry.Policy) stdgrpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, invoker stdgrpc.UnaryInvoker, opts ...stdgrpc.CallOption) error {
		if policy.ShouldRetry == nil {
			policy.ShouldRetry = defaultRetryable
		}
		return coreretry.Do(ctx, policy, func() error {
			return invoker(ctx, method, req, reply, cc, opts...)
		})
	}
}

func BreakerUnaryClientInterceptor(cb *breaker.Breaker) stdgrpc.UnaryClientInterceptor {
	if cb == nil {
		cb = breaker.New()
	}
	return func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, invoker stdgrpc.UnaryInvoker, opts ...stdgrpc.CallOption) error {
		return cb.Do(ctx, func() error { return invoker(ctx, method, req, reply, cc, opts...) })
	}
}

func FallbackUnaryClientInterceptor(fallback FallbackFunc) stdgrpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, invoker stdgrpc.UnaryInvoker, opts ...stdgrpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err == nil || fallback == nil {
			return err
		}
		return fallback(ctx, method, req, reply, err)
	}
}

func TimeoutUnaryClientInterceptor(timeout time.Duration) stdgrpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, invoker stdgrpc.UnaryInvoker, opts ...stdgrpc.CallOption) error {
		if timeout <= 0 {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return invoker(callCtx, method, req, reply, cc, opts...)
	}
}

func contextWithIncomingTrace(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	values := md.Get(coretrace.TraceParentHeader)
	if len(values) == 0 {
		return ctx
	}
	next, _ := coretrace.Start(ctx, values[0])
	return next
}

func defaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	switch coreerrors.CodeOf(err) {
	case coreerrors.CodeUnavailable, coreerrors.CodeResourceExhausted, coreerrors.CodeAborted, coreerrors.CodeDeadlineExceeded:
		return true
	default:
		return false
	}
}

func grpcStatusCode(code codes.Code) int {
	return coreerrors.HTTPStatus(coreerrors.CodeFromGRPCStatus(code))
}

func normalizeGRPCError(err error) error {
	return coreerrors.GRPCError(err)
}
