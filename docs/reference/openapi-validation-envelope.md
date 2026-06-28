# OpenAPI Validation Envelope

Schema: `gofly.openapi_validation_envelope.v1`

The generated service contract must keep runtime binding and OpenAPI schema in
sync across path, query, header, body, tag, schema, and error code behavior.
The blocking invalid-request smoke matrix lives in
`docs/reference/openapi-invalid-request-smoke.json`.
Each smoke case records an `alignmentInvariant`, `consumerAction`, and
`rollbackOrEscalation` entry so adopters can decide whether a generated REST
contract is safe to publish or must stay pinned to the previous scaffold.
The same manifest exposes `gofly.rest_adopter_contract.v1`, which links
invalid request smoke, OpenAPI schema golden tests, generated-service smoke,
and `make api-example-consistency-check` before adopters publish generated REST
services.
HTTP framework migration DX is governed by
`docs/reference/http-migration-dx.json` with schema
`gofly.http_migration_dx.v1`. It maps Gin, Echo, Fiber, and Hertz route,
binding, middleware, error envelope, OpenAPI, invalid request smoke, and
rollback steps into one blocking contract.

## Contract

| Surface | Runtime source | OpenAPI source | Error behavior |
| --- | --- | --- | --- |
| path | route path and `ctx.PathValue` | path parameter schema | stable `rest.ErrorResponse` |
| query | `query` tags and `BindRequest` | query parameter schema | stable `rest.ErrorResponse` |
| header | `header` tags and `BindRequest` | header parameter schema | stable `rest.ErrorResponse` |
| body | JSON binding | request body schema | stable `rest.ErrorResponse` |
| tag | struct tags and validator adapter | schema metadata | stable field-level error code |
| schema | `rest.StructSchema` and generated DTOs | OpenAPI components | golden tests |

## Required gates

```sh
make openapi-validation-check
go test -shuffle=on ./rest -run 'Test.*(Bind|OpenAPI|Validation)'
go test -shuffle=on ./cmd/gofly/internal/generator -run 'Test.*Invalid.*Request|Test.*OpenAPI'
```

The generated service invalid request smoke coverage must prove that invalid
path, query, header, body, and validator adapter failures return stable
`rest.ErrorResponse` JSON.

## HTTP Migration DX

Before switching traffic from Gin, Echo, Fiber, or Hertz, run the migration DX
contract and compare:

- route path parameters, especially `:id` to `{id}` conversions;
- `ctx.BindRequest` coverage for path, query, header, and JSON body fields;
- middleware ordering for recovery, request id, tracing, logging, metrics,
  browser safety, auth, validation, SSE, and WebSocket bounds;
- stable `rest.ErrorResponse` output for bind and validation failures;
- OpenAPI request body, parameter, response, and default error schemas;
- rollback behavior that keeps the previous router active until local gates
  pass.
