# Resilience Drill Evidence

Schema: `gofly.resilience_drill_evidence.v1`

The resilience drill is a deterministic example that proves gofly's rate limit,
retry, circuit breaker, and recovery behavior as one runnable contract. The
machine-readable evidence manifest is
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

## Reference App Signals

The drill is paired with `examples/production-orders` so resilience evidence
also maps to production-style failure paths:

| Signal | Evidence |
| --- | --- |
| saga compensation | `examples/production-orders/README.md` documents the inventory failure path. |
| outbox retry | `examples/production-orders/main.go` configures relay `MaxAttempts`. |
| topology fallback | `topology_evidence` entries include fallback notes for memory and Docker-backed profiles. |
| rollback note | `examples/production-orders/README.md` documents rollback by keeping the previous deployment active. |
