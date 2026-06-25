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
4. significant `benchstat` rows in release notes when a baseline exists.
