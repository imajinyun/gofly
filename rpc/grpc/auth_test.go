package grpc

import (
	"context"
	"testing"

	"github.com/gofly/gofly/core/auth"

	stdgrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func incomingBearer(token string) context.Context {
	md := metadata.New(map[string]string{auth.MetadataKey: auth.BearerValue(token)})
	return metadata.NewIncomingContext(context.Background(), md)
}

func rbacValidator() auth.Validator {
	return func(ctx context.Context, token string) (context.Context, error) {
		switch token {
		case "admin":
			return auth.NewContext(ctx, auth.Principal{Subject: "admin", Roles: []string{"admin"}, Permissions: []string{"orders:*"}}), nil
		case "viewer":
			return auth.NewContext(ctx, auth.Principal{Subject: "viewer", Roles: []string{"viewer"}, Permissions: []string{"orders:read"}}), nil
		default:
			return ctx, auth.ErrInvalidCredentials
		}
	}
}

func TestAuthUnaryServerInterceptor(t *testing.T) {
	conf := AuthConfig{
		Validator: rbacValidator(),
		Requirements: map[string]auth.Requirement{
			"/orders.Orders/Write": auth.RequirePermissions("orders:write"),
		},
		RequireAuthentication: true,
		SkipMethods:           []string{"/grpc.health.v1.Health/Check"},
	}
	interceptor := AuthUnaryServerInterceptor(conf)

	call := func(ctx context.Context, method string) (auth.Principal, error) {
		info := &stdgrpc.UnaryServerInfo{FullMethod: method}
		var captured auth.Principal
		_, err := interceptor(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
			captured, _ = auth.FromContext(ctx)
			return nil, nil
		})
		return captured, err
	}

	// missing credentials -> unauthenticated
	if _, err := call(context.Background(), "/orders.Orders/Read"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing creds code = %s, want Unauthenticated", status.Code(err))
	}

	// invalid token -> unauthenticated
	if _, err := call(incomingBearer("bogus"), "/orders.Orders/Read"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("invalid token code = %s, want Unauthenticated", status.Code(err))
	}

	// authenticated, no specific requirement -> ok with principal
	p, err := call(incomingBearer("viewer"), "/orders.Orders/Read")
	if err != nil {
		t.Fatalf("viewer read err = %v", err)
	}
	if p.Subject != "viewer" {
		t.Fatalf("principal subject = %q, want viewer", p.Subject)
	}

	// viewer lacks orders:write -> permission denied
	if _, err := call(incomingBearer("viewer"), "/orders.Orders/Write"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("viewer write code = %s, want PermissionDenied", status.Code(err))
	}

	// admin has orders:* -> ok
	if _, err := call(incomingBearer("admin"), "/orders.Orders/Write"); err != nil {
		t.Fatalf("admin write err = %v", err)
	}

	// skipped method bypasses auth entirely
	if _, err := call(context.Background(), "/grpc.health.v1.Health/Check"); err != nil {
		t.Fatalf("health check err = %v", err)
	}
}

func TestAuthStreamServerInterceptor(t *testing.T) {
	conf := AuthConfig{
		Validator: rbacValidator(),
		Requirements: map[string]auth.Requirement{
			"/orders.Orders/Watch": auth.RequireRoles("admin"),
		},
		RequireAuthentication: true,
	}
	interceptor := AuthStreamServerInterceptor(conf)

	call := func(ctx context.Context, method string) error {
		info := &stdgrpc.StreamServerInfo{FullMethod: method}
		return interceptor(nil, testServerStream{ctx: ctx}, info, func(srv any, stream stdgrpc.ServerStream) error {
			if _, ok := auth.FromContext(stream.Context()); !ok {
				t.Error("stream context missing principal")
			}
			return nil
		})
	}

	if err := call(context.Background(), "/orders.Orders/Watch"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing creds code = %s, want Unauthenticated", status.Code(err))
	}
	if err := call(incomingBearer("viewer"), "/orders.Orders/Watch"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("viewer watch code = %s, want PermissionDenied", status.Code(err))
	}
	if err := call(incomingBearer("admin"), "/orders.Orders/Watch"); err != nil {
		t.Fatalf("admin watch err = %v", err)
	}
}

func TestAuthOptionalWhenNotRequired(t *testing.T) {
	conf := AuthConfig{Validator: rbacValidator()}
	interceptor := AuthUnaryServerInterceptor(conf)

	called := false
	info := &stdgrpc.UnaryServerInfo{FullMethod: "/orders.Orders/Read"}
	_, err := interceptor(context.Background(), nil, info, func(ctx context.Context, req any) (any, error) {
		called = true
		if _, ok := auth.FromContext(ctx); ok {
			t.Error("unexpected principal for anonymous request")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("anonymous request err = %v", err)
	}
	if !called {
		t.Fatal("handler not invoked for optional auth")
	}
}
