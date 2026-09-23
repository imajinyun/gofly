# API Semantic Parity

schema: gofly.api_semantic_parity.v1

gofly treats API compatibility as equivalent routes, request binding, type
shapes, response statuses, and error behavior. It does not claim byte-identical
goctl output or directory layout.

The blocking gate is:

```sh
make api-semantic-parity-check
```

To compare against the pinned go-zero checkout explicitly:

```sh
GOZERO_ROOT=/path/to/go-zero make api-semantic-parity-check
```

An explicitly configured invalid `GOZERO_ROOT` fails. Without the environment
variable, existing goctl compatibility scripts may use `../gozero` or run in
documented contract-only mode. CI pins go-zero commit
`84c92d710b9f2ae11c3cbcee242cea40eec42e70`.

## Supported semantics

- grouped imports and grouped type blocks
- pointer, slice, nested map, and imported inline types
- path, form/query, header, and JSON body locations
- request-less and response-less routes
- native and gozero-compatible generated modules
- OpenAPI locations, required flags, body schemas, and response descriptions
- stable `rest.ErrorResponse` invalid request envelopes

## Intentional differences

- The pinned goctl path returns HTTP 200 for a successful response-less route.
  gofly preserves `@doc(respCode: ...)`, including 204 with no response body.
- gofly accepts route-local `@server` metadata as an enhancement. The pinned
  goctl execution path uses service-level `@server` plus route `@handler`.
- Type aliases and source-comment round-trip fidelity are not claimed.

The machine-readable status and rollback contract is
`docs/reference/api-semantic-parity.json`.
