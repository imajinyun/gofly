# Architecture

gofly is organized around generated services plus reusable runtime packages.

## Layers

```text
cmd/gofly/        CLI and generators
app/              lifecycle and service configuration
rest/             HTTP server, binding, middleware, OpenAPI
rpc/              HTTP RPC and gRPC integration
gateway/          routing, discovery, and REST-to-RPC transcoding
core/             governance, discovery, config, MQ, observability, storage
ops/admin/        admin and control-plane primitives
examples/         runnable adoption paths
```

## Request flow

1. A generated entrypoint loads config.
2. REST and RPC servers are built from the same service baseline.
3. Governance rules match requests by transport, service, method, path, and tags.
4. Discovery registers local instances and resolves upstreams.
5. Observability exports logs, metrics, traces, and admin diagnostics.
6. `/admin/control-plane` exposes machine-readable runtime metadata.

## Design rule

Use generated structure for service consistency, then customize domain code inside `internal/handler`, `internal/rpc`, and config files. Do not fork runtime packages unless you are extending the framework itself.

## Minimal example

```sh
gofly new service orders --style production --module example.com/orders
cd orders
go test ./...
go run ./cmd/orders
```

See [First service](../getting-started/first-service.md) for the generated layout.
