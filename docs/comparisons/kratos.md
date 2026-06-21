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

## When gofly fits

Choose gofly when generated delivery, CLI governance, control-plane snapshots, and framework-provided smoke tests matter more than preserving Kratos application structure.
