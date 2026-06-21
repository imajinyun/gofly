# Quickstart

Use this path when you want a runnable production-shaped service, not a toy router.

## 1. Install

```sh
go install github.com/gofly/gofly/cmd/gofly@latest
gofly version
```

## 2. Generate the service

```sh
gofly new service orders --style production --module example.com/orders
cd orders
go test ./...
go run ./cmd/orders
```

## 3. Verify runtime endpoints

In another terminal:

```sh
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9090/admin/control-plane
```

The control-plane response includes generated metadata such as `generated.project` and `generated.project.runtime`. The generated smoke test in `internal/smoke/service_smoke_test.go` starts the service and checks the same contract.

## What was generated

| Capability | Path |
| --- | --- |
| REST API | `cmd/orders`, `internal/handler`, `docs/openapi.yaml` |
| RPC service | `internal/rpc` |
| Governance | `etc/governance.json` |
| Discovery | `etc/orders.yaml` memory provider defaults |
| Admin control plane | `:9090/admin/control-plane` |
| Tests | config tests plus generated smoke tests |

Next: [First service](first-service.md).
