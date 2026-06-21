# First service

This guide explains the project created by:

```sh
gofly new service orders --style production --module example.com/orders
```

## Layout

```text
cmd/orders/                 # service entrypoint
etc/orders.yaml             # runtime config
etc/governance.json         # governance policy
internal/handler/           # REST handlers
internal/rpc/               # RPC service implementation
internal/config/            # generated config types and tests
internal/smoke/             # generated smoke test
docs/openapi.yaml           # API contract
```

## Development loop

```sh
go test ./...
go run ./cmd/orders
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9090/admin/control-plane
```

## Production knobs

| Concern | Config |
| --- | --- |
| REST port | `server.rest.port` in `etc/orders.yaml` |
| RPC port | `server.rpc.port` in `etc/orders.yaml` |
| Admin port | `admin.port` in `etc/orders.yaml` |
| Discovery | `discovery.provider`, `discovery.address`, `discovery.endpoints` |
| Governance | `etc/governance.json` |

## Next changes to make

1. Add domain-specific REST routes under `internal/handler`.
2. Add RPC methods under `internal/rpc`.
3. Update `docs/openapi.yaml` when API contracts change.
4. Add policy rules to `etc/governance.json` for timeouts, retries, breakers, or rate limits.
5. Keep `go test ./...` green before committing.
