# Adopter Decision Guide

Schema: `gofly.adopter_decision_guide.v1`

This guide turns the capability index into a decision manual. Each path names a
runnable example, rollback note, and gate command.

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
