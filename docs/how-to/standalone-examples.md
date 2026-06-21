# How-to: Use Standalone Examples

gofly examples are copyable modules. Use them when you want a runnable slice before committing to a generated production service.

## Run all examples

```sh
make examples-smoke
```

This verifies every example module has its own `go.mod`, builds each module, runs tests, and checks machine-readable outputs for the microshop and AI-governed examples.

## Copy an example out of the repository

```sh
cp -R examples/production-orders /tmp/orders-demo
cd /tmp/orders-demo
go mod edit -dropreplace github.com/gofly/gofly
go get github.com/gofly/gofly@latest
go test ./...
```

Keep the local `replace` directive only when developing inside the gofly repository.

## Choose the right example

| Need | Example |
| --- | --- |
| Basic REST with OpenAPI and health | `examples/restserver` |
| Lightweight RPC server/client | `examples/rpcserver` |
| REST + RPC + discovery + MQ + saga | `examples/production-orders` |
| Multi-service topology and gateway | `examples/microshop` |
| AI-readable runtime drift contract | `examples/ai-governed-service` |
| Metrics, trace, health, admin surface | `examples/observability` |

See the [examples catalog](../../examples/README.md) for every command and port.
