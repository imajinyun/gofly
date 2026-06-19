// Package rpc provides a gRPC-compatible RPC server and client with
// governance, discovery, load balancing and streaming support.
package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/gofly/gofly/core/auth"
	"github.com/gofly/gofly/core/breaker"
	"github.com/gofly/gofly/core/limit"
	"github.com/gofly/gofly/core/metadata"
	"github.com/gofly/gofly/core/observability/metrics"
	"github.com/gofly/gofly/core/observability"
	"github.com/gofly/gofly/core/observability/trace"
	"github.com/gofly/gofly/rpc/endpoint"
)

func RecoverMiddleware() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (resp any, err error) {
			defer func() {
				if v := recover(); v != nil {
					slog.ErrorContext(ctx, "rpc panic recovered", "panic", v, "stack", string(debug.Stack()))
					err = Errorf(CodeInternal, "panic recovered: %v", v)
				}
			}()
			return next(ctx, req)
		}
	}
}

func StreamRecoverMiddleware() StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, stream *Stream) (err error) {
			defer func() {
				if v := recover(); v != nil {
					slog.ErrorContext(ctx, "rpc stream panic recovered", "panic", v, "stack", string(debug.Stack()))
					err = Errorf(CodeInternal, "panic recovered: %v", v)
				}
			}()
			return next(ctx, stream)
		}
	}
}

func TraceMiddleware(service string) endpoint.Middleware {
	return TraceMiddlewareWithSampler(service, nil)
}

func TraceMiddlewareWithSampler(service string, sampler trace.Sampler) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromContext(ctx)
			ctx, _ = observability.StartTrace(ctx, md.Get(trace.TraceParentHeader), service, sampler)
			return next(ctx, req)
		}
	}
}

func StreamTraceMiddleware(service string) StreamMiddleware {
	return StreamTraceMiddlewareWithSampler(service, nil)
}

func StreamTraceMiddlewareWithSampler(service string, sampler trace.Sampler) StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, stream *Stream) error {
			md, _ := metadata.FromContext(ctx)
			ctx, _ = observability.StartTrace(ctx, md.Get(trace.TraceParentHeader), service, sampler)
			return next(ctx, stream)
		}
	}
}

func ClientStreamTraceMiddleware(service string) ClientStreamMiddleware {
	return ClientStreamTraceMiddlewareWithSampler(service, nil)
}

func ClientStreamTraceMiddlewareWithSampler(service string, sampler trace.Sampler) ClientStreamMiddleware {
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			md, _ := metadata.FromContext(ctx)
			ctx, _ = observability.StartTrace(ctx, md.Get(trace.TraceParentHeader), service, sampler)
			return next(ctx, method)
		}
	}
}

func RequestIDMiddleware() endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if metadata.RequestIDFromContext(ctx) == "" {
				ctx = metadata.Append(ctx, metadata.RequestIDKey, metadata.NewRequestID())
			}
			return next(ctx, req)
		}
	}
}

func StreamRequestIDMiddleware() StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, stream *Stream) error {
			if metadata.RequestIDFromContext(ctx) == "" {
				ctx = metadata.Append(ctx, metadata.RequestIDKey, metadata.NewRequestID())
			}
			return next(ctx, stream)
		}
	}
}

func ClientStreamRequestIDMiddleware() ClientStreamMiddleware {
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			if metadata.RequestIDFromContext(ctx) == "" {
				ctx = metadata.Append(ctx, metadata.RequestIDKey, metadata.NewRequestID())
			}
			return next(ctx, method)
		}
	}
}

func TimeoutMiddleware(timeout time.Duration) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if timeout <= 0 {
				return next(ctx, req)
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			resp, err := next(ctx, req)
			if ctx.Err() != nil {
				return nil, NewError(CodeDeadlineExceeded, ctx.Err().Error())
			}
			return resp, err
		}
	}
}

func LoggingMiddleware(name string) endpoint.Middleware {
	return LoggingMiddlewareWithSampler(name, nil)
}

