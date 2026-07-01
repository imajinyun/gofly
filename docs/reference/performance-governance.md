# Performance Governance

Schema: `gofly.performance_governance.v1`

gofly does not optimize hot paths from intuition. REST router, path params,
JSON binding, middleware, OpenAPI, and governance overhead changes require
benchmark or pprof evidence first.

## Required evidence

| Area | Benchmark | Evidence |
| --- | --- | --- |
| REST router | `BenchmarkHTTPHello` | latency, allocations, and optional pprof CPU profile |
| path params | `BenchmarkHTTPPathParams` | param extraction allocation report |
| JSON binding | `BenchmarkHTTPJSONBinding` | decoder allocation report |
| middleware | `BenchmarkHTTPMiddlewareChain` | per-chain overhead |
| OpenAPI | `BenchmarkHTTPOpenAPI` | disabled/enabled contract metadata cost |
| governance overhead | `BenchmarkHTTPGovernance` | disabled/enabled policy decision cost |
| gateway proxy | `BenchmarkGatewayProxy` | candidate report-only proxy evidence |
| cache hot path | `BenchmarkCacheHotPath`, `BenchmarkCacheHotPathGetOrLoadHit` | candidate report-only cache hit evidence |

## Regression budget

`make bench-trend` produces the current trend artifact. The regression budget is
enforced by `make bench-regression-check`, which upgrades HTTP hot-path
allocation drift and selected latency drift from review guidance to blocking
gates:

- `allocs/op` median is blocking for the tracked `gofly` HTTP rows;
- latency defaults to report-only, with promoted rows defined in
  `bench/budget-ratchet.json`;
- `BenchmarkHTTPOpenAPI/disabled` and `BenchmarkHTTPOpenAPI/enabled` are the
  first blocking latency rows because they have five-sample baseline history
  and current smoke evidence below baseline;
- `BenchmarkHTTPGovernance/disabled` and `BenchmarkHTTPGovernance/enabled` are
  also blocking latency rows because the governance policy path now has
  five-sample baseline history, no allocation drift, and current smoke evidence
  below baseline;
- governance overhead allocation growth must have a linked feature and baseline
  refresh rationale;
- any optimization must cite the benchmark or pprof signal that motivated it.

The regression gate writes `bench/regression-report.json` with schema
`gofly.benchmark_regression_report.v1`. CI uploads the report with the benchmark
smoke artifacts so release reviewers can inspect latency and byte trends even
when the blocking allocation budget passes.

`make bench-regression-check` also validates the ratchet policy before comparing
numbers. The policy check requires `allocationPolicy.blocking` to stay true,
`latencyPolicy.defaultMode` to stay `report-only`, promoted latency rows to
declare at least five baseline samples and a promotion reason, and RPC
candidates to remain out of `trackedBenchmarks` until their promotion criteria
are met.

The ratchet also carries a `surfacePolicy` section so unsupported performance
claims stay machine-visible. REST route dispatch is allocation-blocking but
latency report-only, governance rule matching has selected latency and
allocation blocking rows, and RPC unary remains a candidate. P9 adds dedicated
gateway and cache benchmark rows through `p9GatewayCacheOwnership`; those rows
are candidate report-only evidence until the committed baseline has five
samples, the current trend has three samples, and promotion keeps rollback
guidance attached.

P10 adds `p10PerformanceBudgetRatchet` to close the current promotion decision:
only OpenAPI and governance rows with five-sample baseline evidence stay
latency-and-allocation blocking. REST router/path/binding/middleware latency,
RPC unary/stream latency, gateway proxy, and cache hot-path rows remain
report-only until they publish three current trend samples, explicit
`maxRegressionRatio` policy, allocation promotion, and rollback notes. This
prevents weak-signal benchmark rows from becoming release blockers while still
making every hold reason auditable.

`make bench-evidence-check` follows the same boundary: committed baseline
evidence is required for published blocking and historical candidate rows, while
P10 gateway/cache candidates are checked through source, matrix, and ratchet
contract presence until they are deliberately promoted.

Run:

```sh
make perf-governance-check
make bench-smoke
make bench-regression-check
make bench-trend
```
