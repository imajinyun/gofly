# Production checklist

Use this list after `gofly new service --style production` or before promoting
an example into a real environment.

1. `go test ./...` passes in the service module.
2. `/healthz` and `/admin/control-plane` respond.
3. `bin/production-check.sh` or `make production-check` fails closed on unsafe
   defaults (open admin, skipped TLS, unbounded body limits).
4. Generated `deploy/k8s/` and `deploy/helm/` are reviewed before apply.
5. Observability starters in `deploy/observability/` are wired to a real
   Prometheus / OTel collector, not left as localhost-only.
6. Secrets are not copied into control-plane snapshots or `gofly bug --json`
   support bundles.
7. RPC performance claims are not copied from a single benchmark run.

Related: [troubleshooting](troubleshooting.md),
[zero-to-production](../tutorials/zero-to-production.md).
