# Benchmark matrix

Public benchmark work lives in `bench/`. Do not reintroduce a `benchmarks/`
tree. Reproduce with:

```sh
make bench-evidence-check
make bench-smoke
```

## What is blocking today

- REST hot-path **allocations** for hello, path params, JSON binding, and
  middleware chain
- OpenAPI metadata overhead when disabled or enabled, on both latency and
  allocations
- Governance enabled/disabled allocation budgets where the ratchet says
  blocking

## What stays report-only

- REST latency versus Gin, Echo, Chi, Fiber, Hertz, or `net/http`
- RPC unary (`BenchmarkRPCUnary/gofly_rpc`) versus gRPC-Go
- RPC stream governance versus Kitex
- Gateway proxy and cache hot-path candidate rows

Release notes must not claim RPC tier-1 throughput parity while
`docs/reference/rpc-tier1-evidence.json` stays `report-only`.

Tracked artifacts: `bench/baseline.txt`, `bench/budget-ratchet.json`, and
`make bench-regression-check`.