func LoggingMiddlewareWithSampler(name string, sampler trace.Sampler) endpoint.Middleware {
	if name == "" {
		name = "rpc"
	}
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			attrs := []any{"name", name, "code", CodeOf(err), "duration", time.Since(start)}
			attrs = append(attrs, observability.TraceAttrs(ctx)...)
			if err != nil {
				attrs = append(attrs, "error", fmt.Sprint(err))
				slog.WarnContext(ctx, "rpc call", attrs...)
				return resp, err
			}
			if observability.ShouldLog(ctx, sampler, 200) {
				slog.InfoContext(ctx, "rpc call", attrs...)
			}
			return resp, err
		}
	}
}

func StreamLoggingMiddleware(name string) StreamMiddleware {
	return StreamLoggingMiddlewareWithSampler(name, nil)
}

func StreamLoggingMiddlewareWithSampler(name string, sampler trace.Sampler) StreamMiddleware {
	if name == "" {
		name = "rpc"
	}
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, stream *Stream) error {
			start := time.Now()
			err := next(ctx, stream)
			attrs := []any{"name", name, "code", CodeOf(err), "duration", time.Since(start)}
			attrs = append(attrs, observability.TraceAttrs(ctx)...)
			if err != nil {
				attrs = append(attrs, "error", fmt.Sprint(err))
				slog.WarnContext(ctx, "rpc stream", attrs...)
				return err
			}
			if observability.ShouldLog(ctx, sampler, 200) {
				slog.InfoContext(ctx, "rpc stream", attrs...)
			}
			return nil
		}
	}
}

func ClientStreamLoggingMiddleware(name string) ClientStreamMiddleware {
	return ClientStreamLoggingMiddlewareWithSampler(name, nil)
}

func ClientStreamLoggingMiddlewareWithSampler(name string, sampler trace.Sampler) ClientStreamMiddleware {
	if name == "" {
		name = "rpc"
	}
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			start := time.Now()
			stream, err := next(ctx, method)
			if err != nil {
				attrs := []any{"name", name, "method", method, "code", CodeOf(err), "duration", time.Since(start), "error", fmt.Sprint(err)}
				attrs = append(attrs, observability.TraceAttrs(ctx)...)
				slog.WarnContext(ctx, "rpc client stream", attrs...)
				return stream, err
			}
			stream.onClose(func() {
				attrs := []any{"name", name, "method", method, "code", CodeOK, "duration", time.Since(start)}
				attrs = append(attrs, observability.TraceAttrs(ctx)...)
				if observability.ShouldLog(ctx, sampler, 200) {
					slog.InfoContext(ctx, "rpc client stream", attrs...)
				}
			})
			return stream, nil
		}
	}
}

func MetricsMiddleware(name string, reg *metrics.Registry) endpoint.Middleware {
	if name == "" {
		name = "rpc"
	}
	if reg == nil {
		reg = metrics.Default
	}
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			start := time.Now()
			reg.IncInFlight()
			defer reg.DecInFlight()
			resp, err := next(ctx, req)
			status := httpStatusFromCode(CodeOf(err))
			if err == nil {
				status = 200
			}
			observability.Record(reg, name, status, time.Since(start))
			return resp, err
		}
	}
}

func StreamMetricsMiddleware(name string, reg *metrics.Registry) StreamMiddleware {
	if name == "" {
		name = "rpc"
	}
	if reg == nil {
		reg = metrics.Default
	}
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, stream *Stream) error {
			start := time.Now()
			reg.IncInFlight()
			defer reg.DecInFlight()
			err := next(ctx, stream)
			status := httpStatusFromCode(CodeOf(err))
			if err == nil {
				status = 200
			}
			observability.Record(reg, name, status, time.Since(start))
			return err
		}
	}
}

