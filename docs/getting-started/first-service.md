# First production service

`gofly new service orders --style production --module example.com/orders`
creates the Tier 0 layout. These paths are the contract:

| Path | Role |
| --- | --- |
| `cmd/orders/main.go` | Process entry |
| `etc/orders.json` | Runtime config |
| `etc/governance.json` | Policy defaults |
| `internal/routes/` | REST route registration |
| `internal/api/http/v1/ping/` | Default REST handler |
| `internal/app/` | Business logic |
| `internal/api/rpc/` | Default RPC service |
| `internal/admin/` | Control-plane contributors |
| `internal/discovery/` | Discovery wiring |
| `internal/smoke/` | Generated smoke tests |
| `deploy/k8s/` | Kubernetes starter |
| `deploy/helm/` | Helm chart |
| `deploy/observability/` | Prometheus and OTel starters |
| `bin/production-check.sh` | Fail-closed production defaults |

Run:

```sh
cd orders
go test ./...
go run ./cmd/orders
make production-check
```

Then:

```sh
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9090/admin/control-plane
```

Do not relocate those directories without migration notes. Details:
[generated-service-layout.md](../reference/generated-service-layout.md).
