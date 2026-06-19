package endpoint

import (
	"context"
	"reflect"
	"testing"
)

func TestChainComposesInDeclarationOrder(t *testing.T) {
	var order []string
	mw := func(name string) Middleware {
		return func(next Endpoint) Endpoint {
			return func(ctx context.Context, req any) (any, error) {
				order = append(order, name+":before")
				resp, err := next(ctx, req)
				order = append(order, name+":after")
				return resp, err
			}
		}
	}
	ep := Chain(mw("a"), mw("b"))(func(context.Context, any) (any, error) {
		order = append(order, "endpoint")
		return "ok", nil
	})

	resp, err := ep(context.Background(), nil)
	if err != nil {
		t.Fatalf("endpoint returned error: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("response = %v, want ok", resp)
	}
	want := []string{"a:before", "b:before", "endpoint", "b:after", "a:after"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestChainWithoutMiddlewaresReturnsEndpoint(t *testing.T) {
	ep := Chain()(func(context.Context, any) (any, error) { return "ok", nil })
	resp, err := ep(context.Background(), nil)
	if err != nil || resp != "ok" {
		t.Fatalf("resp=%v err=%v, want ok nil", resp, err)
	}
}
