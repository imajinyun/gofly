# Benchmark matrix

This is the authoritative public benchmark matrix for gofly release notes and CI smoke checks. It explains what is measured, which competitors are comparable, and which command reproduces the data.

| Area | Benchmark | Candidates | Reproduce | Trust signal |
| --- | --- | --- | --- | --- |
| REST routing | BenchmarkHTTPHello | net/http, gofly, Gin, Echo, Chi, Fiber, Hertz | BENCH_PATTERN=BenchmarkHTTPHello make bench-stat | Dispatch latency and allocations |
| REST path params | BenchmarkHTTPPathParams | net/http, gofly, Gin, Echo, Chi, Fiber, Hertz | BENCH_PATTERN=BenchmarkHTTPPathParams make bench-stat | Param extraction overhead |
| REST JSON binding | BenchmarkHTTPJSONBinding | net/http, gofly, Gin, Echo, Chi, Fiber, Hertz | BENCH_PATTERN=BenchmarkHTTPJSONBinding make bench-stat | Binding and response overhead |
| REST middleware | BenchmarkHTTPMiddlewareChain | net/http, gofly, Gin, Echo, Chi, Fiber, Hertz | BENCH_PATTERN=BenchmarkHTTPMiddlewareChain make bench-stat | Five-layer middleware overhead |
| OpenAPI overhead | BenchmarkHTTPOpenAPI | gofly disabled/enabled | BENCH_PATTERN=BenchmarkHTTPOpenAPI make bench-stat | Contract metadata cost |
| Governance overhead | BenchmarkHTTPGovernance | gofly disabled/enabled | BENCH_PATTERN=BenchmarkHTTPGovernance make bench-stat | Runtime policy decision cost |
| RPC unary | BenchmarkRPCUnary | gofly RPC, gRPC-Go | BENCH_PATTERN=BenchmarkRPCUnary make bench-stat | Service-to-service call cost |

## Release rule

Every stable release should attach raw benchmark output from `make bench-stat` plus `bench/summary.md` from `make bench-trend`. If a release changes REST, RPC, gateway, or governance hot paths, paste the significant benchstat rows into the release notes.
