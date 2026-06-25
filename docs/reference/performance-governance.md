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
allocation drift from review guidance to a blocking gate:

- `allocs/op` median is blocking for the tracked `gofly` HTTP rows;
- latency remains report-only until enough cross-run history exists;
- governance overhead allocation growth must have a linked feature and baseline
  refresh rationale;
- any optimization must cite the benchmark or pprof signal that motivated it.

The regression gate writes `bench/regression-report.json` with schema
`gofly.benchmark_regression_report.v1`. CI uploads the report with the benchmark
smoke artifacts so release reviewers can inspect latency and byte trends even
when the blocking allocation budget passes.

Run:

```sh
make perf-governance-check
make bench-smoke
make bench-regression-check
make bench-trend
```
