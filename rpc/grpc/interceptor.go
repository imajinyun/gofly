// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/breaker"
	coreerrors "github.com/imajinyun/gofly/core/errors"
	"github.com/imajinyun/gofly/core/limit"
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

func RecoveryStreamServerInterceptor(logger *slog.Logger) stdgrpc.StreamServerInterceptor {
	if logger == nil {
		logger = slog.Default()
	}
	return func(srv any, stream stdgrpc.ServerStream, info *stdgrpc.StreamServerInfo, handler stdgrpc.StreamHandler) (err error) {
		defer func() {
			if v := recover(); v != nil {
				logger.ErrorContext(stream.Context(), "grpc panic recovered", "method", info.FullMethod, "panic", v, "stack", string(debug.Stack()))
				err = coreerrors.GRPCError(coreerrors.New(coreerrors.CodeInternal, "internal server error"))
			}
		}()
		return handler(srv, stream)
	}
}

// AdaptiveLimitUnaryServerInterceptor rejects work when the shared adaptive
// limiter detects saturation and feeds completion latency/status back into the
// limiter. A nil limiter is a no-op.
func AdaptiveLimitUnaryServerInterceptor(limiter *limit.AdaptiveLimiter) stdgrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *stdgrpc.UnaryServerInfo, handler stdgrpc.UnaryHandler) (any, error) {
		token, err := limiter.Allow()
		if err != nil {
			return nil, coreerrors.GRPCError(coreerrors.New(coreerrors.CodeResourceExhausted, err.Error()))
		}
		success := false
		defer func() { token.Done(success) }()
		resp, handlerErr := handler(ctx, req)
		success = grpcOutcomeAcceptable(handlerErr)
		return resp, handlerErr
	}
}

// AdaptiveLimitStreamServerInterceptor applies one admission token to the full
// lifetime of a server-side stream handler.
func AdaptiveLimitStreamServerInterceptor(limiter *limit.AdaptiveLimiter) stdgrpc.StreamServerInterceptor {
	return func(srv any, stream stdgrpc.ServerStream, info *stdgrpc.StreamServerInfo, handler stdgrpc.StreamHandler) error {
		token, err := limiter.Allow()
		if err != nil {
			return coreerrors.GRPCError(coreerrors.New(coreerrors.CodeResourceExhausted, err.Error()))
		}
		success := false
		defer func() { token.Done(success) }()
		handlerErr := handler(srv, stream)
		success = grpcOutcomeAcceptable(handlerErr)
		return handlerErr
	}
}

func grpcOutcomeAcceptable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	switch status.Code(err) {
	case codes.OK, codes.Canceled, codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition:
		return true
	default:
		return false
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

// ObservabilityUnaryClientInterceptor records latency, status and structured logs for unary calls.
func ObservabilityUnaryClientInterceptor(service string, registry *metrics.Registry, logger *slog.Logger) stdgrpc.UnaryClientInterceptor {
	if registry == nil {
		registry = metrics.Default
	}
	if logger == nil {
		logger = slog.Default()
	}
	observer := observability.New(observability.Config{Service: service, Registry: registry, Logger: logger})
	return func(ctx context.Context, method string, req any, reply any, cc *stdgrpc.ClientConn, invoker stdgrpc.UnaryInvoker, opts ...stdgrpc.CallOption) error {
		op := observer.Start("grpc_client:"+method, "method", method)
		err := invoker(ctx, method, req, reply, cc, opts...)
		code := status.Code(err)
		op.End(ctx, grpcStatusCode(code), err, "grpc client call", "code", code.String())
		return err
	}
}

// ObservabilityStreamClientInterceptor records the lifecycle of client streams.
func ObservabilityStreamClientInterceptor(service string, registry *metrics.Registry, logger *slog.Logger) stdgrpc.StreamClientInterceptor {
	if registry == nil {
		registry = metrics.Default
	}
	if logger == nil {
		logger = slog.Default()
	}
	observer := observability.New(observability.Config{Service: service, Registry: registry, Logger: logger})
	return func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, streamer stdgrpc.Streamer, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		op := observer.Start("grpc_client_stream:"+method, "method", method)
		ctx, cancel := context.WithCancel(ctx)
		stream, err := streamer(ctx, desc, cc, method, opts...)
		wrapped := &observabilityClientStream{ClientStream: stream, ctx: ctx, cancel: cancel, operation: op}
		if err != nil {
			wrapped.finish(err)
			return nil, err
		}
		context.AfterFunc(ctx, func() { wrapped.finish(status.FromContextError(ctx.Err()).Err()) })
		return wrapped, nil
	}
}

type observabilityClientStream struct {
	stdgrpc.ClientStream
	ctx       context.Context
	operation *observability.Operation
	once      sync.Once
	cancel    context.CancelFunc
}

func (s *observabilityClientStream) RecvMsg(message any) error {
	err := s.ClientStream.RecvMsg(message)
	// The stream is complete only after a terminal receive error (normally EOF).
	// A nil result may still be followed by grpc-go's trailer receive.
	if err != nil {
		s.finish(err)
	}
	return err
}

func (s *observabilityClientStream) CloseSend() error {
	return s.ClientStream.CloseSend()
}

func (s *observabilityClientStream) SendMsg(message any) error {
	err := s.ClientStream.SendMsg(message)
	if err != nil && !errors.Is(err, io.EOF) {
		s.finish(err)
	}
	return err
}

func (s *observabilityClientStream) Header() (metadata.MD, error) {
	md, err := s.ClientStream.Header()
	if err != nil {
		s.finish(err)
	}
	return md, err
}

func (s *observabilityClientStream) finish(err error) {
	s.once.Do(func() {
		recordedErr := err
		if errors.Is(err, io.EOF) {
			recordedErr = nil
		}
		code := status.Code(recordedErr)
		s.operation.End(s.ctx, grpcStatusCode(code), recordedErr, "grpc client stream", "code", code.String())
		s.cancel()
	})
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

// TimeoutStreamClientInterceptor applies one deadline to the complete client
// stream lifecycle. CloseSend only half-closes the sending side; the deadline
// remains active until the final response or terminal receive error arrives.
func TimeoutStreamClientInterceptor(timeout time.Duration) stdgrpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, streamer stdgrpc.Streamer, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		if timeout <= 0 {
			return streamer(ctx, desc, cc, method, opts...)
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		stream, err := streamer(callCtx, desc, cc, method, opts...)
		if err != nil {
			cancel()
			return nil, err
		}
		wrapped := &timeoutClientStream{ClientStream: stream, cancel: cancel}
		context.AfterFunc(callCtx, wrapped.finish)
		return wrapped, nil
	}
}

type timeoutClientStream struct {
	stdgrpc.ClientStream
	cancel context.CancelFunc
	once   sync.Once
}

func (s *timeoutClientStream) CloseSend() error {
	return s.ClientStream.CloseSend()
}

func (s *timeoutClientStream) Header() (metadata.MD, error) {
	md, err := s.ClientStream.Header()
	if err != nil {
		s.finish()
	}
	return md, err
}

func (s *timeoutClientStream) RecvMsg(message any) error {
	err := s.ClientStream.RecvMsg(message)
	if err != nil {
		s.finish()
	}
	return err
}

func (s *timeoutClientStream) SendMsg(message any) error {
	err := s.ClientStream.SendMsg(message)
	// Send EOF requires RecvMsg to obtain the final response and status.
	if err != nil && !errors.Is(err, io.EOF) {
		s.finish()
	}
	return err
}

func (s *timeoutClientStream) finish() {
	s.once.Do(s.cancel)
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