func ClientStreamMetricsMiddleware(name string, reg *metrics.Registry) ClientStreamMiddleware {
	if name == "" {
		name = "rpc"
	}
	if reg == nil {
		reg = metrics.Default
	}
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			start := time.Now()
			reg.IncInFlight()
			stream, err := next(ctx, method)
			if err != nil {
				reg.DecInFlight()
				status := httpStatusFromCode(CodeOf(err))
				observability.Record(reg, name, status, time.Since(start))
				return stream, err
			}
			stream.onClose(func() {
				reg.DecInFlight()
				observability.Record(reg, name, 200, time.Since(start))
			})
			return stream, nil
		}
	}
}

func MaxConcurrencyMiddleware(max int) endpoint.Middleware {
	limiter := limit.NewConcurrency(max)
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if !limiter.TryAcquire() {
				return nil, NewError(CodeUnavailable, "too many concurrent requests")
			}
			defer limiter.Release()
			return next(ctx, req)
		}
	}
}

func AdaptiveLimitMiddleware(limiter *limit.AdaptiveLimiter) endpoint.Middleware {
	if limiter == nil {
		limiter = limit.NewAdaptiveLimiter()
	}
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			token, err := limiter.Allow()
			if err != nil {
				return nil, NewError(CodeResourceExhausted, err.Error())
			}
			resp, err := next(ctx, req)
			token.Done(err == nil)
			return resp, err
		}
	}
}

func ClientStreamMaxConcurrencyMiddleware(max int) ClientStreamMiddleware {
	limiter := limit.NewConcurrency(max)
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			if !limiter.TryAcquire() {
				return nil, NewError(CodeUnavailable, "too many concurrent streams")
			}
			stream, err := next(ctx, method)
			if err != nil {
				limiter.Release()
				return nil, err
			}
			stream.onClose(limiter.Release)
			return stream, nil
		}
	}
}

func ClientStreamAdaptiveLimitMiddleware(limiter *limit.AdaptiveLimiter) ClientStreamMiddleware {
	if limiter == nil {
		limiter = limit.NewAdaptiveLimiter()
	}
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			token, err := limiter.Allow()
			if err != nil {
				return nil, NewError(CodeResourceExhausted, err.Error())
			}
			stream, err := next(ctx, method)
			if err != nil {
				token.Done(false)
				return nil, err
			}
			stream.onClose(func() { token.Done(true) })
			return stream, nil
		}
	}
}

func AdaptiveBreakerMiddleware(brk *breaker.AdaptiveBreaker) endpoint.Middleware {
	if brk == nil {
		brk = breaker.NewAdaptive()
	}
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if err := brk.Allow(); err != nil {
				return nil, NewError(CodeUnavailable, err.Error())
			}
			resp, err := next(ctx, req)
			if err != nil {
				brk.MarkFailure()
				return resp, err
			}
			brk.MarkSuccess()
			return resp, nil
		}
	}
}

func ClientStreamBreakerMiddleware(brk *breaker.Breaker) ClientStreamMiddleware {
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			if brk == nil {
				return next(ctx, method)
			}
			var stream *Stream
			err := brk.Do(ctx, func() error {
				var err error
				stream, err = next(ctx, method)
				return err
			})
			if errors.Is(err, breaker.ErrOpen) {
				return nil, NewError(CodeUnavailable, err.Error())
			}
			return stream, err
		}
	}
}

func ClientStreamAdaptiveBreakerMiddleware(brk *breaker.AdaptiveBreaker) ClientStreamMiddleware {
	if brk == nil {
		brk = breaker.NewAdaptive()
	}
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			if err := brk.Allow(); err != nil {
				return nil, NewError(CodeUnavailable, err.Error())
			}
			stream, err := next(ctx, method)
			if err != nil {
				brk.MarkFailure()
				return nil, err
			}
			stream.onClose(brk.MarkSuccess)
			return stream, nil
		}
	}
}

