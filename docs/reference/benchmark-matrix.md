# Benchmark Matrix Reference

gofly publishes benchmark scenarios as a matrix instead of a single headline number. This keeps the comparison honest across REST routing, RPC calls, gateway routing, OpenAPI metadata, and governance policy decisions.

## Generate the matrix

```sh
make bench-matrix
```

This writes `bench/matrix.md` from `bin/scripts/benchstat.sh`. The generated file lives next to the benchmark sources in `bench/` so source and artifacts share one workspace.

The committed public baseline and environment evidence live in `bench/evidence.md` and are validated by:

```sh
make bench-evidence-check
```

## Publishing manifest

Schema: `gofly.benchmark_publishing.v1`

`bench/publishing.json` is the machine-readable publishing checklist for
release automation. It names the raw benchmark output, public baseline, trend
summary, and allocation regression report that should be attached when a release
changes REST, RPC, gateway, governance, OpenAPI, or code generation hot paths.

Validate the publishing contract with:

```sh
make bench-publish-check
make bench-regression-check
```

`make bench-regression-check` writes `bench/regression-report.json` with schema
`gofly.benchmark_regression_report.v1`. Allocation regression is blocking for
tracked HTTP hot-path rows. `bench/budget-ratchet.json` defines the
`gofly.benchmark_budget_ratchet.v1` policy that promotes selected latency rows
to release-blocking metrics while keeping the rest explicitly report-only.
The first promoted rows are `BenchmarkHTTPOpenAPI/disabled` and
`BenchmarkHTTPOpenAPI/enabled`. Governance overhead rows,
`BenchmarkHTTPGovernance/disabled` and `BenchmarkHTTPGovernance/enabled`, are
also blocking latency rows once the current ratchet confirms five baseline
samples, no allocation drift, and a smoke run below baseline.

## Run the benchmark suite

```sh
make bench-smoke
make bench-stat
make bench-trend
```

Use `BENCH_PATTERN` to scope a run:

```sh
BENCH_PATTERN=BenchmarkRPCUnary make bench-stat
```

## Public scenarios

| Area | Benchmark | Compared with |
| --- | --- | --- |
| REST routing | `BenchmarkHTTPHello` | `net/http`, gofly, Gin, Echo, Chi, Fiber, Hertz |
| REST path parameters | `BenchmarkHTTPPathParams` | `net/http`, gofly, Gin, Echo, Chi, Fiber, Hertz |
| REST JSON binding | `BenchmarkHTTPJSONBinding` | `net/http`, gofly, Gin, Echo, Chi, Fiber, Hertz |
| REST middleware chain | `BenchmarkHTTPMiddlewareChain` | `net/http`, gofly, Gin, Echo, Chi, Fiber, Hertz |
| OpenAPI route metadata | `BenchmarkHTTPOpenAPI` | gofly disabled/enabled |
| Governance decision path | `BenchmarkHTTPGovernance` | gofly disabled/enabled |
| RPC unary call | `BenchmarkRPCUnary` | gofly RPC, gRPC-Go |
| RPC stream governance | `BenchmarkRPCStreamGovernance` | gofly RPC stream governance |
| RPC server-stream governance | `BenchmarkRPCServerStreamGovernance` | gofly RPC stream governance |
| RPC client-stream governance | `BenchmarkRPCClientStreamGovernance` | gofly RPC stream governance |
| RPC bidirectional stream governance | `BenchmarkRPCBidiStreamGovernance` | gofly RPC stream governance |
| RPC resolver/balancer smoke | `examples/rpc-idl-matrix` | resolver/balancer smoke and Kitex boundary evidence |

## Release rule

Every release that changes REST, RPC, gateway, governance, OpenAPI, or code generation hot paths should attach:

1. raw output from `make bench-stat`;
2. `bench/evidence.md` from `make bench-baseline` when publishing a new public baseline;
3. `bench/summary.md` from `make bench-trend`;
4. `bench/regression-report.json` from `make bench-regression-check`;
5. significant `benchstat` rows in release notes when a baseline exists.
