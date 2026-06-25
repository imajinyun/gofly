package rpc_test

import (
	"context"
	"fmt"

	"github.com/imajinyun/gofly/rpc"
	"github.com/imajinyun/gofly/rpc/endpoint"
)

// ExampleNewServer demonstrates creating an RPC server, registering a service,
// and inspecting the registered method descriptors.
func ExampleNewServer() {
	greetHandler := rpc.Handler(func(ctx context.Context, req any) (any, error) {
		return map[string]string{"reply": "hello " + req.(string)}, nil
	})

	desc := rpc.ServiceDesc{
		Name:    "Greeter",
		Version: "1.0.0",
		Methods: []rpc.MethodDesc{
			{
				Name:    "SayHello",
				Handler: greetHandler,
				NewRequest: func() any {
					return ""
				},
				Request: "string",
			},
		},
	}

	srv := rpc.NewServer(rpc.WithAddress(":9090"))
	if err := srv.RegisterService(desc, nil); err != nil {
		fmt.Println("register error:", err)
		return
	}

	infos := srv.GetServiceInfos()
	for name := range infos {
		fmt.Println(name)
	}
	// Output:
	// Greeter
}

// ExampleChain demonstrates composing RPC middlewares with endpoint.Chain.
// The first middleware becomes the outermost wrapper.
func ExampleChain() {
	// Two middlewares: one adds a header, one adds a footer annotation.
	header := func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			fmt.Println("header: before")
			resp, err := next(ctx, req)
			fmt.Println("header: after")
			return resp, err
		}
	}

	footer := func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, req any) (any, error) {
			fmt.Println("footer: before")
			resp, err := next(ctx, req)
			fmt.Println("footer: after")
			return resp, err
		}
	}

	ep := endpoint.Chain(header, footer)(func(ctx context.Context, req any) (any, error) {
		fmt.Println("endpoint")
		return "result:" + req.(string), nil
	})

	resp, err := ep(context.Background(), "ok")
	fmt.Println(resp, err)
	// Output:
	// header: before
	// footer: before
	// endpoint
	// footer: after
	// header: after
	// result:ok <nil>
}

// ExampleMiddleware shows how to apply governance middlewares when creating
// a server.
func ExampleMiddleware() {
	srv := rpc.NewServer(
		rpc.WithAddress(":9091"),
		rpc.WithServerMiddleware(endpoint.Chain(
			rpc.RecoverMiddleware(),
		)),
	)
	infos := srv.GetServiceInfos()
	fmt.Println(len(infos))
	// Output:
	// 0
}
