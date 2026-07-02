# RPC Guide

gofly provides a lightweight HTTP RPC stack and gRPC integration paths for service-to-service communication.

## Minimal example

```sh
go run ./examples/rpcserver
```

The example registers `examples.greeter.Greeter/SayHello` and exposes a local RPC endpoint.

## RPC IDL matrix

Use the copyable RPC matrix when evaluating gofly against Kitex or go-zero IDL workflows:

```sh
go test -C examples/rpc-idl-matrix ./...
go run -C examples/rpc-idl-matrix .
```

The matrix includes standalone `contracts/greeter.proto` and `contracts/greeter.thrift` fixtures, a runtime descriptor, and a deterministic JSON report with:

- unary, server-streaming, client-streaming, and bidirectional streaming paths;
- unary middleware coverage for recovery, tracing, logging, timeout, retry, breaker, validation, and metrics;
- stream middleware coverage for recovery, tracing, logging, timeout, breaker, metrics, and request IDs;
- registry resolver updates;
- round-robin, weighted round-robin, P2C, consistent-hash, and health-aware load-balancing picks.

`make examples-smoke` validates the JSON shape so the matrix stays runnable as the RPC stack evolves.

## Smallest service

```go
server := rpc.NewServer(rpc.WithAddress(":8081"))
_ = server.RegisterService(rpc.ServiceDesc{
    Name: "greeter",
    Methods: []rpc.MethodDesc{{
        Name:       "SayHello",
        NewRequest: func() any { return new(helloRequest) },
        Handler: func(ctx context.Context, req any) (any, error) {
            return helloResponse{Message: "hello " + req.(*helloRequest).Name}, nil
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
