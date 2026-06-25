# Runtime SLO And Golden Signals

Schema: `gofly.runtime_slo.v1`

gofly already ships logs, metrics, traces, health probes, admin diagnostics,
Prometheus/Grafana examples, ServiceMonitor assets, and trace/log correlation.
This contract makes the adopter-facing runtime SLO evidence explicit. The
machine-readable manifest is [`runtime-slo.json`](runtime-slo.json).

## Gate

```sh
make runtime-slo-check
```

`docs-check` depends on this gate so observability examples, cloud-native
assets, governance docs, and release dashboards do not drift apart.

## Golden Signals

| Signal | Evidence | Query or Contract |
| --- | --- | --- |
| latency | `examples/observability`, `core/observability/metrics`, REST middleware | `histogram_quantile(0.95, sum(rate(gofly_route_duration_seconds_bucket[5m])) by (le, route))` |
| errors | REST/RPC middleware and Grafana dashboard | `sum(rate(gofly_errors_total[5m])) / clamp_min(sum(rate(gofly_requests_total[5m])), 1)` |
| traffic | Prometheus scrape config, ServiceMonitor, observability example | `sum(rate(gofly_requests_total[5m])) by (route)` |
| saturation | adaptive limiter, concurrency limiter, RPC connection pool | adaptive limit passes/drops, concurrency slots, connection pool exhaustion |
| cache | local cache and LLM response-cache governance | cache hit/miss/error accounting and LLM response-cache governance stage |
| governance-decisions | governance manager, rules, resilience drill | rate-limit, retry, circuit-breaker, timeout, and canary decisions |
| trace-log-correlation | trace propagation, generated dashboards, observability example | `trace_id`, `request_id`, `traceparent`, and `Logs by trace_id` |

## Operator Runbook Drills

Operator drills are indexed in
[`operator-runbook-drills.json`](operator-runbook-drills.json) with schema
`gofly.operator_runbook_drills.v1`. Each drill maps an observed symptom to
golden signals, evidence files, local check commands, expected observation,
operator action, and rollback or escalation path.

| Drill | Symptom | Primary Checks |
| --- | --- | --- |
| health-probe-failure | startup, liveness, or readiness probes fail during rollout | `curl -v http://127.0.0.1:8080/healthz`, `curl -v http://127.0.0.1:8080/readyz`, `make p1-growth-check` |
| metrics-regression | request rate, error ratio, latency, or in-flight request metrics drift after a release | `make runtime-slo-check`, `go test -C examples/observability ./...` |
| trace-correlation-break | logs, REST requests, RPC calls, or traces cannot be correlated by request_id or trace_id | `make runtime-slo-check`, `go test -C examples/observability ./...` |
| resilience-policy-regression | rate limit, retry, timeout, breaker, or canary behavior changes unexpectedly | `make resilience-drill-check`, `go run -C examples/resilience . --json` |
| control-plane-drift | runtime metadata, service discovery, config, or governance policy differs from the expected deployment state | `curl http://127.0.0.1:9090/admin/control-plane`, `make generated-control-plane-smoke` |
| rollback-decision | release promotion is blocked by runtime, benchmark, security, or governance evidence | `make governance-report-check`, `make bench-evidence-check`, `make govulncheck` |

## Incident Rehearsals

The runbook also includes `incidentRehearsals` for adopter-facing incident
practice. Each rehearsal names symptoms, golden signals, required artifacts,
rollback trigger, and post-incident evidence so incident practice stays tied to
runnable gates instead of prose-only runbooks.

| Incident | Source drill | Diagnosis gate |
| --- | --- | --- |
| `rollout-readiness-incident` | `health-probe-failure` | `make runtime-slo-check` |
| `latency-error-regression-incident` | `metrics-regression` | `make runtime-slo-check` |
| `governance-policy-incident` | `resilience-policy-regression` | `make resilience-drill-check` |
| `release-gate-incident` | `rollback-decision` | `make governance-report-check` |

## Verification

Run the contract gate for documentation or observability evidence changes:

```sh
make runtime-slo-check
```

Run the executable observability example smoke when changing runtime
instrumentation:

```sh
go test -C examples/observability ./...
```

Run the production evidence gate when changing cloud-native observability
assets:

```sh
make p1-growth-check
```

Release notes should reference this page when runtime behavior, middleware
metrics, generated dashboards, or governance policy evidence changes.
