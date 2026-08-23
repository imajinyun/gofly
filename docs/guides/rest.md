# REST binding and errors

## Binding and validation

Handlers should bind and validate through `Context` so the same validator
reaches path, query, header, and JSON body fields.

```text
func (h handler) create(ctx *rest.Context) {
    var req createOrderRequest
    if err := ctx.BindRequest(&req); err != nil {
        ctx.Error(err)
        return
    }
}
```

`ctx.BindRequest` uses `rest.Config.Validator` when the server set one. Leave
the validator nil to keep gofly's built-in struct tags. Package-level
`rest.BindRequest` is the same helper for non-`Context` call sites.

Custom adapters implement `rest.Validator` and can return
`rest.ValidationFailures` so field errors survive the JSON envelope.

## Error envelope

Invalid requests write `rest.ErrorResponse` via `ctx.Error(err)` or
`rest.WriteError`. A validation failure looks like:

```json
{
  "code": "invalid_argument",
  "text": "field email failed email validation",
  "fields": [
    {"field": "email", "rule": "email", "code": "invalid_email"}
  ]
}
```

OpenAPI docs should reuse `rest.JSONErrorResponse` and
`rest.DefaultErrorResponses()` so generated `/openapi.json` matches the
runtime envelope. See [openapi.md](openapi.md).
