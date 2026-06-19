// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"

	coretrace "github.com/gofly/gofly/core/observability/trace"

	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const otelTracerName = "gofly"

// OTelUnaryServerInterceptor instruments unary RPCs handled by the server.
// It picks up a traceparent (or grpc-trace-bin) from incoming metadata and
// links it into a new server span.
func OTelUnaryServerInterceptor() stdgrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *stdgrpc.UnaryServerInfo, handler stdgrpc.UnaryHandler) (any, error) {
		if sc := oteltrace.SpanContextFromContext(ctx); !sc.IsValid() {
			if parent := traceFromIncoming(ctx); parent != "" {
				if ps, ok := coretrace.ParseTraceParent(parent); ok {
					if otelSc, ok := coretrace.ToOTel(ps); ok {
						ctx = oteltrace.ContextWithSpanContext(ctx, otelSc)
					}
				}
			}
		}
		ctx, span := oteltrace.SpanFromContext(ctx).TracerProvider().Tracer(otelTracerName).Start(
			ctx, info.FullMethod, oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		)
		defer span.End()
		resp, err := handler(ctx, req)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, err.Error())
		}
		return resp, err
	}
}

// OTelStreamServerInterceptor instruments server-side streaming RPCs.
func OTelStreamServerInterceptor() stdgrpc.StreamServerInterceptor {
	return func(srv any, ss stdgrpc.ServerStream, info *stdgrpc.StreamServerInfo, handler stdgrpc.StreamHandler) error {
		ctx := ss.Context()
		if sc := oteltrace.SpanContextFromContext(ctx); !sc.IsValid() {
			if parent := traceFromIncoming(ctx); parent != "" {
				if ps, ok := coretrace.ParseTraceParent(parent); ok {
					if otelSc, ok := coretrace.ToOTel(ps); ok {
						ctx = oteltrace.ContextWithSpanContext(ctx, otelSc)
					}
				}
			}
		}
		ctx, span := oteltrace.SpanFromContext(ctx).TracerProvider().Tracer(otelTracerName).Start(
			ctx, info.FullMethod, oteltrace.WithSpanKind(oteltrace.SpanKindServer),
		)
		defer span.End()
		ss = otelServerStream{ServerStream: ss, ctx: ctx}
		err := handler(srv, ss)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, err.Error())
		}
		return err
	}
}

// OTelUnaryClientInterceptor instruments outgoing unary RPCs from the client.
func OTelUnaryClientInterceptor() stdgrpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *stdgrpc.ClientConn, invoker stdgrpc.UnaryInvoker, opts ...stdgrpc.CallOption) error {
		ctx, span := oteltrace.SpanFromContext(ctx).TracerProvider().Tracer(otelTracerName).Start(
			ctx, method, oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		)
		defer span.End()
		ctx = injectTraceOutgoing(ctx)
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(otelcodes.Error, err.Error())
		}
		return err
	}
}

// OTelStreamClientInterceptor instruments outgoing streaming RPCs.
func OTelStreamClientInterceptor() stdgrpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *stdgrpc.StreamDesc, cc *stdgrpc.ClientConn, method string, streamer stdgrpc.Streamer, opts ...stdgrpc.CallOption) (stdgrpc.ClientStream, error) {
		ctx, span := oteltrace.SpanFromContext(ctx).TracerProvider().Tracer(otelTracerName).Start(
			ctx, method, oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		)
		ctx = injectTraceOutgoing(ctx)
		cs, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			span.End()
			return nil, err
		}
		return otelClientStream{ClientStream: cs, span: span}, nil
	}
}

// traceFromIncoming extracts a traceparent from incoming metadata.
func traceFromIncoming(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if v := md.Get("traceparent"); len(v) > 0 {
		return v[0]
	}
	if v := md.Get("grpc-trace-bin"); len(v) > 0 {
		return v[0]
	}
	return ""
}

// injectTraceOutgoing attaches a traceparent to the outgoing metadata so the
// next hop can continue the trace. Falls back to starting a new span context
// when the incoming context has none.
func injectTraceOutgoing(ctx context.Context) context.Context {
	if sc, ok := coretrace.FromContext(ctx); ok {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}
		md = metadata.Join(md, metadata.Pairs("traceparent", coretrace.TraceParent(sc)))
		return metadata.NewOutgoingContext(ctx, md)
	}
	if otelSc := oteltrace.SpanContextFromContext(ctx); otelSc.IsValid() {
		if sc, ok := coretrace.FromOTel(otelSc); ok {
			md, _ := metadata.FromOutgoingContext(ctx)
			if md == nil {
				md = metadata.New(nil)
			}
			md = metadata.Join(md, metadata.Pairs("traceparent", coretrace.TraceParent(sc)))
			return metadata.NewOutgoingContext(ctx, md)
		}
	}
	_, sc := coretrace.Start(ctx, "")
	md, _ := metadata.FromOutgoingContext(ctx)
	if md == nil {
		md = metadata.New(nil)
	}
	md = metadata.Join(md, metadata.Pairs("traceparent", coretrace.TraceParent(sc)))
	return metadata.NewOutgoingContext(ctx, md)
}

// otelServerStream overrides the stream's context so the handler sees the
// tracing context attached by the interceptor.
type otelServerStream struct {
	stdgrpc.ServerStream
	ctx context.Context
}

func (s otelServerStream) Context() context.Context { return s.ctx }

// otelClientStream wraps a client stream and attaches error annotations on
// sends/receives. It ends the overall client span when the stream closes.
type otelClientStream struct {
	stdgrpc.ClientStream
	span oteltrace.Span
}

func (s otelClientStream) Context() context.Context { return s.ClientStream.Context() }

func (s otelClientStream) SendMsg(m any) error {
	if err := s.ClientStream.SendMsg(m); err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(otelcodes.Error, err.Error())
		return err
	}
	return nil
}

func (s otelClientStream) RecvMsg(m any) error {
	if err := s.ClientStream.RecvMsg(m); err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(otelcodes.Error, err.Error())
		return err
	}
	return nil
}

func (s otelClientStream) CloseSend() error {
	if err := s.ClientStream.CloseSend(); err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(otelcodes.Error, err.Error())
		return err
	}
	return nil
}

func (s otelClientStream) Header() (metadata.MD, error) {
	md, err := s.ClientStream.Header()
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(otelcodes.Error, err.Error())
	}
	return md, err
}

func (s otelClientStream) Trailer() metadata.MD { return s.ClientStream.Trailer() }

func (s otelClientStream) Close() error {
	defer s.span.End()
	return nil
}
