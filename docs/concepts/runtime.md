# Runtime

The runtime is the part of gofly that starts servers, applies configuration, and exposes health and admin surfaces.

## Default ports

| Surface | Default in generated production service | Purpose |
| --- | --- | --- |
| REST | `:8080` | Public HTTP API |
| RPC | `:8081` | Service-to-service RPC |
| Admin | `:9090` | Diagnostics and control plane |

## Health endpoints

REST services expose:

```sh
curl http://127.0.0.1:8080/startupz
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

Use `/startupz` for startup probes, `/healthz` for liveness, and `/readyz` for dependencies.

## Runtime configuration

Generated services keep runtime settings in `etc/<service>.yaml`. Production capabilities should be configured there instead of hard-coded in handlers.

```yaml
server:
  rest:
    port: 8080
  rpc:
    port: 8081
admin:
  port: 9090
  pathPrefix: /admin
```

## Local verification

```sh
go run ./cmd/orders
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:9090/admin/control-plane
```

## Runtime diagnostics

gofly exposes runtime diagnostics as low-cardinality snapshots so production
services can explain the effective dispatch chain without leaking credentials or
high-cardinality request data.

Current diagnostic entry points:

| Surface | Endpoint | Content |
| --- | --- | --- |
| REST | `/admin/runtime` | Unified REST runtime component, resolved middleware chain, governance runtime cache |
| REST governance | `/admin/governance/runtime` | Runtime components registered with the governance admin surface |
| HTTP RPC | `/rpc/admin/runtime` | Unified RPC server component, unary and stream middleware counts, governance runtime cache |
| RPC governance | `/rpc/admin/governance/runtime` | Runtime components registered with the RPC governance admin surface |
| gRPC | `/runtime` on the gRPC admin listener | Unified gRPC server component, unary and stream interceptor chain, governance rule count |
| Service bootstrap | `app.BootstrapWithRuntime(...).Snapshot(...)` or profile `/debug/runtime.json` | Unified app bootstrap component, lifecycle, log/trace/metrics/profile and health-check state |

The shared snapshot model lives in `core/runtime`. Each component reports a
stable `name`, `kind`, `owner`, `target`, `status`, and optional diagnostic
sections for middleware, governance, resolver, balancer, connection pool, retry,
and breaker state.

Generated services use `app.ServiceConf` and `app.BootstrapWithRuntime` as the
single bootstrap path for logging, metrics, tracing, profile endpoints, health
checks, startup checks, and graceful shutdown timeouts. The legacy bootstrap
summary remains available in `/debug/runtime.json`; its `runtime` field now
contains the same `core/runtime` component model used by REST, HTTP RPC, and
gRPC admin endpoints.

Middleware and interceptor entries include a stable `name`, `source`, `order`,
and `reason`. REST production presets surface built-in layers such as
`security_headers`, `trace`, `request_id`, `recover`, `log`, `metrics`,
`timeout`, and `max_body_bytes`; user-registered REST middleware is reported as
`custom_middleware`. gRPC default servers report unary interceptors in
`recover`, `observability`, `otel_trace`, `governance` order and stream
interceptors in `observability`, `otel_trace`, `governance` order.

The first runtime governance slice intentionally keeps existing control-plane
contracts unchanged. Existing REST control-plane snapshots under
`/admin/control-plane` remain available, while the runtime endpoints provide the
common shape needed for follow-up discovery event, load balancer, connection
pool, and policy-matrix governance.

## RPC runtime scheduling diagnostics

HTTP RPC clients include scheduling diagnostics in `RPCRuntimeSnapshot`.

| Section | Purpose |
| --- | --- |
| `policy.state.loadShedderMode` | Explains whether policy load shedding is static concurrency based. Adaptive CPU/latency shedding is reported by `core/limit.AdaptiveSnapshot` through registered adaptive limiter components. |
| `policy.state.loadShedderLimit` | Effective static concurrency or in-flight limit from the RPC policy matrix. |
| `policy.state.loadShedderWindow` | Policy window metadata used for diagnostics and future adaptive policies. |
| `stats.phases` | Low-cardinality phase aggregates for `governance`, `resolve`, `lb`, `connect`, `send`, `recv`, `retry`, `fallback`, `breaker`, and future server `handler` phases. |
| `warmup` | Opt-in client warmup result covering resolver, balancer, and optional connection-pool preheat. |

Adaptive limiter snapshots expose `passes` and `drops` counters in the current
window alongside CPU load, latency target, error ratio, current limit, in-flight
count, and peak in-flight count. These counters are intentionally window-scoped
so operators can read recent shedding pressure without creating unbounded
per-route or per-endpoint labels.

Client warmup is disabled by default. Enable it with `rpc.WithClientWarmup` when
startup should fail fast if the resolver, load balancer, or optional connection
pool preheat cannot complete. Warmup resolves the endpoint set, selects a target
with the configured balancer, and only dials the connection pool when
`RPCWarmupConfig.ConnPool` is true. It does not issue a business RPC request.
