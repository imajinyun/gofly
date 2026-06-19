// Package grpc provides gRPC server and client wrappers with governance,
// authentication, observability and OpenTelemetry tracing.
package grpc

import (
	"context"

	"github.com/gofly/gofly/core/auth"
	coreerrors "github.com/gofly/gofly/core/errors"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// AuthConfig configures authentication and RBAC authorization for gRPC servers.
//
// Validator authenticates the bearer token carried in the "authorization"
// metadata and returns a context holding the resulting Principal. Requirements
// maps a full method ("/pkg.Service/Method") to the authorization rule applied
// after authentication. Methods absent from Requirements still require a valid
// principal when RequireAuthentication is true.
type AuthConfig struct {
	Validator             auth.Validator
	Requirements          map[string]auth.Requirement
	RequireAuthentication bool
	// SkipMethods lists full methods that bypass authentication entirely (e.g.
	// health checks).
	SkipMethods []string
}

func (c AuthConfig) skip(method string) bool {
	for _, m := range c.SkipMethods {
		if m == method {
			return true
		}
	}
	return false
}

func (c AuthConfig) authenticate(ctx context.Context, method string) (context.Context, error) {
	if c.skip(method) {
		return ctx, nil
	}
	token := bearerFromIncoming(ctx)
	if token == "" {
		if c.RequireAuthentication || hasRequirement(c.Requirements, method) {
			return ctx, coreerrors.New(coreerrors.CodeUnauthenticated, "missing credentials")
		}
		return ctx, nil
	}
	if c.Validator != nil {
		next, err := c.Validator(ctx, token)
		if err != nil {
			return ctx, coreerrors.New(coreerrors.CodeUnauthenticated, err.Error())
		}
		ctx = next
	}
	if req, ok := c.Requirements[method]; ok {
		if err := auth.Authorize(ctx, req); err != nil {
			return ctx, coreerrors.New(coreerrors.CodePermissionDenied, err.Error())
		}
	}
	return ctx, nil
}

// AuthUnaryServerInterceptor authenticates and authorizes unary RPCs.
func AuthUnaryServerInterceptor(conf AuthConfig) stdgrpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *stdgrpc.UnaryServerInfo, handler stdgrpc.UnaryHandler) (any, error) {
		next, err := conf.authenticate(ctx, info.FullMethod)
		if err != nil {
			return nil, normalizeGRPCError(err)
		}
		return handler(next, req)
	}
}

// AuthStreamServerInterceptor authenticates and authorizes streaming RPCs.
func AuthStreamServerInterceptor(conf AuthConfig) stdgrpc.StreamServerInterceptor {
	return func(srv any, stream stdgrpc.ServerStream, info *stdgrpc.StreamServerInfo, handler stdgrpc.StreamHandler) error {
		next, err := conf.authenticate(stream.Context(), info.FullMethod)
		if err != nil {
			return normalizeGRPCError(err)
		}
		return handler(srv, &authServerStream{ServerStream: stream, ctx: next})
	}
}

type authServerStream struct {
	stdgrpc.ServerStream
	ctx context.Context
}

func (s *authServerStream) Context() context.Context { return s.ctx }

func hasRequirement(reqs map[string]auth.Requirement, method string) bool {
	_, ok := reqs[method]
	return ok
}

func bearerFromIncoming(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get(auth.MetadataKey)
	if len(values) == 0 {
		return ""
	}
	token, ok := auth.ExtractBearer(values[0])
	if !ok {
		return ""
	}
	return token
}