func ServerAuthMiddleware(validator auth.Validator) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if validator == nil {
				return nil, NewError(CodeInternal, "auth validator is required")
			}
			md, _ := metadata.FromContext(ctx)
			token, ok := auth.ExtractBearer(md.Get(auth.MetadataKey))
			if !ok {
				token, ok = auth.ExtractBearer(md.Get(auth.AuthorizationHeader))
			}
			if !ok {
				return nil, NewError(CodeUnauthenticated, auth.ErrMissingCredentials.Error())
			}
			ctx, err := validator(ctx, token)
			if err != nil {
				return nil, NewError(CodeUnauthenticated, auth.ErrInvalidCredentials.Error())
			}
			return next(ctx, req)
		}
	}
}

func StreamServerAuthMiddleware(validator auth.Validator) StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, stream *Stream) error {
			if validator == nil {
				return NewError(CodeInternal, "auth validator is required")
			}
			md, _ := metadata.FromContext(ctx)
			token, ok := auth.ExtractBearer(md.Get(auth.MetadataKey))
			if !ok {
				token, ok = auth.ExtractBearer(md.Get(auth.AuthorizationHeader))
			}
			if !ok {
				return NewError(CodeUnauthenticated, auth.ErrMissingCredentials.Error())
			}
			ctx, err := validator(ctx, token)
			if err != nil {
				return NewError(CodeUnauthenticated, auth.ErrInvalidCredentials.Error())
			}
			return next(ctx, stream)
		}
	}
}

func ClientBearerTokenMiddleware(token string) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			if token != "" {
				ctx = metadata.Append(ctx, auth.MetadataKey, auth.BearerValue(token))
			}
			return next(ctx, req)
		}
	}
}

func ClientStreamBearerTokenMiddleware(token string) ClientStreamMiddleware {
	return func(next ClientStreamHandler) ClientStreamHandler {
		return func(ctx context.Context, method string) (*Stream, error) {
			if token != "" {
				ctx = metadata.Append(ctx, auth.MetadataKey, auth.BearerValue(token))
			}
			return next(ctx, method)
		}
	}
}

type GovernanceConfig struct {
	Recover        bool
	RequestID      bool
	Trace          bool
	Log            bool
	Metrics        bool
	Timeout        time.Duration
	TimeoutConfig  RPCTimeoutConfig
	Breaker        bool
	MaxConcurrency int
	AdaptiveLimit  bool
	ServerAdaptive *limit.AdaptiveLimiter
	ClientAdaptive *limit.AdaptiveLimiter
	ServerBreaker  *breaker.AdaptiveBreaker
	ClientBreaker  *breaker.AdaptiveBreaker
	TraceSampler   trace.Sampler
	LogSampler     trace.Sampler
	ServerAuth     auth.Validator
	ClientToken    string
}

type RPCTimeoutConfig struct {
	Server time.Duration
	Client time.Duration
}

func DefaultGovernanceConfig(timeout time.Duration) GovernanceConfig {
	return GovernanceConfig{
		Recover:   true,
		RequestID: true,
		Trace:     true,
		Log:       true,
		Metrics:   true,
		Timeout:   timeout,
	}
}

