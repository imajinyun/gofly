# REST Guide

Use gofly REST when you need generated structure, request binding, health routes, OpenAPI, and policy-aware runtime behavior.

## Minimal example

Run the example:

```sh
go run ./examples/restserver
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/users/42
curl http://127.0.0.1:8080/openapi.json
```

## Smallest server

```go
srv := rest.MustNewServer(rest.Config{})
srv.AddRoute(rest.Route{
    Method: http.MethodGet,
    Path:   "/users/{id}",
    Handler: func(c *rest.Context) {
        c.JSON(http.StatusOK, map[string]string{"id": c.PathValue("id")})
    },
})
srv.AddOpenAPIRoutes(rest.OpenAPIInfo{Title: "users", Version: "v1"})
_ = srv.Start()
```

## Binding and validation

Use `Context.BindRequest` for normal request DTOs. It merges supported sources in this order: JSON body for methods with a body, path values, query values, and headers. Use the smaller helpers only when a handler intentionally binds one source:

| Helper | Source |
| --- | --- |
| `ctx.Bind` / `rest.BindJSON` | JSON request body |
| `ctx.BindQuery` / `rest.BindQuery` | URL query values and `form` tag fallback |
| `ctx.BindPath` / `rest.BindPath` | `net/http` path values |
| `ctx.BindHeader` / `rest.BindHeader` | HTTP headers |
| `ctx.BindRequest` / `rest.BindRequest` | body, path, query, and headers |

The built-in validator supports the portable `validate` tag subset used by OpenAPI schema generation: `required`, `min`, `max`, and `oneof`. Set `rest.Config.Validator` to adapt a project validator without adding it to gofly's root module dependency graph.

```go
type CreateOrderRequest struct {
    TenantID string `path:"tenantId" validate:"required"`
    SKU      string `json:"sku" validate:"required"`
    Quantity int    `json:"quantity" query:"quantity" validate:"min=1"`
}

srv.AddRoute(rest.Route{
    Method: http.MethodPost,
    Path:   "/tenants/{tenantId}/orders",
    RequestBody: rest.JSONBodySchema(rest.StructSchema(CreateOrderRequest{}), true),
    Responses: rest.DefaultErrorResponses(),
    Handler: func(ctx *rest.Context) {
        var req CreateOrderRequest
        if err := ctx.BindRequest(&req); err != nil {
            ctx.Error(err)
            return
        }
        ctx.JSON(http.StatusCreated, map[string]string{"sku": req.SKU})
    },
})
```

Binding, parsing, and validation failures are reported as the stable REST error envelope:

```json
{
  "code": "invalid_argument",
  "text": "field Quantity failed min=1 validation",
  "message": "field Quantity failed min=1 validation",
  "status": 400,
  "fields": [
    {
      "field": "Quantity",
      "rule": "min=1",
      "message": "field Quantity failed min=1 validation"
    }
  ]
}
```

The concrete response type is `rest.ErrorResponse`; generated routes should use
`rest.JSONErrorResponse(...)` in OpenAPI metadata when documenting custom error
codes.

Generated handlers and hand-written handlers should use `ctx.Error(err)` for binding and validation failures, and should document `rest.DefaultErrorResponses()` or `rest.JSONErrorResponse(...)` in route metadata.

## Production configuration

| Concern | Config |
| --- | --- |
| Port | `server.rest.port` |
| Timeout | `server.rest.timeout` and governance rules |
| Middleware defaults | `rest.Config.Middlewares` |
| Health | `Middlewares.Health` |
| Metrics | `Middlewares.Metrics` |
| Admin split | keep admin on `:9090`, not the public REST port |

## Verification

```sh
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/openapi.json
```

Related: [OpenAPI](openapi.md), [Governance](../concepts/governance.md).
