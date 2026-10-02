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

## Related

- [Examples catalog](../../README.md)
- [Documentation index](../../../docs/index.md)
