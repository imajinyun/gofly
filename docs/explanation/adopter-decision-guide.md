# Adopter Decision And Migration Manual

Schema: `gofly.adopter_decision_guide.v1`

This guide turns the capability index into a decision and migration manual. Each
path names a runnable migration case, rollback note, compatibility caveat, and
gate command so teams can choose a path without reading every capability page.

## Migration path matrix

| Path | Use when | Runnable migration case | Compatibility caveat | Gate command | Rollback note |
| --- | --- | --- | --- | --- | --- |
| Gin to gofly | The HTTP service needs OpenAPI, generated contracts, runtime governance, or control-plane state. | `examples/restserver`; proof row in `examples/migration-proof` | Gin `:id` routes become gofly `{id}` routes; compare status codes, JSON names, and the stable error envelope before switching traffic. | `go test -C examples/restserver ./...`; `make examples-smoke` | Keep the Gin route active behind the existing router until sampled responses and metrics match. |
| go-zero to gofly | The team wants generated services plus governance files, discovery, release gates, and admin diagnostics. | `examples/production-orders`; proof row in `examples/migration-proof` | Preserve `.api` request/response field names and verify generated OpenAPI plus `/admin/control-plane` before changing discovery. | `make generated-version-compat-check`; `make reference-app-smoke` | Keep the go-zero endpoint addressable through discovery and switch routing back if generated compatibility or smoke checks fail. |
| Kratos to gofly | Cloud-native operations remain important, but generated governance and AI-readable runtime state are needed. | `examples/microshop`; proof row in `examples/migration-proof` | Keep domain services separate from transport wiring; compare lifecycle hooks, health checks, discovery registration, and topology output. | `make cloud-native-render-check`; `go test -C examples/microshop ./...` | Restore the previous Kratos deployment target while keeping shared domain services unchanged. |
| Kitex with gofly | Kitex owns latency-critical RPC and gofly should own REST ingress, governance, release checks, or non-hot-path service glue. | `examples/rpc-idl-matrix`; proof row in `examples/migration-proof` | Do not migrate hot methods without `bench/` evidence; compare unary and stream contracts, resolver updates, balancing, tracing, auth, and rollback behavior. | `make rpc-boundary-check`; `make bench-evidence-check` | Route latency-critical methods back to Kitex and keep gofly for REST ingress, governance, or generated service surfaces. |

Run the executable proof matrix:

```sh
go test -C examples/migration-proof ./...
go run -C examples/migration-proof .
make examples-smoke
```

## When to choose gofly

Choose gofly when a service needs generated structure, REST/RPC composition,
runtime governance, OpenAPI, control-plane snapshots, observability, release
checks, and AI-readable automation output.

- runnable example: `examples/production-orders`
- rollback note: keep the previous deployment serving while the new generated
  service is validated; disable the new gateway route if control-plane drift is
  detected
- gate command: `make reference-app-smoke`

## When to choose Gin

Choose Gin when the service is a focused HTTP API and does not need generated
contracts, runtime governance, or control-plane metadata.

- runnable example: `examples/restserver`
- rollback note: retain Gin as the router and adopt gofly only for OpenAPI,
  governance, or control-plane sidecars
- gate command: `go test -C examples/restserver ./...`

## When to keep Kitex

Keep Kitex when latency-critical internal RPC paths already depend on Kitex IDL
generation and benchmark evidence does not justify migration.

- runnable example: `examples/rpc-idl-matrix`
- rollback note: route the hot method back to Kitex and keep gofly for REST
  ingress, governance, and release checks
- gate command: `make rpc-boundary-check`

## How to migrate go-zero

Migrate go-zero services when the generated-service workflow is useful but the
team also needs control-plane snapshots, runtime governance, and release gates.

- runnable example: `examples/production-orders`
- rollback note: keep the go-zero deployment serving until the gofly generated
  project passes generated-version compatibility and reference-app smoke
- gate command: `make generated-version-compat-check`

## How to migrate Kratos

Migrate Kratos services when cloud-native operations remain important but the
team wants generated governance contracts and AI-readable runtime state.

- runnable example: `examples/microshop`
- rollback note: keep Kratos as the serving deployment and use gofly first for
  control-plane comparison or non-critical service slices
- gate command: `make cloud-native-render-check`

Run the guide gate with:

```sh
make docs-check
make adopter-decision-check
```

<!-- claim-provenance: http-dx-openapi-envelope -->
<!-- claim-provenance: generated-scaffold-upgrade -->
<!-- claim-provenance: rpc-boundary-tier1 -->
<!-- claim-provenance: production-reference-proof -->
<!-- claim-provenance: release-trust-evidence -->
