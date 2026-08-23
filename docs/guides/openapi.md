# OpenAPI schemas

gofly derives request and error schemas from Go structs. Use
`rest.StructSchema` for application types and keep error responses shared.

```text
schema := rest.StructSchema(createOrderRequest{})
responses := rest.DefaultErrorResponses()
responses["200"] = rest.JSONResponse("created", rest.StructSchema(orderResponse{}))
responses["400"] = rest.JSONErrorResponse("Invalid request")
```

Struct tags flow into the schema:

- `validate:"required"` becomes OpenAPI `required`
- `validate:"oneof=pending paid"` becomes an OpenAPI `oneof` / enum constraint
- `json:"sku"` becomes the property name

`rest.DefaultErrorResponses()` documents the 400 and 500 envelopes that
`rest.WriteError` actually emits. Do not invent a second error object for
generated handlers.

Related: [REST binding](rest.md) and
[invalid-request smoke](../reference/openapi-invalid-request-smoke.json).
