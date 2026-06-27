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
allocation blocking rows, RPC unary remains a candidate, and gateway proxy plus
cache hot path stay `unsupported-report-only` until `bench/` publishes dedicated
baseline/current rows with enough samples to promote.

Run:

```sh
make perf-governance-check
make bench-smoke
make bench-regression-check
make bench-trend
```
