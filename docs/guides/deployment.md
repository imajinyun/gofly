# Deployment Guide

Use the production-orders example as the reference deployment shape for gofly services.

## Minimal production-shaped example

```sh
go run ./examples/production-orders
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9090/admin/control-plane
```

## Kubernetes starter

See:

- `examples/k8s`
- `k8s/deployment.yaml`

## Production configuration checklist

| Area | What to configure |
| --- | --- |
| Ports | REST, RPC, admin |
| Discovery | provider and endpoints |
| Governance | timeout, retry, breaker, rate limit |
| Observability | metrics, tracing, admin diagnostics |
| OpenAPI | keep contract accessible for review |
| Security | admin listener isolation and secrets handling |

## Release verification

```sh
go test ./...
make bench-smoke
gofly release check --strict
```

Use [Production checklist](../operations/production-checklist.md) before shipping.
