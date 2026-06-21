# RPC Guide

gofly provides a lightweight HTTP RPC stack and gRPC integration paths for service-to-service communication.

## Minimal example

```sh
go run ./examples/rpcserver
```

The example registers `examples.greeter.Greeter/SayHello` and exposes a local RPC endpoint.

## Smallest service

```go
server := rpc.NewServer(rpc.WithAddress(":8081"))
_ = server.RegisterService(rpc.ServiceDesc{
    Name: "greeter",
    Methods: []rpc.MethodDesc{{
        Name:       "SayHello",
        NewRequest: func() any { return new(helloReq) },
        Handler: func(ctx context.Context, req any) (any, error) {
            return helloResp{Message: "hello " + req.(*helloReq).Name}, nil
        },
    }},
}, nil)
_ = server.Start()
```

## Production configuration

| Concern | Config |
| --- | --- |
| Listen address | `server.rpc.port` |
| Discovery | `discovery.*` in service config |
| Governance | transport `rpc` rules in `etc/governance.json` |
| gRPC mode | use `rpc/grpc` when you need grpc-go compatibility |

## Verification

- run the server;
- call it from a local client or benchmark;
- verify discovery registration and admin metadata.

For gRPC-oriented comparisons, see [Kitex migration](../comparisons/kitex.md).
