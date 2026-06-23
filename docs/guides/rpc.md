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

## Policy precedence

HTTP RPC clients resolve retry, hedge, fallback, breaker, balancer, timeout,
metadata, and header policy in this order:

1. `client_default`
2. `governance_rule`
3. `dynamic_policy`
4. `method_policy`

Method policies may be keyed by `service/method`, `/service/method`, the raw
method string, or the method name. Runtime diagnostics expose the matched method
key and final effective policy so operators can explain why a request used a
specific retry, fallback, hedge, or breaker configuration.

## Verification

- run the server;
- call it from a local client or benchmark;
- verify discovery registration and admin metadata.

For gRPC-oriented comparisons, see [Kitex migration](../comparisons/kitex.md).
