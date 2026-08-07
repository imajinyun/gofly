# Generated Service Layout Contract

schema: gofly.generated_service_layout.v1

This document is the durable layout contract for the generated production
service created by:

```sh
gofly new service <name> --style production --module <module>
```

The Tier 0 Golden Path is intentionally narrow. It is the default service
shape that adopters, examples, release checks, and AI agents may assume without
reading generator internals.

## Tier 0 Golden Path

The generated service must keep these stable responsibilities:

| Area | Required path | Responsibility |
| --- | --- | --- |
| Entrypoint | `cmd/<name>/main.go` | Wires config loading, REST, RPC, discovery, governance, admin endpoints, and lifecycle bootstrap. |
| Runtime config | `etc/<name>.json` | Provides production defaults for REST, RPC, governance, OpenAPI, and control-plane behavior. |
| Governance config | `etc/governance.json` | Carries runtime policy defaults used by generated services. |
| REST routes | `internal/routes/routes.go` | Registers generated REST routes through one stable route package. |
| REST handler | `internal/api/v1/ping/ping.go` | Provides the default generated REST handler surface. |
| Business service | `internal/service/ping.go` | Holds the default generated service behavior behind REST/RPC entry points. |
| RPC service | `internal/rpc/greeter.go` | Provides the default generated RPC service. |
| Admin control-plane | `internal/admin/admin.go` | Registers generated admin and control-plane contributors. |
| Discovery | `internal/discovery/registry.go` | Provides generated service-discovery wiring. |
| Smoke test | `internal/smoke/service_smoke_test.go` | Verifies `/healthz`, `/admin/control-plane`, and runtime governance metadata. |
| Production check | `internal/config/production_check.go` and `bin/production-check.sh` | Fail closed on unsafe production config defaults. |
| Deployment | `deploy/k8s/<name>.yaml` and `deploy/helm/Chart.yaml` | Provide checked-in production deployment starters. |
| Observability | `deploy/observability/prometheus.yaml` and `deploy/observability/otel-collector.yaml` | Provide production observability starters. |

The layout may add files, but removing or renaming the paths above is a Tier 0
contract change and must be treated as compatibility-sensitive.

## Runtime Requirements

The generated `cmd/<name>/main.go` must preserve these wiring points:

- `app.BootstrapWithRuntime`
- `appadmin.NewServer`
- `appdiscovery.NewRegistry`
- `rest.WithGovernanceManager`
- `rpc.NewServer`
- `httpServer.AddOpenAPIRoutes`

The generated smoke test must preserve these runtime assertions:

- `/healthz` responds through the generated REST runtime.
- `/admin/control-plane` exposes a `gofly-control-plane.v1` snapshot.
- `assertControlPlaneResilience` validates generated timeout, rate-limit,
  concurrency, breaker, and retry metadata.

## Verification

The focused gate is:

```sh
make generated-service-layout-check
```

This gate runs:

- `TestGoldenPathProductionServiceLayoutContract`
- `TestNewServiceGeneratedProjectSmokeMatrix`
- generated `TestGeneratedProductionServiceSmoke`
- marker checks against this document

The broader generated-output gate also depends on this contract:

```sh
make generated-output-governance
```

## Compatibility Rules

1. Additive files are allowed when they do not change the Tier 0 paths.
2. Moving a Tier 0 path requires a migration note and release evidence.
3. Removing `/admin/control-plane` from the golden path is a breaking change.
4. Production defaults must remain testable without Docker or network access.
5. Generated project dependencies must stay in the generated project's own
   `go.mod`; root-module dependency changes require separate governance.
