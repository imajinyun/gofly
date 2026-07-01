# Resilience Drill Evidence

Schema: `gofly.resilience_drill_evidence.v1`

The resilience drill is a deterministic example that proves gofly's rate limit,
retry, circuit breaker, and recovery behavior as one runnable contract. P13 adds
`GOFLY-P13-04-GOZERO-RESILIENCE-DEFAULTS`, which turns the broader REST, RPC,
and Gateway resilience matrix into blocking evidence instead of prose-only
claims. The machine-readable evidence manifest is
[`resilience-drill.json`](resilience-drill.json), and the runtime report schema
is `gofly.resilience_drill.v1`.

## Run

```sh
go run -C examples/resilience . --json
make resilience-drill-check
```

`make resilience-drill-check` validates that the JSON drill report includes:

- rate-limit rejection evidence;
- retry attempts through downstream call count;
- breaker-open fast-fail evidence;
- recovery back to a closed breaker state.

The same JSON contract is covered by `make examples-smoke` so example drift is
visible in the standard runnable examples gate.

## P13 Resilience Matrix

The P13 matrix aligns default resilience behavior with go-zero style production
expectations across REST, RPC, and Gateway surfaces. The gate validates that
each surface has machine-readable evidence for:

- timeout;
- concurrency-limit;
- rate-limit;
- breaker;
- adaptive-shedding;
- retry;
- enable-disable paths;
- invalid-config rejection;
- downgrade-fallback behavior.

`make resilience-drill-check` reads
`p13GoZeroResilienceDefaults` from the manifest and verifies that every listed
test reference points to a real Go test function. It also blocks
`documentation-only` and `unsupported-report-only` modes so the matrix cannot be
closed with narrative text alone.

| Surface | Status | Evidence shape |
| --- | --- | --- |
| REST | `runtime-tested` | Server middleware, route options, production config validation, REST client retry, and adaptive limiter tests. |
| RPC | `runtime-tested` | HTTP RPC client/server, streams, gRPC governance interceptors, load shedder, fallback, and policy validation tests. |
| Gateway | `mixed-runtime-tested` | Route governance, retry budget, breaker, timeout, passive-health fallback, and config/runtime-key tests. |

Gateway adaptive shedding is intentionally recorded as
`candidate-via-governance`: current Gateway resilience evidence uses passive
health ejection and governance fallback behavior, not a dedicated Gateway
adaptive limiter. Promotion to `runtime-tested` requires adding that dedicated
limiter and updating the manifest and tests in the same change.

## Reference App Signals

The drill is paired with `examples/production-orders` so resilience evidence
also maps to production-style failure paths:

| Signal | Evidence |
| --- | --- |
| saga compensation | `examples/production-orders/README.md` documents the inventory failure path. |
| outbox retry | `examples/production-orders/main.go` configures relay `MaxAttempts`. |
| topology fallback | `topology_evidence` entries include fallback notes for memory and Docker-backed profiles. |
| rollback note | `examples/production-orders/README.md` documents rollback by keeping the previous deployment active. |
