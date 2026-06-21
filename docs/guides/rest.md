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
