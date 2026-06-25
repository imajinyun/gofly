# Migrating from Kitex

Kitex is a high-performance RPC framework. gofly does not try to replace specialized RPC performance work; it focuses on end-to-end service delivery with REST, RPC, governance, discovery, OpenAPI, and admin diagnostics.
The coexistence strategy is documented in
[RPC Boundary Governance](../reference/rpc-boundary.md).

## Mapping

| Kitex | gofly |
| --- | --- |
| IDL-first RPC | gofly RPC or `rpc/grpc` integration |
| client/server middleware | RPC middleware plus governance rules |
| service discovery | `core/discovery` providers |
| performance benchmarks | `bench/` and `make bench-smoke` |
| operations | `/admin/control-plane` and observability docs |

## Migration steps

1. Keep high-throughput Kitex services where raw RPC performance is the primary goal.
2. Use gofly for services that need REST+RPC composition, generated admin metadata, and release governance.
3. If migrating a method, wrap the domain handler behind gofly RPC or gRPC.
4. Add benchmark rows before and after migration using `make bench-stat`.
5. Keep Kitex as an optional benchmark extension if your repo already has generated Kitex fixtures.

## Validation gates

Run these before moving any latency-sensitive method:

```sh
make examples-copyable-check
make docs-check
BENCH_PATTERN=BenchmarkRPCUnary make bench-stat
BENCH_PATTERN=BenchmarkRPCStreamGovernance make bench-stat
make bench-evidence-check
```

Do not migrate a hot RPC path unless benchmark and production telemetry show that the governance and control-plane benefits justify the overhead.

## Rollback plan

Keep Kitex as the serving path for latency-critical methods until gofly RPC or
gRPC behavior matches contract, error, and latency expectations. Roll back by
routing the method back to Kitex and retaining gofly for REST ingress,
governance, or non-hot-path services. This coexistence model lets teams use
Kitex for the hot path while gofly owns control-plane metadata, release gates,
and generated REST/RPC glue.

## Demo path

Use `examples/rpc-idl-matrix` for IDL-oriented RPC migration evidence, the RPC
demo for lightweight runtime behavior, and the benchmark matrix for performance
evidence:

```sh
go test ./examples/rpc-idl-matrix/...
cd examples/rpcserver
go test ./...
go run .
```

From the repository root, compare RPC behavior with:

```sh
BENCH_PATTERN=BenchmarkRPCUnary make bench-stat
make bench-trend
```
The runnable migration proof matrix in `examples/migration-proof` records the
Kitex smoke example, validation gates, and rollback note used by `make
examples-smoke`.

## Recommendation

Use gofly and Kitex together when Kitex owns latency-critical internal RPC and gofly owns generated service scaffolding, REST ingress, governance, and control-plane transparency.
