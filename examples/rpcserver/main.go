// Command rpcserver is a minimal, runnable gofly RPC service.
package main

import (
	"context"
	"fmt"

	"github.com/imajinyun/gofly/app"
	"github.com/imajinyun/gofly/rpc"
)

type helloReq struct {
	Name string `json:"name,omitempty"`
}

type helloResp struct {
	Message string `json:"message,omitempty"`
}

type greeter struct{}

func (greeter) SayHello(ctx context.Context, req *helloReq) (*helloResp, error) {
	name := "world"
	if req != nil && req.Name != "" {
		name = req.Name
	}
	return &helloResp{Message: "hello " + name}, nil
}

func main() {
	conf := app.DefaultServiceConf("greeter-rpc")
	server := rpc.NewServer(append([]rpc.ServerOption{
		rpc.WithAddress(":8081"),
	}, conf.RPCServerOptions()...)...)
	if err := server.RegisterService(greeterServiceDesc(greeter{}), nil); err != nil {
		panic(err)
	}
	if err := app.RunService(context.Background(), conf, server); err != nil {
		panic(err)
	}
}

func greeterServiceDesc(impl greeter) rpc.ServiceDesc {
	return rpc.ServiceDesc{
		Name:    "examples.greeter.Greeter",
		Version: "v1",
		Methods: []rpc.MethodDesc{{
			Name:       "SayHello",
			NewRequest: func() any { return new(helloReq) },
			Metadata:   map[string]string{"request": "helloReq", "response": "helloResp"},
			Handler: func(ctx context.Context, req any) (any, error) {
				typed, ok := req.(*helloReq)
				if !ok {
					return nil, fmt.Errorf("unexpected request type %T", req)
				}
				return impl.SayHello(ctx, typed)
			},
		}},
	}
}
