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
- `k8s/kustomization.yaml`
- `k8s/servicemonitor.yaml`
- `k8s/hpa.yaml`
- `k8s/pdb.yaml`
- `charts/gofly`

Direct YAML users can apply the static assets:

```sh
kubectl apply -k k8s
```

Helm users can render or install the starter chart:

```sh
helm template gofly charts/gofly
helm install gofly charts/gofly
```

The starter assets include liveness, readiness, and startup probes, Prometheus scrape annotations, optional ServiceMonitor support, HPA, and PodDisruptionBudget.

Generated production services also include `deploy/observability/prometheus.yaml`, `deploy/observability/otel-collector.yaml`, `deploy/observability/grafana-dashboard.json`, `deploy/observability/logs-correlation.yaml`, and `bin/production-check.sh`. Run the generated `make production-check` after replacing placeholder secrets to verify Kubernetes, Helm, metrics, tracing, dashboard, and log-correlation assets stay in sync.

For production adoption proof, use
[`cloud-native-policy-conformance.json`](../reference/cloud-native-policy-conformance.json).
Its P10 contract links Helm/Kustomize render evidence, the `production-orders`
reference topology, runtime SLO checks, and rollback decisions. Run
`make p1-growth-check` before treating rendered assets as production-ready.

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
