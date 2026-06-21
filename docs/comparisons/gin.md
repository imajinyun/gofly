# Migrating from Gin

Gin is a focused HTTP router. gofly adds code generation, runtime governance, RPC, discovery, OpenAPI, and control-plane metadata around REST services.

## Mapping

| Gin | gofly |
| --- | --- |
| `gin.New()` | `rest.MustNewServer(rest.Config{})` |
| `r.GET("/users/:id", h)` | `AddRoute(rest.Route{Method: GET, Path: "/users/{id}"})` |
| `c.Param("id")` | `ctx.PathValue("id")` |
| `c.ShouldBindJSON(&v)` | `ctx.Bind(&v)` |
| middleware | `rest.Middleware` or generated governance rules |

## Migration steps

1. Generate a production service with `gofly new service`.
2. Move Gin handlers into `internal/handler`, replacing `gin.Context` with `*rest.Context`.
3. Convert route patterns from `:id` to `{id}`.
4. Move cross-cutting behavior into middleware or `etc/governance.json`.
5. Add OpenAPI metadata to routes and verify `/openapi.json`.

## Example

Gin:

```go
r.GET("/users/:id", func(c *gin.Context) {
    c.JSON(200, gin.H{"id": c.Param("id")})
})
```

gofly:

```go
srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/users/{id}", Handler: func(c *rest.Context) {
    c.JSON(http.StatusOK, map[string]string{"id": c.PathValue("id")})
}})
```

## Keep Gin when

Keep Gin if you only need a small router and do not need generated projects, governance, discovery, or control-plane diagnostics.
