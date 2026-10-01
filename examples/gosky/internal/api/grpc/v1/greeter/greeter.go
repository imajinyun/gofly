package greeterrpc

import (
	"context"

	"github.com/imajinyun/gofly/examples/gosky/internal/svc"
	"github.com/imajinyun/gofly/rpc"
)

type SayHelloRequest struct {
	Name string `json:"name"`
}

type SayHelloResponse struct {
	Message string `json:"message"`
}

const sayHelloAuthorizationResource = "rpc:greeter/SayHello"

func GreeterService(svcCtx *svc.ServiceContext) rpc.ServiceDesc {
	method := rpc.MethodDesc{
		Name:       "SayHello",
		NewRequest: func() any { return new(SayHelloRequest) },
		Request:    "SayHelloRequest",
		Response:   "SayHelloResponse",
		Handler: func(ctx context.Context, req any) (any, error) {
			in, ok := req.(*SayHelloRequest)
			if !ok || in == nil {
				return nil, rpc.NewError(rpc.CodeInvalidArgument, "unexpected request type for SayHello")
			}
			name := in.Name
			if name == "" {
				name = "world"
			}
			return SayHelloResponse{Message: "hello " + name}, nil
		},
	}
	if authorizer := svcCtx.CurrentAuthorizer(); authorizer != nil {
		method.Middlewares = append(method.Middlewares, authorizer.RPCMiddleware(sayHelloAuthorizationResource))
	}
	return rpc.ServiceDesc{
		Name:    "greeter",
		Methods: []rpc.MethodDesc{method},
	}
}
