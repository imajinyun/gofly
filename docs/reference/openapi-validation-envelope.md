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
The P13 closeout is recorded as `p13RestValidationEnvelopeCloseout` inside
`docs/reference/openapi-invalid-request-smoke.json`. It keeps path, query,
header, body, tag, schema, error-code, validator adapter, generated-service
invalid request smoke, and stable `rest.ErrorResponse` fields tied to one
blocking `make openapi-validation-check` contract.

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

## P13 REST Validation Closeout

`p13RestValidationEnvelopeCloseout` binds each runtime failure case to the
OpenAPI schema golden:

- path binding uses `path-parse-failure` plus `openapi-schema-alignment`;
- query binding uses `query-validation-failure` plus
  `openapi-schema-alignment`;
- header binding uses `header-validation-failure` plus
  `openapi-schema-alignment`;
- body binding uses `body-decode-failure` plus `openapi-schema-alignment`;
- tag validation uses `body-tag-validation-failure` plus
  `openapi-schema-alignment`;
- validator adapter failures use `validator-adapter-failure` plus
  `openapi-schema-alignment`;
- generated services use `generated-service-invalid-request` plus
  `openapi-schema-alignment`.

Promotion requires the root runtime envelope and P13 envelope to agree on
`rest.ErrorResponse`, HTTP `400`, `invalid_argument`, and the stable fields
`code`, `text`, `message`, `status`, and `fields`. If any surface drifts, keep
the previous handler, DTO, validator adapter, or generated scaffold serving
until the runtime and schema evidence converge again.

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