func GovernanceSuite(name string, conf GovernanceConfig) Suite {
	server := make([]ServerOption, 0, 8)
	client := make([]ClientOption, 0, 8)
	if conf.Recover {
		server = append(server, WithServerMiddleware(RecoverMiddleware()))
		server = append(server, WithServerStreamMiddleware(StreamRecoverMiddleware()))
	}
	if conf.RequestID {
		server = append(server, WithServerMiddleware(RequestIDMiddleware()))
		server = append(server, WithServerStreamMiddleware(StreamRequestIDMiddleware()))
		client = append(client, WithClientMiddleware(RequestIDMiddleware()))
		client = append(client, WithClientStreamMiddleware(ClientStreamRequestIDMiddleware()))
	}
	if conf.Trace {
		server = append(server, WithServerMiddleware(TraceMiddlewareWithSampler(name+".server", conf.TraceSampler)))
		server = append(server, WithServerStreamMiddleware(StreamTraceMiddlewareWithSampler(name+".server", conf.TraceSampler)))
		client = append(client, WithClientMiddleware(TraceMiddlewareWithSampler(name+".client", conf.TraceSampler)))
		client = append(client, WithClientStreamMiddleware(ClientStreamTraceMiddlewareWithSampler(name+".client", conf.TraceSampler)))
	}
	serverTimeout, clientTimeout := rpcTimeouts(conf)
	if serverTimeout > 0 {
		server = append(server, WithServerMiddleware(TimeoutMiddleware(serverTimeout)))
	}
	if clientTimeout > 0 {
		client = append(client, WithClientMiddleware(TimeoutMiddleware(clientTimeout)))
		client = append(client, WithClientStreamTimeout(clientTimeout))
	}
	if conf.Metrics {
		server = append(server, WithServerMiddleware(MetricsMiddleware(name+".server", nil)))
		server = append(server, WithServerStreamMiddleware(StreamMetricsMiddleware(name+".server", nil)))
		client = append(client, WithClientMiddleware(MetricsMiddleware(name+".client", nil)))
		client = append(client, WithClientStreamMiddleware(ClientStreamMetricsMiddleware(name+".client", nil)))
	}
	if conf.Breaker || conf.ServerBreaker != nil {
		server = append(server, WithServerAdaptiveBreaker(conf.ServerBreaker))
	}
	if conf.Breaker || conf.ClientBreaker != nil {
		clientBreaker := conf.ClientBreaker
		if clientBreaker == nil {
			clientBreaker = breaker.NewAdaptive()
		}
		client = append(client, WithAdaptiveBreaker(clientBreaker))
	}
	if conf.MaxConcurrency > 0 {
		server = append(server, WithServerMaxConcurrency(conf.MaxConcurrency))
		client = append(client, WithClientMaxConcurrency(conf.MaxConcurrency))
	}
	if conf.AdaptiveLimit || conf.ServerAdaptive != nil {
		server = append(server, WithServerAdaptiveLimiter(conf.ServerAdaptive))
	}
	if conf.AdaptiveLimit || conf.ClientAdaptive != nil {
		client = append(client, WithClientAdaptiveLimiter(conf.ClientAdaptive))
	}
	if conf.ServerAuth != nil {
		server = append(server, WithServerMiddleware(ServerAuthMiddleware(conf.ServerAuth)))
		server = append(server, WithServerStreamMiddleware(StreamServerAuthMiddleware(conf.ServerAuth)))
	}
	if conf.ClientToken != "" {
		client = append(client, WithClientMiddleware(ClientBearerTokenMiddleware(conf.ClientToken)))
		client = append(client, WithClientStreamMiddleware(ClientStreamBearerTokenMiddleware(conf.ClientToken)))
	}
	if conf.Log {
		server = append(server, WithServerMiddleware(LoggingMiddlewareWithSampler(name+".server", conf.LogSampler)))
		server = append(server, WithServerStreamMiddleware(StreamLoggingMiddlewareWithSampler(name+".server", conf.LogSampler)))
		client = append(client, WithClientMiddleware(LoggingMiddlewareWithSampler(name+".client", conf.LogSampler)))
		client = append(client, WithClientStreamMiddleware(ClientStreamLoggingMiddlewareWithSampler(name+".client", conf.LogSampler)))
	}
	return BasicSuite{Server: server, Client: client}
}

func rpcTimeouts(conf GovernanceConfig) (server time.Duration, client time.Duration) {
	server = conf.TimeoutConfig.Server
	if server <= 0 {
		server = conf.Timeout
	}
	client = conf.TimeoutConfig.Client
	if client <= 0 {
		client = conf.Timeout
	}
	return server, client
}

func ObservabilitySuite(name string, timeout time.Duration) Suite {
	return GovernanceSuite(name, GovernanceConfig{Recover: true, Trace: true, Log: true, Metrics: true, Timeout: timeout})
}
