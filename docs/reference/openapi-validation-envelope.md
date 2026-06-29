# OpenAPI Validation Envelope

Schema: `gofly.openapi_validation_envelope.v1`

The generated service contract must keep runtime binding and OpenAPI schema in
sync across path, query, header, body, tag, schema, and error code behavior.
The blocking invalid-request smoke matrix lives in
`docs/reference/openapi-invalid-request-smoke.json`.
Each smoke case records an `alignmentInvariant`, `consumerAction`, and
`rollbackOrEscalation` entry so adopters can decide whether a generated REST
contract is safe to publish or must stay pinned to the previous scaffold.

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
