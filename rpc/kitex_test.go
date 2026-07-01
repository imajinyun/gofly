package rpc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/imajinyun/gofly/core/observability/metrics"
)

func TestKitexInterceptorMiddlewareChainsInDeclarationOrder(t *testing.T) {
	var order []string
	first := func(ctx context.Context, req any, next KitexEndpoint) (any, error) {
		order = append(order, "first-before")
		resp, err := next(ctx, req)
		order = append(order, "first-after")
		return resp, err
	}
	second := func(ctx context.Context, req any, next KitexEndpoint) (any, error) {
		order = append(order, "second-before")
		resp, err := next(ctx, req)
		order = append(order, "second-after")
		return resp, err
	}
	ep := KitexInterceptorMiddleware(first, nil, second)(func(ctx context.Context, req any) (any, error) {
		order = append(order, "handler")
		return "ok", nil
	})

	resp, err := ep(context.Background(), "request")
	if err != nil || resp != "ok" {
		t.Fatalf("endpoint response/error = %v/%v, want ok/nil", resp, err)
	}
	want := []string{"first-before", "second-before", "handler", "second-after", "first-after"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("interceptor order = %v, want %v", order, want)
	}
}

func TestKitexEndpointChainAndOptions(t *testing.T) {
	var order []string
	first := func(next KitexEndpoint) KitexEndpoint {
		return func(ctx context.Context, req any) (any, error) {
			order = append(order, "first-before")
			resp, err := next(ctx, req)
			order = append(order, "first-after")
			return resp, err
		}
	}
	second := func(next KitexEndpoint) KitexEndpoint {
		return func(ctx context.Context, req any) (any, error) {
			order = append(order, "second-before")
			resp, err := next(ctx, req)
			order = append(order, "second-after")
			return resp, err
		}
	}
	ep := KitexEndpointChain(first, second)(func(context.Context, any) (any, error) {
		order = append(order, "handler")
		return "ok", nil
	})
	if got, err := ep(context.Background(), "req"); err != nil || got != "ok" {
		t.Fatalf("KitexEndpointChain result = %v/%v, want ok/nil", got, err)
	}
	if want := []string{"first-before", "second-before", "handler", "second-after", "first-after"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("KitexEndpointChain order = %v, want %v", order, want)
	}

	serverInterceptorCalled := false
	var serverOpts serverOptions
	WithKitexServerInterceptors(func(ctx context.Context, req any, next KitexEndpoint) (any, error) {
		serverInterceptorCalled = true
		return next(ctx, req)
	})(&serverOpts)
	if len(serverOpts.middlewares) != 1 {
		t.Fatalf("server middleware count = %d, want 1", len(serverOpts.middlewares))
	}
	serverEndpoint := serverOpts.middlewares[0](func(context.Context, any) (any, error) { return "server", nil })
	if got, err := serverEndpoint(context.Background(), "req"); err != nil || got != "server" || !serverInterceptorCalled {
		t.Fatalf("server interceptor result = %v/%v called=%t, want server/nil/true", got, err, serverInterceptorCalled)
	}

	clientInterceptorCalled := false
	var clientOpts clientOptions
	WithKitexClientInterceptors(func(ctx context.Context, req any, next KitexEndpoint) (any, error) {
		clientInterceptorCalled = true
		return next(ctx, req)
	})(&clientOpts)
	if len(clientOpts.middlewares) != 1 {
		t.Fatalf("client middleware count = %d, want 1", len(clientOpts.middlewares))
	}
	clientEndpoint := clientOpts.middlewares[0](func(context.Context, any) (any, error) { return "client", nil })
	if got, err := clientEndpoint(context.Background(), "req"); err != nil || got != "client" || !clientInterceptorCalled {
		t.Fatalf("client interceptor result = %v/%v called=%t, want client/nil/true", got, err, clientInterceptorCalled)
	}
}

func TestKitexObservabilityInterceptorRecordsMetricsAndErrors(t *testing.T) {
	reg := metrics.NewRegistry()
	interceptor := KitexObservabilityInterceptor("orders.kitex", reg, nil)
	wantErr := NewError(CodeUnavailable, "downstream unavailable")
	ep := KitexInterceptorMiddleware(interceptor)(func(ctx context.Context, req any) (any, error) {
		return nil, wantErr
	})

	resp, err := ep(context.Background(), "request")
	if resp != nil || !errors.Is(err, wantErr) {
		t.Fatalf("endpoint response/error = %v/%v, want nil/%v", resp, err, wantErr)
	}
	snapshot := reg.Snapshot()
	if snapshot.Requests != 1 || snapshot.Errors != 1 || snapshot.InFlight != 0 || snapshot.Statuses[httpStatusFromCode(CodeUnavailable)] != 1 {
		t.Fatalf("metrics snapshot = %+v, want one unavailable request and no in-flight leak", snapshot)
	}
	if snapshot.Routes["orders.kitex"].Requests != 1 || snapshot.Routes["orders.kitex"].Errors != 1 {
		t.Fatalf("route metrics = %+v, want one errored kitex route", snapshot.Routes["orders.kitex"])
	}
}

func TestKitexObservabilityInterceptorRecordsSuccessWithDefaults(t *testing.T) {
	interceptor := KitexObservabilityInterceptor("", nil, nil)
	ep := KitexInterceptorMiddleware(interceptor)(func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})

	resp, err := ep(context.Background(), "request")
	if err != nil || resp != "ok" {
		t.Fatalf("endpoint response/error = %v/%v, want ok/nil", resp, err)
	}
}
