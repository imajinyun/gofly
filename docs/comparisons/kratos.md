# Migrating from Kratos

Kratos is a cloud-native application framework. gofly is more opinionated around generated service delivery, governance files, admin metadata, and release gates.

## Mapping

| Kratos | gofly |
| --- | --- |
| app lifecycle | `app.Run` and generated entrypoint |
| HTTP server | `rest.Server` |
| gRPC server | `rpc/grpc` or gofly RPC |
| config | `core/config` layered providers |
| middleware | REST/RPC middleware plus governance rules |
| health/admin | health endpoints and `/admin/control-plane` |

## Migration steps

1. Keep domain services and repositories separate from transport code.
2. Generate a gofly service and wire existing domain services into REST/RPC handlers.
3. Convert Kratos middleware concerns into gofly middleware or governance policies.
4. Move service discovery configuration into gofly discovery config.
5. Verify observability and admin endpoints before release.

## Validation gates

Run these for each migrated service boundary:

```sh
make examples-copyable-check
make docs-check
go test ./...
```

For multi-service migrations, also capture the gateway or service topology output and compare it with the expected dependency graph.

## Rollback plan

Keep Kratos app wiring intact until gofly owns the same lifecycle hooks, health checks, and discovery registration. Roll back by restoring the previous deployment target and keeping shared domain services unchanged.

## Demo path

Use the multi-service microshop demo to map app lifecycle, gateway, and control-plane visibility:

```sh
cd examples/microshop
go test ./...
go run . describe
```

Use `examples/production-orders` when you need a single-service migration with messaging and saga behavior.
The runnable migration proof matrix in `examples/migration-proof` records the
Kratos smoke example, validation gates, and rollback note used by `make
examples-smoke`.

## When gofly fits

Choose gofly when generated delivery, CLI governance, control-plane snapshots, and framework-provided smoke tests matter more than preserving Kratos application structure.
