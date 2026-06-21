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

## Validation gates

Run these before merging the migration branch:

```sh
make examples-copyable-check
make docs-check
go test ./...
```

For route-level parity, replay sampled requests against the old Gin route and the new gofly route, then compare status codes, JSON field names, and error bodies.

## Rollback plan

Keep the Gin route registered until the gofly route has passed canary traffic. Roll back by sending traffic to the old Gin route; do not delete old middleware until governance policy and metrics have equivalent coverage.

## Demo path

Start with the standalone REST demo:

```sh
cd examples/restserver
go test ./...
go run .
```

Then follow [Moving a Gin-style Service into Gofly](../case-studies/migrate-from-gin.md) for a deeper migration path.

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
