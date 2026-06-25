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
