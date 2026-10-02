# HTTP middleware matrix

JWT, CORS, CSRF, sessions, OpenTelemetry, Prometheus, SSE, and WebSocket routes.
Reusable middleware implementations and their behavior matrix live in the
[`middlewares`](middlewares) package in this module.

## Run

```sh
go test ./...
go run .
```

Describe the matrix:

```sh
go run . --describe
```

Install maintained middleware source into another gofly service without
changing its routes, configuration, secrets, or `go.mod`:

```sh
gofly api middleware --list
gofly api middleware --preset auth,cors --dir /path/to/service
gofly api middleware --preset all --dir /path/to/service --dry-run --json
```

Existing different files are treated as conflicts and are never overwritten.
See the [middleware preset contract](../../../docs/reference/middleware-presets.md).

## Related

- [Examples catalog](../../README.md)
- [Documentation index](../../../docs/index.md)
