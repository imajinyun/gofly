package rpc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gofly/gofly/core/observability/metrics"
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
