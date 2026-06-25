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

`make bench-trend` produces the current trend artifact. Release reviewers should
treat this as a non-blocking regression budget until enough historical data is
available:

- no unexplained allocation growth on `gofly` REST rows;
- no unexplained governance overhead increase without a linked feature;
- any optimization must cite the benchmark or pprof signal that motivated it.

Run:

```sh
make perf-governance-check
make bench-trend
```
