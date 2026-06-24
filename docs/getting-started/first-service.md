# First service

This guide explains the project created by:

```sh
gofly new service orders --style production --module example.com/orders
```

## Layout

```text
cmd/orders/                 # service entrypoint
etc/orders.json             # runtime config
etc/governance.json         # governance policy
internal/routes/            # REST route registration
internal/api/v1/ping/       # generated REST handler package
internal/rpc/               # RPC service implementation
internal/admin/             # admin and control-plane server
internal/discovery/         # generated discovery registry wiring
internal/config/            # generated config types and tests
internal/smoke/             # generated smoke test
deploy/k8s/                 # Kubernetes production assets
deploy/helm/                # Helm production chart
deploy/observability/       # Prometheus, OTel, Grafana, and log assets
bin/production-check.sh     # generated production readiness gate
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
| REST port | `rest.addr` in `etc/orders.json` |
| RPC port | `rpc.addr` in `etc/orders.json` |
| Admin port | `admin.addr` in `etc/orders.json` |
| Discovery | `discovery.provider`, `discovery.address`, `discovery.endpoints` |
| Governance | `etc/governance.json` |

## Next changes to make

1. Add domain-specific REST routes under `internal/routes` and handler packages under `internal/api`.
2. Add RPC methods under `internal/rpc`.
3. Update route OpenAPI metadata when API contracts change.
4. Add policy rules to `etc/governance.json` for timeouts, retries, breakers, or rate limits.
5. Keep `go test ./...` and `make production-check` green before committing.
