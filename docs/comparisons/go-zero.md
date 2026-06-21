# Migrating from go-zero

go-zero and gofly both value generated services. gofly keeps the generated-service workflow and adds a stronger product surface around governance, discovery, control-plane metadata, benchmark transparency, and AI-readable manifests.

## Mapping

| go-zero | gofly |
| --- | --- |
| `.api` driven REST | `gofly new service` plus REST/OpenAPI output |
| rpc service | generated gofly RPC package |
| service config | `etc/<service>.yaml` |
| middleware | `rest.Middleware` and governance rules |
| operational checks | `/admin/control-plane`, `gofly release check` |

## Migration steps

1. Generate a gofly service using the same module path.
2. Port `.api` routes into REST handlers and OpenAPI metadata.
3. Move timeout, retry, breaker, and rate-limit policy into `etc/governance.json`.
4. Replace hard-coded upstream addresses with discovery config.
5. Run `go test ./...` and verify `/admin/control-plane`.

## What changes most

- gofly expects runtime metadata to be visible to operators.
- production defaults are part of the generated baseline;
- AI tooling can inspect command capabilities through `gofly ai manifest --json`.

## Keep go-zero when

Keep go-zero when your organization is standardized on go-zero IDL and does not need gofly's control-plane or governance surfaces.
